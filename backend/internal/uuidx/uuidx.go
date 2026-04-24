// Package uuidx provides a thin wrapper around github.com/google/uuid for
// UUIDv7 generation. IDs are generated app-side (not DB-side) so we can include
// them in transactions with deterministic ordering guarantees.
package uuidx

import "github.com/google/uuid"

// New returns a new UUIDv7. Panics only on an unrecoverable entropy failure,
// which on modern Linux/macOS effectively cannot happen.
func New() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic("uuidx: NewV7 failed: " + err.Error())
	}
	return id
}
