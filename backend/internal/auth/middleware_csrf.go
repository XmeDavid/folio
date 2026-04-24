package auth

import (
	"net/http"
	"net/url"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// CSRF enforces on state-changing methods:
//  1. Origin (fallback Referer) is in allowedOrigins.
//  2. Custom X-Folio-Request: 1 header present.
//
// GET/HEAD/OPTIONS pass through.
func CSRF(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				if ref := r.Header.Get("Referer"); ref != "" {
					if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
						origin = u.Scheme + "://" + u.Host
					}
				}
			}
			if _, ok := allowed[origin]; !ok {
				httpx.WriteError(w, http.StatusForbidden, "csrf_origin", "origin not allowed")
				return
			}
			if r.Header.Get("X-Folio-Request") != "1" {
				httpx.WriteError(w, http.StatusForbidden, "csrf_header", "X-Folio-Request header required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
