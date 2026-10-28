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
		ok, err := s.store.IsMember(r.Context(), chatID, claimsFrom(r.Context()).Subject)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chatKey{}, chatID)))
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
	chat, err := s.store.GetChat(r.Context(), me, chatID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.withPresence(r.Context(), []*store.PublicUser{&chat.Peer})

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, chat)
}

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chats, err := s.store.ListChats(r.Context(), me)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	peers := make([]*store.PublicUser, len(chats))
	for i := range chats {
		peers[i] = &chats[i].Peer
	}
	s.withPresence(r.Context(), peers)
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
	s.withPresence(r.Context(), []*store.PublicUser{&chat.Peer})
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
	limit := defaultPageSize
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			okAfter = false
		}
		limit = min(n, maxPageSize)
	}
	if !okBefore || !okAfter || (before > 0 && after > 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid paging parameters")
		return
	}

	messages, err := s.store.ListMessages(r.Context(), chatIDFrom(r.Context()), before, after, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())

	var req struct {
		ClientID string `json:"client_id"`
		Body     string `json:"body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ClientID = strings.ToLower(strings.TrimSpace(req.ClientID))
	req.Body = strings.TrimSpace(req.Body)

	fields := map[string]string{}
	if !uuidRe.MatchString(req.ClientID) {
		fields["client_id"] = "invalid"
	}
	switch {
	case req.Body == "":
		fields["body"] = "empty"
	case !utf8.ValidString(req.Body):
		fields["body"] = "invalid"
	case utf8.RuneCountInString(req.Body) > maxMessageLen:
		fields["body"] = "too_long"
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	if !s.allow(w, r, "send:"+me, 30, 10*time.Second) {
		return
	}

	msg, created, err := s.store.InsertMessage(r.Context(), chatID, me, req.ClientID, req.Body)
	if errors.Is(err, store.ErrClientIDConflict) {
		writeError(w, http.StatusConflict, "client_id_conflict", "client_id already used")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	if !created {
		writeJSON(w, http.StatusOK, msg)
		return
	}

	s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	writeJSON(w, http.StatusCreated, msg)
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
