package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrCallOver = errors.New("call is over")

const (
	CallRinging  = "ringing"
	CallActive   = "active"
	CallEnded    = "ended"
	CallMissed   = "missed"
	CallDeclined = "declined"
)

type Call struct {
	ID           string     `json:"id"`
	ChatID       string     `json:"chat_id"`
	ChatKind     string     `json:"chat_kind"`
	StartedBy    string     `json:"started_by"`
	Video        bool       `json:"video"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	AnsweredAt   *time.Time `json:"answered_at"`
	EndedAt      *time.Time `json:"ended_at"`
	Participants []string   `json:"participants"`
}

func (c Call) Live() bool { return c.Status == CallRinging || c.Status == CallActive }

type CallMeta struct {
	Action    string `json:"action"`
	CallID    string `json:"call_id"`
	Video     bool   `json:"video"`
	Status    string `json:"status"`
	DurationS int64  `json:"duration_s"`
}

const callColumns = `c.id::text, c.conversation_id::text, cv.kind, c.started_by::text, c.video, c.status,
	c.created_at, c.answered_at, c.ended_at,
	COALESCE(ARRAY(SELECT p.user_id::text FROM call_participants p WHERE p.call_id = c.id AND p.left_at IS NULL ORDER BY p.joined_at), '{}')`

func scanCall(row scanner) (Call, error) {
	var c Call
	err := row.Scan(&c.ID, &c.ChatID, &c.ChatKind, &c.StartedBy, &c.Video, &c.Status,
		&c.CreatedAt, &c.AnsweredAt, &c.EndedAt, &c.Participants)
	if errors.Is(err, pgx.ErrNoRows) {
		return Call{}, ErrNotFound
	}
	return c, err
}

func callByID(ctx context.Context, q querier, id string, lock bool) (Call, error) {
	sql := `SELECT ` + callColumns + ` FROM calls c JOIN conversations cv ON cv.id = c.conversation_id WHERE c.id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE OF c`
	}
	return scanCall(q.QueryRow(ctx, sql, id))
}

func (s *Store) Call(ctx context.Context, id string) (Call, error) {
	return callByID(ctx, s.pool, id, false)
}

func (s *Store) StartCall(ctx context.Context, chatID, userID string, video bool) (Call, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Call{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO calls (conversation_id, started_by, video)
		VALUES ($1::uuid, $2::uuid, $3)
		RETURNING id::text`,
		chatID, userID, video,
	).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "calls_one_live_per_chat" {
		_ = tx.Rollback(ctx)
		var existing string
		if err := s.pool.QueryRow(ctx, `
			SELECT id::text FROM calls WHERE conversation_id = $1::uuid AND status IN ('ringing', 'active')`,
			chatID,
		).Scan(&existing); err != nil {
			return Call{}, false, err
		}
		call, err := s.JoinCall(ctx, existing, userID)
		return call, false, err
	}
	if err != nil {
		return Call{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		id, userID,
	); err != nil {
		return Call{}, false, err
	}
	call, err := callByID(ctx, tx, id, false)
	if err != nil {
		return Call{}, false, err
	}
	return call, true, tx.Commit(ctx)
}

func (s *Store) JoinCall(ctx context.Context, callID, userID string) (Call, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Call{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	call, err := callByID(ctx, tx, callID, true)
	if err != nil {
		return Call{}, err
	}
	if !call.Live() {
		return Call{}, ErrCallOver
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id) VALUES ($1::uuid, $2::uuid)
		ON CONFLICT (call_id, user_id) DO UPDATE SET left_at = NULL, joined_at = now()`,
		callID, userID,
	); err != nil {
		return Call{}, err
	}
	if call.Status == CallRinging && userID != call.StartedBy {
		if _, err := tx.Exec(ctx, `
			UPDATE calls SET status = 'active', answered_at = now() WHERE id = $1::uuid`,
			callID,
		); err != nil {
			return Call{}, err
		}
	}
	call, err = callByID(ctx, tx, callID, false)
	if err != nil {
		return Call{}, err
	}
	return call, tx.Commit(ctx)
}

func finishCall(ctx context.Context, tx pgx.Tx, call Call, status string) (Call, Message, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE calls SET status = $2, ended_at = now() WHERE id = $1::uuid`,
		call.ID, status,
	); err != nil {
		return Call{}, Message{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE call_participants SET left_at = now() WHERE call_id = $1::uuid AND left_at IS NULL`,
		call.ID,
	); err != nil {
		return Call{}, Message{}, err
	}
	ended, err := callByID(ctx, tx, call.ID, false)
	if err != nil {
		return Call{}, Message{}, err
	}
	var duration int64
	if ended.AnsweredAt != nil && ended.EndedAt != nil {
		duration = int64(ended.EndedAt.Sub(*ended.AnsweredAt).Seconds())
	}
	msg, err := insertIn(ctx, tx, newMessage{
		ChatID: call.ChatID, SenderID: call.StartedBy, Kind: "system",
		Meta: CallMeta{Action: "call", CallID: call.ID, Video: call.Video, Status: status, DurationS: duration},
	})
	if err != nil {
		return Call{}, Message{}, err
	}
	return ended, msg, nil
}

type CallChange struct {
	Call    Call
	Message *Message
}

func (s *Store) LeaveCall(ctx context.Context, callID, userID string) (CallChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallChange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	call, err := callByID(ctx, tx, callID, true)
	if err != nil {
		return CallChange{}, err
	}
	if !call.Live() {
		return CallChange{Call: call}, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE call_participants SET left_at = now()
		WHERE call_id = $1::uuid AND user_id = $2::uuid AND left_at IS NULL`,
		callID, userID,
	); err != nil {
		return CallChange{}, err
	}
	call, err = callByID(ctx, tx, callID, false)
	if err != nil {
		return CallChange{}, err
	}

	end := len(call.Participants) == 0 || call.ChatKind == KindDirect
	if !end {
		return CallChange{Call: call}, tx.Commit(ctx)
	}
	status := CallEnded
	if call.AnsweredAt == nil {
		status = CallMissed
	}
	ended, msg, err := finishCall(ctx, tx, call, status)
	if err != nil {
		return CallChange{}, err
	}
	return CallChange{Call: ended, Message: &msg}, tx.Commit(ctx)
}

func (s *Store) DeclineCall(ctx context.Context, callID, userID string) (CallChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallChange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	call, err := callByID(ctx, tx, callID, true)
	if err != nil {
		return CallChange{}, err
	}
	if call.Status != CallRinging || call.ChatKind != KindDirect || userID == call.StartedBy {
		return CallChange{Call: call}, tx.Commit(ctx)
	}
	ended, msg, err := finishCall(ctx, tx, call, CallDeclined)
	if err != nil {
		return CallChange{}, err
	}
	return CallChange{Call: ended, Message: &msg}, tx.Commit(ctx)
}

func (s *Store) EndRoom(ctx context.Context, callID string) (CallChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CallChange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	call, err := callByID(ctx, tx, callID, true)
	if err != nil {
		return CallChange{}, err
	}
	if !call.Live() {
		return CallChange{Call: call}, tx.Commit(ctx)
	}
	status := CallEnded
	if call.AnsweredAt == nil {
		status = CallMissed
	}
	ended, msg, err := finishCall(ctx, tx, call, status)
	if err != nil {
		return CallChange{}, err
	}
	return CallChange{Call: ended, Message: &msg}, tx.Commit(ctx)
}

func (s *Store) callIDs(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) StaleRingingCalls(ctx context.Context, olderThan time.Duration) ([]string, error) {
	return s.callIDs(ctx, `
		SELECT id::text FROM calls
		WHERE status = 'ringing' AND created_at < now() - make_interval(secs => $1)`,
		olderThan.Seconds(),
	)
}

func (s *Store) ActiveCallsToCheck(ctx context.Context, joinedBefore time.Duration) ([]string, error) {
	return s.callIDs(ctx, `
		SELECT c.id::text FROM calls c
		WHERE c.status IN ('ringing', 'active')
		  AND NOT EXISTS (
			SELECT 1 FROM call_participants p
			WHERE p.call_id = c.id AND p.joined_at > now() - make_interval(secs => $1)
		  )`,
		joinedBefore.Seconds(),
	)
}

func (s *Store) SyncCallParticipants(ctx context.Context, callID string, present []string) (Call, bool, error) {
	if present == nil {
		present = []string{}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE call_participants SET left_at = now()
		WHERE call_id = $1::uuid AND left_at IS NULL AND NOT (user_id::text = ANY($2::text[]))`,
		callID, present,
	)
	if err != nil {
		return Call{}, false, err
	}
	call, err := s.Call(ctx, callID)
	return call, tag.RowsAffected() > 0, err
}

func (s *Store) LiveCalls(ctx context.Context, userID string) ([]Call, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+callColumns+`
		FROM calls c
		JOIN conversations cv ON cv.id = c.conversation_id
		JOIN conversation_members me ON me.conversation_id = c.conversation_id AND me.user_id = $1::uuid
		WHERE c.status IN ('ringing', 'active')
		ORDER BY c.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Call{}
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

type CallEntry struct {
	Call
	ChatTitle string      `json:"chat_title"`
	Peer      *PublicUser `json:"peer"`
}

func (s *Store) RecentCalls(ctx context.Context, userID string, limit int) ([]CallEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+callColumns+`, COALESCE(cv.title, ''),
		       pu.id::text, pu.username, pu.display_name, pu.last_seen_at
		FROM calls c
		JOIN conversations cv ON cv.id = c.conversation_id
		JOIN conversation_members me ON me.conversation_id = c.conversation_id AND me.user_id = $1::uuid
		LEFT JOIN LATERAL (
			SELECT u.id, u.username, u.display_name, u.last_seen_at
			FROM conversation_members o JOIN users u ON u.id = o.user_id
			WHERE cv.kind = 'direct' AND o.conversation_id = cv.id AND o.user_id <> $1::uuid
			LIMIT 1
		) pu ON true
		ORDER BY c.created_at DESC
		LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []CallEntry{}
	for rows.Next() {
		var (
			e                    CallEntry
			pid, pname, pdisplay *string
			pseen                *time.Time
		)
		if err := rows.Scan(&e.ID, &e.ChatID, &e.ChatKind, &e.StartedBy, &e.Video, &e.Status,
			&e.CreatedAt, &e.AnsweredAt, &e.EndedAt, &e.Participants, &e.ChatTitle,
			&pid, &pname, &pdisplay, &pseen); err != nil {
			return nil, err
		}
		if pid != nil {
			e.Peer = &PublicUser{ID: *pid, Username: *pname, DisplayName: *pdisplay, LastSeenAt: pseen}
			e.ChatTitle = *pdisplay
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
