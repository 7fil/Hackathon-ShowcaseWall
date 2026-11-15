package middleware

import (
	"strings"
	"testing"
)

func TestValidRequestID(t *testing.T) {
	ok := []string{"abc123", "req-1_A", strings.Repeat("a", 64)}
	bad := []string{"", strings.Repeat("a", 65), "a b", "a\nb", `a"b`, "a;b", "ünï"}
	for _, id := range ok {
		if !validRequestID(id) {
			t.Errorf("%q should be accepted", id)
		}
	}
	for _, id := range bad {
		if validRequestID(id) {
			t.Errorf("%q must be rejected (log injection / oversized)", id)
		}
	}
}
