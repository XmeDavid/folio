package classification

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// MergeMerchantsInput is the validated input to MergeMerchants. Source is
// passed positionally on the method (matches the URL shape that Task 5.3 will
// add: POST /merchants/:sourceId/merge). ApplyTargetDefault, when true, also
// re-categorises the just-moved transactions whose category equals the
// source's old default to the target's default (when the target has one).
type MergeMerchantsInput struct {
	TargetID           uuid.UUID
	ApplyTargetDefault bool
}

// MergeMerchantsResult is the return shape of MergeMerchants. Counts are
// reported so the API/UI can surface meaningful numbers ("X transactions
// moved, Y re-categorised, Z aliases captured") to the user.
type MergeMerchantsResult struct {
	Target             *Merchant `json:"target"`
	MovedCount         int       `json:"movedCount"`
	CascadedCount      int       `json:"cascadedCount"`
	CapturedAliasCount int       `json:"capturedAliasCount"`
}

// MergeMerchants atomically folds a source merchant into a target. It:
//
//  1. Locks both rows FOR UPDATE in deterministic id order (deadlock safety).
//  2. Reparents the source's existing aliases to the target (collisions are
//     dropped silently because the new alias would have pointed to target
//     anyway).
//  3. Captures the source's canonical_name as an alias of the target so future
//     imports of that raw string still resolve to the target merchant.
//  4. Reassigns every transaction that pointed to source so it now points to
//     target, capturing the moved IDs.
//  5. Fills blanks on the target metadata (logo_url, industry, website, notes)
//     using the source's values via COALESCE. default_category_id is NOT
//     filled — the target wins on category policy.
//  6. Optionally cascades the target's default_category_id onto the
//     just-moved transactions whose category was equal to the source's old
//     default (manual overrides are preserved). Scoped to the moved IDs only,
//     so target's pre-existing transactions are never touched.
//  7. Deletes the source merchant row. Order matters here: the
//     merchant_aliases.merchant_id FK is ON DELETE CASCADE, so the source's
//     aliases must be reparented BEFORE the source row is deleted, otherwise
//     the cascade would wipe them.
//
// All steps run inside a single DB transaction. Any error → rollback.
//
// Source can be in any state, including archived (the user might want to
// merge an old archived noise merchant into a real one to reclaim its name).
// Target must NOT be archived — merging into an archived merchant would
// effectively bury active transactions inside an archived row.
func (s *Service) MergeMerchants(ctx context.Context, workspaceID, sourceID uuid.UUID, in MergeMerchantsInput) (*MergeMerchantsResult, error) {
	if sourceID == in.TargetID {
		return nil, httpx.NewValidationError("source and target merchants must differ")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin merge: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock both rows in deterministic id order to avoid deadlocks under
	// concurrent merges (e.g. user-A merges S→T while user-B merges T→S).
	first, second := sourceID, in.TargetID
	if first.String() > second.String() {
		first, second = second, first
	}
	lockRows, err := tx.Query(ctx,
		`select id from merchants where workspace_id = $1 and id = any($2) order by id for update`,
		workspaceID, []uuid.UUID{first, second},
	)
	if err != nil {
		return nil, fmt.Errorf("lock merchants: %w", err)
	}
	lockRows.Close()

	// Read source. Source can be archived; we still allow the merge.
	var src Merchant
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id = $1 and id = $2`,
		workspaceID, sourceID,
	), &src); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("merchant")
		}
		return nil, fmt.Errorf("read source merchant: %w", err)
	}

	// Read target. Target must not be archived.
	var tgt Merchant
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id = $1 and id = $2`,
		workspaceID, in.TargetID,
	), &tgt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("merchant")
		}
		return nil, fmt.Errorf("read target merchant: %w", err)
	}
	if tgt.ArchivedAt != nil {
		return nil, httpx.NewValidationError("merge target is archived")
	}

	// 1. Reparent the source's existing aliases. The unique constraint is
	// on (workspace_id, raw_pattern) — independent of merchant_id — so a
	// raw_pattern that already exists on target collides at the workspace
	// level. We do this in two steps:
	//   a) DELETE source aliases whose raw_pattern is already mapped on
	//      target. The colliding alias on target already resolves to target,
	//      so dropping the source-side row is correct.
	//   b) UPDATE the surviving source aliases to point to target. This
	//      preserves their id and created_at, which AddAlias-style auditing
	//      may want to keep.
	if _, err := tx.Exec(ctx, `
		delete from merchant_aliases sa
		using merchant_aliases ta
		where sa.workspace_id = $1
		  and sa.merchant_id = $3
		  and ta.workspace_id = $1
		  and ta.merchant_id = $2
		  and ta.raw_pattern = sa.raw_pattern
	`, workspaceID, in.TargetID, sourceID); err != nil {
		return nil, fmt.Errorf("drop colliding source aliases: %w", err)
	}
	aliasReparentTag, err := tx.Exec(ctx, `
		update merchant_aliases
		set merchant_id = $2
		where workspace_id = $1 and merchant_id = $3
	`, workspaceID, in.TargetID, sourceID)
	if err != nil {
		return nil, fmt.Errorf("reparent aliases: %w", err)
	}

	// 2. Capture the source canonical name as an alias of target. ON CONFLICT
	// DO NOTHING in case target already had an alias with that exact pattern
	// (e.g. earlier merge into target captured the same name).
	canonicalAliasTag, err := tx.Exec(ctx, `
		insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern)
		values ($1, $2, $3, $4)
		on conflict (workspace_id, raw_pattern) do nothing
	`, uuidx.New(), workspaceID, in.TargetID, src.CanonicalName)
	if err != nil {
		return nil, fmt.Errorf("capture canonical alias: %w", err)
	}

	capturedAliasCount := int(aliasReparentTag.RowsAffected() + canonicalAliasTag.RowsAffected())

	// 3. Move transactions, capturing the moved IDs for the optional cascade.
	txRows, err := tx.Query(ctx, `
		update transactions
		set merchant_id = $2
		where workspace_id = $1 and merchant_id = $3
		returning id
	`, workspaceID, in.TargetID, sourceID)
	if err != nil {
		return nil, fmt.Errorf("move transactions: %w", err)
	}
	var movedIDs []uuid.UUID
	for txRows.Next() {
		var id uuid.UUID
		if err := txRows.Scan(&id); err != nil {
			txRows.Close()
			return nil, fmt.Errorf("scan moved id: %w", err)
		}
		movedIDs = append(movedIDs, id)
	}
	txRows.Close()
	if err := txRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate moved ids: %w", err)
	}

	// 4. Fill blanks on target metadata (NOT default_category_id — target
	// wins on category policy). COALESCE keeps the target's value when set,
	// pulling from source only where target was null.
	if _, err := tx.Exec(ctx, `
		update merchants t set
			logo_url = coalesce(t.logo_url, s.logo_url),
			industry = coalesce(t.industry, s.industry),
			website  = coalesce(t.website,  s.website),
			notes    = coalesce(t.notes,    s.notes)
		from merchants s
		where t.workspace_id = $1 and t.id = $2 and s.id = $3
	`, workspaceID, in.TargetID, sourceID); err != nil {
		return nil, fmt.Errorf("fill blanks on target: %w", err)
	}

	// 5. Optional cascade. Only the just-moved transactions are eligible —
	// target's pre-existing transactions are never touched. Manual overrides
	// (rows whose category was neither null nor source's old default) are
	// preserved. IS NOT DISTINCT FROM correctly handles a null
	// source-old-default (filling null categories on first set).
	cascadedCount := 0
	if in.ApplyTargetDefault && len(movedIDs) > 0 && tgt.DefaultCategoryID != nil {
		tag, err := tx.Exec(ctx, `
			update transactions
			set category_id = $2
			where id = any($1)
			  and category_id is not distinct from $3
		`, movedIDs, *tgt.DefaultCategoryID, src.DefaultCategoryID)
		if err != nil {
			return nil, fmt.Errorf("cascade target default: %w", err)
		}
		cascadedCount = int(tag.RowsAffected())
	}

	// 6. Delete the source row. Must come AFTER alias reparent + transaction
	// move because the merchant_aliases FK is ON DELETE CASCADE — deleting
	// source first would wipe its aliases before we got to reparent them.
	if _, err := tx.Exec(ctx,
		`delete from merchants where workspace_id = $1 and id = $2`,
		workspaceID, sourceID,
	); err != nil {
		return nil, fmt.Errorf("delete source merchant: %w", err)
	}

	// 7. Re-read target post-merge so the response reflects any filled blanks
	// and the bumped updated_at.
	var finalTgt Merchant
	if err := scanMerchant(tx.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id = $1 and id = $2`,
		workspaceID, in.TargetID,
	), &finalTgt); err != nil {
		return nil, fmt.Errorf("re-read target: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit merge: %w", err)
	}

	return &MergeMerchantsResult{
		Target:             &finalTgt,
		MovedCount:         len(movedIDs),
		CascadedCount:      cascadedCount,
		CapturedAliasCount: capturedAliasCount,
	}, nil
}
