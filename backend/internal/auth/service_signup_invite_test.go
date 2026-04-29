package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func uniqueEmail(t *testing.T, local string) string {
	t.Helper()
	return local + "+" + strings.ReplaceAll(uuid.New().String(), "-", "") + "@example.com"
}

func newAuthService(t *testing.T) *auth.Service {
	t.Helper()
	pool := testdb.Open(t)
	return auth.NewService(pool, identity.NewService(pool), identity.NewPlatformInviteService(pool), auth.Config{
		Registration:  auth.RegistrationOpen,
		SecureCookies: false,
	})
}

func TestSignup_WithInviteToken_JoinsInvitedWorkspaceAndConsumesInvite(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	inviteSvc := identity.NewInviteService(pool)

	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	alice := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, alice, "owner")
	bobEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from workspace_invites where workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `delete from workspace_memberships where workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `delete from users where email = $1 or id = $2`, bobEmail, alice)
		_, _ = pool.Exec(context.Background(), `delete from workspaces where id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `delete from sessions`)
	})

	_, plaintext, err := inviteSvc.Create(context.Background(), workspaceID, alice, bobEmail, identity.RoleMember)
	if err != nil {
		t.Fatalf("Create invite: %v", err)
	}

	res, err := authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        bobEmail,
		Password:     "correct horse battery staple",
		DisplayName:  "Bob",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from workspace_memberships where user_id = $1`, res.User.ID).Scan(&count); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 memberships (Personal + invited), got %d", count)
	}

	var accepted bool
	if err := pool.QueryRow(context.Background(),
		`select accepted_at is not null from workspace_invites where workspace_id = $1`, workspaceID).Scan(&accepted); err != nil {
		t.Fatalf("check invite: %v", err)
	}
	if !accepted {
		t.Error("invite should have been marked accepted")
	}

	var auditCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'member.invite_accepted' and workspace_id = $1`,
		workspaceID).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("want 1 member.invite_accepted audit, got %d", auditCount)
	}
}

func TestSignup_WithInviteToken_EmailMismatchRejects(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	inviteSvc := identity.NewInviteService(pool)

	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	alice := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, alice, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from workspace_invites where workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `delete from workspace_memberships where workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, alice)
		_, _ = pool.Exec(context.Background(), `delete from workspaces where id = $1`, workspaceID)
	})

	_, plaintext, err := inviteSvc.Create(context.Background(), workspaceID, alice, uniqueEmail(t, "bob"), identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	_, err = authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        uniqueEmail(t, "carol"),
		Password:     "correct horse battery staple",
		DisplayName:  "Carol",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if !errors.Is(err, identity.ErrInviteEmailMismatch) {
		t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
	}
}

// platformInviteAdmin creates a throw-away admin user to act as the platform
// invite issuer + cleans up any platform_invites they own at test end.
func platformInviteAdmin(t *testing.T) uuid.UUID {
	t.Helper()
	pool := testdb.Open(t)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`delete from platform_invites where created_by = $1 or accepted_by = $1 or revoked_by = $1`, admin)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, admin)
	})
	return admin
}

func TestSignup_ConsumesPlatformInvite_NoWorkspaceMembershipAdded(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	platformSvc := identity.NewPlatformInviteService(pool)

	admin := platformInviteAdmin(t)
	bobEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`delete from workspace_memberships where user_id in (select id from users where email = $1)`, bobEmail)
		_, _ = pool.Exec(context.Background(),
			`delete from workspaces where id in (select last_workspace_id from users where email = $1)`, bobEmail)
		_, _ = pool.Exec(context.Background(), `delete from users where email = $1`, bobEmail)
		_, _ = pool.Exec(context.Background(), `delete from sessions`)
	})

	_, plaintext, err := platformSvc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatalf("Create platform invite: %v", err)
	}

	res, err := authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        bobEmail,
		Password:     "correct horse battery staple",
		DisplayName:  "Bob",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}

	// Platform invite acceptance must NOT add an extra workspace membership —
	// only the Personal workspace created by signup itself.
	var count int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from workspace_memberships where user_id = $1`, res.User.ID).Scan(&count); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 1 {
		t.Errorf("want exactly 1 membership (Personal), got %d", count)
	}

	// platform_invites row must be marked accepted.
	var acceptedAt *time.Time
	var acceptedBy *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`select accepted_at, accepted_by from platform_invites where created_by = $1`, admin).
		Scan(&acceptedAt, &acceptedBy); err != nil {
		t.Fatalf("read platform invite: %v", err)
	}
	if acceptedAt == nil || acceptedBy == nil || *acceptedBy != res.User.ID {
		t.Errorf("expected platform invite accepted by %s, got %v / %v", res.User.ID, acceptedAt, acceptedBy)
	}

	// Audit row written.
	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'user.signup_via_platform_invite' and actor_user_id = $1`,
		res.User.ID).Scan(&auditCount); err != nil {
		t.Fatalf("scan audit count: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("want 1 user.signup_via_platform_invite audit, got %d", auditCount)
	}
}

func TestSignup_PlatformInviteEmailMismatchRejected(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	platformSvc := identity.NewPlatformInviteService(pool)

	admin := platformInviteAdmin(t)
	intendedEmail := uniqueEmail(t, "intended")
	_, plaintext, err := platformSvc.Create(context.Background(), admin, intendedEmail)
	if err != nil {
		t.Fatal(err)
	}

	_, err = authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        uniqueEmail(t, "imposter"),
		Password:     "correct horse battery staple",
		DisplayName:  "Imposter",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if !errors.Is(err, identity.ErrInviteEmailMismatch) {
		t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
	}
}

func TestSignup_RevokedPlatformInviteRejected(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	platformSvc := identity.NewPlatformInviteService(pool)

	admin := platformInviteAdmin(t)
	bobEmail := uniqueEmail(t, "bob")
	inv, plaintext, err := platformSvc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	if err := platformSvc.Revoke(context.Background(), inv.ID, admin); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err = authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        bobEmail,
		Password:     "correct horse battery staple",
		DisplayName:  "Bob",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if !errors.Is(err, identity.ErrInviteRevoked) {
		t.Fatalf("want ErrInviteRevoked, got %v", err)
	}
}

func TestSignup_ExpiredPlatformInviteRejected(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	platformSvc := identity.NewPlatformInviteService(pool)

	admin := platformInviteAdmin(t)
	bobEmail := uniqueEmail(t, "bob")
	inv, plaintext, err := platformSvc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	// Force the invite to be expired in the DB. We can't inject a custom
	// `now` into auth.Service from the _test package, so we travel time the
	// other way: set expires_at into the past.
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set expires_at = now() - interval '1 hour' where id = $1`, inv.ID); err != nil {
		t.Fatalf("expire invite: %v", err)
	}

	_, err = authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        bobEmail,
		Password:     "correct horse battery staple",
		DisplayName:  "Bob",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if !errors.Is(err, identity.ErrInviteExpired) {
		t.Fatalf("want ErrInviteExpired, got %v", err)
	}
}

func TestSignup_PlatformInviteOpenAcceptsAnyEmail(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	platformSvc := identity.NewPlatformInviteService(pool)

	admin := platformInviteAdmin(t)
	// Open invite (empty email).
	_, plaintext, err := platformSvc.Create(context.Background(), admin, "")
	if err != nil {
		t.Fatal(err)
	}

	anyEmail := uniqueEmail(t, "anyone")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`delete from workspace_memberships where user_id in (select id from users where email = $1)`, anyEmail)
		_, _ = pool.Exec(context.Background(),
			`delete from workspaces where id in (select last_workspace_id from users where email = $1)`, anyEmail)
		_, _ = pool.Exec(context.Background(), `delete from users where email = $1`, anyEmail)
		_, _ = pool.Exec(context.Background(), `delete from sessions`)
	})

	res, err := authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        anyEmail,
		Password:     "correct horse battery staple",
		DisplayName:  "Anyone",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  plaintext,
	})
	if err != nil {
		t.Fatalf("Signup with open platform invite: %v", err)
	}

	// One Personal membership only (no extra membership added).
	var count int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from workspace_memberships where user_id = $1`, res.User.ID).Scan(&count); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 1 {
		t.Errorf("want 1 membership (Personal), got %d", count)
	}
}

func TestSignup_BadTokenRejected(t *testing.T) {
	authSvc := newAuthService(t)

	_, err := authSvc.Signup(context.Background(), auth.SignupInput{
		Email:        uniqueEmail(t, "nobody"),
		Password:     "correct horse battery staple",
		DisplayName:  "Nobody",
		BaseCurrency: "CHF",
		Locale:       "en-CH",
		InviteToken:  "this-token-does-not-exist-anywhere",
	})
	if !errors.Is(err, identity.ErrInviteNotFound) {
		t.Fatalf("want ErrInviteNotFound, got %v", err)
	}
}
