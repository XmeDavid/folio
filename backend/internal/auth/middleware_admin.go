package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireAdmin gates a route on users.is_admin. It returns 404 on miss so
// admin-ness is not enumerable. Chain it after RequireSession.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok || !user.IsAdmin {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}
