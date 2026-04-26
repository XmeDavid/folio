package identity_test

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestService_UpdateWorkspace_RenamesAndChangesSlug(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from workspaces where id = $1`, workspaceID)
	})

	newName := "Alice Home"
	// Hex (not Base64URL) because the slug regex forbids `_`; hex is also
	// lowercase so UpdateWorkspace.normalize() is a no-op.
	newSlug := "alice-home-" + hex.EncodeToString(workspaceID[:4])
	updated, err := svc.UpdateWorkspace(context.Background(), workspaceID, identity.UpdateWorkspaceInput{
		Name: &newName,
		Slug: &newSlug,
	})
	if err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}
	if updated.Name != newName || updated.Slug != newSlug {
		t.Fatalf("workspace not updated: %+v", updated)
	}
}

func TestService_UpdateWorkspace_RejectsBadSlug(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from workspaces where id = $1`, workspaceID)
	})

	bad := "Not a Slug!"
	_, err := svc.UpdateWorkspace(context.Background(), workspaceID, identity.UpdateWorkspaceInput{Slug: &bad})
	if err == nil {
		t.Fatal("expected validation error for bad slug")
	}
}

func TestService_UpdateWorkspace_SlugCollision(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	_, existingSlug := testdb.CreateTestWorkspace(t, pool, "Shared "+t.Name())
	targetID, _ := testdb.CreateTestWorkspace(t, pool, "Other "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from workspaces where slug = $1`, existingSlug)
		pool.Exec(context.Background(), `delete from workspaces where id = $1`, targetID)
	})

	_, err := svc.UpdateWorkspace(context.Background(), targetID, identity.UpdateWorkspaceInput{Slug: &existingSlug})
	if err == nil {
		t.Fatal("expected slug-collision error")
	}
}

func TestService_SoftDeleteAndRestoreWorkspace(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `delete from workspaces where id = $1`, workspaceID)
	})

	if err := svc.SoftDeleteWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("SoftDeleteWorkspace: %v", err)
	}
	if _, err := svc.GetWorkspace(context.Background(), workspaceID); err == nil {
		t.Fatal("expected GetWorkspace to miss soft-deleted workspace")
	}
	if err := svc.RestoreWorkspace(context.Background(), workspaceID); err != nil {
		t.Fatalf("RestoreWorkspace: %v", err)
	}
	got, err := svc.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace after restore: %v", err)
	}
	if got.DeletedAt != nil {
		t.Fatalf("expected deleted_at cleared, got %v", got.DeletedAt)
	}
}
