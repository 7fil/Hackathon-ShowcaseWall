package config_test

import (
	"strings"
	"testing"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/config"
)

// baseEnv is a valid production-like environment; tests override single keys.
func setEnv(t *testing.T, over map[string]string) {
	t.Helper()
	env := map[string]string{
		"APP_ENV":        "production",
		"DATABASE_URL":   "postgres://u:p@db:5432/x",
		"REDIS_URL":      "redis://redis:6379/0",
		"JWT_SECRET":     strings.Repeat("s", 32),
		"RESEND_API_KEY": "re_key",
		"RESEND_FROM":    "A <a@example.com>",
		"COOKIE_SECURE":  "true",
		"DEV_PRINT_OTP":  "false",
	}
	for k, v := range over {
		env[k] = v
	}
	for _, k := range []string{"LOG_LEVEL", "LOG_FORMAT", "APP_VERSION", "COOKIE_SAMESITE"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadAcceptsEachEnvironment(t *testing.T) {
	for _, env := range []string{"development", "staging", "production"} {
		setEnv(t, map[string]string{"APP_ENV": env})
		c, err := config.Load()
		if err != nil {
			t.Fatalf("%s: %v", env, err)
		}
		if c.Env != env {
			t.Errorf("Env = %q, want %q", c.Env, env)
		}
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	setEnv(t, map[string]string{"APP_ENV": "prod"})
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("a typo in APP_ENV must fail loudly, got %v", err)
	}
}

func TestDevShortcutsRefusedWhenDeployed(t *testing.T) {
	for _, env := range []string{"staging", "production"} {
		setEnv(t, map[string]string{"APP_ENV": env, "DEV_PRINT_OTP": "true"})
		if _, err := config.Load(); err == nil {
			t.Errorf("%s must refuse DEV_PRINT_OTP=true", env)
		}
		setEnv(t, map[string]string{"APP_ENV": env, "RESEND_API_KEY": ""})
		if _, err := config.Load(); err == nil {
			t.Errorf("%s must require the mail provider", env)
		}
	}
	setEnv(t, map[string]string{"APP_ENV": "development", "DEV_PRINT_OTP": "true", "RESEND_API_KEY": "", "RESEND_FROM": ""})
	if _, err := config.Load(); err != nil {
		t.Errorf("development may print codes: %v", err)
	}
}

func TestProductionRequiresSecureCookie(t *testing.T) {
	setEnv(t, map[string]string{"COOKIE_SECURE": "false"})
	if _, err := config.Load(); err == nil {
		t.Fatal("production must refuse COOKIE_SECURE=false")
	}
}

func TestLogSettingsValidated(t *testing.T) {
	setEnv(t, map[string]string{"LOG_LEVEL": "WARN", "LOG_FORMAT": "JSON", "APP_VERSION": "abc"})
	c, err := config.Load()
	if err != nil || c.LogLevel != "warn" || c.LogFormat != "json" || c.Version != "abc" {
		t.Fatalf("got %+v, %v", c, err)
	}
	setEnv(t, map[string]string{"LOG_LEVEL": "verbose"})
	if _, err := config.Load(); err == nil {
		t.Error("unknown LOG_LEVEL must be rejected")
	}
}

func TestLoadSeedNeedsOnlyDatabase(t *testing.T) {
	for _, k := range []string{"APP_ENV", "REDIS_URL", "JWT_SECRET", "RESEND_API_KEY", "RESEND_FROM"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@db/x")
	if _, err := config.LoadSeed(); err != nil {
		t.Fatalf("seed must not demand API-only settings: %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	if _, err := config.LoadSeed(); err == nil {
		t.Fatal("seed must require DATABASE_URL")
	}
}
