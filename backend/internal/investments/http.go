package investments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/providers/ibkr"
	"github.com/xmedavid/folio/backend/internal/providers/revolut"
)

// Handler bundles the investments HTTP endpoints. Routes are mounted under
// /api/v1/t/{workspaceId}/investments.
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Mount installs investment routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/dashboard", h.dashboard)
	r.Get("/positions", h.listPositions)
	r.Post("/refresh", h.refresh)

	r.Get("/instruments", h.listInstruments)
	r.Post("/instruments", h.upsertInstrument)
	r.Get("/instruments/{instrumentId}", h.getInstrumentDetail)

	r.Get("/trades", h.listTrades)
	r.Post("/trades", h.createTrade)
	r.Delete("/trades/{tradeId}", h.deleteTrade)

	r.Get("/dividends", h.listDividends)
	r.Post("/dividends", h.createDividend)
	r.Delete("/dividends/{dividendId}", h.deleteDividend)

	r.Post("/corporate-actions", h.createCorporateAction)
	r.Get("/corporate-actions", h.listCorporateActions)
	r.Delete("/corporate-actions/{actionId}", h.deleteCorporateAction)

	// Investment-format imports route through the unified smart-import
	// endpoint at POST /accounts/import-preview. There is no dedicated
	// per-format endpoint anymore.
	_ = h.importUpload
}

// ---------------------------------------------------------------------------
// Corporate actions (manual entry — splits, delistings, etc.)
// ---------------------------------------------------------------------------

type corporateActionReq struct {
	AccountID     *string `json:"accountId"`
	InstrumentID  string  `json:"instrumentId"`
	Symbol        string  `json:"symbol"`
	Kind          string  `json:"kind"`
	EffectiveDate string  `json:"effectiveDate"`
	Factor        *string `json:"factor"`
	Amount        *string `json:"amount"`
	NewSymbol     *string `json:"newSymbol"`
}

func (h *Handler) createCorporateAction(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	var req corporateActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := CorporateActionInput{Kind: req.Kind}
	if req.AccountID != nil && *req.AccountID != "" {
		id, err := uuid.Parse(*req.AccountID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		in.AccountID = &id
	}
	if req.InstrumentID != "" {
		id, err := uuid.Parse(req.InstrumentID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
			return
		}
		in.InstrumentID = id
	} else if strings.TrimSpace(req.Symbol) != "" {
		inst, err := h.svc.GetInstrumentBySymbol(r.Context(), req.Symbol)
		if err != nil {
			httpx.WriteServiceError(w, err)
			return
		}
		in.InstrumentID = inst.ID
	} else {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId or symbol is required")
		return
	}
	if req.EffectiveDate == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "effectiveDate is required")
		return
	}
	d, err := time.Parse("2006-01-02", req.EffectiveDate)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "effectiveDate must be YYYY-MM-DD")
		return
	}
	in.EffectiveDate = d
	if req.Factor != nil && *req.Factor != "" {
		f, err := decimal.NewFromString(strings.TrimSpace(*req.Factor))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "factor must be a decimal string")
			return
		}
		in.Factor = f
	}
	if req.Amount != nil && *req.Amount != "" {
		a, err := decimal.NewFromString(strings.TrimSpace(*req.Amount))
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "amount must be a decimal string")
			return
		}
		in.Amount = a
	}
	if req.NewSymbol != nil {
		in.NewSymbol = *req.NewSymbol
	}
	res, err := h.svc.CreateCorporateAction(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) listCorporateActions(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	raw := r.URL.Query().Get("instrumentId")
	if raw == "" {
		// Without an instrument scope, return an empty list — the table is
		// global and could be huge; clients drive lookups from the
		// instrument-detail page.
		httpx.WriteJSON(w, http.StatusOK, []CorporateAction{})
		return
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
		return
	}
	res, err := h.svc.ListCorporateActions(r.Context(), workspaceID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteCorporateAction(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "actionId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "actionId must be a UUID")
		return
	}
	if err := h.svc.DeleteCorporateAction(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Imports
// ---------------------------------------------------------------------------

// importUpload accepts a multipart/form-data file upload at
// /imports/{format}/{accountId}. format is "ibkr" or "revolut_trading"; the
// parser routes by format and yields []ImportEvent which the service ingests.
// Max upload size is capped at 8 MiB.
func (h *Handler) importUpload(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	format := chi.URLParam(r, "format")
	accountID, err := uuid.Parse(chi.URLParam(r, "accountId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountId must be a UUID")
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "expected multipart/form-data with file=...")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "missing file field")
		return
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "failed to read upload")
		return
	}

	var events []ImportEvent
	switch format {
	case "ibkr":
		res, err := ibkr.Parse(body)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "parse_error", "ibkr: "+err.Error())
			return
		}
		events = res.Events
	case "revolut_trading":
		res, err := revolut.ParseTradingCSV(body)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "parse_error", "revolut: "+err.Error())
			return
		}
		events = res.Events
	default:
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "format must be ibkr or revolut_trading")
		return
	}

	summary, err := h.svc.IngestImport(r.Context(), workspaceID, accountID, events)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, summary)
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	q := r.URL.Query()
	f := DashboardFilter{
		ReportCurrency: q.Get("currency"),
	}
	if raw := q.Get("accountId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		f.AccountID = &id
	}
	res, err := h.svc.BuildDashboardSummary(r.Context(), workspaceID, f)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------------------
// Positions
// ---------------------------------------------------------------------------

func (h *Handler) listPositions(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	q := r.URL.Query()
	f := PositionFilter{Search: q.Get("search")}
	if raw := q.Get("accountId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		f.AccountID = &id
	}
	switch q.Get("status") {
	case "open":
		f.OpenOnly = true
	case "closed":
		f.ClosedOnly = true
	}
	res, err := h.svc.ListPositions(r.Context(), workspaceID, f)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	count, err := h.svc.RefreshAllPositions(r.Context(), workspaceID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	// Force-refresh prices for every open position regardless of cache age
	// so the user gets a definitively up-to-date view after clicking Refresh.
	priced, _ := h.svc.PrefetchPrices(r.Context(), workspaceID, 0)
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"refreshed": count, "priced": priced})
}

// ---------------------------------------------------------------------------
// Instruments
// ---------------------------------------------------------------------------

type instrumentReq struct {
	Symbol     string  `json:"symbol"`
	ISIN       *string `json:"isin"`
	Name       string  `json:"name"`
	AssetClass string  `json:"assetClass"`
	Currency   string  `json:"currency"`
	Exchange   *string `json:"exchange"`
}

func (h *Handler) upsertInstrument(w http.ResponseWriter, r *http.Request) {
	_ = auth.MustWorkspace(r) // gate by membership
	var req instrumentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := InstrumentInput{
		Symbol:     req.Symbol,
		ISIN:       req.ISIN,
		Name:       req.Name,
		AssetClass: req.AssetClass,
		Currency:   req.Currency,
		Exchange:   req.Exchange,
	}
	res, err := h.svc.UpsertInstrument(r.Context(), in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) listInstruments(w http.ResponseWriter, r *http.Request) {
	_ = auth.MustWorkspace(r)
	q := r.URL.Query().Get("q")
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	res, err := h.svc.SearchInstruments(r.Context(), q, limit)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) getInstrumentDetail(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	idStr := chi.URLParam(r, "instrumentId")
	// Accept either a UUID (instrument id) or a symbol.
	var instID uuid.UUID
	if parsed, err := uuid.Parse(idStr); err == nil {
		instID = parsed
	} else {
		inst, err := h.svc.GetInstrumentBySymbol(r.Context(), idStr)
		if err != nil {
			httpx.WriteServiceError(w, err)
			return
		}
		instID = inst.ID
	}
	res, err := h.svc.GetInstrumentDetail(r.Context(), workspaceID, instID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------------------
// Trades
// ---------------------------------------------------------------------------

type tradeReq struct {
	AccountID    string  `json:"accountId"`
	InstrumentID string  `json:"instrumentId"`
	Symbol       string  `json:"symbol"`
	Side         string  `json:"side"`
	Quantity     string  `json:"quantity"`
	Price        string  `json:"price"`
	Currency     string  `json:"currency"`
	FeeAmount    *string `json:"feeAmount"`
	TradeDate    string  `json:"tradeDate"`
	SettleDate   *string `json:"settleDate"`
	// Optional: when symbol is provided and instrumentId is empty, auto-create.
	Name       string  `json:"name"`
	AssetClass string  `json:"assetClass"`
	ISIN       *string `json:"isin"`
	Exchange   *string `json:"exchange"`
}

func (h *Handler) createTrade(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	var req tradeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := TradeInput{
		Side:     req.Side,
		Currency: req.Currency,
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

	// Resolve instrument: either by id or by symbol (auto-upsert).
	if req.InstrumentID != "" {
		instID, err := uuid.Parse(req.InstrumentID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
			return
		}
		in.InstrumentID = instID
	} else if strings.TrimSpace(req.Symbol) != "" {
		inst, err := h.svc.UpsertInstrument(r.Context(), InstrumentInput{
			Symbol:     req.Symbol,
			Name:       req.Name,
			ISIN:       req.ISIN,
			AssetClass: req.AssetClass,
			Currency:   req.Currency,
			Exchange:   req.Exchange,
		})
		if err != nil {
			httpx.WriteServiceError(w, err)
			return
		}
		in.InstrumentID = inst.ID
	} else {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId or symbol is required")
		return
	}

	if req.TradeDate == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "tradeDate is required")
		return
	}
	td, err := time.Parse("2006-01-02", req.TradeDate)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "tradeDate must be YYYY-MM-DD")
		return
	}
	in.TradeDate = td
	if req.SettleDate != nil && *req.SettleDate != "" {
		sd, err := time.Parse("2006-01-02", *req.SettleDate)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "settleDate must be YYYY-MM-DD")
			return
		}
		in.SettleDate = &sd
	}

	q, err := decimal.NewFromString(strings.TrimSpace(req.Quantity))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "quantity must be a decimal string")
		return
	}
	in.Quantity = q

	p, err := decimal.NewFromString(strings.TrimSpace(req.Price))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "price must be a decimal string")
		return
	}
	in.Price = p

	if req.FeeAmount != nil && *req.FeeAmount != "" {
		fee, err := decimal.NewFromString(*req.FeeAmount)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "feeAmount must be a decimal string")
			return
		}
		in.FeeAmount = fee
	}

	res, err := h.svc.CreateTrade(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) deleteTrade(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "tradeId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "tradeId must be a UUID")
		return
	}
	if err := h.svc.DeleteTrade(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listTrades(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	q := r.URL.Query()
	var accountID, instrumentID *uuid.UUID
	if raw := q.Get("accountId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		accountID = &id
	}
	if raw := q.Get("instrumentId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
			return
		}
		instrumentID = &id
	}
	res, err := h.svc.ListTrades(r.Context(), workspaceID, accountID, instrumentID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------------------
// Dividends
// ---------------------------------------------------------------------------

type dividendReq struct {
	AccountID     string  `json:"accountId"`
	InstrumentID  string  `json:"instrumentId"`
	Symbol        string  `json:"symbol"`
	ExDate        string  `json:"exDate"`
	PayDate       string  `json:"payDate"`
	AmountPerUnit string  `json:"amountPerUnit"`
	Currency      string  `json:"currency"`
	TotalAmount   string  `json:"totalAmount"`
	TaxWithheld   *string `json:"taxWithheld"`
}

func (h *Handler) createDividend(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	var req dividendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := DividendInput{Currency: req.Currency}
	accID, err := uuid.Parse(req.AccountID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
		return
	}
	in.AccountID = accID
	if req.InstrumentID != "" {
		id, err := uuid.Parse(req.InstrumentID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
			return
		}
		in.InstrumentID = id
	} else {
		inst, err := h.svc.GetInstrumentBySymbol(context.Background(), req.Symbol)
		if err != nil {
			httpx.WriteServiceError(w, err)
			return
		}
		in.InstrumentID = inst.ID
	}
	if req.PayDate == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "payDate is required")
		return
	}
	pd, err := time.Parse("2006-01-02", req.PayDate)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "payDate must be YYYY-MM-DD")
		return
	}
	in.PayDate = pd
	if req.ExDate != "" {
		ed, err := time.Parse("2006-01-02", req.ExDate)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "exDate must be YYYY-MM-DD")
			return
		}
		in.ExDate = ed
	}
	apu, err := decimal.NewFromString(strings.TrimSpace(req.AmountPerUnit))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "amountPerUnit must be a decimal string")
		return
	}
	in.AmountPerUnit = apu
	tot, err := decimal.NewFromString(strings.TrimSpace(req.TotalAmount))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "totalAmount must be a decimal string")
		return
	}
	in.TotalAmount = tot
	if req.TaxWithheld != nil && *req.TaxWithheld != "" {
		tw, err := decimal.NewFromString(*req.TaxWithheld)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "taxWithheld must be a decimal string")
			return
		}
		in.TaxWithheld = tw
	}
	res, err := h.svc.CreateDividend(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) deleteDividend(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "dividendId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "dividendId must be a UUID")
		return
	}
	if err := h.svc.DeleteDividend(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDividends(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	q := r.URL.Query()
	var accountID, instrumentID *uuid.UUID
	if raw := q.Get("accountId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "accountId must be a UUID")
			return
		}
		accountID = &id
	}
	if raw := q.Get("instrumentId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "instrumentId must be a UUID")
			return
		}
		instrumentID = &id
	}
	res, err := h.svc.ListDividends(r.Context(), workspaceID, accountID, instrumentID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
