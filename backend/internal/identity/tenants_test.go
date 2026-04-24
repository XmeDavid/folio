package identity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestService_UpdateTenant_RenamesAndChangesSlug(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
	})

	newName := "Alice Home"
	// Slug is lowercased by UpdateTenant.normalize(); match that here so the
	// equality check passes regardless of Base64 case in the random suffix.
	newSlug := strings.ToLower("alice-home-" + testdb.Base64URL(tenantID[:4]))
	updated, err := svc.UpdateTenant(context.Background(), tenantID, identity.UpdateTenantInput{
		Name: &newName,
		Slug: &newSlug,
	})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if updated.Name != newName || updated.Slug != newSlug {
		t.Fatalf("tenant not updated: %+v", updated)
	}
}

func TestService_UpdateTenant_RejectsBadSlug(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
	})

	bad := "Not a Slug!"
	_, err := svc.UpdateTenant(context.Background(), tenantID, identity.UpdateTenantInput{Slug: &bad})
	if err == nil {
		t.Fatal("expected validation error for bad slug")
	}
}

func TestService_UpdateTenant_SlugCollision(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	_, existingSlug := testdb.CreateTestTenant(t, pool, "Shared "+t.Name())
	targetID, _ := testdb.CreateTestTenant(t, pool, "Other "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from tenants where slug = $1`, existingSlug)
		pool.Exec(context.Background(), `delete from tenants where id = $1`, targetID)
	})

	_, err := svc.UpdateTenant(context.Background(), targetID, identity.UpdateTenantInput{Slug: &existingSlug})
	if err == nil {
		t.Fatal("expected slug-collision error")
	}
}

func TestService_SoftDeleteAndRestoreTenant(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	tenantID, _ := testdb.CreateTestTenant(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
	})

	if err := svc.SoftDeleteTenant(context.Background(), tenantID); err != nil {
		t.Fatalf("SoftDeleteTenant: %v", err)
	}
	if _, err := svc.GetTenant(context.Background(), tenantID); err == nil {
		t.Fatal("expected GetTenant to miss soft-deleted tenant")
	}
	if err := svc.RestoreTenant(context.Background(), tenantID); err != nil {
		t.Fatalf("RestoreTenant: %v", err)
	}
	got, err := svc.GetTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("GetTenant after restore: %v", err)
	}
	if got.DeletedAt != nil {
		t.Fatalf("expected deleted_at cleared, got %v", got.DeletedAt)
	}
}
