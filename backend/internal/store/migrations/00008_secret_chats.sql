-- +goose Up
ALTER TABLE conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('direct', 'group', 'channel', 'secret'));
ALTER TABLE conversations
    ADD COLUMN secret_state text CHECK (secret_state IN ('pending', 'active')),
    ADD CONSTRAINT conversations_secret_kind_check CHECK ((kind = 'secret') = (secret_state IS NOT NULL));

ALTER TABLE conversation_members
    ADD COLUMN session_id uuid REFERENCES sessions (id) ON DELETE CASCADE,
    ADD COLUMN public_key text;

CREATE INDEX conversation_members_session_idx ON conversation_members (session_id) WHERE session_id IS NOT NULL;

-- +goose Down
DROP INDEX conversation_members_session_idx;
ALTER TABLE conversation_members DROP COLUMN public_key, DROP COLUMN session_id;
DELETE FROM conversations WHERE kind = 'secret';
ALTER TABLE conversations DROP CONSTRAINT conversations_secret_kind_check, DROP COLUMN secret_state;
ALTER TABLE conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('direct', 'group', 'channel'));
