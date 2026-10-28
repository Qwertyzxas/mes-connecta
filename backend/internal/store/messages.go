package store

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrClientIDConflict = errors.New("client id already used in another chat")
	ErrReplyNotFound    = errors.New("reply target not found in this chat")
	ErrForbidden        = errors.New("forbidden")
	ErrMessageDeleted   = errors.New("message deleted")
)

const replyPreviewLen = 200

type Sender struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type ReplyPreview struct {
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Body       string `json:"body"`
	Deleted    bool   `json:"deleted"`
}

type Message struct {
	ID        string        `json:"id"`
	ChatID    string        `json:"chat_id"`
	Seq       int64         `json:"seq"`
	Rev       int64         `json:"rev"`
	SenderID  string        `json:"sender_id"`
	Sender    Sender        `json:"sender"`
	ClientID  string        `json:"client_id"`
	Body      string        `json:"body"`
	ReplyTo   *ReplyPreview `json:"reply_to"`
	CreatedAt time.Time     `json:"created_at"`
	EditedAt  *time.Time    `json:"edited_at"`
	DeletedAt *time.Time    `json:"deleted_at"`
}

type MessageQuery struct {
	BeforeSeq int64
	AfterSeq  int64
	SinceRev  int64
	Limit     int
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const messageSelect = `
	SELECT m.id::text, m.conversation_id::text, m.seq, m.rev, m.sender_id::text,
	       su.username, su.display_name, m.client_id::text, m.body,
	       m.created_at, m.edited_at, m.deleted_at,
	       r.id::text, r.seq, r.sender_id::text, ru.display_name, r.body, r.deleted_at
	FROM messages m
	JOIN users su ON su.id = m.sender_id
	LEFT JOIN messages r ON r.id = m.reply_to_id
	LEFT JOIN users ru ON ru.id = r.sender_id`

func scanMessage(row scanner) (Message, error) {
	var (
		m           Message
		replyID     *string
		replySeq    *int64
		replySender *string
		replyName   *string
		replyBody   *string
		replyDel    *time.Time
	)
	err := row.Scan(
		&m.ID, &m.ChatID, &m.Seq, &m.Rev, &m.SenderID,
		&m.Sender.Username, &m.Sender.DisplayName, &m.ClientID, &m.Body,
		&m.CreatedAt, &m.EditedAt, &m.DeletedAt,
		&replyID, &replySeq, &replySender, &replyName, &replyBody, &replyDel,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	m.Sender.ID = m.SenderID
	if replyID != nil {
		m.ReplyTo = &ReplyPreview{
			ID:         *replyID,
			Seq:        *replySeq,
			SenderID:   *replySender,
			SenderName: *replyName,
			Body:       truncate(*replyBody, replyPreviewLen),
			Deleted:    replyDel != nil,
		}
	}
	return m, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func messageByID(ctx context.Context, q querier, id string) (Message, error) {
	return scanMessage(q.QueryRow(ctx, messageSelect+` WHERE m.id = $1::uuid`, id))
}

func (s *Store) MessageInChat(ctx context.Context, chatID, messageID string) (Message, error) {
	return scanMessage(s.pool.QueryRow(ctx, messageSelect+`
		WHERE m.id = $1::uuid AND m.conversation_id = $2::uuid`,
		messageID, chatID,
	))
}

func (s *Store) InsertMessage(ctx context.Context, chatID, senderID, clientID, body, replyToID string) (Message, bool, error) {
	existing, err := s.messageByClientID(ctx, senderID, clientID)
	switch {
	case err == nil:
		if existing.ChatID != chatID {
			return Message{}, false, ErrClientIDConflict
		}
		return existing, false, nil
	case !errors.Is(err, ErrNotFound):
		return Message{}, false, err
	}

	msg, err := s.insertMessageTx(ctx, chatID, senderID, clientID, body, replyToID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "messages_sender_client_key" {
		existing, err := s.messageByClientID(ctx, senderID, clientID)
		if err != nil {
			return Message{}, false, err
		}
		if existing.ChatID != chatID {
			return Message{}, false, ErrClientIDConflict
		}
		return existing, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	return msg, true, nil
}

func (s *Store) insertMessageTx(ctx context.Context, chatID, senderID, clientID, body, replyToID string) (Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var replyTo *string
	if replyToID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM messages WHERE id = $1::uuid AND conversation_id = $2::uuid)`,
			replyToID, chatID,
		).Scan(&ok); err != nil {
			return Message{}, err
		}
		if !ok {
			return Message{}, ErrReplyNotFound
		}
		replyTo = &replyToID
	}

	var seq, rev int64
	if err := tx.QueryRow(ctx, `
		UPDATE conversations
		SET last_seq = last_seq + 1, last_rev = last_rev + 1, last_message_at = now()
		WHERE id = $1::uuid
		RETURNING last_seq, last_rev`,
		chatID,
	).Scan(&seq, &rev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		return Message{}, err
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, seq, rev, sender_id, client_id, body, reply_to_id)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6, $7::uuid)
		RETURNING id::text`,
		chatID, seq, rev, senderID, clientID, body, replyTo,
	).Scan(&id); err != nil {
		return Message{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE conversation_members SET last_read_seq = $3
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid`,
		chatID, senderID, seq,
	); err != nil {
		return Message{}, err
	}

	msg, err := messageByID(ctx, tx, id)
	if err != nil {
		return Message{}, err
	}
	return msg, tx.Commit(ctx)
}

func (s *Store) messageByClientID(ctx context.Context, senderID, clientID string) (Message, error) {
	return scanMessage(s.pool.QueryRow(ctx, messageSelect+`
		WHERE m.sender_id = $1::uuid AND m.client_id = $2::uuid`,
		senderID, clientID,
	))
}

type messageChange func(ctx context.Context, tx pgx.Tx, rev int64) error

func (s *Store) changeMessage(ctx context.Context, chatID, messageID string, authorize func(senderID, body string, deleted bool) error, change messageChange) (Message, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		senderID  string
		body      string
		deletedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT sender_id::text, body, deleted_at FROM messages
		WHERE id = $1::uuid AND conversation_id = $2::uuid
		FOR UPDATE`,
		messageID, chatID,
	).Scan(&senderID, &body, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, false, ErrNotFound
	}
	if err != nil {
		return Message{}, false, err
	}
	if err := authorize(senderID, body, deletedAt != nil); err != nil {
		if errors.Is(err, errUnchanged) {
			msg, err := messageByID(ctx, tx, messageID)
			return msg, false, err
		}
		return Message{}, false, err
	}

	var rev int64
	if err := tx.QueryRow(ctx, `
		UPDATE conversations SET last_rev = last_rev + 1
		WHERE id = $1::uuid
		RETURNING last_rev`,
		chatID,
	).Scan(&rev); err != nil {
		return Message{}, false, err
	}
	if err := change(ctx, tx, rev); err != nil {
		return Message{}, false, err
	}

	msg, err := messageByID(ctx, tx, messageID)
	if err != nil {
		return Message{}, false, err
	}
	return msg, true, tx.Commit(ctx)
}

var errUnchanged = errors.New("unchanged")

func (s *Store) EditMessage(ctx context.Context, chatID, messageID, editorID, body string) (Message, bool, error) {
	return s.changeMessage(ctx, chatID, messageID,
		func(senderID, current string, deleted bool) error {
			switch {
			case deleted:
				return ErrMessageDeleted
			case senderID != editorID:
				return ErrForbidden
			case current == body:
				return errUnchanged
			}
			return nil
		},
		func(ctx context.Context, tx pgx.Tx, rev int64) error {
			_, err := tx.Exec(ctx, `
				UPDATE messages SET body = $2, edited_at = now(), rev = $3
				WHERE id = $1::uuid`,
				messageID, body, rev,
			)
			return err
		},
	)
}

func (s *Store) DeleteMessage(ctx context.Context, chatID, messageID, actorID string, moderator bool) (Message, bool, error) {
	return s.changeMessage(ctx, chatID, messageID,
		func(senderID, _ string, deleted bool) error {
			switch {
			case senderID != actorID && !moderator:
				return ErrForbidden
			case deleted:
				return errUnchanged
			}
			return nil
		},
		func(ctx context.Context, tx pgx.Tx, rev int64) error {
			_, err := tx.Exec(ctx, `
				UPDATE messages
				SET body = '', reply_to_id = NULL, deleted_at = now(), rev = $2
				WHERE id = $1::uuid`,
				messageID, rev,
			)
			return err
		},
	)
}

func (s *Store) ListMessages(ctx context.Context, chatID string, q MessageQuery) ([]Message, error) {
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case q.SinceRev > 0:
		rows, err = s.pool.Query(ctx, messageSelect+`
			WHERE m.conversation_id = $1::uuid AND m.rev > $2
			ORDER BY m.rev ASC
			LIMIT $3`,
			chatID, q.SinceRev, q.Limit,
		)
	case q.AfterSeq > 0:
		rows, err = s.pool.Query(ctx, messageSelect+`
			WHERE m.conversation_id = $1::uuid AND m.seq > $2
			ORDER BY m.seq ASC
			LIMIT $3`,
			chatID, q.AfterSeq, q.Limit,
		)
	default:
		before := q.BeforeSeq
		if before <= 0 {
			before = 1<<62 - 1
		}
		rows, err = s.pool.Query(ctx, messageSelect+`
			WHERE m.conversation_id = $1::uuid AND m.seq < $2
			ORDER BY m.seq DESC
			LIMIT $3`,
			chatID, before, q.Limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if q.SinceRev <= 0 && q.AfterSeq <= 0 {
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}
	}
	return messages, nil
}

func (s *Store) MarkRead(ctx context.Context, chatID, userID string, seq int64) (int64, bool, error) {
	var newSeq int64
	err := s.pool.QueryRow(ctx, `
		UPDATE conversation_members m
		SET last_read_seq = LEAST($3, c.last_seq)
		FROM conversations c
		WHERE m.conversation_id = $1::uuid
		  AND m.user_id = $2::uuid
		  AND c.id = m.conversation_id
		  AND m.last_read_seq < LEAST($3, c.last_seq)
		RETURNING m.last_read_seq`,
		chatID, userID, seq,
	).Scan(&newSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return newSeq, true, nil
}
