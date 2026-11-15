package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
)

// OAuthLogin signs a user in (or up) with a Firebase ID token. The provider has
// already verified the email, so no code is sent: it goes straight to a session.
//
//	new email            -> create user (no password), role from the Admin email list
//	existing, unlinked   -> link firebase_uid to that account, then log in
//	already linked       -> log in
func (s *Service) OAuthLogin(ctx context.Context, in OAuthInput, ip string) (*AuthResult, error) {
	if s.d.IDTokens == nil {
		return nil, apperr.New(apperr.KindUnavailable, "oauth_unavailable", "Login dengan Google belum tersedia")
	}
	token := strings.TrimSpace(in.IDToken)
	if token == "" {
		return nil, apperr.Validation(map[string]string{"id_token": "id_token wajib diisi"})
	}

	claims, err := s.d.IDTokens.Verify(ctx, token)
	if err != nil {
		if errors.Is(err, ErrInvalidIDToken) {
			s.d.Logger.Warn("audit: login failed", "step", "oauth", "ip", ip)
			return nil, apperr.New(apperr.KindUnauthorized, "invalid_id_token", "Token Google tidak valid atau sudah kedaluwarsa")
		}
		return nil, apperr.Internal(err)
	}
	if !claims.EmailVerified {
		s.d.Logger.Warn("audit: login failed", "step", "oauth_unverified_email", "ip", ip)
		return nil, apperr.New(apperr.KindForbidden, "email_not_verified", "Email Anda belum diverifikasi oleh penyedia login")
	}
	email := normalizeEmail(claims.Email)
	if claims.UID == "" || !validEmail(email) {
		return nil, apperr.New(apperr.KindUnauthorized, "invalid_id_token", "Token Google tidak valid atau sudah kedaluwarsa")
	}

	user, err := s.resolveOAuthUser(ctx, claims, email)
	if err != nil {
		return nil, err
	}
	res, err := s.startSession(ctx, user)
	if err != nil {
		return nil, err
	}
	s.d.Logger.Info("audit: login success", "purpose", "oauth", "user_id", user.ID, "email", logger.MaskEmail(email), "ip", ip)
	return res, nil
}

func (s *Service) resolveOAuthUser(ctx context.Context, c *IDTokenClaims, email string) (*User, error) {
	// A returning Google user is matched by uid first, so an email change on the Google side does not split accounts.
	if u, err := s.d.Users.FindByFirebaseUID(ctx, c.UID); err == nil {
		return u, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, apperr.Internal(err)
	}

	// Two attempts: if a concurrent sign-up creates the email between our lookup and insert, link instead.
	for attempt := 0; attempt < 2; attempt++ {
		u, err := s.d.Users.FindByEmail(ctx, email)
		switch {
		case err == nil:
			return s.linkOAuthUser(ctx, u, c.UID)
		case !errors.Is(err, ErrNotFound):
			return nil, apperr.Internal(err)
		}

		id, err := NewUUID()
		if err != nil {
			return nil, apperr.Internal(err)
		}
		now := s.d.Now()
		uid := c.UID
		nu := &User{
			ID: id, Email: email, DisplayName: oauthDisplayName(c.Name, email),
			Role: identity.RoleContributor, FirebaseUID: &uid,
			AuthProvider: ProviderGoogle, EmailVerifiedAt: &now,
		}
		err = s.d.Users.Create(ctx, nu)
		if err == nil {
			return nu, nil
		}
		if !errors.Is(err, ErrEmailTaken) {
			return nil, apperr.Internal(err)
		}
	}
	return nil, apperr.New(apperr.KindConflict, "oauth_account_conflict", "Akun tidak dapat ditautkan, coba lagi")
}

func (s *Service) linkOAuthUser(ctx context.Context, u *User, uid string) (*User, error) {
	if u.FirebaseUID != nil {
		if *u.FirebaseUID == uid {
			return u, nil
		}
		// Same email, different Google identity: never merge automatically.
		s.d.Logger.Warn("audit: oauth link refused", "user_id", u.ID)
		return nil, apperr.New(apperr.KindConflict, "oauth_account_conflict", "Email ini sudah tertaut ke akun Google lain")
	}
	provider := ProviderGoogle
	if u.PasswordHash != "" {
		provider = ProviderBoth
	}
	now := s.d.Now()
	if err := s.d.Users.LinkFirebase(ctx, u.ID, uid, provider, now); err != nil {
		if errors.Is(err, ErrAlreadyLinked) {
			return nil, apperr.New(apperr.KindConflict, "oauth_account_conflict", "Akun tidak dapat ditautkan, coba lagi")
		}
		return nil, apperr.Internal(err)
	}
	u.FirebaseUID, u.AuthProvider = &uid, provider
	if u.EmailVerifiedAt == nil {
		u.EmailVerifiedAt = &now
	}
	s.d.Logger.Info("audit: oauth account linked", "user_id", u.ID, "provider", provider)
	return u, nil
}

// oauthDisplayName uses the Google name, falling back to the email prefix, within the name limits.
func oauthDisplayName(name, email string) string {
	n := strings.TrimSpace(name)
	if utf8.RuneCountInString(n) < MinNameRunes {
		n = email[:strings.LastIndex(email, "@")]
	}
	if utf8.RuneCountInString(n) < MinNameRunes {
		n = n + "_" // single-character prefix, e.g. "a@x.co"
	}
	if r := []rune(n); len(r) > MaxNameRunes {
		n = string(r[:MaxNameRunes])
	}
	return n
}
