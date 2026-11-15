package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/logger"
)

type Deps struct {
	Users       UserRepository
	Sessions    SessionRepository
	Codes       CodeStore
	Limiter     RateLimiter
	Mailer      Mailer
	Hasher      PasswordHasher
	Tokens      TokenIssuer
	Logger      *slog.Logger
	IDTokens    IDTokenVerifier  // nil when Firebase is not configured
	AdminEmails []string         // emails that become Admin on first OAuth sign-in
	CodeSecret  []byte           // keys the code hash
	Now         func() time.Time // optional, for tests
}

type Service struct {
	d         Deps
	dummyHash string // compared when the email is unknown, so timing does not reveal accounts
	wg        sync.WaitGroup
}

func New(d Deps) (*Service, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	dummy, err := d.Hasher.Hash("dummy-password-for-constant-time")
	if err != nil {
		return nil, fmt.Errorf("auth: build dummy hash: %w", err)
	}
	return &Service{d: d, dummyHash: dummy}, nil
}

// Close waits for emails that are still being sent in the background.
func (s *Service) Close() { s.wg.Wait() }

var (
	errInvalidCredentials = apperr.New(apperr.KindUnauthorized, "invalid_credentials", "Email atau password salah")
	errInvalidSession     = apperr.New(apperr.KindUnauthorized, "invalid_session", "Sesi tidak valid, silakan masuk kembali")
)

func invalidCode() *apperr.Error {
	const msg = "Kode salah atau sudah kedaluwarsa"
	e := apperr.New(apperr.KindValidation, "invalid_code", msg)
	e.Fields = map[string]string{"code": msg}
	return e
}

func codeAttemptsExceeded() *apperr.Error {
	const msg = "Terlalu banyak percobaan, minta kode baru"
	e := apperr.New(apperr.KindValidation, "code_attempts_exceeded", msg)
	e.Fields = map[string]string{"code": msg}
	return e
}

func rateLimited(retry time.Duration) *apperr.Error {
	e := apperr.New(apperr.KindRateLimited, "rate_limited", "Terlalu banyak permintaan, coba lagi nanti")
	e.RetryAfter = retry
	return e
}

// ---------------------------------------------------------------- registration

// Register validates the input and stores a pending registration plus a code.
// The account is created only after VerifyOTP succeeds.
func (s *Service) Register(ctx context.Context, in RegisterInput) error {
	name := strings.TrimSpace(in.Name)
	email := normalizeEmail(in.Email)

	fields := map[string]string{}
	if n := utf8.RuneCountInString(name); n < MinNameRunes {
		fields["name"] = "Nama minimal 2 karakter"
	} else if n > MaxNameRunes {
		fields["name"] = "Nama maksimal 100 karakter"
	}
	if !validEmail(email) {
		fields["email"] = "Email tidak valid"
	}
	if msg, ok := validPassword(in.Password); !ok {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}

	if _, err := s.d.Users.FindByEmail(ctx, email); err == nil {
		return emailTaken()
	} else if !errors.Is(err, ErrNotFound) {
		return apperr.Internal(err)
	}

	if err := s.checkCodeQuota(ctx, email); err != nil {
		return err
	}
	hash, err := s.d.Hasher.Hash(in.Password)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.d.Codes.SavePending(ctx, email, PendingRegistration{Name: name, PasswordHash: hash}, OTPTTL); err != nil {
		return apperr.Internal(err)
	}
	return s.sendCode(ctx, PurposeRegister, email)
}

func emailTaken() *apperr.Error {
	e := apperr.New(apperr.KindConflict, "email_taken", "Email sudah terdaftar")
	e.Fields = map[string]string{"email": "Email sudah terdaftar"}
	return e
}

// ----------------------------------------------------------------------- login

// Login checks the password, then emails a code. No tokens are issued here.
func (s *Service) Login(ctx context.Context, in LoginInput, ip string) error {
	email := normalizeEmail(in.Email)

	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "Email tidak valid"
	}
	if in.Password == "" {
		fields["password"] = "Password wajib diisi"
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}

	failKey := "login_fail:" + email
	if n, ttl, err := s.d.Limiter.Count(ctx, failKey); err != nil {
		return apperr.Internal(err)
	} else if n >= FailedLoginLimit {
		s.d.Logger.Warn("audit: rate limit hit", "scope", "login_fail", "email", logger.MaskEmail(email), "ip", ip)
		return rateLimited(ttl)
	}

	user, err := s.d.Users.FindByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return apperr.Internal(err)
	}
	// Same bcrypt work for unknown and known emails, same 401 for both.
	// OAuth-only users (no password) take the same path and get the same 401.
	hash := s.dummyHash
	if user != nil && user.PasswordHash != "" {
		hash = user.PasswordHash
	}
	match := s.d.Hasher.Compare(hash, in.Password)
	if user == nil || user.PasswordHash == "" || !match {
		if _, _, err := s.d.Limiter.Allow(ctx, failKey, FailedLoginLimit, FailedLoginWindow); err != nil {
			s.d.Logger.Error("count failed login", "error", err.Error())
		}
		s.d.Logger.Warn("audit: login failed", "step", "password", "email", logger.MaskEmail(email), "ip", ip)
		return errInvalidCredentials
	}
	_ = s.d.Limiter.Reset(ctx, failKey)

	if err := s.checkCodeQuota(ctx, email); err != nil {
		return err
	}
	return s.sendCode(ctx, PurposeLogin, email)
}

// ------------------------------------------------------------------ OTP verify

// VerifyOTP confirms a code. For register it creates the Contributor account; for
// login it loads the existing user. Both return a fresh session.
func (s *Service) VerifyOTP(ctx context.Context, in VerifyInput, ip string) (*AuthResult, error) {
	email := normalizeEmail(in.Email)
	purpose := Purpose(in.Purpose)
	code := strings.TrimSpace(in.Code)

	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "Email tidak valid"
	}
	if !purpose.Valid() {
		fields["purpose"] = "Purpose harus register atau login"
	}
	if !validCode(code) {
		fields["code"] = "Kode harus 6 digit angka"
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields)
	}

	var pending *PendingRegistration
	if purpose == PurposeRegister {
		p, err := s.d.Codes.GetPending(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return nil, invalidCode()
		}
		if err != nil {
			return nil, apperr.Internal(err)
		}
		pending = p
	}

	status, err := s.d.Codes.ConsumeCode(ctx, purpose, email, hashCode(s.d.CodeSecret, purpose, email, code), MaxOTPAttempts)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	switch status {
	case CodeOK:
	case CodeExhausted:
		s.d.Logger.Warn("audit: code attempts exceeded", "purpose", in.Purpose, "email", logger.MaskEmail(email), "ip", ip)
		return nil, codeAttemptsExceeded()
	default:
		s.d.Logger.Warn("audit: login failed", "step", "code", "purpose", in.Purpose, "email", logger.MaskEmail(email), "ip", ip)
		return nil, invalidCode()
	}

	var user *User
	if purpose == PurposeRegister {
		id, err := NewUUID()
		if err != nil {
			return nil, apperr.Internal(err)
		}
		verifiedAt := s.d.Now()
		user = &User{
			ID: id, Email: email, DisplayName: pending.Name,
			PasswordHash: pending.PasswordHash, Role: identity.RoleContributor,
			AuthProvider: ProviderLocal, EmailVerifiedAt: &verifiedAt,
		}
		if err := s.d.Users.Create(ctx, user); err != nil {
			if errors.Is(err, ErrEmailTaken) { // someone else registered it in the meantime
				_ = s.d.Codes.DeletePending(ctx, email)
				return nil, emailTaken()
			}
			return nil, apperr.Internal(err)
		}
		_ = s.d.Codes.DeletePending(ctx, email)
		user.CreatedAt = s.d.Now()
	} else {
		user, err = s.d.Users.FindByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return nil, invalidCode()
		}
		if err != nil {
			return nil, apperr.Internal(err)
		}
	}

	res, err := s.startSession(ctx, user)
	if err != nil {
		return nil, err
	}
	s.d.Logger.Info("audit: login success", "purpose", in.Purpose, "user_id", user.ID, "ip", ip)
	return res, nil
}

// ---------------------------------------------------------------- OTP resend

// ResendOTP sends a new code while a pending step exists. The reply is the same
// whether or not one exists, so it cannot be used to probe for accounts.
func (s *Service) ResendOTP(ctx context.Context, in ResendInput) error {
	email := normalizeEmail(in.Email)
	purpose := Purpose(in.Purpose)

	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "Email tidak valid"
	}
	if !purpose.Valid() {
		fields["purpose"] = "Purpose harus register atau login"
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}

	// The quota applies to every request, so hitting it reveals nothing either.
	if err := s.checkCodeQuota(ctx, email); err != nil {
		return err
	}

	var pending bool
	var err error
	if purpose == PurposeRegister {
		_, err = s.d.Codes.GetPending(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		pending = err == nil
	} else {
		pending, err = s.d.Codes.HasCode(ctx, purpose, email)
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if !pending {
		return nil
	}
	return s.sendCode(ctx, purpose, email)
}

// -------------------------------------------------------------------- sessions

// Refresh rotates the refresh token and issues a new access token. A token that
// was already rotated away is treated as stolen and revokes the whole session.
func (s *Service) Refresh(ctx context.Context, rawToken, ip string) (*RefreshResult, error) {
	if rawToken == "" {
		return nil, errInvalidSession
	}
	sess, err := s.d.Sessions.FindByTokenHash(ctx, hashToken(rawToken))
	if errors.Is(err, ErrNotFound) {
		return nil, errInvalidSession
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	if sess.RevokedAt != nil {
		if sess.ReplacedBy != nil { // rotated away, then presented again
			s.revokeFamilyForReuse(ctx, sess, ip)
		}
		return nil, errInvalidSession
	}
	if !s.d.Now().Before(sess.ExpiresAt) {
		return nil, errInvalidSession
	}

	user, err := s.d.Users.FindByID(ctx, sess.UserID)
	if errors.Is(err, ErrNotFound) {
		_ = s.d.Sessions.RevokeFamily(ctx, sess.FamilyID)
		return nil, errInvalidSession
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	refresh, next, err := s.newSession(user.ID, sess.FamilyID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.d.Sessions.Rotate(ctx, sess.ID, next); err != nil {
		if errors.Is(err, ErrSessionConflict) { // lost a race with another use of the same token
			s.revokeFamilyForReuse(ctx, sess, ip)
			return nil, errInvalidSession
		}
		return nil, apperr.Internal(err)
	}
	access, _, err := s.d.Tokens.NewAccessToken(user.ID, user.Role)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &RefreshResult{AccessToken: access, RefreshToken: refresh, RefreshExpiresAt: next.ExpiresAt}, nil
}

func (s *Service) revokeFamilyForReuse(ctx context.Context, sess *Session, ip string) {
	s.d.Logger.Warn("audit: refresh token reuse", "user_id", sess.UserID, "session_id", sess.ID, "ip", ip)
	if err := s.d.Sessions.RevokeFamily(ctx, sess.FamilyID); err != nil {
		s.d.Logger.Error("revoke session family", "error", err.Error())
	}
}

// Logout revokes the session that owns the refresh token, if it belongs to userID.
func (s *Service) Logout(ctx context.Context, userID, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	sess, err := s.d.Sessions.FindByTokenHash(ctx, hashToken(rawToken))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if sess.UserID != userID {
		return nil
	}
	if err := s.d.Sessions.RevokeFamily(ctx, sess.FamilyID); err != nil {
		return apperr.Internal(err)
	}
	s.d.Logger.Info("audit: logout", "user_id", userID)
	return nil
}

// Me returns the current user. Reading the database means a removed user is refused at once.
func (s *Service) Me(ctx context.Context, userID string) (*User, error) {
	u, err := s.d.Users.FindByID(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, apperr.New(apperr.KindUnauthorized, "unauthorized", "Silakan masuk terlebih dahulu")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return u, nil
}

// ParseAccessToken lets the HTTP middleware verify bearer tokens.
func (s *Service) ParseAccessToken(token string) (identity.Identity, error) {
	return s.d.Tokens.ParseAccessToken(token)
}

// ------------------------------------------------------------------- internals

func (s *Service) newSession(userID, familyID string) (refresh string, sess *Session, err error) {
	refresh, err = newRefreshToken()
	if err != nil {
		return "", nil, err
	}
	id, err := NewUUID()
	if err != nil {
		return "", nil, err
	}
	if familyID == "" {
		if familyID, err = NewUUID(); err != nil {
			return "", nil, err
		}
	}
	return refresh, &Session{
		ID: id, UserID: userID, FamilyID: familyID,
		TokenHash: hashToken(refresh), ExpiresAt: s.d.Now().Add(RefreshTTL),
	}, nil
}

func (s *Service) startSession(ctx context.Context, user *User) (*AuthResult, error) {
	refresh, sess, err := s.newSession(user.ID, "")
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.d.Sessions.Create(ctx, sess); err != nil {
		return nil, apperr.Internal(err)
	}
	access, _, err := s.d.Tokens.NewAccessToken(user.ID, user.Role)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &AuthResult{AccessToken: access, User: user, RefreshToken: refresh, RefreshExpiresAt: sess.ExpiresAt}, nil
}

// checkCodeQuota enforces 1 code per minute and 5 per hour per email.
func (s *Service) checkCodeQuota(ctx context.Context, email string) error {
	limits := []struct {
		key    string
		limit  int
		window time.Duration
	}{
		{"otp:min:" + email, CodePerMinute, time.Minute},
		{"otp:hour:" + email, CodePerHour, time.Hour},
	}
	for _, l := range limits {
		ok, retry, err := s.d.Limiter.Allow(ctx, l.key, l.limit, l.window)
		if err != nil {
			return apperr.Internal(err)
		}
		if !ok {
			s.d.Logger.Warn("audit: rate limit hit", "scope", "otp", "email", logger.MaskEmail(email))
			return rateLimited(retry)
		}
	}
	return nil
}

// sendCode creates a code, stores only its hash, and emails it in the background.
func (s *Service) sendCode(ctx context.Context, purpose Purpose, email string) error {
	code, err := newCode()
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.d.Codes.SaveCode(ctx, purpose, email, hashCode(s.d.CodeSecret, purpose, email, code), OTPTTL); err != nil {
		return apperr.Internal(err)
	}
	msg := buildCodeEmail(email, code)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		sendCtx, cancel := context.WithTimeout(context.Background(), mailSendTimeout)
		defer cancel()
		if err := s.d.Mailer.Send(sendCtx, msg); err != nil {
			// Log the failure, never the message: it contains the code.
			s.d.Logger.Error("send code email failed", "email", logger.MaskEmail(email), "error", err.Error())
		}
	}()
	return nil
}

func buildCodeEmail(to, code string) Message {
	minutes := int(OTPTTL / time.Minute)
	return Message{
		To:      to,
		Subject: "Kode verifikasi BCC Showcase Wall",
		Text: fmt.Sprintf("Kode verifikasi Anda: %s\n\nKode berlaku %d menit dan hanya dapat dipakai sekali. "+
			"Jangan bagikan kode ini kepada siapa pun. Abaikan email ini jika Anda tidak memintanya.", code, minutes),
		HTML: fmt.Sprintf(`<div style="font-family:sans-serif;max-width:420px">`+
			`<p>Kode verifikasi Anda untuk BCC Showcase Wall:</p>`+
			`<p style="font-size:32px;letter-spacing:6px;font-weight:700">%s</p>`+
			`<p>Kode berlaku %d menit dan hanya dapat dipakai sekali. Jangan bagikan kode ini kepada siapa pun.</p>`+
			`<p style="color:#666">Abaikan email ini jika Anda tidak memintanya.</p></div>`, code, minutes),
	}
}
