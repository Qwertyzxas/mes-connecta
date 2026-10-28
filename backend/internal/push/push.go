package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"connecta/internal/store"
)

var allowedHosts = []string{
	"fcm.googleapis.com",
	"push.services.mozilla.com",
	"push.apple.com",
	"notify.windows.com",
}

var ErrBadSubscription = errors.New("invalid push subscription")

type Payload struct {
	Type   string `json:"type"`
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	Tag    string `json:"tag"`
}

type job struct {
	chatID   string
	senderID string
	payload  Payload
}

type OnlineFunc func(ctx context.Context, userIDs []string) (map[string]bool, error)

type Sender struct {
	store         *store.Store
	log           *slog.Logger
	publicKey     string
	privateKey    string
	subject       string
	allowInsecure bool
	online        OnlineFunc
	client        *http.Client
	jobs          chan job
}

type Config struct {
	Subject       string
	AllowInsecure bool
	Workers       int
}

func New(ctx context.Context, st *store.Store, log *slog.Logger, online OnlineFunc, cfg Config) (*Sender, error) {
	pair, err := st.ServerKey(ctx, "vapid", func() (string, error) {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return "", err
		}
		return pub + ":" + priv, nil
	})
	if err != nil {
		return nil, err
	}
	pub, priv, ok := strings.Cut(pair, ":")
	if !ok {
		return nil, errors.New("corrupt vapid key pair")
	}
	s := &Sender{
		store:         st,
		log:           log,
		publicKey:     pub,
		privateKey:    priv,
		subject:       cfg.Subject,
		allowInsecure: cfg.AllowInsecure,
		online:        online,
		client:        &http.Client{Timeout: 15 * time.Second},
		jobs:          make(chan job, 1024),
	}
	workers := cfg.Workers
	if workers < 1 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		go s.work(ctx)
	}
	return s, nil
}

func (s *Sender) PublicKey() string { return s.publicKey }

func (s *Sender) ValidateSubscription(endpoint, p256dh, auth string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || len(endpoint) > 1024 {
		return ErrBadSubscription
	}
	if !s.allowInsecure {
		if u.Scheme != "https" {
			return ErrBadSubscription
		}
		host := strings.ToLower(u.Hostname())
		allowed := false
		for _, h := range allowedHosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrBadSubscription
		}
	} else if u.Scheme != "https" && u.Scheme != "http" {
		return ErrBadSubscription
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(p256dh, "="))
	if err != nil || len(key) != 65 || key[0] != 4 {
		return ErrBadSubscription
	}
	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(auth, "="))
	if err != nil || len(secret) != 16 {
		return ErrBadSubscription
	}
	return nil
}

func (s *Sender) NotifyMessage(chatID, senderID string, payload Payload) {
	select {
	case s.jobs <- job{chatID: chatID, senderID: senderID, payload: payload}:
	default:
		s.log.Warn("push queue full, dropping notification", "chat_id", chatID)
	}
}

func (s *Sender) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			s.deliver(ctx, j)
		}
	}
}

func (s *Sender) deliver(ctx context.Context, j job) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	subs, err := s.store.PushTargets(ctx, j.chatID, j.senderID)
	if err != nil {
		s.log.Error("load push targets failed", "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	users := make([]string, 0, len(subs))
	for _, sub := range subs {
		users = append(users, sub.UserID)
	}
	online, err := s.online(ctx, users)
	if err != nil {
		online = map[string]bool{}
	}
	body, err := json.Marshal(j.payload)
	if err != nil {
		return
	}

	for _, sub := range subs {
		if online[sub.UserID] {
			continue
		}
		res, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{Auth: sub.Auth, P256dh: sub.P256dh},
		}, &webpush.Options{
			HTTPClient:      s.client,
			Subscriber:      s.subject,
			VAPIDPublicKey:  s.publicKey,
			VAPIDPrivateKey: s.privateKey,
			TTL:             24 * 60 * 60,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			s.log.Warn("push send failed", "err", err)
			continue
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusGone || res.StatusCode == http.StatusNotFound {
			if err := s.store.DeletePushSubscriptionByID(ctx, sub.ID); err != nil {
				s.log.Warn("delete stale subscription failed", "err", err)
			}
		}
	}
}
