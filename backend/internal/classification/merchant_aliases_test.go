package classification_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

func TestAddAlias_Inserts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-add")
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	a, err := svc.AddAlias(ctx, wsID, m.ID, "COOP-4382 ZUR")
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	if a.MerchantID != m.ID {
		t.Errorf("MerchantID = %v, want %v", a.MerchantID, m.ID)
	}
	if a.WorkspaceID != wsID {
		t.Errorf("WorkspaceID = %v, want %v", a.WorkspaceID, wsID)
	}
	if a.RawPattern != "COOP-4382 ZUR" {
		t.Errorf("RawPattern = %q, want COOP-4382 ZUR", a.RawPattern)
	}
	if a.IsRegex {
		t.Error("IsRegex should be false in v1")
	}
	if a.ID == uuid.Nil {
		t.Error("alias ID should not be nil")
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestAddAlias_TrimsAndRejectsEmpty(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-trim")
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	_, err = svc.AddAlias(ctx, wsID, m.ID, "  ")
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want ValidationError for whitespace, got %v", err)
	}
	_, err = svc.AddAlias(ctx, wsID, m.ID, "")
	if !errors.As(err, &verr) {
		t.Errorf("want ValidationError for empty, got %v", err)
	}

	a, err := svc.AddAlias(ctx, wsID, m.ID, "  COOP  ")
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	if a.RawPattern != "COOP" {
		t.Errorf("RawPattern = %q, want COOP", a.RawPattern)
	}
}

func TestAddAlias_DuplicateInWorkspaceConflicts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-dup")
	m1, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant m1: %v", err)
	}
	m2, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant m2: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m1.ID, "FOO"); err != nil {
		t.Fatalf("first AddAlias: %v", err)
	}

	_, err = svc.AddAlias(ctx, wsID, m2.ID, "FOO")
	if err == nil {
		t.Fatal("expected conflict on second AddAlias with same raw_pattern")
	}
	// Local convention (mapWriteError in service.go) surfaces 23505
	// duplicate-key violations as ValidationError.
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want ValidationError for duplicate alias, got %T: %v", err, err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already mapped") {
		t.Errorf("expected message about 'already mapped', got %q", err.Error())
	}

	// Same merchant, same pattern — also rejected.
	_, err = svc.AddAlias(ctx, wsID, m1.ID, "FOO")
	if !errors.As(err, &verr) {
		t.Errorf("want ValidationError for re-adding same alias, got %v", err)
	}
}

func TestAddAlias_UnknownMerchantNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-unknown")

	_, err := svc.AddAlias(ctx, wsID, uuid.New(), "FOO")
	var nferr *httpx.NotFoundError
	if !errors.As(err, &nferr) {
		t.Errorf("want NotFoundError, got %v", err)
	}
}

func TestAddAlias_MerchantInOtherWorkspaceNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	ws1, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-cross-1")
	ws2, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-cross-2")
	m, err := svc.CreateMerchant(ctx, ws1, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	// Use ws2 with merchant from ws1 — must 404, not leak the merchant.
	_, err = svc.AddAlias(ctx, ws2, m.ID, "FOO")
	var nferr *httpx.NotFoundError
	if !errors.As(err, &nferr) {
		t.Errorf("want NotFoundError for cross-workspace merchant, got %v", err)
	}
}

func TestListAliases_ScopedToMerchant(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-list")
	m1, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant m1: %v", err)
	}
	m2, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant m2: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m1.ID, "COOP-A"); err != nil {
		t.Fatalf("AddAlias COOP-A: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m1.ID, "COOP-B"); err != nil {
		t.Fatalf("AddAlias COOP-B: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m2.ID, "MIGROS-X"); err != nil {
		t.Fatalf("AddAlias MIGROS-X: %v", err)
	}

	a1, err := svc.ListAliases(ctx, wsID, m1.ID)
	if err != nil {
		t.Fatalf("ListAliases m1: %v", err)
	}
	if len(a1) != 2 {
		t.Errorf("m1 alias count = %d, want 2", len(a1))
	}
	for _, a := range a1 {
		if a.MerchantID != m1.ID {
			t.Errorf("alias %v has merchant_id %v, want %v", a.ID, a.MerchantID, m1.ID)
		}
	}

	a2, err := svc.ListAliases(ctx, wsID, m2.ID)
	if err != nil {
		t.Fatalf("ListAliases m2: %v", err)
	}
	if len(a2) != 1 {
		t.Errorf("m2 alias count = %d, want 1", len(a2))
	}
	if len(a2) > 0 && a2[0].RawPattern != "MIGROS-X" {
		t.Errorf("m2 alias[0].RawPattern = %q, want MIGROS-X", a2[0].RawPattern)
	}
}

func TestListAliases_UnknownMerchantNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-list-unknown")

	_, err := svc.ListAliases(ctx, wsID, uuid.New())
	var nferr *httpx.NotFoundError
	if !errors.As(err, &nferr) {
		t.Errorf("want NotFoundError, got %v", err)
	}
}

func TestRemoveAlias_DeletesByID(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-remove")
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}
	a, err := svc.AddAlias(ctx, wsID, m.ID, "COOP-X")
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}

	if err := svc.RemoveAlias(ctx, wsID, m.ID, a.ID); err != nil {
		t.Fatalf("RemoveAlias: %v", err)
	}

	got, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 aliases after remove, got %d", len(got))
	}

	// Removing again returns 404.
	err = svc.RemoveAlias(ctx, wsID, m.ID, a.ID)
	var nferr *httpx.NotFoundError
	if !errors.As(err, &nferr) {
		t.Errorf("second remove should be 404, got %v", err)
	}
}

func TestRemoveAlias_WrongMerchantNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-alias-remove-wrong")
	m1, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"})
	if err != nil {
		t.Fatalf("CreateMerchant m1: %v", err)
	}
	m2, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant m2: %v", err)
	}
	a, err := svc.AddAlias(ctx, wsID, m1.ID, "COOP-X")
	if err != nil {
		t.Fatalf("AddAlias: %v", err)
	}

	// Right alias id, wrong merchant id — must 404.
	err = svc.RemoveAlias(ctx, wsID, m2.ID, a.ID)
	var nferr *httpx.NotFoundError
	if !errors.As(err, &nferr) {
		t.Errorf("want NotFoundError when merchant doesn't own alias, got %v", err)
	}

	// And the alias should still exist.
	got, err := svc.ListAliases(ctx, wsID, m1.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("alias should still exist, got %d aliases", len(got))
	}
}
