package testdb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// CreateTestWorkspace inserts a workspace row and returns it. name is used for
// display name; the slug is derived via identity.Slugify (with a short
// random suffix to avoid collisions across tests that share a pool).
func CreateTestWorkspace(t *testing.T, pool *pgxpool.Pool, name string) (id uuid.UUID, slug string) {
	t.Helper()
	id = uuidx.New()
	base := identity.Slugify(name)
	if len(base) < 2 {
		base = "workspace"
	}
	// Append a short hex suffix so tests that reuse the pool don't collide.
	// Hex (not base64url) because the workspaces_slug_check regex
	// `^[a-z0-9][a-z0-9-]{1,62}$` doesn't accept `_` / `-` from base64url,
	// and the slug is already lowercased by Slugify. Cap at 63 chars.
	suffix := "-" + hex.EncodeToString(id[:6])
	trimmed := base
	if max := 63 - len(suffix); len(trimmed) > max {
		trimmed = strings.TrimRight(trimmed[:max], "-")
	}
	slug = trimmed + suffix
	err := dbq.New(pool).InsertTestWorkspace(context.Background(), dbq.InsertTestWorkspaceParams{
		ID:   id,
		Name: name,
		Slug: slug,
	})
	if err != nil {
		t.Fatalf("CreateTestWorkspace: %v", err)
	}
	return id, slug
}

// CreateTestUser inserts a user row with a stubbed password hash. The email
// is unique-enforced by schema; callers should pass a per-test unique value.
// display_name is passed separately from email so pgx doesn't have to deduce
// a shared type for the citext (email) and text (display_name) columns.
func CreateTestUser(t *testing.T, pool *pgxpool.Pool, email string, verified bool) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	var verifiedAt *time.Time
	if verified {
		now := time.Now()
		verifiedAt = &now
	}
	err := dbq.New(pool).InsertTestUser(context.Background(), dbq.InsertTestUserParams{
		ID:              id,
		Email:           email,
		DisplayName:     email,
		EmailVerifiedAt: verifiedAt,
	})
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	return id
}

// CreateTestMembership inserts a workspace_memberships row with the given role.
func CreateTestMembership(t *testing.T, pool *pgxpool.Pool, workspaceID, userID uuid.UUID, role string) {
	t.Helper()
	err := dbq.New(pool).InsertInvitedMembership(context.Background(), dbq.InsertInvitedMembershipParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
		Column3:     dbq.WorkspaceRole(role),
	})
	if err != nil {
		t.Fatalf("CreateTestMembership: %v", err)
	}
}

// SetSessionReauth bumps sessions.reauth_at — used by plan 4's re-auth tests.
// Plan 2 defines it eagerly so downstream plans don't need to touch this file.
func SetSessionReauth(t *testing.T, pool *pgxpool.Pool, sessionID string, ts time.Time) {
	t.Helper()
	err := dbq.New(pool).UpdateSessionReauthByID(context.Background(), dbq.UpdateSessionReauthByIDParams{
		ID:       sessionID,
		ReauthAt: &ts,
	})
	if err != nil {
		t.Fatalf("SetSessionReauth: %v", err)
	}
}

// HashInviteToken returns the SHA-256 of a base64url invite token — matches
// the production rule for workspace_invites.token_hash.
func HashInviteToken(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// Base64URL encodes b with no padding.
func Base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
