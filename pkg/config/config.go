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

	// Logging. Version is the image tag or git SHA stamped at build time.
	LogLevel  string
	LogFormat string
	Version   string

	FirebaseProjectID       string
	FirebaseCredentialsJSON string
	FirebaseCredentialsFile string

	// MediaLocalDir is where cover images are stored on disk.
	MediaLocalDir string
}

const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

func (c Config) IsProduction() bool  { return c.Env == EnvProduction }
func (c Config) IsDevelopment() bool { return c.Env == EnvDevelopment }

// IsDeployed is true for staging and production: real infrastructure that must never
// run with development shortcuts (printed login codes, insecure cookies, debug output).
func (c Config) IsDeployed() bool { return c.Env == EnvStaging || c.Env == EnvProduction }

// Load reads and validates the configuration for the API server.
func Load() (Config, error) {
	c, err := parse()
	if err != nil {
		return c, err
	}
	return c, c.validate()
}

// LoadSeed reads only what the admin command needs: the database. Admins are never
// described in environment variables; the command takes the emails as arguments.
func LoadSeed() (Config, error) {
	c, err := parse()
	if err != nil {
		return c, err
	}
	if c.DatabaseURL == "" {
		return c, errors.New("missing required env: DATABASE_URL")
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
		LogLevel:       strings.ToLower(get("LOG_LEVEL", "")),
		LogFormat:      strings.ToLower(get("LOG_FORMAT", "")),
		Version:        get("APP_VERSION", ""),

		FirebaseProjectID:       os.Getenv("FIREBASE_PROJECT_ID"),
		FirebaseCredentialsJSON: strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_JSON")),
		FirebaseCredentialsFile: strings.TrimSpace(os.Getenv("FIREBASE_CREDENTIALS_FILE")),

		MediaLocalDir: get("MEDIA_LOCAL_DIR", "./data/media"),
	}
	switch c.Env {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		return c, fmt.Errorf("APP_ENV must be %s, %s or %s (got %q)", EnvDevelopment, EnvStaging, EnvProduction, c.Env)
	}
	switch c.LogLevel {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return c, errors.New("LOG_LEVEL must be debug, info, warn or error")
	}
	switch c.LogFormat {
	case "", "json", "text":
	default:
		return c, errors.New("LOG_FORMAT must be json or text")
	}
	var err error
	if c.CookieSecure, err = boolEnv("COOKIE_SECURE", true); err != nil {
		return c, err
	}
	if c.DevPrintOTP, err = boolEnv("DEV_PRINT_OTP", false); err != nil {
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
	if c.IsDeployed() {
		if c.DevPrintOTP {
			return fmt.Errorf("DEV_PRINT_OTP must be false in %s", c.Env)
		}
		if c.ResendAPIKey == "" || c.ResendFrom == "" {
			return fmt.Errorf("RESEND_API_KEY and RESEND_FROM are required in %s", c.Env)
		}
	}
	if c.IsProduction() && !c.CookieSecure {
		return errors.New("COOKIE_SECURE must be true in production")
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
