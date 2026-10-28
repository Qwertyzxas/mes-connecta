package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

const (
	userChannelPrefix = "rt:user:"
	controlChannel    = "rt:control"
	sendBuffer        = 64
)

type envelope struct {
	Kind      string          `json:"k"`
	SessionID string          `json:"sid,omitempty"`
	Only      []string        `json:"only,omitempty"`
	Except    string          `json:"not,omitempty"`
	Event     json.RawMessage `json:"e,omitempty"`
}

type Target struct {
	UserID    string
	SessionID string
	Except    string
}

type Client struct {
	UserID    string
	SessionID string
	ConnID    string

	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func NewClient(userID, sessionID, connID string) *Client {
	return &Client{
		UserID:    userID,
		SessionID: sessionID,
		ConnID:    connID,
		send:      make(chan []byte, sendBuffer),
		done:      make(chan struct{}),
	}
}

func (c *Client) Send() <-chan []byte { return c.send }

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.done) })
}

func (c *Client) enqueue(payload []byte) {
	select {
	case <-c.done:
	case c.send <- payload:
	default:
		c.Close()
	}
}

type Hub struct {
	rdb *redis.Client
	ps  *redis.PubSub
	log *slog.Logger

	mu      sync.Mutex
	clients map[string]map[*Client]struct{}
}

func NewHub(ctx context.Context, rdb *redis.Client, log *slog.Logger) (*Hub, error) {
	ps := rdb.Subscribe(ctx, controlChannel)
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return nil, err
	}
	h := &Hub{rdb: rdb, ps: ps, log: log, clients: map[string]map[*Client]struct{}{}}
	go h.run()
	return h, nil
}

func (h *Hub) Close() error {
	return h.ps.Close()
}

func (h *Hub) Register(ctx context.Context, c *Client) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.clients[c.UserID]
	if !ok {
		if err := h.ps.Subscribe(ctx, userChannelPrefix+c.UserID); err != nil {
			return err
		}
		set = map[*Client]struct{}{}
		h.clients[c.UserID] = set
	}
	set[c] = struct{}{}
	return nil
}

func (h *Hub) Unregister(ctx context.Context, c *Client) {
	c.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.clients[c.UserID]
	if !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.clients, c.UserID)
		if err := h.ps.Unsubscribe(ctx, userChannelPrefix+c.UserID); err != nil {
			h.log.Warn("realtime unsubscribe failed", "user_id", c.UserID, "err", err)
		}
	}
}

func (h *Hub) Publish(ctx context.Context, userIDs []string, event any) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(envelope{Kind: "event", Event: raw})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	_, err = h.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, id := range userIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			p.Publish(ctx, userChannelPrefix+id, payload)
		}
		return nil
	})
	return err
}

func (h *Hub) PublishTargets(ctx context.Context, targets []Target, event any) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	type route struct {
		all    bool
		only   []string
		except string
	}
	routes := map[string]*route{}
	order := []string{}
	for _, t := range targets {
		r, ok := routes[t.UserID]
		if !ok {
			r = &route{}
			routes[t.UserID] = r
			order = append(order, t.UserID)
		}
		switch {
		case t.SessionID != "":
			r.only = append(r.only, t.SessionID)
		case t.Except != "":
			r.except = t.Except
		default:
			r.all = true
		}
	}
	_, err = h.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, userID := range order {
			r := routes[userID]
			env := envelope{Kind: "event", Event: raw}
			if !r.all {
				env.Only = r.only
				env.Except = r.except
			}
			payload, err := json.Marshal(env)
			if err != nil {
				return err
			}
			p.Publish(ctx, userChannelPrefix+userID, payload)
		}
		return nil
	})
	return err
}

func (e envelope) accepts(sessionID string) bool {
	if e.Except != "" {
		return sessionID != e.Except
	}
	if len(e.Only) == 0 {
		return true
	}
	for _, s := range e.Only {
		if s == sessionID {
			return true
		}
	}
	return false
}

func (h *Hub) RevokeSession(ctx context.Context, userID, sessionID string) error {
	payload, err := json.Marshal(envelope{Kind: "revoke", SessionID: sessionID})
	if err != nil {
		return err
	}
	return h.rdb.Publish(ctx, userChannelPrefix+userID, payload).Err()
}

func (h *Hub) run() {
	for msg := range h.ps.Channel() {
		userID, ok := strings.CutPrefix(msg.Channel, userChannelPrefix)
		if !ok {
			continue
		}
		var env envelope
		if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
			h.log.Warn("realtime bad envelope", "err", err)
			continue
		}

		h.mu.Lock()
		targets := make([]*Client, 0, len(h.clients[userID]))
		for c := range h.clients[userID] {
			targets = append(targets, c)
		}
		h.mu.Unlock()

		for _, c := range targets {
			switch env.Kind {
			case "event":
				if env.accepts(c.SessionID) {
					c.enqueue(env.Event)
				}
			case "revoke":
				if c.SessionID == env.SessionID {
					c.Close()
				}
			}
		}
	}
}
