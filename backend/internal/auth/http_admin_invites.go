package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/mailer"
)

// AdminInviteHandler owns the platform-level (admin-issued) invite routes.
// These are distinct from workspace invites: they gate signup in invite-only
// mode and are managed by platform admins, not workspace owners.
//
// Lives in the auth package alongside the workspace InviteHandler so it can
// reuse the inviteURL helper and the identity → auth one-way import edge.
type AdminInviteHandler struct {
	auth    *Service
	invites *identity.PlatformInviteService
	mail    mailer.Mailer
}

// NewAdminInviteHandler constructs an AdminInviteHandler. inv is usually
// identity.NewPlatformInviteService(pool); m is the configured mailer.Mailer.
func NewAdminInviteHandler(authSvc *Service, inv *identity.PlatformInviteService, m mailer.Mailer) *AdminInviteHandler {
	return &AdminInviteHandler{auth: authSvc, invites: inv, mail: m}
}

// MountAdmin mounts the admin-only platform invite routes under the admin
// subrouter. The caller wires admin gating (RequireSession + RequireAdmin)
// upstream — this matches the admin.Handler.Mount pattern in router.go.
//
// fresh is RequireFreshReauth(ttl) — applied only to mutating operations
// (create, revoke). list is read-only and stays outside the fresh gate so the
// admin console can show the active list without forcing a reauth round-trip.
func (h *AdminInviteHandler) MountAdmin(r chi.Router, fresh func(http.Handler) http.Handler) {
	r.Get("/invites", h.list)
	r.With(fresh).Post("/invites", h.create)
	r.With(fresh).Delete("/invites/{id}", h.revoke)
}

// MountPublic mounts the no-auth preview at /auth/platform-invites/{token}.
// The caller positions this under /api/v1 alongside the workspace invite
// public preview (see InviteHandler.MountPublicInvites).
func (h *AdminInviteHandler) MountPublic(r chi.Router) {
	r.Get("/auth/platform-invites/{token}", h.preview)
}

type createPlatformInviteReq struct {
	Email string `json:"email"`
}

type createPlatformInviteResp struct {
	Invite    identity.PlatformInvite `json:"invite"`
	Token     string                  `json:"token"`
	AcceptURL string                  `json:"acceptUrl"`
}

func (h *AdminInviteHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	actor := MustUser(r)

	// Body is optional; an empty body is treated as "open invite" (no email).
	var body createPlatformInviteReq
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
			return
		}
	}

	inv, plaintext, err := h.invites.Create(r.Context(), actor.ID, body.Email)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}

	// Best-effort send if the invite is targeted to a specific email.
	// NB: the "platform_invite" template is intentionally not shipped in this
	// task — the LogMailer will accept the message and log a warning when
	// LoadTemplate fails. The follow-up task that adds the signup wiring will
	// also add the template. Documented here so future readers don't chase a
	// missing-template log line as a bug.
	if h.mail != nil && inv.Email != nil && *inv.Email != "" {
		if err := h.mail.Send(r.Context(), mailer.Message{
			To:       *inv.Email,
			Subject:  "You're invited to Folio",
			Template: "platform_invite",
			Data: map[string]any{
				"InviterName": actor.DisplayName,
				"AcceptURL":   inviteURL(plaintext),
			},
		}); err != nil {
			slog.Default().Warn("mailer.send_failed",
				"err", err, "platform_invite_id", inv.ID)
		}
	}

	// Audit at the platform level: workspace_id is NULL (uuid.Nil → nullUUID
	// stores SQL NULL — see auth.WriteAudit). entity_id = the invite id.
	h.auth.WriteAudit(r.Context(), uuid.Nil, actor.ID,
		"admin.invite_created", "platform_invite", inv.ID, nil,
		map[string]any{"email": inv.Email})

	httpx.WriteJSON(w, http.StatusCreated, createPlatformInviteResp{
		Invite:    inv,
		Token:     plaintext,
		AcceptURL: inviteURL(plaintext),
	})
}

func (h *AdminInviteHandler) revoke(w http.ResponseWriter, r *http.Request) {
	actor := MustUser(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be a UUID")
		return
	}

	switch err := h.invites.Revoke(r.Context(), id, actor.ID); {
	case errors.Is(err, identity.ErrInviteNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	case errors.Is(err, identity.ErrInviteRevoked),
		errors.Is(err, identity.ErrInviteAlreadyUsed):
		httpx.WriteError(w, http.StatusGone, inviteErrCode(err), err.Error())
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}

	h.auth.WriteAudit(r.Context(), uuid.Nil, actor.ID,
		"admin.invite_revoked", "platform_invite", id, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminInviteHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.invites.ListActive(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
}

func (h *AdminInviteHandler) preview(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_token", "token is required")
		return
	}
	prev, err := h.invites.Preview(r.Context(), token)
	switch {
	case errors.Is(err, identity.ErrInviteNotFound),
		errors.Is(err, identity.ErrInviteRevoked),
		errors.Is(err, identity.ErrInviteAlreadyUsed),
		errors.Is(err, identity.ErrInviteExpired):
		httpx.WriteError(w, http.StatusGone, inviteErrCode(err), err.Error())
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prev)
}
