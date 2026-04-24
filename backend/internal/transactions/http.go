package transactions

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler bundles the transactions HTTP endpoints.
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Mount installs the routes under a tenant-scoped subrouter rooted at
// /transactions.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{transactionId}", h.get)
	r.Patch("/{transactionId}", h.update)
	r.Delete("/{transactionId}", h.delete)
}

type createReq struct {
	AccountID       string  `json:"accountId"`
	Status          string  `json:"status"`
	BookedAt        string  `json:"bookedAt"`
	ValueAt         *string `json:"valueAt"`
	PostedAt        *string `json:"postedAt"`
	Amount          string  `json:"amount"`
	Currency        string  `json:"currency"`
	CategoryID      *string `json:"categoryId"`
	MerchantID      *string `json:"merchantId"`
	CounterpartyRaw *string `json:"counterpartyRaw"`
	Description     *string `json:"description"`
	Notes           *string `json:"notes"`
	CountAsExpense  *bool   `json:"countAsExpense"`
}

type patchReq struct {
	Status          *string         `json:"status"`
	BookedAt        *string         `json:"bookedAt"`
	ValueAt         *string         `json:"valueAt"`
	PostedAt        *string         `json:"postedAt"`
	Amount          *string         `json:"amount"`
	Currency        *string         `json:"currency"`
	CategoryID      *string         `json:"categoryId"`
	MerchantID      *string         `json:"merchantId"`
	CounterpartyRaw *string         `json:"counterpartyRaw"`
	Description     *string         `json:"description"`
	Notes           *string         `json:"notes"`
	CountAsExpense  json.RawMessage `json:"countAsExpense"`
}

func requireTenant(_ http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return auth.MustTenant(r).ID, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	f := ListFilter{}

	if raw := q.Get("accountId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		f.AccountID = &id
	}
	if raw := q.Get("from"); raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "from must be YYYY-MM-DD")
			return
		}
		f.From = &t
	}
	if raw := q.Get("to"); raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "to must be YYYY-MM-DD")
			return
		}
		f.To = &t
	}
	if raw := q.Get("status"); raw != "" {
		s := strings.ToLower(strings.TrimSpace(raw))
		f.Status = &s
	}
	if strings.EqualFold(q.Get("uncategorized"), "true") {
		f.Uncategorized = true
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "limit must be a positive integer")
			return
		}
		f.Limit = n
	}

	res, err := h.svc.List(r.Context(), tenantID, f)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	in := CreateInput{
		Status:          req.Status,
		Currency:        req.Currency,
		CounterpartyRaw: req.CounterpartyRaw,
		Description:     req.Description,
		Notes:           req.Notes,
		CountAsExpense:  req.CountAsExpense,
	}

	if req.AccountID == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId is required")
		return
	}
	accID, err := uuid.Parse(req.AccountID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
		return
	}
	in.AccountID = accID

	if req.BookedAt == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "bookedAt is required")
		return
	}
	bookedAt, err := time.Parse("2006-01-02", req.BookedAt)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "bookedAt must be YYYY-MM-DD")
		return
	}
	in.BookedAt = bookedAt

	if req.ValueAt != nil && *req.ValueAt != "" {
		t, err := time.Parse("2006-01-02", *req.ValueAt)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "valueAt must be YYYY-MM-DD")
			return
		}
		in.ValueAt = &t
	}
	if req.PostedAt != nil && *req.PostedAt != "" {
		t, err := time.Parse(time.RFC3339, *req.PostedAt)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "postedAt must be RFC3339 timestamp")
			return
		}
		in.PostedAt = &t
	}

	if req.Amount == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "amount is required")
		return
	}
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "amount must be a decimal string")
		return
	}
	in.Amount = amt

	if req.CategoryID != nil && *req.CategoryID != "" {
		id, err := uuid.Parse(*req.CategoryID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "categoryId must be a UUID")
			return
		}
		in.CategoryID = &id
	}
	if req.MerchantID != nil && *req.MerchantID != "" {
		id, err := uuid.Parse(*req.MerchantID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "merchantId must be a UUID")
			return
		}
		in.MerchantID = &id
	}

	res, err := h.svc.Create(r.Context(), tenantID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "transactionId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "transactionId must be a UUID")
		return
	}
	res, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "transactionId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "transactionId must be a UUID")
		return
	}
	var req patchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := PatchInput{
		Status:          req.Status,
		BookedAt:        req.BookedAt,
		ValueAt:         req.ValueAt,
		PostedAt:        req.PostedAt,
		Amount:          req.Amount,
		Currency:        req.Currency,
		CategoryID:      req.CategoryID,
		MerchantID:      req.MerchantID,
		CounterpartyRaw: req.CounterpartyRaw,
		Description:     req.Description,
		Notes:           req.Notes,
		CountAsExpense:  req.CountAsExpense,
	}
	res, err := h.svc.Update(r.Context(), tenantID, id, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := requireTenant(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "transactionId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "transactionId must be a UUID")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
