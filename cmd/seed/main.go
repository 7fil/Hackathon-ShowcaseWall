// Command seed makes registered users Admin. It is the only way to create an Admin:
// no HTTP endpoint does, and no environment variable lists admins.
//
//	docker exec -it showcase-api /app/seed alice@example.com bob@example.com
//
// It needs shell access to the Docker host, which is the access control. The result
// is printed to the terminal and an audit line is written to the container's log
// (`docker logs showcase-api`).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/adminseed"
	authrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/repository"
	authsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/config"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/database"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
)

func main() { os.Exit(run()) }

func run() int {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(adminseed.Usage)
		return 2
	}

	cfg, err := config.LoadSeed()
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 1
	}
	audit := logger.New(logger.Options{
		Env: cfg.Env, Service: "showcase-seed", Version: cfg.Version,
		Level: cfg.LogLevel, Format: "json", Output: auditOutput(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := database.Open(ctx, cfg.DatabaseURL, audit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 1
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	return adminseed.Run(ctx, store{users: authrepo.NewUserRepo(db)}, args, os.Stdout, audit)
}

// auditOutput sends audit lines to the main process's stdout, which is what
// `docker logs` shows, so a grant made through `docker exec` leaves a trace next to the
// API's own logs. Outside a container it falls back to stderr.
func auditOutput() io.Writer {
	if f, err := os.OpenFile("/proc/1/fd/1", os.O_WRONLY, 0); err == nil {
		return f
	}
	return os.Stderr
}

// store adapts the user repository to adminseed.Store.
type store struct{ users *authrepo.UserRepo }

func (s store) PromoteToAdmin(ctx context.Context, email string) (adminseed.Outcome, error) {
	changed, err := s.users.PromoteToAdmin(ctx, email)
	switch {
	case errors.Is(err, authsvc.ErrNotFound):
		return adminseed.NotFound, nil
	case err != nil:
		return "", err
	case changed:
		return adminseed.Promoted, nil
	default:
		return adminseed.AlreadyAdmin, nil
	}
}
