package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type PushSubscription struct {
	ID       string
	UserID   string
	Endpoint string
	P256dh   string
	Auth     string
}

func (s *Store) ServerKey(ctx context.Context, name string, create func() (string, error)) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM server_keys WHERE name = $1`, name).Scan(&value)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	fresh, err := create()
	if err != nil {
		return "", err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO server_keys (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING value`,
		name, fresh,
	).Scan(&value)
	return value, err
}

func (s *Store) SavePushSubscription(ctx context.Context, userID, sessionID, endpoint, p256dh, auth string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO push_subscriptions (user_id, session_id, endpoint, p256dh, auth)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE
		SET user_id = EXCLUDED.user_id, session_id = EXCLUDED.session_id,
		    p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, created_at = now()`,
		userID, sessionID, endpoint, p256dh, auth,
	)
	return err
}

func (s *Store) DeletePushSubscription(ctx context.Context, userID, endpoint string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1::uuid AND endpoint = $2`, userID, endpoint)
	return err
}

func (s *Store) DeletePushSubscriptionByID(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1::uuid`, id)
	return err
}

func (s *Store) DeleteSessionPushSubscriptions(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE session_id = $1::uuid`, sessionID)
	return err
}

func (s *Store) PushTargets(ctx context.Context, chatID, senderID string) ([]PushSubscription, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ps.id::text, ps.user_id::text, ps.endpoint, ps.p256dh, ps.auth
		FROM conversation_members cm
		JOIN push_subscriptions ps ON ps.user_id = cm.user_id
		JOIN sessions se ON se.id = ps.session_id AND se.revoked_at IS NULL AND se.expires_at > now()
		WHERE cm.conversation_id = $1::uuid
		  AND cm.user_id <> $2::uuid
		  AND NOT cm.muted
		  AND (cm.session_id IS NULL OR cm.session_id = ps.session_id)`,
		chatID, senderID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subs []PushSubscription
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, p)
	}
	return subs, rows.Err()
}

func (s *Store) SetMuted(ctx context.Context, chatID, userID string, muted bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE conversation_members SET muted = $3
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid`,
		chatID, userID, muted,
	)
	return err
}
