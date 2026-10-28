-- +goose Up
CREATE TABLE attachments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    uploader_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    message_id      uuid REFERENCES messages (id) ON DELETE SET NULL,
    kind            text        NOT NULL CHECK (kind IN ('image', 'file', 'voice')),
    name            text        NOT NULL,
    content_type    text        NOT NULL,
    size            bigint      NOT NULL,
    width           int,
    height          int,
    duration_ms     int,
    object_key      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE INDEX attachments_message_idx ON attachments (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX attachments_pending_idx ON attachments (created_at) WHERE message_id IS NULL AND deleted_at IS NULL;

-- +goose Down
DROP TABLE attachments;
