package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"connecta/internal/store"
)

func publicKey(t *testing.T) string {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

func cipherText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func (c *client) createSecret(peerID, key string) store.Chat {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/secret", map[string]string{"user_id": peerID, "public_key": key})
	expectStatus(c.t, res, http.StatusCreated)
	var chat store.Chat
	res.json(c.t, &chat)
	return chat
}

func (c *client) sendBody(chatID, body string, extra map[string]any) response {
	c.t.Helper()
	req := map[string]any{"client_id": newUUID(), "body": body}
	for k, v := range extra {
		req[k] = v
	}
	return c.do(http.MethodPost, "/api/chats/"+chatID+"/messages", req)
}

func (e *env) device(t *testing.T, cr creds) *client {
	t.Helper()
	c := e.client(t)
	c.login(cr)
	return c
}

func expectNoEvent(t *testing.T, conn *websocket.Conn, eventType string, match func(wsEvent) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	for {
		var ev wsEvent
		if err := wsjson.Read(ctx, conn, &ev); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return
			}
			t.Fatalf("read: %v", err)
		}
		if ev["type"] == eventType && (match == nil || match(ev)) {
			t.Fatalf("unexpected %s event: %v", eventType, ev)
		}
	}
}

func forChat(id string) func(wsEvent) bool {
	return func(ev wsEvent) bool { return ev["chat_id"] == id }
}

func chatIDs(list []store.Chat) map[string]store.Chat {
	out := map[string]store.Chat{}
	for _, c := range list {
		out[c.ID] = c
	}
	return out
}

func TestSecretChatLifecycle(t *testing.T) {
	e := setup(t)
	aliceCreds, bobCreds := newCreds(), newCreds()
	alice := e.client(t)
	alice.register(aliceCreds)
	bob := e.client(t)
	bob.register(bobCreds)
	bobPhone := e.device(t, bobCreds)
	aliceTablet := e.device(t, aliceCreds)
	bobConn := bob.connect()
	phoneConn := bobPhone.connect()

	keyA, keyB := publicKey(t), publicKey(t)
	chat := alice.createSecret(bob.user.ID, keyA)
	if chat.Kind != store.KindSecret || chat.SecretState != store.SecretPending || chat.MyKey != keyA || chat.PeerKey != "" ||
		chat.Peer == nil || chat.Peer.ID != bob.user.ID {
		t.Fatalf("created = %+v", chat)
	}
	waitEvent(t, bobConn, "chat.updated", forChat(chat.ID))
	waitEvent(t, phoneConn, "chat.updated", forChat(chat.ID))

	for _, device := range []*client{bob, bobPhone} {
		got, ok := chatIDs(device.chats())[chat.ID]
		if !ok || got.SecretState != store.SecretPending || got.PeerKey != keyA || got.MyKey != "" || got.Peer.ID != alice.user.ID {
			t.Fatalf("invitee sees %+v (ok=%v)", got, ok)
		}
	}
	if _, ok := chatIDs(aliceTablet.chats())[chat.ID]; ok {
		t.Fatal("secret chat leaked to another device of the initiator")
	}
	expectError(t, aliceTablet.do(http.MethodGet, "/api/chats/"+chat.ID, nil), http.StatusNotFound, "chat_not_found")
	expectError(t, alice.sendBody(chat.ID, cipherText(64), nil), http.StatusConflict, "secret_not_ready")
	expectError(t, bob.sendBody(chat.ID, cipherText(64), nil), http.StatusConflict, "secret_not_ready")
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+chat.ID+"/accept", map[string]string{"public_key": keyB}), http.StatusConflict, "secret_state")

	res := bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/accept", map[string]string{"public_key": keyB})
	expectStatus(t, res, http.StatusOK)
	var accepted store.Chat
	res.json(t, &accepted)
	if accepted.SecretState != store.SecretActive || accepted.MyKey != keyB || accepted.PeerKey != keyA {
		t.Fatalf("accepted = %+v", accepted)
	}
	waitEvent(t, phoneConn, "chat.removed", forChat(chat.ID))
	if _, ok := chatIDs(bobPhone.chats())[chat.ID]; ok {
		t.Fatal("secret chat still visible on the other device after accept")
	}
	expectError(t, bobPhone.do(http.MethodPost, "/api/chats/"+chat.ID+"/accept", map[string]string{"public_key": publicKey(t)}), http.StatusNotFound, "chat_not_found")
	if got := alice.chat(chat.ID); got.SecretState != store.SecretActive || got.PeerKey != keyB {
		t.Fatalf("initiator sees %+v", got)
	}

	fields := expectError(t, alice.sendBody(chat.ID, "привет открытым текстом", nil), http.StatusUnprocessableEntity, "validation_failed")
	if fields["body"] != "invalid" {
		t.Fatalf("fields = %v", fields)
	}
	long := cipherText(600)
	res = alice.sendBody(chat.ID, long, nil)
	expectStatus(t, res, http.StatusCreated)
	var first store.Message
	res.json(t, &first)
	if !first.E2E || first.Body != long {
		t.Fatalf("first = %+v", first)
	}
	ev := waitEvent(t, bobConn, "message.new", forChat(chat.ID))
	if msg, _ := ev["message"].(map[string]any); msg["e2e"] != true || msg["body"] != long {
		t.Fatalf("event = %v", ev)
	}
	expectNoEvent(t, phoneConn, "message.new", forChat(chat.ID))

	res = bob.sendBody(chat.ID, cipherText(40), map[string]any{"reply_to_id": first.ID})
	expectStatus(t, res, http.StatusCreated)
	var reply store.Message
	res.json(t, &reply)
	if reply.ReplyTo == nil || reply.ReplyTo.Body != long {
		t.Fatalf("reply preview was altered: %+v", reply.ReplyTo)
	}

	edited := cipherText(48)
	expectStatus(t, alice.do(http.MethodPatch, "/api/chats/"+chat.ID+"/messages/"+first.ID, map[string]string{"body": edited}), http.StatusOK)
	expectError(t, alice.do(http.MethodPatch, "/api/chats/"+chat.ID+"/messages/"+first.ID, map[string]string{"body": "plain"}), http.StatusUnprocessableEntity, "validation_failed")

	expectError(t, alice.upload(chat.ID, "kind=image&name=a.png", "image/png", pngBytes(t, 4, 4)), http.StatusUnprocessableEntity, "validation_failed")
	att := alice.uploadOK(chat.ID, "kind=file&name=secret-plans.pdf", "application/pdf", []byte(cipherText(200)))
	if att.Name != "encrypted" || att.ContentType != "application/octet-stream" {
		t.Fatalf("attachment = %+v", att)
	}
	expectStatus(t, alice.sendBody(chat.ID, cipherText(80), map[string]any{"attachment_ids": []string{att.ID}}), http.StatusCreated)

	expectError(t, alice.do(http.MethodPost, "/api/chats/"+chat.ID+"/calls", map[string]bool{}), http.StatusForbidden, "calls_not_allowed")
	expectError(t, alice.do(http.MethodPatch, "/api/chats/"+chat.ID, map[string]string{"title": "x"}), http.StatusForbidden, "forbidden")
	expectError(t, bobPhone.do(http.MethodGet, "/api/chats/"+chat.ID+"/messages", nil), http.StatusNotFound, "chat_not_found")

	list := bob.messages(chat.ID, "")
	if len(list) != 3 || !list[0].E2E || list[0].Body != edited {
		t.Fatalf("messages = %+v", list)
	}
	if got := chatIDs(bob.chats())[chat.ID]; got.LastMessage == nil || !got.LastMessage.E2E || got.Unread != 1 {
		t.Fatalf("bob chat = %+v", got)
	}
}

func TestSecretChatClosing(t *testing.T) {
	e := setup(t)
	aliceCreds, bobCreds := newCreds(), newCreds()
	alice := e.client(t)
	alice.register(aliceCreds)
	bob := e.client(t)
	bob.register(bobCreds)
	aliceConn := alice.connect()

	declined := alice.createSecret(bob.user.ID, publicKey(t))
	expectStatus(t, bob.do(http.MethodPost, "/api/chats/"+declined.ID+"/leave", nil), http.StatusNoContent)
	waitEvent(t, aliceConn, "chat.removed", forChat(declined.ID))
	expectError(t, alice.do(http.MethodGet, "/api/chats/"+declined.ID, nil), http.StatusNotFound, "chat_not_found")

	chat := alice.createSecret(bob.user.ID, publicKey(t))
	expectStatus(t, bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/accept", map[string]string{"public_key": publicKey(t)}), http.StatusOK)
	expectStatus(t, alice.sendBody(chat.ID, cipherText(64), nil), http.StatusCreated)

	expectStatus(t, bob.do(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent)
	waitEvent(t, aliceConn, "chat.removed", forChat(chat.ID))
	if _, ok := chatIDs(alice.chats())[chat.ID]; ok {
		t.Fatal("secret chat survived the peer logging out")
	}

	bobAgain := e.device(t, bobCreds)
	if _, ok := chatIDs(bobAgain.chats())[chat.ID]; ok {
		t.Fatal("secret chat visible on a new session")
	}

	third := alice.createSecret(bobAgain.user.ID, publicKey(t))
	expectStatus(t, bobAgain.do(http.MethodPost, "/api/chats/"+third.ID+"/accept", map[string]string{"public_key": publicKey(t)}), http.StatusOK)
	sessions := bobAgain.do(http.MethodGet, "/api/sessions", nil)
	var list []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
	}
	sessions.json(t, &list)
	var current string
	for _, s := range list {
		if s.Current {
			current = s.ID
		}
	}
	other := e.device(t, bobCreds)
	expectStatus(t, other.do(http.MethodDelete, "/api/sessions/"+current, nil), http.StatusNoContent)
	waitEvent(t, aliceConn, "chat.removed", forChat(third.ID))
}

func TestSecretChatValidation(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	key := publicKey(t)

	cases := []struct {
		body  map[string]string
		field string
		want  string
	}{
		{map[string]string{"user_id": bob.user.ID, "public_key": "AAAA"}, "public_key", "invalid"},
		{map[string]string{"user_id": bob.user.ID, "public_key": base64.StdEncoding.EncodeToString(make([]byte, 65))}, "public_key", "invalid"},
		{map[string]string{"user_id": alice.user.ID, "public_key": key}, "user_id", "self"},
		{map[string]string{"user_id": "nope", "public_key": key}, "user_id", "invalid"},
	}
	for _, c := range cases {
		fields := expectError(t, alice.do(http.MethodPost, "/api/chats/secret", c.body), http.StatusUnprocessableEntity, "validation_failed")
		if fields[c.field] != c.want {
			t.Fatalf("%v: fields = %v", c.body, fields)
		}
	}
	expectError(t, alice.do(http.MethodPost, "/api/chats/secret", map[string]string{"user_id": newUUID(), "public_key": key}), http.StatusNotFound, "user_not_found")

	direct := alice.openChat(bob.user.ID)
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+direct.ID+"/accept", map[string]string{"public_key": key}), http.StatusUnprocessableEntity, "not_secret")
	if !strings.HasPrefix(direct.Kind, "direct") {
		t.Fatalf("kind = %s", direct.Kind)
	}
}
