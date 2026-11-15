// Package apperr defines the application error type shared by every layer.
// It carries a kind and a machine code, and knows nothing about HTTP.
package apperr

import (
	"errors"
	"fmt"
	"time"
)

type Kind int

const (
	KindInternal     Kind = iota
	KindInvalid           // malformed input (400)
	KindValidation        // field-level problems (422)
	KindUnauthorized      // missing/wrong credentials or expired token (401)
	KindForbidden         // authenticated but not allowed (403)
	KindNotFound          // missing or not visible (404)
	KindConflict          // conflicting state (409)
	KindRateLimited       // too many requests (429)
	KindUnavailable       // feature not configured (503)
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string
	Err     error // internal cause, never sent to clients

	RetryAfter time.Duration // set on rate-limit errors
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

func Validation(fields map[string]string) *Error {
	return &Error{Kind: KindValidation, Code: "validation_failed", Message: "Input tidak valid", Fields: fields}
}

func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "internal_error", Message: "Terjadi kesalahan pada server", Err: err}
}

// As extracts an *Error from err, wrapping unknown errors as internal.
func As(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err)
}
