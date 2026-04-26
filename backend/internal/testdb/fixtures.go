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
	_, err := pool.Exec(context.Background(), `
		insert into workspaces (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
		values ($1, $2, $3, 'CHF', 1, 'en', 'UTC')
	`, id, name, slug)
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
	var verifiedAt any
	if verified {
		verifiedAt = time.Now()
	}
	_, err := pool.Exec(context.Background(), `
		insert into users (id, email, display_name, password_hash, email_verified_at)
		values ($1, $2, $3, '$argon2id$stub', $4)
	`, id, email, email, verifiedAt)
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	return id
}

// CreateTestMembership inserts a workspace_memberships row with the given role.
func CreateTestMembership(t *testing.T, pool *pgxpool.Pool, workspaceID, userID uuid.UUID, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		insert into workspace_memberships (workspace_id, user_id, role) values ($1, $2, $3::workspace_role)
	`, workspaceID, userID, role)
	if err != nil {
		t.Fatalf("CreateTestMembership: %v", err)
	}
}

// SetSessionReauth bumps sessions.reauth_at — used by plan 4's re-auth tests.
// Plan 2 defines it eagerly so downstream plans don't need to touch this file.
func SetSessionReauth(t *testing.T, pool *pgxpool.Pool, sessionID string, ts time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`update sessions set reauth_at = $1 where id = $2`,
		ts, sessionID)
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
