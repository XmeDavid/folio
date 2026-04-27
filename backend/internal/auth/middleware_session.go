package auth

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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
		q := dbq.New(s.pool)

		row, err := q.GetSessionByID(r.Context(), sid)
		if err != nil && errors.Is(err, pgx.ErrNoRows) {
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		sess := Session{
			ID: row.ID, UserID: row.UserID, CreatedAt: row.CreatedAt,
			ExpiresAt: row.ExpiresAt, LastSeenAt: row.LastSeenAt, ReauthAt: row.ReauthAt,
		}
		if !sess.ExpiresAt.After(now) {
			_ = q.DeleteSessionByID(r.Context(), sid)
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_expired", "sign in again")
			return
		}
		if now.Sub(sess.LastSeenAt) > s.cfg.SessionIdle {
			_ = q.DeleteSessionByID(r.Context(), sid)
			ClearSessionCookie(w, s.cfg.SecureCookies)
			httpx.WriteError(w, http.StatusUnauthorized, "session_idle", "sign in again")
			return
		}
		_ = q.UpdateSessionLastSeen(r.Context(), dbq.UpdateSessionLastSeenParams{LastSeenAt: now, ID: sid})

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
