package classification_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// readMerchantRowExists checks whether a merchant row still exists.
func readMerchantRowExists(t *testing.T, ctx context.Context, svc *classification.Service, wsID, id uuid.UUID) bool {
	t.Helper()
	_, err := svc.GetMerchant(ctx, wsID, id)
	if err == nil {
		return true
	}
	var nfe *httpx.NotFoundError
	if errors.As(err, &nfe) {
		return false
	}
	t.Fatalf("readMerchantRowExists: unexpected error: %v", err)
	return false
}

// readTxMerchantID returns a transaction's merchant_id (nil when NULL).
func readTxMerchantID(t *testing.T, ctx context.Context, svc *classification.Service, id uuid.UUID, wsID uuid.UUID) *uuid.UUID {
	t.Helper()
	// Use the pool indirectly by re-running raw SQL. The package's existing
	// test helpers don't expose a direct read of merchant_id, so we use the
	// pool from testdb.Open which is process-global.
	pool := testdb.Open(t)
	var got *uuid.UUID
	if err := pool.QueryRow(ctx,
		`select merchant_id from transactions where id = $1 and workspace_id = $2`, id, wsID,
	).Scan(&got); err != nil {
		t.Fatalf("readTxMerchantID: %v", err)
	}
	return got
}

// TestMergeMerchants_HappyPath: 2 transactions on source, 1 alias on source,
// merge into target with applyTargetDefault=false. Asserts the txns now point
// at target, the target has 2 aliases (source's pre-existing + source's
// canonical name), source row is deleted, counts are right.
func TestMergeMerchants_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-happy")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	snacks, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Snacks"})
	if err != nil {
		t.Fatalf("CreateCategory snacks: %v", err)
	}
	groceries, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory groceries: %v", err)
	}

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "S-canonical",
		DefaultCategoryID: &snacks.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "T-canonical",
		DefaultCategoryID: &groceries.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	if _, err := svc.AddAlias(ctx, wsID, source.ID, "S-alias-1"); err != nil {
		t.Fatalf("AddAlias source: %v", err)
	}

	t1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, &snacks.ID)
	t2 := insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, nil)

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID:           target.ID,
		ApplyTargetDefault: false,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}

	if res.MovedCount != 2 {
		t.Errorf("MovedCount = %d, want 2", res.MovedCount)
	}
	if res.CapturedAliasCount != 2 {
		t.Errorf("CapturedAliasCount = %d, want 2 (1 reparented + 1 canonical)", res.CapturedAliasCount)
	}
	if res.CascadedCount != 0 {
		t.Errorf("CascadedCount = %d, want 0 (applyTargetDefault=false)", res.CascadedCount)
	}
	if res.Target == nil || res.Target.ID != target.ID {
		t.Fatalf("Target id = %v, want %v", res.Target, target.ID)
	}

	if got := readTxMerchantID(t, ctx, svc, t1, wsID); got == nil || *got != target.ID {
		t.Errorf("t1.merchant_id = %v, want %v", got, target.ID)
	}
	if got := readTxMerchantID(t, ctx, svc, t2, wsID); got == nil || *got != target.ID {
		t.Errorf("t2.merchant_id = %v, want %v", got, target.ID)
	}

	if readMerchantRowExists(t, ctx, svc, wsID, source.ID) {
		t.Errorf("source merchant still exists, want deleted")
	}

	aliases, err := svc.ListAliases(ctx, wsID, target.ID)
	if err != nil {
		t.Fatalf("ListAliases target: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("target alias count = %d, want 2 (S-canonical + S-alias-1)", len(aliases))
	}
	patterns := map[string]bool{}
	for _, a := range aliases {
		patterns[a.RawPattern] = true
	}
	if !patterns["S-canonical"] || !patterns["S-alias-1"] {
		t.Errorf("aliases = %v, want both 'S-canonical' and 'S-alias-1'", patterns)
	}
}

// TestMergeMerchants_FillsBlankMetadata: source has logo_url, target's is
// null → target picks it up. Source has notes, target has different notes →
// target keeps its own (COALESCE prefers t.notes when not null).
func TestMergeMerchants_FillsBlankMetadata(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-fill-blanks")

	srcLogo := "https://example.com/source.png"
	srcNotes := "source notes"
	tgtNotes := "target notes"

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName: "S",
		LogoURL:       &srcLogo,
		Notes:         &srcNotes,
	})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName: "T",
		Notes:         &tgtNotes,
		// LogoURL deliberately nil.
	})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID: target.ID,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}

	if res.Target.LogoURL == nil || *res.Target.LogoURL != srcLogo {
		t.Errorf("target.LogoURL = %v, want %q (filled from source)", res.Target.LogoURL, srcLogo)
	}
	if res.Target.Notes == nil || *res.Target.Notes != tgtNotes {
		t.Errorf("target.Notes = %v, want %q (preserved, COALESCE keeps t.notes when not null)", res.Target.Notes, tgtNotes)
	}
}

// TestMergeMerchants_DefaultCategoryNotFilled: target has no default,
// source does. After merge target.default is still null (target wins on
// category policy).
func TestMergeMerchants_DefaultCategoryNotFilled(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-default-not-filled")

	cat, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "X"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "S",
		DefaultCategoryID: &cat.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName: "T",
	})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID: target.ID,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}
	if res.Target.DefaultCategoryID != nil {
		t.Errorf("target.DefaultCategoryID = %v, want nil (target wins on category policy)", res.Target.DefaultCategoryID)
	}
}

// TestMergeMerchants_ApplyTargetDefaultCascadesOnlyMatching: with
// applyTargetDefault=true, only just-moved transactions whose category equals
// source's old default get re-categorised; manual overrides preserved.
func TestMergeMerchants_ApplyTargetDefaultCascadesOnlyMatching(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-cascade")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	oldDef, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "OldDef"})
	if err != nil {
		t.Fatalf("CreateCategory OldDef: %v", err)
	}
	manual, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Manual"})
	if err != nil {
		t.Fatalf("CreateCategory Manual: %v", err)
	}
	newDef, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "NewDef"})
	if err != nil {
		t.Fatalf("CreateCategory NewDef: %v", err)
	}

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "S",
		DefaultCategoryID: &oldDef.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "T",
		DefaultCategoryID: &newDef.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	// Pre-existing transaction on target with category=newDef. MUST NOT be
	// touched by the cascade (cascade scope is just-moved IDs only).
	tPre := insertTxForCascade(t, ctx, pool, wsID, accountID, &target.ID, &newDef.ID)
	// Source transactions:
	t1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, &oldDef.ID) // matches old default → cascades.
	t2 := insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, &manual.ID) // manual override → unchanged.

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID:           target.ID,
		ApplyTargetDefault: true,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}
	if res.MovedCount != 2 {
		t.Errorf("MovedCount = %d, want 2", res.MovedCount)
	}
	if res.CascadedCount != 1 {
		t.Errorf("CascadedCount = %d, want 1", res.CascadedCount)
	}

	if got := readTxCategoryID(t, ctx, pool, t1); got == nil || *got != newDef.ID {
		t.Errorf("t1 category_id = %v, want %v (cascaded)", got, newDef.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, t2); got == nil || *got != manual.ID {
		t.Errorf("t2 category_id = %v, want %v (manual override preserved)", got, manual.ID)
	}
	if got := readTxCategoryID(t, ctx, pool, tPre); got == nil || *got != newDef.ID {
		t.Errorf("tPre category_id = %v, want %v (target's pre-existing tx untouched)", got, newDef.ID)
	}
}

// TestMergeMerchants_OverlappingAliasesNoConflict: both source and target
// have an alias "FOO". Reparent must not error; final state has "FOO" on
// target exactly once.
func TestMergeMerchants_OverlappingAliasesNoConflict(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-alias-overlap")

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "S"})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "T"})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	if _, err := svc.AddAlias(ctx, wsID, source.ID, "FOO"); err != nil {
		t.Fatalf("AddAlias source: %v", err)
	}
	// Note: alias "FOO" is workspace-unique, so we can't have it on both
	// merchants at once. Stage instead: target had its own alias first, then
	// we attempt the same on source — but the unique constraint blocks that.
	// Instead, simulate "overlap" by putting "FOO" on target and on source's
	// canonical-name capture path: source.canonical_name = "FOO"-equivalent
	// pattern. Easier: keep "FOO" on source only, and put "FOO" on target.
	// But unique violates. So the realistic overlap case is the
	// canonical-name capture: source.CanonicalName already exists as an alias
	// on target. Let's use that.

	// Reset: drop the source alias, recreate the test using canonical-name
	// overlap (the realistic collision path during merge).
	aliases, err := svc.ListAliases(ctx, wsID, source.ID)
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	for _, a := range aliases {
		if err := svc.RemoveAlias(ctx, wsID, source.ID, a.ID); err != nil {
			t.Fatalf("RemoveAlias: %v", err)
		}
	}
	// Pre-seed target with an alias matching source's canonical name.
	if _, err := svc.AddAlias(ctx, wsID, target.ID, source.CanonicalName); err != nil {
		t.Fatalf("AddAlias target with source canonical: %v", err)
	}

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID: target.ID,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}
	// Captured alias count: 0 reparented + 0 from canonical (it collided).
	if res.CapturedAliasCount != 0 {
		t.Errorf("CapturedAliasCount = %d, want 0 (canonical collided with existing target alias)", res.CapturedAliasCount)
	}

	// Target should have exactly one alias matching source's canonical name.
	tgtAliases, err := svc.ListAliases(ctx, wsID, target.ID)
	if err != nil {
		t.Fatalf("ListAliases target: %v", err)
	}
	count := 0
	for _, a := range tgtAliases {
		if a.RawPattern == source.CanonicalName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("target has %d aliases matching source canonical, want 1", count)
	}
}

// TestMergeMerchants_SourceEqualsTargetReturnsValidationError: passing the
// same id for source and target is rejected before any DB mutation.
func TestMergeMerchants_SourceEqualsTargetReturnsValidationError(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-self")

	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "M"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}
	_, err = svc.MergeMerchants(ctx, wsID, m.ID, classification.MergeMerchantsInput{TargetID: m.ID})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError, got %T: %v", err, err)
	}

	// And the merchant is still there, untouched.
	if !readMerchantRowExists(t, ctx, svc, wsID, m.ID) {
		t.Errorf("merchant should still exist after rejected self-merge")
	}
}

// TestMergeMerchants_TargetArchivedRejected: archiving target before merge
// must produce ValidationError, no mutations.
func TestMergeMerchants_TargetArchivedRejected(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-target-archived")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "S"})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "T"})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}
	if err := svc.ArchiveMerchant(ctx, wsID, target.ID); err != nil {
		t.Fatalf("ArchiveMerchant: %v", err)
	}

	tx1 := insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, nil)

	_, err = svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID: target.ID,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError, got %T: %v", err, err)
	}

	// Source still exists; its txn still points at source.
	if !readMerchantRowExists(t, ctx, svc, wsID, source.ID) {
		t.Errorf("source should still exist after rejected merge into archived target")
	}
	if got := readTxMerchantID(t, ctx, svc, tx1, wsID); got == nil || *got != source.ID {
		t.Errorf("tx1.merchant_id = %v, want %v (unchanged)", got, source.ID)
	}
}

// TestMergeMerchants_SourceMissingReturnsNotFound: random source UUID.
func TestMergeMerchants_SourceMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-source-missing")

	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "T"})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}
	missing := uuid.New()
	_, err = svc.MergeMerchants(ctx, wsID, missing, classification.MergeMerchantsInput{TargetID: target.ID})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var nfe *httpx.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("want *httpx.NotFoundError, got %T: %v", err, err)
	}
}

// TestPreviewMerge_MatchesActualMerge: preview returns the same counts as
// the real merge produces. Setup mirrors the cascade test: source with 2
// transactions (1 matching old default, 1 manual override), 1 alias, default
// = snacks; target with default = groceries, no logo (source has one). The
// preview is computed first, then the merge is executed, and every count
// must match field-for-field.
func TestPreviewMerge_MatchesActualMerge(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-preview-matches")
	accountID := seedAccountForCascade(t, ctx, pool, wsID, "CHF")

	snacks, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Snacks"})
	if err != nil {
		t.Fatalf("CreateCategory snacks: %v", err)
	}
	groceries, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Groceries"})
	if err != nil {
		t.Fatalf("CreateCategory groceries: %v", err)
	}
	manual, err := svc.CreateCategory(ctx, wsID, classification.CategoryCreateInput{Name: "Manual"})
	if err != nil {
		t.Fatalf("CreateCategory manual: %v", err)
	}

	srcLogo := "https://example.com/source.png"
	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "S-canonical",
		DefaultCategoryID: &snacks.ID,
		LogoURL:           &srcLogo,
	})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{
		CanonicalName:     "T-canonical",
		DefaultCategoryID: &groceries.ID,
	})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}

	if _, err := svc.AddAlias(ctx, wsID, source.ID, "S-alias-1"); err != nil {
		t.Fatalf("AddAlias source: %v", err)
	}

	insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, &snacks.ID) // matches source default → cascades.
	insertTxForCascade(t, ctx, pool, wsID, accountID, &source.ID, &manual.ID) // manual override.

	preview, err := svc.PreviewMerge(ctx, wsID, source.ID, target.ID)
	if err != nil {
		t.Fatalf("PreviewMerge: %v", err)
	}
	if preview.SourceCanonicalName != "S-canonical" {
		t.Errorf("SourceCanonicalName = %q, want %q", preview.SourceCanonicalName, "S-canonical")
	}
	if preview.TargetCanonicalName != "T-canonical" {
		t.Errorf("TargetCanonicalName = %q, want %q", preview.TargetCanonicalName, "T-canonical")
	}
	if len(preview.BlankFillFields) != 1 || preview.BlankFillFields[0] != "logoUrl" {
		t.Errorf("BlankFillFields = %v, want [logoUrl]", preview.BlankFillFields)
	}

	res, err := svc.MergeMerchants(ctx, wsID, source.ID, classification.MergeMerchantsInput{
		TargetID:           target.ID,
		ApplyTargetDefault: true,
	})
	if err != nil {
		t.Fatalf("MergeMerchants: %v", err)
	}

	if preview.MovedCount != res.MovedCount {
		t.Errorf("preview.MovedCount = %d, merge.MovedCount = %d (must match)", preview.MovedCount, res.MovedCount)
	}
	if preview.CapturedAliasCount != res.CapturedAliasCount {
		t.Errorf("preview.CapturedAliasCount = %d, merge.CapturedAliasCount = %d (must match)", preview.CapturedAliasCount, res.CapturedAliasCount)
	}
	if preview.CascadedCountIfApplied != res.CascadedCount {
		t.Errorf("preview.CascadedCountIfApplied = %d, merge.CascadedCount = %d (must match)", preview.CascadedCountIfApplied, res.CascadedCount)
	}
}

// TestPreviewMerge_SourceEqualsTargetReturnsValidationError: same id for
// source and target is rejected before any DB read.
func TestPreviewMerge_SourceEqualsTargetReturnsValidationError(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-preview-self")

	m, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "M"})
	if err != nil {
		t.Fatalf("CreateMerchant: %v", err)
	}
	_, err = svc.PreviewMerge(ctx, wsID, m.ID, m.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError, got %T: %v", err, err)
	}
}

// TestPreviewMerge_TargetArchivedRejected: archived target produces a
// ValidationError, mirroring MergeMerchants.
func TestPreviewMerge_TargetArchivedRejected(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-preview-target-archived")

	source, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "S"})
	if err != nil {
		t.Fatalf("CreateMerchant source: %v", err)
	}
	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "T"})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}
	if err := svc.ArchiveMerchant(ctx, wsID, target.ID); err != nil {
		t.Fatalf("ArchiveMerchant: %v", err)
	}

	_, err = svc.PreviewMerge(ctx, wsID, source.ID, target.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("want *httpx.ValidationError, got %T: %v", err, err)
	}
}

// TestPreviewMerge_SourceMissingReturnsNotFound: random source UUID returns
// NotFound, no panic.
func TestPreviewMerge_SourceMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-preview-source-missing")

	target, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "T"})
	if err != nil {
		t.Fatalf("CreateMerchant target: %v", err)
	}
	missing := uuid.New()
	_, err = svc.PreviewMerge(ctx, wsID, missing, target.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var nfe *httpx.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("want *httpx.NotFoundError, got %T: %v", err, err)
	}
}

// TestMergeMerchants_PreservesPreviouslyMergedAliases: build chain:
// M1 (alias "A") merge into M2 (alias "B"); then M2 merge into M3.
// After: M3 has aliases A, B, M1's name, and M2's name. AttachByRaw "A"
// resolves to M3.
func TestMergeMerchants_PreservesPreviouslyMergedAliases(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	svc := classification.NewService(pool)
	wsID, _ := testdb.CreateTestWorkspace(t, pool, "ws-merge-chain")

	m1, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "M1-canonical"})
	if err != nil {
		t.Fatalf("CreateMerchant m1: %v", err)
	}
	m2, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "M2-canonical"})
	if err != nil {
		t.Fatalf("CreateMerchant m2: %v", err)
	}
	m3, err := svc.CreateMerchant(ctx, wsID, classification.MerchantCreateInput{CanonicalName: "M3-canonical"})
	if err != nil {
		t.Fatalf("CreateMerchant m3: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m1.ID, "A"); err != nil {
		t.Fatalf("AddAlias m1 A: %v", err)
	}
	if _, err := svc.AddAlias(ctx, wsID, m2.ID, "B"); err != nil {
		t.Fatalf("AddAlias m2 B: %v", err)
	}

	// First merge: M1 → M2. Now M2 has: A, B, M1-canonical.
	if _, err := svc.MergeMerchants(ctx, wsID, m1.ID, classification.MergeMerchantsInput{
		TargetID: m2.ID,
	}); err != nil {
		t.Fatalf("MergeMerchants m1->m2: %v", err)
	}

	// Second merge: M2 → M3. Now M3 should have: A, B, M1-canonical, M2-canonical.
	if _, err := svc.MergeMerchants(ctx, wsID, m2.ID, classification.MergeMerchantsInput{
		TargetID: m3.ID,
	}); err != nil {
		t.Fatalf("MergeMerchants m2->m3: %v", err)
	}

	aliases, err := svc.ListAliases(ctx, wsID, m3.ID)
	if err != nil {
		t.Fatalf("ListAliases m3: %v", err)
	}
	got := map[string]bool{}
	for _, a := range aliases {
		got[a.RawPattern] = true
	}
	for _, want := range []string{"A", "B", "M1-canonical", "M2-canonical"} {
		if !got[want] {
			t.Errorf("missing alias %q on m3; got = %v", want, got)
		}
	}

	// AttachByRaw "A" should resolve to m3 via the carried-along alias chain.
	resolved, err := svc.AttachByRaw(ctx, wsID, "A")
	if err != nil {
		t.Fatalf("AttachByRaw A: %v", err)
	}
	if resolved == nil || resolved.ID != m3.ID {
		t.Errorf("AttachByRaw 'A' resolved to %v, want %v (m3)", resolved, m3.ID)
	}
}
