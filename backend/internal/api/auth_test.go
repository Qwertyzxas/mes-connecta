package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"connecta/internal/store"
)

func TestHealth(t *testing.T) {
	e := setup(t)
	res := e.client(t).do(http.MethodGet, "/api/health", nil)
	expectStatus(t, res, http.StatusOK)
}

func TestRegisterCreatesUserAndSession(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	cr := newCreds()
	cr.Email = "  Mixed.Case@Example.COM "
	cr.Username = "@Timur_Dev"

	out := c.register(cr)

	if out.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if out.User.Email != "mixed.case@example.com" {
		t.Errorf("email = %q, want normalized lowercase", out.User.Email)
	}
	if out.User.Username != "timur_dev" {
		t.Errorf("username = %q, want timur_dev", out.User.Username)
	}
	if out.User.DisplayName != "timur_dev" {
		t.Errorf("display name = %q, want username fallback", out.User.DisplayName)
	}
	if c.refreshCookie() == "" {
		t.Fatal("refresh cookie was not set")
	}

	res := c.do(http.MethodGet, "/api/me", nil)
	expectStatus(t, res, http.StatusOK)
	var me store.User
	res.json(t, &me)
	if me.ID != out.User.ID {
		t.Errorf("me.id = %q, want %q", me.ID, out.User.ID)
	}
}

func TestRegisterSetsHardenedCookie(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	cr := newCreds()
	res := c.do(http.MethodPost, "/api/auth/register", cr)
	expectStatus(t, res, http.StatusCreated)

	header := res.header.Get("Set-Cookie")
	for _, want := range []string{"refresh_token=", "Path=/api/auth", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(header, want) {
			t.Errorf("Set-Cookie %q does not contain %q", header, want)
		}
	}
	if got := res.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestRegisterValidation(t *testing.T) {
	e := setup(t)
	c := e.client(t)

	cases := []struct {
		name  string
		body  map[string]string
		field string
		code  string
	}{
		{"bad email", map[string]string{"email": "not-an-email", "username": "valid_name", "password": "password1"}, "email", "invalid"},
		{"email without tld", map[string]string{"email": "a@localhost", "username": "valid_name", "password": "password1"}, "email", "invalid"},
		{"short username", map[string]string{"email": "a@b.co", "username": "ab", "password": "password1"}, "username", "invalid"},
		{"cyrillic username", map[string]string{"email": "a@b.co", "username": "тимур", "password": "password1"}, "username", "invalid"},
		{"short password", map[string]string{"email": "a@b.co", "username": "valid_name", "password": "1234567"}, "password", "too_short"},
		{"long display name", map[string]string{"email": "a@b.co", "username": "valid_name", "password": "password1", "display_name": strings.Repeat("я", 65)}, "display_name", "too_long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c.t = t
			fields := expectError(t, c.do(http.MethodPost, "/api/auth/register", tc.body), http.StatusUnprocessableEntity, "validation_failed")
			if fields[tc.field] != tc.code {
				t.Fatalf("fields = %v, want %s=%s", fields, tc.field, tc.code)
			}
		})
	}
}

func TestRegisterRejectsMalformedBody(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	expectError(t, c.do(http.MethodPost, "/api/auth/register", `{"email":`), http.StatusBadRequest, "bad_request")
	expectError(t, c.do(http.MethodPost, "/api/auth/register", `{"email":"a@b.co","admin":true}`), http.StatusBadRequest, "bad_request")
	expectError(t, c.do(http.MethodPost, "/api/auth/register", `{} {}`), http.StatusBadRequest, "bad_request")
	big := `{"email":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	expectError(t, c.do(http.MethodPost, "/api/auth/register", big), http.StatusRequestEntityTooLarge, "body_too_large")
}

func TestRegisterDuplicates(t *testing.T) {
	e := setup(t)
	cr := newCreds()
	e.client(t).register(cr)

	dupEmail := newCreds()
	dupEmail.Email = strings.ToUpper(cr.Email)
	expectError(t, e.client(t).do(http.MethodPost, "/api/auth/register", dupEmail), http.StatusConflict, "email_taken")

	dupUsername := newCreds()
	dupUsername.Username = strings.ToUpper(cr.Username)
	expectError(t, e.client(t).do(http.MethodPost, "/api/auth/register", dupUsername), http.StatusConflict, "username_taken")
}

func TestLogin(t *testing.T) {
	e := setup(t)
	cr := newCreds()
	e.client(t).register(cr)

	t.Run("wrong password", func(t *testing.T) {
		res := e.client(t).do(http.MethodPost, "/api/auth/login", map[string]string{"email": cr.Email, "password": "wrong password"})
		expectError(t, res, http.StatusUnauthorized, "invalid_credentials")
	})

	t.Run("unknown email has the same answer", func(t *testing.T) {
		res := e.client(t).do(http.MethodPost, "/api/auth/login", map[string]string{"email": "nobody@example.com", "password": cr.Password})
		expectError(t, res, http.StatusUnauthorized, "invalid_credentials")
	})

	t.Run("email is case insensitive", func(t *testing.T) {
		c := e.client(t)
		upper := cr
		upper.Email = strings.ToUpper(cr.Email)
		c.login(upper)
		expectStatus(t, c.do(http.MethodGet, "/api/me", nil), http.StatusOK)
	})
}

func TestLoginRateLimitedPerEmail(t *testing.T) {
	e := setup(t)
	cr := newCreds()
	e.client(t).register(cr)
	c := e.client(t)

	body := map[string]string{"email": cr.Email, "password": "wrong password"}
	for i := 0; i < 10; i++ {
		expectError(t, c.do(http.MethodPost, "/api/auth/login", body), http.StatusUnauthorized, "invalid_credentials")
	}
	res := c.do(http.MethodPost, "/api/auth/login", body)
	expectError(t, res, http.StatusTooManyRequests, "rate_limited")
	retry, err := strconv.Atoi(res.header.Get("Retry-After"))
	if err != nil || retry <= 0 || retry > int((15*time.Minute).Seconds()) {
		t.Fatalf("Retry-After = %q, want seconds within the window", res.header.Get("Retry-After"))
	}

	expectError(t, c.do(http.MethodPost, "/api/auth/login", map[string]string{"email": cr.Email, "password": cr.Password}),
		http.StatusTooManyRequests, "rate_limited")
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	expectError(t, c.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")

	c.token = "not.a.jwt"
	expectError(t, c.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")

	expired, _, err := e.srv.tokens.Issue("00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c.token = expired
	expectError(t, c.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")
}

func TestRefreshRotatesToken(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	first := c.refreshCookie()

	expectStatus(t, c.refresh(), http.StatusOK)
	second := c.refreshCookie()
	if second == "" || second == first {
		t.Fatalf("refresh cookie was not rotated")
	}
	expectStatus(t, c.do(http.MethodGet, "/api/me", nil), http.StatusOK)
	expectStatus(t, c.refresh(), http.StatusOK)
}

func TestRefreshWithoutCookie(t *testing.T) {
	e := setup(t)
	expectError(t, e.client(t).refresh(), http.StatusUnauthorized, "no_session")
}

func TestRefreshWithinGraceReissuesToken(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	stale := c.refreshCookie()
	expectStatus(t, c.refresh(), http.StatusOK)

	lostResponse := e.client(t)
	lostResponse.setRefreshCookie(stale)
	expectStatus(t, lostResponse.refresh(), http.StatusOK)
	reissued := lostResponse.refreshCookie()
	if reissued == "" || reissued == stale {
		t.Fatalf("cookie was not reissued: %q", reissued)
	}
	expectStatus(t, lostResponse.do(http.MethodGet, "/api/me", nil), http.StatusOK)
	expectStatus(t, lostResponse.refresh(), http.StatusOK)

	e.srv.refreshGrace = 0
	thief := e.client(t)
	thief.setRefreshCookie(reissued)
	expectError(t, thief.refresh(), http.StatusUnauthorized, "session_expired")
	expectError(t, lostResponse.refresh(), http.StatusUnauthorized, "session_expired")
}

func TestRefreshReuseRevokesSession(t *testing.T) {
	e := setup(t)
	e.srv.refreshGrace = 0
	victim := e.client(t)
	victim.register(newCreds())
	stolen := victim.refreshCookie()
	expectStatus(t, victim.refresh(), http.StatusOK)

	attacker := e.client(t)
	attacker.setRefreshCookie(stolen)
	expectError(t, attacker.refresh(), http.StatusUnauthorized, "session_expired")

	expectError(t, victim.refresh(), http.StatusUnauthorized, "session_expired")
	expectError(t, victim.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")
}

func TestLogout(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.register(newCreds())
	cookie := c.refreshCookie()

	res := c.do(http.MethodPost, "/api/auth/logout", nil)
	expectStatus(t, res, http.StatusNoContent)
	if c.refreshCookie() != "" {
		t.Error("refresh cookie was not cleared")
	}
	expectError(t, c.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")

	replay := e.client(t)
	replay.setRefreshCookie(cookie)
	expectError(t, replay.refresh(), http.StatusUnauthorized, "session_expired")

	expectStatus(t, e.client(t).do(http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent)
}

func TestSessionsListAndRevoke(t *testing.T) {
	e := setup(t)
	cr := newCreds()
	phone := e.client(t)
	phone.ua = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"
	phone.register(cr)
	laptop := e.client(t)
	laptop.ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/141.0"
	laptop.login(cr)

	res := phone.do(http.MethodGet, "/api/sessions", nil)
	expectStatus(t, res, http.StatusOK)
	var sessions []sessionView
	res.json(t, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}

	var laptopID string
	current := 0
	for _, s := range sessions {
		if s.Current {
			current++
			if s.UserAgent != phone.ua {
				t.Errorf("current session user agent = %q, want phone", s.UserAgent)
			}
		} else {
			laptopID = s.ID
		}
	}
	if current != 1 || laptopID == "" {
		t.Fatalf("expected exactly one current session, got %+v", sessions)
	}

	expectStatus(t, phone.do(http.MethodDelete, "/api/sessions/"+laptopID, nil), http.StatusNoContent)
	expectError(t, laptop.do(http.MethodGet, "/api/me", nil), http.StatusUnauthorized, "unauthorized")
	expectError(t, laptop.refresh(), http.StatusUnauthorized, "session_expired")
	expectStatus(t, phone.do(http.MethodGet, "/api/me", nil), http.StatusOK)

	expectError(t, phone.do(http.MethodDelete, "/api/sessions/"+laptopID, nil), http.StatusNotFound, "not_found")
	expectError(t, phone.do(http.MethodDelete, "/api/sessions/not-a-uuid", nil), http.StatusNotFound, "not_found")
}

func TestCannotRevokeForeignSession(t *testing.T) {
	e := setup(t)
	alice := e.client(t)
	alice.register(newCreds())
	bob := e.client(t)
	bob.register(newCreds())

	res := bob.do(http.MethodGet, "/api/sessions", nil)
	var sessions []sessionView
	res.json(t, &sessions)
	if len(sessions) != 1 {
		t.Fatalf("bob sessions = %d, want 1", len(sessions))
	}

	expectError(t, alice.do(http.MethodDelete, "/api/sessions/"+sessions[0].ID, nil), http.StatusNotFound, "not_found")
	expectStatus(t, bob.do(http.MethodGet, "/api/me", nil), http.StatusOK)
}

func TestUnknownRoute(t *testing.T) {
	e := setup(t)
	expectError(t, e.client(t).do(http.MethodGet, "/api/nope", nil), http.StatusNotFound, "not_found")
}
