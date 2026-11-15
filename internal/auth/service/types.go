// Package service holds the auth business rules. It knows nothing about HTTP, Gin or Gorm:
// every store and client sits behind an interface defined here.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
)

const (
	OTPTTL         = 10 * time.Minute
	MaxOTPAttempts = 5
	AccessTTL      = 15 * time.Minute
	RefreshTTL     = 24 * time.Hour

	// Suggested limits from the backend spec.
	CodePerMinute     = 1
	CodePerHour       = 5
	FailedLoginLimit  = 5
	FailedLoginWindow = 15 * time.Minute
	MinPasswordRunes  = 8
	MaxPasswordBytes  = 72 // bcrypt ignores everything past 72 bytes
	MinNameRunes      = 2
	MaxNameRunes      = 100
	MaxEmailLength    = 254
	CodeLength        = 6
	mailSendTimeout   = 15 * time.Second
)

type Purpose string

const (
	PurposeRegister Purpose = "register"
	PurposeLogin    Purpose = "login"
)

func (p Purpose) Valid() bool { return p == PurposeRegister || p == PurposeLogin }

// Sentinel errors returned by repositories.
var (
	ErrNotFound        = errors.New("not found")
	ErrEmailTaken      = errors.New("email already registered")
	ErrSessionConflict = errors.New("session already retired")
	ErrAlreadyLinked   = errors.New("account already linked to a provider")
	ErrInvalidIDToken  = errors.New("invalid id token")
)

const (
	ProviderLocal  = "local"
	ProviderGoogle = "google"
	ProviderBoth   = "both"
)

type User struct {
	ID              string
	Email           string
	DisplayName     string
	PasswordHash    string // empty for OAuth-only users: they cannot use email+password login
	Role            string
	FirebaseUID     *string
	AuthProvider    string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
}

type Session struct {
	ID         string
	UserID     string
	FamilyID   string
	TokenHash  string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *string
}

type PendingRegistration struct {
	Name         string
	PasswordHash string
}

type CodeStatus int

const (
	CodeOK CodeStatus = iota
	CodeMismatch
	CodeMissing   // expired, never issued or already used
	CodeExhausted // too many wrong attempts; the code is now void
)

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// ---- Interfaces the service depends on ----

type UserRepository interface {
	Create(ctx context.Context, u *User) error // ErrEmailTaken on duplicate
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id string) (*User, error)           // ErrNotFound
	FindByFirebaseUID(ctx context.Context, uid string) (*User, error) // ErrNotFound
	// LinkFirebase sets firebase_uid on a user that has none (ErrAlreadyLinked otherwise),
	// updates the provider and marks the email verified if it was not.
	LinkFirebase(ctx context.Context, userID, uid, provider string, verifiedAt time.Time) error
}

// IDTokenClaims are the fields read from a verified Firebase ID token.
type IDTokenClaims struct {
	UID           string
	Email         string
	Name          string
	EmailVerified bool
}

// IDTokenVerifier checks a Firebase ID token. It returns ErrInvalidIDToken for a
// bad, expired or revoked token and any other error for infrastructure failures.
type IDTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (*IDTokenClaims, error)
}

type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	FindByTokenHash(ctx context.Context, hash string) (*Session, error) // ErrNotFound
	// Rotate inserts next and retires old in one transaction. It returns
	// ErrSessionConflict if old was already retired.
	Rotate(ctx context.Context, oldID string, next *Session) error
	RevokeFamily(ctx context.Context, familyID string) error
}

type CodeStore interface {
	SavePending(ctx context.Context, email string, p PendingRegistration, ttl time.Duration) error
	GetPending(ctx context.Context, email string) (*PendingRegistration, error) // ErrNotFound
	DeletePending(ctx context.Context, email string) error
	// SaveCode stores only the hash and resets the attempt counter.
	SaveCode(ctx context.Context, purpose Purpose, email, codeHash string, ttl time.Duration) error
	HasCode(ctx context.Context, purpose Purpose, email string) (bool, error)
	// ConsumeCode compares hashes in constant time, counts the attempt, and
	// deletes the code on success so it works once.
	ConsumeCode(ctx context.Context, purpose Purpose, email, codeHash string, maxAttempts int) (CodeStatus, error)
}

type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	Count(ctx context.Context, key string) (count int, ttl time.Duration, err error)
	Reset(ctx context.Context, key string) error
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) bool
}

type TokenIssuer interface {
	NewAccessToken(userID, role string) (token string, expiresAt time.Time, err error)
	// ParseAccessToken returns identity.ErrTokenExpired or identity.ErrTokenInvalid.
	ParseAccessToken(token string) (identity.Identity, error)
}

// ---- Inputs and results ----

type RegisterInput struct {
	Name     string
	Email    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type VerifyInput struct {
	Email   string
	Code    string
	Purpose string
}

type OAuthInput struct {
	IDToken string
}

type ResendInput struct {
	Email   string
	Purpose string
}

type AuthResult struct {
	AccessToken      string
	User             *User
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type RefreshResult struct {
	AccessToken      string
	RefreshToken     string
	RefreshExpiresAt time.Time
}
