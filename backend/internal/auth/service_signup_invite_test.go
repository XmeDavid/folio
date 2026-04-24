package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	return auth.NewService(pool, identity.NewService(pool), auth.Config{
		Registration:  auth.RegistrationOpen,
		SecureCookies: false,
	})
}

func TestSignup_WithInviteToken_JoinsInvitedTenantAndConsumesInvite(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	inviteSvc := identity.NewInviteService(pool)

	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	alice := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, alice, "owner")
	bobEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from tenant_memberships where tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from users where email = $1 or id = $2`, bobEmail, alice)
		_, _ = pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from sessions`)
	})

	_, plaintext, err := inviteSvc.Create(context.Background(), tenantID, alice, bobEmail, identity.RoleMember)
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
		`select count(*) from tenant_memberships where user_id = $1`, res.User.ID).Scan(&count); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 memberships (Personal + invited), got %d", count)
	}

	var accepted bool
	if err := pool.QueryRow(context.Background(),
		`select accepted_at is not null from tenant_invites where tenant_id = $1`, tenantID).Scan(&accepted); err != nil {
		t.Fatalf("check invite: %v", err)
	}
	if !accepted {
		t.Error("invite should have been marked accepted")
	}

	var auditCount int
	_ = pool.QueryRow(context.Background(),
		`select count(*) from audit_events where action = 'member.invite_accepted' and tenant_id = $1`,
		tenantID).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("want 1 member.invite_accepted audit, got %d", auditCount)
	}
}

func TestSignup_WithInviteToken_EmailMismatchRejects(t *testing.T) {
	pool := testdb.Open(t)
	authSvc := newAuthService(t)
	inviteSvc := identity.NewInviteService(pool)

	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	alice := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, alice, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from tenant_memberships where tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, alice)
		_, _ = pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
	})

	_, plaintext, err := inviteSvc.Create(context.Background(), tenantID, alice, uniqueEmail(t, "bob"), identity.RoleMember)
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
