package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrHandleTaken    = errors.New("handle already taken")
	ErrNotMember      = errors.New("user is not a member")
	ErrAlreadyMember  = errors.New("already a member")
	ErrTooManyMembers = errors.New("too many members")
)

const MaxGroupMembers = 200

const (
	KindDirect  = "direct"
	KindGroup   = "group"
	KindChannel = "channel"

	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type PublicUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	Online      bool       `json:"online"`
}

type Chat struct {
	ID              string      `json:"id"`
	Kind            string      `json:"kind"`
	Title           string      `json:"title"`
	About           string      `json:"about"`
	Handle          *string     `json:"handle"`
	IsPublic        bool        `json:"is_public"`
	Peer            *PublicUser `json:"peer"`
	Role            string      `json:"role"`
	Muted           bool        `json:"muted"`
	MemberCount     int64       `json:"member_count"`
	OnlineCount     int64       `json:"online_count"`
	LastSeq         int64       `json:"last_seq"`
	LastRev         int64       `json:"last_rev"`
	LastReadSeq     int64       `json:"last_read_seq"`
	PeerLastReadSeq int64       `json:"peer_last_read_seq"`
	Unread          int64       `json:"unread"`
	LastMessage     *Message    `json:"last_message"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type Member struct {
	PublicUser
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type Membership struct {
	Kind string
	Role string
}

func (m Membership) CanPost() bool {
	return m.Kind != KindChannel || m.Role == RoleOwner || m.Role == RoleAdmin
}

func (m Membership) CanManage() bool {
	return m.Kind != KindDirect && (m.Role == RoleOwner || m.Role == RoleAdmin)
}

type ChannelResult struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	About       string  `json:"about"`
	Handle      *string `json:"handle"`
	MemberCount int64   `json:"member_count"`
	Joined      bool    `json:"joined"`
}

func escapeLike(prefix string) string {
	return strings.ReplaceAll(prefix, `_`, `\_`) + "%"
}

func (s *Store) SearchUsers(ctx context.Context, excludeID, prefix string, limit int) ([]PublicUser, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, username, display_name, last_seen_at
		FROM users
		WHERE lower(username) LIKE $1
		  AND id <> $2::uuid
		ORDER BY lower(username)
		LIMIT $3`,
		escapeLike(prefix), excludeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []PublicUser{}
	for rows.Next() {
		var u PublicUser
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.LastSeenAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) SearchChannels(ctx context.Context, userID, prefix string, limit int) ([]ChannelResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text, COALESCE(c.title, ''), c.about, c.handle,
		       (SELECT count(*) FROM conversation_members o WHERE o.conversation_id = c.id),
		       EXISTS (SELECT 1 FROM conversation_members me WHERE me.conversation_id = c.id AND me.user_id = $2::uuid)
		FROM conversations c
		WHERE c.kind = 'channel' AND c.is_public AND lower(c.handle) LIKE $1
		ORDER BY lower(c.handle)
		LIMIT $3`,
		escapeLike(prefix), userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channels := []ChannelResult{}
	for rows.Next() {
		var c ChannelResult
		if err := rows.Scan(&c.ID, &c.Title, &c.About, &c.Handle, &c.MemberCount, &c.Joined); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func (s *Store) UserExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid)`, id).Scan(&exists)
	return exists, err
}

func (s *Store) ExistingUsers(ctx context.Context, ids []string) ([]PublicUser, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, username, display_name, last_seen_at
		FROM users WHERE id = ANY($1::uuid[])`,
		ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []PublicUser
	for rows.Next() {
		var u PublicUser
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.LastSeenAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func directKey(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return ids[0] + ":" + ids[1]
}

func (s *Store) GetOrCreateDirect(ctx context.Context, userID, peerID string) (string, bool, error) {
	key := directKey(userID, peerID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var chatID string
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (kind, direct_key, created_by)
		VALUES ('direct', $1, $2::uuid)
		ON CONFLICT (direct_key) DO NOTHING
		RETURNING id::text`,
		key, userID,
	).Scan(&chatID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text FROM conversations WHERE direct_key = $1`, key).Scan(&chatID)
		if err != nil {
			return "", false, err
		}
		return chatID, false, tx.Commit(ctx)
	}
	if err != nil {
		return "", false, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id)
		VALUES ($1::uuid, $2::uuid), ($1::uuid, $3::uuid)`,
		chatID, userID, peerID,
	); err != nil {
		return "", false, err
	}
	return chatID, true, tx.Commit(ctx)
}

type NewGroup struct {
	Kind      string
	OwnerID   string
	Title     string
	About     string
	Handle    *string
	IsPublic  bool
	MemberIDs []string
}

type SystemMeta struct {
	Action     string `json:"action"`
	Title      string `json:"title,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	TargetName string `json:"target_name,omitempty"`
	Role       string `json:"role,omitempty"`
}

func (s *Store) CreateGroup(ctx context.Context, g NewGroup) (string, Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var chatID string
	err = tx.QueryRow(ctx, `
		INSERT INTO conversations (kind, title, about, handle, is_public, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::uuid)
		RETURNING id::text`,
		g.Kind, g.Title, g.About, g.Handle, g.IsPublic, g.OwnerID,
	).Scan(&chatID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "conversations_handle_key" {
		return "", Message{}, ErrHandleTaken
	}
	if err != nil {
		return "", Message{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id, role)
		VALUES ($1::uuid, $2::uuid, 'owner')`,
		chatID, g.OwnerID,
	); err != nil {
		return "", Message{}, err
	}
	if len(g.MemberIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role)
			SELECT $1::uuid, u.id, 'member' FROM users u
			WHERE u.id = ANY($2::uuid[]) AND u.id <> $3::uuid
			ON CONFLICT DO NOTHING`,
			chatID, g.MemberIDs, g.OwnerID,
		); err != nil {
			return "", Message{}, err
		}
	}

	msg, err := insertIn(ctx, tx, newMessage{
		ChatID: chatID, SenderID: g.OwnerID, Kind: "system",
		Meta: SystemMeta{Action: "created", Title: g.Title},
	})
	if err != nil {
		return "", Message{}, err
	}
	return chatID, msg, tx.Commit(ctx)
}

func (s *Store) Membership(ctx context.Context, chatID, userID string) (Membership, bool, error) {
	var m Membership
	err := s.pool.QueryRow(ctx, `
		SELECT c.kind, cm.role
		FROM conversation_members cm
		JOIN conversations c ON c.id = cm.conversation_id
		WHERE cm.conversation_id = $1::uuid AND cm.user_id = $2::uuid`,
		chatID, userID,
	).Scan(&m.Kind, &m.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, false, nil
	}
	return m, err == nil, err
}

func (s *Store) IsMember(ctx context.Context, chatID, userID string) (bool, error) {
	_, ok, err := s.Membership(ctx, chatID, userID)
	return ok, err
}

func (s *Store) MemberIDs(ctx context.Context, chatID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id::text FROM conversation_members WHERE conversation_id = $1::uuid`,
		chatID,
	)
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

func (s *Store) Members(ctx context.Context, chatID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.username, u.display_name, u.last_seen_at, cm.role, cm.joined_at
		FROM conversation_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.conversation_id = $1::uuid
		ORDER BY CASE cm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, cm.joined_at`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName, &m.LastSeenAt, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) PeerIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT other.user_id::text
		FROM conversation_members me
		JOIN conversations c ON c.id = me.conversation_id AND c.kind <> 'channel'
		JOIN conversation_members other
		  ON other.conversation_id = me.conversation_id AND other.user_id <> me.user_id
		WHERE me.user_id = $1::uuid`,
		userID,
	)
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

func memberCount(ctx context.Context, tx pgx.Tx, chatID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_members WHERE conversation_id = $1::uuid`, chatID).Scan(&n)
	return n, err
}

func (s *Store) AddMembers(ctx context.Context, chatID, actorID string, users []PublicUser) ([]Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT 1 FROM conversations WHERE id = $1::uuid FOR UPDATE`, chatID); err != nil {
		return nil, err
	}
	count, err := memberCount(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}

	var messages []Message
	for _, u := range users {
		tag, err := tx.Exec(ctx, `
			INSERT INTO conversation_members (conversation_id, user_id, role)
			VALUES ($1::uuid, $2::uuid, 'member')
			ON CONFLICT DO NOTHING`,
			chatID, u.ID,
		)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		count++
		if count > MaxGroupMembers {
			return nil, ErrTooManyMembers
		}
		msg, err := insertIn(ctx, tx, newMessage{
			ChatID: chatID, SenderID: actorID, Kind: "system",
			Meta: SystemMeta{Action: "added", TargetID: u.ID, TargetName: u.DisplayName},
		})
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, tx.Commit(ctx)
}

func (s *Store) RemoveMember(ctx context.Context, chatID, actorID, targetID string) (Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name string
	err = tx.QueryRow(ctx, `
		DELETE FROM conversation_members cm
		USING users u
		WHERE cm.conversation_id = $1::uuid AND cm.user_id = $2::uuid AND u.id = cm.user_id
		RETURNING u.display_name`,
		chatID, targetID,
	).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotMember
	}
	if err != nil {
		return Message{}, err
	}

	msg, err := insertIn(ctx, tx, newMessage{
		ChatID: chatID, SenderID: actorID, Kind: "system",
		Meta: SystemMeta{Action: "removed", TargetID: targetID, TargetName: name},
	})
	if err != nil {
		return Message{}, err
	}
	return msg, tx.Commit(ctx)
}

func (s *Store) Leave(ctx context.Context, chatID, userID string, announce bool) (*Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var role string
	err = tx.QueryRow(ctx, `
		DELETE FROM conversation_members
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid
		RETURNING role`,
		chatID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}

	if role == RoleOwner {
		if _, err := tx.Exec(ctx, `
			UPDATE conversation_members SET role = 'owner'
			WHERE conversation_id = $1::uuid AND user_id = (
				SELECT user_id FROM conversation_members
				WHERE conversation_id = $1::uuid
				ORDER BY (role = 'admin') DESC, joined_at ASC
				LIMIT 1
			)`,
			chatID,
		); err != nil {
			return nil, err
		}
	}

	var msg *Message
	if announce {
		m, err := insertIn(ctx, tx, newMessage{
			ChatID: chatID, SenderID: userID, Kind: "system",
			Meta: SystemMeta{Action: "left"},
		})
		if err != nil {
			return nil, err
		}
		msg = &m
	}
	return msg, tx.Commit(ctx)
}

func (s *Store) Join(ctx context.Context, chatID, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO conversation_members (conversation_id, user_id, role)
		SELECT c.id, $2::uuid, 'member' FROM conversations c
		WHERE c.id = $1::uuid AND c.kind = 'channel' AND c.is_public
		ON CONFLICT DO NOTHING`,
		chatID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var ok bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM conversations WHERE id = $1::uuid AND kind = 'channel' AND is_public)`,
			chatID,
		).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		return ErrAlreadyMember
	}
	return nil
}

func (s *Store) UpdateInfo(ctx context.Context, chatID, actorID string, title, about *string) (*Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldTitle string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(title, '') FROM conversations WHERE id = $1::uuid FOR UPDATE`, chatID).Scan(&oldTitle); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE conversations
		SET title = COALESCE($2, title), about = COALESCE($3, about)
		WHERE id = $1::uuid`,
		chatID, title, about,
	); err != nil {
		return nil, err
	}

	var msg *Message
	if title != nil && *title != oldTitle {
		m, err := insertIn(ctx, tx, newMessage{
			ChatID: chatID, SenderID: actorID, Kind: "system",
			Meta: SystemMeta{Action: "renamed", Title: *title},
		})
		if err != nil {
			return nil, err
		}
		msg = &m
	}
	return msg, tx.Commit(ctx)
}

func (s *Store) SetRole(ctx context.Context, chatID, targetID, role string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE conversation_members SET role = $3
		WHERE conversation_id = $1::uuid AND user_id = $2::uuid AND role <> 'owner'`,
		chatID, targetID, role,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotMember
	}
	return nil
}

const chatQuery = `
	SELECT c.id::text, c.kind, COALESCE(c.title, ''), c.about, c.handle, c.is_public, me.role, me.muted,
	       (SELECT count(*) FROM conversation_members o WHERE o.conversation_id = c.id),
	       c.last_seq, c.last_rev, me.last_read_seq,
	       COALESCE((SELECT max(o.last_read_seq) FROM conversation_members o
	                  WHERE o.conversation_id = c.id AND o.user_id <> me.user_id), 0),
	       COALESCE(c.last_message_at, c.created_at),
	       p.id::text, p.username, p.display_name, p.last_seen_at,
	       m.id::text, m.seq, m.rev, m.kind, m.meta, m.sender_id::text, mu.username, mu.display_name,
	       m.client_id::text, m.body, m.created_at, m.edited_at, m.deleted_at,
	       (SELECT count(*) FROM messages x
	         WHERE x.conversation_id = c.id
	           AND x.seq > me.last_read_seq
	           AND x.sender_id <> me.user_id
	           AND x.kind = 'text'
	           AND x.deleted_at IS NULL)
	FROM conversation_members me
	JOIN conversations c ON c.id = me.conversation_id
	LEFT JOIN LATERAL (
		SELECT u.id, u.username, u.display_name, u.last_seen_at
		FROM conversation_members o
		JOIN users u ON u.id = o.user_id
		WHERE c.kind = 'direct' AND o.conversation_id = c.id AND o.user_id <> me.user_id
		LIMIT 1
	) p ON true
	LEFT JOIN messages m ON m.conversation_id = c.id AND m.seq = c.last_seq
	LEFT JOIN users mu ON mu.id = m.sender_id
	WHERE me.user_id = $1::uuid`

func scanChat(row scanner) (Chat, error) {
	var (
		c          Chat
		peerID     *string
		peerName   *string
		peerDisp   *string
		peerSeen   *time.Time
		msgID      *string
		msgSeq     *int64
		msgRev     *int64
		msgKind    *string
		msgMeta    []byte
		msgSender  *string
		senderName *string
		senderDisp *string
		msgClient  *string
		msgBody    *string
		msgAt      *time.Time
		msgEdited  *time.Time
		msgDeleted *time.Time
	)
	err := row.Scan(
		&c.ID, &c.Kind, &c.Title, &c.About, &c.Handle, &c.IsPublic, &c.Role, &c.Muted,
		&c.MemberCount, &c.LastSeq, &c.LastRev, &c.LastReadSeq, &c.PeerLastReadSeq, &c.UpdatedAt,
		&peerID, &peerName, &peerDisp, &peerSeen,
		&msgID, &msgSeq, &msgRev, &msgKind, &msgMeta, &msgSender, &senderName, &senderDisp,
		&msgClient, &msgBody, &msgAt, &msgEdited, &msgDeleted,
		&c.Unread,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	if err != nil {
		return Chat{}, err
	}
	if peerID != nil {
		c.Peer = &PublicUser{ID: *peerID, Username: *peerName, DisplayName: *peerDisp, LastSeenAt: peerSeen}
		c.Title = *peerDisp
	}
	if msgID != nil {
		c.LastMessage = &Message{
			ID: *msgID, ChatID: c.ID, Seq: *msgSeq, Rev: *msgRev, Kind: *msgKind, SenderID: *msgSender,
			Sender:   Sender{ID: *msgSender, Username: *senderName, DisplayName: *senderDisp},
			ClientID: *msgClient, Body: *msgBody, CreatedAt: *msgAt,
			EditedAt: msgEdited, DeletedAt: msgDeleted,
		}
		if len(msgMeta) > 0 {
			c.LastMessage.Meta = msgMeta
		}
	}
	return c, nil
}

func (s *Store) ListChats(ctx context.Context, userID string) ([]Chat, error) {
	rows, err := s.pool.Query(ctx, chatQuery+`
		  AND (c.last_seq > 0 OR c.created_by = me.user_id)
		ORDER BY COALESCE(c.last_message_at, c.created_at) DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := []Chat{}
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var last []*Message
	for i := range chats {
		if chats[i].LastMessage != nil {
			last = append(last, chats[i].LastMessage)
		}
	}
	return chats, s.finishMessages(ctx, last)
}

func (s *Store) finishMessages(ctx context.Context, messages []*Message) error {
	if err := loadAttachments(ctx, s.pool, messages); err != nil {
		return err
	}
	for _, m := range messages {
		if m.Attachments == nil {
			m.Attachments = []Attachment{}
		}
	}
	return nil
}

func (s *Store) GetChat(ctx context.Context, userID, chatID string) (Chat, error) {
	c, err := scanChat(s.pool.QueryRow(ctx, chatQuery+` AND c.id = $2::uuid`, userID, chatID))
	if err != nil || c.LastMessage == nil {
		return c, err
	}
	return c, s.finishMessages(ctx, []*Message{c.LastMessage})
}

func (s *Store) SetLastSeen(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1::uuid`, userID, at)
	return err
}

func (s *Store) ChatTitle(ctx context.Context, chatID string) (string, string, error) {
	var kind, title string
	err := s.pool.QueryRow(ctx, `SELECT kind, COALESCE(title, '') FROM conversations WHERE id = $1::uuid`, chatID).Scan(&kind, &title)
	return kind, title, err
}
