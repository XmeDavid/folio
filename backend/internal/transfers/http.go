package transfers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Handler exposes the transfer-pair detector endpoints over HTTP.
type Handler struct{ svc *Service }

// NewHandler returns a Handler for svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Mount installs the transfer routes under the parent router (expected to be
// the workspace-scoped subrouter, i.e. /api/v1/t/{workspaceId}/transfers).
func (h *Handler) Mount(r chi.Router) {
	r.Post("/detect", h.detect)
	r.Post("/manual-pair", h.manualPair)
	r.Delete("/{matchId}", h.unpair)
	r.Get("/candidates", h.listCandidates)
	r.Get("/candidates/count", h.candidateCount)
	r.Post("/candidates/{candidateId}/decline", h.declineCandidate)
}

func (h *Handler) detect(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	res, err := h.svc.DetectAndPair(r.Context(), wsID, DetectScope{All: true})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type manualPairReq struct {
	SourceID      string  `json:"sourceId"`
	DestinationID *string `json:"destinationId"` // nil ⇒ outbound-to-external
	FeeAmount     *string `json:"feeAmount"`
	FeeCurrency   *string `json:"feeCurrency"`
	ToleranceNote *string `json:"toleranceNote"`
}

func (h *Handler) manualPair(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	var req manualPairReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	src, err := uuid.Parse(req.SourceID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "sourceId must be a UUID")
		return
	}
	in := ManualPairInput{
		SourceID:      src,
		FeeAmount:     req.FeeAmount,
		FeeCurrency:   req.FeeCurrency,
		ToleranceNote: req.ToleranceNote,
	}
	if req.DestinationID != nil {
		dst, err := uuid.Parse(*req.DestinationID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "destinationId must be a UUID")
			return
		}
		in.DestinationID = &dst
	}
	res, err := h.svc.ManualPair(r.Context(), wsID, in)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) unpair(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "matchId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "matchId must be a UUID")
		return
	}
	if err := h.svc.Unpair(r.Context(), wsID, id); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCandidates(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	res, err := h.svc.ListPendingCandidates(r.Context(), wsID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) candidateCount(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	n, err := h.svc.CountPendingCandidates(r.Context(), wsID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (h *Handler) declineCandidate(w http.ResponseWriter, r *http.Request) {
	wsID := auth.MustWorkspace(r).ID
	id, err := uuid.Parse(chi.URLParam(r, "candidateId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "candidateId must be a UUID")
		return
	}
	user := auth.MustUser(r)
	if err := h.svc.DeclineCandidate(r.Context(), wsID, id, &user.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
