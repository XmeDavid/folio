package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	AppEnv              string
	AppURL              string
	HTTPAddr            string
	LogLevel            string
	DatabaseURL         string
	SessionSecret       []byte
	EncryptionKey       []byte // 32 bytes, decoded from base64
	WebAuthnRPID        string
	WebAuthnRPName      string
	WebAuthnRPOrigin    string
	GoCardlessSecretID  string
	GoCardlessSecretKey string
	SentryDSN           string
}

func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:              env("APP_ENV", "development"),
		AppURL:              env("APP_URL", "http://localhost:3000"),
		HTTPAddr:            env("HTTP_ADDR", ":8080"),
		LogLevel:            env("LOG_LEVEL", "info"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		WebAuthnRPID:        env("WEBAUTHN_RP_ID", "localhost"),
		WebAuthnRPName:      env("WEBAUTHN_RP_NAME", "Folio"),
		WebAuthnRPOrigin:    env("WEBAUTHN_RP_ORIGIN", "http://localhost:3000"),
		GoCardlessSecretID:  os.Getenv("GOCARDLESS_SECRET_ID"),
		GoCardlessSecretKey: os.Getenv("GOCARDLESS_SECRET_KEY"),
		SentryDSN:           os.Getenv("SENTRY_DSN"),
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if len(sessionSecret) < 32 {
		return nil, errors.New("SESSION_SECRET must be at least 32 characters")
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
