package api

import (
	"strings"
	"testing"
)

func TestValidEmail(t *testing.T) {
	cases := map[string]bool{
		"user@example.com":                 true,
		"first.last@sub.domain.ru":         true,
		"user+tag@example.co":              true,
		"":                                 false,
		"user":                             false,
		"user@localhost":                   false,
		"user@example.":                    false,
		"user @example.com":                false,
		"User <user@example.com>":          false,
		strings.Repeat("a", 250) + "@b.co": false,
	}
	for email, want := range cases {
		if got := validEmail(email); got != want {
			t.Errorf("validEmail(%q) = %v, want %v", email, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	if got := normalizeEmail("  Foo@Bar.COM "); got != "foo@bar.com" {
		t.Errorf("normalizeEmail = %q", got)
	}
	if got := normalizeUsername(" @Timur_01 "); got != "timur_01" {
		t.Errorf("normalizeUsername = %q", got)
	}
}

func TestPasswordProblem(t *testing.T) {
	cases := map[string]string{
		"1234567":                "too_short",
		"12345678":               "",
		"пароль12":               "",
		strings.Repeat("я", 128): "",
		strings.Repeat("я", 129): "too_long",
	}
	for password, want := range cases {
		if got := passwordProblem(password); got != want {
			t.Errorf("passwordProblem(len %d) = %q, want %q", len([]rune(password)), got, want)
		}
	}
}
