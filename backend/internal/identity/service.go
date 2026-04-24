// Package identity owns tenants and users (the root of the financial data
// graph) and the "bootstrap" onboarding flow that provisions them together.
//
// The v1 data model is intentionally 1:1 tenant→user; this package assumes
// that invariant when resolving the current user from a tenant id.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Service is the identity service. It owns writes to tenants/users.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService constructs an identity Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// Tenant is the read-model representation of a tenant row.
type Tenant struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	BaseCurrency   string    `json:"baseCurrency"`
	CycleAnchorDay int       `json:"cycleAnchorDay"`
	Locale         string    `json:"locale"`
	Timezone       string    `json:"timezone"`
	CreatedAt      time.Time `json:"createdAt"`
}

// User is the read-model representation of a user row.
type User struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenantId"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

// OnboardInput is the validated input to Bootstrap.
type OnboardInput struct {
	TenantName     string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
	Email          string
	DisplayName    string
}

// OnboardResult is returned from Bootstrap.
type OnboardResult struct {
	Tenant Tenant `json:"tenant"`
	User   User   `json:"user"`
}

// normalize applies defaults and validates an OnboardInput. Pure function —
// tested directly without a database.
func (in OnboardInput) normalize() (OnboardInput, error) {
	in.TenantName = strings.TrimSpace(in.TenantName)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)

	if in.TenantName == "" {
		return in, httpx.NewValidationError("tenantName is required")
	}
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required and must look like an email")
	}
	if in.DisplayName == "" {
		return in, httpx.NewValidationError("displayName is required")
	}
	if in.Locale == "" {
		return in, httpx.NewValidationError("locale is required (e.g. en-US)")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.CycleAnchorDay == 0 {
		in.CycleAnchorDay = 1
	}
	if in.CycleAnchorDay < 1 || in.CycleAnchorDay > 31 {
		return in, httpx.NewValidationError("cycleAnchorDay must be between 1 and 31")
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// Bootstrap provisions a tenant and its first user in a single transaction.
// Password auth is not yet implemented: password_hash is stored NULL and will
// be populated by a future /auth flow.
func (s *Service) Bootstrap(ctx context.Context, raw OnboardInput) (*OnboardResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	tenantID := uuidx.New()
	userID := uuidx.New()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenant Tenant
	err = tx.QueryRow(ctx, `
		insert into tenants (id, name, base_currency, cycle_anchor_day, locale, timezone)
		values ($1, $2, $3, $4, $5, $6)
		returning id, name, base_currency, cycle_anchor_day, locale, timezone, created_at
	`, tenantID, in.TenantName, in.BaseCurrency, in.CycleAnchorDay, in.Locale, in.Timezone).
		Scan(&tenant.ID, &tenant.Name, &tenant.BaseCurrency, &tenant.CycleAnchorDay,
			&tenant.Locale, &tenant.Timezone, &tenant.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert tenant: %w", err)
	}

	var user User
	err = tx.QueryRow(ctx, `
		insert into users (id, tenant_id, email, display_name)
		values ($1, $2, $3, $4)
		returning id, tenant_id, email, display_name, created_at
	`, userID, tenantID, in.Email, in.DisplayName).
		Scan(&user.ID, &user.TenantID, &user.Email, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &OnboardResult{Tenant: tenant, User: user}, nil
}

// Me resolves the (unique) user for a tenant. The 1:1 tenant→user invariant
// is enforced by a UNIQUE constraint on users.tenant_id.
func (s *Service) Me(ctx context.Context, tenantID uuid.UUID) (*User, *Tenant, error) {
	var user User
	var tenant Tenant
	err := s.pool.QueryRow(ctx, `
		select u.id, u.tenant_id, u.email, u.display_name, u.created_at,
		       t.id, t.name, t.base_currency, t.cycle_anchor_day, t.locale, t.timezone, t.created_at
		from users u
		join tenants t on t.id = u.tenant_id
		where u.tenant_id = $1
	`, tenantID).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.DisplayName, &user.CreatedAt,
		&tenant.ID, &tenant.Name, &tenant.BaseCurrency, &tenant.CycleAnchorDay,
		&tenant.Locale, &tenant.Timezone, &tenant.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, httpx.NewNotFoundError("user for tenant")
		}
		return nil, nil, err
	}
	return &user, &tenant, nil
}
