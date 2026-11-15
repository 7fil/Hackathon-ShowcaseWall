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
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/platform"
	rthandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/handler"
	rtrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/repository"
	rtsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/service"
	workshandler "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/handler"
	worksrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/repository"
	workssvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

func main() {
	// Until the config is loaded, startup failures are still logged in the right shape
	// (JSON in staging and production) so a log collector can read them.
	slog.SetDefault(logger.New(logger.Options{Env: os.Getenv("APP_ENV"), Version: os.Getenv("APP_VERSION")}))
	if err := run(); err != nil {
		slog.Error("fatal: api stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plat, err := platform.Init(ctx)
	if err != nil {
		return err
	}
	defer plat.Redis.Close()

	cfg := plat.Config
	log := plat.Logger
	db := plat.DB
	rdb := plat.Redis
	limiter := plat.Limiter

	var mailer authsvc.Mailer
	if cfg.DevPrintOTP {
		log.Warn("DEV_PRINT_OTP is on: login codes are printed to the console (never use outside development)")
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
		Users:      authrepo.NewUserRepo(db),
		Sessions:   authrepo.NewSessionRepo(db),
		Codes:      authrepo.NewCodeRepo(rdb),
		Limiter:    limiter,
		Mailer:     mailer,
		Hasher:     security.NewBcrypt(),
		Tokens:     security.NewJWT(cfg.JWTSecret, authsvc.AccessTTL),
		Logger:     log,
		IDTokens:   idTokens,
		CodeSecret: []byte(cfg.JWTSecret),
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	// Release mode everywhere except development: no route dump or debug text on stdout.
	if !cfg.IsDevelopment() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return err
	}
	r.Use(middleware.RequestID(), middleware.AccessLog(log), middleware.Recovery(log), middleware.CORS(cfg.FrontendOrigin))

	// ---- media: object storage + image rules --------------------------------
	storage, err := mediarepo.NewLocalDiskStorage(cfg.MediaLocalDir)
	if err != nil {
		return err
	}
	log.Info("object storage: local disk", "dir", cfg.MediaLocalDir)
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
	mediaService.SetWorkLookup(worksCoverLookup{works: worksService})
	mediaService.SetScreenshotLookup(worksScreenshotLookup{works: worksService})

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

	// ---- realtime ------------------------------------------------------------
	wsHub := rtsvc.NewHub(rtsvc.Deps{
		Subscriber:     rtrepo.NewRedisSubscriber(rdb),
		Limiter:        limiter,
		FrontendOrigin: cfg.FrontendOrigin,
		Logger:         log,
	})
	go wsHub.Run(ctx)

	r.GET("/health", func(c *gin.Context) { response.OK(c, http.StatusOK, gin.H{"status": "ok"}) })

	requireAuth := middleware.Auth(svc)
	v1 := r.Group("/api/v1")
	authhandler.New(svc, authhandler.CookieConfig{Secure: cfg.CookieSecure, SameSite: cfg.CookieSameSite}, cfg.FrontendOrigin).
		Routes(v1, limiter, log, requireAuth)
	workshandler.New(worksService).Routes(v1, requireAuth)
	adminhandler.New(adminService).Routes(v1, requireAuth)
	mediahandler.New(mediaService).Routes(v1)
	rthandler.New(wsHub).Routes(r, v1)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	metricsSrv := platform.NewMetricsServer(cfg.MetricsPort)

	errCh := make(chan error, 2)
	go func() {
		log.Info("api listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()
	go func() {
		log.Info("metrics listening", "addr", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server error", "error", err.Error())
		}
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
		_ = metricsSrv.Shutdown(shutdownCtx)
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
