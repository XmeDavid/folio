package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/accounts"
	"github.com/xmedavid/folio/backend/internal/admin"
	"github.com/xmedavid/folio/backend/internal/auth"
	"github.com/xmedavid/folio/backend/internal/bankimport"
	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/config"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
	"github.com/xmedavid/folio/backend/internal/investments"
	"github.com/xmedavid/folio/backend/internal/jobs"
	"github.com/xmedavid/folio/backend/internal/mailer"
	"github.com/xmedavid/folio/backend/internal/marketdata"
	"github.com/xmedavid/folio/backend/internal/transactions"
	"github.com/xmedavid/folio/backend/internal/transfers"
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
	platformInviteSvc := identity.NewPlatformInviteService(d.DB)
	adminSvc := admin.NewService(d.DB).WithJobs(d.Jobs).WithMailer(d.Mailer)
	authSvc := auth.NewService(d.DB, identitySvc, platformInviteSvc, auth.Config{
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
	adminInviteH := auth.NewAdminInviteHandler(authSvc, platformInviteSvc, d.Mailer)

	accountsSvc := accounts.NewService(d.DB)
	accountsH := accounts.NewHandler(accountsSvc)
	classificationSvc := classification.NewService(d.DB)
	classificationH := classification.NewHandler(classificationSvc)
	transfersSvc := transfers.NewService(d.DB)
	transfersH := transfers.NewHandler(transfersSvc)
	importSvc := bankimport.NewService(d.DB, classificationSvc, transfersSvc)
	importH := bankimport.NewHandler(importSvc)
	transactionsSvc := transactions.NewService(d.DB, classificationSvc)
	transactionsH := transactions.NewHandler(transactionsSvc)

	// Investments use a marketdata service (FX + price cache + providers).
	// Providers are HTTP clients to public sources (Yahoo, Frankfurter); they
	// are wired up unconditionally and the cache layer falls back to stale
	// rows when an upstream call fails. To disable network calls in tests or
	// for offline development, set MARKETDATA_OFFLINE=1.
	var priceProvider marketdata.PriceProvider
	var fxProvider marketdata.FXProvider
	if os.Getenv("MARKETDATA_OFFLINE") == "" {
		priceProvider = marketdata.NewYahooProvider()
		fxProvider = marketdata.NewFrankfurterProvider()
	}
	mdSvc := marketdata.NewService(d.DB, priceProvider, fxProvider)
	investmentsSvc := investments.NewService(d.DB, mdSvc)
	investmentsH := investments.NewHandler(investmentsSvc)
	// Brokerage/asset accounts hold positions, not cash; fold their market
	// value into account.Balance so the dashboard and accounts list reflect
	// real worth and net-worth totals add up.
	accountsSvc.SetPositionValuator(investmentsSvc)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/version", versionHandler)

		// Public auth surface: /auth/signup, /auth/login, /auth/logout
		authH.MountPublic(r)
		authH.MountEmailFlows(r)
		// Public invite surface: GET /auth/invites/{token} (no auth),
		// POST /auth/invites/{token}/accept (RequireSession).
		inviteH.MountPublicInvites(r)
		// Public platform invite preview: GET /auth/platform-invites/{token}.
		// Sanitised metadata only; no auth required.
		adminInviteH.MountPublic(r)

		// Authenticated, non-workspace-scoped: /me, POST /workspaces
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			authH.MountAuthed(r)
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			r.Use(authSvc.RequireAdmin)
			adminH.Mount(r, auth.RequireFreshReauth(authSvc.Config().ReauthWindow))
			adminInviteH.MountAdmin(r, auth.RequireFreshReauth(authSvc.Config().ReauthWindow))
		})

		// Restore is workspace-scoped but must see soft-deleted workspaces, so it
		// cannot sit behind RequireMembership (which hides deleted rows).
		r.With(authSvc.RequireSession, auth.RequireFreshReauth(authSvc.Config().ReauthWindow), authSvc.RequireWorkspaceOwnerIncludingDeleted).
			Post("/t/{workspaceId}/restore", authH.RestoreWorkspace)

		// Workspace-scoped active-workspace routes: /api/v1/t/{workspaceId}/...
		r.Route("/t/{workspaceId}", func(r chi.Router) {
			r.Use(authSvc.RequireSession)
			r.Use(authSvc.RequireMembership)

			authH.MountWorkspaceScoped(r) // /members
			authH.MountWorkspaceAdmin(r)  // PATCH /, DELETE / — owner-gated
			r.With(authSvc.RequireEmailVerified).Route("/invites", inviteH.MountWorkspaceInvites)

			r.Route("/accounts", func(r chi.Router) {
				// Order matters: chi's Mux.Handle is last-write-wins, and
				// MountAccountRoutes also registers POST /import-preview. The
				// smart-import dispatcher MUST be registered *after* it so the
				// dispatcher (which falls through to bankimport itself when no
				// investment format is detected) wins the route.
				importH.MountAccountRoutes(r)
				r.Post("/import-preview", smartImportPreview(investmentsSvc, importSvc))
				r.Post("/imports/multi-preview", smartImportMultiPreview(investmentsSvc, importSvc))
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
			r.Route("/investments", investmentsH.Mount)
			r.Route("/transfers", transfersH.Mount)
		})
	})
	return r
}

// smartImportPreview reads the upload, asks the investments service whether
// it recognises an investment format and ingests on the spot when it does,
// otherwise replays the request body into the bank-import previewForNewAccount
// path. Returns the same JSON envelope shape so the existing UI can decide
// what to render via the `kind` field.
func smartImportPreview(invSvc *investments.Service, bank *bankimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := auth.MustWorkspace(r).ID
		// Cap upload size at 8 MiB to match bankimport.
		const maxBytes = 8 << 20
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_multipart", "request must include a file field")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "file is required")
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "failed to read upload")
			return
		}

		entry := smartPreviewOneFile(r.Context(), workspaceID, header.Filename, body, invSvc, bank)
		if entry.HTTPError != nil {
			httpx.WriteServiceError(w, entry.HTTPError)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, entry.Body)
	}
}

// smartImportMultiPreview is the multi-file companion to smartImportPreview.
// Files are accepted under repeated `files` form fields. Each file runs
// through the same smart-detect + bank-preview pipeline as the single-file
// endpoint; per-file errors land as `{kind: "error", error: "..."}`
// entries instead of aborting siblings, since the user reasonably expects
// "two of three files imported" rather than the whole drop being rejected
// because one file was malformed.
func smartImportMultiPreview(invSvc *investments.Service, bank *bankimport.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := auth.MustWorkspace(r).ID
		// Cap each upload at 8 MiB to match bankimport. We allow up to 20 MiB
		// total across the whole multipart body to leave room for several
		// real Revolut exports without bloating the cap.
		const maxBytes = 20 << 20
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_multipart", "request must include file fields")
			return
		}
		headers := r.MultipartForm.File["files"]
		if len(headers) == 0 {
			// Accept the singular `file` field as a convenience so the
			// frontend can use the same endpoint for one-or-many uploads.
			headers = r.MultipartForm.File["file"]
		}
		if len(headers) == 0 {
			httpx.WriteError(w, http.StatusBadRequest, "validation_error", "at least one file is required")
			return
		}
		entries := make([]map[string]any, 0, len(headers))
		for _, fh := range headers {
			file, err := fh.Open()
			if err != nil {
				entries = append(entries, errorEntry(fh.Filename, "could not read upload"))
				continue
			}
			body, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil {
				entries = append(entries, errorEntry(fh.Filename, "could not read upload"))
				continue
			}
			res := smartPreviewOneFile(r.Context(), workspaceID, fh.Filename, body, invSvc, bank)
			if res.HTTPError != nil {
				entries = append(entries, errorEntry(fh.Filename, res.HTTPError.Error()))
				continue
			}
			res.Body["fileName"] = fh.Filename
			entries = append(entries, res.Body)
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": entries})
	}
}

type smartPreviewResult struct {
	Body      map[string]any
	HTTPError error
}

func smartPreviewOneFile(ctx context.Context, workspaceID uuid.UUID, fileName string, body []byte, invSvc *investments.Service, bank *bankimport.Service) smartPreviewResult {
	smart, err := invSvc.SmartImport(ctx, workspaceID, body, fileName)
	if err != nil {
		return smartPreviewResult{HTTPError: err}
	}
	if smart != nil && smart.Detected {
		return smartPreviewResult{Body: map[string]any{
			"kind":       "investment",
			"investment": smart,
		}}
	}
	preview, err := bank.Preview(ctx, workspaceID, fileName, bytes.NewReader(body), nil)
	if err != nil {
		return smartPreviewResult{HTTPError: err}
	}
	return smartPreviewResult{Body: map[string]any{
		"kind":    "bank",
		"preview": preview,
	}}
}

func errorEntry(fileName, message string) map[string]any {
	return map[string]any{
		"kind":     "error",
		"fileName": fileName,
		"error":    message,
	}
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
