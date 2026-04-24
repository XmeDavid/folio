package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func (h *Handler) mfaStatus(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	st, err := h.svc.MFAStatus(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) enrollTOTP(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	out, err := h.svc.EnrollTOTP(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	codes, err := h.svc.ConfirmTOTP(r.Context(), user.ID, body.Code)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (h *Handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	sess := MustSession(r)
	if err := h.svc.DisableTOTP(r.Context(), user.ID, sess.ID); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	codes, err := h.svc.RegenerateRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (h *Handler) beginPasskeyEnrollment(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	options, session, err := h.svc.BeginPasskeyEnrollment(r.Context(), user.ID)
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"options": options, "session": session})
}

func (h *Handler) completePasskeyEnrollment(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	var body struct {
		Session    string          `json:"session"`
		Label      string          `json:"label"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	if len(body.Credential) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "credential is required")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body.Credential))
	r.ContentLength = int64(len(body.Credential))
	r.Header.Set("Content-Type", "application/json")
	if err := h.svc.FinishPasskeyEnrollment(r.Context(), user.ID, body.Session, body.Label, r); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) beginMFAWebAuthn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChallengeID string `json:"challengeId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	id, err := uuid.Parse(body.ChallengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_challenge", "invalid challenge")
		return
	}
	options, err := h.svc.BeginWebAuthnAssertion(r.Context(), id, parseIPForStorage(ipFromRequest(r)), r.UserAgent())
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"options": options})
}

func (h *Handler) completeMFAWebAuthn(w http.ResponseWriter, r *http.Request) {
	challengeID := r.URL.Query().Get("challengeId")
	id, err := uuid.Parse(challengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_challenge", "invalid challenge")
		return
	}
	out, err := h.svc.CompleteMFA(r.Context(), CompleteMFAInput{
		ChallengeID: id, Method: "webauthn", Request: r,
		IP: parseIPForStorage(ipFromRequest(r)), UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "mfa_failed", "MFA verification failed")
		return
	}
	SetSessionCookie(w, out.SessionToken, h.svc.cfg.SecureCookies)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": out.User, "mfaRequired": false})
}

func (h *Handler) reauth(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	sess := MustSession(r)
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	err := h.svc.CompleteReauth(r.Context(), sess.ID, user.ID, body.Password, body.Code)
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, ErrUseWebAuthnReauth) {
		httpx.WriteError(w, http.StatusConflict, "webauthn_required", "use POST /auth/reauth/webauthn/begin then /complete")
		return
	}
	httpx.WriteError(w, http.StatusUnauthorized, "reauth_failed", "re-authentication failed")
}

func (h *Handler) beginReauthWebauthn(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	options, challengeID, err := h.svc.BeginReauthWebAuthn(r.Context(), user.ID,
		parseIPForStorage(ipFromRequest(r)), r.UserAgent())
	if err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"challengeId": challengeID.String(),
		"options":     options,
	})
}

func (h *Handler) completeReauthWebauthn(w http.ResponseWriter, r *http.Request) {
	user := MustUser(r)
	sess := MustSession(r)
	challengeID := r.URL.Query().Get("challengeId")
	id, err := uuid.Parse(challengeID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_challenge", "invalid challenge")
		return
	}
	if err := h.svc.CompleteReauthWebAuthn(r.Context(), sess.ID, user.ID, id,
		parseIPForStorage(ipFromRequest(r)), r.UserAgent(), r); err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "reauth_failed", "re-authentication failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
