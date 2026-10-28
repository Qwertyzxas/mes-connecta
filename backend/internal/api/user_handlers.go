package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"connecta/internal/store"
)

type sessionView struct {
	store.Session
	Current bool `json:"current"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	user, err := s.store.UserByID(r.Context(), claims.Subject)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "user not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	sessions, err := s.store.ListSessions(r.Context(), claims.Subject)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	views := make([]sessionView, 0, len(sessions))
	for _, sess := range sessions {
		views = append(views, sessionView{Session: sess, Current: sess.ID == claims.SessionID})
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	id := chi.URLParam(r, "id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	ok, err := s.store.RevokeUserSession(r.Context(), claims.Subject, id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	s.markRevoked(r.Context(), id)
	if id == claims.SessionID {
		s.clearRefreshCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}
