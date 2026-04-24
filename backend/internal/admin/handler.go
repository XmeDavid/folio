package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Mount(r chi.Router, requireFreshReauth func(http.Handler) http.Handler) {
	r.Get("/tenants", h.listTenants)
	r.Get("/tenants/{tenantId}", h.tenantDetail)
	r.Get("/users", h.listUsers)
	r.Get("/users/{userId}", h.userDetail)
	r.Get("/audit", h.listAudit)
	r.Get("/jobs", h.listJobs)
	r.Group(func(r chi.Router) {
		r.Use(requireFreshReauth)
		r.Post("/jobs/{jobId}/retry", h.retryJob)
		r.Post("/emails/{emailId}/resend", h.resendEmail)
		r.Post("/users/{userId}/grant-admin", h.grantAdmin)
		r.Post("/users/{userId}/revoke-admin", h.revokeAdmin)
	})
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, p, err := h.svc.ListTenants(r.Context(), TenantListFilter{
		AdminListFilter: AdminListFilter{Limit: intQuery(q.Get("limit")), Cursor: q.Get("cursor")},
		Search:          q.Get("search"),
		IncludeDeleted:  boolQuery(q.Get("includeDeleted")),
	})
	writeList(w, rows, p, err)
}

func (h *Handler) tenantDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "tenantId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid tenant id")
		return
	}
	out, err := h.svc.TenantDetail(r.Context(), id, auth.MustUser(r).ID)
	writeOne(w, out, err)
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, p, err := h.svc.ListUsers(r.Context(), UserListFilter{
		AdminListFilter: AdminListFilter{Limit: intQuery(q.Get("limit")), Cursor: q.Get("cursor")},
		Search:          q.Get("search"),
		IsAdminOnly:     boolQuery(q.Get("isAdminOnly")),
	})
	writeList(w, rows, p, err)
}

func (h *Handler) userDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid user id")
		return
	}
	out, err := h.svc.UserDetail(r.Context(), id, auth.MustUser(r).ID)
	writeOne(w, out, err)
}

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := AuditFilter{AdminListFilter: AdminListFilter{Limit: intQuery(q.Get("limit")), Cursor: q.Get("cursor")}, Action: q.Get("action")}
	if v := q.Get("actorUserId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid actor user id")
			return
		}
		filter.ActorUserID = &id
	}
	if v := q.Get("tenantId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid tenant id")
			return
		}
		filter.TenantID = &id
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid since")
			return
		}
		filter.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid until")
			return
		}
		filter.Until = &t
	}
	rows, p, err := h.svc.ListAudit(r.Context(), filter, auth.MustUser(r).ID)
	writeList(w, rows, p, err)
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, p, err := h.svc.ListJobs(r.Context(), JobFilter{
		AdminListFilter: AdminListFilter{Limit: intQuery(q.Get("limit")), Cursor: q.Get("cursor")},
		State:           q.Get("state"),
		Kind:            q.Get("kind"),
	})
	writeList(w, rows, p, err)
}

func (h *Handler) retryJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "jobId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid job id")
		return
	}
	writeNoData(w, h.svc.RetryJob(r.Context(), id, auth.MustUser(r).ID))
}

func (h *Handler) resendEmail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "emailId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid email id")
		return
	}
	writeNoData(w, h.svc.ResendEmail(r.Context(), id, auth.MustUser(r).ID))
}

func (h *Handler) grantAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid user id")
		return
	}
	writeNoData(w, h.svc.GrantAdmin(r.Context(), id, auth.MustUser(r).ID))
}

func (h *Handler) revokeAdmin(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", "invalid user id")
		return
	}
	writeNoData(w, h.svc.RevokeAdmin(r.Context(), id, auth.MustUser(r).ID))
}

func writeList[T any](w http.ResponseWriter, data []T, p Pagination, err error) {
	if err != nil {
		writeAdminError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": data, "pagination": p})
}

func writeOne(w http.ResponseWriter, data any, err error) {
	if err != nil {
		writeAdminError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
}

func writeNoData(w http.ResponseWriter, err error) {
	if err != nil {
		writeAdminError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeAdminError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrLastAdmin) {
		httpx.WriteError(w, http.StatusConflict, "last_admin", "cannot revoke the last admin")
		return
	}
	httpx.WriteServiceError(w, err)
}

func intQuery(v string) int {
	i, _ := strconv.Atoi(v)
	return i
}

func boolQuery(v string) bool {
	b, _ := strconv.ParseBool(v)
	return b
}
