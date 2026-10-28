package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"connecta/internal/realtime"
)

const (
	wsTicketTTL     = 30 * time.Second
	wsReadLimit     = 4 << 10
	wsWriteTimeout  = 10 * time.Second
	wsPingInterval  = 25 * time.Second
	typingThrottle  = 2 * time.Second
	ticketKeyPrefix = "rt:ticket:"
)

type typingEvent struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id"`
	UserID string `json:"user_id"`
}

type presenceEvent struct {
	Type       string     `json:"type"`
	UserID     string     `json:"user_id"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

type readyEvent struct {
	Type string `json:"type"`
}

type clientMessage struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id"`
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if !s.allow(w, r, "wsticket:"+claims.Subject, 30, time.Minute) {
		return
	}
	ticket, err := randomToken()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	value := claims.Subject + ":" + claims.SessionID
	if err := s.rdb.Set(r.Context(), ticketKeyPrefix+ticket, value, wsTicketTTL).Err(); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"ticket": ticket})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" || len(ticket) > 64 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing ticket")
		return
	}
	value, err := s.rdb.GetDel(r.Context(), ticketKeyPrefix+ticket).Result()
	if errors.Is(err, redis.Nil) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or used ticket")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	userID, sessionID, ok := strings.Cut(value, ":")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid ticket")
		return
	}
	revoked, err := s.rdb.Exists(r.Context(), revokedKey(sessionID)).Result()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if revoked > 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "session revoked")
		return
	}

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(wsReadLimit)

	connID, err := randomToken()
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := realtime.NewClient(userID, sessionID, connID)
	if err := s.hub.Register(ctx, client); err != nil {
		s.log.Error("realtime register failed", "err", err)
		_ = conn.Close(websocket.StatusTryAgainLater, "")
		return
	}
	wasOnline, _ := s.presence.IsOnline(ctx, userID)
	if err := s.presence.Touch(ctx, userID, connID); err != nil {
		s.log.Warn("presence touch failed", "err", err)
	}
	if !wasOnline {
		s.broadcastPresence(ctx, userID, true, nil)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.writeLoop(ctx, conn, client)
	}()

	s.readLoop(ctx, conn, client)

	s.hub.Unregister(ctx, client)
	cancel()
	wg.Wait()

	cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := s.presence.Leave(cleanup, userID, connID); err != nil {
		s.log.Warn("presence leave failed", "err", err)
	}
	if online, err := s.presence.IsOnline(cleanup, userID); err == nil && !online {
		now := s.now().UTC()
		if err := s.store.SetLastSeen(cleanup, userID, now); err != nil {
			s.log.Warn("set last seen failed", "err", err)
		}
		s.broadcastPresence(cleanup, userID, false, &now)
	}
}

func (s *Server) writeLoop(ctx context.Context, conn *websocket.Conn, client *realtime.Client) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	write := func(payload []byte) error {
		wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, payload)
	}

	ready, _ := json.Marshal(readyEvent{Type: "ready"})
	if err := write(ready); err != nil {
		client.Close()
		_ = conn.CloseNow()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			_ = conn.Close(websocket.StatusPolicyViolation, "session closed")
			return
		case payload := <-client.Send():
			if err := write(payload); err != nil {
				client.Close()
				_ = conn.CloseNow()
				return
			}
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				client.Close()
				_ = conn.CloseNow()
				return
			}
			if err := s.presence.Touch(ctx, client.UserID, client.ConnID); err != nil {
				s.log.Warn("presence touch failed", "err", err)
			}
		}
	}
}

func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn, client *realtime.Client) {
	members := map[string]bool{}
	lastTyping := map[string]time.Time{}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg clientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "typing":
			if !uuidRe.MatchString(msg.ChatID) {
				continue
			}
			if time.Since(lastTyping[msg.ChatID]) < typingThrottle {
				continue
			}
			isMember, cached := members[msg.ChatID]
			if !cached {
				ok, err := s.store.IsMember(ctx, msg.ChatID, client.UserID)
				if err != nil {
					continue
				}
				members[msg.ChatID], isMember = ok, ok
			}
			if !isMember {
				continue
			}
			lastTyping[msg.ChatID] = time.Now()
			ids, err := s.store.MemberIDs(ctx, msg.ChatID)
			if err != nil {
				continue
			}
			others := ids[:0]
			for _, id := range ids {
				if id != client.UserID {
					others = append(others, id)
				}
			}
			_ = s.hub.Publish(ctx, others, typingEvent{Type: "typing", ChatID: msg.ChatID, UserID: client.UserID})
		}
	}
}

func (s *Server) broadcastPresence(ctx context.Context, userID string, online bool, lastSeen *time.Time) {
	peers, err := s.store.PeerIDs(ctx, userID)
	if err != nil {
		s.log.Warn("load peers failed", "err", err)
		return
	}
	if len(peers) == 0 {
		return
	}
	if err := s.hub.Publish(ctx, peers, presenceEvent{Type: "presence", UserID: userID, Online: online, LastSeenAt: lastSeen}); err != nil {
		s.log.Warn("presence publish failed", "err", err)
	}
}
