package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// MountWorkspaceAdmin mounts owner-gated active-workspace admin routes under
// `/t/{workspaceId}`: PATCH / and DELETE /. Caller wires RequireSession +
// RequireMembership upstream. Restore is mounted separately because it must
// be able to load soft-deleted workspaces.
func (h *Handler) MountWorkspaceAdmin(r chi.Router) {
	owner := RequireRole(identity.RoleOwner)
	fresh := RequireFreshReauth(h.svc.cfg.ReauthWindow)
	r.With(owner, fresh).Patch("/", h.patchWorkspace)
	r.With(owner, fresh).Delete("/", h.softDeleteWorkspace)

	// Role change: owner-only. Remove/leave dispatches inside the handler
	// because the "any member can self-leave, only owners can remove
	// others" rule is context-dependent on actor vs. target.
	r.With(owner, fresh).Patch("/members/{userId}", h.changeMemberRole)
	r.Delete("/members/{userId}", h.removeOrLeaveMember)
}

type patchWorkspaceReq struct {
	Name           *string `json:"name,omitempty"`
	Slug           *string `json:"slug,omitempty"`
	BaseCurrency   *string `json:"baseCurrency,omitempty"`
	CycleAnchorDay *int    `json:"cycleAnchorDay,omitempty"`
	Locale         *string `json:"locale,omitempty"`
	Timezone       *string `json:"timezone,omitempty"`
}

func (h *Handler) patchWorkspace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	workspace := MustWorkspace(r)
	user := MustUser(r)

	var body patchWorkspaceReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}

	in := identity.UpdateWorkspaceInput{
		Name:           body.Name,
		Slug:           body.Slug,
		BaseCurrency:   body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay,
		Locale:         body.Locale,
		Timezone:       body.Timezone,
	}

	before, _ := h.svc.identity.GetWorkspace(r.Context(), workspace.ID)
	updated, err := h.svc.identity.UpdateWorkspace(r.Context(), workspace.ID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), workspace.ID, user.ID,
		"workspace.settings_changed", "workspace", workspace.ID, before, updated)

	httpx.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) softDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	workspace := MustWorkspace(r)
	user := MustUser(r)

	if err := h.svc.identity.SoftDeleteWorkspace(r.Context(), workspace.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), workspace.ID, user.ID,
		"workspace.deleted", "workspace", workspace.ID, nil, map[string]any{"softDelete": true})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RestoreWorkspace(w http.ResponseWriter, r *http.Request) {
	workspace := MustWorkspace(r)
	user := MustUser(r)

	if err := h.svc.identity.RestoreWorkspace(r.Context(), workspace.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), workspace.ID, user.ID,
		"workspace.restored", "workspace", workspace.ID, nil, nil)

	restored, err := h.svc.identity.GetWorkspace(r.Context(), workspace.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, restored)
}

type patchMemberReq struct {
	Role string `json:"role"`
}

// changeMemberRole is owner-gated (by the mount chain). Surfaces
// ErrLastOwner as 422 so the UI can show a stable code.
func (h *Handler) changeMemberRole(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	workspace := MustWorkspace(r)
	actor := MustUser(r)
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "userId must be a UUID")
		return
	}

	var body patchMemberReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	role := identity.Role(body.Role)
	if !role.Valid() {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_role", "role must be 'owner' or 'member'")
		return
	}

	err = h.svc.identity.ChangeRole(r.Context(), workspace.ID, userID, role)
	switch {
	case errors.Is(err, identity.ErrLastOwner):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
			"cannot demote the last owner of this workspace")
		return
	case errors.Is(err, identity.ErrNotAMember):
		httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), workspace.ID, actor.ID,
		"member.role_changed", "membership", userID, nil,
		map[string]any{"role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeOrLeaveMember dispatches on (userID == actor.ID):
//   - self-leave: any member may call (subject to last-owner / last-workspace guards).
//   - remove-other: owner only.
func (h *Handler) removeOrLeaveMember(w http.ResponseWriter, r *http.Request) {
	workspace := MustWorkspace(r)
	actor := MustUser(r)
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "userId must be a UUID")
		return
	}

	var action string
	if userID == actor.ID {
		err = h.svc.identity.LeaveWorkspace(r.Context(), workspace.ID, userID)
		action = "member.left"
	} else {
		role, _ := RoleFromCtx(r.Context())
		if role != identity.RoleOwner {
			httpx.WriteError(w, http.StatusForbidden, "forbidden",
				"only owners can remove other members")
			return
		}
		if !hasFreshReauth(r, h.svc.cfg.ReauthWindow) {
			httpx.WriteError(w, http.StatusForbidden, "reauth_required", "re-authentication required")
			return
		}
		err = h.svc.identity.RemoveMember(r.Context(), workspace.ID, userID)
		action = "member.removed"
	}
	switch {
	case errors.Is(err, identity.ErrLastOwner):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
			"cannot remove the last owner of this workspace")
		return
	case errors.Is(err, identity.ErrLastWorkspace):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_workspace",
			"cannot leave your last workspace — create another workspace first")
		return
	case errors.Is(err, identity.ErrNotAMember):
		httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), workspace.ID, actor.ID, action,
		"membership", userID, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

func hasFreshReauth(r *http.Request, window time.Duration) bool {
	sess, ok := SessionFromCtx(r.Context())
	return ok && sess.ReauthAt != nil && time.Since(*sess.ReauthAt) < window
}
