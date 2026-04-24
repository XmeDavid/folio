package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

// RequireRole permits the request if the caller's role is in allowed.
// 403 otherwise. Must be mounted AFTER RequireMembership.
func RequireRole(allowed ...identity.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r_, ok := RoleFromCtx(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "role required")
				return
			}
			for _, a := range allowed {
				if r_ == a {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "insufficient role")
		})
	}
}
