// Package httpx contains small HTTP helpers shared across feature packages:
// typed errors and JSON response writers. Authentication and workspace context
// live in backend/internal/auth.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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

// ConflictError maps to HTTP 409. Carries a code + optional details payload.
type ConflictError struct {
	Code    string
	Msg     string
	Details any
}

func (e *ConflictError) Error() string { return e.Msg }

// NewConflictError creates a ConflictError. Details is rendered into the
// response body's "details" field when non-nil.
func NewConflictError(code, msg string, details any) error {
	return &ConflictError{Code: code, Msg: msg, Details: details}
}

// WriteServiceError maps a service-layer error to the appropriate HTTP response.
func WriteServiceError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	var nferr *NotFoundError
	var cerr *ConflictError
	switch {
	case errors.As(err, &cerr):
		WriteJSON(w, http.StatusConflict, ErrorBody{Error: cerr.Msg, Code: cerr.Code, Details: cerr.Details})
	case errors.As(err, &verr):
		WriteError(w, http.StatusBadRequest, "validation_error", verr.Msg)
	case errors.As(err, &nferr):
		WriteError(w, http.StatusNotFound, "not_found", nferr.Error())
	default:
		// Unmapped error — log the details so debugging isn't a black hole.
		slog.Default().Warn("httpx.unmapped_service_error", "err", err)
		WriteError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
