package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"-"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

const sessionColumns = `id::text, user_id::text, user_agent, ip, created_at, last_used_at, expires_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner, extra ...any) (Session, error) {
	var s Session
	dest := append([]any{&s.ID, &s.UserID, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, userAgent, ip string, expiresAt time.Time) (Session, error) {
	return scanSession(s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5)
		RETURNING `+sessionColumns,
		userID, tokenHash, userAgent, ip, expiresAt,
	))
}

func (s *Store) RotateSession(ctx context.Context, oldHash, newHash []byte, expiresAt time.Time) (Session, error) {
	return scanSession(s.pool.QueryRow(ctx, `
		UPDATE sessions
		SET prev_token_hash = token_hash,
		    token_hash      = $2,
		    last_used_at    = now(),
		    expires_at      = $3
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		RETURNING `+sessionColumns,
		oldHash, newHash, expiresAt,
	))
}

func (s *Store) ReissueFromPrev(ctx context.Context, prevHash, newHash []byte, expiresAt time.Time, grace time.Duration) (Session, error) {
	return scanSession(s.pool.QueryRow(ctx, `
		UPDATE sessions
		SET token_hash = $2,
		    expires_at = $3
		WHERE prev_token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		  AND last_used_at > now() - make_interval(secs => $4)
		RETURNING `+sessionColumns,
		prevHash, newHash, expiresAt, grace.Seconds(),
	))
}

func (s *Store) SessionByPrevHash(ctx context.Context, hash []byte, grace time.Duration) (Session, bool, error) {
	var recent bool
	sess, err := scanSession(s.pool.QueryRow(ctx, `
		SELECT `+sessionColumns+`, last_used_at > now() - make_interval(secs => $2)
		FROM sessions
		WHERE prev_token_hash = $1
		  AND revoked_at IS NULL`,
		hash, grace.Seconds(),
	), &recent)
	return sess, recent, err
}

func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1::uuid AND revoked_at IS NULL`,
		id,
	)
	return err
}

func (s *Store) RevokeSessionByHash(ctx context.Context, hash []byte) (Session, error) {
	return scanSession(s.pool.QueryRow(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING `+sessionColumns,
		hash,
	))
}

func (s *Store) RevokeUserSession(ctx context.Context, userID, sessionID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL`,
		sessionID, userID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+sessionColumns+`
		FROM sessions
		WHERE user_id = $1::uuid
		  AND revoked_at IS NULL
		  AND expires_at > now()
		ORDER BY last_used_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := []Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}
