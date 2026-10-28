package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"connecta/internal/store"
)

func validPublicKey(key string) bool {
	raw, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(raw) == 65 && raw[0] == 0x04
}

func (s *Server) handleCreateSecret(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	var req struct {
		UserID    string `json:"user_id"`
		PublicKey string `json:"public_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.UserID = strings.ToLower(strings.TrimSpace(req.UserID))
	fields := map[string]string{}
	if !uuidRe.MatchString(req.UserID) {
		fields["user_id"] = "invalid"
	} else if req.UserID == claims.Subject {
		fields["user_id"] = "self"
	}
	if !validPublicKey(req.PublicKey) {
		fields["public_key"] = "invalid"
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}
	if !s.allow(w, r, "secret:"+claims.Subject, 20, time.Hour) {
		return
	}
	if _, err := s.store.UserByID(r.Context(), req.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user_not_found", "user not found")
			return
		}
		s.internalError(w, r, err)
		return
	}

	chatID, err := s.store.CreateSecret(r.Context(), claims.Subject, claims.SessionID, req.UserID, req.PublicKey)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.publishTargets(r.Context(), []store.Target{{UserID: req.UserID}}, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.respondChat(w, r, claims.Subject, chatID, http.StatusCreated)
}

func (s *Server) handleAcceptSecret(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	chatID := chatIDFrom(r.Context())
	if membershipFrom(r.Context()).Kind != store.KindSecret {
		writeError(w, http.StatusUnprocessableEntity, "not_secret", "chat is not a secret chat")
		return
	}
	var req struct {
		PublicKey string `json:"public_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validPublicKey(req.PublicKey) {
		writeValidation(w, map[string]string{"public_key": "invalid"})
		return
	}
	err := s.store.AcceptSecret(r.Context(), chatID, claims.Subject, claims.SessionID, req.PublicKey)
	if errors.Is(err, store.ErrSecretState) {
		writeError(w, http.StatusConflict, "secret_state", "secret chat cannot be accepted")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	targets, err := s.store.ChatTargets(r.Context(), chatID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var others []store.Target
	for _, t := range targets {
		if t.UserID != claims.Subject {
			others = append(others, t)
		}
	}
	s.publishTargets(r.Context(), others, chatEvent{Type: "chat.updated", ChatID: chatID})
	s.publishExcept(r.Context(), claims.Subject, claims.SessionID, chatEvent{Type: "chat.removed", ChatID: chatID})
	s.respondChat(w, r, claims.Subject, chatID, http.StatusOK)
}

func (s *Server) closeSecret(ctx context.Context, chatID string) error {
	targets, keys, err := s.store.DeleteSecret(ctx, chatID)
	if err != nil {
		return err
	}
	s.removeObjects(keys)
	s.publishTargets(ctx, targets, chatEvent{Type: "chat.removed", ChatID: chatID})
	return nil
}
