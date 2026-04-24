package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// seedInviteContext creates a tenant + verified inviter + owner membership
// and returns the tenantID, inviterID, and invitee email.
func seedInviteContext(t *testing.T) (tenantID, inviter string, inviteeEmail string) {
	t.Helper()
	return "", "", ""
}

func cleanupInviteTest(t *testing.T, tenantID, inviter string) {
	t.Helper()
}

func TestInviteService_Create_ReturnsPlaintextTokenOnceAndStoresHash(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	inviterEmail := uniqueEmail(t, "alice")
	inviter := testdb.CreateTestUser(t, pool, inviterEmail, true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	inviteeEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
	})

	inv, plaintext, err := svc.Create(context.Background(), tenantID, inviter, inviteeEmail, identity.RoleMember)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if plaintext == "" {
		t.Fatal("expected non-empty plaintext token")
	}
	if inv.Email != inviteeEmail || inv.Role != identity.RoleMember {
		t.Fatalf("invite mismatch: %+v", inv)
	}
	var dbHash []byte
	if err := pool.QueryRow(context.Background(),
		`select token_hash from tenant_invites where id = $1`, inv.ID).Scan(&dbHash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if string(dbHash) == plaintext {
		t.Fatal("plaintext stored in DB")
	}
}

func TestInviteService_Create_RejectsDuplicatePending(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Dup "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	inviteeEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
	})

	if _, _, err := svc.Create(context.Background(), tenantID, inviter, inviteeEmail, identity.RoleMember); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, _, err := svc.Create(context.Background(), tenantID, inviter, inviteeEmail, identity.RoleMember)
	if err == nil {
		t.Fatal("expected duplicate-pending error")
	}
}

func TestInviteService_Preview_NoAuth_ReturnsSanitizedShape(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Shared "+t.Name())
	inviterEmail := uniqueEmail(t, "alice")
	inviter := testdb.CreateTestUser(t, pool, inviterEmail, true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	inviteeEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
	})

	_, plaintext, err := svc.Create(context.Background(), tenantID, inviter, inviteeEmail, identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	prev, err := svc.Preview(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if prev.Email != inviteeEmail {
		t.Errorf("email = %q, want %q", prev.Email, inviteeEmail)
	}
	if prev.InviterDisplayName != inviterEmail {
		t.Errorf("inviter display = %q, want %q (fixture sets display_name = email)", prev.InviterDisplayName, inviterEmail)
	}
	if prev.Role != identity.RoleMember {
		t.Errorf("role = %q, want member", prev.Role)
	}
	if prev.TenantID != tenantID {
		t.Errorf("tenantID mismatch")
	}
}

func TestInviteService_Preview_ExpiredReturnsError(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Exp "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
	})

	_, plaintext, err := svc.Create(context.Background(), tenantID, inviter, uniqueEmail(t, "bob"), identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update tenant_invites set expires_at = now() - interval '1 hour' where tenant_id = $1`, tenantID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Preview(context.Background(), plaintext); !errors.Is(err, identity.ErrInviteExpired) {
		t.Fatalf("want ErrInviteExpired, got %v", err)
	}
}

func TestInviteService_Accept_MatchesEmailAndCreatesMembership(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Accept "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		_, _ = pool.Exec(context.Background(), `delete from tenant_memberships where user_id = $1`, bob)
		cleanupMembership(t, tenantID, inviter)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
	})

	_, plaintext, err := svc.Create(context.Background(), tenantID, inviter, bobEmail, identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	mem, err := svc.Accept(context.Background(), plaintext, bob)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if mem.UserID != bob || mem.Role != identity.RoleMember {
		t.Fatalf("membership mismatch: %+v", mem)
	}

	var acceptedAt *string
	_ = pool.QueryRow(context.Background(),
		`select accepted_at::text from tenant_invites where tenant_id = $1`, tenantID).Scan(&acceptedAt)
	if acceptedAt == nil {
		t.Fatal("expected accepted_at set")
	}
}

func TestInviteService_Accept_MismatchedEmailRejected(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Mism "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	other := testdb.CreateTestUser(t, pool, uniqueEmail(t, "carol"), true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, other)
	})

	_, plaintext, err := svc.Create(context.Background(), tenantID, inviter, uniqueEmail(t, "bob"), identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Accept(context.Background(), plaintext, other); !errors.Is(err, identity.ErrInviteEmailMismatch) {
		t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
	}
}

func TestInviteService_Accept_UnverifiedEmailRejected(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Unver "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, false) // unverified
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
	})

	_, plaintext, err := svc.Create(context.Background(), tenantID, inviter, bobEmail, identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Accept(context.Background(), plaintext, bob); !errors.Is(err, identity.ErrEmailUnverified) {
		t.Fatalf("want ErrEmailUnverified, got %v", err)
	}
}

func TestInviteService_Revoke_BlockedForUnrelatedRequester(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Rev "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	stranger := testdb.CreateTestUser(t, pool, uniqueEmail(t, "mallory"), true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, stranger)
	})

	inv, _, err := svc.Create(context.Background(), tenantID, inviter, uniqueEmail(t, "bob"), identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Revoke(context.Background(), inv.ID, stranger); !errors.Is(err, identity.ErrNotAuthorized) {
		t.Fatalf("want ErrNotAuthorized, got %v", err)
	}
}

func TestInviteService_Revoke_AllowedForInviter(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewInviteService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Rev2 "+t.Name())
	inviter := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, inviter, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, inviter)
	})

	inv, _, err := svc.Create(context.Background(), tenantID, inviter, uniqueEmail(t, "bob"), identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Revoke(context.Background(), inv.ID, inviter); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Idempotent second call.
	if err := svc.Revoke(context.Background(), inv.ID, inviter); err != nil {
		t.Fatalf("Revoke (second): %v", err)
	}
}

// silence the unused seed helpers — kept as placeholders for future
// convenience helpers if test setup grows.
var _ = seedInviteContext
var _ = cleanupInviteTest
