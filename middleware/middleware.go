// Package middleware holds the shared Gin middleware: request ID, logging, recovery,
// CORS, origin check, body limit, rate limit, and bearer-token auth.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
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

// RequestID assigns an ID to each request and echoes it in X-Request-ID.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		c.Set(response.CtxRequestID, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// AccessLog writes one JSON line per request and records Prometheus HTTP metrics.
// It logs the route template, never the query string, headers, cookies or bodies.
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
			"ip", c.ClientIP(),
		}
		if id, ok := IdentityFrom(c); ok {
			attrs = append(attrs, "user_id", id.UserID)
		}
		log.Info("request", attrs...)
	}
}

// Recovery turns a panic into a 500 using the standard error body.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", "request_id", response.RequestID(c), "route", c.FullPath())
				response.Fail(c, apperr.Internal(errors.New("panic")))
			}
		}()
		c.Next()
	}
}

// CORS allows credentialed requests from the frontend origin only.
func CORS(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Origin") == origin {
			h := c.Writer.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
// Origin header it must be the frontend origin.
func RequireOrigin(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if o := c.GetHeader("Origin"); o != "" && o != origin {
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
