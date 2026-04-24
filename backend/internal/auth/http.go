package auth

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// Handler mounts HTTP routes backed by auth.Service and identity.Service.
type Handler struct {
	svc *Service
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MountPublic mounts unauthenticated auth routes at the current chi router.
// Caller is responsible for mounting this under /api/v1.
func (h *Handler) MountPublic(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.With(RateLimitByIP(5, time.Hour)).Post("/signup", h.signup)     // 5/hr/IP
		r.With(RateLimitByIP(10, 10*time.Minute)).Post("/login", h.login) // 10/10min/IP
		r.Post("/logout", h.logout)
	})
}

// MountAuthed mounts authenticated, non-tenant-scoped routes (session required).
func (h *Handler) MountAuthed(r chi.Router) {
	r.Get("/me", h.me)
	r.Post("/tenants", h.createTenant)
}

// MountTenantScoped mounts routes under /t/{tenantId} that need a membership.
// Caller wires RequireSession + RequireMembership upstream.
func (h *Handler) MountTenantScoped(r chi.Router) {
	r.Get("/members", h.listMembers)
}

type signupReq struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	DisplayName    string `json:"displayName"`
	TenantName     string `json:"tenantName,omitempty"`
	BaseCurrency   string `json:"baseCurrency,omitempty"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	InviteToken    string `json:"inviteToken,omitempty"`
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var body signupReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := parseIPForStorage(ipFromRequest(r))
	out, err := h.svc.Signup(r.Context(), SignupInput{
		Email: body.Email, Password: body.Password, DisplayName: body.DisplayName,
		TenantName: body.TenantName, BaseCurrency: body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay, Locale: body.Locale, Timezone: body.Timezone,
		InviteToken: body.InviteToken, IP: ip, UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	SetSessionCookie(w, out.SessionToken)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user":        out.User,
		"tenant":      out.Tenant,
		"membership":  out.Membership,
		"mfaRequired": false,
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
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
	SetSessionCookie(w, out.SessionToken)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":        out.User,
		"mfaRequired": out.MFARequired,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	// Parse cookie directly — logout works even if the session is already
	// expired in the DB, and is mounted without RequireSession upstream so
	// the server-side DELETE must happen here.
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		sid := SessionIDFromToken(c.Value)
		var userID *uuid.UUID
		if sess, ok := SessionFromCtx(r.Context()); ok {
			userID = &sess.UserID
		}
		// Delete the row regardless of whether ctx had a user (best-effort
		// unconditional invalidation).
		_, _ = h.svc.pool.Exec(r.Context(), `delete from sessions where id = $1`, sid)
		// Audit: we know the session id; actor may be nil if the cookie
		// was stale. Swallow errors — audit is best-effort on logout.
		if userID != nil {
			ip := parseIPForStorage(ipFromRequest(r))
			h.svc.logAuditDirect(r.Context(), nil, userID, "user.logout", "user", *userID, ip, r.UserAgent())
		}
	}
	ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	_, tenants, err := h.svc.identity.Me(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":    user,
		"tenants": tenants,
	})
}

type createTenantReq struct {
	Name           string `json:"name"`
	BaseCurrency   string `json:"baseCurrency"`
	CycleAnchorDay int    `json:"cycleAnchorDay,omitempty"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone,omitempty"`
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	var body createTenantReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	t, m, err := h.svc.identity.CreateTenant(r.Context(), user.ID, identity.CreateTenantInput{
		Name: body.Name, BaseCurrency: body.BaseCurrency,
		CycleAnchorDay: body.CycleAnchorDay, Locale: body.Locale, Timezone: body.Timezone,
	})
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"tenant":     t,
		"membership": m,
	})
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	tenant := MustTenant(r)
	members, err := h.svc.identity.ListMembers(r.Context(), tenant.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

// parseIPForStorage parses a string IP to net.IP. Returns nil on parse failure.
func parseIPForStorage(s string) net.IP {
	if s == "" {
		return nil
	}
	ip := net.ParseIP(s)
	return ip
}

// silence unused import if chi isn't referenced at compile (it is, by router).
var _ = chi.NewRouter
