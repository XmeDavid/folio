package testdb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// CreateTestTenant inserts a tenant row and returns it. name is used for
// display name; the slug is derived via identity.Slugify (with a short
// random suffix to avoid collisions across tests that share a pool).
func CreateTestTenant(t *testing.T, pool *pgxpool.Pool, name string) (id uuid.UUID, slug string) {
	t.Helper()
	id = uuidx.New()
	base := identity.Slugify(name)
	if len(base) < 2 {
		base = "workspace"
	}
	// Append a short random suffix so tests that reuse the pool don't collide.
	slug = base + "-" + Base64URL(id[:6])
	_, err := pool.Exec(context.Background(), `
		insert into tenants (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
		values ($1, $2, $3, 'CHF', 1, 'en', 'UTC')
	`, id, name, slug)
	if err != nil {
		t.Fatalf("CreateTestTenant: %v", err)
	}
	return id, slug
}

// CreateTestUser inserts a user row with a stubbed password hash. The email
// is unique-enforced by schema; callers should pass a per-test unique value.
func CreateTestUser(t *testing.T, pool *pgxpool.Pool, email string, verified bool) uuid.UUID {
	t.Helper()
	id := uuidx.New()
	var verifiedAt any
	if verified {
		verifiedAt = time.Now()
	}
	_, err := pool.Exec(context.Background(), `
		insert into users (id, email, display_name, password_hash, email_verified_at)
		values ($1, $2, $2, '$argon2id$stub', $3)
	`, id, email, verifiedAt)
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	return id
}

// CreateTestMembership inserts a tenant_memberships row with the given role.
func CreateTestMembership(t *testing.T, pool *pgxpool.Pool, tenantID, userID uuid.UUID, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		insert into tenant_memberships (tenant_id, user_id, role) values ($1, $2, $3::tenant_role)
	`, tenantID, userID, role)
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
// the production rule for tenant_invites.token_hash.
func HashInviteToken(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// Base64URL encodes b with no padding.
func Base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
