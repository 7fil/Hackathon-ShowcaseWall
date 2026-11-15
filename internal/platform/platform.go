// Package platform wires configuration, JSON slog logging, Prometheus metrics,
// shared HTTP middleware, and PostgreSQL / Redis connections.
package platform

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/config"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/database"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

type Platform struct {
	Config  config.Config
	Logger  *slog.Logger
	DB      *gorm.DB
	Redis   *redis.Client
	Limiter *cache.Limiter
}

// Init loads config, initializes slog, opens and migrates PostgreSQL, and connects to Redis.
func Init(ctx context.Context) (*Platform, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := logger.New(logger.Options{
		Env: cfg.Env, Version: cfg.Version, Level: cfg.LogLevel, Format: cfg.LogFormat,
	})
	slog.SetDefault(log)
	log.Info("starting",
		"go_version", runtime.Version(), "port", cfg.Port,
		"log_level", logger.ParseLevel(cfg.LogLevel, logger.DefaultLevel(cfg.Env)).String())

	db, err := database.Open(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx, db); err != nil {
		return nil, err
	}
	rdb, err := cache.NewRedis(ctx, cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	return &Platform{
		Config:  cfg,
		Logger:  log,
		DB:      db,
		Redis:   rdb,
		Limiter: cache.NewLimiter(rdb),
	}, nil
}

// NewMetricsServer builds the internal-only Prometheus scrape server (e.g. :9100).
func NewMetricsServer(port string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
