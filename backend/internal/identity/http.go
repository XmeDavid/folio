package identity

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler mounts identity read endpoints scoped to a tenant.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountTenantScoped mounts under /t/{tenantId}/…
func (h *Handler) MountTenantScoped(r chi.Router) {
	r.Get("/members", h.listMembers)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	tenant := auth.MustTenant(r)
	members, err := h.svc.ListMembers(r.Context(), tenant.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

// ParseTenantID returns the {tenantId} URL param parsed as a UUID.
// Exposed for tests and other handlers.
func ParseTenantID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "tenantId"))
}
