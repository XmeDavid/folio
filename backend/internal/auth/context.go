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
	ctxKeyWorkspace ctxKey = "folio.auth.workspace"
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

// WithWorkspace / WorkspaceFromCtx attach the workspace under inspection.
func WithWorkspace(ctx context.Context, t identity.Workspace) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspace, t)
}

func WorkspaceFromCtx(ctx context.Context) (identity.Workspace, bool) {
	t, ok := ctx.Value(ctxKeyWorkspace).(identity.Workspace)
	return t, ok
}

func MustWorkspace(r *http.Request) identity.Workspace {
	t, ok := WorkspaceFromCtx(r.Context())
	if !ok {
		panic("MustWorkspace called without RequireMembership upstream")
	}
	return t
}

// WithRole / RoleFromCtx attach the caller's role in the current workspace.
func WithRole(ctx context.Context, r identity.Role) context.Context {
	return context.WithValue(ctx, ctxKeyRole, r)
}

func RoleFromCtx(ctx context.Context) (identity.Role, bool) {
	r, ok := ctx.Value(ctxKeyRole).(identity.Role)
	return r, ok
}
