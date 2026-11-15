// Command seed creates (or refreshes) the Admin account from ADMIN_EMAIL, ADMIN_PASSWORD
// and ADMIN_NAME. It is the only way to create an Admin: no endpoint does.
package main

import (
	"context"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	authrepo "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/repository"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/security"
	authsvc "github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/config"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadSeed()
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(cfg.AdminPassword) < authsvc.MinPasswordRunes || len(cfg.AdminPassword) > authsvc.MaxPasswordBytes {
		return fmt.Errorf("ADMIN_PASSWORD must be 8 to 72 characters")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if err := database.Migrate(ctx, db); err != nil {
		return err
	}

	hash, err := security.NewBcrypt().Hash(cfg.AdminPassword)
	if err != nil {
		return err
	}
	id, err := authsvc.NewUUID()
	if err != nil {
		return err
	}
	verifiedAt := time.Now()
	err = authrepo.NewUserRepo(db).UpsertAdmin(ctx, &authsvc.User{
		ID: id, Email: cfg.AdminEmail, DisplayName: cfg.AdminName,
		PasswordHash: hash, Role: identity.RoleAdmin, AuthProvider: authsvc.ProviderLocal, EmailVerifiedAt: &verifiedAt,
	})
	if err != nil {
		return err
	}
	fmt.Println("admin account ready")
	return nil
}
