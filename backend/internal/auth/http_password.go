package auth

import (
	"encoding/json"
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

type changePasswordReq struct {
	Current string `json:"current"`
	Next    string `json:"next"`
}

// changePassword verifies the current password, applies the password policy to
// the new password, persists it, and revokes every other session. Mounted
// under RequireFreshReauth — a stolen cookie alone shouldn't be able to lock
// the legitimate owner out by rotating credentials.
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "expected JSON")
		return
	}
	user := MustUser(r)
	sess := MustSession(r)
	if err := h.svc.ChangePassword(r.Context(), user.ID, sess.ID, body.Current, body.Next); err != nil {
		httpx.WriteServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
