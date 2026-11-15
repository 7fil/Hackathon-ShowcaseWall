// Package repository implements the auth service's stores: PostgreSQL (Gorm),
// Redis, and the email clients. It makes no decisions about who may do what.
package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
)

// ---- Gorm models (private to this package) ----

type userModel struct {
	ID              string `gorm:"type:uuid;primaryKey"`
	Email           string
	DisplayName     string
	PasswordHash    *string // NULL for OAuth-only users
	Role            string
	FirebaseUID     *string
	AuthProvider    string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
}

func (userModel) TableName() string { return "users" }

type sessionModel struct {
	ID         string `gorm:"type:uuid;primaryKey"`
	UserID     string `gorm:"type:uuid"`
	FamilyID   string `gorm:"type:uuid"`
	TokenHash  string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *string `gorm:"type:uuid"`
	CreatedAt  time.Time
}

func (sessionModel) TableName() string { return "sessions" }

func (m userModel) toDomain() *service.User {
	u := &service.User{
		ID: m.ID, Email: m.Email, DisplayName: m.DisplayName, Role: m.Role,
		FirebaseUID: m.FirebaseUID, AuthProvider: m.AuthProvider,
		EmailVerifiedAt: m.EmailVerifiedAt, CreatedAt: m.CreatedAt,
	}
	if m.PasswordHash != nil {
		u.PasswordHash = *m.PasswordHash
	}
	return u
}

func newUserModel(u *service.User) userModel {
	m := userModel{
		ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: u.Role,
		FirebaseUID: u.FirebaseUID, AuthProvider: u.AuthProvider, EmailVerifiedAt: u.EmailVerifiedAt,
	}
	if m.AuthProvider == "" {
		m.AuthProvider = service.ProviderLocal
	}
	if u.PasswordHash != "" {
		h := u.PasswordHash
		m.PasswordHash = &h
	}
	return m
}

func (m sessionModel) toDomain() *service.Session {
	return &service.Session{
		ID: m.ID, UserID: m.UserID, FamilyID: m.FamilyID, TokenHash: m.TokenHash,
		ExpiresAt: m.ExpiresAt, RevokedAt: m.RevokedAt, ReplacedBy: m.ReplacedBy,
	}
}

// ---- Users ----

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *service.User) error {
	m := newUserModel(u)
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return service.ErrEmailTaken
		}
		return err
	}
	u.CreatedAt = m.CreatedAt
	return nil
}

func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*service.User, error) {
	var m userModel
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

func (r *UserRepo) FindByID(ctx context.Context, id string) (*service.User, error) {
	var m userModel
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

func (r *UserRepo) FindByFirebaseUID(ctx context.Context, uid string) (*service.User, error) {
	var m userModel
	if err := r.db.WithContext(ctx).Where("firebase_uid = ?", uid).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

// LinkFirebase only touches a row that has no firebase_uid yet, so two concurrent
// links cannot overwrite each other.
func (r *UserRepo) LinkFirebase(ctx context.Context, userID, uid, provider string, verifiedAt time.Time) error {
	res := r.db.WithContext(ctx).Model(&userModel{}).
		Where("id = ? AND firebase_uid IS NULL", userID).
		Updates(map[string]any{
			"firebase_uid":      uid,
			"auth_provider":     provider,
			"email_verified_at": gorm.Expr("COALESCE(email_verified_at, ?)", verifiedAt),
		})
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return service.ErrAlreadyLinked // this uid already belongs to another user
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return service.ErrAlreadyLinked
	}
	return nil
}

// PromoteToAdmin makes the user with this email an Admin. It is used by the seed
// command only and never creates an account: an unknown email returns
// service.ErrNotFound. changed is false when the user already was an Admin.
func (r *UserRepo) PromoteToAdmin(ctx context.Context, email string) (changed bool, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m userModel
		if err := tx.Where("email = ?", email).First(&m).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ErrNotFound
			}
			return err
		}
		if m.Role == identity.RoleAdmin {
			return nil
		}
		if err := tx.Model(&userModel{}).Where("id = ?", m.ID).Update("role", identity.RoleAdmin).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// ---- Sessions ----

type SessionRepo struct{ db *gorm.DB }

func NewSessionRepo(db *gorm.DB) *SessionRepo { return &SessionRepo{db: db} }

func (r *SessionRepo) Create(ctx context.Context, s *service.Session) error {
	m := sessionModel{
		ID: s.ID, UserID: s.UserID, FamilyID: s.FamilyID,
		TokenHash: s.TokenHash, ExpiresAt: s.ExpiresAt,
	}
	return r.db.WithContext(ctx).Create(&m).Error
}

func (r *SessionRepo) FindByTokenHash(ctx context.Context, hash string) (*service.Session, error) {
	var m sessionModel
	if err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return m.toDomain(), nil
}

// Rotate inserts the new session first (the old row points at it), then retires
// the old one only if it is still active. Two concurrent uses of one token: the
// second updates zero rows, the transaction rolls back and it gets ErrSessionConflict.
func (r *SessionRepo) Rotate(ctx context.Context, oldID string, next *service.Session) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		m := sessionModel{
			ID: next.ID, UserID: next.UserID, FamilyID: next.FamilyID,
			TokenHash: next.TokenHash, ExpiresAt: next.ExpiresAt,
		}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		res := tx.Model(&sessionModel{}).
			Where("id = ? AND revoked_at IS NULL", oldID).
			Updates(map[string]any{"revoked_at": time.Now(), "replaced_by": next.ID})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return service.ErrSessionConflict
		}
		return nil
	})
}

func (r *SessionRepo) RevokeFamily(ctx context.Context, familyID string) error {
	return r.db.WithContext(ctx).Model(&sessionModel{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", time.Now()).Error
}
