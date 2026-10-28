package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"connecta/internal/push"
	"connecta/internal/store"
)

const janitorEvery = 10 * time.Second

type callEvent struct {
	Type   string     `json:"type"`
	ChatID string     `json:"chat_id"`
	Call   store.Call `json:"call"`
}

type incomingEvent struct {
	Type      string     `json:"type"`
	ChatID    string     `json:"chat_id"`
	Call      store.Call `json:"call"`
	Caller    callerInfo `json:"caller"`
	ChatTitle string     `json:"chat_title"`
}

type callerInfo struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type callSession struct {
	Call  store.Call `json:"call"`
	Token string     `json:"token"`
	URL   string     `json:"url"`
}

func (s *Server) callsEnabled(w http.ResponseWriter) bool {
	if s.livekit == nil {
		writeError(w, http.StatusServiceUnavailable, "calls_unavailable", "calls are not configured")
		return false
	}
	return true
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, call store.Call, status int) {
	claims := claimsFrom(r.Context())
	user, err := s.store.UserByID(r.Context(), claims.Subject)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	token, err := s.livekit.JoinToken(call.ID, user.ID, user.DisplayName)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, callSession{Call: call, Token: token, URL: s.livekit.URL()})
}

func (s *Server) handleStartCall(w http.ResponseWriter, r *http.Request) {
	if !s.callsEnabled(w) {
		return
	}
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	var req struct {
		Video bool `json:"video"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if membershipFrom(r.Context()).Kind == store.KindChannel {
		writeError(w, http.StatusForbidden, "calls_not_allowed", "calls are not available in channels")
		return
	}
	if !s.allow(w, r, "call:"+me, 10, time.Minute) {
		return
	}

	call, created, err := s.store.StartCall(r.Context(), chatID, me, req.Video)
	if errors.Is(err, store.ErrCallOver) {
		writeError(w, http.StatusConflict, "call_over", "call is over")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if created {
		s.announceCall(r.Context(), call, me)
		s.session(w, r, call, http.StatusCreated)
		return
	}
	s.broadcastToChat(r.Context(), call.ChatID, callEvent{Type: "call.updated", ChatID: call.ChatID, Call: call})
	s.session(w, r, call, http.StatusOK)
}

func (s *Server) announceCall(ctx context.Context, call store.Call, callerID string) {
	caller, err := s.store.UserByID(ctx, callerID)
	if err != nil {
		s.log.Error("load caller failed", "err", err)
		return
	}
	kind, title, err := s.store.ChatTitle(ctx, call.ChatID)
	if err != nil {
		s.log.Error("load chat title failed", "err", err)
		return
	}
	members, err := s.store.MemberIDs(ctx, call.ChatID)
	if err != nil {
		s.log.Error("load chat members failed", "err", err)
		return
	}
	event := incomingEvent{
		Type: "call.incoming", ChatID: call.ChatID, Call: call, ChatTitle: title,
		Caller: callerInfo{ID: caller.ID, Username: caller.Username, DisplayName: caller.DisplayName},
	}
	if err := s.hub.Publish(ctx, members, event); err != nil {
		s.log.Error("realtime publish failed", "err", err)
	}

	body := "Входящий звонок"
	if call.Video {
		body = "Входящий видеозвонок"
	}
	payload := push.Payload{
		Type: "call", ChatID: call.ChatID, URL: "/chat/" + call.ChatID, Tag: "call-" + call.ID,
		Title: caller.DisplayName, Body: body,
	}
	if kind == store.KindGroup {
		payload.Title = title
		payload.Body = caller.DisplayName + ": " + body
	}
	s.push.NotifyMessage(call.ChatID, callerID, payload)
}

func (s *Server) callForMember(w http.ResponseWriter, r *http.Request) (store.Call, bool) {
	id := chi.URLParam(r, "callID")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "call_not_found", "call not found")
		return store.Call{}, false
	}
	call, err := s.store.Call(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "call_not_found", "call not found")
		return store.Call{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return store.Call{}, false
	}
	_, ok, err := s.store.Membership(r.Context(), call.ChatID, claimsFrom(r.Context()).Subject)
	if err != nil {
		s.internalError(w, r, err)
		return store.Call{}, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "call_not_found", "call not found")
		return store.Call{}, false
	}
	return call, true
}

func (s *Server) handleJoinCall(w http.ResponseWriter, r *http.Request) {
	if !s.callsEnabled(w) {
		return
	}
	call, ok := s.callForMember(w, r)
	if !ok {
		return
	}
	call, err := s.store.JoinCall(r.Context(), call.ID, claimsFrom(r.Context()).Subject)
	if errors.Is(err, store.ErrCallOver) {
		writeError(w, http.StatusConflict, "call_over", "call is over")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.broadcastToChat(r.Context(), call.ChatID, callEvent{Type: "call.updated", ChatID: call.ChatID, Call: call})
	s.session(w, r, call, http.StatusOK)
}

func (s *Server) handleLeaveCall(w http.ResponseWriter, r *http.Request) {
	call, ok := s.callForMember(w, r)
	if !ok {
		return
	}
	change, err := s.store.LeaveCall(r.Context(), call.ID, claimsFrom(r.Context()).Subject)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.publishCallChange(r.Context(), change, call.Live())
	writeJSON(w, http.StatusOK, change.Call)
}

func (s *Server) handleDeclineCall(w http.ResponseWriter, r *http.Request) {
	call, ok := s.callForMember(w, r)
	if !ok {
		return
	}
	change, err := s.store.DeclineCall(r.Context(), call.ID, claimsFrom(r.Context()).Subject)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.publishCallChange(r.Context(), change, call.Live())
	writeJSON(w, http.StatusOK, change.Call)
}

func (s *Server) handleListCalls(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	if r.URL.Query().Get("live") == "1" {
		list, err := s.store.LiveCalls(r.Context(), me)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, list)
		return
	}
	list, err := s.store.RecentCalls(r.Context(), me, 100)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	peers := make([]*store.PublicUser, 0, len(list))
	for i := range list {
		if list[i].Peer != nil {
			peers = append(peers, list[i].Peer)
		}
	}
	s.withPresence(r.Context(), peers)
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) publishCallChange(ctx context.Context, change store.CallChange, wasLive bool) {
	if !wasLive {
		return
	}
	chatID := change.Call.ChatID
	s.broadcastToChat(ctx, chatID, callEvent{Type: "call.updated", ChatID: chatID, Call: change.Call})
	if change.Message != nil {
		s.broadcastToChat(ctx, chatID, messageEvent{Type: "message.new", ChatID: chatID, Message: *change.Message})
	}
	if !change.Call.Live() && s.livekit != nil {
		go func(room string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.livekit.CloseRoom(ctx, room); err != nil {
				s.log.Warn("close livekit room failed", "room", room, "err", err)
			}
		}(change.Call.ID)
	}
}

func (s *Server) RunCallJanitor(ctx context.Context) {
	t := time.NewTicker(janitorEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepCalls(ctx)
		}
	}
}

func (s *Server) sweepCalls(ctx context.Context) {
	stale, err := s.store.StaleRingingCalls(ctx, s.ringTimeout)
	if err != nil {
		s.log.Error("load stale calls failed", "err", err)
		return
	}
	for _, id := range stale {
		change, err := s.store.EndRoom(ctx, id)
		if err != nil {
			s.log.Error("end stale call failed", "call_id", id, "err", err)
			continue
		}
		s.publishCallChange(ctx, change, true)
	}

	if s.livekit == nil {
		return
	}
	ids, err := s.store.ActiveCallsToCheck(ctx, s.callJoinGrace)
	if err != nil {
		s.log.Error("load active calls failed", "err", err)
		return
	}
	for _, id := range ids {
		present, err := s.livekit.Participants(ctx, id)
		if err != nil {
			s.log.Warn("livekit participants failed", "call_id", id, "err", err)
			continue
		}
		call, changed, err := s.store.SyncCallParticipants(ctx, id, present)
		if err != nil {
			s.log.Error("sync call participants failed", "call_id", id, "err", err)
			continue
		}
		if !call.Live() {
			continue
		}
		over := len(call.Participants) == 0 ||
			(call.ChatKind == store.KindDirect && call.Status == store.CallActive && len(call.Participants) < 2)
		if over {
			change, err := s.store.EndRoom(ctx, id)
			if err != nil {
				s.log.Error("end call failed", "call_id", id, "err", err)
				continue
			}
			s.publishCallChange(ctx, change, true)
			continue
		}
		if changed {
			s.broadcastToChat(ctx, call.ChatID, callEvent{Type: "call.updated", ChatID: call.ChatID, Call: call})
		}
	}
}
