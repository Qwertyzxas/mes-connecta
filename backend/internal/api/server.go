package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"

	"connecta/internal/auth"
	"connecta/internal/calls"
	"connecta/internal/config"
	"connecta/internal/files"
	"connecta/internal/push"
	"connecta/internal/ratelimit"
	"connecta/internal/realtime"
	"connecta/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	tokens   *auth.Tokens
	limiter  *ratelimit.Limiter
	rdb      *redis.Client
	hub      *realtime.Hub
	presence *realtime.Presence
	files    *files.Storage
	signer   *files.Signer
	push     *push.Sender
	livekit  *calls.LiveKit
	log      *slog.Logger
	now      func() time.Time

	refreshGrace  time.Duration
	ringTimeout   time.Duration
	callJoinGrace time.Duration
}

func New(cfg config.Config, st *store.Store, tokens *auth.Tokens, limiter *ratelimit.Limiter, rdb *redis.Client, hub *realtime.Hub, storage *files.Storage, pusher *push.Sender, livekit *calls.LiveKit, log *slog.Logger) *Server {
	return &Server{
		cfg:      cfg,
		store:    st,
		tokens:   tokens,
		limiter:  limiter,
		rdb:      rdb,
		hub:      hub,
		presence: realtime.NewPresence(rdb),
		files:    storage,
		signer:   files.NewSigner(cfg.JWTSecret, 24*time.Hour),
		push:     pusher,
		livekit:  livekit,
		log:      log,
		now:      time.Now,

		refreshGrace:  10 * time.Second,
		ringTimeout:   45 * time.Second,
		callJoinGrace: 30 * time.Second,
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.realIP)
	r.Use(s.logRequests)
	r.Use(middleware.Recoverer)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", s.handleRegister)
			r.Post("/login", s.handleLogin)
			r.Post("/refresh", s.handleRefresh)
			r.Post("/logout", s.handleLogout)
		})

		r.Get("/ws", s.handleWebSocket)
		r.Get("/files/{fileID}", s.handleFile)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/me", s.handleMe)
			r.Get("/sessions", s.handleListSessions)
			r.Delete("/sessions/{id}", s.handleRevokeSession)

			r.Post("/ws/ticket", s.handleWSTicket)
			r.Get("/files/{fileID}/url", s.handleFileURL)
			r.Get("/push/key", s.handlePushKey)
			r.Post("/push/subscriptions", s.handleSubscribe)
			r.Delete("/push/subscriptions", s.handleUnsubscribe)
			r.Get("/users/search", s.handleSearchUsers)
			r.Get("/search", s.handleSearch)
			r.Post("/channels/{chatID}/join", s.handleJoin)
			r.Get("/calls", s.handleListCalls)
			r.Post("/calls/{callID}/join", s.handleJoinCall)
			r.Post("/calls/{callID}/leave", s.handleLeaveCall)
			r.Post("/calls/{callID}/decline", s.handleDeclineCall)

			r.Route("/chats", func(r chi.Router) {
				r.Get("/", s.handleListChats)
				r.Post("/direct", s.handleOpenDirect)
				r.Post("/group", s.handleCreateGroup)
				r.Post("/channel", s.handleCreateChannel)
				r.Post("/secret", s.handleCreateSecret)
				r.Route("/{chatID}", func(r chi.Router) {
					r.Use(s.requireMember)
					r.Get("/", s.handleGetChat)
					r.Patch("/", s.handleUpdateChat)
					r.Post("/leave", s.handleLeave)
					r.Post("/accept", s.handleAcceptSecret)
					r.Get("/members", s.handleMembers)
					r.Post("/members", s.handleAddMembers)
					r.Patch("/members/{userID}", s.handleSetRole)
					r.Delete("/members/{userID}", s.handleRemoveMember)
					r.Get("/messages", s.handleListMessages)
					r.Post("/messages", s.handleSendMessage)
					r.Patch("/messages/{messageID}", s.handleEditMessage)
					r.Delete("/messages/{messageID}", s.handleDeleteMessage)
					r.Post("/read", s.handleMarkRead)
					r.Post("/attachments", s.handleUpload)
					r.Post("/mute", s.handleMute)
					r.Post("/calls", s.handleStartCall)
				})
			})
		})
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "database unavailable")
		return
	}
	if err := s.rdb.Ping(r.Context()).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis_unavailable", "redis unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
