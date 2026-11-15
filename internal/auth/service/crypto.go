package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

// newCode returns a 6-digit code from a cryptographic random source.
func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashCode is HMAC-SHA256 over purpose, email and code, keyed with a server secret,
// so a code only verifies for the flow and address it was issued for.
func hashCode(secret []byte, purpose Purpose, email, code string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(string(purpose) + "\x00" + email + "\x00" + code))
	return hex.EncodeToString(m.Sum(nil))
}

// newRefreshToken returns an opaque 256-bit token for the cookie.
func newRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the SHA-256 hex digest stored in place of a refresh token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewUUID returns a random (version 4) UUID string.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
