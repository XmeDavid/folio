package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppEnv              string
	AppURL              string
	RegistrationMode    string
	HTTPAddr            string
	LogLevel            string
	DatabaseURL         string
	SessionSecret       []byte
	EncryptionKey       []byte // 32 bytes, decoded from base64
	WebAuthnRPID        string
	WebAuthnRPName      string
	WebAuthnRPOrigins   []string
	MFAChallengeTTL     time.Duration
	ReauthWindow        time.Duration
	GoCardlessSecretID  string
	GoCardlessSecretKey string
	SentryDSN           string
	ResendAPIKey        string
	EmailFrom           string
	MarketdataOffline   bool
}

func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:              env("APP_ENV", "development"),
		AppURL:              env("APP_URL", "http://localhost:3000"),
		RegistrationMode:    os.Getenv("REGISTRATION_MODE"),
		HTTPAddr:            env("HTTP_ADDR", ":8080"),
		LogLevel:            env("LOG_LEVEL", "info"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		WebAuthnRPID:        env("WEBAUTHN_RP_ID", "localhost"),
		WebAuthnRPName:      env("WEBAUTHN_RP_NAME", "Folio"),
		WebAuthnRPOrigins:   []string{env("WEBAUTHN_RP_ORIGIN", "http://localhost:3000")},
		MFAChallengeTTL:     envDuration("MFA_CHALLENGE_TTL", 5*time.Minute),
		ReauthWindow:        envDuration("REAUTH_WINDOW", 5*time.Minute),
		GoCardlessSecretID:  os.Getenv("GOCARDLESS_SECRET_ID"),
		GoCardlessSecretKey: os.Getenv("GOCARDLESS_SECRET_KEY"),
		SentryDSN:           os.Getenv("SENTRY_DSN"),
		ResendAPIKey:        os.Getenv("RESEND_API_KEY"),
		EmailFrom:           env("EMAIL_FROM", "Folio <onboarding@localhost>"),
		MarketdataOffline:   os.Getenv("MARKETDATA_OFFLINE") != "",
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if len(sessionSecret) < 32 {
		return nil, errors.New("SESSION_SECRET must be at least 32 characters")
	}
	if strings.Contains(sessionSecret, "CHANGE_ME") {
		return nil, errors.New("SESSION_SECRET is still the .env.example placeholder; generate with: openssl rand -base64 48")
	}
	cfg.SessionSecret = []byte(sessionSecret)

	rawKey := os.Getenv("SECRET_ENCRYPTION_KEY")
	if rawKey == "" {
		return nil, errors.New("SECRET_ENCRYPTION_KEY is required (base64-encoded 32 bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY: invalid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY: must decode to 32 bytes, got %d", len(key))
	}
	cfg.EncryptionKey = key

	return cfg, nil
}

func (c *Config) IsProd() bool {
	return strings.EqualFold(c.AppEnv, "production")
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
