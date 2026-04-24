package auth

import "net/http"

const sessionCookieName = "folio_session"

// SetSessionCookie writes the session cookie (HttpOnly, Secure, SameSite=Lax,
// Path=/, host-only). The server-side sessions.expires_at is the real bound;
// the cookie is a session cookie (evicted on browser quit).
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes the cookie client-side via Max-Age=-1.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionCookieName returns the cookie name, exported for test + handler reuse.
func SessionCookieName() string { return sessionCookieName }
