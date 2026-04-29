package bankimport

import (
	"context"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

// InsertImportableTxForTest exposes the internal insertImportableTx helper
// to integration tests in the bankimport_test package. Lets tests drive the
// new merchant-attachment code path with a hand-rolled []ParsedTransaction
// without standing up the full Apply/ApplyPlan parser scaffolding.
func (s *Service) InsertImportableTxForTest(ctx context.Context, q *dbq.Queries, workspaceID, accountID, batchID uuid.UUID, provider string, rows []ParsedTransaction) ([]uuid.UUID, error) {
	return s.insertImportableTx(ctx, q, workspaceID, accountID, batchID, provider, rows)
}
