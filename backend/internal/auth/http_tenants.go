package auth

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

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

// chiURLParam is kept as an aliased reference so wider refactors don't
// silently break the chi import in this file (handlers that use URL params
// are added in Tasks 10 and 11).
var _ = chi.URLParam
