package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Service owns writes and reads for users, tenants, and memberships.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService constructs a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// Me returns the user + every tenant they belong to, with their role per
// tenant. Soft-deleted tenants are excluded.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, []TenantWithRole, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		select id, email, display_name, email_verified_at, is_admin, last_tenant_id, created_at
		from users
		where id = $1
	`, userID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.EmailVerifiedAt, &u.IsAdmin, &u.LastTenantID, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, nil, httpx.NewNotFoundError("user")
		}
		return u, nil, fmt.Errorf("select user: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		select t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day, t.locale, t.timezone, t.deleted_at, t.created_at, m.role
		from tenant_memberships m
		join tenants t on t.id = m.tenant_id
		where m.user_id = $1 and t.deleted_at is null
		order by t.name
	`, userID)
	if err != nil {
		return u, nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()
	var tenants []TenantWithRole
	for rows.Next() {
		var tr TenantWithRole
		if err := rows.Scan(&tr.ID, &tr.Name, &tr.Slug, &tr.BaseCurrency, &tr.CycleAnchorDay,
			&tr.Locale, &tr.Timezone, &tr.DeletedAt, &tr.CreatedAt, &tr.Role); err != nil {
			return u, nil, fmt.Errorf("scan membership: %w", err)
		}
		tenants = append(tenants, tr)
	}
	if rows.Err() != nil {
		return u, nil, rows.Err()
	}
	return u, tenants, nil
}

// CreateTenantInput is the validated input to CreateTenant.
type CreateTenantInput struct {
	Name           string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
}

// Normalize trims + validates the input. Exported for cross-package use
// (auth.Service.Signup reuses it).
func (in CreateTenantInput) Normalize() (CreateTenantInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Name == "" {
		return in, httpx.NewValidationError("name is required")
	}
	if in.Locale == "" {
		return in, httpx.NewValidationError("locale is required")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.CycleAnchorDay == 0 {
		in.CycleAnchorDay = 1
	}
	if in.CycleAnchorDay < 1 || in.CycleAnchorDay > 31 {
		return in, httpx.NewValidationError("cycleAnchorDay must be 1-31")
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// CreateTenant creates a tenant with a unique slug derived from its name,
// and installs the calling user as an owner in the same transaction.
func (s *Service) CreateTenant(ctx context.Context, userID uuid.UUID, raw CreateTenantInput) (Tenant, Membership, error) {
	in, err := raw.Normalize()
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tenant{}, Membership{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := InsertTenantTx(ctx, tx, uuidx.New(), in)
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	m, err := InsertMembershipTx(ctx, tx, t.ID, userID, RoleOwner)
	if err != nil {
		return Tenant{}, Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Tenant{}, Membership{}, fmt.Errorf("commit: %w", err)
	}
	return t, m, nil
}

// ListMembers returns every membership in tenantID. Plan 1 list-only;
// plan 2 extends to include pending invites.
func (s *Service) ListMembers(ctx context.Context, tenantID uuid.UUID) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		select tenant_id, user_id, role, created_at
		from tenant_memberships
		where tenant_id = $1
		order by created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.TenantID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateTenantInput is the PATCH body for tenant settings. Pointer fields
// mean "absent"; a non-nil pointer to the zero value means "clear".
type UpdateTenantInput struct {
	Name           *string
	Slug           *string
	BaseCurrency   *string
	CycleAnchorDay *int
	Locale         *string
	Timezone       *string
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// normalize validates provided fields and canonicalises strings.
func (in UpdateTenantInput) normalize() (UpdateTenantInput, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return in, httpx.NewValidationError("name cannot be empty")
		}
		in.Name = &n
	}
	if in.Slug != nil {
		s := strings.ToLower(strings.TrimSpace(*in.Slug))
		if !slugPattern.MatchString(s) {
			return in, httpx.NewValidationError("slug must match ^[a-z0-9][a-z0-9-]{1,62}$")
		}
		in.Slug = &s
	}
	if in.BaseCurrency != nil {
		cur, err := money.ParseCurrency(*in.BaseCurrency)
		if err != nil {
			return in, httpx.NewValidationError(err.Error())
		}
		s := string(cur)
		in.BaseCurrency = &s
	}
	if in.CycleAnchorDay != nil {
		d := *in.CycleAnchorDay
		if d < 1 || d > 31 {
			return in, httpx.NewValidationError("cycleAnchorDay must be between 1 and 31")
		}
		in.CycleAnchorDay = &d
	}
	if in.Locale != nil {
		l := strings.TrimSpace(*in.Locale)
		if l == "" {
			return in, httpx.NewValidationError("locale cannot be empty")
		}
		in.Locale = &l
	}
	if in.Timezone != nil {
		tz := strings.TrimSpace(*in.Timezone)
		if tz == "" {
			return in, httpx.NewValidationError("timezone cannot be empty")
		}
		in.Timezone = &tz
	}
	return in, nil
}

// GetTenant returns a tenant by id, skipping soft-deleted rows.
func (s *Service) GetTenant(ctx context.Context, tenantID uuid.UUID) (*Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx, `
		select id, name, slug, base_currency, cycle_anchor_day, locale, timezone,
		       deleted_at, created_at
		from tenants where id = $1 and deleted_at is null
	`, tenantID).Scan(
		&t.ID, &t.Name, &t.Slug, &t.BaseCurrency, &t.CycleAnchorDay,
		&t.Locale, &t.Timezone, &t.DeletedAt, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("tenant")
		}
		return nil, err
	}
	return &t, nil
}

// UpdateTenant applies the PATCH and returns the updated tenant. Soft-deleted
// tenants are not updatable — restore first.
func (s *Service) UpdateTenant(ctx context.Context, tenantID uuid.UUID, raw UpdateTenantInput) (*Tenant, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	args = append(args, tenantID) // $1 in WHERE

	next := func(val any) string {
		args = append(args, val)
		return fmt.Sprintf("$%d", len(args))
	}

	if in.Name != nil {
		sets = append(sets, "name = "+next(*in.Name))
	}
	if in.Slug != nil {
		sets = append(sets, "slug = "+next(*in.Slug))
	}
	if in.BaseCurrency != nil {
		sets = append(sets, "base_currency = "+next(*in.BaseCurrency))
	}
	if in.CycleAnchorDay != nil {
		sets = append(sets, "cycle_anchor_day = "+next(*in.CycleAnchorDay))
	}
	if in.Locale != nil {
		sets = append(sets, "locale = "+next(*in.Locale))
	}
	if in.Timezone != nil {
		sets = append(sets, "timezone = "+next(*in.Timezone))
	}

	if len(sets) == 0 {
		return s.GetTenant(ctx, tenantID)
	}

	q := fmt.Sprintf(
		`update tenants set %s where id = $1 and deleted_at is null returning id`,
		strings.Join(sets, ", "),
	)
	var gotID uuid.UUID
	err = s.pool.QueryRow(ctx, q, args...).Scan(&gotID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("tenant")
		}
		if isUniqueViolation(err, "tenants_slug_key") {
			return nil, httpx.NewValidationError("slug is already in use")
		}
		return nil, fmt.Errorf("update tenant: %w", err)
	}
	return s.GetTenant(ctx, tenantID)
}

// SoftDeleteTenant sets deleted_at = now(). Idempotent (coalesce keeps the
// first deletion timestamp). NotFound if the tenant id doesn't exist at all.
func (s *Service) SoftDeleteTenant(ctx context.Context, tenantID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx,
		`update tenants set deleted_at = coalesce(deleted_at, now()) where id = $1`, tenantID)
	if err != nil {
		return fmt.Errorf("soft-delete tenant: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return httpx.NewNotFoundError("tenant")
	}
	return nil
}

// RestoreTenant clears deleted_at. Idempotent (restoring a non-deleted tenant
// is a no-op, not an error). NotFound if the tenant id doesn't exist at all.
func (s *Service) RestoreTenant(ctx context.Context, tenantID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx,
		`update tenants set deleted_at = null where id = $1`, tenantID)
	if err != nil {
		return fmt.Errorf("restore tenant: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return httpx.NewNotFoundError("tenant")
	}
	return nil
}

// InsertTenantTx inserts a tenants row with a unique slug derived from the
// input name; retries with numeric suffixes up to 100 times on slug collision.
// Exported so auth.Service.Signup can reuse it inside its own transaction.
func InsertTenantTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in CreateTenantInput) (Tenant, error) {
	base := Slugify(in.Name)
	if base == "" || len(base) < 2 {
		base = "workspace"
	}
	slug := base
	for i := 0; i < 100; i++ {
		var t Tenant
		err := tx.QueryRow(ctx, `
			insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
			values ($1,$2,$3,$4,$5,$6,$7)
			returning id, name, slug, base_currency, cycle_anchor_day, locale, timezone, deleted_at, created_at
		`, id, in.Name, slug, in.BaseCurrency, in.CycleAnchorDay, in.Locale, in.Timezone).
			Scan(&t.ID, &t.Name, &t.Slug, &t.BaseCurrency, &t.CycleAnchorDay, &t.Locale, &t.Timezone, &t.DeletedAt, &t.CreatedAt)
		if err == nil {
			return t, nil
		}
		if !isUniqueViolation(err, "tenants_slug_key") {
			return Tenant{}, fmt.Errorf("insert tenant: %w", err)
		}
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}
	return Tenant{}, httpx.NewValidationError("could not generate unique slug")
}

// InsertMembershipTx inserts a tenant_memberships row. Exported for reuse.
func InsertMembershipTx(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, role Role) (Membership, error) {
	var m Membership
	err := tx.QueryRow(ctx, `
		insert into tenant_memberships (tenant_id, user_id, role)
		values ($1, $2, $3)
		returning tenant_id, user_id, role, created_at
	`, tenantID, userID, role).Scan(&m.TenantID, &m.UserID, &m.Role, &m.CreatedAt)
	if err != nil {
		return Membership{}, fmt.Errorf("insert membership: %w", err)
	}
	return m, nil
}

// isUniqueViolation reports whether err is a Postgres 23505 unique-violation
// for the given constraint name. Accepts any constraint when name is "".
//
// *pgconn.PgError exposes Code and ConstraintName as fields (not methods), so
// an interface-based errors.As would never match. Unwrap to the concrete type.
func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	if pe.Code != "23505" {
		return false
	}
	if constraint == "" {
		return true
	}
	return pe.ConstraintName == constraint
}
