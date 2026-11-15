// Package logger builds the application's slog logger.
//
// Production and staging behave the way log pipelines expect: one JSON object per
// line on stdout (Docker collects and rotates it), UTC RFC 3339 timestamps, a fixed
// set of identifying fields on every line (service, env, version), a configurable
// level (info by default), and automatic redaction of secrets by attribute name so
// a careless call site cannot leak a password or token.
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Environment names accepted for APP_ENV.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// Redacted replaces the value of a sensitive attribute.
const Redacted = "[REDACTED]"

// Options configures New.
type Options struct {
	Env     string    // development | staging | production
	Service string    // reported as "service"; defaults to "showcase-api"
	Version string    // reported as "version" (image tag or git SHA); empty omits it
	Level   string    // debug | info | warn | error; empty picks the default for Env
	Format  string    // json | text; empty picks the default for Env
	Output  io.Writer // defaults to os.Stdout
}

// New returns the logger for the given options. Unknown levels fall back to the
// environment default instead of failing, because logging must never stop startup.
func New(o Options) *slog.Logger {
	if o.Output == nil {
		o.Output = os.Stdout
	}
	if o.Service == "" {
		o.Service = "showcase-api"
	}
	hopts := &slog.HandlerOptions{
		Level:       ParseLevel(o.Level, DefaultLevel(o.Env)),
		ReplaceAttr: replaceAttr,
	}

	format := strings.ToLower(strings.TrimSpace(o.Format))
	if format == "" {
		format = "json"
		if o.Env == EnvDevelopment || o.Env == "" {
			format = "text"
		}
	}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(o.Output, hopts)
	} else {
		h = slog.NewJSONHandler(o.Output, hopts)
	}

	attrs := []any{"service", o.Service, "env", envOrUnknown(o.Env)}
	if o.Version != "" {
		attrs = append(attrs, "version", o.Version)
	}
	return slog.New(h).With(attrs...)
}

// DefaultLevel is info everywhere except development, where debug helps.
func DefaultLevel(env string) slog.Level {
	if env == EnvDevelopment || env == "" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// ParseLevel converts a LOG_LEVEL value, returning def when it is empty or unknown.
func ParseLevel(s string, def slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return def
	}
}

func envOrUnknown(env string) string {
	if env == "" {
		return "unknown"
	}
	return env
}

// sensitiveKeys are attribute names whose values are never written. Matching is on the
// lower-cased key, so "Password" and "password" are both caught.
var sensitiveKeys = map[string]struct{}{
	"password": {}, "passwd": {}, "pass": {}, "password_hash": {},
	"token": {}, "access_token": {}, "refresh_token": {}, "id_token": {}, "token_hash": {},
	"authorization": {}, "cookie": {}, "set-cookie": {},
	"secret": {}, "jwt_secret": {}, "api_key": {}, "apikey": {}, "private_key": {},
	"code": {}, "otp": {},
	"credentials": {}, "dsn": {}, "database_url": {}, "redis_url": {},
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.UTC().Format(time.RFC3339Nano))
		}
		return a
	}
	key := strings.ToLower(a.Key)
	if _, bad := sensitiveKeys[key]; bad {
		return slog.String(a.Key, Redacted)
	}
	if key == "email" && a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, MaskEmail(a.Value.String()))
	}
	return a
}

// MaskEmail turns "alice@example.com" into "a***@example.com".
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	// Already masked by the caller: do not mask twice.
	if strings.Contains(email[:at], "***") {
		return email
	}
	return email[:1] + "***" + email[at:]
}
