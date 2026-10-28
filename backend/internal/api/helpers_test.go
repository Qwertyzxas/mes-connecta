package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"connecta/internal/auth"
	"connecta/internal/config"
	"connecta/internal/ratelimit"
	"connecta/internal/realtime"
	"connecta/internal/store"
)

var (
	migrateOnce sync.Once
	migrateErr  error
	userSeq     atomic.Int64
)

type env struct {
	srv *Server
	ts  *httptest.Server
	rdb *redis.Client
}

func setup(t *testing.T) *env {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL are required for integration tests")
	}

	ctx := context.Background()
	migrateOnce.Do(func() { migrateErr = store.Migrate(dbURL) })
	if migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}

	pool, err := store.Connect(ctx, dbURL, 5*time.Second)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE users, sessions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush redis: %v", err)
	}

	cfg := config.Config{
		JWTSecret:    []byte(strings.Repeat("k", 32)),
		JWTIssuer:    "connecta-test",
		AccessTTL:    time.Minute,
		RefreshTTL:   time.Hour,
		CookieSecure: false,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub, err := realtime.NewHub(ctx, rdb, log)
	if err != nil {
		t.Fatalf("realtime hub: %v", err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	srv := New(cfg, store.New(pool), auth.NewTokens(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTTL),
		ratelimit.New(rdb), rdb, hub, log)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return &env{srv: srv, ts: ts, rdb: rdb}
}

type client struct {
	t     *testing.T
	base  string
	http  *http.Client
	token string
	ua    string
	user  store.User
}

func (e *env) client(t *testing.T) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: e.ts.URL, http: &http.Client{Jar: jar}, ua: "connecta-test"}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (r response) errorCode(t *testing.T) (string, map[string]string) {
	t.Helper()
	var body errorBody
	r.json(t, &body)
	return body.Error.Code, body.Error.Fields
}

func (c *client) do(method, path string, body any, headers ...string) response {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, ok := body.(string)
		if !ok {
			b, err := json.Marshal(body)
			if err != nil {
				c.t.Fatal(err)
			}
			raw = string(b)
		}
		reader = strings.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", c.ua)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return response{status: res.StatusCode, header: res.Header, body: bytes.TrimSpace(data)}
}

func (c *client) refreshCookie() string {
	c.t.Helper()
	u, _ := url.Parse(c.base + refreshPath + "/refresh")
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == refreshCookie {
			return ck.Value
		}
	}
	return ""
}

func (c *client) setRefreshCookie(value string) {
	u, _ := url.Parse(c.base + refreshPath + "/refresh")
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: refreshCookie, Value: value, Path: refreshPath}})
}

type creds struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func newCreds() creds {
	n := userSeq.Add(1)
	return creds{
		Email:    fmt.Sprintf("user%d@example.com", n),
		Username: fmt.Sprintf("user_%d", n),
		Password: "correct horse battery",
	}
}

func (c *client) register(cr creds) authResponse {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/auth/register", map[string]string{
		"email":    cr.Email,
		"username": cr.Username,
		"password": cr.Password,
	})
	if res.status != http.StatusCreated {
		c.t.Fatalf("register: status %d, body %s", res.status, res.body)
	}
	var out authResponse
	res.json(c.t, &out)
	c.token = out.AccessToken
	c.user = out.User
	return out
}

func (c *client) login(cr creds) authResponse {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/auth/login", map[string]string{"email": cr.Email, "password": cr.Password})
	if res.status != http.StatusOK {
		c.t.Fatalf("login: status %d, body %s", res.status, res.body)
	}
	var out authResponse
	res.json(c.t, &out)
	c.token = out.AccessToken
	c.user = out.User
	return out
}

func (c *client) refresh() response {
	c.t.Helper()
	res := c.do(http.MethodPost, "/api/auth/refresh", nil)
	if res.status == http.StatusOK {
		var out authResponse
		res.json(c.t, &out)
		c.token = out.AccessToken
	}
	return res
}

func expectStatus(t *testing.T, res response, want int) {
	t.Helper()
	if res.status != want {
		t.Fatalf("status = %d, want %d, body %s", res.status, want, res.body)
	}
}

func expectError(t *testing.T, res response, status int, code string) map[string]string {
	t.Helper()
	expectStatus(t, res, status)
	got, fields := res.errorCode(t)
	if got != code {
		t.Fatalf("error code = %q, want %q", got, code)
	}
	return fields
}
