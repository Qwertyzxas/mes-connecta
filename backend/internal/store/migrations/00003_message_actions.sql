-- +goose Up
ALTER TABLE conversations ADD COLUMN last_rev bigint NOT NULL DEFAULT 0;

ALTER TABLE messages
    ADD COLUMN rev         bigint,
    ADD COLUMN reply_to_id uuid REFERENCES messages (id) ON DELETE SET NULL,
    ADD COLUMN edited_at   timestamptz,
    ADD COLUMN deleted_at  timestamptz;

UPDATE messages SET rev = seq;
UPDATE conversations SET last_rev = last_seq;

ALTER TABLE messages ALTER COLUMN rev SET NOT NULL;
CREATE INDEX messages_conversation_rev_idx ON messages (conversation_id, rev);

-- +goose Down
DROP INDEX messages_conversation_rev_idx;
ALTER TABLE messages
    DROP COLUMN deleted_at,
    DROP COLUMN edited_at,
    DROP COLUMN reply_to_id,
    DROP COLUMN rev;
ALTER TABLE conversations DROP COLUMN last_rev;
