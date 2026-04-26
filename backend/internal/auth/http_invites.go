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

// InviteHandler owns the workspace-scoped invite CRUD routes plus the public
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

// MountWorkspaceInvites mounts workspace-scoped invite routes at /invites under
// the workspace path. The caller wires RequireSession + RequireMembership
// upstream. Role rules are enforced inside each handler because the "only
// owners can invite owners" rule depends on the target role in the body.
// RequireFreshReauth is deliberately deferred to Plan 4 (spec §0.2 adjust).
func (h *InviteHandler) MountWorkspaceInvites(r chi.Router) {
	r.Post("/", h.createInvite)
	r.Delete("/{inviteId}", h.revokeInvite)
}

// MountPublicInvites mounts the two public invite endpoints at
// `/auth/invites/{token}` (no auth — preview) and
// `/auth/invites/{token}/accept` (session required). The caller positions
// these under /api/v1 alongside the other auth routes.
func (h *InviteHandler) MountPublicInvites(r chi.Router) {
	r.Route("/auth/invites/{token}", func(r chi.Router) {
		r.Get("/", h.previewInvite)
		r.With(h.auth.RequireSession, h.auth.RequireEmailVerified).Post("/accept", h.acceptInvite)
	})
}

type createInviteReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *InviteHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	workspace := MustWorkspace(r)
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

	inv, plaintext, err := h.invites.Create(r.Context(), workspace.ID, inviter.ID, body.Email, role)
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
				"InviterName": inviter.DisplayName,
				"WorkspaceName":  workspace.Name,
				"Role":        string(inv.Role),
				"AcceptURL":   inviteURL(plaintext),
			},
			WorkspaceID: workspace.ID.String(),
		}); err != nil {
			slog.Default().Warn("mailer.send_failed", "err", err, "invite_id", inv.ID)
		}
	}

	h.auth.WriteAudit(r.Context(), workspace.ID, inviter.ID,
		"member.invited", "invite", inv.ID, nil,
		map[string]any{"email": inv.Email, "role": string(inv.Role)})

	httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *InviteHandler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	workspace := MustWorkspace(r)
	requester := MustUser(r)
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "inviteId must be a UUID")
		return
	}

	err = h.invites.Revoke(r.Context(), workspace.ID, inviteID, requester.ID)
	switch {
	case errors.Is(err, identity.ErrInviteNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	case errors.Is(err, identity.ErrNotAuthorized):
		httpx.WriteError(w, http.StatusForbidden, "forbidden",
			"only the inviter or a workspace owner can revoke this invite")
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.auth.WriteAudit(r.Context(), workspace.ID, requester.ID,
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

// previewInvite is the no-auth endpoint that returns sanitized invite
// metadata (workspace name, inviter, role, expiry, invited email) so the
// landing page can show "Alice invited you to join Household as a member".
// Invalid/expired/revoked/consumed invites return 410 with a typed code.
func (h *InviteHandler) previewInvite(w http.ResponseWriter, r *http.Request) {
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

// acceptInvite consumes an invite on behalf of the authenticated user.
// Requires RequireSession (mounted by MountPublicInvites). Email-mismatch
// and unverified-email rejections surface as 403 with typed codes so the
// UI can show the right remediation (sign in as a different account,
// verify your email first).
func (h *InviteHandler) acceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_token", "token is required")
		return
	}
	user := MustUser(r)
	mem, err := h.invites.Accept(r.Context(), token, user.ID)
	switch {
	case errors.Is(err, identity.ErrInviteEmailMismatch):
		httpx.WriteError(w, http.StatusForbidden, "email_mismatch",
			"invite email does not match your account")
		return
	case errors.Is(err, identity.ErrEmailUnverified):
		httpx.WriteError(w, http.StatusForbidden, "email_unverified",
			"verify your email before accepting this invite")
		return
	case errors.Is(err, identity.ErrInviteExpired),
		errors.Is(err, identity.ErrInviteRevoked),
		errors.Is(err, identity.ErrInviteAlreadyUsed),
		errors.Is(err, identity.ErrInviteNotFound):
		httpx.WriteError(w, http.StatusGone, inviteErrCode(err), err.Error())
		return
	case err != nil:
		httpx.WriteServiceError(w, err)
		return
	}
	h.auth.WriteAudit(r.Context(), mem.WorkspaceID, user.ID,
		"member.invite_accepted", "membership", mem.UserID, nil,
		map[string]any{"role": string(mem.Role)})
	httpx.WriteJSON(w, http.StatusOK, mem)
}

// inviteErrCode maps sentinel errors to wire-stable error codes so the UI
// can pick the right CTA (expired → "get a new invite", already-used →
// "sign in instead", revoked → "contact the inviter").
func inviteErrCode(err error) string {
	switch {
	case errors.Is(err, identity.ErrInviteNotFound):
		return "invite_not_found"
	case errors.Is(err, identity.ErrInviteRevoked):
		return "invite_revoked"
	case errors.Is(err, identity.ErrInviteAlreadyUsed):
		return "invite_already_used"
	case errors.Is(err, identity.ErrInviteExpired):
		return "invite_expired"
	}
	return "invite_invalid"
}
