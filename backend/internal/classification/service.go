// Package classification owns the categories, merchants, and tags aggregates
// plus the transaction_tags join operations. It also defines the
// classification-shaped filters that the transactions listing consumes
// (e.g. the uncategorized queue).
//
// tenant_id always comes from request context. IDs are generated app-side
// (UUIDv7) via internal/uuidx. Deletes on categories/merchants/tags archive
// rather than hard-delete.
package classification

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// Service exposes the CRUD + archive operations for categories, merchants,
// tags, and transaction-tag assignments.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService returns a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// mapWriteError translates Postgres errors into clean ValidationError /
// NotFoundError where practical. Unique-violation (duplicate name, duplicate
// (parent, name)) and foreign-key-violation (parent / default category does
// not belong to tenant) are the two cases worth rewriting; everything else
// falls through as a generic internal error.
func mapWriteError(resource string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return httpx.NewValidationError(fmt.Sprintf("%s with this name already exists", resource))
		case "23503": // foreign_key_violation
			return httpx.NewValidationError("referenced entity does not exist for this tenant")
		case "23514": // check_violation
			return httpx.NewValidationError(pgErr.Message)
		case "P0001": // raise_exception from triggers
			return httpx.NewValidationError(pgErr.Message)
		}
	}
	return fmt.Errorf("%s write: %w", resource, err)
}
