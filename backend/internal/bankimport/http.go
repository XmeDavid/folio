package bankimport

import (
	"encoding/json"
	"mime/multipart"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) MountAccountRoutes(r chi.Router) {
	r.Post("/import-preview", h.previewForNewAccount)
	r.Post("/imports/apply-plan", h.applyPlan)
	r.Post("/imports/apply-multi", h.applyMulti)
	r.Post("/{accountId}/imports/preview", h.previewForExistingAccount)
	r.Post("/{accountId}/imports", h.applyToExistingAccount)
}

func (h *Handler) previewForNewAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	fileName, file, ok := readImportFile(w, r)
	if !ok {
		return
	}
	defer file.Close()
	res, err := h.svc.Preview(r.Context(), workspaceID, fileName, file, nil)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) applyPlan(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	userID := auth.MustUser(r).ID
	var req ApplyPlanInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	res, err := h.svc.ApplyPlan(r.Context(), workspaceID, userID, req)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// applyMulti is the multi-file companion to applyPlan. The frontend posts
// the list of {fileToken, groups[]} entries it wants applied; each entry
// runs through ApplyPlan independently so a single bad file is reported
// in-band without blocking the rest of the batch from committing.
func (h *Handler) applyMulti(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	userID := auth.MustUser(r).ID
	var req ApplyMultiPlanInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if len(req.Files) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "files is required")
		return
	}
	res, err := h.svc.ApplyMultiPlan(r.Context(), workspaceID, userID, req)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) previewForExistingAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	accountID, ok := parseAccountID(w, r)
	if !ok {
		return
	}
	fileName, file, ok := readImportFile(w, r)
	if !ok {
		return
	}
	defer file.Close()
	res, err := h.svc.Preview(r.Context(), workspaceID, fileName, file, &accountID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type applyReq struct {
	FileToken string `json:"fileToken"`
	Currency  string `json:"currency,omitempty"`
}

func (h *Handler) applyToExistingAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := auth.MustWorkspace(r).ID
	userID := auth.MustUser(r).ID
	accountID, ok := parseAccountID(w, r)
	if !ok {
		return
	}
	var req applyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if req.FileToken == "" {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "fileToken is required")
		return
	}
	res, err := h.svc.Apply(r.Context(), workspaceID, accountID, userID, req.FileToken, req.Currency)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func parseAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "accountId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "accountId must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func readImportFile(w http.ResponseWriter, r *http.Request) (string, multipart.File, bool) {
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_multipart", "request must include a file field")
		return "", nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "file is required")
		return "", nil, false
	}
	return header.Filename, file, true
}
