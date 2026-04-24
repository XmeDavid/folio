package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// uniqueEmail returns a per-test email so concurrent/repeated runs don't
// collide on the global users.email unique index.
func uniqueEmail(t *testing.T, local string) string {
	t.Helper()
	return local + "+" + strings.ReplaceAll(uuid.New().String(), "-", "") + "@example.com"
}

func cleanupMembership(t *testing.T, tenantID, userID uuid.UUID) {
	t.Helper()
	pool := testdb.Open(t)
	_, _ = pool.Exec(context.Background(),
		`delete from tenant_memberships where tenant_id = $1 and user_id = $2`, tenantID, userID)
	_, _ = pool.Exec(context.Background(),
		`delete from tenants where id = $1`, tenantID)
	_, _ = pool.Exec(context.Background(),
		`delete from users where id = $1`, userID)
}

func TestService_ChangeRole_DemotionBlockedOnLastOwner(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	userID := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, userID, "owner")
	t.Cleanup(func() { cleanupMembership(t, tenantID, userID) })

	err := svc.ChangeRole(context.Background(), tenantID, userID, identity.RoleMember)
	if !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestService_ChangeRole_AllowsDemotionWhenOtherOwnerPresent(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Shared "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	b := testdb.CreateTestUser(t, pool, uniqueEmail(t, "bob"), true)
	testdb.CreateTestMembership(t, pool, tenantID, a, "owner")
	testdb.CreateTestMembership(t, pool, tenantID, b, "owner")
	t.Cleanup(func() {
		cleanupMembership(t, tenantID, a)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, b)
	})

	if err := svc.ChangeRole(context.Background(), tenantID, a, identity.RoleMember); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
}

func TestService_RemoveMember_BlockedOnLastOwner(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Solo "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, a, "owner")
	t.Cleanup(func() { cleanupMembership(t, tenantID, a) })

	err := svc.RemoveMember(context.Background(), tenantID, a)
	if !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestService_LeaveTenant_BlockedWhenOnlyMembership(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "OnlyHome "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, a, "member")
	t.Cleanup(func() { cleanupMembership(t, tenantID, a) })

	err := svc.LeaveTenant(context.Background(), tenantID, a)
	if !errors.Is(err, identity.ErrLastTenant) {
		t.Fatalf("expected ErrLastTenant, got %v", err)
	}
}

func TestService_LeaveTenant_Succeeds_WhenOtherTenantExists(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	t1, _ := testdb.CreateTestTenant(t, pool, "Personal "+t.Name())
	t2, _ := testdb.CreateTestTenant(t, pool, "Household "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, t1, a, "member")
	testdb.CreateTestMembership(t, pool, t2, a, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_memberships where user_id = $1`, a)
		_, _ = pool.Exec(context.Background(), `delete from tenants where id in ($1, $2)`, t1, t2)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, a)
	})

	if err := svc.LeaveTenant(context.Background(), t1, a); err != nil {
		t.Fatalf("LeaveTenant: %v", err)
	}
}

func TestService_ListMembers_IncludesUserFieldsAndPendingInvites(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	ownerEmail := uniqueEmail(t, "alice")
	owner := testdb.CreateTestUser(t, pool, ownerEmail, true)
	testdb.CreateTestMembership(t, pool, tenantID, owner, "owner")
	inviteEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, owner)
	})

	if _, err := pool.Exec(context.Background(), `
		insert into tenant_invites (id, tenant_id, email, role, token_hash, invited_by_user_id, expires_at)
		values ($1, $2, $3, 'member', $4, $5, now() + interval '7 days')
	`, uuidx.New(), tenantID, inviteEmail, testdb.HashInviteToken("raw-"+t.Name()), owner); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	res, err := svc.ListMembers(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(res.Members) != 1 {
		t.Fatalf("want 1 member, got %d", len(res.Members))
	}
	if res.Members[0].Email != ownerEmail {
		t.Errorf("member email = %q, want %q", res.Members[0].Email, ownerEmail)
	}
	if res.Members[0].DisplayName == "" {
		t.Error("member displayName should be populated")
	}
	if len(res.PendingInvites) != 1 {
		t.Fatalf("want 1 pending invite, got %d", len(res.PendingInvites))
	}
	if res.PendingInvites[0].Email != inviteEmail {
		t.Errorf("invite email = %q, want %q", res.PendingInvites[0].Email, inviteEmail)
	}
	if res.PendingInvites[0].Role != identity.RoleMember {
		t.Errorf("invite role = %q, want member", res.PendingInvites[0].Role)
	}
}

func TestService_ListMembers_ExcludesExpiredAndConsumedInvites(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Shared "+t.Name())
	owner := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, tenantID, owner, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenant_invites where tenant_id = $1`, tenantID)
		cleanupMembership(t, tenantID, owner)
	})

	// Expired invite.
	if _, err := pool.Exec(context.Background(), `
		insert into tenant_invites (id, tenant_id, email, role, token_hash, invited_by_user_id, expires_at)
		values ($1, $2, 'expired@example.com', 'member', $3, $4, now() - interval '1 hour')
	`, uuidx.New(), tenantID, testdb.HashInviteToken("expired"), owner); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	// Accepted invite.
	if _, err := pool.Exec(context.Background(), `
		insert into tenant_invites (id, tenant_id, email, role, token_hash, invited_by_user_id, expires_at, accepted_at)
		values ($1, $2, 'accepted@example.com', 'member', $3, $4, now() + interval '7 days', now())
	`, uuidx.New(), tenantID, testdb.HashInviteToken("accepted"), owner); err != nil {
		t.Fatalf("seed accepted: %v", err)
	}
	// Revoked invite.
	if _, err := pool.Exec(context.Background(), `
		insert into tenant_invites (id, tenant_id, email, role, token_hash, invited_by_user_id, expires_at, revoked_at)
		values ($1, $2, 'revoked@example.com', 'member', $3, $4, now() + interval '7 days', now())
	`, uuidx.New(), tenantID, testdb.HashInviteToken("revoked"), owner); err != nil {
		t.Fatalf("seed revoked: %v", err)
	}

	res, err := svc.ListMembers(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(res.PendingInvites) != 0 {
		t.Fatalf("expected 0 pending invites (all expired/accepted/revoked), got %d", len(res.PendingInvites))
	}
}

