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
	r.Put("/order", h.reorder)
	r.Route("/groups", func(r chi.Router) {
		r.Get("/", h.listGroups)
		r.Post("/", h.createGroup)
		r.Patch("/{groupId}", h.updateGroup)
		r.Delete("/{groupId}", h.deleteGroup)
	})
	r.Get("/{accountId}", h.get)
	r.Patch("/{accountId}", h.update)
	r.Delete("/{accountId}", h.delete)
}

type groupReq struct {
	Name string `json:"name"`
}

type groupPatchReq struct {
	Name      *string `json:"name"`
	SortOrder *int    `json:"sortOrder"`
	Archived  *bool   `json:"archived"`
}

type createReq struct {
	Name                 string  `json:"name"`
	Nickname             *string `json:"nickname"`
	Kind                 string  `json:"kind"`
	Currency             string  `json:"currency"`
	Institution          *string `json:"institution"`
	AccountGroupID       *string `json:"accountGroupId"`
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
	AccountGroupID       *string `json:"accountGroupId"`
	AccountSortOrder     *int    `json:"accountSortOrder"`
	IncludeInNetworth    *bool   `json:"includeInNetworth"`
	IncludeInSavingsRate *bool   `json:"includeInSavingsRate"`
	CloseDate            *string `json:"closeDate"`
	Archived             *bool   `json:"archived"`
}

type reorderReq struct {
	Groups []struct {
		ID        string `json:"id"`
		SortOrder int    `json:"sortOrder"`
	} `json:"groups"`
	Accounts []struct {
		ID             string  `json:"id"`
		AccountGroupID *string `json:"accountGroupId"`
		SortOrder      int     `json:"sortOrder"`
	} `json:"accounts"`
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	includeArchived := strings.EqualFold(r.URL.Query().Get("includeArchived"), "true")
	res, err := h.svc.ListGroups(r.Context(), tenantID, includeArchived)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	var req groupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	group, err := h.svc.CreateGroup(r.Context(), tenantID, CreateGroupInput{Name: req.Name})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, group)
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "groupId must be a UUID")
		return
	}
	var req groupPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	group, err := h.svc.UpdateGroup(r.Context(), tenantID, id, PatchGroupInput{
		Name: req.Name, SortOrder: req.SortOrder, Archived: req.Archived,
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, group)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "groupId must be a UUID")
		return
	}
	if err := h.svc.DeleteGroup(r.Context(), tenantID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if req.AccountGroupID != nil && *req.AccountGroupID != "" {
		groupID, err := uuid.Parse(*req.AccountGroupID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountGroupId must be a UUID")
			return
		}
		in.AccountGroupID = &groupID
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
		AccountSortOrder:     req.AccountSortOrder,
		IncludeInNetworth:    req.IncludeInNetworth,
		IncludeInSavingsRate: req.IncludeInSavingsRate,
		CloseDate:            req.CloseDate,
		Archived:             req.Archived,
	}
	if req.AccountGroupID != nil {
		var groupID *uuid.UUID
		if *req.AccountGroupID != "" {
			parsed, err := uuid.Parse(*req.AccountGroupID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountGroupId must be a UUID")
				return
			}
			groupID = &parsed
		}
		in.AccountGroupID = &groupID
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

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.MustTenant(r).ID
	var req reorderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	in := ReorderInput{
		Groups:   make([]GroupOrderInput, 0, len(req.Groups)),
		Accounts: make([]AccountOrderInput, 0, len(req.Accounts)),
	}
	for _, group := range req.Groups {
		id, err := uuid.Parse(group.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "group id must be a UUID")
			return
		}
		in.Groups = append(in.Groups, GroupOrderInput{ID: id, SortOrder: group.SortOrder})
	}
	for _, account := range req.Accounts {
		id, err := uuid.Parse(account.ID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "account id must be a UUID")
			return
		}
		var groupID *uuid.UUID
		if account.AccountGroupID != nil && *account.AccountGroupID != "" {
			parsed, err := uuid.Parse(*account.AccountGroupID)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountGroupId must be a UUID")
				return
			}
			groupID = &parsed
		}
		in.Accounts = append(in.Accounts, AccountOrderInput{
			ID: id, AccountGroupID: groupID, SortOrder: account.SortOrder,
		})
	}
	if err := h.svc.Reorder(r.Context(), tenantID, in); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
