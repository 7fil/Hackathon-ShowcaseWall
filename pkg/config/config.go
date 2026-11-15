// Package config loads configuration from environment variables only.
package config

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Env            string
	Port           string
	MetricsPort    string
	DatabaseURL    string
	RedisURL       string
	JWTSecret      string
	CookieSecure   bool
	CookieSameSite http.SameSite
	DevPrintOTP    bool
	ResendAPIKey   string
	ResendFrom     string
	FrontendOrigin string
	TrustedProxies []string
	AdminEmail     string
	AdminPassword  string
	AdminName      string
	AdminEmails    []string // every email that becomes Admin on OAuth sign-up (includes ADMIN_EMAIL)

	FirebaseProjectID       string
	FirebaseCredentialsJSON string
	FirebaseCredentialsFile string

	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioUseSSL    bool
	MediaLocalDir  string
}

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads and validates the configuration for the API server.
func Load() (Config, error) {
	c, err := parse()
	if err != nil {
		return c, err
	}
	return c, c.validate()
}

// LoadSeed reads only what the seed command needs (database and Admin account).
func LoadSeed() (Config, error) {
	c, err := parse()
	if err != nil {
		return c, err
	}
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.AdminEmail == "" {
		missing = append(missing, "ADMIN_EMAIL")
	}
	if c.AdminPassword == "" {
		missing = append(missing, "ADMIN_PASSWORD")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func parse() (Config, error) {
	c := Config{
		Env:            get("APP_ENV", "development"),
		Port:           get("PORT", "8080"),
		MetricsPort:    get("METRICS_PORT", "9100"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		RedisURL:       os.Getenv("REDIS_URL"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		ResendAPIKey:   os.Getenv("RESEND_API_KEY"),
		ResendFrom:     os.Getenv("RESEND_FROM"),
		FrontendOrigin: strings.TrimRight(get("FRONTEND_ORIGIN", "https://group5.bccdev.id"), "/"),
		AdminEmail:     strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))),
		AdminPassword:  os.Getenv("ADMIN_PASSWORD"),
		AdminName:      get("ADMIN_NAME", "Admin"),

		FirebaseProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
		FirebaseCredentialsJSON: strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_JSON")),
		FirebaseCredentialsFile: strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_FILE")),

		MinioEndpoint:  strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")),
		MinioAccessKey: get("MINIO_ACCESS_KEY", "minioadmin"),
		MinioSecretKey: get("MINIO_SECRET_KEY", "minioadmin"),
		MinioBucket:    get("MINIO_BUCKET", "showcase-works"),
		MediaLocalDir:  get("MEDIA_LOCAL_DIR", "/tmp/showcase-media"),
	}
	c.AdminEmails = splitEmails(os.Getenv("ADMIN_EMAILS"))
	if c.AdminEmail != "" {
		c.AdminEmails = append(c.AdminEmails, c.AdminEmail)
	}
	var err error
	if c.CookieSecure, err = boolEnv("COOKIE_SECURE", true); err != nil {
		return c, err
	}
	if c.DevPrintOTP, err = boolEnv("DEV_PRINT_OTP", false); err != nil {
		return c, err
	}
	if c.MinioUseSSL, err = boolEnv("MINIO_USE_SSL", false); err != nil {
		return c, err
	}
	switch strings.ToLower(get("COOKIE_SAMESITE", "lax")) {
	case "lax":
		c.CookieSameSite = http.SameSiteLaxMode
	case "none":
		c.CookieSameSite = http.SameSiteNoneMode
	default:
		return c, errors.New("COOKIE_SAMESITE must be lax or none")
	}
	if c.CookieSameSite == http.SameSiteNoneMode && !c.CookieSecure {
		return c, errors.New("COOKIE_SAMESITE=none requires COOKIE_SECURE=true")
	}
	for _, p := range strings.Split(get("TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	return c, nil
}

func (c Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.RedisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 characters")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	if c.IsProduction() {
		if c.DevPrintOTP {
			return errors.New("DEV_PRINT_OTP must be false in production")
		}
		if c.ResendAPIKey == "" || c.ResendFrom == "" {
			return errors.New("RESEND_API_KEY and RESEND_FROM are required in production")
		}
	}
	if !c.DevPrintOTP && (c.ResendAPIKey == "" || c.ResendFrom == "") {
		return errors.New("set RESEND_API_KEY and RESEND_FROM, or enable DEV_PRINT_OTP for local development")
	}
	return nil
}

func get(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func boolEnv(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return b, nil
}

func splitEmails(v string) []string {
	var out []string
	for _, e := range strings.Split(v, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// FirebaseEnabled reports whether Firebase credentials were provided.
func (c Config) FirebaseEnabled() bool {
	return c.FirebaseCredentialsJSON != "" || c.FirebaseCredentialsFile != ""
}

// FirebaseCredentials returns the service-account JSON from the env value or the mounted file.
func (c Config) FirebaseCredentials() ([]byte, error) {
	if c.FirebaseCredentialsJSON != "" {
		return []byte(c.FirebaseCredentialsJSON), nil
	}
	return os.ReadFile(c.FirebaseCredentialsFile)
}
