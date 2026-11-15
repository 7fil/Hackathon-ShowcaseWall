// Package database opens PostgreSQL through Gorm and applies versioned SQL migrations.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Open connects to PostgreSQL. Failed and slow queries are logged to log (nil keeps
// Gorm silent). Queries are logged with placeholders only, never with their values.
func Open(ctx context.Context, url string, log *slog.Logger) (*gorm.DB, error) {
	var gl gormlogger.Interface = gormlogger.Discard
	if log != nil {
		gl = newSlogGorm(log)
	}
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{
		TranslateError: true,
		// Statements carry password hashes and token hashes. The logger drops the bound
		// values (see slogGorm.ParamsFilter), so SQL is logged with "$1" placeholders.
		Logger: gl,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}
