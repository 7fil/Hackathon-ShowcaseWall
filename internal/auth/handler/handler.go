// Package handler is the HTTP layer for auth: it binds input, calls the service,
// and maps results to DTOs. It holds no business rules.
package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

const (
	refreshCookieName = "refresh_token"
	refreshCookiePath = "/api/v1/auth" // the browser sends the cookie to /auth/* only
	maxBodyBytes      = 16 << 10
)

// Per-IP limits from the backend spec: 20 requests per hour on each endpoint group.
const (
	ipLimit  = 20
	ipWindow = time.Hour
)

type CookieConfig struct {
	Secure   bool
	SameSite http.SameSite
}

type Handler struct {
	svc            *service.Service
	cookie         CookieConfig
	frontendOrigin string
}

func New(svc *service.Service, cookie CookieConfig, frontendOrigin string) *Handler {
	return &Handler{svc: svc, cookie: cookie, frontendOrigin: frontendOrigin}
}

// Routes mounts the auth routes and /me on the /api/v1 group.
func (h *Handler) Routes(v1 *gin.RouterGroup, limiter middleware.Limiter, log *slog.Logger, requireAuth gin.HandlerFunc) {
	body := middleware.BodyLimit(maxBodyBytes)
	origin := middleware.RequireOrigin(h.frontendOrigin)

	a := v1.Group("/auth")
	a.POST("/register", body, middleware.RateLimitIP(limiter, log, "register", ipLimit, ipWindow), h.register)
	a.POST("/login", body, middleware.RateLimitIP(limiter, log, "login", ipLimit, ipWindow), h.login)
	code := middleware.RateLimitIP(limiter, log, "code", ipLimit, ipWindow) // verify and resend share one budget
	a.POST("/otp/verify", body, code, h.verify)
	a.POST("/otp/resend", body, code, h.resend)
	a.POST("/oauth/firebase", body, middleware.RateLimitIP(limiter, log, "oauth", ipLimit, ipWindow), h.oauthFirebase)
	a.POST("/refresh", origin, h.refresh)
	a.POST("/logout", origin, requireAuth, h.logout)

	v1.GET("/me", requireAuth, h.me)
}

// ---- DTOs ----

type userDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

func toUserDTO(u *service.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: u.Role}
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyRequest struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
}

type resendRequest struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

type oauthRequest struct {
	IDToken string `json:"id_token"`
}

var codeSent = gin.H{"status": "code_sent"}

func badJSON(c *gin.Context) {
	response.Fail(c, apperr.New(apperr.KindInvalid, "invalid_json", "Body permintaan tidak valid"))
}

// ---- Handlers ----

func (h *Handler) register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badJSON(c)
		return
	}
	if err := h.svc.Register(c.Request.Context(), service.RegisterInput{Name: req.Name, Email: req.Email, Password: req.Password}); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusAccepted, codeSent)
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badJSON(c)
		return
	}
	res, err := h.svc.Login(c.Request.Context(), service.LoginInput{Email: req.Email, Password: req.Password}, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.setRefreshCookie(c, res.RefreshToken, res.RefreshExpiresAt)
	response.OK(c, http.StatusOK, gin.H{"access_token": res.AccessToken, "user": toUserDTO(res.User)})
}

func (h *Handler) verify(c *gin.Context) {
	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badJSON(c)
		return
	}
	res, err := h.svc.VerifyOTP(c.Request.Context(), service.VerifyInput{Email: req.Email, Code: req.Code, Purpose: req.Purpose}, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.setRefreshCookie(c, res.RefreshToken, res.RefreshExpiresAt)
	response.OK(c, http.StatusOK, gin.H{"access_token": res.AccessToken, "user": toUserDTO(res.User)})
}

// oauthFirebase exchanges a Firebase ID token for this API's own session (no email code).
func (h *Handler) oauthFirebase(c *gin.Context) {
	var req oauthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badJSON(c)
		return
	}
	res, err := h.svc.OAuthLogin(c.Request.Context(), service.OAuthInput{IDToken: req.IDToken}, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.setRefreshCookie(c, res.RefreshToken, res.RefreshExpiresAt)
	response.OK(c, http.StatusOK, gin.H{"access_token": res.AccessToken, "user": toUserDTO(res.User)})
}

func (h *Handler) resend(c *gin.Context) {
	var req resendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badJSON(c)
		return
	}
	if err := h.svc.ResendOTP(c.Request.Context(), service.ResendInput{Email: req.Email, Purpose: req.Purpose}); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusAccepted, codeSent)
}

func (h *Handler) refresh(c *gin.Context) {
	raw, _ := c.Cookie(refreshCookieName)
	res, err := h.svc.Refresh(c.Request.Context(), raw, c.ClientIP())
	if err != nil {
		if apperr.As(err).Kind == apperr.KindUnauthorized {
			h.clearRefreshCookie(c) // drop a dead cookie so the client stops sending it
		}
		response.Fail(c, err)
		return
	}
	h.setRefreshCookie(c, res.RefreshToken, res.RefreshExpiresAt)
	response.OK(c, http.StatusOK, gin.H{"access_token": res.AccessToken})
}

func (h *Handler) logout(c *gin.Context) {
	id, _ := middleware.IdentityFrom(c)
	raw, _ := c.Cookie(refreshCookieName)
	if err := h.svc.Logout(c.Request.Context(), id.UserID, raw); err != nil {
		response.Fail(c, err)
		return
	}
	h.clearRefreshCookie(c)
	response.NoContent(c)
}

func (h *Handler) me(c *gin.Context) {
	id, _ := middleware.IdentityFrom(c)
	u, err := h.svc.Me(c.Request.Context(), id.UserID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, toUserDTO(u))
}

// ---- Cookie ----

func (h *Handler) setRefreshCookie(c *gin.Context, token string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: h.cookie.SameSite,
	})
}

func (h *Handler) clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: h.cookie.SameSite,
	})
}
