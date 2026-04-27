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

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Service owns writes and reads for users, workspaces, and memberships.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService constructs a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// Me returns the user + every workspace they belong to, with their role per
// workspace. Soft-deleted workspaces are excluded.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, []WorkspaceWithRole, error) {
	q := dbq.New(s.pool)
	row, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, nil, httpx.NewNotFoundError("user")
		}
		return User{}, nil, fmt.Errorf("select user: %w", err)
	}
	u := User{
		ID:              row.ID,
		Email:           row.Email,
		DisplayName:     row.DisplayName,
		EmailVerifiedAt: row.EmailVerifiedAt,
		IsAdmin:         row.IsAdmin,
		LastWorkspaceID: row.LastWorkspaceID,
		CreatedAt:       row.CreatedAt,
	}

	rows, err := q.ListWorkspacesWithRoleByUser(ctx, userID)
	if err != nil {
		return u, nil, fmt.Errorf("list memberships: %w", err)
	}
	var workspaces []WorkspaceWithRole
	for _, r := range rows {
		workspaces = append(workspaces, WorkspaceWithRole{
			Workspace: Workspace{
				ID:             r.ID,
				Name:           r.Name,
				Slug:           r.Slug,
				BaseCurrency:   r.BaseCurrency,
				CycleAnchorDay: int(r.CycleAnchorDay),
				Locale:         r.Locale,
				Timezone:       r.Timezone,
				DeletedAt:      r.DeletedAt,
				CreatedAt:      r.CreatedAt,
			},
			Role: Role(r.Role),
		})
	}
	return u, workspaces, nil
}

// CreateWorkspaceInput is the validated input to CreateWorkspace.
type CreateWorkspaceInput struct {
	Name           string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
}

// Normalize trims + validates the input. Exported for cross-package use
// (auth.Service.Signup reuses it).
func (in CreateWorkspaceInput) Normalize() (CreateWorkspaceInput, error) {
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

// CreateWorkspace creates a workspace with a unique slug derived from its name,
// and installs the calling user as an owner in the same transaction.
func (s *Service) CreateWorkspace(ctx context.Context, userID uuid.UUID, raw CreateWorkspaceInput) (Workspace, Membership, error) {
	in, err := raw.Normalize()
	if err != nil {
		return Workspace{}, Membership{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, Membership{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	t, err := InsertWorkspaceTx(ctx, tx, uuidx.New(), in)
	if err != nil {
		return Workspace{}, Membership{}, err
	}
	m, err := InsertMembershipTx(ctx, tx, t.ID, userID, RoleOwner)
	if err != nil {
		return Workspace{}, Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, Membership{}, fmt.Errorf("commit: %w", err)
	}
	return t, m, nil
}

// ListMembers returns memberships (enriched with user display fields) plus
// currently-pending invites for workspaceID. "Pending" means accepted_at IS NULL
// AND revoked_at IS NULL AND expires_at > now().
func (s *Service) ListMembers(ctx context.Context, workspaceID uuid.UUID) (*MembersResponse, error) {
	out := &MembersResponse{Members: []MemberWithUser{}, PendingInvites: []PendingInvite{}}
	q := dbq.New(s.pool)

	mRows, err := q.ListMembersWithUser(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	for _, r := range mRows {
		out.Members = append(out.Members, MemberWithUser{
			Membership: Membership{
				WorkspaceID: r.WorkspaceID,
				UserID:      r.UserID,
				Role:        Role(r.Role),
				CreatedAt:   r.CreatedAt,
			},
			Email:       r.Email,
			DisplayName: r.DisplayName,
		})
	}

	iRows, err := q.ListPendingInvites(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	for _, r := range iRows {
		out.PendingInvites = append(out.PendingInvites, PendingInvite{
			ID:        r.ID,
			Email:     r.Email,
			Role:      Role(r.Role),
			InvitedBy: r.InvitedByUserID,
			InvitedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt,
		})
	}
	return out, nil
}

// UpdateWorkspaceInput is the PATCH body for workspace settings. Pointer fields
// mean "absent"; a non-nil pointer to the zero value means "clear".
type UpdateWorkspaceInput struct {
	Name           *string
	Slug           *string
	BaseCurrency   *string
	CycleAnchorDay *int
	Locale         *string
	Timezone       *string
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// normalize validates provided fields and canonicalises strings.
func (in UpdateWorkspaceInput) normalize() (UpdateWorkspaceInput, error) {
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

// GetWorkspace returns a workspace by id, skipping soft-deleted rows.
func (s *Service) GetWorkspace(ctx context.Context, workspaceID uuid.UUID) (*Workspace, error) {
	row, err := dbq.New(s.pool).GetWorkspaceByID(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("workspace")
		}
		return nil, err
	}
	t := &Workspace{
		ID:             row.ID,
		Name:           row.Name,
		Slug:           row.Slug,
		BaseCurrency:   row.BaseCurrency,
		CycleAnchorDay: int(row.CycleAnchorDay),
		Locale:         row.Locale,
		Timezone:       row.Timezone,
		DeletedAt:      row.DeletedAt,
		CreatedAt:      row.CreatedAt,
	}
	return t, nil
}

// UpdateWorkspace applies the PATCH and returns the updated workspace. Soft-deleted
// workspaces are not updatable — restore first.
//
// NOTE: This method uses dynamic SQL (conditional SET clauses) which cannot be
// expressed as a single sqlc query. The raw pool.QueryRow call is intentional.
func (s *Service) UpdateWorkspace(ctx context.Context, workspaceID uuid.UUID, raw UpdateWorkspaceInput) (*Workspace, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	args = append(args, workspaceID) // $1 in WHERE

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
		return s.GetWorkspace(ctx, workspaceID)
	}

	q := fmt.Sprintf(
		`update workspaces set %s where id = $1 and deleted_at is null returning id`,
		strings.Join(sets, ", "),
	)
	var gotID uuid.UUID
	err = s.pool.QueryRow(ctx, q, args...).Scan(&gotID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("workspace")
		}
		if isUniqueViolation(err, "workspaces_slug_key") {
			return nil, httpx.NewValidationError("slug is already in use")
		}
		return nil, fmt.Errorf("update workspace: %w", err)
	}
	return s.GetWorkspace(ctx, workspaceID)
}

// SoftDeleteWorkspace sets deleted_at = now(). Idempotent (coalesce keeps the
// first deletion timestamp). NotFound if the workspace id doesn't exist at all.
func (s *Service) SoftDeleteWorkspace(ctx context.Context, workspaceID uuid.UUID) error {
	n, err := dbq.New(s.pool).SoftDeleteWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("soft-delete workspace: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("workspace")
	}
	return nil
}

// RestoreWorkspace clears deleted_at. Idempotent (restoring a non-deleted workspace
// is a no-op, not an error). NotFound if the workspace id doesn't exist at all.
func (s *Service) RestoreWorkspace(ctx context.Context, workspaceID uuid.UUID) error {
	n, err := dbq.New(s.pool).RestoreWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("restore workspace: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("workspace")
	}
	return nil
}

// InsertWorkspaceTx inserts a workspaces row with a unique slug derived from the
// input name; retries with numeric suffixes up to 100 times on slug
// collision. Exported so auth.Service.Signup can reuse it inside its own
// transaction.
//
// Each attempt is wrapped in a SAVEPOINT so a collision aborts only that
// attempt — not the whole outer transaction. Without this, a single
// collision would poison later statements in the same tx with
// SQLSTATE 25P02 (transaction aborted).
func InsertWorkspaceTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in CreateWorkspaceInput) (Workspace, error) {
	base := Slugify(in.Name)
	if base == "" || len(base) < 2 {
		base = "workspace"
	}
	slug := base
	for i := 0; i < 100; i++ {
		if _, err := tx.Exec(ctx, `savepoint insert_workspace`); err != nil {
			return Workspace{}, fmt.Errorf("savepoint: %w", err)
		}

		row, err := dbq.New(tx).InsertWorkspace(ctx, dbq.InsertWorkspaceParams{
			ID:             id,
			Name:           in.Name,
			Slug:           slug,
			BaseCurrency:   in.BaseCurrency,
			CycleAnchorDay: int16(in.CycleAnchorDay),
			Locale:         in.Locale,
			Timezone:       in.Timezone,
		})
		if err == nil {
			if _, relErr := tx.Exec(ctx, `release savepoint insert_workspace`); relErr != nil {
				return Workspace{}, fmt.Errorf("release savepoint: %w", relErr)
			}
			return Workspace{
				ID:             row.ID,
				Name:           row.Name,
				Slug:           row.Slug,
				BaseCurrency:   row.BaseCurrency,
				CycleAnchorDay: int(row.CycleAnchorDay),
				Locale:         row.Locale,
				Timezone:       row.Timezone,
				DeletedAt:      row.DeletedAt,
				CreatedAt:      row.CreatedAt,
			}, nil
		}
		// Rollback to the savepoint so the outer tx stays alive.
		if _, rbErr := tx.Exec(ctx, `rollback to savepoint insert_workspace`); rbErr != nil {
			return Workspace{}, fmt.Errorf("rollback to savepoint: %w (orig: %v)", rbErr, err)
		}
		if !isUniqueViolation(err, "workspaces_slug_key") {
			return Workspace{}, fmt.Errorf("insert workspace: %w", err)
		}
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}
	return Workspace{}, httpx.NewValidationError("could not generate unique slug")
}

// InsertMembershipTx inserts a workspace_memberships row. Exported for reuse.
func InsertMembershipTx(ctx context.Context, tx pgx.Tx, workspaceID, userID uuid.UUID, role Role) (Membership, error) {
	row, err := dbq.New(tx).InsertMembership(ctx, dbq.InsertMembershipParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
		Role:        dbq.WorkspaceRole(role),
	})
	if err != nil {
		return Membership{}, fmt.Errorf("insert membership: %w", err)
	}
	return Membership{
		WorkspaceID: row.WorkspaceID,
		UserID:      row.UserID,
		Role:        Role(row.Role),
		CreatedAt:   row.CreatedAt,
	}, nil
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

// Sentinel errors surfaced by the member-management methods. Handlers map
// these to typed 422 responses so the UI can show stable error codes.
var (
	ErrLastOwner     = errors.New("identity: operation would leave workspace without an owner")
	ErrLastWorkspace = errors.New("identity: user cannot leave their last workspace")
	ErrNotAMember    = errors.New("identity: user is not a member of the workspace")
)

func lockWorkspaceMembershipsTx(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) error {
	if err := dbq.New(tx).AcquireWorkspaceMembershipLock(ctx, workspaceID.String()); err != nil {
		return fmt.Errorf("lock workspace memberships: %w", err)
	}
	return nil
}

func lockUserMembershipsTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if err := dbq.New(tx).AcquireUserMembershipLock(ctx, userID.String()); err != nil {
		return fmt.Errorf("lock user memberships: %w", err)
	}
	return nil
}

// ChangeRole updates (workspaceID, userID)'s role. Blocks demotion that would
// remove the last owner. Locks the membership row to serialise concurrent
// role changes.
func (s *Service) ChangeRole(ctx context.Context, workspaceID, userID uuid.UUID, newRole Role) error {
	if !newRole.Valid() {
		return httpx.NewValidationError("role must be 'owner' or 'member'")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockWorkspaceMembershipsTx(ctx, tx, workspaceID); err != nil {
		return err
	}

	q := dbq.New(tx)
	current, err := q.GetMembershipRoleForUpdate(ctx, dbq.GetMembershipRoleForUpdateParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotAMember
		}
		return fmt.Errorf("lock membership: %w", err)
	}
	if Role(current) == newRole {
		return tx.Commit(ctx) // no-op
	}

	// Demoting the last owner is forbidden.
	if Role(current) == RoleOwner && newRole == RoleMember {
		ownerCount, err := q.CountWorkspaceOwners(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if ownerCount <= 1 {
			return ErrLastOwner
		}
	}

	if err := q.UpdateMembershipRole(ctx, dbq.UpdateMembershipRoleParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
		Role:        dbq.WorkspaceRole(newRole),
	}); err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return tx.Commit(ctx)
}

// RemoveMember deletes the (workspaceID, userID) membership. Blocks removing
// the last owner. Does not revoke the user's sessions (spec §6.3).
func (s *Service) RemoveMember(ctx context.Context, workspaceID, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockWorkspaceMembershipsTx(ctx, tx, workspaceID); err != nil {
		return err
	}

	q := dbq.New(tx)
	role, err := q.GetMembershipRoleForUpdate(ctx, dbq.GetMembershipRoleForUpdateParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotAMember
		}
		return fmt.Errorf("lock membership: %w", err)
	}

	if Role(role) == RoleOwner {
		ownerCount, err := q.CountWorkspaceOwners(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if ownerCount <= 1 {
			return ErrLastOwner
		}
	}

	if err := q.DeleteMembership(ctx, dbq.DeleteMembershipParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	}); err != nil {
		return fmt.Errorf("delete membership: %w", err)
	}
	return tx.Commit(ctx)
}

// LeaveWorkspace is the self-serve variant of RemoveMember. Blocks if it would
// leave the workspace without an owner OR if it would leave the user with zero
// memberships (spec §3.4 invariants #1 and #2).
func (s *Service) LeaveWorkspace(ctx context.Context, workspaceID, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockWorkspaceMembershipsTx(ctx, tx, workspaceID); err != nil {
		return err
	}
	if err := lockUserMembershipsTx(ctx, tx, userID); err != nil {
		return err
	}

	q := dbq.New(tx)
	role, err := q.GetMembershipRoleForUpdate(ctx, dbq.GetMembershipRoleForUpdateParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotAMember
		}
		return fmt.Errorf("lock membership: %w", err)
	}

	// Last-workspace guard.
	membershipCount, err := q.CountUserMemberships(ctx, userID)
	if err != nil {
		return fmt.Errorf("count memberships: %w", err)
	}
	if membershipCount <= 1 {
		return ErrLastWorkspace
	}

	// Last-owner guard.
	if Role(role) == RoleOwner {
		ownerCount, err := q.CountWorkspaceOwners(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if ownerCount <= 1 {
			return ErrLastOwner
		}
	}

	if err := q.DeleteMembership(ctx, dbq.DeleteMembershipParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	}); err != nil {
		return fmt.Errorf("delete membership: %w", err)
	}
	return tx.Commit(ctx)
}
