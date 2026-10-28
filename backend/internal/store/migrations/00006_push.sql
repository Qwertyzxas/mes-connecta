-- +goose Up
CREATE TABLE server_keys (
    name       text PRIMARY KEY,
    value      text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE push_subscriptions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id uuid        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    endpoint   text        NOT NULL UNIQUE,
    p256dh     text        NOT NULL,
    auth       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX push_subscriptions_user_idx ON push_subscriptions (user_id);

ALTER TABLE conversation_members ADD COLUMN muted boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE conversation_members DROP COLUMN muted;
DROP TABLE push_subscriptions;
DROP TABLE server_keys;
