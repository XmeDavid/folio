package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// Handler mounts HTTP routes backed by auth.Service and identity.Service.
type Handler struct {
	svc        *Service
	emailRates *emailFlowRateLimits
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, emailRates: newEmailFlowRateLimits()}
}

// MountPublic mounts unauthenticated auth routes at the current chi router.
// Caller is responsible for mounting this under /api/v1.
func (h *Handler) MountPublic(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.With(RateLimitByIP(5, time.Hour)).Post("/signup", h.signup)     // 5/hr/IP
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login", h.login) // 10/10min/IP
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login/mfa/totp", h.completeMFATOTP)
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login/mfa/recovery", h.completeMFARecovery)
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login/mfa/webauthn/begin", h.beginMFAWebAuthn)
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login/mfa/webauthn/complete", h.completeMFAWebAuthn)
		r.Post("/logout", h.logout)
	})
}

// MountAuthed mounts authenticated, non-workspace-scoped routes (session required).
func (h *Handler) MountAuthed(r chi.Router) {
	r.Get("/me", h.me)
	r.Get("/me/mfa", h.mfaStatus)
	// Enroll/disable/regenerate all pin a new factor to the account, so they
	// all require a fresh reauth. Otherwise a stolen session cookie could pin
	// an attacker's TOTP or passkey onto the victim's account silently.
	reauth := RequireFreshReauth(h.svc.cfg.ReauthWindow)
	r.With(reauth).Post("/me/mfa/totp/enroll", h.enrollTOTP)
	r.With(reauth).Post("/me/mfa/totp/confirm", h.confirmTOTP)
	r.With(reauth).Delete("/me/mfa/totp", h.disableTOTP)
	r.With(reauth).Post("/me/mfa/recovery-codes", h.regenerateRecoveryCodes)
	r.With(reauth).Post("/me/mfa/passkeys/begin", h.beginPasskeyEnrollment)
	r.With(reauth).Post("/me/mfa/passkeys/complete", h.completePasskeyEnrollment)
	// Reauth is a password/passkey gate — brute-force protection parallels /login.
	r.With(RateLimitByIP(10, 10*time.Minute)).Post("/auth/reauth", h.reauth)
	r.With(RateLimitByIP(10, 10*time.Minute)).Post("/auth/reauth/webauthn/begin", h.beginReauthWebauthn)
	r.With(RateLimitByIP(10, 10*time.Minute)).Post("/auth/reauth/webauthn/complete", h.completeReauthWebauthn)
	r.Post("/workspaces", h.createWorkspace)
}

// MountWorkspaceScoped mounts routes under /t/{workspaceId} that need a membership.
// Caller wires RequireSession + RequireMembership upstream.
func (h *Handler) MountWorkspaceScoped(r chi.Router) {
	r.Get("/members", h.listMembers)
}

type signupReq struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	DisplayName    string `json:"displayName"`
	WorkspaceName     string `json:"workspaceName,omitempty"`
	BaseCurrency   string `json:"baseCurrency,omitempty"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	InviteToken    string `json:"inviteToken,omitempty"`
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB cap on auth payloads
	var body signupReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := parseIPForStorage(ipFromRequest(r))
	out, err := h.svc.Signup(r.Context(), SignupInput{
		Email: body.Email, Password: body.Password, DisplayName: body.DisplayName,
		WorkspaceName: body.WorkspaceName, BaseCurrency: body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay, Locale: body.Locale, Timezone: body.Timezone,
		InviteToken: body.InviteToken, IP: ip, UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	SetSessionCookie(w, out.SessionToken, h.svc.cfg.SecureCookies)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user":        out.User,
		"workspace":      out.Workspace,
		"membership":  out.Membership,
		"mfaRequired": false,
	})
}

type mfaCodeReq struct {
	ChallengeID string `json:"challengeId"`
	Code        string `json:"code"`
}

func (h *Handler) completeMFATOTP(w http.ResponseWriter, r *http.Request) {
	h.completeMFACode(w, r, "totp")
}

func (h *Handler) completeMFARecovery(w http.ResponseWriter, r *http.Request) {
	h.completeMFACode(w, r, "recovery")
}

func (h *Handler) completeMFACode(w http.ResponseWriter, r *http.Request, method string) {
	var body mfaCodeReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	id, err := uuid.Parse(body.ChallengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_challenge", "invalid challenge")
		return
	}
	out, err := h.svc.CompleteMFA(r.Context(), CompleteMFAInput{
		ChallengeID: id, Method: method, Code: body.Code,
		IP: parseIPForStorage(ipFromRequest(r)), UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "mfa_failed", "MFA verification failed")
		return
	}
	SetSessionCookie(w, out.SessionToken, h.svc.cfg.SecureCookies)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": out.User, "mfaRequired": false})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB cap on auth payloads
	var body loginReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := parseIPForStorage(ipFromRequest(r))
	out, err := h.svc.Login(r.Context(), LoginInput{
		Email: body.Email, Password: body.Password, IP: ip, UserAgent: r.UserAgent(),
	})
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	if out.MFARequired {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"mfaRequired": true,
			"challengeId": out.ChallengeID,
			"user": map[string]any{
				"id":          out.User.ID,
				"email":       out.User.Email,
				"displayName": out.User.DisplayName,
			},
		})
		return
	}
	SetSessionCookie(w, out.SessionToken, h.svc.cfg.SecureCookies)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        out.User,
		"mfaRequired": out.MFARequired,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		sid := SessionIDFromToken(c.Value)
		userID, err := dbq.New(h.svc.pool).DeleteSessionByIDReturningUserID(r.Context(), sid)
		switch {
		case err == nil:
			ip := parseIPForStorage(ipFromRequest(r))
			h.svc.logAuditDirect(r.Context(), nil, &userID, "user.logout", "user", userID, ip, r.UserAgent())
		case errors.Is(err, pgx.ErrNoRows):
			// Session already absent (stale cookie) — nothing to audit.
		default:
			// Log-but-don't-fail — cookie clearing still happens below.
			slog.Default().Warn("logout: delete session failed", "err", err)
		}
	}
	ClearSessionCookie(w, h.svc.cfg.SecureCookies)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	_, workspaces, err := h.svc.identity.Me(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":    user,
		"workspaces": workspaces,
	})
}

type createWorkspaceReq struct {
	Name           string `json:"name"`
	BaseCurrency   string `json:"baseCurrency"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone,omitempty"`
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB cap on auth payloads
	var body createWorkspaceReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	t, m, err := h.svc.identity.CreateWorkspace(r.Context(), user.ID, identity.CreateWorkspaceInput{
		Name: body.Name, BaseCurrency: body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay, Locale: body.Locale, Timezone: body.Timezone,
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"workspace":     t,
		"membership": m,
	})
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	workspace := MustWorkspace(r)
	resp, err := h.svc.identity.ListMembers(r.Context(), workspace.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// parseIPForStorage parses a string IP to net.IP. Returns nil on parse failure.
func parseIPForStorage(s string) net.IP {
	if s == "" {
		return nil
	}
	ip := net.ParseIP(s)
	return ip
}
