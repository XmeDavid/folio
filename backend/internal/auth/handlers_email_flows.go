package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func (h *Handler) MountEmailFlows(r chi.Router) {
	r.Post("/auth/verify", h.verifyEmail)
	r.Post("/auth/password/reset-request", h.requestPasswordReset)
	r.Post("/auth/password/reset-confirm", h.confirmPasswordReset)
	r.With(h.svc.RequireSession).Post("/auth/verify/resend", h.resendVerification)
	r.With(h.svc.RequireSession, h.svc.RequireEmailVerified).Post("/auth/email/change-request", h.requestEmailChange)
	r.Post("/auth/email/change-confirm", h.confirmEmailChange)
}

type tokenReq struct {
	Token string `json:"token"`
}

func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body tokenReq
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	if err := h.svc.VerifyEmail(r.Context(), body.Token); err != nil {
		writeTokenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type resetRequestReq struct {
	Email string `json:"email"`
}

func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body resetRequestReq
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	ip := ipFromRequest(r)
	if !h.emailRates.allowPasswordReset(ip, body.Email) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.svc.RequestPasswordReset(r.Context(), body.Email, parseIPForStorage(ip), r.UserAgent()); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type confirmResetReq struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

func (h *Handler) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body confirmResetReq
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	if err := h.svc.ResetPassword(r.Context(), body.Token, body.NewPassword); err != nil {
		writeTokenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	if !h.emailRates.allowVerifyResend(user.ID) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
		return
	}
	if err := h.svc.SendEmailVerification(r.Context(), user.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type emailChangeReq struct {
	Email string `json:"email"`
}

func (h *Handler) requestEmailChange(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	var body emailChangeReq
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	if !h.emailRates.allowEmailChange(user.ID) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
		return
	}
	if err := h.svc.RequestEmailChange(r.Context(), user.ID, body.Email); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var body tokenReq
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	if err := h.svc.ConfirmEmailChange(r.Context(), body.Token); err != nil {
		writeTokenError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTokenExpired):
		httpx.WriteError(w, http.StatusGone, "token_expired", "token expired")
	case errors.Is(err, ErrTokenInvalid):
		httpx.WriteError(w, http.StatusGone, "token_invalid", "token invalid")
	default:
		httpx.WriteServiceError(w, err)
	}
}
