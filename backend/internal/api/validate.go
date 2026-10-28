package api

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const (
	minPasswordLen    = 8
	maxPasswordLen    = 128
	maxDisplayNameLen = 64
	maxEmailLen       = 254
)

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeUsername(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@"))
}

func validEmail(email string) bool {
	if email == "" || len(email) > maxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	domain := email[at+1:]
	return strings.Contains(domain, ".") && !strings.HasSuffix(domain, ".")
}

func passwordProblem(password string) string {
	n := utf8.RuneCountInString(password)
	switch {
	case n < minPasswordLen:
		return "too_short"
	case n > maxPasswordLen:
		return "too_long"
	}
	return ""
}
