package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"connecta/internal/store"
)

const (
	maxTitleLen   = 64
	maxAboutLen   = 255
	maxBatchUsers = 50
)

var handleRe = regexp.MustCompile(`^[a-z][a-z0-9_]{4,31}$`)

type chatEvent struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id"`
}

func membershipFrom(ctx context.Context) store.Membership {
	m, _ := ctx.Value(membershipKey{}).(store.Membership)
	return m
}

func titleProblem(title string) string {
	n := utf8.RuneCountInString(title)
	switch {
	case n == 0:
		return "empty"
	case n > maxTitleLen:
		return "too_long"
	}
	return ""
}

func normalizeIDs(raw []string) ([]string, bool) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.ToLower(strings.TrimSpace(id))
		if !uuidRe.MatchString(id) {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

func (s *Server) respondChat(w http.ResponseWriter, r *http.Request, userID, chatID string, status int) {
	chat, err := s.store.GetChat(r.Context(), userID, chatID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.decorateChat(r.Context(), &chat)
	writeJSON(w, status, chat)
}

func (s *Server) decorateChat(ctx context.Context, chat *store.Chat) {
	if chat.LastMessage != nil {
		s.signMessage(chat.LastMessage)
	}
	if chat.Peer != nil {
		s.withPresence(ctx, []*store.PublicUser{chat.Peer})
		return
	}
	if chat.Kind != store.KindGroup {
		return
	}
	ids, err := s.store.MemberIDs(ctx, chat.ID)
	if err != nil {
		return
	}
	online, err := s.presence.Online(ctx, ids)
	if err != nil {
		return
	}
	for _, v := range online {
		if v {
			chat.OnlineCount++
		}
	}
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	var req struct {
		Title     string   `json:"title"`
		MemberIDs []string `json:"member_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)

	fields := map[string]string{}
	if p := titleProblem(req.Title); p != "" {
		fields["title"] = p
	}
	ids, ok := normalizeIDs(req.MemberIDs)
	switch {
	case !ok:
		fields["member_ids"] = "invalid"
	case len(ids) > maxBatchUsers:
		fields["member_ids"] = "too_many"
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	if !s.allow(w, r, "create-chat:"+me, 20, time.Hour) {
		return
	}

	chatID, msg, err := s.store.CreateGroup(r.Context(), store.NewGroup{
		Kind: store.KindGroup, OwnerID: me, Title: req.Title, MemberIDs: ids,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	s.respondChat(w, r, me, chatID, http.StatusCreated)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	var req struct {
		Title    string `json:"title"`
		About    string `json:"about"`
		Handle   string `json:"handle"`
		IsPublic bool   `json:"is_public"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.About = strings.TrimSpace(req.About)
	req.Handle = normalizeUsername(req.Handle)

	fields := map[string]string{}
	if p := titleProblem(req.Title); p != "" {
		fields["title"] = p
	}
	if utf8.RuneCountInString(req.About) > maxAboutLen {
		fields["about"] = "too_long"
	}
	var handle *string
	if req.IsPublic {
		if !handleRe.MatchString(req.Handle) {
			fields["handle"] = "invalid"
		}
		handle = &req.Handle
	} else if req.Handle != "" {
		fields["handle"] = "not_allowed"
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	if !s.allow(w, r, "create-chat:"+me, 20, time.Hour) {
		return
	}

	chatID, msg, err := s.store.CreateGroup(r.Context(), store.NewGroup{
		Kind: store.KindChannel, OwnerID: me, Title: req.Title, About: req.About, Handle: handle, IsPublic: req.IsPublic,
	})
	if errors.Is(err, store.ErrHandleTaken) {
		writeError(w, http.StatusConflict, "handle_taken", "handle already taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	s.respondChat(w, r, me, chatID, http.StatusCreated)
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.store.Members(r.Context(), chatIDFrom(r.Context()))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ptrs := make([]*store.PublicUser, len(members))
	for i := range members {
		ptrs[i] = &members[i].PublicUser
	}
	s.withPresence(r.Context(), ptrs)
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) handleAddMembers(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	if !membershipFrom(r.Context()).CanManage() {
		writeError(w, http.StatusForbidden, "forbidden", "only admins can add members")
		return
	}
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ids, ok := normalizeIDs(req.UserIDs)
	if !ok || len(ids) == 0 || len(ids) > maxBatchUsers {
		writeValidation(w, map[string]string{"user_ids": "invalid"})
		return
	}
	users, err := s.store.ExistingUsers(r.Context(), ids)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(users) != len(ids) {
		writeError(w, http.StatusNotFound, "user_not_found", "some users do not exist")
		return
	}

	messages, err := s.store.AddMembers(r.Context(), chatID, me, users)
	if errors.Is(err, store.ErrTooManyMembers) {
		writeError(w, http.StatusUnprocessableEntity, "too_many_members", "member limit reached")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, msg := range messages {
		s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	}
	if len(messages) > 0 {
		s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	}
	s.handleMembers(w, r)
}

func (s *Server) targetMember(w http.ResponseWriter, r *http.Request) (store.Membership, string, bool) {
	target := strings.ToLower(chi.URLParam(r, "userID"))
	if !uuidRe.MatchString(target) {
		writeError(w, http.StatusNotFound, "member_not_found", "member not found")
		return store.Membership{}, "", false
	}
	m, ok, err := s.store.Membership(r.Context(), chatIDFrom(r.Context()), target)
	if err != nil {
		s.internalError(w, r, err)
		return store.Membership{}, "", false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "member_not_found", "member not found")
		return store.Membership{}, "", false
	}
	return m, target, true
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	actor := membershipFrom(r.Context())
	if !actor.CanManage() {
		writeError(w, http.StatusForbidden, "forbidden", "only admins can remove members")
		return
	}
	target, targetID, ok := s.targetMember(w, r)
	if !ok {
		return
	}
	if targetID == me {
		writeError(w, http.StatusUnprocessableEntity, "use_leave", "use leave to exit the chat")
		return
	}
	if target.Role == store.RoleOwner || (target.Role == store.RoleAdmin && actor.Role != store.RoleOwner) {
		writeError(w, http.StatusForbidden, "forbidden", "cannot remove this member")
		return
	}

	msg, err := s.store.RemoveMember(r.Context(), chatID, me, targetID)
	if errors.Is(err, store.ErrNotMember) {
		writeError(w, http.StatusNotFound, "member_not_found", "member not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: msg})
	s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.publish(r.Context(), []string{targetID}, chatEvent{Type: "chat.removed", ChatID: chatID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	chatID := chatIDFrom(r.Context())
	actor := membershipFrom(r.Context())
	if actor.Kind == store.KindDirect || actor.Role != store.RoleOwner {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can change roles")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role != store.RoleAdmin && req.Role != store.RoleMember {
		writeValidation(w, map[string]string{"role": "invalid"})
		return
	}
	target, targetID, ok := s.targetMember(w, r)
	if !ok {
		return
	}
	if target.Role == store.RoleOwner {
		writeError(w, http.StatusForbidden, "forbidden", "cannot change the owner role")
		return
	}
	if err := s.store.SetRole(r.Context(), chatID, targetID, req.Role); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	m := membershipFrom(r.Context())
	if m.Kind == store.KindDirect {
		writeError(w, http.StatusUnprocessableEntity, "cannot_leave_direct", "direct chats cannot be left")
		return
	}

	msg, err := s.store.Leave(r.Context(), chatID, me, m.Kind == store.KindGroup)
	if errors.Is(err, store.ErrNotMember) {
		writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg != nil {
		s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: *msg})
	}
	s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.publish(r.Context(), []string{me}, chatEvent{Type: "chat.removed", ChatID: chatID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateChat(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	if !membershipFrom(r.Context()).CanManage() {
		writeError(w, http.StatusForbidden, "forbidden", "only admins can edit the chat")
		return
	}
	var req struct {
		Title *string `json:"title"`
		About *string `json:"about"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		req.Title = &t
		if p := titleProblem(t); p != "" {
			fields["title"] = p
		}
	}
	if req.About != nil {
		a := strings.TrimSpace(*req.About)
		req.About = &a
		if utf8.RuneCountInString(a) > maxAboutLen {
			fields["about"] = "too_long"
		}
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}

	msg, err := s.store.UpdateInfo(r.Context(), chatID, me, req.Title, req.About)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg != nil {
		s.broadcastToChat(r.Context(), chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: *msg})
	}
	s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.respondChat(w, r, me, chatID, http.StatusOK)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := strings.ToLower(chi.URLParam(r, "chatID"))
	if !uuidRe.MatchString(chatID) {
		writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return
	}
	err := s.store.Join(r.Context(), chatID, me)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return
	case errors.Is(err, store.ErrAlreadyMember):
		s.respondChat(w, r, me, chatID, http.StatusOK)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), chatID, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.respondChat(w, r, me, chatID, http.StatusCreated)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	if !s.allow(w, r, "search:"+me, 60, time.Minute) {
		return
	}
	type result struct {
		Users    []store.PublicUser    `json:"users"`
		Channels []store.ChannelResult `json:"channels"`
	}
	out := result{Users: []store.PublicUser{}, Channels: []store.ChannelResult{}}
	q := normalizeUsername(r.URL.Query().Get("q"))
	if !searchRe.MatchString(q) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var err error
	if out.Users, err = s.store.SearchUsers(r.Context(), me, q, 10); err != nil {
		s.internalError(w, r, err)
		return
	}
	if out.Channels, err = s.store.SearchChannels(r.Context(), me, q, 10); err != nil {
		s.internalError(w, r, err)
		return
	}
	ptrs := make([]*store.PublicUser, len(out.Users))
	for i := range out.Users {
		ptrs[i] = &out.Users[i]
	}
	s.withPresence(r.Context(), ptrs)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) publish(ctx context.Context, userIDs []string, event any) {
	if err := s.hub.Publish(ctx, userIDs, event); err != nil {
		s.log.Error("realtime publish failed", "err", err)
	}
}
