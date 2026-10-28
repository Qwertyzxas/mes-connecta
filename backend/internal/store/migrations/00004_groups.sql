-- +goose Up
ALTER TABLE conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('direct', 'group', 'channel'));
ALTER TABLE conversations
    ADD COLUMN title     text,
    ADD COLUMN about     text    NOT NULL DEFAULT '',
    ADD COLUMN handle    text,
    ADD COLUMN is_public boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX conversations_handle_key ON conversations (lower(handle)) WHERE handle IS NOT NULL;
CREATE INDEX conversations_handle_prefix_idx ON conversations (lower(handle) text_pattern_ops) WHERE is_public;

ALTER TABLE conversation_members
    ADD COLUMN role text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member'));

ALTER TABLE messages
    ADD COLUMN kind text NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'system')),
    ADD COLUMN meta jsonb;

-- +goose Down
ALTER TABLE messages DROP COLUMN meta, DROP COLUMN kind;
ALTER TABLE conversation_members DROP COLUMN role;
DROP INDEX conversations_handle_prefix_idx;
DROP INDEX conversations_handle_key;
ALTER TABLE conversations DROP COLUMN is_public, DROP COLUMN handle, DROP COLUMN about, DROP COLUMN title;
DELETE FROM conversations WHERE kind <> 'direct';
ALTER TABLE conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('direct'));
