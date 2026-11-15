// Package logger sets up JSON slog output and helpers that keep secrets out of logs.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

func New(env string) *slog.Logger {
	level := slog.LevelInfo
	if env != "production" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// MaskEmail turns "alice@example.com" into "a***@example.com".
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
