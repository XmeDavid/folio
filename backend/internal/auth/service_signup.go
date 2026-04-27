package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// firstRunSignupLockKey serialises concurrent first-run signups. Picked once
// from /dev/urandom; the only contract is "stable across processes".
const firstRunSignupLockKey int64 = 0x46_4F_4C_49_4F_5F_46_52 // "FOLIO_FR"

// SignupInput is the validated input to Signup.
type SignupInput struct {
	Email          string
	Password       string
	DisplayName    string
	WorkspaceName     string
	BaseCurrency   string
	CycleAnchorDay int
	Locale         string
	Timezone       string
	InviteToken    string // plan 2 wires consumption; plan 1 ignores
	IP             net.IP
	UserAgent      string
}

func (in SignupInput) normalize() (SignupInput, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.WorkspaceName = strings.TrimSpace(in.WorkspaceName)
	in.Locale = strings.TrimSpace(in.Locale)
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Email == "" || !strings.Contains(in.Email, "@") {
		return in, httpx.NewValidationError("email is required")
	}
	if in.DisplayName == "" {
		return in, httpx.NewValidationError("displayName is required")
	}
	if err := CheckPasswordPolicy(in.Password, in.Email, in.DisplayName); err != nil {
		return in, err
	}
	if in.WorkspaceName == "" {
		fields := strings.Fields(in.DisplayName)
		first := in.DisplayName
		if len(fields) > 0 {
			first = fields[0]
		}
		in.WorkspaceName = fmt.Sprintf("%s's Workspace", first)
	}
	if in.Locale == "" {
		in.Locale = "en-US"
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
	if in.BaseCurrency == "" {
		in.BaseCurrency = "USD"
	}
	cur, err := money.ParseCurrency(in.BaseCurrency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.BaseCurrency = string(cur)
	return in, nil
}

// SignupResult is returned by Signup.
type SignupResult struct {
	User         identity.User
	Workspace       identity.Workspace
	Membership   identity.Membership
	SessionToken string
}

// Signup creates a user, their Personal workspace, an owner membership, and a
// session — all in one transaction. Returns the plaintext session token for
// the handler to set in a cookie.
func (s *Service) Signup(ctx context.Context, raw SignupInput) (*SignupResult, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password, s.cfg.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.enforceRegistrationModeTx(ctx, tx, in.InviteToken); err != nil {
		return nil, err
	}

	q := dbq.New(tx)
	userID := uuidx.New()
	row, err := q.InsertUserReturning(ctx, dbq.InsertUserReturningParams{
		ID: userID, Email: in.Email, PasswordHash: hash, DisplayName: in.DisplayName,
	})
	if err != nil {
		if isUsersEmailKey(err) {
			return nil, httpx.NewValidationError("that email is already registered")
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	user := identity.User{
		ID: row.ID, Email: row.Email, DisplayName: row.DisplayName,
		EmailVerifiedAt: row.EmailVerifiedAt, IsAdmin: row.IsAdmin,
		LastWorkspaceID: row.LastWorkspaceID, CreatedAt: row.CreatedAt,
	}

	workspaceCI := identity.CreateWorkspaceInput{
		Name: in.WorkspaceName, BaseCurrency: in.BaseCurrency,
		CycleAnchorDay: in.CycleAnchorDay, Locale: in.Locale, Timezone: in.Timezone,
	}
	if _, err := workspaceCI.Normalize(); err != nil {
		return nil, err
	}
	workspace, err := identity.InsertWorkspaceTx(ctx, tx, uuidx.New(), workspaceCI)
	if err != nil {
		return nil, err
	}
	membership, err := identity.InsertMembershipTx(ctx, tx, workspace.ID, userID, identity.RoleOwner)
	if err != nil {
		return nil, err
	}

	if err := q.UpdateUserLastWorkspace(ctx, dbq.UpdateUserLastWorkspaceParams{
		LastWorkspaceID: &workspace.ID, ID: userID,
	}); err != nil {
		return nil, fmt.Errorf("set last_workspace_id: %w", err)
	}
	user.LastWorkspaceID = &workspace.ID

	plaintext, _ := GenerateSessionToken()
	sid := SessionIDFromToken(plaintext)
	now := s.now().UTC()
	if err := q.InsertSession(ctx, dbq.InsertSessionParams{
		ID: sid, UserID: userID, CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.SessionAbsolute),
		UserAgent: &in.UserAgent, Ip: netIPToAddr(in.IP),
	}); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	// Audit: user.signup and workspace.created.
	if err := writeAuditTx(ctx, tx, &workspace.ID, &userID, "user.signup", "user", userID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	if err := writeAuditTx(ctx, tx, &workspace.ID, &userID, "workspace.created", "workspace", workspace.ID, nil, nil, in.IP, in.UserAgent); err != nil {
		return nil, err
	}
	verifyPlaintext, verifyHash := GenerateSessionToken()
	verifyTokenID := uuidx.New()
	if err := q.InsertAuthToken(ctx, dbq.InsertAuthTokenParams{
		ID: verifyTokenID, UserID: userID, Purpose: purposeEmailVerify,
		TokenHash: verifyHash, Email: &user.Email,
		ExpiresAt: now.Add(verifyEmailTTL),
	}); err != nil {
		return nil, fmt.Errorf("insert verify token: %w", err)
	}
	if err := s.enqueueEmailTx(ctx, tx, jobs.SendEmailArgs{
		TemplateName:   "verify_email",
		ToAddress:      user.Email,
		IdempotencyKey: fmt.Sprintf("verify_email:%s", verifyTokenID),
		Data: map[string]any{
			"DisplayName": user.DisplayName,
			"VerifyURL":   s.cfg.AppURL + "/auth/verify/" + verifyPlaintext,
		},
	}); err != nil {
		return nil, err
	}

	// Consume an invite if one was supplied. The invite must match the
	// signup email (spec §4.2). Verification is bypassed on purpose: signing
	// up with an invite token sent to this email proves the address.
	if in.InviteToken != "" {
		inv, err := q.GetWorkspaceInviteByTokenHash(ctx, identity.HashInviteToken(in.InviteToken))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, identity.ErrInviteNotFound
			}
			return nil, fmt.Errorf("select invite: %w", err)
		}
		if inv.RevokedAt != nil {
			return nil, identity.ErrInviteRevoked
		}
		if inv.AcceptedAt != nil {
			return nil, identity.ErrInviteAlreadyUsed
		}
		if inv.ExpiresAt.Before(s.now()) {
			return nil, identity.ErrInviteExpired
		}
		if strings.ToLower(inv.Email) != strings.ToLower(in.Email) {
			return nil, identity.ErrInviteEmailMismatch
		}
		if err := q.InsertInvitedMembership(ctx, dbq.InsertInvitedMembershipParams{
			WorkspaceID: inv.WorkspaceID, UserID: userID,
			Column3: dbq.WorkspaceRole(inv.Role),
		}); err != nil {
			return nil, fmt.Errorf("insert invited membership: %w", err)
		}
		if err := q.AcceptWorkspaceInvite(ctx, inv.ID); err != nil {
			return nil, fmt.Errorf("consume invite: %w", err)
		}
		if err := writeAuditTx(ctx, tx, &inv.WorkspaceID, &userID, "member.invite_accepted",
			"invite", inv.ID, nil, map[string]any{"role": inv.Role, "email": inv.Email},
			in.IP, in.UserAgent); err != nil {
			return nil, err
		}
	}

	if s.cfg.AdminBootstrapHook != nil {
		if err := s.cfg.AdminBootstrapHook(ctx, tx, user.ID, user.Email); err != nil {
			return nil, fmt.Errorf("admin bootstrap: %w", err)
		}
		// Keep the returned user in sync with the in-tx grant so the first
		// signup response reflects is_admin=true without a refetch.
		isAdmin, err := q.GetUserIsAdmin(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("refresh is_admin: %w", err)
		}
		user.IsAdmin = isAdmin
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &SignupResult{User: user, Workspace: workspace, Membership: membership, SessionToken: plaintext}, nil
}

// enforceRegistrationModeTx runs inside the signup transaction so the
// first-run "is this the first user?" check sees a consistent snapshot,
// guarded by an advisory lock that serialises concurrent first-run signups.
// invite_only allows the first-ever user to bootstrap the instance; after
// that it requires a non-empty token. Token validity is verified later in the
// same transaction.
func (s *Service) enforceRegistrationModeTx(ctx context.Context, tx dbq.DBTX, inviteToken string) error {
	switch s.cfg.Registration {
	case RegistrationOpen:
		return nil
	case RegistrationFirstRunOnly:
		exists, err := s.userExistsForRegistrationTx(ctx, tx)
		if err != nil {
			return err
		}
		if exists {
			return httpx.NewValidationError("registration is closed on this instance")
		}
		return nil
	case RegistrationInviteOnly:
		if strings.TrimSpace(inviteToken) == "" {
			exists, err := s.userExistsForRegistrationTx(ctx, tx)
			if err != nil {
				return err
			}
			if exists {
				return httpx.NewValidationError("invite-only mode: signup requires an invite token")
			}
		}
		return nil
	default:
		return errors.New("unknown registration mode")
	}
}

func (s *Service) userExistsForRegistrationTx(ctx context.Context, tx dbq.DBTX) (bool, error) {
	q := dbq.New(tx)
	if err := q.AcquireFirstRunLock(ctx, firstRunSignupLockKey); err != nil {
		return false, fmt.Errorf("first-run lock: %w", err)
	}
	exists, err := q.UserExists(ctx)
	if err != nil {
		return false, fmt.Errorf("first-run check: %w", err)
	}
	return exists, nil
}

func isUsersEmailKey(err error) bool {
	// *pgconn.PgError exposes Code and ConstraintName as fields (not methods),
	// so an interface-based errors.As would never match. Unwrap to the concrete
	// type instead.
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Code == "23505" && pe.ConstraintName == "users_email_key"
}
