package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
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
