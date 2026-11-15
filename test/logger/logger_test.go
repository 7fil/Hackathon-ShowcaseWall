package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
)

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not valid JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestProductionIsJSONInfoWithIdentity(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Env: logger.EnvProduction, Version: "abc123", Output: &buf})
	log.Debug("hidden")
	log.Info("hello", "k", "v")

	got := lines(t, &buf)
	if len(got) != 1 {
		t.Fatalf("want 1 line (debug filtered), got %d: %s", len(got), buf.String())
	}
	m := got[0]
	for k, want := range map[string]string{"msg": "hello", "level": "INFO", "service": "showcase-api", "env": "production", "version": "abc123", "k": "v"} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	ts, _ := m["time"].(string)
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("time must be UTC RFC3339, got %q", ts)
	}
}

func TestStagingIsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger.New(logger.Options{Env: logger.EnvStaging, Output: &buf}).Info("x")
	if got := lines(t, &buf); len(got) != 1 || got[0]["env"] != "staging" {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestDevelopmentIsTextDebug(t *testing.T) {
	var buf bytes.Buffer
	logger.New(logger.Options{Env: logger.EnvDevelopment, Output: &buf}).Debug("dbg")
	out := buf.String()
	if !strings.Contains(out, "level=DEBUG") || strings.HasPrefix(out, "{") {
		t.Fatalf("development should be readable text at debug level, got %q", out)
	}
}

func TestLevelAndFormatOverrides(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Env: logger.EnvProduction, Level: "warn", Output: &buf})
	log.Info("no")
	log.Warn("yes")
	if got := lines(t, &buf); len(got) != 1 || got[0]["msg"] != "yes" {
		t.Fatalf("LOG_LEVEL=warn not honoured: %s", buf.String())
	}
	if logger.ParseLevel("nonsense", slog.LevelInfo) != slog.LevelInfo {
		t.Error("unknown level must fall back to the default")
	}
}

func TestSecretsAreRedactedByKey(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Env: logger.EnvProduction, Output: &buf})
	log.Info("x",
		"password", "hunter2", "Authorization", "Bearer abc", "refresh_token", "r",
		"code", "123456", "cookie", "c", "user_id", "u1",
		slog.Group("req", slog.String("token", "t")),
	)
	out := buf.String()
	for _, leak := range []string{"hunter2", "Bearer abc", "123456", `"t"`} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %s", leak, out)
		}
	}
	m := lines(t, &buf)[0]
	if m["password"] != logger.Redacted || m["user_id"] != "u1" {
		t.Errorf("unexpected redaction result: %v", m)
	}
}

func TestEmailMasking(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Env: logger.EnvProduction, Output: &buf})
	log.Info("x", "email", "alice@example.com", "masked", logger.MaskEmail("bob@example.com"))
	m := lines(t, &buf)[0]
	if m["email"] != "a***@example.com" {
		t.Errorf("email not masked: %v", m["email"])
	}
	if logger.MaskEmail("a***@example.com") != "a***@example.com" {
		t.Error("masking must be idempotent")
	}
	if logger.MaskEmail("not-an-email") != "***" {
		t.Error("non-email input must be fully masked")
	}
}
