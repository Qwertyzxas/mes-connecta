package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"connecta/internal/store"
)

const (
	maxMessageLen   = 4096
	defaultPageSize = 50
	maxPageSize     = 100
	searchLimit     = 20
)

var searchRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type chatKey struct{}

type membershipKey struct{}

type messageEvent struct {
	Type    string        `json:"type"`
	ChatID  string        `json:"chat_id"`
	Message store.Message `json:"message"`
}

type readEvent struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id"`
	Seq    int64  `json:"seq"`
}

func (s *Server) requireMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chatID := chi.URLParam(r, "chatID")
		if !uuidRe.MatchString(chatID) {
			writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
			return
		}
		m, ok, err := s.store.Membership(r.Context(), chatID, claimsFrom(r.Context()).Subject)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
			return
		}
		ctx := context.WithValue(r.Context(), chatKey{}, chatID)
		ctx = context.WithValue(ctx, membershipKey{}, m)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func chatIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(chatKey{}).(string)
	return id
}

func (s *Server) withPresence(ctx context.Context, users []*store.PublicUser) {
	if len(users) == 0 {
		return
	}
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	online, err := s.presence.Online(ctx, ids)
	if err != nil {
		s.log.Warn("presence lookup failed", "err", err)
		return
	}
	for _, u := range users {
		u.Online = online[u.ID]
	}
}

func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	if !s.allow(w, r, "search:"+me, 60, time.Minute) {
		return
	}
	q := normalizeUsername(r.URL.Query().Get("q"))
	if !searchRe.MatchString(q) {
		writeJSON(w, http.StatusOK, []store.PublicUser{})
		return
	}
	users, err := s.store.SearchUsers(r.Context(), me, q, searchLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ptrs := make([]*store.PublicUser, len(users))
	for i := range users {
		ptrs[i] = &users[i]
	}
	s.withPresence(r.Context(), ptrs)
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleOpenDirect(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.UserID = strings.ToLower(strings.TrimSpace(req.UserID))
	if !uuidRe.MatchString(req.UserID) {
		writeValidation(w, map[string]string{"user_id": "invalid"})
		return
	}
	if req.UserID == me {
		writeError(w, http.StatusUnprocessableEntity, "cannot_chat_with_self", "cannot open a chat with yourself")
		return
	}
	exists, err := s.store.UserExists(r.Context(), req.UserID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
		return
	}

	chatID, created, err := s.store.GetOrCreateDirect(r.Context(), me, req.UserID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	s.respondChat(w, r, me, chatID, status)
}

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chats, err := s.store.ListChats(r.Context(), me)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	peers := make([]*store.PublicUser, 0, len(chats))
	for i := range chats {
		if chats[i].Peer != nil {
			peers = append(peers, chats[i].Peer)
		}
	}
	s.withPresence(r.Context(), peers)
	for i := range chats {
		if chats[i].LastMessage != nil {
			s.signMessage(chats[i].LastMessage)
		}
	}
	writeJSON(w, http.StatusOK, chats)
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	chat, err := s.store.GetChat(r.Context(), claimsFrom(r.Context()).Subject, chatIDFrom(r.Context()))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.decorateChat(r.Context(), &chat)
	writeJSON(w, http.StatusOK, chat)
}

func parseSeq(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= 0
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, okBefore := parseSeq(q.Get("before_seq"))
	after, okAfter := parseSeq(q.Get("after_seq"))
	since, okSince := parseSeq(q.Get("since_rev"))
	limit, okLimit := defaultPageSize, true
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		okLimit = err == nil && n >= 1
		limit = min(n, maxPageSize)
	}
	modes := 0
	for _, v := range []int64{before, after, since} {
		if v > 0 {
			modes++
		}
	}
	if !okBefore || !okAfter || !okSince || !okLimit || modes > 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid paging parameters")
		return
	}

	messages, err := s.store.ListMessages(r.Context(), chatIDFrom(r.Context()), store.MessageQuery{
		BeforeSeq: before, AfterSeq: after, SinceRev: since, Limit: limit,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.signMessages(messages)
	writeJSON(w, http.StatusOK, messages)
}

func validateBody(body string) string {
	switch {
	case body == "":
		return "empty"
	case !utf8.ValidString(body):
		return "invalid"
	case utf8.RuneCountInString(body) > maxMessageLen:
		return "too_long"
	}
	return ""
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())

	var req struct {
		ClientID      string   `json:"client_id"`
		Body          string   `json:"body"`
		ReplyToID     string   `json:"reply_to_id"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ClientID = strings.ToLower(strings.TrimSpace(req.ClientID))
	req.ReplyToID = strings.ToLower(strings.TrimSpace(req.ReplyToID))
	req.Body = strings.TrimSpace(req.Body)

	fields := map[string]string{}
	if !uuidRe.MatchString(req.ClientID) {
		fields["client_id"] = "invalid"
	}
	if req.ReplyToID != "" && !uuidRe.MatchString(req.ReplyToID) {
		fields["reply_to_id"] = "invalid"
	}
	attachmentIDs, ok := normalizeIDs(req.AttachmentIDs)
	switch {
	case !ok || len(attachmentIDs) > store.MaxAttachmentsPerMessage:
		fields["attachment_ids"] = "invalid"
	case len(attachmentIDs) > 0 && req.Body == "":
	default:
		if p := validateBody(req.Body); p != "" {
			fields["body"] = p
		}
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	if !membershipFrom(r.Context()).CanPost() {
		writeError(w, http.StatusForbidden, "read_only", "only admins can post in this channel")
		return
	}
	if !s.allow(w, r, "send:"+me, 30, 10*time.Second) {
		return
	}

	msg, created, err := s.store.InsertMessage(r.Context(), chatID, me, req.ClientID, req.Body, req.ReplyToID, attachmentIDs)
	if errors.Is(err, store.ErrClientIDConflict) {
		writeError(w, http.StatusConflict, "client_id_conflict", "client_id already used")
		return
	}
	if errors.Is(err, store.ErrReplyNotFound) {
		writeValidation(w, map[string]string{"reply_to_id": "not_found"})
		return
	}
	if errors.Is(err, store.ErrAttachmentInvalid) {
		writeValidation(w, map[string]string{"attachment_ids": "invalid"})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	s.signMessage(&msg)
	if !created {
		writeJSON(w, http.StatusOK, msg)
		return
	}

	s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) messageIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.ToLower(chi.URLParam(r, "messageID"))
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "message_not_found", "message not found")
		return "", false
	}
	return id, true
}

func (s *Server) writeChangeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "message_not_found", "message not found")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "not allowed to change this message")
	case errors.Is(err, store.ErrMessageDeleted):
		writeError(w, http.StatusConflict, "message_deleted", "message was deleted")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	messageID, ok := s.messageIDParam(w, r)
	if !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if p := validateBody(req.Body); p != "" {
		writeValidation(w, map[string]string{"body": p})
		return
	}
	if !s.allow(w, r, "send:"+me, 30, 10*time.Second) {
		return
	}

	msg, changed, err := s.store.EditMessage(r.Context(), chatID, messageID, me, req.Body)
	if err != nil {
		s.writeChangeError(w, r, err)
		return
	}
	s.signMessage(&msg)
	if changed {
		s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.updated", ChatID: chatID, Message: msg})
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	messageID, ok := s.messageIDParam(w, r)
	if !ok {
		return
	}

	msg, changed, err := s.store.DeleteMessage(r.Context(), chatID, messageID, me, membershipFrom(r.Context()).CanManage())
	if err != nil {
		s.writeChangeError(w, r, err)
		return
	}
	if changed {
		keys, err := s.store.DeleteMessageAttachments(r.Context(), msg.ID)
		if err != nil {
			s.log.Error("detach attachments failed", "message_id", msg.ID, "err", err)
		}
		s.removeObjects(keys)
		msg.Attachments = []store.Attachment{}
		s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.updated", ChatID: chatID, Message: msg})
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())

	var req struct {
		Seq int64 `json:"seq"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Seq < 1 {
		writeValidation(w, map[string]string{"seq": "invalid"})
		return
	}

	seq, changed, err := s.store.MarkRead(r.Context(), chatID, me, req.Seq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed {
		s.broadcastToChat(r.Context(), chatID, readEvent{Type: "chat.read", ChatID: chatID, UserID: me, Seq: seq})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) broadcastToChat(ctx context.Context, chatID string, event any) {
	members, err := s.store.MemberIDs(ctx, chatID)
	if err != nil {
		s.log.Error("load chat members failed", "chat_id", chatID, "err", err)
		return
	}
	if err := s.hub.Publish(ctx, members, event); err != nil {
		s.log.Error("realtime publish failed", "chat_id", chatID, "err", err)
	}
}
