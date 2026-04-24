package auth

import (
	"net/http"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func (s *Service) RequireEmailVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromCtx(r.Context())
		if !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		if user.EmailVerifiedAt == nil {
			httpx.WriteError(w, http.StatusForbidden, "email_unverified", "please verify your email before continuing")
			return
		}
		next.ServeHTTP(w, r)
	})
}
