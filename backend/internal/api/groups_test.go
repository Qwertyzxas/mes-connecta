package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"connecta/internal/store"
)

func (c *client) createGroup(title string, members ...string) store.Chat {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/group", map[string]any{"title": title, "member_ids": members})
	expectStatus(c.t, res, http.StatusCreated)
	var chat store.Chat
	res.json(c.t, &chat)
	return chat
}

func (c *client) createChannel(title, handle string, public bool) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/api/chats/channel", map[string]any{"title": title, "handle": handle, "is_public": public, "about": "о канале"})
}

func (c *client) members(chatID string) map[string]string {
	c.t.Helper()
	res := c.do(http.MethodGet, "/api/chats/"+chatID+"/members", nil)
	expectStatus(c.t, res, http.StatusOK)
	var list []store.Member
	res.json(c.t, &list)
	roles := map[string]string{}
	for _, m := range list {
		roles[m.ID] = m.Role
	}
	return roles
}

func systemAction(t *testing.T, m *store.Message) store.SystemMeta {
	t.Helper()
	if m == nil || m.Kind != "system" {
		t.Fatalf("expected system message, got %+v", m)
	}
	var meta store.SystemMeta
	if err := json.Unmarshal(m.Meta, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	return meta
}

type trio struct{ alice, bob, carol *client }

func (e *env) trio(t *testing.T) trio {
	t.Helper()
	tr := trio{e.client(t), e.client(t), e.client(t)}
	for _, c := range []*client{tr.alice, tr.bob, tr.carol} {
		c.register(newCreds())
	}
	return tr
}

func TestCreateGroup(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	bobConn := tr.bob.connect()

	group := tr.alice.createGroup("  Команда  ", tr.bob.user.ID, tr.carol.user.ID, tr.bob.user.ID)
	if group.Kind != "group" || group.Title != "Команда" || group.Role != "owner" || group.MemberCount != 3 || group.Peer != nil {
		t.Fatalf("group = %+v", group)
	}
	if meta := systemAction(t, group.LastMessage); meta.Action != "created" || meta.Title != "Команда" {
		t.Fatalf("meta = %+v", meta)
	}

	ev := waitEvent(t, bobConn, "message.new", nil)
	if ev["chat_id"] != group.ID {
		t.Fatalf("bob event = %v", ev)
	}
	list := tr.bob.chats()
	if len(list) != 1 || list[0].ID != group.ID || list[0].Role != "member" || list[0].Unread != 0 {
		t.Fatalf("bob chats = %+v", list)
	}

	fields := expectError(t, tr.alice.do(http.MethodPost, "/api/chats/group", map[string]any{"title": " ", "member_ids": []string{}}),
		http.StatusUnprocessableEntity, "validation_failed")
	if fields["title"] != "empty" {
		t.Fatalf("fields = %v", fields)
	}
	fields = expectError(t, tr.alice.do(http.MethodPost, "/api/chats/group", map[string]any{"title": "x", "member_ids": []string{"nope"}}),
		http.StatusUnprocessableEntity, "validation_failed")
	if fields["member_ids"] != "invalid" {
		t.Fatalf("fields = %v", fields)
	}
}

func TestGroupMessagingAndReceipts(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	group := tr.alice.createGroup("Чат", tr.bob.user.ID, tr.carol.user.ID)
	carolConn := tr.carol.connect()

	msg := tr.bob.send(group.ID, "всем привет")
	ev := waitEvent(t, carolConn, "message.new", func(ev wsEvent) bool {
		return ev["message"].(map[string]any)["body"] == "всем привет"
	})
	sender := ev["message"].(map[string]any)["sender"].(map[string]any)
	if sender["display_name"] != tr.bob.user.DisplayName {
		t.Fatalf("sender = %v", sender)
	}

	if got := tr.carol.chat(group.ID).Unread; got != 1 {
		t.Fatalf("carol unread = %d, system messages must not count", got)
	}
	expectStatus(t, tr.carol.do(http.MethodPost, "/api/chats/"+group.ID+"/read", map[string]int64{"seq": msg.Seq}), http.StatusNoContent)
	if got := tr.bob.chat(group.ID).PeerLastReadSeq; got != msg.Seq {
		t.Fatalf("bob sees read seq %d, want %d", got, msg.Seq)
	}
}

func TestGroupMembershipManagement(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	dave := e.client(t)
	dave.register(newCreds())
	group := tr.alice.createGroup("Проект", tr.bob.user.ID)
	members := "/api/chats/" + group.ID + "/members"

	expectError(t, tr.bob.do(http.MethodPost, members, map[string]any{"user_ids": []string{tr.carol.user.ID}}),
		http.StatusForbidden, "forbidden")

	res := tr.alice.do(http.MethodPost, members, map[string]any{"user_ids": []string{tr.carol.user.ID, dave.user.ID, tr.bob.user.ID}})
	expectStatus(t, res, http.StatusOK)
	if roles := tr.alice.members(group.ID); len(roles) != 4 || roles[tr.alice.user.ID] != "owner" || roles[dave.user.ID] != "member" {
		t.Fatalf("roles = %v", roles)
	}
	last := tr.alice.chat(group.ID).LastMessage
	if meta := systemAction(t, last); meta.Action != "added" || meta.TargetID != dave.user.ID || meta.TargetName != dave.user.DisplayName {
		t.Fatalf("added meta = %+v", meta)
	}
	expectError(t, tr.alice.do(http.MethodPost, members, map[string]any{"user_ids": []string{newUUID()}}), http.StatusNotFound, "user_not_found")

	expectError(t, tr.bob.do(http.MethodPatch, members+"/"+tr.carol.user.ID, map[string]string{"role": "admin"}), http.StatusForbidden, "forbidden")
	expectStatus(t, tr.alice.do(http.MethodPatch, members+"/"+tr.bob.user.ID, map[string]string{"role": "admin"}), http.StatusNoContent)
	if roles := tr.alice.members(group.ID); roles[tr.bob.user.ID] != "admin" {
		t.Fatalf("bob role = %s", roles[tr.bob.user.ID])
	}
	expectError(t, tr.alice.do(http.MethodPatch, members+"/"+tr.bob.user.ID, map[string]string{"role": "owner"}),
		http.StatusUnprocessableEntity, "validation_failed")

	daveConn := dave.connect()
	expectStatus(t, tr.bob.do(http.MethodDelete, members+"/"+dave.user.ID, nil), http.StatusNoContent)
	waitEvent(t, daveConn, "chat.removed", func(ev wsEvent) bool { return ev["chat_id"] == group.ID })
	expectError(t, dave.do(http.MethodGet, "/api/chats/"+group.ID, nil), http.StatusNotFound, "chat_not_found")
	expectError(t, dave.do(http.MethodGet, "/api/chats/"+group.ID+"/messages", nil), http.StatusNotFound, "chat_not_found")
	if meta := systemAction(t, tr.alice.chat(group.ID).LastMessage); meta.Action != "removed" || meta.TargetID != dave.user.ID {
		t.Fatalf("removed meta = %+v", meta)
	}

	expectError(t, tr.bob.do(http.MethodDelete, members+"/"+tr.alice.user.ID, nil), http.StatusForbidden, "forbidden")
	expectError(t, tr.carol.do(http.MethodDelete, members+"/"+tr.bob.user.ID, nil), http.StatusForbidden, "forbidden")
	expectError(t, tr.bob.do(http.MethodDelete, members+"/"+tr.bob.user.ID, nil), http.StatusUnprocessableEntity, "use_leave")
	expectError(t, tr.alice.do(http.MethodDelete, members+"/"+dave.user.ID, nil), http.StatusNotFound, "member_not_found")
}

func TestOwnerLeavingTransfersOwnership(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	group := tr.alice.createGroup("Клуб", tr.bob.user.ID, tr.carol.user.ID)
	expectStatus(t, tr.alice.do(http.MethodPatch, "/api/chats/"+group.ID+"/members/"+tr.carol.user.ID, map[string]string{"role": "admin"}), http.StatusNoContent)

	expectStatus(t, tr.alice.do(http.MethodPost, "/api/chats/"+group.ID+"/leave", nil), http.StatusNoContent)
	expectError(t, tr.alice.do(http.MethodGet, "/api/chats/"+group.ID, nil), http.StatusNotFound, "chat_not_found")

	roles := tr.bob.members(group.ID)
	if len(roles) != 2 || roles[tr.carol.user.ID] != "owner" || roles[tr.bob.user.ID] != "member" {
		t.Fatalf("roles after owner left = %v, want admin promoted to owner", roles)
	}
	if meta := systemAction(t, tr.bob.chat(group.ID).LastMessage); meta.Action != "left" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDirectChatCannotBeLeftOrManaged(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+chat.ID+"/leave", nil), http.StatusUnprocessableEntity, "cannot_leave_direct")
	expectError(t, alice.do(http.MethodPatch, "/api/chats/"+chat.ID, map[string]string{"title": "x"}), http.StatusForbidden, "forbidden")
	expectError(t, alice.do(http.MethodDelete, "/api/chats/"+chat.ID+"/members/"+bob.user.ID, nil), http.StatusForbidden, "forbidden")
}

func TestModerationAndSystemMessages(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	group := tr.alice.createGroup("Модерация", tr.bob.user.ID, tr.carol.user.ID)
	spam := tr.carol.send(group.ID, "спам")

	expectError(t, tr.bob.remove(group.ID, spam.ID), http.StatusForbidden, "forbidden")
	expectStatus(t, tr.alice.remove(group.ID, spam.ID), http.StatusOK)

	system := tr.alice.messages(group.ID, "")[0]
	if system.Kind != "system" {
		t.Fatalf("first message = %+v", system)
	}
	expectError(t, tr.alice.edit(group.ID, system.ID, "взлом"), http.StatusForbidden, "forbidden")
	expectError(t, tr.alice.remove(group.ID, system.ID), http.StatusForbidden, "forbidden")
}

func TestRenameGroup(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	group := tr.alice.createGroup("Старое", tr.bob.user.ID)
	path := "/api/chats/" + group.ID

	expectError(t, tr.bob.do(http.MethodPatch, path, map[string]string{"title": "Моё"}), http.StatusForbidden, "forbidden")
	res := tr.alice.do(http.MethodPatch, path, map[string]string{"title": "Новое", "about": "описание"})
	expectStatus(t, res, http.StatusOK)
	var chat store.Chat
	res.json(t, &chat)
	if chat.Title != "Новое" || chat.About != "описание" {
		t.Fatalf("chat = %+v", chat)
	}
	if meta := systemAction(t, tr.bob.chat(group.ID).LastMessage); meta.Action != "renamed" || meta.Title != "Новое" {
		t.Fatalf("meta = %+v", meta)
	}

	before := tr.bob.chat(group.ID).LastSeq
	expectStatus(t, tr.alice.do(http.MethodPatch, path, map[string]string{"about": "только описание"}), http.StatusOK)
	if got := tr.bob.chat(group.ID).LastSeq; got != before {
		t.Fatal("changing only about must not add a system message")
	}
}

func TestChannels(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)

	res := tr.alice.createChannel("Новости", "@News_Daily", true)
	expectStatus(t, res, http.StatusCreated)
	var channel store.Chat
	res.json(t, &channel)
	if channel.Kind != "channel" || channel.Handle == nil || *channel.Handle != "news_daily" || !channel.IsPublic || channel.Role != "owner" {
		t.Fatalf("channel = %+v", channel)
	}

	expectError(t, tr.bob.createChannel("Копия", "news_daily", true), http.StatusConflict, "handle_taken")
	if f := expectError(t, tr.bob.createChannel("Плохой", "ab", true), http.StatusUnprocessableEntity, "validation_failed"); f["handle"] != "invalid" {
		t.Fatalf("fields = %v", f)
	}
	if f := expectError(t, tr.bob.createChannel("Частный", "private_one", false), http.StatusUnprocessableEntity, "validation_failed"); f["handle"] != "not_allowed" {
		t.Fatalf("fields = %v", f)
	}

	res = tr.bob.do(http.MethodGet, "/api/search?q=news", nil)
	expectStatus(t, res, http.StatusOK)
	var found struct {
		Users    []store.PublicUser    `json:"users"`
		Channels []store.ChannelResult `json:"channels"`
	}
	res.json(t, &found)
	if len(found.Channels) != 1 || found.Channels[0].ID != channel.ID || found.Channels[0].Joined || found.Channels[0].MemberCount != 1 {
		t.Fatalf("search = %+v", found)
	}

	expectError(t, tr.bob.send2(channel.ID), http.StatusNotFound, "chat_not_found")
	res = tr.bob.do(http.MethodPost, "/api/channels/"+channel.ID+"/join", nil)
	expectStatus(t, res, http.StatusCreated)
	expectStatus(t, tr.bob.do(http.MethodPost, "/api/channels/"+channel.ID+"/join", nil), http.StatusOK)
	if got := tr.bob.chat(channel.ID); got.Role != "member" || got.MemberCount != 2 {
		t.Fatalf("bob channel = %+v", got)
	}

	expectError(t, tr.bob.send2(channel.ID), http.StatusForbidden, "read_only")
	bobConn := tr.bob.connect()
	tr.alice.send(channel.ID, "первый выпуск")
	waitEvent(t, bobConn, "message.new", func(ev wsEvent) bool {
		return ev["message"].(map[string]any)["body"] == "первый выпуск"
	})

	expectStatus(t, tr.bob.do(http.MethodPost, "/api/chats/"+channel.ID+"/leave", nil), http.StatusNoContent)
	expectError(t, tr.bob.do(http.MethodGet, "/api/chats/"+channel.ID, nil), http.StatusNotFound, "chat_not_found")
	if got := tr.alice.chat(channel.ID).LastMessage.Body; got != "первый выпуск" {
		t.Fatalf("leaving a channel must not post a system message, last = %q", got)
	}

	private := tr.alice.createChannel("Закрытый", "", false)
	expectStatus(t, private, http.StatusCreated)
	var closed store.Chat
	private.json(t, &closed)
	expectError(t, tr.carol.do(http.MethodPost, "/api/channels/"+closed.ID+"/join", nil), http.StatusNotFound, "chat_not_found")
}

func (c *client) send2(chatID string) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/api/chats/"+chatID+"/messages", map[string]string{"client_id": newUUID(), "body": "x"})
}
