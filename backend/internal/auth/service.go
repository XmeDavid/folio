package auth

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
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
	SessionIdle     time.Duration // default 14*24h
	SessionAbsolute time.Duration // default 90*24h
	Registration    RegistrationMode
	BootstrapEmail  string // ADMIN_BOOTSTRAP_EMAIL; plan 5 consumes this
}

// Service wraps the db pool and the identity.Service. Handlers call Signup,
// Login, Logout; middleware reads sessions directly from the pool.
type Service struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	cfg      Config
	now      func() time.Time
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
	return &Service{pool: pool, identity: identitySvc, cfg: cfg, now: time.Now}
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
