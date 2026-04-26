package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

		var workspace identity.Workspace
		var role identity.Role
		err = s.pool.QueryRow(r.Context(), `
			select t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
			       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
			from workspaces t
			join workspace_memberships m on m.workspace_id = t.id
			where t.id = $1 and m.user_id = $2 and t.deleted_at is null
		`, tid, user.ID).Scan(&workspace.ID, &workspace.Name, &workspace.Slug, &workspace.BaseCurrency,
			&workspace.CycleAnchorDay, &workspace.Locale, &workspace.Timezone, &workspace.DeletedAt,
			&workspace.CreatedAt, &role)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}

		ctx := WithWorkspace(r.Context(), workspace)
		ctx = WithRole(ctx, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

		var workspace identity.Workspace
		var role identity.Role
		err = s.pool.QueryRow(r.Context(), `
			select t.id, t.name, t.slug, t.base_currency, t.cycle_anchor_day,
			       t.locale, t.timezone, t.deleted_at, t.created_at, m.role
			from workspaces t
			join workspace_memberships m on m.workspace_id = t.id
			where t.id = $1 and m.user_id = $2 and m.role = 'owner'
		`, tid, user.ID).Scan(&workspace.ID, &workspace.Name, &workspace.Slug, &workspace.BaseCurrency,
			&workspace.CycleAnchorDay, &workspace.Locale, &workspace.Timezone, &workspace.DeletedAt,
			&workspace.CreatedAt, &role)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "lookup failed")
			return
		}

		ctx := WithWorkspace(r.Context(), workspace)
		ctx = WithRole(ctx, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
