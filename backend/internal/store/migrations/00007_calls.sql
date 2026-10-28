-- +goose Up
CREATE TABLE calls (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id uuid        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    started_by      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    video           boolean     NOT NULL DEFAULT false,
    status          text        NOT NULL DEFAULT 'ringing'
        CHECK (status IN ('ringing', 'active', 'ended', 'missed', 'declined')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    answered_at     timestamptz,
    ended_at        timestamptz
);

CREATE UNIQUE INDEX calls_one_live_per_chat ON calls (conversation_id) WHERE status IN ('ringing', 'active');
CREATE INDEX calls_conversation_idx ON calls (conversation_id, created_at DESC);

CREATE TABLE call_participants (
    call_id   uuid        NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL DEFAULT now(),
    left_at   timestamptz,
    PRIMARY KEY (call_id, user_id)
);

-- +goose Down
DROP TABLE call_participants;
DROP TABLE calls;
