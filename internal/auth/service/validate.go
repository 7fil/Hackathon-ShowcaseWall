package service

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// validEmail accepts a bare address only ("a@b.co"), not "Name <a@b.co>".
func validEmail(email string) bool {
	if email == "" || len(email) > MaxEmailLength {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && strings.Contains(email[at+1:], ".")
}

func validPassword(p string) (string, bool) {
	if utf8.RuneCountInString(p) < MinPasswordRunes {
		return "Password minimal 8 karakter", false
	}
	if len(p) > MaxPasswordBytes {
		return "Password maksimal 72 karakter", false
	}
	return "", true
}

func validCode(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
