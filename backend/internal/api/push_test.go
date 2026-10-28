package api

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pushServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	status   int
	got      chan string
}

func newPushServer(t *testing.T) *pushServer {
	t.Helper()
	ps := &pushServer{status: http.StatusCreated, got: make(chan string, 16)}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.requests = append(ps.requests, r)
		status := ps.status
		ps.mu.Unlock()
		w.WriteHeader(status)
		ps.got <- r.URL.Path
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushServer) expect(t *testing.T, path string) *http.Request {
	t.Helper()
	select {
	case got := <-ps.got:
		if got != path {
			t.Fatalf("push went to %s, want %s", got, path)
		}
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return ps.requests[len(ps.requests)-1]
	case <-time.After(5 * time.Second):
		t.Fatalf("no push delivered to %s", path)
		return nil
	}
}

func (ps *pushServer) expectNone(t *testing.T) {
	t.Helper()
	select {
	case got := <-ps.got:
		t.Fatalf("unexpected push to %s", got)
	case <-time.After(700 * time.Millisecond):
	}
}

func clientKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(secret)
}

func (c *client) subscribe(endpoint string) response {
	c.t.Helper()
	p256dh, auth := clientKeys(c.t)
	return c.do(http.MethodPost, "/api/push/subscriptions", map[string]any{
		"endpoint": endpoint,
		"keys":     map[string]string{"p256dh": p256dh, "auth": auth},
	})
}

func TestPushKeyIsStable(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	res := c.do(http.MethodGet, "/api/push/key", nil)
	expectStatus(t, res, http.StatusOK)
	var first struct {
		PublicKey string `json:"public_key"`
	}
	res.json(t, &first)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(first.PublicKey, "="))
	if err != nil || len(raw) != 65 {
		t.Fatalf("public key = %q", first.PublicKey)
	}
}

func TestPushDeliveredToOfflineRecipients(t *testing.T) {
	e := setup(t)
	ps := newPushServer(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	expectStatus(t, bob.subscribe(ps.URL+"/bob"), http.StatusNoContent)
	expectStatus(t, alice.subscribe(ps.URL+"/alice"), http.StatusNoContent)

	alice.send(chat.ID, "ты тут?")
	req := ps.expect(t, "/bob")
	if req.Header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(req.Header.Get("Authorization"), "vapid t=") ||
		req.Header.Get("TTL") == "" || req.Header.Get("Urgency") != "high" {
		t.Fatalf("push headers = %v", req.Header)
	}
	ps.expectNone(t)

	bobConn := bob.connect()
	alice.send(chat.ID, "вижу, ты онлайн")
	waitEvent(t, bobConn, "message.new", nil)
	ps.expectNone(t)
}

func TestMutedChatsDoNotPush(t *testing.T) {
	e := setup(t)
	ps := newPushServer(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	expectStatus(t, bob.subscribe(ps.URL+"/bob"), http.StatusNoContent)

	expectStatus(t, bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/mute", map[string]bool{"muted": true}), http.StatusNoContent)
	if !bob.chat(chat.ID).Muted {
		t.Fatal("chat is not marked muted")
	}
	alice.send(chat.ID, "тихо")
	ps.expectNone(t)

	expectStatus(t, bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/mute", map[string]bool{"muted": false}), http.StatusNoContent)
	alice.send(chat.ID, "громко")
	ps.expect(t, "/bob")
}

func TestGoneSubscriptionsAreDropped(t *testing.T) {
	e := setup(t)
	ps := newPushServer(t)
	ps.status = http.StatusGone
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	expectStatus(t, bob.subscribe(ps.URL+"/gone"), http.StatusNoContent)

	alice.send(chat.ID, "1")
	ps.expect(t, "/gone")
	time.Sleep(200 * time.Millisecond)
	alice.send(chat.ID, "2")
	ps.expectNone(t)
}

func TestLogoutStopsPush(t *testing.T) {
	e := setup(t)
	ps := newPushServer(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	expectStatus(t, bob.subscribe(ps.URL+"/bob"), http.StatusNoContent)
	expectStatus(t, bob.do(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent)

	alice.send(chat.ID, "ты вышел?")
	ps.expectNone(t)
}

func TestSubscriptionValidation(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	_, auth := clientKeys(t)

	bad := []map[string]any{
		{"endpoint": "ftp://push.example/x", "keys": map[string]string{"p256dh": "x", "auth": auth}},
		{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": "AAAA", "auth": auth}},
		{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(make([]byte, 65)), "auth": auth}},
	}
	for _, body := range bad {
		expectError(t, c.do(http.MethodPost, "/api/push/subscriptions", body), http.StatusUnprocessableEntity, "validation_failed")
	}
}
