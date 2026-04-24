package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/mailer"
)

// InviteHandler owns the tenant-scoped invite CRUD routes plus the public
// preview/accept endpoints (wired under /api/v1/auth/invites/{token}).
// Lives in the auth package to keep the identity <- auth import direction
// one-way (identity never imports auth).
type InviteHandler struct {
	auth    *Service
	invites *identity.InviteService
	mail    mailer.Mailer
}

// NewInviteHandler constructs an InviteHandler. invites is usually
// identity.NewInviteService(pool); mail is the configured mailer.Mailer
// (LogMailer in Plan 2; ResendMailer in Plan 3).
func NewInviteHandler(authSvc *Service, invites *identity.InviteService, mail mailer.Mailer) *InviteHandler {
	return &InviteHandler{auth: authSvc, invites: invites, mail: mail}
}

// MountTenantInvites mounts tenant-scoped invite routes at /invites under
// the tenant path. The caller wires RequireSession + RequireMembership
// upstream. Role rules are enforced inside each handler because the "only
// owners can invite owners" rule depends on the target role in the body.
// RequireFreshReauth is deliberately deferred to Plan 4 (spec §0.2 adjust).
func (h *InviteHandler) MountTenantInvites(r chi.Router) {
	r.Post("/", h.createInvite)
	r.Delete("/{inviteId}", h.revokeInvite)
}

type createInviteReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *InviteHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	tenant := MustTenant(r)
	inviter := MustUser(r)
	callerRole, _ := RoleFromCtx(r.Context())

	var body createInviteReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	role := identity.Role(body.Role)
	if !role.Valid() {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_role", "role must be 'owner' or 'member'")
		return
	}
	// Non-owners may only invite members.
	if callerRole != identity.RoleOwner && role == identity.RoleOwner {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "only owners can invite owners")
		return
	}

	inv, plaintext, err := h.invites.Create(r.Context(), tenant.ID, inviter.ID, body.Email, role)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}

	// Best-effort email. A transient mailer outage shouldn't abort the
	// invite — the admin console has a resend action (Plan 5).
	if h.mail != nil {
		if err := h.mail.Send(r.Context(), mailer.Message{
			To:       inv.Email,
			Subject:  "You're invited to Folio",
			Template: "invite",
			Data: map[string]any{
				"inviteURL": inviteURL(plaintext),
				"tenantId":  tenant.ID.String(),
				"role":      string(inv.Role),
			},
			TenantID: tenant.ID.String(),
		}); err != nil {
			slog.Default().Warn("mailer.send_failed", "err", err, "invite_id", inv.ID)
		}
	}

	h.auth.WriteAudit(r.Context(), tenant.ID, inviter.ID,
		"member.invited", "invite", inv.ID, nil,
		map[string]any{"email": inv.Email, "role": string(inv.Role)})

	httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *InviteHandler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	tenant := MustTenant(r)
	requester := MustUser(r)
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "inviteId must be a UUID")
		return
	}

	err = h.invites.Revoke(r.Context(), inviteID, requester.ID)
	switch {
	case errors.Is(err, identity.ErrInviteNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	case errors.Is(err, identity.ErrNotAuthorized):
		httpx.WriteError(w, http.StatusForbidden, "forbidden",
			"only the inviter or a tenant owner can revoke this invite")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.auth.WriteAudit(r.Context(), tenant.ID, requester.ID,
		"member.invite_revoked", "invite", inviteID, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// inviteURL builds the accept-invite URL the recipient clicks. Reads
// APP_URL from the environment; defaults to http://localhost:3000 for dev.
func inviteURL(plaintext string) string {
	base := os.Getenv("APP_URL")
	if base == "" {
		base = "http://localhost:3000"
	}
	return base + "/accept-invite/" + plaintext
}
