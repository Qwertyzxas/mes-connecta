package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrClientIDConflict = errors.New("client id already used in another chat")
)

type PublicUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	Online      bool       `json:"online"`
}

type Message struct {
	ID        string    `json:"id"`
	ChatID    string    `json:"chat_id"`
	Seq       int64     `json:"seq"`
	SenderID  string    `json:"sender_id"`
	ClientID  string    `json:"client_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Chat struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Peer            PublicUser `json:"peer"`
	LastSeq         int64      `json:"last_seq"`
	LastReadSeq     int64      `json:"last_read_seq"`
	PeerLastReadSeq int64      `json:"peer_last_read_seq"`
	Unread          int64      `json:"unread"`
	LastMessage     *Message   `json:"last_message"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const messageColumns = `id::text, conversation_id::text, seq, sender_id::text, client_id::text, body, created_at`

func scanMessage(row scanner) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.ChatID, &m.Seq, &m.SenderID, &m.ClientID, &m.Body, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

func (s *Store) SearchUsers(ctx context.Context, excludeID, prefix string, limit int) ([]PublicUser, error) {
	pattern := strings.ReplaceAll(prefix, `_`, `\_`) + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, username, display_name, last_seen_at
		FROM users
		WHERE lower(username) LIKE $1
		  AND id <> $2::uuid
		ORDER BY lower(username)
		LIMIT $3`,
		pattern, excludeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []PublicUser{}
	for rows.Next() {
		var u PublicUser
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.LastSeenAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) UserExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid)`, id).Scan(&exists)
	return exists, err
}

func directKey(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return ids[0] + ":" + ids[1]
}

func (s *Store) GetOrCreateDirect(ctx context.Context, userID, peerID string) (string, bool, error) {
	key := directKey(userID, peerID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var chatID string
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (kind, direct_key, created_by)
		VALUES ('direct', $1, $2::uuid)
		ON CONFLICT (direct_key) DO NOTHING
		RETURNING id::text`,
		key, userID,
	).Scan(&chatID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text FROM conversations WHERE direct_key = $1`, key).Scan(&chatID)
		if err != nil {
			return "", false, err
		}
		return chatID, false, tx.Commit(ctx)
	}
	if err != nil {
		return "", false, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id)
		VALUES ($1::uuid, $2::uuid), ($1::uuid, $3::uuid)`,
		chatID, userID, peerID,
	); err != nil {
		return "", false, err
	}
	return chatID, true, tx.Commit(ctx)
}

func (s *Store) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM conversation_members
			WHERE conversation_id = $1::uuid AND user_id = $2::uuid
		)`,
		chatID, userID,
	).Scan(&ok)
	return ok, err
}

func (s *Store) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id::text FROM conversation_members WHERE conversation_id = $1::uuid`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) PeerIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT other.user_id::text
		FROM conversation_members me
		JOIN conversation_members other
		  ON other.conversation_id = me.conversation_id AND other.user_id <> me.user_id
		WHERE me.user_id = $1::uuid`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

const chatQuery = `
	SELECT c.id::text, c.kind, c.last_seq, me.last_read_seq, peer.last_read_seq,
	       COALESCE(c.last_message_at, c.created_at),
	       u.id::text, u.username, u.display_name, u.last_seen_at,
	       m.id::text, m.seq, m.sender_id::text, m.client_id::text, m.body, m.created_at,
	       (SELECT count(*) FROM messages x
	         WHERE x.conversation_id = c.id
	           AND x.seq > me.last_read_seq
	           AND x.sender_id <> me.user_id)
	FROM conversation_members me
	JOIN conversations c ON c.id = me.conversation_id
	JOIN conversation_members peer ON peer.conversation_id = c.id AND peer.user_id <> me.user_id
	JOIN users u ON u.id = peer.user_id
	LEFT JOIN messages m ON m.conversation_id = c.id AND m.seq = c.last_seq
	WHERE me.user_id = $1::uuid`

func scanChat(row scanner) (Chat, error) {
	var (
		c         Chat
		msgID     *string
		msgSeq    *int64
		msgSender *string
		msgClient *string
		msgBody   *string
		msgAt     *time.Time
	)
	err := row.Scan(
		&c.ID, &c.Kind, &c.LastSeq, &c.LastReadSeq, &c.PeerLastReadSeq, &c.UpdatedAt,
		&c.Peer.ID, &c.Peer.Username, &c.Peer.DisplayName, &c.Peer.LastSeenAt,
		&msgID, &msgSeq, &msgSender, &msgClient, &msgBody, &msgAt,
		&c.Unread,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	if err != nil {
		return Chat{}, err
	}
	if msgID != nil {
		c.LastMessage = &Message{
			ID: *msgID, ChatID: c.ID, Seq: *msgSeq, SenderID: *msgSender,
			ClientID: *msgClient, Body: *msgBody, CreatedAt: *msgAt,
		}
	}
	return c, nil
}

func (s *Store) ListChats(ctx context.Context, userID string) ([]Chat, error) {
	rows, err := s.pool.Query(ctx, chatQuery+`
		  AND (c.last_seq > 0 OR c.created_by = me.user_id)
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := []Chat{}
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

func (s *Store) GetChat(ctx context.Context, userID, chatID string) (Chat, error) {
	return scanChat(s.pool.QueryRow(ctx, chatQuery+` AND c.id = $2::uuid`, userID, chatID))
}

func (s *Store) InsertMessage(ctx context.Context, chatID, senderID, clientID, body string) (Message, bool, error) {
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

	msg, err := s.insertMessageTx(ctx, chatID, senderID, clientID, body)
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

func (s *Store) insertMessageTx(ctx context.Context, chatID, senderID, clientID, body string) (Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var seq int64
	if err := tx.QueryRow(ctx, `
		UPDATE conversations
		SET last_seq = last_seq + 1, last_message_at = now()
		WHERE id = $1::uuid
		RETURNING last_seq`,
		chatID,
	).Scan(&seq); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		return Message{}, err
	}

	msg, err := scanMessage(tx.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, seq, sender_id, client_id, body)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5)
		RETURNING `+messageColumns,
		chatID, seq, senderID, clientID, body,
	))
	if err != nil {
		return Message{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE conversation_members SET last_read_seq = $3
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid`,
		chatID, senderID, seq,
	); err != nil {
		return Message{}, err
	}
	return msg, tx.Commit(ctx)
}

func (s *Store) messageByClientID(ctx context.Context, senderID, clientID string) (Message, error) {
	return scanMessage(s.pool.QueryRow(ctx, `
		SELECT `+messageColumns+`
		FROM messages
		WHERE sender_id = $1::uuid AND client_id = $2::uuid`,
		senderID, clientID,
	))
}

func (s *Store) ListMessages(ctx context.Context, chatID string, beforeSeq, afterSeq int64, limit int) ([]Message, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if afterSeq > 0 {
		rows, err = s.pool.Query(ctx, `
			SELECT `+messageColumns+`
			FROM messages
			WHERE conversation_id = $1::uuid AND seq > $2
			ORDER BY seq ASC
			LIMIT $3`,
			chatID, afterSeq, limit,
		)
	} else {
		if beforeSeq <= 0 {
			beforeSeq = 1<<62 - 1
		}
		rows, err = s.pool.Query(ctx, `
			SELECT `+messageColumns+`
			FROM messages
			WHERE conversation_id = $1::uuid AND seq < $2
			ORDER BY seq DESC
			LIMIT $3`,
			chatID, beforeSeq, limit,
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
	if afterSeq <= 0 {
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

func (s *Store) SetLastSeen(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1::uuid`, userID, at)
	return err
}
