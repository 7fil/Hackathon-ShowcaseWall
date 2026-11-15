package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SlowQuery is the duration above which a query is logged as a warning.
const SlowQuery = 300 * time.Millisecond

const maxSQLLogLen = 500

// slogGorm sends Gorm's diagnostics to slog. It logs only failed and slow queries. It
// implements gorm's ParamsFilter, so the SQL text holds "$1" placeholders and never the
// values: password hashes and token hashes cannot reach the log.
type slogGorm struct {
	log  *slog.Logger
	slow time.Duration
}

func newSlogGorm(log *slog.Logger) gormlogger.Interface {
	return slogGorm{log: log, slow: SlowQuery}
}

// ParamsFilter is called by Gorm before it renders a statement for Trace. Returning no
// parameters makes the rendered SQL keep its placeholders.
func (l slogGorm) ParamsFilter(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

func (l slogGorm) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }

func (l slogGorm) Info(ctx context.Context, msg string, args ...any) {
	l.log.InfoContext(ctx, fmt.Sprintf(msg, args...), "component", "gorm")
}

func (l slogGorm) Warn(ctx context.Context, msg string, args ...any) {
	l.log.WarnContext(ctx, fmt.Sprintf(msg, args...), "component", "gorm")
}

func (l slogGorm) Error(ctx context.Context, msg string, args ...any) {
	l.log.ErrorContext(ctx, fmt.Sprintf(msg, args...), "component", "gorm")
}

func (l slogGorm) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)

	switch {
	case err != nil:
		// Not found, duplicate keys and cancelled requests are normal business flow that the
		// services already handle; they are not database faults.
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gormlogger.ErrRecordNotFound) ||
			errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, context.Canceled) {
			return
		}
		sql, rows := fc()
		l.log.ErrorContext(ctx, "database query failed",
			"component", "gorm", "error", err.Error(), "sql", truncateSQL(sql),
			"rows", rows, "duration_ms", elapsed.Milliseconds())
	case l.slow > 0 && elapsed > l.slow:
		sql, rows := fc()
		l.log.WarnContext(ctx, "slow database query",
			"component", "gorm", "sql", truncateSQL(sql),
			"rows", rows, "duration_ms", elapsed.Milliseconds())
	}
}

func truncateSQL(s string) string {
	if len(s) <= maxSQLLogLen {
		return s
	}
	return s[:maxSQLLogLen] + "..."
}
