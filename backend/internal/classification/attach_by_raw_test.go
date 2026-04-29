package classification_test

import (
	"context"
	"testing"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestAttachByRaw_EmptyReturnsNil(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-attach-empty")

	got, err := svc.AttachByRaw(ctx, wsID, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != nil {
		t.Errorf("want nil for empty raw, got %v", got)
	}

	got, err = svc.AttachByRaw(ctx, wsID, "    ")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != nil {
		t.Errorf("want nil for whitespace raw, got %v", got)
	}
}

func TestAttachByRaw_CreatesOnFirstSight(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-attach-create")

	got, err := svc.AttachByRaw(ctx, wsID, "COOP-4382 ZUR")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got == nil {
		t.Fatal("want merchant, got nil")
	}
	if got.CanonicalName != "COOP-4382 ZUR" {
		t.Errorf("CanonicalName = %q, want COOP-4382 ZUR", got.CanonicalName)
	}

	again, err := svc.AttachByRaw(ctx, wsID, "COOP-4382 ZUR")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if again.ID != got.ID {
		t.Errorf("second call returned different merchant: %v vs %v", again.ID, got.ID)
	}
}

func TestAttachByRaw_TrimsWhitespace(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-attach-trim")

	a, err := svc.AttachByRaw(ctx, wsID, "  Coop  ")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a.CanonicalName != "Coop" {
		t.Errorf("CanonicalName = %q, want Coop", a.CanonicalName)
	}
	b, err := svc.AttachByRaw(ctx, wsID, "Coop")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if b.ID != a.ID {
		t.Error("trimmed raw should resolve to the same merchant")
	}
}

func TestAttachByRaw_ArchivedIgnored(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-attach-archived")

	first, err := svc.AttachByRaw(ctx, wsID, "Old Coop")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := svc.ArchiveMerchant(ctx, wsID, first.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	second, err := svc.AttachByRaw(ctx, wsID, "Old Coop")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if second.ID == first.ID {
		t.Error("archived match must not be reused; want a fresh merchant row")
	}
}

func TestAttachByRaw_ResolvesViaAlias(t *testing.T) {
	t.Skip("alias resolution covered in Task 3.1 once AddAlias exists")
}
