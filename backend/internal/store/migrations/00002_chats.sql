-- +goose Up
ALTER TABLE users ADD COLUMN last_seen_at timestamptz;
CREATE INDEX users_username_prefix_idx ON users (lower(username) text_pattern_ops);

CREATE TABLE conversations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind            text        NOT NULL CHECK (kind IN ('direct')),
    direct_key      text UNIQUE,
    created_by      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    last_seq        bigint      NOT NULL DEFAULT 0,
    last_message_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE conversation_members (
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    last_read_seq   bigint      NOT NULL DEFAULT 0,
    joined_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);

CREATE INDEX conversation_members_user_idx ON conversation_members (user_id);

CREATE TABLE messages (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    seq             bigint      NOT NULL,
    sender_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id       uuid        NOT NULL,
    body            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT messages_conversation_seq_key UNIQUE (conversation_id, seq),
    CONSTRAINT messages_sender_client_key UNIQUE (sender_id, client_id)
);

-- +goose Down
DROP TABLE messages;
DROP TABLE conversation_members;
DROP TABLE conversations;
DROP INDEX users_username_prefix_idx;
ALTER TABLE users DROP COLUMN last_seen_at;
