package classification_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// TestUpdateMerchant_RenameCapturesAlias verifies that renaming a merchant
// captures the old canonical_name as an alias, so future imports of the old
// raw string still resolve to the (renamed) merchant.
func TestUpdateMerchant_RenameCapturesAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-alias")

	original := "coop-zrh-4567"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: original})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	newName := "Coop"
	got, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &newName})
	if err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}
	if got.CanonicalName != newName {
		t.Errorf("CanonicalName = %q, want %q", got.CanonicalName, newName)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("alias count = %d, want 1", len(aliases))
	}
	if aliases[0].RawPattern != original {
		t.Errorf("alias.RawPattern = %q, want %q", aliases[0].RawPattern, original)
	}

	// AttachByRaw with the old raw string should resolve to the renamed
	// merchant via the captured alias.
	resolved, err := svc.AttachByRaw(ctx, wsID, original)
	if err != nil {
		t.Fatalf("AttachByRaw: %v", err)
	}
	if resolved == nil {
		t.Fatal("AttachByRaw returned nil merchant")
	}
	if resolved.ID != m.ID {
		t.Errorf("AttachByRaw resolved to %v, want %v (renamed merchant)", resolved.ID, m.ID)
	}
	if resolved.CanonicalName != newName {
		t.Errorf("resolved.CanonicalName = %q, want %q", resolved.CanonicalName, newName)
	}
}

// TestUpdateMerchant_NoOpRenameDoesNotCaptureAlias verifies that PATCHing
// canonicalName with the same value does not produce an alias row.
func TestUpdateMerchant_NoOpRenameDoesNotCaptureAlias(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-noop")

	name := "Coop"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: name})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	same := "Coop"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &same}); err != nil {
		t.Fatalf("UpdateMerchant: %v", err)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("alias count = %d, want 0 (no-op rename should not capture alias)", len(aliases))
	}
}

// TestUpdateMerchant_RenameTwiceCapturesBothNames verifies multiple renames
// each capture their respective old name as a separate alias.
func TestUpdateMerchant_RenameTwiceCapturesBothNames(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-twice")

	a := "A"
	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: a})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}

	b := "B"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &b}); err != nil {
		t.Fatalf("UpdateMerchant A->B: %v", err)
	}
	c := "C"
	if _, err := svc.UpdateMerchant(ctx, wsID, m.ID, classification.MerchantPatchInput{CanonicalName: &c}); err != nil {
		t.Fatalf("UpdateMerchant B->C: %v", err)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("alias count = %d, want 2", len(aliases))
	}
	got := map[string]bool{}
	for _, al := range aliases {
		got[al.RawPattern] = true
	}
	if !got["A"] || !got["B"] {
		t.Errorf("aliases = %v, want both A and B captured", got)
	}
}

// TestUpdateMerchant_RenameCollisionWithActiveMerchant verifies that renaming
// a merchant to the canonical_name of another active merchant in the same
// workspace fails. Per the classification package convention (mapWriteError),
// the 23505 unique-violation surfaces as *httpx.ValidationError.
func TestUpdateMerchant_RenameCollisionWithActiveMerchant(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-collision")

	if _, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Coop"}); err != nil {
		t.Fatalf("CreateMerchant Coop: %v", err)
	}
	migros, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant Migros: %v", err)
	}

	conflict := "Coop"
	_, err = svc.UpdateMerchant(ctx, wsID, migros.ID, classification.MerchantPatchInput{CanonicalName: &conflict})
	if err == nil {
		t.Fatal("expected error renaming Migros to Coop, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError (per mapWriteError convention), got %T: %v", err, err)
	}

	// And no alias should have been captured (transaction rolled back).
	aliases, err := svc.ListAliases(ctx, wsID, migros.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("alias count = %d, want 0 (failed rename should not leave alias)", len(aliases))
	}
}

// TestUpdateMerchant_RenameTakesArchivedMerchantsName verifies the partial
// unique index allows renaming an active merchant to the canonical_name of
// an archived merchant.
func TestUpdateMerchant_RenameTakesArchivedMerchantsName(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merchant-rename-archived")

	old, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "OldCoop"})
	if err != nil {
		t.Fatalf("CreateMerchant OldCoop: %v", err)
	}
	if err := svc.ArchiveMerchant(ctx, wsID, old.ID); err != nil {
		t.Fatalf("ArchiveMerchant OldCoop: %v", err)
	}

	migros, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "Migros"})
	if err != nil {
		t.Fatalf("CreateMerchant Migros: %v", err)
	}

	target := "OldCoop"
	got, err := svc.UpdateMerchant(ctx, wsID, migros.ID, classification.MerchantPatchInput{CanonicalName: &target})
	if err != nil {
		t.Fatalf("UpdateMerchant Migros->OldCoop (archived): %v", err)
	}
	if got.CanonicalName != target {
		t.Errorf("CanonicalName = %q, want %q", got.CanonicalName, target)
	}

	aliases, err := svc.ListAliases(ctx, wsID, migros.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 1 {
		t.Fatalf("alias count = %d, want 1", len(aliases))
	}
	if aliases[0].RawPattern != "Migros" {
		t.Errorf("alias.RawPattern = %q, want Migros", aliases[0].RawPattern)
	}
}
