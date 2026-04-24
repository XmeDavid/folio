package auth

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RequireSession reads the session cookie, checks sliding + absolute
// expiry, bumps last_seen_at, loads the user, attaches Session + User
// to context. 401 on any failure.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		sid := SessionIDFromToken(c.Value)
		now := s.now().UTC()

		var sess Session
		err = s.pool.QueryRow(r.Context(), `
			select id, user_id, created_at, expires_at, last_seen_at, reauth_at
			from sessions where id = $1
		`, sid).Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.ReauthAt)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		if !sess.ExpiresAt.After(now) {
			_, _ = s.pool.Exec(r.Context(), `delete from sessions where id = $1`, sid)
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		if now.Sub(sess.LastSeenAt) > s.cfg.SessionIdle {
			_, _ = s.pool.Exec(r.Context(), `delete from sessions where id = $1`, sid)
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_idle", "sign in again")
			return
		}
		_, _ = s.pool.Exec(r.Context(), `update sessions set last_seen_at = $1 where id = $2`, now, sid)

		user, _, err := s.identity.Me(r.Context(), sess.UserID)
		if err != nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthenticated", "user not found")
			return
		}

		ctx := WithSession(r.Context(), sess)
		ctx = WithUser(ctx, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
