package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"connecta/internal/store"
)

const (
	maxImageBytes  = 15 << 20
	maxVoiceBytes  = 20 << 20
	maxFileBytes   = 50 << 20
	maxVoiceMs     = 15 * 60 * 1000
	sniffBytes     = 64 << 10
	maxNameLen     = 200
	transferWindow = 10 * time.Minute
)

var (
	imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}
	voiceTypes = map[string]bool{"audio/webm": true, "audio/ogg": true, "audio/mp4": true, "audio/mpeg": true, "audio/wav": true}
	limits     = map[string]int64{"image": maxImageBytes, "voice": maxVoiceBytes, "file": maxFileBytes}
	defaultExt = map[string]string{"image": "image", "voice": "voice", "file": "file"}
)

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func baseType(ct string) string {
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return strings.ToLower(t)
}

func cleanName(raw, kind string) string {
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(raw, `\`, "/")))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	if name == "" || name == "." || name == "/" {
		name = defaultExt[kind]
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		name = string([]rune(name)[:maxNameLen])
	}
	return name
}

func (s *Server) signMessage(m *store.Message) {
	for i := range m.Attachments {
		m.Attachments[i].URL = s.signer.URL(m.Attachments[i].ID)
	}
}

func (s *Server) signMessages(ms []store.Message) {
	for i := range ms {
		s.signMessage(&ms[i])
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	if !membershipFrom(r.Context()).CanPost() {
		writeError(w, http.StatusForbidden, "read_only", "only admins can post in this channel")
		return
	}

	q := r.URL.Query()
	kind := q.Get("kind")
	limit, ok := limits[kind]
	if !ok {
		writeValidation(w, map[string]string{"kind": "invalid"})
		return
	}
	var duration *int
	if kind == "voice" {
		ms, err := strconv.Atoi(q.Get("duration_ms"))
		if err != nil || ms < 1 || ms > maxVoiceMs {
			writeValidation(w, map[string]string{"duration_ms": "invalid"})
			return
		}
		duration = &ms
	}
	size := r.ContentLength
	if size <= 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if size > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "file is too large")
		return
	}
	if !s.allow(w, r, "upload:"+me, 60, 10*time.Minute) {
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(transferWindow))
	_ = rc.SetWriteDeadline(time.Now().Add(transferWindow))

	body := http.MaxBytesReader(w, r.Body, size)
	head := make([]byte, min(size, sniffBytes))
	if _, err := io.ReadFull(body, head); err != nil {
		writeError(w, http.StatusBadRequest, "bad_upload", "upload body is shorter than Content-Length")
		return
	}
	sniffed := baseType(http.DetectContentType(head))
	declared := baseType(r.Header.Get("Content-Type"))

	att := store.NewAttachment{
		ID: randomUUID(), ChatID: chatID, UploaderID: me, Kind: kind,
		Name: cleanName(q.Get("name"), kind), Size: size, DurationMs: duration,
	}
	switch kind {
	case "image":
		if !imageTypes[sniffed] {
			writeValidation(w, map[string]string{"file": "not_an_image"})
			return
		}
		att.ContentType = sniffed
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(head)); err == nil {
			att.Width, att.Height = &cfg.Width, &cfg.Height
		}
	case "voice":
		if !voiceTypes[declared] || strings.HasPrefix(sniffed, "text/") {
			writeValidation(w, map[string]string{"file": "not_audio"})
			return
		}
		att.ContentType = declared
	default:
		att.ContentType = declared
		if att.ContentType == "" {
			att.ContentType = sniffed
		}
	}
	att.ObjectKey = "att/" + chatID + "/" + att.ID

	if err := s.files.Put(r.Context(), att.ObjectKey, io.MultiReader(bytes.NewReader(head), body), size, att.ContentType); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "file is too large")
			return
		}
		s.internalError(w, r, err)
		return
	}
	created, err := s.store.CreateAttachment(r.Context(), att)
	if err != nil {
		s.removeObjects([]string{att.ObjectKey})
		s.internalError(w, r, err)
		return
	}
	created.URL = s.signer.URL(created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(chi.URLParam(r, "fileID"))
	if !uuidRe.MatchString(id) || s.signer.Verify(id, r.URL.Query().Get("exp"), r.URL.Query().Get("sig")) != nil {
		writeError(w, http.StatusForbidden, "forbidden", "invalid or expired link")
		return
	}
	att, err := s.store.Attachment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	obj, modified, err := s.files.Get(r.Context(), att.ObjectKey)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer obj.Close()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(transferWindow))

	disposition := "inline"
	contentType := att.ContentType
	if att.Kind == "file" {
		disposition = "attachment"
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": att.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", modified, obj)
}

func (s *Server) handleFileURL(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(chi.URLParam(r, "fileID"))
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	att, err := s.store.Attachment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ok, err := s.store.IsMember(r.Context(), att.ChatID, claimsFrom(r.Context()).Subject)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "file_not_found", "file not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.signer.URL(id)})
}

func (s *Server) removeObjects(keys []string) {
	if len(keys) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, k := range keys {
			if err := s.files.Remove(ctx, k); err != nil {
				s.log.Warn("remove object failed", "key", k, "err", err)
			}
		}
	}()
}
