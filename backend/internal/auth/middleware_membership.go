package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// RequireMembership extracts `{workspaceId}` from the URL, verifies membership,
// attaches Workspace + Role to context. 404 on miss (spec §4.5).
func (s *Service) RequireMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		raw := chi.URLParam(r, "workspaceId")
		tid, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}

		row, err := dbq.New(s.pool).GetWorkspaceWithMembership(r.Context(), dbq.GetWorkspaceWithMembershipParams{
			ID: tid, UserID: user.ID,
		})
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}

		workspace := identity.Workspace{
			ID: row.ID, Name: row.Name, Slug: row.Slug,
			BaseCurrency: asString(row.BaseCurrency), CycleAnchorDay: int(row.CycleAnchorDay),
			Locale: row.Locale, Timezone: row.Timezone,
			DeletedAt: row.DeletedAt, CreatedAt: row.CreatedAt,
		}
		role := identity.Role(row.Role)

		ctx := WithWorkspace(r.Context(), workspace)
		ctx = WithRole(ctx, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// asString converts an interface{} (from sqlc domain types) to string.
func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// RequireWorkspaceOwnerIncludingDeleted verifies that the authenticated user is
// an owner of `{workspaceId}` and attaches the workspace even when it is soft-deleted.
// This is intentionally narrower than RequireMembership: it exists for
// restore, where the active-workspace middleware would hide the row.
func (s *Service) RequireWorkspaceOwnerIncludingDeleted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		raw := chi.URLParam(r, "workspaceId")
		tid, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}

		row, err := dbq.New(s.pool).GetWorkspaceWithOwnership(r.Context(), dbq.GetWorkspaceWithOwnershipParams{
			ID: tid, UserID: user.ID,
		})
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}

		workspace := identity.Workspace{
			ID: row.ID, Name: row.Name, Slug: row.Slug,
			BaseCurrency: asString(row.BaseCurrency), CycleAnchorDay: int(row.CycleAnchorDay),
			Locale: row.Locale, Timezone: row.Timezone,
			DeletedAt: row.DeletedAt, CreatedAt: row.CreatedAt,
		}
		role := identity.Role(row.Role)

		ctx := WithWorkspace(r.Context(), workspace)
		ctx = WithRole(ctx, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
