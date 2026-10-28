package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrAttachmentInvalid = errors.New("attachment cannot be used")

const MaxAttachmentsPerMessage = 10

type Attachment struct {
	ID          string    `json:"id"`
	ChatID      string    `json:"-"`
	UploaderID  string    `json:"-"`
	MessageID   *string   `json:"-"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Width       *int      `json:"width,omitempty"`
	Height      *int      `json:"height,omitempty"`
	DurationMs  *int      `json:"duration_ms,omitempty"`
	ObjectKey   string    `json:"-"`
	CreatedAt   time.Time `json:"-"`
	URL         string    `json:"url"`
}

const attachmentColumns = `id::text, conversation_id::text, uploader_id::text, message_id::text, kind, name,
	content_type, size, width, height, duration_ms, object_key, created_at`

func scanAttachment(row scanner) (Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.ChatID, &a.UploaderID, &a.MessageID, &a.Kind, &a.Name,
		&a.ContentType, &a.Size, &a.Width, &a.Height, &a.DurationMs, &a.ObjectKey, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	return a, err
}

type NewAttachment struct {
	ID          string
	ChatID      string
	UploaderID  string
	Kind        string
	Name        string
	ContentType string
	Size        int64
	Width       *int
	Height      *int
	DurationMs  *int
	ObjectKey   string
}

func (s *Store) CreateAttachment(ctx context.Context, a NewAttachment) (Attachment, error) {
	return scanAttachment(s.pool.QueryRow(ctx, `
		INSERT INTO attachments (id, conversation_id, uploader_id, kind, name, content_type, size, width, height, duration_ms, object_key)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+attachmentColumns,
		a.ID, a.ChatID, a.UploaderID, a.Kind, a.Name, a.ContentType, a.Size, a.Width, a.Height, a.DurationMs, a.ObjectKey,
	))
}

func (s *Store) Attachment(ctx context.Context, id string) (Attachment, error) {
	return scanAttachment(s.pool.QueryRow(ctx, `
		SELECT `+attachmentColumns+` FROM attachments
		WHERE id = $1::uuid AND deleted_at IS NULL`,
		id,
	))
}

func linkAttachments(ctx context.Context, tx pgx.Tx, chatID, uploaderID, messageID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE attachments SET message_id = $1::uuid
		WHERE id = ANY($2::uuid[])
		  AND conversation_id = $3::uuid
		  AND uploader_id = $4::uuid
		  AND message_id IS NULL
		  AND deleted_at IS NULL`,
		messageID, ids, chatID, uploaderID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return ErrAttachmentInvalid
	}
	return nil
}

func (s *Store) DeleteMessageAttachments(ctx context.Context, messageID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE attachments SET deleted_at = now()
		WHERE message_id = $1::uuid AND deleted_at IS NULL
		RETURNING object_key`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

type rowsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadAttachments(ctx context.Context, q rowsQuerier, messages []*Message) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(messages))
	index := make(map[string]*Message, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
		index[m.ID] = m
	}
	rows, err := q.Query(ctx, `
		SELECT `+attachmentColumns+` FROM attachments
		WHERE message_id = ANY($1::uuid[]) AND deleted_at IS NULL
		ORDER BY created_at, id`,
		ids,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return err
		}
		if a.MessageID != nil {
			if m := index[*a.MessageID]; m != nil {
				m.Attachments = append(m.Attachments, a)
			}
		}
	}
	return rows.Err()
}
