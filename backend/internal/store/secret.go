package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrSecretState = errors.New("secret chat is not awaiting this action")

type Target struct {
	UserID    string
	SessionID string
}

func (s *Store) CreateSecret(ctx context.Context, userID, sessionID, peerID, publicKey string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var chatID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO conversations (kind, secret_state, created_by)
		VALUES ('secret', 'pending', $1::uuid)
		RETURNING id::text`,
		userID,
	).Scan(&chatID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id, session_id, public_key)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4), ($1::uuid, $5::uuid, NULL, NULL)`,
		chatID, userID, sessionID, publicKey, peerID,
	); err != nil {
		return "", err
	}
	return chatID, tx.Commit(ctx)
}

func (s *Store) AcceptSecret(ctx context.Context, chatID, userID, sessionID, publicKey string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var state string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(secret_state, '') FROM conversations WHERE id = $1::uuid FOR UPDATE`,
		chatID,
	).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != SecretPending {
		return ErrSecretState
	}
	tag, err := tx.Exec(ctx, `
		UPDATE conversation_members SET session_id = $3::uuid, public_key = $4
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid AND session_id IS NULL`,
		chatID, userID, sessionID, publicKey,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSecretState
	}
	if _, err := tx.Exec(ctx, `
		UPDATE conversations SET secret_state = 'active' WHERE id = $1::uuid`,
		chatID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ChatTargets(ctx context.Context, chatID string) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id::text, COALESCE(session_id::text, '')
		FROM conversation_members WHERE conversation_id = $1::uuid`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.UserID, &t.SessionID); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

func (s *Store) DeleteSecret(ctx context.Context, chatID string) ([]Target, []string, error) {
	targets, err := s.ChatTargets(ctx, chatID)
	if err != nil {
		return nil, nil, err
	}
	keys, err := s.chatObjectKeys(ctx, chatID)
	if err != nil {
		return nil, nil, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM conversations WHERE id = $1::uuid AND kind = 'secret'`, chatID)
	if err != nil {
		return nil, nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, ErrNotFound
	}
	return targets, keys, nil
}

func (s *Store) chatObjectKeys(ctx context.Context, chatID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT object_key FROM attachments WHERE conversation_id = $1::uuid`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) SecretChatsForSession(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text
		FROM conversation_members cm
		JOIN conversations c ON c.id = cm.conversation_id
		WHERE cm.session_id = $1::uuid AND c.kind = 'secret'`,
		sessionID,
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
