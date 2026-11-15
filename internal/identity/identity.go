// Package identity holds the authenticated caller shared by auth and the middleware.
package identity

import "errors"

// ErrTokenExpired lets a verifier tell an expired access token from an invalid one.
var ErrTokenExpired = errors.New("token expired")

// ErrTokenInvalid means the access token is malformed, badly signed or otherwise unusable.
var ErrTokenInvalid = errors.New("token invalid")

const (
	RoleContributor = "Contributor"
	RoleAdmin       = "Admin"
)

// Identity is what the access token carries: who the caller is and their role.
// Ownership checks must still read the database.
type Identity struct {
	UserID string
	Role   string
}
