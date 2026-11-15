package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
)

const jwtIssuer = "bcc-showcase-wall"

type accessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// JWT issues and parses HS256 access tokens that carry only user ID and role.
type JWT struct {
	secret []byte
	ttl    time.Duration
}

func NewJWT(secret string, ttl time.Duration) *JWT {
	return &JWT{secret: []byte(secret), ttl: ttl}
}

func (j *JWT) NewAccessToken(userID, role string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(j.ttl)
	claims := accessClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

func (j *JWT) ParseAccessToken(token string) (identity.Identity, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return j.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return identity.Identity{}, identity.ErrTokenExpired
		}
		return identity.Identity{}, identity.ErrTokenInvalid
	}
	if claims.Subject == "" || claims.Role == "" {
		return identity.Identity{}, identity.ErrTokenInvalid
	}
	return identity.Identity{UserID: claims.Subject, Role: claims.Role}, nil
}
