package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"connecta/internal/store"
)

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (c *client) openChat(peerID string) store.Chat {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/direct", map[string]string{"user_id": peerID})
	if res.status != http.StatusCreated && res.status != http.StatusOK {
		c.t.Fatalf("open chat: status %d, body %s", res.status, res.body)
	}
	var chat store.Chat
	res.json(c.t, &chat)
	return chat
}

func (c *client) send(chatID, body string) store.Message {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/"+chatID+"/messages", map[string]string{"client_id": newUUID(), "body": body})
	expectStatus(c.t, res, http.StatusCreated)
	var msg store.Message
	res.json(c.t, &msg)
	return msg
}

func (c *client) chats() []store.Chat {
	c.t.Helper()
	res := c.do(http.MethodGet, "/api/chats", nil)
	expectStatus(c.t, res, http.StatusOK)
	var chats []store.Chat
	res.json(c.t, &chats)
	return chats
}

func (c *client) chat(id string) store.Chat {
	c.t.Helper()
	res := c.do(http.MethodGet, "/api/chats/"+id, nil)
	expectStatus(c.t, res, http.StatusOK)
	var chat store.Chat
	res.json(c.t, &chat)
	return chat
}

func (c *client) messages(chatID, query string) []store.Message {
	c.t.Helper()
	res := c.do(http.MethodGet, "/api/chats/"+chatID+"/messages"+query, nil)
	expectStatus(c.t, res, http.StatusOK)
	var msgs []store.Message
	res.json(c.t, &msgs)
	return msgs
}

func seqs(msgs []store.Message) string {
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = strconv.FormatInt(m.Seq, 10)
	}
	return strings.Join(parts, ",")
}

func (e *env) pair(t *testing.T) (*client, *client) {
	t.Helper()
	alice := e.client(t)
	alice.register(newCreds())
	bob := e.client(t)
	bob.register(newCreds())
	return alice, bob
}

func TestSearchUsers(t *testing.T) {
	e := setup(t)
	me := e.client(t)
	me.register(creds{Email: "me@example.com", Username: "abc_me", Password: "correct horse battery"})
	for _, name := range []string{"abc_1", "abcx1", "zed"} {
		e.client(t).register(creds{Email: name + "@example.com", Username: name, Password: "correct horse battery"})
	}

	names := func(query string) string {
		res := me.do(http.MethodGet, "/api/users/search?q="+query, nil)
		expectStatus(t, res, http.StatusOK)
		var users []store.PublicUser
		res.json(t, &users)
		out := make([]string, len(users))
		for i, u := range users {
			out[i] = u.Username
		}
		return strings.Join(out, ",")
	}

	if got := names("abc"); got != "abc_1,abcx1" {
		t.Errorf("search abc = %q, want abc_1,abcx1 without self", got)
	}
	if got := names("abc_"); got != "abc_1" {
		t.Errorf("search abc_ = %q, underscore must be literal", got)
	}
	if got := names("%40ABCX"); got != "abcx1" {
		t.Errorf("search @ABCX = %q, want abcx1", got)
	}
	if got := names("a%25"); got != "" {
		t.Errorf("search with wildcard = %q, want empty", got)
	}
	if got := names(""); got != "" {
		t.Errorf("empty search = %q, want empty", got)
	}
}

func TestOpenDirectChat(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)

	res := alice.do(http.MethodPost, "/api/chats/direct", map[string]string{"user_id": bob.user.ID})
	expectStatus(t, res, http.StatusCreated)
	var first store.Chat
	res.json(t, &first)
	if first.Peer.ID != bob.user.ID || first.Peer.Username != bob.user.Username {
		t.Fatalf("peer = %+v, want bob", first.Peer)
	}
	if first.LastSeq != 0 || first.LastMessage != nil || first.Unread != 0 {
		t.Fatalf("new chat is not empty: %+v", first)
	}

	again := alice.openChat(bob.user.ID)
	reverse := bob.openChat(alice.user.ID)
	if again.ID != first.ID || reverse.ID != first.ID {
		t.Fatalf("direct chat ids differ: %s %s %s", first.ID, again.ID, reverse.ID)
	}
	if reverse.Peer.ID != alice.user.ID {
		t.Fatalf("bob sees peer %s, want alice", reverse.Peer.ID)
	}

	expectError(t, alice.do(http.MethodPost, "/api/chats/direct", map[string]string{"user_id": alice.user.ID}),
		http.StatusUnprocessableEntity, "cannot_chat_with_self")
	expectError(t, alice.do(http.MethodPost, "/api/chats/direct", map[string]string{"user_id": newUUID()}),
		http.StatusNotFound, "user_not_found")
	expectError(t, alice.do(http.MethodPost, "/api/chats/direct", map[string]string{"user_id": "bob"}),
		http.StatusUnprocessableEntity, "validation_failed")
}

func TestEmptyChatVisibleOnlyToCreator(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	if got := len(alice.chats()); got != 1 {
		t.Fatalf("alice chats = %d, want 1", got)
	}
	if got := len(bob.chats()); got != 0 {
		t.Fatalf("bob sees empty chat before first message: %d", got)
	}

	alice.send(chat.ID, "Привет!")
	list := bob.chats()
	if len(list) != 1 {
		t.Fatalf("bob chats = %d, want 1", len(list))
	}
	if list[0].LastMessage == nil || list[0].LastMessage.Body != "Привет!" || list[0].Unread != 1 {
		t.Fatalf("bob chat = %+v, want last message and 1 unread", list[0])
	}
}

func TestChatListOrderedByActivity(t *testing.T) {
	e := setup(t)
	alice := e.client(t)
	alice.register(newCreds())
	bob := e.client(t)
	bob.register(newCreds())
	carol := e.client(t)
	carol.register(newCreds())

	withBob := alice.openChat(bob.user.ID)
	withCarol := alice.openChat(carol.user.ID)
	alice.send(withCarol.ID, "first")
	alice.send(withBob.ID, "second")

	list := alice.chats()
	if len(list) != 2 || list[0].ID != withBob.ID || list[1].ID != withCarol.ID {
		t.Fatalf("chat order = %v, want bob chat first", []string{list[0].ID, list[1].ID})
	}
}

func TestSendMessage(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	path := "/api/chats/" + chat.ID + "/messages"

	first := alice.send(chat.ID, "  one  ")
	second := bob.send(chat.ID, "two")
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seq = %d,%d, want 1,2", first.Seq, second.Seq)
	}
	if first.Body != "one" || first.SenderID != alice.user.ID || first.ChatID != chat.ID {
		t.Fatalf("message = %+v", first)
	}

	clientID := newUUID()
	res := alice.do(http.MethodPost, path, map[string]string{"client_id": clientID, "body": "retry me"})
	expectStatus(t, res, http.StatusCreated)
	var sent store.Message
	res.json(t, &sent)
	res = alice.do(http.MethodPost, path, map[string]string{"client_id": strings.ToUpper(clientID), "body": "retry me"})
	expectStatus(t, res, http.StatusOK)
	var retried store.Message
	res.json(t, &retried)
	if retried.ID != sent.ID || retried.Seq != sent.Seq {
		t.Fatalf("retry created a new message: %+v vs %+v", retried, sent)
	}
	if got := len(alice.messages(chat.ID, "")); got != 3 {
		t.Fatalf("messages = %d, want 3 after idempotent retry", got)
	}

	carol := e.client(t)
	carol.register(newCreds())
	other := alice.openChat(carol.user.ID)
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+other.ID+"/messages", map[string]string{"client_id": clientID, "body": "x"}),
		http.StatusConflict, "client_id_conflict")

	cases := []struct {
		name  string
		body  map[string]string
		field string
		code  string
	}{
		{"empty", map[string]string{"client_id": newUUID(), "body": ""}, "body", "empty"},
		{"whitespace", map[string]string{"client_id": newUUID(), "body": " \n\t "}, "body", "empty"},
		{"too long", map[string]string{"client_id": newUUID(), "body": strings.Repeat("ж", maxMessageLen+1)}, "body", "too_long"},
		{"bad client id", map[string]string{"client_id": "123", "body": "hi"}, "client_id", "invalid"},
	}
	for _, tc := range cases {
		fields := expectError(t, alice.do(http.MethodPost, path, tc.body), http.StatusUnprocessableEntity, "validation_failed")
		if fields[tc.field] != tc.code {
			t.Errorf("%s: fields = %v, want %s=%s", tc.name, fields, tc.field, tc.code)
		}
	}

	exact := alice.send(chat.ID, strings.Repeat("ж", maxMessageLen))
	if len([]rune(exact.Body)) != maxMessageLen {
		t.Fatalf("max length message was altered")
	}
}

func TestStrangerCannotAccessChat(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	alice.send(chat.ID, "secret")

	mallory := e.client(t)
	mallory.register(newCreds())
	base := "/api/chats/" + chat.ID
	expectError(t, mallory.do(http.MethodGet, base, nil), http.StatusNotFound, "chat_not_found")
	expectError(t, mallory.do(http.MethodGet, base+"/messages", nil), http.StatusNotFound, "chat_not_found")
	expectError(t, mallory.do(http.MethodPost, base+"/messages", map[string]string{"client_id": newUUID(), "body": "hi"}),
		http.StatusNotFound, "chat_not_found")
	expectError(t, mallory.do(http.MethodPost, base+"/read", map[string]int{"seq": 1}), http.StatusNotFound, "chat_not_found")
	expectError(t, mallory.do(http.MethodGet, "/api/chats/not-a-uuid", nil), http.StatusNotFound, "chat_not_found")
	expectError(t, mallory.do(http.MethodGet, "/api/chats/"+newUUID(), nil), http.StatusNotFound, "chat_not_found")

	if got := len(mallory.chats()); got != 0 {
		t.Fatalf("stranger sees %d chats", got)
	}
}

func TestMessagePaging(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	for i := 1; i <= 5; i++ {
		alice.send(chat.ID, "m"+strconv.Itoa(i))
	}

	checks := map[string]string{
		"":                         "1,2,3,4,5",
		"?limit=2":                 "4,5",
		"?before_seq=4&limit=2":    "2,3",
		"?before_seq=2":            "1",
		"?after_seq=3":             "4,5",
		"?after_seq=5":             "",
		"?after_seq=1&limit=1":     "2",
		"?limit=1000&before_seq=6": "1,2,3,4,5",
	}
	for query, want := range checks {
		if got := seqs(alice.messages(chat.ID, query)); got != want {
			t.Errorf("messages%s = %q, want %q", query, got, want)
		}
	}

	for _, bad := range []string{"?before_seq=2&after_seq=1", "?before_seq=-1", "?after_seq=x", "?limit=0"} {
		expectError(t, alice.do(http.MethodGet, "/api/chats/"+chat.ID+"/messages"+bad, nil), http.StatusBadRequest, "bad_request")
	}
}

func TestReadReceiptsAndUnread(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	for i := 0; i < 3; i++ {
		alice.send(chat.ID, "hello")
	}
	read := func(c *client, seq int) {
		t.Helper()
		expectStatus(t, c.do(http.MethodPost, "/api/chats/"+chat.ID+"/read", map[string]int{"seq": seq}), http.StatusNoContent)
	}

	if got := bob.chat(chat.ID).Unread; got != 3 {
		t.Fatalf("bob unread = %d, want 3", got)
	}
	if got := alice.chat(chat.ID).Unread; got != 0 {
		t.Fatalf("own messages counted as unread: %d", got)
	}

	read(bob, 2)
	if got := bob.chat(chat.ID); got.Unread != 1 || got.LastReadSeq != 2 {
		t.Fatalf("after read 2: unread=%d last_read=%d", got.Unread, got.LastReadSeq)
	}
	if got := alice.chat(chat.ID).PeerLastReadSeq; got != 2 {
		t.Fatalf("alice sees peer read %d, want 2", got)
	}

	read(bob, 1)
	if got := bob.chat(chat.ID).LastReadSeq; got != 2 {
		t.Fatalf("read pointer moved backwards to %d", got)
	}

	read(bob, 99)
	if got := bob.chat(chat.ID); got.Unread != 0 || got.LastReadSeq != 3 {
		t.Fatalf("after read 99: unread=%d last_read=%d, want capped at 3", got.Unread, got.LastReadSeq)
	}

	bob.send(chat.ID, "reply")
	if got := bob.chat(chat.ID); got.LastReadSeq != 4 || got.Unread != 0 {
		t.Fatalf("sender did not read own message: %+v", got)
	}
	if got := alice.chat(chat.ID).Unread; got != 1 {
		t.Fatalf("alice unread = %d, want 1", got)
	}

	expectError(t, bob.do(http.MethodPost, "/api/chats/"+chat.ID+"/read", map[string]int{"seq": 0}),
		http.StatusUnprocessableEntity, "validation_failed")
}
