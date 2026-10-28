package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type UserWithPassword struct {
	User
	PasswordHash string
}

const userColumns = `id::text, email, username, display_name, created_at`

func (s *Store) CreateUser(ctx context.Context, email, username, displayName, passwordHash string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, username, display_name, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userColumns,
		email, username, displayName, passwordHash,
	).Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "users_email_key":
				return User{}, ErrEmailTaken
			case "users_username_key":
				return User{}, ErrUsernameTaken
			}
		}
		return User{}, err
	}
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (UserWithPassword, error) {
	var u UserWithPassword
	err := s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`, password_hash
		FROM users
		WHERE lower(email) = lower($1)`,
		email,
	).Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserWithPassword{}, ErrNotFound
	}
	return u, err
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM users
		WHERE id = $1::uuid`,
		id,
	).Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
