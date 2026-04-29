package classification

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler bundles the classification HTTP endpoints (categories, merchants,
// tags, and transaction-tag operations).
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountCategories installs /api/v1/categories routes.
func (h *Handler) MountCategories(r chi.Router) {
	r.Get("/", h.listCategories)
	r.Post("/", h.createCategory)
	r.Get("/{categoryId}", h.getCategory)
	r.Patch("/{categoryId}", h.updateCategory)
	r.Delete("/{categoryId}", h.deleteCategory)
}

// MountMerchants installs /api/v1/merchants routes.
func (h *Handler) MountMerchants(r chi.Router) {
	r.Get("/", h.listMerchants)
	r.Post("/", h.createMerchant)
	r.Get("/{merchantId}", h.getMerchant)
	r.Patch("/{merchantId}", h.updateMerchant)
	r.Delete("/{merchantId}", h.deleteMerchant)

	// merchant aliases sub-resource
	r.Get("/{merchantId}/aliases", h.listMerchantAliases)
	r.Post("/{merchantId}/aliases", h.addMerchantAlias)
	r.Delete("/{merchantId}/aliases/{aliasId}", h.removeMerchantAlias)

	// merge: preview + commit
	r.Post("/{merchantId}/merge/preview", h.previewMergeMerchant)
	r.Post("/{merchantId}/merge", h.mergeMerchant)
}

// MountTags installs /api/v1/tags routes.
func (h *Handler) MountTags(r chi.Router) {
	r.Get("/", h.listTags)
	r.Post("/", h.createTag)
	r.Get("/{tagId}", h.getTag)
	r.Patch("/{tagId}", h.updateTag)
	r.Delete("/{tagId}", h.deleteTag)
}

// MountTransactionTags installs /api/v1/transactions/{transactionId}/tags routes.
func (h *Handler) MountTransactionTags(r chi.Router) {
	r.Put("/{tagId}", h.applyTag)
	r.Delete("/{tagId}", h.removeTag)
}

// MountCategorizationRules installs /api/v1/categorization-rules routes.
func (h *Handler) MountCategorizationRules(r chi.Router) {
	r.Get("/", h.listRules)
	r.Post("/", h.createRule)
	r.Get("/{ruleId}", h.getRule)
	r.Patch("/{ruleId}", h.updateRule)
	r.Delete("/{ruleId}", h.deleteRule)
}

// ApplyRulesToTransactionHandler is the standalone
// POST /transactions/{transactionId}/apply-categorization-rules endpoint.
// It evaluates enabled rules in priority/created_at order, applies the
// first match, and stamps last_matched_at. Manual overrides win: category,
// merchant, and countAsExpense only write when those fields are currently
// null; tags are always added idempotently.
func (h *Handler) ApplyRulesToTransactionHandler(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	txID, ok := parseUUIDParam(w, r, "transactionId")
	if !ok {
		return
	}
	res, err := h.svc.ApplyRulesToTransaction(r.Context(), workspaceID, txID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---- shared helpers --------------------------------------------------------

func requireWorkspace(_ http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return auth.MustWorkspace(r).ID, true
}

func parseUUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", name+" must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func includeArchived(r *http.Request) bool {
	return strings.EqualFold(r.URL.Query().Get("includeArchived"), "true")
}

// ---- category request bodies ----------------------------------------------

type categoryCreateReq struct {
	ParentID  *string `json:"parentId"`
	Name      string  `json:"name"`
	Color     *string `json:"color"`
	SortOrder *int    `json:"sortOrder"`
}

type categoryPatchReq struct {
	ParentID  *string `json:"parentId"`
	Name      *string `json:"name"`
	Color     *string `json:"color"`
	SortOrder *int    `json:"sortOrder"`
	Archived  *bool   `json:"archived"`
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	res, err := h.svc.ListCategories(r.Context(), workspaceID, includeArchived(r))
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	var req categoryCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := CategoryCreateInput{
		Name:      req.Name,
		Color:     req.Color,
		SortOrder: req.SortOrder,
	}
	if req.ParentID != nil && *req.ParentID != "" {
		id, err := uuid.Parse(*req.ParentID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "parentId must be a UUID")
			return
		}
		in.ParentID = &id
	}
	res, err := h.svc.CreateCategory(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) getCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "categoryId")
	if !ok {
		return
	}
	res, err := h.svc.GetCategory(r.Context(), workspaceID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "categoryId")
	if !ok {
		return
	}
	var req categoryPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := CategoryPatchInput{
		ParentID:  req.ParentID,
		Name:      req.Name,
		Color:     req.Color,
		SortOrder: req.SortOrder,
		Archived:  req.Archived,
	}
	res, err := h.svc.UpdateCategory(r.Context(), workspaceID, id, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "categoryId")
	if !ok {
		return
	}
	if err := h.svc.ArchiveCategory(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- merchants -------------------------------------------------------------

type merchantCreateReq struct {
	CanonicalName     string  `json:"canonicalName"`
	LogoURL           *string `json:"logoUrl"`
	DefaultCategoryID *string `json:"defaultCategoryId"`
	Industry          *string `json:"industry"`
	Website           *string `json:"website"`
	Notes             *string `json:"notes"`
}

type merchantPatchReq struct {
	CanonicalName     *string `json:"canonicalName"`
	LogoURL           *string `json:"logoUrl"`
	DefaultCategoryID *string `json:"defaultCategoryId"`
	Industry          *string `json:"industry"`
	Website           *string `json:"website"`
	Notes             *string `json:"notes"`
	Archived          *bool   `json:"archived"`
	Cascade           *bool   `json:"cascade"`
}

func (h *Handler) listMerchants(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	res, err := h.svc.ListMerchants(r.Context(), workspaceID, includeArchived(r))
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	var req merchantCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := MerchantCreateInput{
		CanonicalName: req.CanonicalName,
		LogoURL:       req.LogoURL,
		Industry:      req.Industry,
		Website:       req.Website,
		Notes:         req.Notes,
	}
	if req.DefaultCategoryID != nil && *req.DefaultCategoryID != "" {
		id, err := uuid.Parse(*req.DefaultCategoryID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "defaultCategoryId must be a UUID")
			return
		}
		in.DefaultCategoryID = &id
	}
	res, err := h.svc.CreateMerchant(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) getMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	res, err := h.svc.GetMerchant(r.Context(), workspaceID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) updateMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	var req merchantPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := MerchantPatchInput{
		CanonicalName:     req.CanonicalName,
		LogoURL:           req.LogoURL,
		DefaultCategoryID: req.DefaultCategoryID,
		Industry:          req.Industry,
		Website:           req.Website,
		Notes:             req.Notes,
		Archived:          req.Archived,
		Cascade:           req.Cascade,
	}
	res, err := h.svc.UpdateMerchant(r.Context(), workspaceID, id, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	if err := h.svc.ArchiveMerchant(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- merchant aliases ------------------------------------------------------

type addAliasReq struct {
	RawPattern string `json:"rawPattern"`
}

func (h *Handler) listMerchantAliases(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	merchantID, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	res, err := h.svc.ListAliases(r.Context(), workspaceID, merchantID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) addMerchantAlias(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	merchantID, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	var req addAliasReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	res, err := h.svc.AddAlias(r.Context(), workspaceID, merchantID, req.RawPattern)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) removeMerchantAlias(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	merchantID, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	aliasID, ok := parseUUIDParam(w, r, "aliasId")
	if !ok {
		return
	}
	if err := h.svc.RemoveAlias(r.Context(), workspaceID, merchantID, aliasID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- merchant merge --------------------------------------------------------

type mergePreviewReq struct {
	TargetID string `json:"targetId"`
}

type mergeReq struct {
	TargetID           string `json:"targetId"`
	ApplyTargetDefault bool   `json:"applyTargetDefault"`
}

func (h *Handler) previewMergeMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	sourceID, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	var req mergePreviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	targetID, err := uuid.Parse(req.TargetID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "targetId must be a UUID")
		return
	}
	res, err := h.svc.PreviewMerge(r.Context(), workspaceID, sourceID, targetID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) mergeMerchant(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	sourceID, ok := parseUUIDParam(w, r, "merchantId")
	if !ok {
		return
	}
	var req mergeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	targetID, err := uuid.Parse(req.TargetID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "targetId must be a UUID")
		return
	}
	res, err := h.svc.MergeMerchants(r.Context(), workspaceID, sourceID, MergeMerchantsInput{
		TargetID:           targetID,
		ApplyTargetDefault: req.ApplyTargetDefault,
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---- tags ------------------------------------------------------------------

type tagCreateReq struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

type tagPatchReq struct {
	Name     *string `json:"name"`
	Color    *string `json:"color"`
	Archived *bool   `json:"archived"`
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	res, err := h.svc.ListTags(r.Context(), workspaceID, includeArchived(r))
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) createTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	var req tagCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := TagCreateInput{Name: req.Name, Color: req.Color}
	res, err := h.svc.CreateTag(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) getTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "tagId")
	if !ok {
		return
	}
	res, err := h.svc.GetTag(r.Context(), workspaceID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) updateTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "tagId")
	if !ok {
		return
	}
	var req tagPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := TagPatchInput{Name: req.Name, Color: req.Color, Archived: req.Archived}
	res, err := h.svc.UpdateTag(r.Context(), workspaceID, id, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "tagId")
	if !ok {
		return
	}
	if err := h.svc.ArchiveTag(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- transaction tags ------------------------------------------------------

func (h *Handler) applyTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	txID, ok := parseUUIDParam(w, r, "transactionId")
	if !ok {
		return
	}
	tagID, ok := parseUUIDParam(w, r, "tagId")
	if !ok {
		return
	}
	if err := h.svc.AddTransactionTag(r.Context(), workspaceID, txID, tagID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeTag(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	txID, ok := parseUUIDParam(w, r, "transactionId")
	if !ok {
		return
	}
	tagID, ok := parseUUIDParam(w, r, "tagId")
	if !ok {
		return
	}
	if err := h.svc.RemoveTransactionTag(r.Context(), workspaceID, txID, tagID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- categorization rules --------------------------------------------------

type ruleCreateReq struct {
	Priority *int            `json:"priority"`
	Enabled  *bool           `json:"enabled"`
	When     json.RawMessage `json:"when"`
	Then     json.RawMessage `json:"then"`
}

type rulePatchReq struct {
	Priority *int            `json:"priority"`
	Enabled  *bool           `json:"enabled"`
	When     json.RawMessage `json:"when"`
	Then     json.RawMessage `json:"then"`
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	f := ClassificationRuleListFromQuery(r)
	res, err := h.svc.ListRules(r.Context(), workspaceID, f)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ClassificationRuleListFromQuery parses the optional ?enabled=true|false
// filter. Invalid values are ignored (treated as absent) to keep the list
// endpoint forgiving; strict validation happens on writes.
func ClassificationRuleListFromQuery(r *http.Request) RuleListFilter {
	var f RuleListFilter
	raw := r.URL.Query().Get("enabled")
	if raw == "" {
		return f
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		t := true
		f.Enabled = &t
	case "false":
		v := false
		f.Enabled = &v
	}
	return f
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	var req ruleCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := RuleCreateInput{
		Priority: req.Priority,
		Enabled:  req.Enabled,
		When:     req.When,
		Then:     req.Then,
	}
	res, err := h.svc.CreateRule(r.Context(), workspaceID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) getRule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "ruleId")
	if !ok {
		return
	}
	res, err := h.svc.GetRule(r.Context(), workspaceID, id)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "ruleId")
	if !ok {
		return
	}
	var req rulePatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	in := RulePatchInput{
		Priority: req.Priority,
		Enabled:  req.Enabled,
		When:     req.When,
		Then:     req.Then,
	}
	res, err := h.svc.UpdateRule(r.Context(), workspaceID, id, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := requireWorkspace(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "ruleId")
	if !ok {
		return
	}
	if err := h.svc.DeleteRule(r.Context(), workspaceID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
