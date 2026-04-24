package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// ctxKey is a private key type for request-scoped values injected by auth
// middleware. The full middleware surface (RequireSession, RequireMembership,
// RequireRole, RequireFreshReauth) lands in plan §7.
type ctxKey int

const (
	ctxKeyTenant ctxKey = iota + 1
	ctxKeyUser
	ctxKeySession
)

// TenantContext is the minimal tenant identity attached to a request after
// RequireMembership resolves the path-param tenantId against the caller's
// memberships. Plan §7 will populate Role and related fields; plan §1 carries
// just the ID so identity.Handler can compile against the eventual middleware
// contract.
type TenantContext struct {
	ID uuid.UUID
}

// MustTenant returns the tenant attached to r by RequireMembership. Panics
// when called on a request that didn't traverse the middleware — that's a
// programming error, not a user error.
func MustTenant(r *http.Request) *TenantContext {
	t, ok := r.Context().Value(ctxKeyTenant).(*TenantContext)
	if !ok {
		panic("auth: MustTenant called without RequireMembership middleware")
	}
	return t
}

// WithTenant returns a derived context carrying tenant. Exported for tests
// and for middleware in plan §7 to populate the request.
func WithTenant(ctx context.Context, t *TenantContext) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, t)
}

// MustUserID returns the authenticated user id attached to r by RequireSession.
// Panics when the middleware hasn't run. Plan §7 wires the middleware.
func MustUserID(r *http.Request) uuid.UUID {
	id, ok := r.Context().Value(ctxKeyUser).(uuid.UUID)
	if !ok {
		panic("auth: MustUserID called without RequireSession middleware")
	}
	return id
}

// WithUserID returns a derived context carrying the authenticated user id.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyUser, id)
}

// SessionFromContext returns the Session attached to ctx by RequireSession,
// if any. Returns (nil, false) when unset.
func SessionFromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(*Session)
	return s, ok
}

// WithSession returns a derived context carrying the session. Exported for
// tests and for middleware in plan §7 to populate the request.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKeySession, s)
}
