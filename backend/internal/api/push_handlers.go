package api

import (
	"context"
	"net/http"
	"time"

	"connecta/internal/push"
	"connecta/internal/store"
)

func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.push.PublicKey()})
}

type subscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	var req subscriptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.push.ValidateSubscription(req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		writeValidation(w, map[string]string{"subscription": "invalid"})
		return
	}
	if !s.allow(w, r, "push-sub:"+claims.Subject, 20, time.Hour) {
		return
	}
	if err := s.store.SavePushSubscription(r.Context(), claims.Subject, claims.SessionID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.DeletePushSubscription(r.Context(), claimsFrom(r.Context()).Subject, req.Endpoint); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	me := claimsFrom(r.Context()).Subject
	chatID := chatIDFrom(r.Context())
	var req struct {
		Muted bool `json:"muted"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.SetMuted(r.Context(), chatID, me, req.Muted); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.publish(r.Context(), []string{me}, chatEvent{Type: "chat.updated", ChatID: chatID})
	w.WriteHeader(http.StatusNoContent)
}

func attachmentSummary(m store.Message) string {
	if len(m.Attachments) == 0 {
		return ""
	}
	first := m.Attachments[0]
	switch {
	case first.Kind == "voice":
		return "🎤 Голосовое сообщение"
	case first.Kind == "image":
		return "🖼 Фото"
	default:
		return "📎 " + first.Name
	}
}

func (s *Server) notifyPush(ctx context.Context, chatID string, msg store.Message) {
	kind, title, err := s.store.ChatTitle(ctx, chatID)
	if err != nil {
		s.log.Warn("push title lookup failed", "err", err)
		return
	}
	text := msg.Body
	if text == "" {
		text = attachmentSummary(msg)
	}
	if r := []rune(text); len(r) > 140 {
		text = string(r[:140]) + "…"
	}
	payload := push.Payload{Type: "message", ChatID: chatID, URL: "/chat/" + chatID, Tag: "chat-" + chatID, Body: text}
	switch kind {
	case store.KindDirect:
		payload.Title = msg.Sender.DisplayName
	case store.KindGroup:
		payload.Title = title
		payload.Body = msg.Sender.DisplayName + ": " + text
	default:
		payload.Title = title
	}
	s.push.NotifyMessage(chatID, msg.SenderID, payload)
}
