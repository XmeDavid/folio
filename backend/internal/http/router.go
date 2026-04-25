package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/accounts"
	"github.com/xmedavid/folio/backend/internal/admin"
	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/bankimport"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/config"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/transactions"
)

type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Cfg    *config.Config
	Mailer mailer.Mailer
	Jobs   *jobs.Client
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(requestLogger(d.Logger))

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:3000"
	}
	r.Use(auth.CSRF([]string{appURL}))

	r.Get("/healthz", health(d))
	r.Get("/readyz", ready(d))

	identitySvc := identity.NewService(d.DB)
	inviteSvc := identity.NewInviteService(d.DB)
	adminSvc := admin.NewService(d.DB).WithJobs(d.Jobs).WithMailer(d.Mailer)
	authSvc := auth.NewService(d.DB, identitySvc, auth.Config{
		Registration:       auth.RegistrationMode(os.Getenv("REGISTRATION_MODE")),
		AppURL:             d.Cfg.AppURL,
		SecretKey:          d.Cfg.EncryptionKey,
		MFAChallengeTTL:    d.Cfg.MFAChallengeTTL,
		ReauthWindow:       d.Cfg.ReauthWindow,
		WebAuthnRPID:       d.Cfg.WebAuthnRPID,
		WebAuthnRPName:     d.Cfg.WebAuthnRPName,
		WebAuthnOrigins:    d.Cfg.WebAuthnRPOrigins,
		Jobs:               d.Jobs,
		AdminBootstrapHook: adminSvc.EnsureBootstrapAdminTx,
		SecureCookies:      os.Getenv("APP_ENV") != "development",
	})
	authH := auth.NewHandler(authSvc)
	inviteH := auth.NewInviteHandler(authSvc, inviteSvc, d.Mailer)
	adminH := admin.NewHandler(adminSvc)

	accountsSvc := accounts.NewService(d.DB)
	accountsH := accounts.NewHandler(accountsSvc)
	importSvc := bankimport.NewService(d.DB)
	importH := bankimport.NewHandler(importSvc)
	transactionsSvc := transactions.NewService(d.DB)
	transactionsH := transactions.NewHandler(transactionsSvc)
	classificationSvc := classification.NewService(d.DB)
	classificationH := classification.NewHandler(classificationSvc)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/version", versionHandler)

		// Public auth surface: /auth/signup, /auth/login, /auth/logout
		authH.MountPublic(r)
		authH.MountEmailFlows(r)
		// Public invite surface: GET /auth/invites/{token} (no auth),
		// POST /auth/invites/{token}/accept (RequireSession).
		inviteH.MountPublicInvites(r)

		// Authenticated, non-tenant-scoped: /me, POST /tenants
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			authH.MountAuthed(r)
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			r.Use(authSvc.RequireAdmin)
			adminH.Mount(r, auth.RequireFreshReauth(authSvc.Config().ReauthWindow))
		})

		// Restore is tenant-scoped but must see soft-deleted tenants, so it
		// cannot sit behind RequireMembership (which hides deleted rows).
		r.With(authSvc.RequireSession, auth.RequireFreshReauth(authSvc.Config().ReauthWindow), authSvc.RequireTenantOwnerIncludingDeleted).
			Post("/t/{tenantId}/restore", authH.RestoreTenant)

		// Tenant-scoped active-tenant routes: /api/v1/t/{tenantId}/...
		r.Route("/t/{tenantId}", func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			r.Use(authSvc.RequireMembership)

			authH.MountTenantScoped(r) // /members
			authH.MountTenantAdmin(r)  // PATCH /, DELETE / — owner-gated
			r.With(authSvc.RequireEmailVerified).Route("/invites", inviteH.MountTenantInvites)

			r.Route("/accounts", func(r chi.Router) {
				importH.MountAccountRoutes(r)
				accountsH.Mount(r)
			})
			r.Route("/transactions", transactionsH.Mount)
			r.Route("/transactions/{transactionId}/tags", classificationH.MountTransactionTags)
			r.Post("/transactions/{transactionId}/apply-categorization-rules",
				classificationH.ApplyRulesToTransactionHandler)
			r.Route("/categories", classificationH.MountCategories)
			r.Route("/merchants", classificationH.MountMerchants)
			r.Route("/tags", classificationH.MountTags)
			r.Route("/categorization-rules", classificationH.MountCategorizationRules)
		})
	})
	return r
}

func health(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ready(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := ctxWithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unreachable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"name": "folio", "version": "0.0.0-dev"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func ctxWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}
