package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	authhandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/handler"
	authrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/repository"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/security"
	authsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/config"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/database"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.Env)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Pending migrations are applied before the API serves traffic.
	if err := database.Migrate(ctx, db); err != nil {
		return err
	}
	rdb, err := cache.NewRedis(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer rdb.Close()

	limiter := cache.NewLimiter(rdb)

	var mailer authsvc.Mailer
	if cfg.DevPrintOTP {
		log.Warn("DEV_PRINT_OTP is on: login codes are printed to the console")
		mailer = authrepo.ConsoleMailer{}
	} else {
		mailer = authrepo.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom)
	}

	var idTokens authsvc.IDTokenVerifier
	if cfg.FirebaseEnabled() {
		creds, err := cfg.FirebaseCredentials()
		if err != nil {
			return err
		}
		v, err := authrepo.NewFirebaseVerifier(ctx, creds, cfg.FirebaseProjectID)
		if err != nil {
			return err
		}
		idTokens = v
	} else {
		log.Warn("firebase credentials not set: POST /auth/oauth/firebase will answer 503")
	}

	svc, err := authsvc.New(authsvc.Deps{
		Users:       authrepo.NewUserRepo(db),
		Sessions:    authrepo.NewSessionRepo(db),
		Codes:       authrepo.NewCodeRepo(rdb),
		Limiter:     limiter,
		Mailer:      mailer,
		Hasher:      security.NewBcrypt(),
		Tokens:      security.NewJWT(cfg.JWTSecret, authsvc.AccessTTL),
		Logger:      log,
		IDTokens:    idTokens,
		AdminEmails: cfg.AdminEmails,
		CodeSecret:  []byte(cfg.JWTSecret),
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return err
	}
	r.Use(middleware.RequestID(), middleware.AccessLog(log), middleware.Recovery(log), middleware.CORS(cfg.FrontendOrigin))

	r.GET("/health", func(c *gin.Context) { response.OK(c, http.StatusOK, gin.H{"status": "ok"}) })

	v1 := r.Group("/api/v1")
	authhandler.New(svc, authhandler.CookieConfig{Secure: cfg.CookieSecure, SameSite: cfg.CookieSameSite}, cfg.FrontendOrigin).
		Routes(v1, limiter, log, middleware.Auth(svc))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
