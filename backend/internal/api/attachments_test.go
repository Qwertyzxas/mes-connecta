package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"connecta/internal/store"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (c *client) uploadOK(chatID, query, contentType string, data []byte) store.Attachment {
	c.t.Helper()
	res := c.upload(chatID, query, contentType, data)
	expectStatus(c.t, res, http.StatusCreated)
	var a store.Attachment
	res.json(c.t, &a)
	return a
}

func (c *client) sendWith(chatID, body string, ids ...string) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/api/chats/"+chatID+"/messages", map[string]any{
		"client_id": newUUID(), "body": body, "attachment_ids": ids,
	})
}

func TestUploadImageAndSend(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	data := pngBytes(t, 3, 2)

	att := alice.uploadOK(chat.ID, "kind=image&name=..%2F..%2Fphoto.png", "application/octet-stream", data)
	if att.Kind != "image" || att.ContentType != "image/png" || att.Name != "photo.png" || att.Size != int64(len(data)) {
		t.Fatalf("attachment = %+v", att)
	}
	if att.Width == nil || att.Height == nil || *att.Width != 3 || *att.Height != 2 {
		t.Fatalf("dimensions = %v x %v", att.Width, att.Height)
	}
	if !strings.HasPrefix(att.URL, "/api/files/"+att.ID+"?exp=") {
		t.Fatalf("url = %s", att.URL)
	}

	res := alice.sendWith(chat.ID, "", att.ID)
	expectStatus(t, res, http.StatusCreated)
	var msg store.Message
	res.json(t, &msg)
	if msg.Body != "" || len(msg.Attachments) != 1 || msg.Attachments[0].ID != att.ID {
		t.Fatalf("message = %+v", msg)
	}

	history := bob.messages(chat.ID, "")
	if len(history) != 1 || len(history[0].Attachments) != 1 {
		t.Fatalf("history = %+v", history)
	}
	file := bob.get(history[0].Attachments[0].URL)
	expectStatus(t, file, http.StatusOK)
	if !bytes.Equal(file.body, data) {
		t.Fatal("downloaded bytes differ")
	}
	if ct := file.header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %s", ct)
	}
	if cd := file.header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Fatalf("content-disposition = %s", cd)
	}

	if last := bob.chat(chat.ID).LastMessage; last == nil || len(last.Attachments) != 1 || last.Attachments[0].URL == "" {
		t.Fatalf("chat last message = %+v", last)
	}

	tampered := strings.Replace(att.URL, "sig=", "sig=x", 1)
	expectStatus(t, bob.get(tampered), http.StatusForbidden)
	expectStatus(t, bob.get("/api/files/"+att.ID), http.StatusForbidden)
	expectStatus(t, bob.get(strings.Replace(att.URL, "exp=", "exp=1", 1)), http.StatusForbidden)
}

func TestUploadValidation(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	check := func(query, ct string, data []byte, field, code string) {
		t.Helper()
		fields := expectError(t, alice.upload(chat.ID, query, ct, data), http.StatusUnprocessableEntity, "validation_failed")
		if fields[field] != code {
			t.Fatalf("%s: fields = %v, want %s=%s", query, fields, field, code)
		}
	}
	check("kind=video", "video/mp4", []byte("x"), "kind", "invalid")
	check("kind=image&name=a.png", "image/png", []byte("definitely not an image"), "file", "not_an_image")
	check("kind=voice", "audio/webm", []byte("voice"), "duration_ms", "invalid")
	check("kind=voice&duration_ms=99999999", "audio/webm", []byte("voice"), "duration_ms", "invalid")
	check("kind=voice&duration_ms=1000", "text/html", []byte("<html>hi</html>"), "file", "not_audio")
	check("kind=voice&duration_ms=1000", "audio/webm", []byte("<html><script>x</script></html>"), "file", "not_audio")

	mallory := e.client(t)
	mallory.register(newCreds())
	expectError(t, mallory.upload(chat.ID, "kind=file&name=x.txt", "text/plain", []byte("x")), http.StatusNotFound, "chat_not_found")

	channel := alice.createChannel("Канал", "", false)
	var ch store.Chat
	channel.json(t, &ch)
	expectStatus(t, alice.do(http.MethodPost, "/api/chats/"+ch.ID+"/members", map[string]any{"user_ids": []string{bob.user.ID}}), http.StatusOK)
	expectError(t, bob.upload(ch.ID, "kind=file&name=x.txt", "text/plain", []byte("x")), http.StatusForbidden, "read_only")
}

func TestDownloadedFilesCannotRunScripts(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)

	att := alice.uploadOK(chat.ID, "kind=file&name=отчёт.html", "text/html", []byte("<script>alert(1)</script>"))
	if att.Name != "отчёт.html" {
		t.Fatalf("name = %q", att.Name)
	}
	expectStatus(t, alice.sendWith(chat.ID, "смотри", att.ID), http.StatusCreated)

	file := bob.get(att.URL)
	expectStatus(t, file, http.StatusOK)
	h := file.header
	if h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(h.Get("Content-Disposition"), "filename*=utf-8''%D0%BE%D1%82%D1%87%D1%91%D1%82.html") ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe headers: %v", h)
	}
}

func TestAttachmentOwnershipRules(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	att := alice.uploadOK(chat.ID, "kind=file&name=a.txt", "text/plain", []byte("hello"))

	if f := expectError(t, bob.sendWith(chat.ID, "", att.ID), http.StatusUnprocessableEntity, "validation_failed"); f["attachment_ids"] != "invalid" {
		t.Fatalf("someone else's attachment: %v", f)
	}

	carol := e.client(t)
	carol.register(newCreds())
	other := alice.openChat(carol.user.ID)
	expectError(t, alice.sendWith(other.ID, "", att.ID), http.StatusUnprocessableEntity, "validation_failed")

	expectStatus(t, alice.sendWith(chat.ID, "", att.ID), http.StatusCreated)
	expectError(t, alice.sendWith(chat.ID, "again", att.ID), http.StatusUnprocessableEntity, "validation_failed")

	ids := make([]string, 11)
	for i := range ids {
		ids[i] = newUUID()
	}
	expectError(t, alice.sendWith(chat.ID, "", ids...), http.StatusUnprocessableEntity, "validation_failed")

	if f := expectError(t, alice.sendWith(chat.ID, ""), http.StatusUnprocessableEntity, "validation_failed"); f["body"] != "empty" {
		t.Fatalf("empty message without attachments: %v", f)
	}
}

func TestDeletingMessageRemovesFiles(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	att := alice.uploadOK(chat.ID, "kind=image&name=p.png", "image/png", pngBytes(t, 1, 1))
	res := alice.sendWith(chat.ID, "подпись", att.ID)
	expectStatus(t, res, http.StatusCreated)
	var msg store.Message
	res.json(t, &msg)

	res = alice.remove(chat.ID, msg.ID)
	expectStatus(t, res, http.StatusOK)
	var deleted store.Message
	res.json(t, &deleted)
	if len(deleted.Attachments) != 0 {
		t.Fatalf("deleted message still has attachments: %+v", deleted.Attachments)
	}
	expectError(t, bob.get(att.URL), http.StatusNotFound, "file_not_found")
	if h := bob.messages(chat.ID, ""); len(h[0].Attachments) != 0 {
		t.Fatal("history still lists attachments of a deleted message")
	}
}

func TestVoiceRangeAndURLRefresh(t *testing.T) {
	e := setup(t)
	alice, bob := e.pair(t)
	chat := alice.openChat(bob.user.ID)
	data := bytes.Repeat([]byte{0x1a, 0x45, 0xdf, 0xa3}, 64)
	att := alice.uploadOK(chat.ID, "kind=voice&duration_ms=1500", "audio/webm;codecs=opus", data)
	if att.ContentType != "audio/webm" || att.DurationMs == nil || *att.DurationMs != 1500 {
		t.Fatalf("voice = %+v", att)
	}
	expectStatus(t, alice.sendWith(chat.ID, "", att.ID), http.StatusCreated)

	req, _ := http.NewRequest(http.MethodGet, bob.base+att.URL, nil)
	req.Header.Set("Range", "bytes=0-3")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || res.ContentLength != 4 {
		t.Fatalf("range response = %d, length %d", res.StatusCode, res.ContentLength)
	}

	fresh := bob.do(http.MethodGet, "/api/files/"+att.ID+"/url", nil)
	expectStatus(t, fresh, http.StatusOK)
	mallory := e.client(t)
	mallory.register(newCreds())
	expectError(t, mallory.do(http.MethodGet, "/api/files/"+att.ID+"/url", nil), http.StatusNotFound, "file_not_found")
}
