package identity

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// onboardRequest is the JSON wire shape of POST /api/v1/onboarding/bootstrap.
type onboardRequest struct {
	TenantName     string `json:"tenantName"`
	BaseCurrency   string `json:"baseCurrency"`
	CycleAnchorDay int    `json:"cycleAnchorDay"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone"`
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
}

// Handler bundles the identity HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountPublic mounts routes that do not require a tenant in context.
func (h *Handler) MountPublic(r chi.Router) {
	r.Post("/onboarding/bootstrap", h.bootstrap)
}

// MountTenantScoped mounts routes that require a tenant id in context
// (injected by httpx.RequireTenant).
func (h *Handler) MountTenantScoped(r chi.Router) {
	r.Get("/me", h.me)
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var req onboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := OnboardInput{
		TenantName:     req.TenantName,
		BaseCurrency:   req.BaseCurrency,
		CycleAnchorDay: req.CycleAnchorDay,
		Locale:         req.Locale,
		Timezone:       req.Timezone,
		Email:          req.Email,
		DisplayName:    req.DisplayName,
	}
	res, err := h.svc.Bootstrap(r.Context(), in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := httpx.TenantIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "tenant_required", "missing tenant context")
		return
	}
	user, tenant, err := h.svc.Me(r.Context(), tenantID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":   user,
		"tenant": tenant,
	})
}
