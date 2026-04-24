package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// MountTenantAdmin mounts owner-gated tenant-admin routes under
// `/t/{tenantId}`: PATCH /, DELETE /, POST /restore. Caller wires
// RequireSession + RequireMembership upstream.
//
// RequireFreshReauth is deliberately NOT mounted here — Plan 4 adds the
// /auth/reauth endpoint and re-mounts the step-up middleware across these
// routes at that point (spec §5.6). RequireRole(RoleOwner) is the only
// authorisation gate in Plan 2.
func (h *Handler) MountTenantAdmin(r chi.Router) {
	owner := RequireRole(identity.RoleOwner)
	r.With(owner).Patch("/", h.patchTenant)
	r.With(owner).Delete("/", h.softDeleteTenant)
	r.With(owner).Post("/restore", h.restoreTenant)

	// Role change: owner-only. Remove/leave dispatches inside the handler
	// because the "any member can self-leave, only owners can remove
	// others" rule is context-dependent on actor vs. target.
	r.With(owner).Patch("/members/{userId}", h.changeMemberRole)
	r.Delete("/members/{userId}", h.removeOrLeaveMember)
}

type patchTenantReq struct {
	Name           *string `json:"name,omitempty"`
	Slug           *string `json:"slug,omitempty"`
	BaseCurrency   *string `json:"baseCurrency,omitempty"`
	CycleAnchorDay *int    `json:"cycleAnchorDay,omitempty"`
	Locale         *string `json:"locale,omitempty"`
	Timezone       *string `json:"timezone,omitempty"`
}

func (h *Handler) patchTenant(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	tenant := MustTenant(r)
	user := MustUser(r)

	var body patchTenantReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}

	in := identity.UpdateTenantInput{
		Name:           body.Name,
		Slug:           body.Slug,
		BaseCurrency:   body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay,
		Locale:         body.Locale,
		Timezone:       body.Timezone,
	}

	before, _ := h.svc.identity.GetTenant(r.Context(), tenant.ID)
	updated, err := h.svc.identity.UpdateTenant(r.Context(), tenant.ID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), tenant.ID, user.ID,
		"tenant.settings_changed", "tenant", tenant.ID, before, updated)

	httpx.WriteJSON(w, http.StatusOK, updated)
}

func (h *Handler) softDeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenant := MustTenant(r)
	user := MustUser(r)

	if err := h.svc.identity.SoftDeleteTenant(r.Context(), tenant.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), tenant.ID, user.ID,
		"tenant.deleted", "tenant", tenant.ID, nil, map[string]any{"softDelete": true})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) restoreTenant(w http.ResponseWriter, r *http.Request) {
	tenant := MustTenant(r)
	user := MustUser(r)

	if err := h.svc.identity.RestoreTenant(r.Context(), tenant.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), tenant.ID, user.ID,
		"tenant.restored", "tenant", tenant.ID, nil, nil)

	restored, err := h.svc.identity.GetTenant(r.Context(), tenant.ID)
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
	tenant := MustTenant(r)
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

	err = h.svc.identity.ChangeRole(r.Context(), tenant.ID, userID, role)
	switch {
	case errors.Is(err, identity.ErrLastOwner):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
			"cannot demote the last owner of this tenant")
		return
	case errors.Is(err, identity.ErrNotAMember):
		httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), tenant.ID, actor.ID,
		"member.role_changed", "membership", userID, nil,
		map[string]any{"role": string(role)})
	w.WriteHeader(http.StatusNoContent)
}

// removeOrLeaveMember dispatches on (userID == actor.ID):
//   - self-leave: any member may call (subject to last-owner / last-tenant guards).
//   - remove-other: owner only.
func (h *Handler) removeOrLeaveMember(w http.ResponseWriter, r *http.Request) {
	tenant := MustTenant(r)
	actor := MustUser(r)
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "userId must be a UUID")
		return
	}

	var action string
	if userID == actor.ID {
		err = h.svc.identity.LeaveTenant(r.Context(), tenant.ID, userID)
		action = "member.left"
	} else {
		role, _ := RoleFromCtx(r.Context())
		if role != identity.RoleOwner {
			httpx.WriteError(w, http.StatusForbidden, "forbidden",
				"only owners can remove other members")
			return
		}
		err = h.svc.identity.RemoveMember(r.Context(), tenant.ID, userID)
		action = "member.removed"
	}
	switch {
	case errors.Is(err, identity.ErrLastOwner):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_owner",
			"cannot remove the last owner of this tenant")
		return
	case errors.Is(err, identity.ErrLastTenant):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "last_tenant",
			"cannot leave your last tenant — create another workspace first")
		return
	case errors.Is(err, identity.ErrNotAMember):
		httpx.WriteError(w, http.StatusNotFound, "not_a_member", "membership not found")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.svc.WriteAudit(r.Context(), tenant.ID, actor.ID, action,
		"membership", userID, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
