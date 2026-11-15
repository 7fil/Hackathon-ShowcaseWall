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

	adminhandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/handler"
	adminrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/repository"
	adminsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/service"
	authhandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/handler"
	authrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/repository"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/security"
	authsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	mediahandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/handler"
	mediarepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/repository"
	mediasvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
	workshandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/handler"
	worksrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/repository"
	workssvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
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

	// ---- media: object storage + image rules --------------------------------
	var storage mediasvc.ObjectStorage
	if cfg.MinioEndpoint != "" {
		s3Store, err := mediarepo.NewS3Storage(ctx, cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioBucket, cfg.MinioUseSSL)
		if err != nil {
			return err
		}
		storage = s3Store
	} else {
		log.Warn("MINIO_ENDPOINT not set: using local disk storage for covers", "dir", cfg.MediaLocalDir)
		localStore, err := mediarepo.NewLocalDiskStorage(cfg.MediaLocalDir)
		if err != nil {
			return err
		}
		storage = localStore
	}
	mediaService, err := mediasvc.New(mediasvc.Deps{
		Storage:    storage,
		Logger:     log,
		SignSecret: []byte(cfg.JWTSecret),
	})
	if err != nil {
		return err
	}

	// ---- works ---------------------------------------------------------------
	worksRedis := worksrepo.NewRedisCache(rdb)
	worksService := workssvc.New(workssvc.Deps{
		Works:  worksrepo.NewWorkRepo(db),
		Media:  mediaService,
		Cache:  worksRedis,
		Events: worksRedis,
		Logger: log,
	})
	defer worksService.Close()

	// Media decides who may see a cover, but asks the works service about the work.
	// It is bound here because works needs media and media needs works.
	mediaService.SetWorkLookup(worksCoverLookup{works: worksService})

	// ---- admin ---------------------------------------------------------------
	adminRedis := adminrepo.NewRedisCache(rdb)
	adminService, err := adminsvc.New(adminsvc.Deps{
		Works:  adminrepo.NewWorkRepo(db),
		Cache:  adminRedis,
		Events: adminRedis,
		Media:  mediaService,
		Logger: log,
	})
	if err != nil {
		return err
	}

	r.GET("/health", func(c *gin.Context) { response.OK(c, http.StatusOK, gin.H{"status": "ok"}) })

	requireAuth := middleware.Auth(svc)
	v1 := r.Group("/api/v1")
	authhandler.New(svc, authhandler.CookieConfig{Secure: cfg.CookieSecure, SameSite: cfg.CookieSameSite}, cfg.FrontendOrigin).
		Routes(v1, limiter, log, requireAuth)
	workshandler.New(worksService).Routes(v1, requireAuth)
	adminhandler.New(adminService).Routes(v1, requireAuth)
	mediahandler.New(mediaService).Routes(v1)

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
