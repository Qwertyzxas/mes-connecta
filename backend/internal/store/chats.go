package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type PublicUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	Online      bool       `json:"online"`
}

type Chat struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Peer            PublicUser `json:"peer"`
	LastSeq         int64      `json:"last_seq"`
	LastRev         int64      `json:"last_rev"`
	LastReadSeq     int64      `json:"last_read_seq"`
	PeerLastReadSeq int64      `json:"peer_last_read_seq"`
	Unread          int64      `json:"unread"`
	LastMessage     *Message   `json:"last_message"`
	UpdatedAt       time.Time  `json:"updated_at"`
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
	SELECT c.id::text, c.kind, c.last_seq, c.last_rev, me.last_read_seq, peer.last_read_seq,
	       COALESCE(c.last_message_at, c.created_at),
	       u.id::text, u.username, u.display_name, u.last_seen_at,
	       m.id::text, m.seq, m.rev, m.sender_id::text, mu.username, mu.display_name,
	       m.client_id::text, m.body, m.created_at, m.edited_at, m.deleted_at,
	       (SELECT count(*) FROM messages x
	         WHERE x.conversation_id = c.id
	           AND x.seq > me.last_read_seq
	           AND x.sender_id <> me.user_id
	           AND x.deleted_at IS NULL)
	FROM conversation_members me
	JOIN conversations c ON c.id = me.conversation_id
	JOIN conversation_members peer ON peer.conversation_id = c.id AND peer.user_id <> me.user_id
	JOIN users u ON u.id = peer.user_id
	LEFT JOIN messages m ON m.conversation_id = c.id AND m.seq = c.last_seq
	LEFT JOIN users mu ON mu.id = m.sender_id
	WHERE me.user_id = $1::uuid`

func scanChat(row scanner) (Chat, error) {
	var (
		c          Chat
		msgID      *string
		msgSeq     *int64
		msgRev     *int64
		msgSender  *string
		senderName *string
		senderDisp *string
		msgClient  *string
		msgBody    *string
		msgAt      *time.Time
		msgEdited  *time.Time
		msgDeleted *time.Time
	)
	err := row.Scan(
		&c.ID, &c.Kind, &c.LastSeq, &c.LastRev, &c.LastReadSeq, &c.PeerLastReadSeq, &c.UpdatedAt,
		&c.Peer.ID, &c.Peer.Username, &c.Peer.DisplayName, &c.Peer.LastSeenAt,
		&msgID, &msgSeq, &msgRev, &msgSender, &senderName, &senderDisp,
		&msgClient, &msgBody, &msgAt, &msgEdited, &msgDeleted,
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
			ID: *msgID, ChatID: c.ID, Seq: *msgSeq, Rev: *msgRev, SenderID: *msgSender,
			Sender:   Sender{ID: *msgSender, Username: *senderName, DisplayName: *senderDisp},
			ClientID: *msgClient, Body: *msgBody, CreatedAt: *msgAt,
			EditedAt: msgEdited, DeletedAt: msgDeleted,
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

func (s *Store) SetLastSeen(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1::uuid`, userID, at)
	return err
}
