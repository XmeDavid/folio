// Package httpx contains small HTTP helpers shared across feature packages:
// typed errors, JSON response writers, and temporary dev-only middleware that
// extracts a tenant/user identity from request headers.
//
// The middleware here is a stand-in for real authentication. It will be
// replaced by session-cookie auth (see backend/internal/auth) once that lands.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// WriteJSON writes status + body as application/json. Encoding errors are
// swallowed intentionally — a half-written response is already a lost cause.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ErrorBody is the wire shape for error responses.
type ErrorBody struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details any    `json:"details,omitempty"`
}

// WriteError writes a JSON error with an optional code.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: message, Code: code})
}

// ValidationError is returned from service-layer validation/normalization.
// It maps to HTTP 400.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// NewValidationError creates a ValidationError.
func NewValidationError(msg string) error { return &ValidationError{Msg: msg} }

// NotFoundError maps to HTTP 404.
type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What + " not found" }

// NewNotFoundError creates a NotFoundError.
func NewNotFoundError(what string) error { return &NotFoundError{What: what} }

// WriteServiceError maps a service-layer error to the appropriate HTTP response.
func WriteServiceError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	var nferr *NotFoundError
	switch {
	case errors.As(err, &verr):
		WriteError(w, http.StatusBadRequest, "validation_error", verr.Msg)
	case errors.As(err, &nferr):
		WriteError(w, http.StatusNotFound, "not_found", nferr.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// --- Context helpers for the temporary identity stand-in. ---

type ctxKey string

const (
	tenantIDKey ctxKey = "folio.tenant_id"
	userIDKey   ctxKey = "folio.user_id"
)

// TenantIDFrom returns the tenant UUID from request context, if set.
func TenantIDFrom(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(tenantIDKey).(uuid.UUID)
	return v, ok
}

// UserIDFrom returns the user UUID from request context, if set.
func UserIDFrom(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(userIDKey).(uuid.UUID)
	return v, ok
}

// WithTenantID returns a context carrying the tenant id. Exported for tests.
func WithTenantID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantIDKey, id)
}

// WithUserID returns a context carrying the user id. Exported for tests.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// RequireTenant is a TEMPORARY dev-only middleware that reads the tenant id
// from the X-Tenant-ID header. It will be replaced by real session-based
// auth; do not ship to production as-is.
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Tenant-ID")
		if raw == "" {
			WriteError(w, http.StatusUnauthorized, "tenant_required",
				"X-Tenant-ID header required (temporary stand-in for auth)")
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "tenant_invalid",
				"X-Tenant-ID must be a UUID")
			return
		}
		ctx := WithTenantID(r.Context(), id)

		// Optional X-User-ID; ignored if not parseable.
		if raw := r.Header.Get("X-User-ID"); raw != "" {
			if uid, err := uuid.Parse(raw); err == nil {
				ctx = WithUserID(ctx, uid)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
