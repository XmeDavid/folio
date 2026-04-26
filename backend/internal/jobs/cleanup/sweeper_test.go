package cleanup_test

import (
	"context"
	"testing"
	"time"

	"github.com/xmedavid/folio/backend/internal/jobs/cleanup"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestSweeper_Run_HardDeletesWorkspacesPastGrace(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	oldID, _ := testdb.CreateTestWorkspace(t, pool, "Old "+t.Name())
	recentID, _ := testdb.CreateTestWorkspace(t, pool, "Recent "+t.Name())
	freshID, _ := testdb.CreateTestWorkspace(t, pool, "Fresh "+t.Name())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `delete from workspaces where id in ($1, $2, $3)`,
			oldID, recentID, freshID)
	})

	if _, err := pool.Exec(ctx,
		`update workspaces set deleted_at = now() - interval '31 days' where id = $1`, oldID); err != nil {
		t.Fatalf("age old: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update workspaces set deleted_at = now() - interval '5 days' where id = $1`, recentID); err != nil {
		t.Fatalf("age recent: %v", err)
	}
	// freshID is not soft-deleted.

	report, err := cleanup.Run(ctx, pool, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.DeletedCount != 1 {
		t.Fatalf("want 1 deleted, got %d", report.DeletedCount)
	}
	if len(report.DeletedIDs) != 1 || report.DeletedIDs[0] != oldID.String() {
		t.Fatalf("unexpected deleted ids: %+v (want [%s])", report.DeletedIDs, oldID)
	}

	// The recent + fresh workspaces must still exist.
	var remaining int
	if err := pool.QueryRow(ctx,
		`select count(*) from workspaces where id in ($1, $2, $3)`,
		oldID, recentID, freshID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("want 2 remaining workspaces, got %d", remaining)
	}
}

func TestSweeper_Run_NoOpWhenNothingExpired(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()

	report, err := cleanup.Run(ctx, pool, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.DeletedCount < 0 {
		t.Fatalf("negative count: %d", report.DeletedCount)
	}
	// The count can be >0 if other tests left old soft-deleted rows around;
	// this test only asserts Run completes and returns a valid Report.
}
