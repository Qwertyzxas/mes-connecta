package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"connecta/internal/auth"
	"connecta/internal/store"
)

const (
	refreshCookie = "refresh_token"
	refreshPath   = "/api/auth"
)

type registerRequest struct {
	Email       string `json:"email"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	AccessToken string     `json:"access_token"`
	ExpiresAt   time.Time  `json:"expires_at"`
	User        store.User `json:"user"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.allow(w, r, "register:ip:"+clientIP(r), 10, time.Hour) {
		return
	}

	req.Email = normalizeEmail(req.Email)
	req.Username = normalizeUsername(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}

	fields := map[string]string{}
	if !validEmail(req.Email) {
		fields["email"] = "invalid"
	}
	if !usernameRe.MatchString(req.Username) {
		fields["username"] = "invalid"
	}
	if !utf8.ValidString(req.DisplayName) || utf8.RuneCountInString(req.DisplayName) > maxDisplayNameLen {
		fields["display_name"] = "too_long"
	}
	if p := passwordProblem(req.Password); p != "" {
		fields["password"] = p
	}
	if len(fields) > 0 {
		writeValidation(w, fields)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	user, err := s.store.CreateUser(r.Context(), req.Email, req.Username, req.DisplayName, hash)
	switch {
	case errors.Is(err, store.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "email already registered")
		return
	case errors.Is(err, store.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "username_taken", "username already taken")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	s.startSession(w, r, user, http.StatusCreated)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Email = normalizeEmail(req.Email)

	if !s.allow(w, r, "login:ip:"+clientIP(r), 30, 15*time.Minute) {
		return
	}
	if !s.allow(w, r, "login:email:"+req.Email, 10, 15*time.Minute) {
		return
	}

	if req.Email == "" || req.Password == "" || len(req.Password) > 4*maxPasswordLen {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	user, err := s.store.UserByEmail(r.Context(), req.Email)
	if errors.Is(err, store.ErrNotFound) {
		auth.EqualizeTiming(req.Password)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	s.startSession(w, r, user.User, http.StatusOK)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "no_session", "no refresh token")
		return
	}
	if !s.allow(w, r, "refresh:ip:"+clientIP(r), 120, 15*time.Minute) {
		return
	}

	oldHash := auth.HashRefreshToken(cookie.Value)
	newToken, newHash, err := auth.NewRefreshToken()
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	sess, err := s.store.RotateSession(r.Context(), oldHash, newHash, s.now().Add(s.cfg.RefreshTTL))
	if errors.Is(err, store.ErrNotFound) {
		s.handleStaleRefresh(w, r, oldHash)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	user, err := s.store.UserByID(r.Context(), sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		s.clearRefreshCookie(w)
		writeError(w, http.StatusUnauthorized, "session_expired", "session expired")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	s.respondWithTokens(w, r, user, sess.ID, newToken, sess.ExpiresAt, http.StatusOK)
}

func (s *Server) handleStaleRefresh(w http.ResponseWriter, r *http.Request, hash []byte) {
	prev, recent, err := s.store.SessionByPrevHash(r.Context(), hash, s.refreshGrace)
	switch {
	case err == nil && recent:
		writeError(w, http.StatusConflict, "refresh_conflict", "token was just rotated, retry")
		return
	case err == nil:
		s.log.Warn("refresh token reuse detected, revoking session",
			"session_id", prev.ID,
			"user_id", prev.UserID,
			"ip", clientIP(r),
		)
		if err := s.revokeSession(r.Context(), prev.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	case !errors.Is(err, store.ErrNotFound):
		s.internalError(w, r, err)
		return
	}

	s.clearRefreshCookie(w)
	writeError(w, http.StatusUnauthorized, "session_expired", "session expired")
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookie); err == nil && cookie.Value != "" {
		sess, err := s.store.RevokeSessionByHash(r.Context(), auth.HashRefreshToken(cookie.Value))
		switch {
		case err == nil:
			s.markRevoked(r.Context(), sess.ID)
		case !errors.Is(err, store.ErrNotFound):
			s.internalError(w, r, err)
			return
		}
	}
	s.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user store.User, status int) {
	token, hash, err := auth.NewRefreshToken()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sess, err := s.store.CreateSession(r.Context(), user.ID, hash, userAgent(r), clientIP(r), s.now().Add(s.cfg.RefreshTTL))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.respondWithTokens(w, r, user, sess.ID, token, sess.ExpiresAt, status)
}

func (s *Server) respondWithTokens(w http.ResponseWriter, r *http.Request, user store.User, sessionID, refresh string, refreshExpires time.Time, status int) {
	access, exp, err := s.tokens.Issue(user.ID, sessionID, s.now())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    refresh,
		Path:     refreshPath,
		Expires:  refreshExpires,
		MaxAge:   int(time.Until(refreshExpires).Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, authResponse{AccessToken: access, ExpiresAt: exp, User: user})
}

func (s *Server) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    "",
		Path:     refreshPath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) revokeSession(ctx context.Context, sessionID string) error {
	if err := s.store.RevokeSession(ctx, sessionID); err != nil {
		return err
	}
	s.markRevoked(ctx, sessionID)
	return nil
}

func (s *Server) markRevoked(ctx context.Context, sessionID string) {
	if err := s.rdb.Set(ctx, revokedKey(sessionID), 1, s.cfg.AccessTTL+time.Minute).Err(); err != nil {
		s.log.Error("failed to mark session revoked", "session_id", sessionID, "err", err)
	}
}
