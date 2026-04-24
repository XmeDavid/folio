package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/accounts"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/config"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/transactions"
)

type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Cfg    *config.Config
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(requestLogger(d.Logger))

	r.Get("/healthz", health(d))
	r.Get("/readyz", ready(d))

	identitySvc := identity.NewService(d.DB)
	identityH := identity.NewHandler(identitySvc)
	accountsSvc := accounts.NewService(d.DB)
	accountsH := accounts.NewHandler(accountsSvc)
	transactionsSvc := transactions.NewService(d.DB)
	transactionsH := transactions.NewHandler(transactionsSvc)
	classificationSvc := classification.NewService(d.DB)
	classificationH := classification.NewHandler(classificationSvc)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/version", versionHandler)

		// Public: no tenant context yet. Onboarding bootstraps a tenant.
		identityH.MountPublic(r)

		// Tenant-scoped routes: the RequireTenant middleware is a temporary
		// stand-in for real session auth and reads X-Tenant-ID from the
		// request. It will be replaced by backend/internal/auth.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireTenant)
			identityH.MountTenantScoped(r)
			r.Route("/accounts", accountsH.Mount)
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
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    "folio",
		"version": "0.0.0-dev",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
