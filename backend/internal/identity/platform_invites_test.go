package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/testdb"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// cleanupPlatformInviteAdmin removes the admin user (and any platform_invites
// they created) at test end.
func cleanupPlatformInviteAdmin(t *testing.T, adminID uuid.UUID) {
	t.Helper()
	pool := testdb.Open(t)
	_, _ = pool.Exec(context.Background(),
		`delete from platform_invites where created_by = $1 or accepted_by = $1 or revoked_by = $1`, adminID)
	_, _ = pool.Exec(context.Background(),
		`delete from users where id = $1`, adminID)
}

func TestPlatformInviteService_Create_ReturnsPlaintextAndStoresHash(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inviteeEmail := uniqueEmail(t, "alpha")
	inv, plaintext, err := svc.Create(context.Background(), admin, inviteeEmail)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if plaintext == "" {
		t.Fatal("expected non-empty plaintext token")
	}
	if inv.ID == uuid.Nil {
		t.Fatal("expected invite ID")
	}
	if inv.Email == nil || *inv.Email != strings.ToLower(inviteeEmail) {
		t.Fatalf("expected email = %q, got %+v", inviteeEmail, inv.Email)
	}
	if inv.CreatedBy != admin {
		t.Fatalf("created_by = %s, want %s", inv.CreatedBy, admin)
	}
	// expires_at ~ now + 14 days.
	wantExpiry := time.Now().Add(14 * 24 * time.Hour)
	delta := inv.ExpiresAt.Sub(wantExpiry)
	if delta < -2*time.Minute || delta > 2*time.Minute {
		t.Fatalf("expires_at = %s, want ~ %s (delta=%s)", inv.ExpiresAt, wantExpiry, delta)
	}

	// DB must hold a hash (not the plaintext).
	var dbHash []byte
	if err := pool.QueryRow(context.Background(),
		`select token_hash from platform_invites where id = $1`, inv.ID).Scan(&dbHash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if string(dbHash) == plaintext {
		t.Fatal("plaintext stored in DB")
	}
	if len(dbHash) != 32 {
		t.Fatalf("expected sha-256 (32 bytes), got %d", len(dbHash))
	}
}

func TestPlatformInviteService_Create_EmptyEmailIsOpenInvite(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, _, err := svc.Create(context.Background(), admin, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inv.Email != nil {
		t.Fatalf("expected open invite (email=nil), got %v", *inv.Email)
	}

	// And for whitespace-only.
	inv2, _, err := svc.Create(context.Background(), admin, "   ")
	if err != nil {
		t.Fatalf("Create whitespace: %v", err)
	}
	if inv2.Email != nil {
		t.Fatalf("expected open invite for whitespace, got %v", *inv2.Email)
	}
}

func TestPlatformInviteService_Preview_HappyPath(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inviteeEmail := uniqueEmail(t, "alpha")
	inv, plaintext, err := svc.Create(context.Background(), admin, inviteeEmail)
	if err != nil {
		t.Fatal(err)
	}

	prev, err := svc.Preview(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if prev.Email == nil || *prev.Email != strings.ToLower(inviteeEmail) {
		t.Fatalf("preview email = %v, want %q", prev.Email, inviteeEmail)
	}
	if !prev.ExpiresAt.Equal(inv.ExpiresAt) {
		t.Fatalf("preview expires_at = %s, want %s", prev.ExpiresAt, inv.ExpiresAt)
	}
}

func TestPlatformInviteService_Preview_OpenInviteNilEmail(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	_, plaintext, err := svc.Create(context.Background(), admin, "")
	if err != nil {
		t.Fatal(err)
	}
	prev, err := svc.Preview(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if prev.Email != nil {
		t.Fatalf("expected nil email for open invite, got %v", *prev.Email)
	}
}

func TestPlatformInviteService_Preview_Expired(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, plaintext, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set expires_at = now() - interval '1 hour' where id = $1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Preview(context.Background(), plaintext); !errors.Is(err, identity.ErrInviteExpired) {
		t.Fatalf("want ErrInviteExpired, got %v", err)
	}
}

func TestPlatformInviteService_Preview_Revoked(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, plaintext, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Preview(context.Background(), plaintext); !errors.Is(err, identity.ErrInviteRevoked) {
		t.Fatalf("want ErrInviteRevoked, got %v", err)
	}
}

func TestPlatformInviteService_Preview_AlreadyAccepted(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, plaintext, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set accepted_at = now(), accepted_by = $2 where id = $1`, inv.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Preview(context.Background(), plaintext); !errors.Is(err, identity.ErrInviteAlreadyUsed) {
		t.Fatalf("want ErrInviteAlreadyUsed, got %v", err)
	}
}

func TestPlatformInviteService_Preview_BadToken(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	if _, err := svc.Preview(context.Background(), "not-a-real-token"); !errors.Is(err, identity.ErrInviteNotFound) {
		t.Fatalf("want ErrInviteNotFound, got %v", err)
	}
}

func TestPlatformInviteService_Revoke_HappyPath(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	var revokedAt *time.Time
	var revokedBy *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`select revoked_at, revoked_by from platform_invites where id = $1`, inv.ID).Scan(&revokedAt, &revokedBy); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil || revokedBy == nil || *revokedBy != admin {
		t.Fatalf("expected revoked_at + revoked_by=%s, got %v / %v", admin, revokedAt, revokedBy)
	}
}

func TestPlatformInviteService_Revoke_DoubleRevoke(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); !errors.Is(err, identity.ErrInviteRevoked) {
		t.Fatalf("want ErrInviteRevoked, got %v", err)
	}
}

func TestPlatformInviteService_Revoke_AfterAccept(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	inv, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set accepted_at = now(), accepted_by = $2 where id = $1`, inv.ID, admin); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); !errors.Is(err, identity.ErrInviteAlreadyUsed) {
		t.Fatalf("want ErrInviteAlreadyUsed, got %v", err)
	}
}

func TestPlatformInviteService_Revoke_NotFound(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	t.Cleanup(func() { cleanupPlatformInviteAdmin(t, admin) })

	if err := svc.Revoke(context.Background(), uuidx.New(), admin); !errors.Is(err, identity.ErrInviteNotFound) {
		t.Fatalf("want ErrInviteNotFound, got %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_HappyPath_EmailMatch(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	inv, plaintext, err := svc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AcceptTx(context.Background(), tx, plaintext, bobEmail, bob); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("AcceptTx: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var acceptedAt *time.Time
	var acceptedBy *uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`select accepted_at, accepted_by from platform_invites where id = $1`, inv.ID).Scan(&acceptedAt, &acceptedBy); err != nil {
		t.Fatal(err)
	}
	if acceptedAt == nil {
		t.Fatal("expected accepted_at to be set")
	}
	if acceptedBy == nil || *acceptedBy != bob {
		t.Fatalf("expected accepted_by = %s, got %v", bob, acceptedBy)
	}
}

func TestPlatformInviteService_AcceptTx_OpenInvite_AcceptsAnyEmail(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	carolEmail := uniqueEmail(t, "carol")
	carol := testdb.CreateTestUser(t, pool, carolEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, carol)
		cleanupPlatformInviteAdmin(t, admin)
	})

	_, plaintext, err := svc.Create(context.Background(), admin, "")
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AcceptTx(context.Background(), tx, plaintext, carolEmail, carol); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("AcceptTx: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_EmailMismatch_CaseInsensitive(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	malloryEmail := uniqueEmail(t, "mallory")
	mallory := testdb.CreateTestUser(t, pool, malloryEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, mallory)
		cleanupPlatformInviteAdmin(t, admin)
	})

	_, plaintext, err := svc.Create(context.Background(), admin, uniqueEmail(t, "bob"))
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Case-insensitive: mallory's UPPERCASE email should still mismatch the bob invite.
	err = svc.AcceptTx(context.Background(), tx, plaintext, strings.ToUpper(malloryEmail), mallory)
	if !errors.Is(err, identity.ErrInviteEmailMismatch) {
		t.Fatalf("want ErrInviteEmailMismatch, got %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_CaseInsensitiveMatch(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	_, plaintext, err := svc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AcceptTx(context.Background(), tx, plaintext, strings.ToUpper(bobEmail), bob); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("AcceptTx (case-insensitive match): %v", err)
	}
	_ = tx.Commit(context.Background())
}

func TestPlatformInviteService_AcceptTx_Revoked(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	inv, plaintext, err := svc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), inv.ID, admin); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := svc.AcceptTx(context.Background(), tx, plaintext, bobEmail, bob); !errors.Is(err, identity.ErrInviteRevoked) {
		t.Fatalf("want ErrInviteRevoked, got %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_Expired(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	inv, plaintext, err := svc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set expires_at = now() - interval '1 hour' where id = $1`, inv.ID); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := svc.AcceptTx(context.Background(), tx, plaintext, bobEmail, bob); !errors.Is(err, identity.ErrInviteExpired) {
		t.Fatalf("want ErrInviteExpired, got %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_AlreadyAccepted(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bobEmail := uniqueEmail(t, "bob")
	bob := testdb.CreateTestUser(t, pool, bobEmail, true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	inv, plaintext, err := svc.Create(context.Background(), admin, bobEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set accepted_at = now(), accepted_by = $2 where id = $1`, inv.ID, bob); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := svc.AcceptTx(context.Background(), tx, plaintext, bobEmail, bob); !errors.Is(err, identity.ErrInviteAlreadyUsed) {
		t.Fatalf("want ErrInviteAlreadyUsed, got %v", err)
	}
}

func TestPlatformInviteService_AcceptTx_BadToken(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bob := testdb.CreateTestUser(t, pool, uniqueEmail(t, "bob"), true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := svc.AcceptTx(context.Background(), tx, "no-such-token", "anything@example.com", bob); !errors.Is(err, identity.ErrInviteNotFound) {
		t.Fatalf("want ErrInviteNotFound, got %v", err)
	}
}

func TestPlatformInviteService_ListActive_OnlyPending_DescOrder(t *testing.T) {
	pool := testdb.Open(t)
	svc := identity.NewPlatformInviteService(pool)
	admin := testdb.CreateTestUser(t, pool, uniqueEmail(t, "admin"), true)
	bob := testdb.CreateTestUser(t, pool, uniqueEmail(t, "bob"), true)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from users where id = $1`, bob)
		cleanupPlatformInviteAdmin(t, admin)
	})

	// active 1
	inv1, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha1"))
	if err != nil {
		t.Fatal(err)
	}
	// Force created_at order (active 1 older).
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set created_at = now() - interval '10 minutes' where id = $1`, inv1.ID); err != nil {
		t.Fatal(err)
	}

	// active 2 (newer)
	inv2, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "alpha2"))
	if err != nil {
		t.Fatal(err)
	}

	// expired (must not appear)
	invExpired, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "expired"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set expires_at = now() - interval '1 hour' where id = $1`, invExpired.ID); err != nil {
		t.Fatal(err)
	}

	// revoked (must not appear)
	invRevoked, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "revoked"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(context.Background(), invRevoked.ID, admin); err != nil {
		t.Fatal(err)
	}

	// accepted (must not appear)
	invAccepted, _, err := svc.Create(context.Background(), admin, uniqueEmail(t, "accepted"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`update platform_invites set accepted_at = now(), accepted_by = $2 where id = $1`, invAccepted.ID, bob); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}

	// Filter to invites we created (the pool may hold rows from other tests).
	wantSet := map[uuid.UUID]bool{inv1.ID: true, inv2.ID: true}
	excludeSet := map[uuid.UUID]bool{invExpired.ID: true, invRevoked.ID: true, invAccepted.ID: true}
	var seen []uuid.UUID
	for _, inv := range got {
		if excludeSet[inv.ID] {
			t.Fatalf("ListActive returned excluded invite %s", inv.ID)
		}
		if wantSet[inv.ID] {
			seen = append(seen, inv.ID)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("expected to see 2 active invites, saw %d (ids=%v)", len(seen), seen)
	}
	// inv2 (newer) must come before inv1 (older).
	if seen[0] != inv2.ID || seen[1] != inv1.ID {
		t.Fatalf("expected desc order [%s, %s], got %v", inv2.ID, inv1.ID, seen)
	}
}

func TestPlatformInviteService_Create_RejectsBadEmail(t *testing.T) {
	// Validation fires before any DB call, so no real pool is needed.
	svc := identity.NewPlatformInviteService(nil)
	_, _, err := svc.Create(context.Background(), uuidx.New(), "notanemail")
	if err == nil {
		t.Fatal("expected error for email without @, got nil")
	}
	var ve *httpx.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected httpx.ValidationError, got %T: %v", err, err)
	}
}
