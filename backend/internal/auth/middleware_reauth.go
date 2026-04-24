package auth

import (
	"net/http"
	"time"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireFreshReauth checks sessions.reauth_at against the freshness window.
// Plan 4 activates this (adds the /auth/reauth endpoint that bumps reauth_at);
// plan 1 ships the check so routes are correctly gated — callers with a
// stale reauth_at get 403 reauth_required.
func RequireFreshReauth(window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, ok := SessionFromCtx(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusForbidden, "reauth_required", "re-authentication required")
				return
			}
			if sess.ReauthAt != nil && time.Since(*sess.ReauthAt) < window {
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteError(w, http.StatusForbidden, "reauth_required", "re-authentication required")
		})
	}
}
