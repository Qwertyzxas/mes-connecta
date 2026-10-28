package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}

	ok, err := VerifyPassword("correct horse battery", hash)
	if err != nil || !ok {
		t.Fatalf("valid password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong", hash)
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}
}

func TestPasswordHashIsSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=18$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=x$c2FsdA$a2V5"} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("VerifyPassword accepted malformed hash %q", h)
		}
	}
}

func TestTokensRoundTrip(t *testing.T) {
	tokens := NewTokens(bytes.Repeat([]byte("k"), 32), "connecta", time.Minute)
	signed, exp, err := tokens.Issue("user-1", "session-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) <= 0 || time.Until(exp) > time.Minute {
		t.Fatalf("unexpected expiry %v", exp)
	}

	claims, err := tokens.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.SessionID != "session-1" {
		t.Fatalf("unexpected claims %+v", claims)
	}
}

func TestTokensRejectInvalid(t *testing.T) {
	secret := bytes.Repeat([]byte("k"), 32)
	tokens := NewTokens(secret, "connecta", time.Minute)

	expired, _, _ := tokens.Issue("u", "s", time.Now().Add(-time.Hour))
	if _, err := tokens.Parse(expired); err == nil {
		t.Error("expired token accepted")
	}

	foreign, _, _ := NewTokens(bytes.Repeat([]byte("x"), 32), "connecta", time.Minute).Issue("u", "s", time.Now())
	if _, err := tokens.Parse(foreign); err == nil {
		t.Error("token signed with another secret accepted")
	}

	otherIssuer, _, _ := NewTokens(secret, "someone-else", time.Minute).Issue("u", "s", time.Now())
	if _, err := tokens.Parse(otherIssuer); err == nil {
		t.Error("token from another issuer accepted")
	}

	noSession, _, _ := tokens.Issue("u", "", time.Now())
	if _, err := tokens.Parse(noSession); err == nil {
		t.Error("token without session id accepted")
	}

	valid, _, _ := tokens.Issue("u", "s", time.Now())
	parts := strings.Split(valid, ".")
	if _, err := tokens.Parse(parts[0] + "." + parts[1] + ".tampered"); err == nil {
		t.Error("tampered signature accepted")
	}
}

func TestRefreshTokens(t *testing.T) {
	a, hashA, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewRefreshToken()
	if a == b {
		t.Fatal("refresh tokens are not unique")
	}
	if len(a) != 43 {
		t.Fatalf("refresh token length = %d, want 43", len(a))
	}
	if !bytes.Equal(hashA, HashRefreshToken(a)) || len(hashA) != 32 {
		t.Fatal("refresh token hash mismatch")
	}
}
