package accounts

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler bundles the accounts HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Mount installs the routes under a tenant-scoped subrouter.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{accountId}", h.get)
	r.Patch("/{accountId}", h.update)
	r.Delete("/{accountId}", h.delete)
}

type createReq struct {
	Name                 string  `json:"name"`
	Nickname             *string `json:"nickname"`
	Kind                 string  `json:"kind"`
	Currency             string  `json:"currency"`
	Institution          *string `json:"institution"`
	OpenDate             string  `json:"openDate"`
	OpeningBalance       *string `json:"openingBalance"`
	OpeningBalanceDate   *string `json:"openingBalanceDate"`
	IncludeInNetworth    *bool   `json:"includeInNetworth"`
	IncludeInSavingsRate *bool   `json:"includeInSavingsRate"`
}

type patchReq struct {
	Name                 *string `json:"name"`
	Nickname             *string `json:"nickname"`
	Kind                 *string `json:"kind"`
	Institution          *string `json:"institution"`
	IncludeInNetworth    *bool   `json:"includeInNetworth"`
	IncludeInSavingsRate *bool   `json:"includeInSavingsRate"`
	CloseDate            *string `json:"closeDate"`
	Archived             *bool   `json:"archived"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	includeArchived := strings.EqualFold(r.URL.Query().Get("includeArchived"), "true")
	res, err := h.svc.List(r.Context(), tenantID, includeArchived)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	openDate, err := time.Parse("2006-01-02", req.OpenDate)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "openDate must be YYYY-MM-DD")
		return
	}

	in := CreateInput{
		Name:                 req.Name,
		Nickname:             req.Nickname,
		Kind:                 req.Kind,
		Currency:             req.Currency,
		Institution:          req.Institution,
		OpenDate:             openDate,
		IncludeInNetworth:    req.IncludeInNetworth,
		IncludeInSavingsRate: req.IncludeInSavingsRate,
	}

	if req.OpeningBalance != nil {
		d, err := decimal.NewFromString(*req.OpeningBalance)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "openingBalance must be a decimal string")
			return
		}
		in.OpeningBalance = d
	}
	if req.OpeningBalanceDate != nil && *req.OpeningBalanceDate != "" {
		t, err := time.Parse("2006-01-02", *req.OpeningBalanceDate)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "openingBalanceDate must be YYYY-MM-DD")
			return
		}
		in.OpeningBalanceDate = &t
	}

	acc, err := h.svc.Create(r.Context(), tenantID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, acc)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "accountId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountId must be a UUID")
		return
	}
	acc, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, acc)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "accountId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountId must be a UUID")
		return
	}
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := PatchInput{
		Name:                 req.Name,
		Nickname:             req.Nickname,
		Kind:                 req.Kind,
		Institution:          req.Institution,
		IncludeInNetworth:    req.IncludeInNetworth,
		IncludeInSavingsRate: req.IncludeInSavingsRate,
		CloseDate:            req.CloseDate,
		Archived:             req.Archived,
	}
	acc, err := h.svc.Update(r.Context(), tenantID, id, in)
	if err != nil {
		var verr *httpx.ValidationError
		if errors.As(err, &verr) {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", verr.Error())
			return
		}
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, acc)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "accountId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountId must be a UUID")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
