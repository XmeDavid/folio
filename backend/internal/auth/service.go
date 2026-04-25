package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/jobs"
)

// RegistrationMode gates the Signup endpoint per spec §9.
type RegistrationMode string

const (
	RegistrationOpen         RegistrationMode = "open"
	RegistrationInviteOnly   RegistrationMode = "invite_only"
	RegistrationFirstRunOnly RegistrationMode = "first_run_only"
)

// Config is the Service's knobs.
type Config struct {
	SessionIdle        time.Duration // default 14*24h
	SessionAbsolute    time.Duration // default 90*24h
	Registration       RegistrationMode
	BootstrapEmail     string // ADMIN_BOOTSTRAP_EMAIL; plan 5 consumes this
	AppURL             string
	SecretKey          []byte
	MFAChallengeTTL    time.Duration
	ReauthWindow       time.Duration
	WebAuthnRPID       string
	WebAuthnRPName     string
	WebAuthnOrigins    []string
	Jobs               *jobs.Client
	// AdminBootstrapHook, when set, runs inside the Signup transaction so a
	// grant failure rolls the whole signup back. Use a pgx.Tx-scoped writer
	// (see admin.EnsureBootstrapAdminTx).
	AdminBootstrapHook func(ctx context.Context, tx pgx.Tx, userID uuid.UUID, email string) error
	// SecureCookies controls the Secure flag on the session cookie.
	// Callers must set this explicitly: true in prod, false in dev over
	// http://localhost (Firefox refuses to store Secure cookies over http).
	SecureCookies bool
}

// Service wraps the db pool and the identity.Service. Handlers call Signup,
// Login, Logout; middleware reads sessions directly from the pool.
type Service struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	cfg      Config
	now      func() time.Time
	webauthn *webauthn.WebAuthn
}

// NewService constructs a Service with sensible defaults filled in.
func NewService(pool *pgxpool.Pool, identitySvc *identity.Service, cfg Config) *Service {
	if cfg.SessionIdle == 0 {
		cfg.SessionIdle = 14 * 24 * time.Hour
	}
	if cfg.SessionAbsolute == 0 {
		cfg.SessionAbsolute = 90 * 24 * time.Hour
	}
	if cfg.Registration == "" {
		cfg.Registration = RegistrationOpen
	}
	if cfg.AppURL == "" {
		cfg.AppURL = "http://localhost:3000"
	}
	if cfg.MFAChallengeTTL == 0 {
		cfg.MFAChallengeTTL = 5 * time.Minute
	}
	if cfg.ReauthWindow == 0 {
		cfg.ReauthWindow = 5 * time.Minute
	}
	if cfg.WebAuthnRPID == "" {
		cfg.WebAuthnRPID = "localhost"
	}
	if cfg.WebAuthnRPName == "" {
		cfg.WebAuthnRPName = "Folio"
	}
	if len(cfg.WebAuthnOrigins) == 0 {
		cfg.WebAuthnOrigins = []string{"http://localhost:3000"}
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.WebAuthnRPID,
		RPDisplayName: cfg.WebAuthnRPName,
		RPOrigins:     cfg.WebAuthnOrigins,
	})
	if err != nil {
		// Don't fail-fast: webauthn is only required for passkey-using
		// tenants. But we must surface the misconfiguration loudly so a
		// bad RPID/origin doesn't silently disable passkey login.
		slog.Default().Error("webauthn init failed; passkey flows disabled",
			"err", err, "rpid", cfg.WebAuthnRPID, "origins", cfg.WebAuthnOrigins)
		wa = nil
	}
	// SecureCookies is a required knob: zero-value (false) is only acceptable
	// when the caller explicitly passes it (e.g. APP_ENV=development).
	return &Service{pool: pool, identity: identitySvc, cfg: cfg, now: time.Now, webauthn: wa}
}

func (s *Service) Config() Config {
	return s.cfg
}

// Session is the in-memory shape of a sessions row. Middleware attaches
// this to the request context.
type Session struct {
	ID         string
	UserID     uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	ReauthAt   *time.Time
}
