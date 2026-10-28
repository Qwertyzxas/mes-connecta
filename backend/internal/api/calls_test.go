package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"connecta/internal/store"
)

const (
	fakeLiveKitKey    = "devkey"
	fakeLiveKitSecret = "secret-secret-secret-secret-secret"
)

type fakeLiveKit struct {
	*httptest.Server
	mu      sync.Mutex
	rooms   map[string][]string
	deleted []string
}

func newFakeLiveKit(t *testing.T) *fakeLiveKit {
	t.Helper()
	lk := &fakeLiveKit{rooms: map[string][]string{}}
	lk.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var claims struct {
			jwt.RegisteredClaims
			Video struct {
				Room      string `json:"room"`
				RoomAdmin bool   `json:"roomAdmin"`
			} `json:"video"`
		}
		_, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return []byte(fakeLiveKitSecret), nil })
		if err != nil || claims.Issuer != fakeLiveKitKey || !claims.Video.RoomAdmin {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Room string `json:"room"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Room != claims.Video.Room {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		lk.mu.Lock()
		defer lk.mu.Unlock()
		switch r.URL.Path {
		case "/twirp/livekit.RoomService/ListParticipants":
			ids, ok := lk.rooms[body.Room]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"not_found","msg":"room not found"}`))
				return
			}
			list := make([]map[string]string, 0, len(ids))
			for _, id := range ids {
				list = append(list, map[string]string{"identity": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"participants": list})
		case "/twirp/livekit.RoomService/DeleteRoom":
			lk.deleted = append(lk.deleted, body.Room)
			delete(lk.rooms, body.Room)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(lk.Close)
	return lk
}

func (lk *fakeLiveKit) setRoom(room string, ids ...string) {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	lk.rooms[room] = ids
}

type callSessionBody struct {
	Call  store.Call `json:"call"`
	Token string     `json:"token"`
	URL   string     `json:"url"`
}

func (c *client) startCall(chatID string, video bool) (callSessionBody, int) {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/chats/"+chatID+"/calls", map[string]bool{"video": video})
	if res.status != http.StatusCreated && res.status != http.StatusOK {
		c.t.Fatalf("start call: status %d, body %s", res.status, res.body)
	}
	var out callSessionBody
	res.json(c.t, &out)
	return out, res.status
}

func (c *client) callAction(callID, action string) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/api/calls/"+callID+"/"+action, nil)
}

func (c *client) joinCall(callID string) callSessionBody {
	c.t.Helper()
	res := c.callAction(callID, "join")
	expectStatus(c.t, res, http.StatusOK)
	var out callSessionBody
	res.json(c.t, &out)
	return out
}

func (c *client) callDone(callID, action string) store.Call {
	c.t.Helper()
	res := c.callAction(callID, action)
	expectStatus(c.t, res, http.StatusOK)
	var call store.Call
	res.json(c.t, &call)
	return call
}

func callMeta(t *testing.T, m *store.Message) store.CallMeta {
	t.Helper()
	if m == nil || m.Kind != "system" {
		t.Fatalf("expected system message, got %+v", m)
	}
	var meta store.CallMeta
	if err := json.Unmarshal(m.Meta, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.Action != "call" {
		t.Fatalf("meta = %+v", meta)
	}
	return meta
}

func eventCall(t *testing.T, ev wsEvent) store.Call {
	t.Helper()
	raw, _ := json.Marshal(ev["call"])
	var call store.Call
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatal(err)
	}
	return call
}

func (e *env) tokenGrant(t *testing.T, token string) (string, string) {
	t.Helper()
	grant, subject, err := e.srv.livekit.ParseJoinToken(token)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if !grant.RoomJoin || grant.CanPublish == nil || !*grant.CanPublish || grant.RoomAdmin {
		t.Fatalf("grant = %+v", grant)
	}
	return grant.Room, subject
}

func TestDirectCallLifecycle(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	aliceConn := alice.connect()
	bobConn := bob.connect()

	started, status := alice.startCall(chat.ID, true)
	if status != http.StatusCreated || started.Call.Status != store.CallRinging || !started.Call.Video ||
		started.Call.StartedBy != alice.user.ID || len(started.Call.Participants) != 1 {
		t.Fatalf("started = %d %+v", status, started.Call)
	}
	if !strings.HasPrefix(started.URL, "ws://") {
		t.Fatalf("url = %q", started.URL)
	}
	if room, who := e.tokenGrant(t, started.Token); room != started.Call.ID || who != alice.user.ID {
		t.Fatalf("token room=%s who=%s", room, who)
	}

	ev := waitEvent(t, bobConn, "call.incoming", nil)
	caller, _ := ev["caller"].(map[string]any)
	if ev["chat_id"] != chat.ID || caller["id"] != alice.user.ID || eventCall(t, ev).ID != started.Call.ID {
		t.Fatalf("incoming = %v", ev)
	}

	live := bob.do(http.MethodGet, "/api/calls?live=1", nil)
	expectStatus(t, live, http.StatusOK)
	var liveList []store.Call
	live.json(t, &liveList)
	if len(liveList) != 1 || liveList[0].ID != started.Call.ID {
		t.Fatalf("live = %+v", liveList)
	}

	joined := bob.joinCall(started.Call.ID)
	if joined.Call.Status != store.CallActive || joined.Call.AnsweredAt == nil || len(joined.Call.Participants) != 2 {
		t.Fatalf("joined = %+v", joined.Call)
	}
	if room, who := e.tokenGrant(t, joined.Token); room != started.Call.ID || who != bob.user.ID {
		t.Fatalf("bob token room=%s who=%s", room, who)
	}
	waitEvent(t, aliceConn, "call.updated", func(ev wsEvent) bool { return eventCall(t, ev).Status == store.CallActive })

	ended := alice.callDone(started.Call.ID, "leave")
	if ended.Status != store.CallEnded || ended.EndedAt == nil || len(ended.Participants) != 0 {
		t.Fatalf("ended = %+v", ended)
	}
	waitEvent(t, bobConn, "call.updated", func(ev wsEvent) bool { return eventCall(t, ev).Status == store.CallEnded })
	msgEv := waitEvent(t, bobConn, "message.new", nil)
	raw, _ := json.Marshal(msgEv["message"])
	var msg store.Message
	_ = json.Unmarshal(raw, &msg)
	if meta := callMeta(t, &msg); meta.Status != store.CallEnded || !meta.Video || meta.CallID != started.Call.ID {
		t.Fatalf("meta = %+v", meta)
	}
	if msg.SenderID != alice.user.ID {
		t.Fatalf("call message sender = %s", msg.SenderID)
	}

	expectError(t, bob.callAction(started.Call.ID, "join"), http.StatusConflict, "call_over")
	again := bob.callDone(started.Call.ID, "leave")
	if again.Status != store.CallEnded {
		t.Fatalf("second leave = %+v", again)
	}
	if got := bob.chat(chat.ID); got.Unread != 0 {
		t.Fatalf("call message counted as unread: %d", got.Unread)
	}

	history := bob.do(http.MethodGet, "/api/calls", nil)
	expectStatus(t, history, http.StatusOK)
	var entries []store.CallEntry
	history.json(t, &entries)
	if len(entries) != 1 || entries[0].Status != store.CallEnded || entries[0].Peer == nil ||
		entries[0].Peer.ID != alice.user.ID || entries[0].ChatTitle != alice.user.DisplayName {
		t.Fatalf("history = %+v", entries)
	}

	second, status := bob.startCall(chat.ID, false)
	if status != http.StatusCreated || second.Call.ID == started.Call.ID || second.Call.Video {
		t.Fatalf("second call = %d %+v", status, second.Call)
	}
}

func TestDeclineAndCancelCalls(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	first, _ := alice.startCall(chat.ID, false)
	if still := alice.callDone(first.Call.ID, "decline"); still.Status != store.CallRinging {
		t.Fatalf("caller decline changed call: %+v", still)
	}
	declined := bob.callDone(first.Call.ID, "decline")
	if declined.Status != store.CallDeclined {
		t.Fatalf("declined = %+v", declined)
	}
	if meta := callMeta(t, alice.chat(chat.ID).LastMessage); meta.Status != store.CallDeclined || meta.DurationS != 0 {
		t.Fatalf("meta = %+v", meta)
	}

	second, _ := alice.startCall(chat.ID, false)
	cancelled := alice.callDone(second.Call.ID, "leave")
	if cancelled.Status != store.CallMissed {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	if meta := callMeta(t, bob.chat(chat.ID).LastMessage); meta.Status != store.CallMissed {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestSimultaneousCallJoinsExisting(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	first, _ := alice.startCall(chat.ID, true)
	second, status := bob.startCall(chat.ID, false)
	if status != http.StatusOK || second.Call.ID != first.Call.ID || second.Call.Status != store.CallActive {
		t.Fatalf("second = %d %+v", status, second.Call)
	}
}

func TestGroupCall(t *testing.T) {
	e := setup(t)
	tr := e.trio(t)
	group := tr.alice.createGroup("Созвон", tr.bob.user.ID, tr.carol.user.ID)
	carolConn := tr.carol.connect()

	started, _ := tr.alice.startCall(group.ID, false)
	waitEvent(t, carolConn, "call.incoming", nil)
	tr.bob.joinCall(started.Call.ID)

	afterAlice := tr.alice.callDone(started.Call.ID, "leave")
	if afterAlice.Status != store.CallActive || len(afterAlice.Participants) != 1 || afterAlice.Participants[0] != tr.bob.user.ID {
		t.Fatalf("after alice = %+v", afterAlice)
	}
	if still := tr.carol.callDone(started.Call.ID, "decline"); still.Status != store.CallActive {
		t.Fatalf("decline in group changed call: %+v", still)
	}

	carolJoin, status := tr.carol.startCall(group.ID, false)
	if status != http.StatusOK || carolJoin.Call.ID != started.Call.ID || len(carolJoin.Call.Participants) != 2 {
		t.Fatalf("carol = %d %+v", status, carolJoin.Call)
	}
	tr.bob.callDone(started.Call.ID, "leave")
	final := tr.carol.callDone(started.Call.ID, "leave")
	if final.Status != store.CallEnded {
		t.Fatalf("final = %+v", final)
	}
	if meta := callMeta(t, tr.alice.chat(group.ID).LastMessage); meta.Status != store.CallEnded {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCallPermissions(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	outsider := e.client(t)
	outsider.register(newCreds())

	expectError(t, outsider.do(http.MethodPost, "/api/chats/"+chat.ID+"/calls", map[string]bool{}), http.StatusNotFound, "chat_not_found")
	call, _ := alice.startCall(chat.ID, false)
	for _, action := range []string{"join", "leave", "decline"} {
		expectError(t, outsider.callAction(call.Call.ID, action), http.StatusNotFound, "call_not_found")
	}
	expectError(t, alice.callAction("not-a-uuid", "join"), http.StatusNotFound, "call_not_found")
	expectError(t, alice.callAction(newUUID(), "join"), http.StatusNotFound, "call_not_found")

	res := alice.createChannel("Новости", "", false)
	expectStatus(t, res, http.StatusCreated)
	var channel store.Chat
	res.json(t, &channel)
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+channel.ID+"/calls", map[string]bool{}), http.StatusForbidden, "calls_not_allowed")

	var list []store.CallEntry
	history := outsider.do(http.MethodGet, "/api/calls", nil)
	expectStatus(t, history, http.StatusOK)
	history.json(t, &list)
	if len(list) != 0 {
		t.Fatalf("outsider history = %+v", list)
	}
}

func TestCallsUnavailableWithoutLiveKit(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	e.srv.livekit = nil
	expectError(t, alice.do(http.MethodPost, "/api/chats/"+chat.ID+"/calls", map[string]bool{}), http.StatusServiceUnavailable, "calls_unavailable")
}

func TestCallJanitor(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	ctx := context.Background()
	e.srv.callJoinGrace = 0

	ringing, _ := alice.startCall(chat.ID, false)
	e.lk.setRoom(ringing.Call.ID, alice.user.ID)
	e.srv.sweepCalls(ctx)
	if got, _ := e.srv.store.Call(ctx, ringing.Call.ID); got.Status != store.CallRinging {
		t.Fatalf("fresh ringing call swept: %+v", got)
	}
	e.srv.ringTimeout = 0
	e.srv.sweepCalls(ctx)
	if got, _ := e.srv.store.Call(ctx, ringing.Call.ID); got.Status != store.CallMissed {
		t.Fatalf("stale ringing call = %+v", got)
	}
	if meta := callMeta(t, bob.chat(chat.ID).LastMessage); meta.Status != store.CallMissed {
		t.Fatalf("meta = %+v", meta)
	}
	e.srv.ringTimeout = time.Hour

	active, _ := alice.startCall(chat.ID, true)
	bob.joinCall(active.Call.ID)
	e.lk.setRoom(active.Call.ID, alice.user.ID, bob.user.ID)
	e.srv.sweepCalls(ctx)
	if got, _ := e.srv.store.Call(ctx, active.Call.ID); got.Status != store.CallActive || len(got.Participants) != 2 {
		t.Fatalf("healthy call = %+v", got)
	}

	e.lk.setRoom(active.Call.ID, alice.user.ID)
	e.srv.sweepCalls(ctx)
	got, _ := e.srv.store.Call(ctx, active.Call.ID)
	if got.Status != store.CallEnded {
		t.Fatalf("abandoned call = %+v", got)
	}
	if meta := callMeta(t, alice.chat(chat.ID).LastMessage); meta.Status != store.CallEnded || !meta.Video {
		t.Fatalf("meta = %+v", meta)
	}
}
