-- +goose Up
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL,
    username      text        NOT NULL,
    display_name  text        NOT NULL,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_key ON users (lower(email));
CREATE UNIQUE INDEX users_username_key ON users (lower(username));

CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL,
    prev_token_hash bytea,
    user_agent      text        NOT NULL DEFAULT '',
    ip              text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz
);

CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_prev_token_hash_idx ON sessions (prev_token_hash) WHERE prev_token_hash IS NOT NULL;
CREATE INDEX sessions_user_active_idx ON sessions (user_id, last_used_at DESC) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
