package auth

import (
	"context"
	"net/http"

	"github.com/xmedavid/folio/backend/internal/identity"
)

type ctxKey string

const (
	ctxKeyUser    ctxKey = "folio.auth.user"
	ctxKeySession ctxKey = "folio.auth.session"
	ctxKeyTenant  ctxKey = "folio.auth.tenant"
	ctxKeyRole    ctxKey = "folio.auth.role"
)

// WithUser / UserFromCtx attach the authenticated user.
func WithUser(ctx context.Context, u identity.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

func UserFromCtx(ctx context.Context) (identity.User, bool) {
	u, ok := ctx.Value(ctxKeyUser).(identity.User)
	return u, ok
}

// MustUser panics if no user — use only in authed routes mounted under RequireSession.
func MustUser(r *http.Request) identity.User {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		panic("MustUser called without RequireSession upstream")
	}
	return u
}

// WithSession / SessionFromCtx attach the session row.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, ctxKeySession, s)
}

func SessionFromCtx(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(Session)
	return s, ok
}

func MustSession(r *http.Request) Session {
	s, ok := SessionFromCtx(r.Context())
	if !ok {
		panic("MustSession called without RequireSession upstream")
	}
	return s
}

// WithTenant / TenantFromCtx attach the tenant under inspection.
func WithTenant(ctx context.Context, t identity.Tenant) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, t)
}

func TenantFromCtx(ctx context.Context) (identity.Tenant, bool) {
	t, ok := ctx.Value(ctxKeyTenant).(identity.Tenant)
	return t, ok
}

func MustTenant(r *http.Request) identity.Tenant {
	t, ok := TenantFromCtx(r.Context())
	if !ok {
		panic("MustTenant called without RequireMembership upstream")
	}
	return t
}

// WithRole / RoleFromCtx attach the caller's role in the current tenant.
func WithRole(ctx context.Context, r identity.Role) context.Context {
	return context.WithValue(ctx, ctxKeyRole, r)
}

func RoleFromCtx(ctx context.Context) (identity.Role, bool) {
	r, ok := ctx.Value(ctxKeyRole).(identity.Role)
	return r, ok
}
