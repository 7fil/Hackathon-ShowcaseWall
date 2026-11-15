// Package response holds the shared JSON envelope and error mapping for handlers.
package response

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
)

// CtxRequestID is the Gin context key the request-ID middleware writes to.
const CtxRequestID = "request_id"

// RequestID returns the current request's ID, or "" if none was assigned.
func RequestID(c *gin.Context) string { return c.GetString(CtxRequestID) }

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// OK writes a success body shaped { data }.
func OK(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data})
}

// NoContent writes an empty response.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

func statusOf(k apperr.Kind) int {
	switch k {
	case apperr.KindInvalid:
		return http.StatusBadRequest
	case apperr.KindValidation:
		return http.StatusUnprocessableEntity
	case apperr.KindUnauthorized:
		return http.StatusUnauthorized
	case apperr.KindForbidden:
		return http.StatusForbidden
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindRateLimited:
		return http.StatusTooManyRequests
	case apperr.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Fail writes the single error body { error: { code, message, fields? } }.
// Internal causes are logged and never sent to the client.
func Fail(c *gin.Context, err error) {
	ae := apperr.As(err)
	if ae.Kind == apperr.KindInternal {
		slog.ErrorContext(c.Request.Context(), "internal error",
			"request_id", RequestID(c), "route", c.FullPath(), "error", ae.Error())
	}
	if ae.Kind == apperr.KindRateLimited && ae.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(ae.RetryAfter.Seconds()))))
	}
	c.AbortWithStatusJSON(statusOf(ae.Kind), gin.H{"error": errorBody{
		Code: ae.Code, Message: ae.Message, Fields: ae.Fields,
	}})
}
