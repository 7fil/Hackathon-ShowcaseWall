package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
)

// generatedID is what RequestID() produces when it replaces an unusable incoming ID.
var generatedID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// requestIDEcho sends one request through the RequestID middleware and returns the
// X-Request-ID that came back. An empty incoming value means "send no header".
func requestIDEcho(t *testing.T, incoming string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if incoming != "" {
		req.Header.Set("X-Request-ID", incoming)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Header().Get("X-Request-ID")
}

func TestValidRequestID(t *testing.T) {
	ok := []string{"abc123", "req-1_A", strings.Repeat("a", 64)}
	bad := []string{"", strings.Repeat("a", 65), "a b", "a\nb", `a"b`, "a;b", "ünï"}

	for _, id := range ok {
		if got := requestIDEcho(t, id); got != id {
			t.Errorf("%q should be accepted and echoed unchanged, got %q", id, got)
		}
	}
	for _, id := range bad {
		got := requestIDEcho(t, id)
		if got == id {
			t.Errorf("%q must be rejected (log injection / oversized), but it was echoed back", id)
		}
		if !generatedID.MatchString(got) {
			t.Errorf("%q must be replaced by a generated ID, got %q", id, got)
		}
	}
}
