package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type wsEvent map[string]any

func (c *client) wsTicket() string {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/ws/ticket", nil)
	expectStatus(c.t, res, http.StatusOK)
	var out struct {
		Ticket string `json:"ticket"`
	}
	res.json(c.t, &out)
	if out.Ticket == "" {
		c.t.Fatal("empty ticket")
	}
	return out.Ticket
}

func (c *client) wsURL(ticket string) string {
	return "ws" + strings.TrimPrefix(c.base, "http") + "/api/ws?ticket=" + ticket
}

func (c *client) connect() *websocket.Conn {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, c.wsURL(c.wsTicket()), nil)
	if err != nil {
		c.t.Fatalf("ws dial: %v", err)
	}
	c.t.Cleanup(func() { _ = conn.CloseNow() })
	waitEvent(c.t, conn, "ready", nil)
	return conn
}

func waitEvent(t *testing.T, conn *websocket.Conn, eventType string, match func(wsEvent) bool) wsEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var ev wsEvent
		if err := wsjson.Read(ctx, conn, &ev); err != nil {
			t.Fatalf("waiting for %q: %v", eventType, err)
		}
		if ev["type"] == eventType && (match == nil || match(ev)) {
			return ev
		}
	}
}

func TestWSRequiresValidTicket(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	ctx := context.Background()

	for _, ticket := range []string{"", "forged", strings.Repeat("x", 100)} {
		_, res, err := websocket.Dial(ctx, c.wsURL(ticket), nil)
		if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("ticket %q: err=%v res=%v, want 401", ticket, err, res)
		}
	}

	ticket := c.wsTicket()
	conn, _, err := websocket.Dial(ctx, c.wsURL(ticket), nil)
	if err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	_, res, err := websocket.Dial(ctx, c.wsURL(ticket), nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket reuse: err=%v, want 401", err)
	}

	expectError(t, e.client(t).do(http.MethodPost, "/api/ws/ticket", nil), http.StatusUnauthorized, "unauthorized")
}

func TestWSRejectsCrossOrigin(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	_, res, err := websocket.Dial(context.Background(), c.wsURL(c.wsTicket()), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin upgrade: err=%v, want 403", err)
	}
}

func TestWSDeliversMessagesToAllDevices(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	bobConn := bob.connect()
	aliceConn := alice.connect()

	sent := alice.send(chat.ID, "Привет, Боб")

	for _, conn := range []*websocket.Conn{bobConn, aliceConn} {
		ev := waitEvent(t, conn, "message.new", nil)
		msg, _ := ev["message"].(map[string]any)
		if ev["chat_id"] != chat.ID || msg["id"] != sent.ID || msg["body"] != "Привет, Боб" || msg["seq"] != float64(1) {
			t.Fatalf("unexpected event %v", ev)
		}
	}

	mallory := e.client(t)
	mallory.register(newCreds())
	malloryConn := mallory.connect()
	alice.send(chat.ID, "second")
	waitEvent(t, bobConn, "message.new", func(ev wsEvent) bool {
		return ev["message"].(map[string]any)["body"] == "second"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var leaked wsEvent
	if err := wsjson.Read(ctx, malloryConn, &leaked); err == nil {
		t.Fatalf("stranger received event %v", leaked)
	}
}

func TestWSTypingAndReadEvents(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	msg := alice.send(chat.ID, "are you there?")

	aliceConn := alice.connect()
	bobConn := bob.connect()

	ctx := context.Background()
	if err := wsjson.Write(ctx, bobConn, map[string]string{"type": "typing", "chat_id": chat.ID}); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, aliceConn, "typing", nil)
	if ev["chat_id"] != chat.ID || ev["user_id"] != bob.user.ID {
		t.Fatalf("typing event = %v", ev)
	}

	expectStatus(t, bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/read", map[string]int64{"seq": msg.Seq}), http.StatusNoContent)
	ev = waitEvent(t, aliceConn, "chat.read", nil)
	if ev["chat_id"] != chat.ID || ev["user_id"] != bob.user.ID || ev["seq"] != float64(msg.Seq) {
		t.Fatalf("read event = %v", ev)
	}
}

func TestWSPresence(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	aliceConn := alice.connect()

	if alice.chat(chat.ID).Peer.Online {
		t.Fatal("bob is online before connecting")
	}

	bobConn := bob.connect()
	ev := waitEvent(t, aliceConn, "presence", func(ev wsEvent) bool { return ev["user_id"] == bob.user.ID })
	if ev["online"] != true {
		t.Fatalf("presence event = %v, want online", ev)
	}
	if !alice.chat(chat.ID).Peer.Online {
		t.Fatal("chat does not show bob online")
	}

	_ = bobConn.Close(websocket.StatusNormalClosure, "")
	ev = waitEvent(t, aliceConn, "presence", func(ev wsEvent) bool { return ev["user_id"] == bob.user.ID })
	if ev["online"] != false || ev["last_seen_at"] == nil {
		t.Fatalf("presence event = %v, want offline with last_seen_at", ev)
	}
	peer := alice.chat(chat.ID).Peer
	if peer.Online || peer.LastSeenAt == nil {
		t.Fatalf("peer after disconnect = %+v", peer)
	}
}

func TestWSClosedWhenSessionEnds(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	conn := c.connect()

	expectStatus(t, c.do(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var ev wsEvent
		err := wsjson.Read(ctx, conn, &ev)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("socket closed with %v, want policy violation", err)
		}
		break
	}

	_, res, err := websocket.Dial(context.Background(), c.wsURL("x"), nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial after logout: %v", err)
	}
}

func TestWSTicketOfRevokedSessionRejected(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	ticket := c.wsTicket()
	expectStatus(t, c.do(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent)

	_, res, err := websocket.Dial(context.Background(), c.wsURL(ticket), nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session ticket: err=%v", err)
	}
}

func TestWSTypingIsRateLimited(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	aliceConn := alice.connect()
	bobConn := bob.connect()
	waitEvent(t, aliceConn, "presence", nil)

	for i := 0; i < 20; i++ {
		if err := wsjson.Write(context.Background(), bobConn, map[string]string{"type": "typing", "chat_id": chat.ID}); err != nil {
			t.Fatal(err)
		}
	}

	received := 0
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	for {
		var ev wsEvent
		if err := wsjson.Read(ctx, aliceConn, &ev); err != nil {
			break
		}
		if ev["type"] == "typing" {
			received++
		}
	}
	if received < 1 || received > typingBurst+1 {
		t.Fatalf("typing events delivered = %d, want between 1 and %d", received, typingBurst+1)
	}
}
