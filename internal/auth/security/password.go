// Package security implements the password hasher and access-token issuer behind
// the interfaces the auth service defines.
package security

import "golang.org/x/crypto/bcrypt"

type Bcrypt struct{ cost int }

func NewBcrypt() *Bcrypt { return &Bcrypt{cost: bcrypt.DefaultCost} }

func (b *Bcrypt) Hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), b.cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// Compare is constant-time with respect to the password. Passwords longer than
// 72 bytes never match (bcrypt would otherwise ignore the tail).
func (b *Bcrypt) Compare(hash, password string) bool {
	if len(password) > 72 {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
