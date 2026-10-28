package api

import (
	"net/http"
	"strings"
	"testing"

	"connecta/internal/store"
)

func (c *client) sendReply(chatID, body, replyTo string) store.Message {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/"+chatID+"/messages", map[string]string{
		"client_id": newUUID(), "body": body, "reply_to_id": replyTo,
	})
	expectStatus(c.t, res, http.StatusCreated)
	var msg store.Message
	res.json(c.t, &msg)
	return msg
}

func (c *client) edit(chatID, msgID, body string) response {
	c.t.Helper()
	return c.do(http.MethodPatch, "/api/chats/"+chatID+"/messages/"+msgID, map[string]string{"body": body})
}

func (c *client) remove(chatID, msgID string) response {
	c.t.Helper()
	return c.do(http.MethodDelete, "/api/chats/"+chatID+"/messages/"+msgID, nil)
}

func TestMessageCarriesSender(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	msg := alice.send(chat.ID, "hi")
	if msg.Sender.ID != alice.user.ID || msg.Sender.Username != alice.user.Username || msg.Sender.DisplayName != alice.user.DisplayName {
		t.Fatalf("sender = %+v", msg.Sender)
	}
	if msg.Rev != 1 || msg.EditedAt != nil || msg.DeletedAt != nil || msg.ReplyTo != nil {
		t.Fatalf("fresh message = %+v", msg)
	}
}

func TestReplyToMessage(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	original := alice.send(chat.ID, strings.Repeat("длинный текст ", 30))

	reply := bob.sendReply(chat.ID, "ответ", original.ID)
	if reply.ReplyTo == nil {
		t.Fatal("reply_to is empty")
	}
	if reply.ReplyTo.ID != original.ID || reply.ReplyTo.Seq != original.Seq || reply.ReplyTo.SenderID != alice.user.ID ||
		reply.ReplyTo.SenderName != alice.user.DisplayName {
		t.Fatalf("reply_to = %+v", reply.ReplyTo)
	}
	if n := len([]rune(reply.ReplyTo.Body)); n != 200 {
		t.Fatalf("reply preview length = %d, want 200", n)
	}

	carol := e.client(t)
	carol.register(newCreds())
	other := alice.openChat(carol.user.ID)
	foreign := alice.send(other.ID, "elsewhere")
	path := "/api/chats/" + chat.ID + "/messages"

	fields := expectError(t, alice.do(http.MethodPost, path, map[string]string{"client_id": newUUID(), "body": "x", "reply_to_id": foreign.ID}),
		http.StatusUnprocessableEntity, "validation_failed")
	if fields["reply_to_id"] != "not_found" {
		t.Fatalf("fields = %v", fields)
	}
	fields = expectError(t, alice.do(http.MethodPost, path, map[string]string{"client_id": newUUID(), "body": "x", "reply_to_id": "nope"}),
		http.StatusUnprocessableEntity, "validation_failed")
	if fields["reply_to_id"] != "invalid" {
		t.Fatalf("fields = %v", fields)
	}
}

func TestEditMessage(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	msg := alice.send(chat.ID, "опечатка")
	bobConn := bob.connect()

	res := alice.edit(chat.ID, msg.ID, "  исправлено  ")
	expectStatus(t, res, http.StatusOK)
	var edited store.Message
	res.json(t, &edited)
	if edited.Body != "исправлено" || edited.EditedAt == nil || edited.Rev <= msg.Rev || edited.Seq != msg.Seq {
		t.Fatalf("edited = %+v", edited)
	}

	ev := waitEvent(t, bobConn, "message.updated", nil)
	if m := ev["message"].(map[string]any); m["body"] != "исправлено" || m["edited_at"] == nil {
		t.Fatalf("update event = %v", ev)
	}

	res = alice.edit(chat.ID, msg.ID, "исправлено")
	expectStatus(t, res, http.StatusOK)
	var same store.Message
	res.json(t, &same)
	if same.Rev != edited.Rev {
		t.Fatalf("no-op edit bumped rev %d -> %d", edited.Rev, same.Rev)
	}

	expectError(t, bob.edit(chat.ID, msg.ID, "чужое"), http.StatusForbidden, "forbidden")
	fields := expectError(t, alice.edit(chat.ID, msg.ID, "   "), http.StatusUnprocessableEntity, "validation_failed")
	if fields["body"] != "empty" {
		t.Fatalf("fields = %v", fields)
	}
	expectError(t, alice.edit(chat.ID, newUUID(), "x"), http.StatusNotFound, "message_not_found")
	expectError(t, alice.edit(chat.ID, "bad", "x"), http.StatusNotFound, "message_not_found")

	if got := alice.messages(chat.ID, ""); len(got) != 1 || got[0].Body != "исправлено" {
		t.Fatalf("history after edit = %+v", got)
	}
}

func TestDeleteMessage(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	first := alice.send(chat.ID, "секрет")
	reply := bob.sendReply(chat.ID, "что?", first.ID)
	alice.send(chat.ID, "последнее")

	expectError(t, bob.remove(chat.ID, first.ID), http.StatusForbidden, "forbidden")

	res := alice.remove(chat.ID, first.ID)
	expectStatus(t, res, http.StatusOK)
	var deleted store.Message
	res.json(t, &deleted)
	if deleted.DeletedAt == nil || deleted.Body != "" || deleted.Seq != first.Seq || deleted.Rev <= first.Rev {
		t.Fatalf("deleted = %+v", deleted)
	}

	res = alice.remove(chat.ID, first.ID)
	expectStatus(t, res, http.StatusOK)
	var again store.Message
	res.json(t, &again)
	if again.Rev != deleted.Rev {
		t.Fatal("repeated delete bumped rev")
	}

	expectError(t, alice.edit(chat.ID, first.ID, "воскрешение"), http.StatusConflict, "message_deleted")

	history := bob.messages(chat.ID, "")
	if len(history) != 3 {
		t.Fatalf("history = %d messages, tombstone must keep its place", len(history))
	}
	if history[0].Body != "" || history[0].DeletedAt == nil {
		t.Fatalf("tombstone leaks content: %+v", history[0])
	}
	if history[1].ID != reply.ID || history[1].ReplyTo == nil || !history[1].ReplyTo.Deleted || history[1].ReplyTo.Body != "" {
		t.Fatalf("reply to deleted = %+v", history[1].ReplyTo)
	}

	if got := bob.chat(chat.ID).Unread; got != 1 {
		t.Fatalf("bob unread = %d, deleted messages must not count", got)
	}

	last := alice.messages(chat.ID, "")[2]
	expectStatus(t, alice.remove(chat.ID, last.ID), http.StatusOK)
	lm := bob.chat(chat.ID).LastMessage
	if lm == nil || lm.DeletedAt == nil || lm.Body != "" {
		t.Fatalf("chat last message after delete = %+v", lm)
	}
}

func TestSyncSinceRev(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	m1 := alice.send(chat.ID, "one")
	alice.send(chat.ID, "two")
	alice.send(chat.ID, "three")
	expectStatus(t, alice.edit(chat.ID, m1.ID, "one!"), http.StatusOK)

	if got := seqs(bob.messages(chat.ID, "?since_rev=3")); got != "1" {
		t.Fatalf("since_rev=3 = %q, want the edited first message", got)
	}
	if got := seqs(bob.messages(chat.ID, "?since_rev=1")); got != "2,3,1" {
		t.Fatalf("since_rev=1 = %q, want rev order 2,3,1", got)
	}
	if got := seqs(bob.messages(chat.ID, "?since_rev=1&limit=1")); got != "2" {
		t.Fatalf("since_rev paging = %q", got)
	}
	if got := bob.chat(chat.ID).LastRev; got != 4 {
		t.Fatalf("last_rev = %d, want 4", got)
	}
	expectError(t, bob.do(http.MethodGet, "/api/chats/"+chat.ID+"/messages?since_rev=1&before_seq=3", nil), http.StatusBadRequest, "bad_request")
}
