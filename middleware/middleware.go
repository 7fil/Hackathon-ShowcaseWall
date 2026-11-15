// Package middleware holds the shared Gin middleware: request ID, logging, recovery,
// CORS, origin check, body limit, rate limit, and bearer-token auth.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

const (
	ctxIdentity = "identity"
)

// TokenVerifier is implemented by the auth service.
type TokenVerifier interface {
	ParseAccessToken(token string) (identity.Identity, error)
}

// Limiter is a counter-based rate limiter (implemented in platform/cache).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// RequestID assigns an ID to each request and echoes it in X-Request-ID. An ID sent by a
// trusted reverse proxy is kept so one request can be followed across services; anything
// that is not a short token of letters, digits, "-" or "_" is replaced, which also keeps
// log-injection payloads out of the logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if !validRequestID(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set(response.CtxRequestID, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// slowRequest is the latency above which a successful request is logged as a warning.
const slowRequest = 2 * time.Second

// quietRoutes are polled constantly (load balancer and Docker health checks), so they are
// logged at debug level instead of filling the log with one line every few seconds.
var quietRoutes = map[string]bool{"/health": true}

// AccessLog writes one JSON line per request and records Prometheus HTTP metrics.
// The level follows the outcome: 5xx is an error, 4xx and slow requests are warnings,
// the rest is info. It logs the route template, never the query string, headers,
// cookies or bodies.
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		dur := time.Since(start)
		status := c.Writer.Status()
		metrics.IncHTTPRequest(c.Request.Method, route, status)
		metrics.ObserveHTTPDuration(route, dur.Seconds())

		attrs := []any{
			"request_id", response.RequestID(c),
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"latency_ms", dur.Milliseconds(),
			"bytes", c.Writer.Size(),
			"ip", c.ClientIP(),
			"user_agent", truncate(c.Request.UserAgent(), 200),
		}
		if id, ok := IdentityFrom(c); ok {
			attrs = append(attrs, "user_id", id.UserID)
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400, dur > slowRequest:
			level = slog.LevelWarn
		case quietRoutes[route]:
			level = slog.LevelDebug
		}
		log.Log(c.Request.Context(), level, "request", attrs...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Recovery turns a panic into a 500 using the standard error body. The panic value and
// stack trace go to the log (never to the client) so the cause can be found.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered",
					"request_id", response.RequestID(c),
					"method", c.Request.Method,
					"route", c.FullPath(),
					"panic", fmt.Sprint(r),
					"stack", string(debug.Stack()),
				)
				response.Fail(c, apperr.Internal(errors.New("panic")))
			}
		}()
		c.Next()
	}
}

// IsAllowedOrigin checks if reqOrigin matches any comma-separated origin in configured
// or is a local development origin (localhost / 127.0.0.1).
func IsAllowedOrigin(reqOrigin, configured string) bool {
	reqOrigin = strings.TrimRight(strings.TrimSpace(reqOrigin), "/")
	if reqOrigin == "" {
		return false
	}
	for _, allowed := range strings.Split(configured, ",") {
		if strings.TrimRight(strings.TrimSpace(allowed), "/") == reqOrigin {
			return true
		}
	}
	return strings.HasPrefix(reqOrigin, "http://localhost:") ||
		strings.HasPrefix(reqOrigin, "http://127.0.0.1:") ||
		reqOrigin == "http://localhost" ||
		reqOrigin == "http://127.0.0.1"
}

// CORS allows credentialed requests from the configured frontend origins and localhost.
func CORS(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqOrigin := c.GetHeader("Origin")
		if IsAllowedOrigin(reqOrigin, origin) {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", reqOrigin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Origin, X-Requested-With")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
			h.Set("Access-Control-Max-Age", "600")
		}
		c.Writer.Header().Add("Vary", "Origin")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// RequireOrigin protects cookie-authenticated routes: when the browser sends an
// Origin header it must be an allowed frontend origin.
func RequireOrigin(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if o := c.GetHeader("Origin"); o != "" && !IsAllowedOrigin(o, origin) {
			response.Fail(c, apperr.New(apperr.KindForbidden, "forbidden_origin", "Asal permintaan tidak diizinkan"))
			return
		}
		c.Next()
	}
}

// BodyLimit caps the request body size.
func BodyLimit(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

// RateLimitIP limits requests per client IP for a named group of endpoints.
func RateLimitIP(l Limiter, log *slog.Logger, name string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, retry, err := l.Allow(c.Request.Context(), "ip:"+name+":"+c.ClientIP(), limit, window)
		if err != nil {
			response.Fail(c, apperr.Internal(err))
			return
		}
		if !ok {
			log.Warn("audit: rate limit hit", "request_id", response.RequestID(c), "scope", "ip:"+name, "ip", c.ClientIP())
			ae := apperr.New(apperr.KindRateLimited, "rate_limited", "Terlalu banyak permintaan, coba lagi nanti")
			ae.RetryAfter = retry
			response.Fail(c, ae)
			return
		}
		c.Next()
	}
}

// Auth requires a valid Bearer access token and stores the identity on the context.
func Auth(v TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		scheme, token, found := strings.Cut(h, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
			response.Fail(c, apperr.New(apperr.KindUnauthorized, "unauthorized", "Silakan masuk terlebih dahulu"))
			return
		}
		id, err := v.ParseAccessToken(strings.TrimSpace(token))
		if err != nil {
			if errors.Is(err, identity.ErrTokenExpired) {
				response.Fail(c, apperr.New(apperr.KindUnauthorized, "token_expired", "Sesi kedaluwarsa"))
				return
			}
			response.Fail(c, apperr.New(apperr.KindUnauthorized, "unauthorized", "Token tidak valid"))
			return
		}
		c.Set(ctxIdentity, id)
		c.Next()
	}
}

// RequireRole must run after Auth.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := IdentityFrom(c)
		if !ok {
			response.Fail(c, apperr.New(apperr.KindUnauthorized, "unauthorized", "Silakan masuk terlebih dahulu"))
			return
		}
		for _, r := range roles {
			if id.Role == r {
				c.Next()
				return
			}
		}
		slog.Warn("audit: permission denied", "request_id", response.RequestID(c), "user_id", id.UserID, "route", c.FullPath())
		response.Fail(c, apperr.New(apperr.KindForbidden, "forbidden", "Anda tidak memiliki akses"))
	}
}

func IdentityFrom(c *gin.Context) (identity.Identity, bool) {
	v, ok := c.Get(ctxIdentity)
	if !ok {
		return identity.Identity{}, false
	}
	id, ok := v.(identity.Identity)
	return id, ok
}
