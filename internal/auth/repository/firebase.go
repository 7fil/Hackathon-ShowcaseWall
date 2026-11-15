package repository

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/auth/service"
)

// FirebaseVerifier checks Firebase ID tokens with the Firebase Admin SDK.
type FirebaseVerifier struct {
	client *auth.Client
}

// NewFirebaseVerifier builds the verifier from a service-account JSON. projectID may
// be empty when the JSON already names the project.
func NewFirebaseVerifier(ctx context.Context, credentialsJSON []byte, projectID string) (*FirebaseVerifier, error) {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID}, option.WithCredentialsJSON(credentialsJSON))
	if err != nil {
		return nil, fmt.Errorf("firebase app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase auth client: %w", err)
	}
	return &FirebaseVerifier{client: client}, nil
}

func (v *FirebaseVerifier) Verify(ctx context.Context, idToken string) (*service.IDTokenClaims, error) {
	tok, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		if auth.IsIDTokenInvalid(err) || auth.IsIDTokenExpired(err) {
			return nil, service.ErrInvalidIDToken
		}
		return nil, fmt.Errorf("verify firebase id token: %w", err)
	}
	email, _ := tok.Claims["email"].(string)
	name, _ := tok.Claims["name"].(string)
	verified, _ := tok.Claims["email_verified"].(bool)
	return &service.IDTokenClaims{UID: tok.UID, Email: email, Name: name, EmailVerified: verified}, nil
}
