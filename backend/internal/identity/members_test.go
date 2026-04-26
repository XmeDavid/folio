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

func cleanupMembership(t *testing.T, workspaceID, userID uuid.UUID) {
	t.Helper()
	pool := testdb.Open(t)
	_, _ = pool.Exec(context.Background(),
		`delete from workspace_memberships where workspace_id = $1 and user_id = $2`, workspaceID, userID)
	_, _ = pool.Exec(context.Background(),
		`delete from workspaces where id = $1`, workspaceID)
	_, _ = pool.Exec(context.Background(),
		`delete from users where id = $1`, userID)
}

func TestService_ChangeRole_DemotionBlockedOnLastOwner(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	userID := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, userID, "owner")
	t.Cleanup(func() { cleanupMembership(t, workspaceID, userID) })

	err := svc.ChangeRole(context.Background(), workspaceID, userID, identity.RoleMember)
	if !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestService_ChangeRole_AllowsDemotionWhenOtherOwnerPresent(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Shared "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	b := testdb.CreateTestUser(t, pool, uniqueEmail(t, "bob"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, a, "owner")
	testdb.CreateTestMembership(t, pool, workspaceID, b, "owner")
	t.Cleanup(func() {
		cleanupMembership(t, workspaceID, a)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, b)
	})

	if err := svc.ChangeRole(context.Background(), workspaceID, a, identity.RoleMember); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
}

func TestService_RemoveMember_BlockedOnLastOwner(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Solo "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, a, "owner")
	t.Cleanup(func() { cleanupMembership(t, workspaceID, a) })

	err := svc.RemoveMember(context.Background(), workspaceID, a)
	if !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestService_LeaveWorkspace_BlockedWhenOnlyMembership(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "OnlyHome "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, a, "member")
	t.Cleanup(func() { cleanupMembership(t, workspaceID, a) })

	err := svc.LeaveWorkspace(context.Background(), workspaceID, a)
	if !errors.Is(err, identity.ErrLastWorkspace) {
		t.Fatalf("expected ErrLastWorkspace, got %v", err)
	}
}

func TestService_LeaveWorkspace_Succeeds_WhenOtherWorkspaceExists(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	t1, _ := testdb.CreateTestWorkspace(t, pool, "Personal "+t.Name())
	t2, _ := testdb.CreateTestWorkspace(t, pool, "Household "+t.Name())
	a := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, t1, a, "member")
	testdb.CreateTestMembership(t, pool, t2, a, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from workspace_memberships where user_id = $1`, a)
		_, _ = pool.Exec(context.Background(), `delete from workspaces where id in ($1, $2)`, t1, t2)
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, a)
	})

	if err := svc.LeaveWorkspace(context.Background(), t1, a); err != nil {
		t.Fatalf("LeaveWorkspace: %v", err)
	}
}

func TestService_ListMembers_IncludesUserFieldsAndPendingInvites(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewService(pool)
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Alice "+t.Name())
	ownerEmail := uniqueEmail(t, "alice")
	owner := testdb.CreateTestUser(t, pool, ownerEmail, true)
	testdb.CreateTestMembership(t, pool, workspaceID, owner, "owner")
	inviteEmail := uniqueEmail(t, "bob")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from workspace_invites where workspace_id = $1`, workspaceID)
		cleanupMembership(t, workspaceID, owner)
	})

	if _, err := pool.Exec(context.Background(), `
		insert into workspace_invites (id, workspace_id, email, role, token_hash, invited_by_user_id, expires_at)
		values ($1, $2, $3, 'member', $4, $5, now() + interval '7 days')
	`, uuidx.New(), workspaceID, inviteEmail, testdb.HashInviteToken("raw-"+t.Name()), owner); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	res, err := svc.ListMembers(context.Background(), workspaceID)
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
	workspaceID, _ := testdb.CreateTestWorkspace(t, pool, "Shared "+t.Name())
	owner := testdb.CreateTestUser(t, pool, uniqueEmail(t, "alice"), true)
	testdb.CreateTestMembership(t, pool, workspaceID, owner, "owner")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from workspace_invites where workspace_id = $1`, workspaceID)
		cleanupMembership(t, workspaceID, owner)
	})

	// Expired invite.
	if _, err := pool.Exec(context.Background(), `
		insert into workspace_invites (id, workspace_id, email, role, token_hash, invited_by_user_id, expires_at)
		values ($1, $2, 'expired@example.com', 'member', $3, $4, now() - interval '1 hour')
	`, uuidx.New(), workspaceID, testdb.HashInviteToken("expired"), owner); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	// Accepted invite.
	if _, err := pool.Exec(context.Background(), `
		insert into workspace_invites (id, workspace_id, email, role, token_hash, invited_by_user_id, expires_at, accepted_at)
		values ($1, $2, 'accepted@example.com', 'member', $3, $4, now() + interval '7 days', now())
	`, uuidx.New(), workspaceID, testdb.HashInviteToken("accepted"), owner); err != nil {
		t.Fatalf("seed accepted: %v", err)
	}
	// Revoked invite.
	if _, err := pool.Exec(context.Background(), `
		insert into workspace_invites (id, workspace_id, email, role, token_hash, invited_by_user_id, expires_at, revoked_at)
		values ($1, $2, 'revoked@example.com', 'member', $3, $4, now() + interval '7 days', now())
	`, uuidx.New(), workspaceID, testdb.HashInviteToken("revoked"), owner); err != nil {
		t.Fatalf("seed revoked: %v", err)
	}

	res, err := svc.ListMembers(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(res.PendingInvites) != 0 {
		t.Fatalf("expected 0 pending invites (all expired/accepted/revoked), got %d", len(res.PendingInvites))
	}
}

