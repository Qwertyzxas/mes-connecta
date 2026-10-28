package api

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"connecta/internal/auth"
)

type ctxKey int

const claimsKey ctxKey = iota

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}

		claims, err := s.tokens.Parse(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
			return
		}

		revoked, err := s.rdb.Exists(r.Context(), revokedKey(claims.SessionID)).Result()
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if revoked > 0 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session revoked")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	})
}

func claimsFrom(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(claimsKey).(*auth.Claims)
	return claims
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

func (s *Server) allow(w http.ResponseWriter, r *http.Request, key string, limit int64, window time.Duration) bool {
	ok, retry, err := s.limiter.Allow(r.Context(), key, limit, window)
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return false
	}
	return true
}

func (s *Server) realIP(next http.Handler) http.Handler {
	if !s.cfg.TrustProxy {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			r.RemoteAddr = ip.String()
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return strings.ToValidUTF8(ua, "")
}

func revokedKey(sessionID string) string {
	return "revoked:" + sessionID
}
