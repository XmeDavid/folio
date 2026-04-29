package bankimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// decimalToNumeric converts a decimal.Decimal to pgtype.Numeric for sqlc params.
func decimalToNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// stringToNumeric converts a decimal string to pgtype.Numeric for sqlc params.
func stringToNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(s)
	return n
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

func (s *Service) Preview(ctx context.Context, workspaceID uuid.UUID, fileName string, r io.Reader, accountID *uuid.UUID) (*Preview, error) {
	parsed, fileHash, token, err := parseUpload(fileName, r)
	if err != nil {
		return nil, err
	}
	return s.buildPreview(ctx, workspaceID, parsed, fileName, fileHash, token, accountID)
}

func (s *Service) Apply(ctx context.Context, workspaceID, accountID, userID uuid.UUID, token, currency string) (*ApplyResult, error) {
	payload, err := parseToken(token)
	if err != nil {
		return nil, err
	}
	contentBytes, err := decodeContent(payload.Content)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseBytes(contentBytes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(currency) != "" {
		parsed = parsedForCurrency(parsed, strings.ToUpper(strings.TrimSpace(currency)))
	}
	if len(parsed.Transactions) == 0 {
		return nil, httpx.NewValidationError("file contains no importable transactions")
	}
	accountCurrency, err := s.accountCurrency(ctx, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	for _, row := range parsed.Transactions {
		if row.Currency != accountCurrency {
			return nil, httpx.NewValidationError(fmt.Sprintf("file currency %s does not match account currency %s", row.Currency, accountCurrency))
		}
	}

	existing, err := s.loadExisting(ctx, workspaceID, accountID, parsed)
	if err != nil {
		return nil, err
	}
	classified := classify(parsed, existing)
	taken, err := s.workspaceExternalIDs(ctx, workspaceID, parsed.Profile)
	if err != nil {
		return nil, err
	}
	filterByWorkspaceExternalIDs(&classified, taken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batchID := uuidx.New()
	summary, _ := json.Marshal(map[string]any{
		"profile":    parsed.Profile,
		"fileHash":   payload.FileHash,
		"fileName":   payload.FileName,
		"duplicates": len(classified.duplicates),
		"conflicts":  len(classified.conflicts),
		"importable": len(classified.importable),
	})
	q := dbq.New(tx)
	if err := q.InsertImportBatch(ctx, dbq.InsertImportBatchParams{
		ID:              batchID,
		WorkspaceID:     workspaceID,
		FileName:        &payload.FileName,
		FileHash:        &payload.FileHash,
		Summary:         summary,
		CreatedByUserID: &userID,
		StartedAt:       s.now(),
	}); err != nil {
		return nil, fmt.Errorf("insert import batch: %w", err)
	}

	inserted, err := s.insertImportableTx(ctx, q, workspaceID, accountID, batchID, incomingProvider(parsed.Profile), classified.importable)
	if err != nil {
		return nil, err
	}

	if parsed.DateFrom != nil && parsed.DateTo != nil {
		if err := s.retireMMFSummaries(ctx, q, workspaceID, accountID, *parsed.DateFrom, *parsed.DateTo); err != nil {
			return nil, err
		}
	}
	if parsed.DateFrom != nil {
		if err := s.backfillOpeningIfEarlier(ctx, q, workspaceID, accountID, *parsed.DateFrom); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return &ApplyResult{
		BatchID:        batchID,
		InsertedCount:  len(inserted),
		DuplicateCount: len(classified.duplicates),
		ConflictCount:  len(classified.conflicts),
		TransactionIDs: inserted,
		Conflicts:      classified.conflicts,
	}, nil
}

func (s *Service) ApplyPlan(ctx context.Context, workspaceID, userID uuid.UUID, in ApplyPlanInput) (*ApplyResult, error) {
	if strings.TrimSpace(in.FileToken) == "" {
		return nil, httpx.NewValidationError("fileToken is required")
	}
	if len(in.Groups) == 0 {
		return nil, httpx.NewValidationError("at least one import group is required")
	}
	payload, err := parseToken(in.FileToken)
	if err != nil {
		return nil, err
	}
	contentBytes, err := decodeContent(payload.Content)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseBytes(contentBytes)
	if err != nil {
		return nil, err
	}

	type plannedGroup struct {
		in         ApplyPlanGroup
		parsed     ParsedFile
		accountID  uuid.UUID
		classified classifiedRows
	}
	planned := make([]plannedGroup, 0, len(in.Groups))
	for _, group := range in.Groups {
		cur := strings.ToUpper(strings.TrimSpace(group.Currency))
		if cur == "" {
			return nil, httpx.NewValidationError("group currency is required")
		}
		sub := parsedForGroup(parsed, cur, group.SourceKey)
		if len(sub.Transactions) == 0 {
			if group.SourceKey != "" {
				return nil, httpx.NewValidationError(fmt.Sprintf("file contains no %s transactions for %s", cur, group.SourceKey))
			}
			return nil, httpx.NewValidationError(fmt.Sprintf("file contains no %s transactions", cur))
		}

		action := strings.TrimSpace(group.Action)
		if action == "" {
			action = "create_account"
		}
		var accountID uuid.UUID
		switch action {
		case "import_to_account":
			if group.AccountID == nil {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s group requires an account", cur))
			}
			accountCurrency, err := s.accountCurrency(ctx, workspaceID, *group.AccountID)
			if err != nil {
				return nil, err
			}
			if accountCurrency != cur {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s group cannot import into %s account", cur, accountCurrency))
			}
			accountID = *group.AccountID
		case "create_account":
			if strings.TrimSpace(group.Name) == "" {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s group requires an account name", cur))
			}
			if strings.TrimSpace(group.Kind) == "" {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s group requires an account type", cur))
			}
			if _, err := decimal.NewFromString(strings.TrimSpace(group.OpeningBalance)); err != nil {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s opening balance must be a decimal string", cur))
			}
			if _, err := time.Parse(dateOnly, group.OpenDate); err != nil {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s open date must be YYYY-MM-DD", cur))
			}
			if _, err := time.Parse(dateOnly, group.OpeningBalanceDate); err != nil {
				return nil, httpx.NewValidationError(fmt.Sprintf("%s opening balance date must be YYYY-MM-DD", cur))
			}
		default:
			return nil, httpx.NewValidationError(fmt.Sprintf("unknown import action %q", action))
		}

		var existing []existingTx
		if accountID != uuid.Nil {
			existing, err = s.loadExisting(ctx, workspaceID, accountID, sub)
			if err != nil {
				return nil, err
			}
		}
		classified := classify(sub, existing)
		// Workspace-wide source_ref dedup. The unique index on source_refs
		// is workspace-scoped, but the per-account `existing` query above
		// only sees the destination account. Without this extra check, an
		// importable row whose external_id already exists on a different
		// account in the same workspace would 23505 at insert time and
		// roll the whole file back. Move those rows from `importable` to
		// `duplicates` so they're tallied honestly in the result.
		taken, err := s.workspaceExternalIDs(ctx, workspaceID, sub.Profile)
		if err != nil {
			return nil, err
		}
		filterByWorkspaceExternalIDs(&classified, taken)
		planned = append(planned, plannedGroup{
			in:         group,
			parsed:     sub,
			accountID:  accountID,
			classified: classified,
		})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin import plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batchID := uuidx.New()
	now := s.now()
	summary, _ := json.Marshal(map[string]any{
		"profile":  parsed.Profile,
		"fileHash": payload.FileHash,
		"fileName": payload.FileName,
		"groups":   len(planned),
	})
	q := dbq.New(tx)
	if err := q.InsertImportBatch(ctx, dbq.InsertImportBatchParams{
		ID:              batchID,
		WorkspaceID:     workspaceID,
		FileName:        &payload.FileName,
		FileHash:        &payload.FileHash,
		Summary:         summary,
		CreatedByUserID: &userID,
		StartedAt:       now,
	}); err != nil {
		return nil, fmt.Errorf("insert import batch: %w", err)
	}

	out := &ApplyResult{BatchID: batchID}
	for _, group := range planned {
		// Pull the consolidated parser's per-section closure date out of
		// any tx in the group (all rows of a section share the same hint).
		// Empty string means the upstream account is still active.
		closeDate := groupCloseDate(group.parsed)
		accountID := group.accountID
		if accountID == uuid.Nil {
			// Multi-file imports preview every file before applying any of
			// them, so a "create_account" plan from file 2 is unaware that
			// file 1 (already applied earlier in this loop) just created an
			// account it should merge into. Re-check by exact (name, kind,
			// currency) here and divert to import_to_account when a match
			// shows up — otherwise the user gets two duplicate accounts
			// like "Revolut Flexible Cash Funds EUR" sitting side by side.
			merged, err := s.lookupExistingAccount(ctx, q, workspaceID, group.in)
			if err != nil {
				return nil, err
			}
			if merged != uuid.Nil {
				accountID = merged
				// Reload existing rows for the now-known account and
				// re-classify so the importable list is properly deduped
				// against the file-1 transactions just inserted.
				existing, err := s.loadExisting(ctx, workspaceID, accountID, group.parsed)
				if err != nil {
					return nil, err
				}
				reclassified := classify(group.parsed, existing)
				taken, err := s.workspaceExternalIDs(ctx, workspaceID, group.parsed.Profile)
				if err != nil {
					return nil, err
				}
				filterByWorkspaceExternalIDs(&reclassified, taken)
				group.classified = reclassified
			} else {
				accountID, err = s.createImportAccountTx(ctx, q, workspaceID, parsed.Institution, group.in, closeDate)
				if err != nil {
					return nil, err
				}
			}
		} else if group.in.Reactivate {
			// Opt-in reactivate: clear archived_at when the user explicitly
			// asked to resurface this account. No-op for non-archived rows.
			// updated_at is set by trigger. Without this opt-in, a re-import
			// merges new transactions into an archived account silently —
			// archive is a UI-only hide flag, not a write barrier.
			if err := q.UnarchiveAccount(ctx, dbq.UnarchiveAccountParams{
				WorkspaceID: workspaceID,
				ID:          accountID,
			}); err != nil {
				return nil, fmt.Errorf("unarchive import account: %w", err)
			}
		}
		ids, err := s.insertImportableTx(ctx, q, workspaceID, accountID, batchID, incomingProvider(group.parsed.Profile), group.classified.importable)
		if err != nil {
			return nil, err
		}
		// Merge-case archive: the create branch above already populated
		// close_date + archived_at for fresh inserts; if we landed on an
		// existing account that hasn't been archived yet, propagate the
		// closure metadata now (idempotent — the SQL UPDATE keeps any
		// pre-existing close_date / archived_at value).
		if closeDate != "" {
			if t, perr := time.Parse(dateOnly, closeDate); perr == nil {
				archivedTS := s.now().UTC()
				if err := q.ArchiveImportAccount(ctx, dbq.ArchiveImportAccountParams{
					WorkspaceID: workspaceID,
					ID:          accountID,
					CloseDate:   t,
					ArchivedAt:  archivedTS,
				}); err != nil {
					return nil, fmt.Errorf("archive import account: %w", err)
				}
			}
		}
		out.InsertedCount += len(ids)
		out.DuplicateCount += len(group.classified.duplicates)
		out.ConflictCount += len(group.classified.conflicts)
		out.TransactionIDs = append(out.TransactionIDs, ids...)
		out.Conflicts = append(out.Conflicts, group.classified.conflicts...)
		if group.parsed.DateFrom != nil && group.parsed.DateTo != nil {
			if err := s.retireExplainedSynthetics(ctx, q, workspaceID, accountID, *group.parsed.DateFrom, *group.parsed.DateTo); err != nil {
				return nil, err
			}
			if err := s.retireMMFSummaries(ctx, q, workspaceID, accountID, *group.parsed.DateFrom, *group.parsed.DateTo); err != nil {
				return nil, err
			}
			if err := s.backfillOpeningIfEarlier(ctx, q, workspaceID, accountID, *group.parsed.DateFrom); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import plan: %w", err)
	}
	return out, nil
}

// ApplyMultiPlan applies a list of file plans serially, each in its own
// transaction. A parse/validation error on one file is captured into that
// file's result without aborting the rest, since the per-file pipeline
// already produces an order-independent end state — there's no benefit
// to rolling back successful files because of an unrelated bad upload.
//
// File names come back in the result so the UI can pair errors with
// their source files; we extract them from each token's preview payload
// (no DB lookup needed). The aggregate counts let the toast/summary UI
// say "imported 312 transactions across 3 files" without summing client-
// side.
func (s *Service) ApplyMultiPlan(ctx context.Context, workspaceID, userID uuid.UUID, in ApplyMultiPlanInput) (*ApplyMultiPlanResult, error) {
	out := &ApplyMultiPlanResult{Files: make([]ApplyMultiPlanFileResult, 0, len(in.Files))}
	for _, file := range in.Files {
		fileName := ""
		if payload, err := parseToken(file.FileToken); err == nil {
			fileName = payload.FileName
		}
		entry := ApplyMultiPlanFileResult{FileName: fileName}
		res, err := s.ApplyPlan(ctx, workspaceID, userID, file)
		if err != nil {
			entry.Error = err.Error()
			out.Files = append(out.Files, entry)
			continue
		}
		entry.Result = res
		out.InsertedCount += res.InsertedCount
		out.DuplicateCount += res.DuplicateCount
		out.ConflictCount += res.ConflictCount
		out.Files = append(out.Files, entry)
	}
	return out, nil
}

// groupCloseDate returns the consolidated parser's per-section closure
// date for the group's transactions, in YYYY-MM-DD form. All rows in a
// group came from the same Revolut sub-account, so any tx's hint is
// authoritative — we look at the first parsed row that carries the tag.
// Returns the empty string when no transaction tagged a closure date,
// which is the common case (account still open upstream).
func groupCloseDate(parsed ParsedFile) string {
	for _, tx := range parsed.Transactions {
		if v := tx.Raw["section_close_date"]; v != "" {
			return v
		}
	}
	return ""
}

// lookupExistingAccount returns the id of an account in the workspace
// whose (name, kind, currency) exactly match the requested create-account
// plan. Returns uuid.Nil if nothing matches. Used at apply time to merge
// same-batch creates that the preview step couldn't deduplicate; without
// it a multi-file import where file 1 creates "Revolut Flexible Cash
// Funds EUR" and file 2's plan also says create_account would emit a
// second identically-named account.
func (s *Service) lookupExistingAccount(ctx context.Context, q *dbq.Queries, workspaceID uuid.UUID, group ApplyPlanGroup) (uuid.UUID, error) {
	name := strings.TrimSpace(group.Name)
	kind := strings.TrimSpace(group.Kind)
	currency := strings.ToUpper(strings.TrimSpace(group.Currency))
	if name == "" || kind == "" || currency == "" {
		return uuid.Nil, nil
	}
	id, err := q.FindAccountByNameKindCurrency(ctx, dbq.FindAccountByNameKindCurrencyParams{
		WorkspaceID: workspaceID,
		Name:        name,
		Kind:        dbq.AccountKind(kind),
		Currency:    currency,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, fmt.Errorf("lookup existing account: %w", err)
	}
	return id, nil
}

// workspaceExternalIDs returns the set of source_ref external_ids already
// present in the workspace for the given file profile. Used at classify
// time to skip rows whose source_ref would collide with an existing one
// in a different account (e.g. a previous import that targeted a now-
// deleted-and-recreated account). Without this, the unique index on
// source_refs throws 23505 mid-apply and rolls the whole file back.
func (s *Service) workspaceExternalIDs(ctx context.Context, workspaceID uuid.UUID, profile string) (map[string]struct{}, error) {
	provider := incomingProvider(profile)
	rows, err := dbq.New(s.pool).ListWorkspaceExternalIDs(ctx, dbq.ListWorkspaceExternalIDsParams{
		WorkspaceID: workspaceID,
		Provider:    &provider,
	})
	if err != nil {
		return nil, fmt.Errorf("list workspace external ids: %w", err)
	}
	out := make(map[string]struct{}, len(rows))
	for _, id := range rows {
		out[id] = struct{}{}
	}
	return out, nil
}

// filterByWorkspaceExternalIDs reclassifies any importable rows whose
// external_id is already represented somewhere in the workspace as
// duplicates. Mutates classified in place.
func filterByWorkspaceExternalIDs(classified *classifiedRows, taken map[string]struct{}) {
	if len(taken) == 0 {
		return
	}
	kept := classified.importable[:0]
	for _, tx := range classified.importable {
		if tx.ExternalID != "" {
			if _, hit := taken[tx.ExternalID]; hit {
				classified.duplicates = append(classified.duplicates, tx)
				continue
			}
		}
		kept = append(kept, tx)
	}
	classified.importable = kept
}

// backfillOpeningIfEarlier moves an account's opening-balance anchor (and
// the corresponding opening snapshot) back when a re-import drops rows
// that pre-date the existing anchor. The balance query filters on
// `booked_at >= max(latest_snapshot.as_of, opening_balance_date)`, so
// without this nudge those early rows are inserted but silently excluded
// from the displayed balance — exactly the failure mode that produced
// "Flexible Cash Funds GBP = -156.85" after a savings-statement re-import
// landed BUYs on 2024-11-18 in an account whose anchor sat at 2024-11-19.
//
// We only ever shift the anchor *earlier*; the SQL UPDATEs are guarded so
// they no-op when the existing date already covers the new range.
func (s *Service) backfillOpeningIfEarlier(ctx context.Context, q *dbq.Queries, workspaceID, accountID uuid.UUID, dateFrom time.Time) error {
	newDate := time.Date(dateFrom.Year(), dateFrom.Month(), dateFrom.Day(), 0, 0, 0, 0, time.UTC)
	if err := q.BackfillAccountOpeningDate(ctx, dbq.BackfillAccountOpeningDateParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		NewDate:     newDate,
	}); err != nil {
		return fmt.Errorf("backfill opening_balance_date: %w", err)
	}
	if err := q.BackfillOpeningSnapshot(ctx, dbq.BackfillOpeningSnapshotParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		NewAsOf:     newDate,
	}); err != nil {
		return fmt.Errorf("backfill opening snapshot as_of: %w", err)
	}
	return nil
}

// retireMMFSummaries voids consolidated-MMF "net interest" rows in a
// destination account when higher-fidelity savings-statement events are
// present alongside them. Runs after every import touching the account,
// so the cleanup is order-independent: importing the savings statement
// after the consolidated voids the existing summaries (the original
// case), and importing the consolidated after the savings statement
// voids the freshly-inserted summaries on the spot.
//
// The presence check is account-scoped — if the account has no savings-
// statement events, summaries are kept as the user's only source of MMF
// interest history. The void scan stays narrowed to the import's date
// range so older summaries outside the new file's coverage aren't
// nuked when a partial-period file lands.
func (s *Service) retireMMFSummaries(ctx context.Context, q *dbq.Queries, workspaceID, accountID uuid.UUID, dateFrom, dateTo time.Time) error {
	hasSavings, err := q.AccountHasSavingsStatementRows(ctx, dbq.AccountHasSavingsStatementRowsParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
	})
	if err != nil {
		return fmt.Errorf("check savings-statement presence: %w", err)
	}
	if !hasSavings {
		return nil
	}
	rows, err := q.LoadMMFSummaryCandidates(ctx, dbq.LoadMMFSummaryCandidatesParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		DateFrom:    dateFrom,
		DateTo:      dateTo,
	})
	if err != nil {
		return fmt.Errorf("load mmf summary candidates: %w", err)
	}
	for _, r := range rows {
		if err := q.VoidTransaction(ctx, dbq.VoidTransactionParams{
			ID:          r.ID,
			WorkspaceID: workspaceID,
		}); err != nil {
			return fmt.Errorf("void mmf summary %s: %w", r.ID, err)
		}
	}
	return nil
}

func (s *Service) createImportAccountTx(ctx context.Context, q *dbq.Queries, workspaceID uuid.UUID, institution string, group ApplyPlanGroup, closeDate string) (uuid.UUID, error) {
	openDate, _ := time.Parse(dateOnly, group.OpenDate)
	openingBalanceDate, _ := time.Parse(dateOnly, group.OpeningBalanceDate)
	accountID := uuidx.New()
	snapshotID := uuidx.New()
	inst := strings.TrimSpace(institution)
	var instPtr *string
	if inst != "" {
		instPtr = &inst
	}
	openingTS := time.Date(openingBalanceDate.Year(), openingBalanceDate.Month(), openingBalanceDate.Day(), 0, 0, 0, 0, time.UTC)
	includeInSavingsRate := group.Kind == "checking" || group.Kind == "savings" || group.Kind == "cash"
	// Closure metadata: when the consolidated export tells us this sub-
	// account is already closed, populate close_date and archive on insert
	// so the imported row doesn't show up in the active accounts list.
	var closeDatePtr *time.Time
	var archivedAtPtr *time.Time
	if closeDate != "" {
		if t, err := time.Parse(dateOnly, closeDate); err == nil {
			tcp := t
			closeDatePtr = &tcp
			archivedTS := s.now().UTC()
			archivedAtPtr = &archivedTS
		}
	}
	if err := q.InsertImportAccount(ctx, dbq.InsertImportAccountParams{
		ID:                   accountID,
		WorkspaceID:          workspaceID,
		Name:                 strings.TrimSpace(group.Name),
		Kind:                 dbq.AccountKind(strings.TrimSpace(group.Kind)),
		Currency:             strings.ToUpper(strings.TrimSpace(group.Currency)),
		Institution:          instPtr,
		OpenDate:             openDate,
		CloseDate:            closeDatePtr,
		OpeningBalance:       stringToNumeric(strings.TrimSpace(group.OpeningBalance)),
		OpeningBalanceDate:   openingBalanceDate,
		IncludeInSavingsRate: includeInSavingsRate,
		ArchivedAt:           archivedAtPtr,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("insert import account: %w", err)
	}
	if err := q.InsertOpeningSnapshot(ctx, dbq.InsertOpeningSnapshotParams{
		ID:          snapshotID,
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		AsOf:        openingTS,
		Balance:     stringToNumeric(strings.TrimSpace(group.OpeningBalance)),
		Currency:    strings.ToUpper(strings.TrimSpace(group.Currency)),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("insert import opening snapshot: %w", err)
	}
	return accountID, nil
}

func (s *Service) insertImportableTx(ctx context.Context, q *dbq.Queries, workspaceID, accountID, batchID uuid.UUID, provider string, rows []ParsedTransaction) ([]uuid.UUID, error) {
	inserted := make([]uuid.UUID, 0, len(rows))
	for _, incoming := range rows {
		id := uuidx.New()
		rawJSON, _ := json.Marshal(incoming.Raw)
		if err := q.InsertImportTransaction(ctx, dbq.InsertImportTransactionParams{
			ID:              id,
			WorkspaceID:     workspaceID,
			AccountID:       accountID,
			BookedAt:        incoming.BookedAt,
			ValueAt:         incoming.ValueAt,
			PostedAt:        incoming.PostedAt,
			Amount:          decimalToNumeric(incoming.Amount),
			Currency:        incoming.Currency,
			CounterpartyRaw: incoming.CounterpartyRaw,
			Description:     incoming.Description,
			Raw:             rawJSON,
		}); err != nil {
			return nil, fmt.Errorf("insert transaction: %w", err)
		}
		if err := q.InsertSourceRef(ctx, dbq.InsertSourceRefParams{
			ID:            uuidx.New(),
			WorkspaceID:   workspaceID,
			EntityID:      id,
			Provider:      &provider,
			ImportBatchID: &batchID,
			ExternalID:    &incoming.ExternalID,
			RawPayload:    rawJSON,
			ObservedAt:    s.now(),
		}); err != nil {
			return nil, fmt.Errorf("insert source ref: %w", err)
		}
		inserted = append(inserted, id)
	}
	return inserted, nil
}

func (s *Service) buildPreview(ctx context.Context, workspaceID uuid.UUID, parsed ParsedFile, fileName, fileHash, token string, accountID *uuid.UUID) (*Preview, error) {
	if len(parsed.Transactions) == 0 {
		return nil, httpx.NewValidationError("file contains no importable transactions")
	}
	samples := make([]PreviewRow, 0, min(5, len(parsed.Transactions)))
	for _, tx := range parsed.Transactions {
		if len(samples) >= 5 {
			break
		}
		samples = append(samples, previewRow(tx))
	}
	p := &Preview{
		Profile:            parsed.Profile,
		Institution:        parsed.Institution,
		AccountHint:        parsed.AccountHint,
		SuggestedName:      suggestedName(parsed),
		SuggestedKind:      "checking",
		SuggestedCurrency:  parsed.Currency,
		TransactionCount:   len(parsed.Transactions),
		SampleTransactions: samples,
		Warnings:           parsed.Warnings,
		FileToken:          token,
		FileName:           fileName,
		FileHash:           fileHash,
		ExistingAccountID:  accountID,
		ImportableCount:    len(parsed.Transactions),
		DuplicateCount:     0,
		ConflictCount:      0,
		SuggestedOpenDate:  formatDatePtr(parsed.DateFrom),
		DateFrom:           formatDatePtr(parsed.DateFrom),
		DateTo:             formatDatePtr(parsed.DateTo),
	}
	if accountID != nil {
		accountCurrency, err := s.accountCurrency(ctx, workspaceID, *accountID)
		if err != nil {
			return nil, err
		}
		if parsed.Currency != "" && parsed.Currency != accountCurrency {
			return nil, httpx.NewValidationError(fmt.Sprintf("file currency %s does not match account currency %s", parsed.Currency, accountCurrency))
		}
		existing, err := s.loadExisting(ctx, workspaceID, *accountID, parsed)
		if err != nil {
			return nil, err
		}
		classified := classify(parsed, existing)
		p.DuplicateCount = len(classified.duplicates)
		p.ConflictCount = len(classified.conflicts)
		p.ImportableCount = len(classified.importable)
		p.ConflictTransactions = classified.conflicts
		return p, nil
	}
	groups, err := s.currencyGroups(ctx, workspaceID, parsed)
	if err != nil {
		return nil, err
	}
	p.CurrencyGroups = groups
	return p, nil
}

type importAccountMatch struct {
	ID          uuid.UUID
	Name        string
	Currency    string
	Kind        string
	Institution *string
	Archived    bool
}

// groupKey identifies one import target: a currency, plus an optional
// per-section AccountHint for files (like consolidated v2) that emit several
// logical accounts in the same currency. Empty SourceKey collapses every
// tx of the currency into one group — preserves banking-style behaviour.
type groupKey struct {
	currency  string
	sourceKey string
}

func (s *Service) currencyGroups(ctx context.Context, workspaceID uuid.UUID, parsed ParsedFile) ([]CurrencyGroup, error) {
	grouped := map[groupKey]ParsedFile{}
	for _, tx := range parsed.Transactions {
		k := groupKey{currency: tx.Currency, sourceKey: tx.AccountHint}
		g := grouped[k]
		if g.Profile == "" {
			g = ParsedFile{
				Profile:     parsed.Profile,
				Institution: parsed.Institution,
				AccountHint: tx.AccountHint,
				Currency:    k.currency,
			}
		}
		g.Transactions = append(g.Transactions, tx)
		grouped[k] = g
	}

	accounts, err := s.loadImportAccountMatches(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	keys := make([]groupKey, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].currency != keys[j].currency {
			return keys[i].currency < keys[j].currency
		}
		return keys[i].sourceKey < keys[j].sourceKey
	})

	groups := make([]CurrencyGroup, 0, len(keys))
	for _, k := range keys {
		sub := grouped[k]
		finalizeParsedRange(&sub, false)
		samples := make([]PreviewRow, 0, min(5, len(sub.Transactions)))
		for _, tx := range sub.Transactions {
			if len(samples) >= 5 {
				break
			}
			samples = append(samples, previewRow(tx))
		}
		// All txs in a group share the same AccountHint, so any tx's
		// KindHint represents the section's suggested kind. Default to
		// checking when the parser didn't supply one.
		kind := "checking"
		if h := sub.Transactions[0].KindHint; h != "" {
			kind = h
		}
		group := CurrencyGroup{
			Currency:           k.currency,
			SourceKey:          k.sourceKey,
			SuggestedName:      suggestedNameForGroup(parsed.Institution, k),
			SuggestedKind:      kind,
			SuggestedOpenDate:  formatDatePtr(sub.DateFrom),
			TransactionCount:   len(sub.Transactions),
			DateFrom:           formatDatePtr(sub.DateFrom),
			DateTo:             formatDatePtr(sub.DateTo),
			Action:             "create_account",
			ImportableCount:    len(sub.Transactions),
			SampleTransactions: samples,
		}
		candidates := importCandidates(accounts, parsed.Institution, k.currency, kind)
		for i := range candidates {
			existing, err := s.loadExisting(ctx, workspaceID, candidates[i].ID, sub)
			if err != nil {
				return nil, err
			}
			classified := classify(sub, existing)
			candidates[i].ImportableCount = len(classified.importable)
			candidates[i].DuplicateCount = len(classified.duplicates)
			candidates[i].ConflictCount = len(classified.conflicts)
			candidates[i].ConflictTransactions = classified.conflicts
		}
		// Rank by transaction overlap so the wizard suggests whichever
		// existing account has the most matching fingerprints first. Falls
		// back to alphabetical order on ties for stability.
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].DuplicateCount != candidates[j].DuplicateCount {
				return candidates[i].DuplicateCount > candidates[j].DuplicateCount
			}
			return candidates[i].Name < candidates[j].Name
		})
		group.CandidateAccounts = candidates
		if match, ok := bestImportCandidate(candidates, len(sub.Transactions)); ok {
			group.ExistingAccountID = &match.ID
			group.ExistingAccountName = match.Name
			group.Action = "import_to_account"
			group.ImportableCount = match.ImportableCount
			group.DuplicateCount = match.DuplicateCount
			group.ConflictCount = match.ConflictCount
			group.ConflictTransactions = match.ConflictTransactions
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// suggestedNameForGroup builds a default account name for an import group.
// For section-aware files it incorporates the per-section account hint so
// pockets get distinct, recognisable names (e.g. "Revolut Travel - 200 CHF").
func suggestedNameForGroup(institution string, k groupKey) string {
	parts := []string{}
	if strings.TrimSpace(institution) != "" {
		parts = append(parts, strings.TrimSpace(institution))
	}
	if k.sourceKey != "" {
		parts = append(parts, k.sourceKey)
	}
	if k.currency != "" {
		parts = append(parts, k.currency)
	}
	if len(parts) == 0 {
		return "Imported account"
	}
	return strings.Join(parts, " ")
}

// bestImportCandidate picks the candidate the wizard should auto-suggest as
// the import target. We rely on transaction-fingerprint overlap as the
// primary signal — whichever existing account already contains the most
// of the parsed transactions is the most likely target. When the user has
// only one same-currency account it's an easy auto-pick; with several we
// pick whichever shares the most history.
//
// The threshold prevents false-matches when the user really does want a
// brand-new account (e.g. a fresh Revolut PHP account they just opened
// has no prior fingerprints). Either ≥10 absolute overlapping rows OR
// ≥40% of the incoming rows is enough to consider it a match.
func bestImportCandidate(candidates []AccountCandidate, incoming int) (AccountCandidate, bool) {
	if len(candidates) == 0 {
		return AccountCandidate{}, false
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	top := candidates[0]
	if top.DuplicateCount == 0 {
		return AccountCandidate{}, false
	}
	if top.DuplicateCount >= 10 {
		return top, true
	}
	if incoming > 0 && top.DuplicateCount*100 >= incoming*40 {
		return top, true
	}
	return AccountCandidate{}, false
}

func (s *Service) loadImportAccountMatches(ctx context.Context, workspaceID uuid.UUID) ([]importAccountMatch, error) {
	// Archived accounts are kept in the candidate set so re-importing the
	// same file matches the account the user already imported into instead
	// of silently creating a duplicate. The wizard surfaces the archived
	// state and the apply path unarchives the account before importing.
	rows, err := dbq.New(s.pool).ListImportAccountMatches(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load import account matches: %w", err)
	}
	out := make([]importAccountMatch, 0, len(rows))
	for _, r := range rows {
		out = append(out, importAccountMatch{
			ID:          r.ID,
			Name:        r.Name,
			Currency:    r.Currency,
			Kind:        string(r.Kind),
			Institution: r.Institution,
			Archived:    r.ArchivedAt != nil,
		})
	}
	return out, nil
}

// importCandidates filters known accounts down to viable import targets for
// a group. Currency must match exactly. Kind compatibility is grouped:
// cash-like (checking, savings, cash) and investment-like (brokerage,
// crypto_wallet, asset, pillar_3a, pillar_2) are kept separate so MMF
// interest rows from a Flexible Cash Funds (brokerage) group don't
// auto-import into a Conta Pessoal (checking) account just because it's
// the only matching-currency account in the workspace.
func importCandidates(accounts []importAccountMatch, institution, currency, suggestedKind string) []AccountCandidate {
	var matches []AccountCandidate
	for _, account := range accounts {
		if account.Currency != currency {
			continue
		}
		if !accountKindsCompatible(account.Kind, suggestedKind) {
			continue
		}
		if institution != "" {
			if account.Institution != nil && strings.TrimSpace(*account.Institution) != "" && !strings.EqualFold(strings.TrimSpace(*account.Institution), institution) {
				continue
			}
		}
		c := AccountCandidate{ID: account.ID, Name: account.Name, Currency: account.Currency, Archived: account.Archived}
		if account.Institution != nil {
			c.Institution = strings.TrimSpace(*account.Institution)
		}
		matches = append(matches, c)
	}
	return matches
}

func accountKindsCompatible(existing, suggested string) bool {
	if suggested == "" || existing == "" {
		return true
	}
	cash := map[string]bool{"checking": true, "savings": true, "cash": true}
	invest := map[string]bool{"brokerage": true, "crypto_wallet": true, "asset": true, "pillar_3a": true, "pillar_2": true}
	if cash[existing] && cash[suggested] {
		return true
	}
	if invest[existing] && invest[suggested] {
		return true
	}
	return existing == suggested
}

func (s *Service) accountCurrency(ctx context.Context, workspaceID, accountID uuid.UUID) (string, error) {
	currency, err := dbq.New(s.pool).GetAccountCurrency(ctx, dbq.GetAccountCurrencyParams{
		WorkspaceID: workspaceID,
		ID:          accountID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", httpx.NewNotFoundError("account")
		}
		return "", fmt.Errorf("load account: %w", err)
	}
	return currency, nil
}

type existingTx struct {
	ID          uuid.UUID
	BookedAt    time.Time
	PostedAt    *time.Time
	Amount      decimal.Decimal
	Currency    string
	Description string
	SourceID    *string
	Synthetic   bool
}

func (s *Service) loadExisting(ctx context.Context, workspaceID, accountID uuid.UUID, parsed ParsedFile) ([]existingTx, error) {
	if parsed.DateFrom == nil || parsed.DateTo == nil {
		return nil, nil
	}
	provider := incomingProvider(parsed.Profile)
	rows, err := dbq.New(s.pool).LoadExistingTransactions(ctx, dbq.LoadExistingTransactionsParams{
		Provider:    &provider,
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		DateFrom:    *parsed.DateFrom,
		DateTo:      *parsed.DateTo,
		Currency:    parsed.Currency,
	})
	if err != nil {
		return nil, fmt.Errorf("query existing transactions: %w", err)
	}
	out := make([]existingTx, 0, len(rows))
	for _, r := range rows {
		d, err := decimal.NewFromString(r.Amount)
		if err != nil {
			return nil, err
		}
		out = append(out, existingTx{
			ID:          r.ID,
			BookedAt:    r.BookedAt,
			PostedAt:    r.PostedAt,
			Amount:      d,
			Currency:    r.Currency,
			Description: r.Description,
			SourceID:    r.SourceID,
			Synthetic:   r.Synthetic,
		})
	}
	return out, nil
}

type classifiedRows struct {
	importable []ParsedTransaction
	duplicates []ParsedTransaction
	conflicts  []ConflictPreview
}

func classify(parsed ParsedFile, existing []existingTx) classifiedRows {
	var out classifiedRows
	for _, incoming := range parsed.Transactions {
		if incoming.Raw[syntheticTagKey] == syntheticBalanceReconcile {
			if residualAlreadyImported(incoming, existing) {
				out.duplicates = append(out.duplicates, incoming)
				continue
			}
		}
		if duplicateBySource(incoming, existing) || duplicateByFingerprint(incoming, existing) {
			out.duplicates = append(out.duplicates, incoming)
			continue
		}
		if conflict, ok := conflictByStableFields(incoming, existing); ok {
			out.conflicts = append(out.conflicts, conflict)
			continue
		}
		out.importable = append(out.importable, incoming)
	}
	return out
}

const syntheticTagKey = "synthetic"

// residualAlreadyImported decides whether a parser-emitted synthetic
// balance-reconcile row should be skipped because the existing transactions
// in the destination account already net to its residual within ±7 days.
// This is the banking-first → consolidated-second direction; the post-apply
// retirement step covers the opposite ordering.
func residualAlreadyImported(incoming ParsedTransaction, existing []existingTx) bool {
	residualStr := incoming.Raw["synthetic_residual"]
	if residualStr == "" {
		residualStr = incoming.Amount.String()
	}
	residual, err := decimal.NewFromString(residualStr)
	if err != nil {
		return false
	}
	real := make([]existingTx, 0, len(existing))
	for _, e := range existing {
		if e.Synthetic {
			continue
		}
		real = append(real, e)
	}
	var gapStart time.Time
	if raw := incoming.Raw["gap_start_date"]; raw != "" {
		if t, err := time.Parse(dateOnly, raw); err == nil {
			gapStart = t
		}
	}
	return residualExplainedByExisting(incoming.BookedAt, gapStart, incoming.Currency, residual, real)
}

func duplicateBySource(incoming ParsedTransaction, existing []existingTx) bool {
	for _, e := range existing {
		if e.SourceID != nil && *e.SourceID == incoming.ExternalID {
			return true
		}
	}
	return false
}

func duplicateByFingerprint(incoming ParsedTransaction, existing []existingTx) bool {
	incomingDesc := normalizeDescription(valueOf(incoming.Description))
	for _, e := range existing {
		// Synthetic balance-adjustment rows are placeholders inserted to
		// reconcile gaps in a single source's running balance — they aren't
		// real transactions. A real row arriving from a second source should
		// never be absorbed into a synthetic; let it land as importable and
		// rely on retireExplainedSynthetics to void the synthetic afterwards.
		if e.Synthetic {
			continue
		}
		if !fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue
		}
		if incomingDesc == "" || normalizeDescription(e.Description) == incomingDesc {
			return true
		}
	}
	return false
}

func conflictByStableFields(incoming ParsedTransaction, existing []existingTx) (ConflictPreview, bool) {
	incomingDesc := normalizeDescription(valueOf(incoming.Description))
	// Pass 1: auto-window match with description disagreement.
	for _, e := range existing {
		if e.Synthetic {
			continue
		}
		if !fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue
		}
		if incomingDesc != "" && normalizeDescription(e.Description) != incomingDesc {
			return previewConflict("description_mismatch", incoming, e), true
		}
	}
	// Pass 2: review-window drift on amount+currency. Originally fired
	// regardless of description, but that produced false positives whenever
	// two unrelated transactions of the same amount happened within 7 days
	// of each other (e.g. a +1 "Carregamento" 2 days before a +1 "Balance
	// migration"). The auto-apply path drops conflicts silently, so a false
	// match here is a real row vanishing from the ledger. Require the
	// descriptions to agree (after normalization) before treating drifted
	// rows as the same transaction. If descriptions don't agree, we trust
	// that the row is genuinely new and let it import.
	for _, e := range existing {
		if e.Synthetic {
			continue
		}
		if fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue // already handled
		}
		if !fuzzyStableMatch(incoming, e, reviewDedupDays) {
			continue
		}
		if incomingDesc != "" && normalizeDescription(e.Description) != incomingDesc {
			continue
		}
		return previewConflict("date_drift", incoming, e), true
	}
	return ConflictPreview{}, false
}

func previewConflict(reason string, incoming ParsedTransaction, e existingTx) ConflictPreview {
	return ConflictPreview{
		Reason:   reason,
		Incoming: previewRow(incoming),
		Existing: PreviewRow{
			BookedAt:    e.BookedAt.Format(dateOnly),
			Amount:      e.Amount.String(),
			Currency:    e.Currency,
			Description: e.Description,
		},
	}
}

func previewRow(tx ParsedTransaction) PreviewRow {
	return PreviewRow{
		BookedAt:    tx.BookedAt.Format(dateOnly),
		Amount:      tx.Amount.String(),
		Currency:    tx.Currency,
		Description: firstNonBlank(valueOf(tx.Description), valueOf(tx.CounterpartyRaw)),
	}
}

func suggestedName(parsed ParsedFile) string {
	if parsed.Institution == "" {
		return "Imported account"
	}
	if parsed.AccountHint != "" {
		return parsed.Institution + " " + parsed.AccountHint
	}
	return parsed.Institution
}

func formatDatePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(dateOnly)
}

func incomingProvider(profile string) string {
	return "file:" + profile
}

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func decodeContent(encoded string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, httpx.NewValidationError("fileToken content is invalid")
	}
	return b, nil
}

// retireExplainedSynthetics scans synthetic balance-reconcile rows in the
// destination account that fall in the imported file's date range, and
// voids any whose residual is now covered by real (non-synthetic) rows
// within ±7d. Called once per affected account after each section's inserts.
// Voiding (status='voided') keeps the row visible in audit history while
// removing it from the running balance.
func (s *Service) retireExplainedSynthetics(ctx context.Context, q *dbq.Queries, workspaceID, accountID uuid.UUID, dateFrom, dateTo time.Time) error {
	synthRows, err := q.LoadSyntheticCandidates(ctx, dbq.LoadSyntheticCandidatesParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		DateFrom:    dateFrom.Add(-time.Duration(reviewDedupDays) * 24 * time.Hour),
		DateTo:      dateTo.Add(time.Duration(reviewDedupDays) * 24 * time.Hour),
	})
	if err != nil {
		return fmt.Errorf("scan synthetic rows: %w", err)
	}
	type synthCandidate struct {
		id       uuid.UUID
		bookedAt time.Time
		gapStart time.Time
		currency string
		residual decimal.Decimal
		batchID  *uuid.UUID
	}
	var candidates []synthCandidate
	for _, r := range synthRows {
		residual, err := decimal.NewFromString(r.Residual)
		if err != nil {
			continue
		}
		gapStart := r.BookedAt
		if r.GapStartDate != "" {
			if t, err := time.Parse(dateOnly, r.GapStartDate); err == nil {
				gapStart = t
			}
		}
		candidates = append(candidates, synthCandidate{
			id:       r.ID,
			bookedAt: r.BookedAt,
			gapStart: gapStart,
			currency: r.Currency,
			residual: residual,
			batchID:  r.ImportBatchID,
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	// Walk synthetics in date order so consume-once is deterministic and
	// each banking row goes to the earliest synthetic whose gap window
	// covers it. Two synthetics with the same residual (e.g. multiple
	// missing -15 REVX transfers across consecutive days) would otherwise
	// each match the same single explaining row, double-retiring and
	// shifting the running balance.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].bookedAt.Before(candidates[j].bookedAt)
	})
	consumed := map[uuid.UUID]struct{}{}
	for _, c := range candidates {
		// Search the entire gap interval (gapStart..bookedAt) plus the
		// review-window slop on either side. The synthetic represents a
		// missing real transaction somewhere inside that interval, so the
		// matching banking row may sit anywhere along it. Using only ±7d
		// around bookedAt misses gaps wider than 14 days.
		from := c.gapStart.Add(-time.Duration(reviewDedupDays) * 24 * time.Hour)
		to := c.bookedAt.Add(time.Duration(reviewDedupDays) * 24 * time.Hour)
		// Only consider rows from a different import batch than the
		// synthetic itself. The matcher's subset-sum search false-positives
		// when same-batch rows coincidentally add up to the residual (e.g.
		// many -15 transfers in the window covering a -15 synthetic). The
		// synthetic only deserves voiding when *new* data — from a
		// subsequent import — actually explains the residual.
		realRows, err := q.LoadRealRowsForSynthetic(ctx, dbq.LoadRealRowsForSyntheticParams{
			WorkspaceID:    workspaceID,
			AccountID:      accountID,
			Currency:       c.currency,
			DateFrom:       from,
			DateTo:         to,
			ExcludeID:      c.id,
			ExcludeBatchID: c.batchID,
		})
		if err != nil {
			return fmt.Errorf("scan real rows for synthetic: %w", err)
		}
		var existing []existingTx
		for _, r := range realRows {
			if _, taken := consumed[r.ID]; taken {
				continue
			}
			d, err := decimal.NewFromString(r.Amount)
			if err != nil {
				continue
			}
			existing = append(existing, existingTx{
				ID:          r.ID,
				BookedAt:    r.BookedAt,
				PostedAt:    r.PostedAt,
				Amount:      d,
				Currency:    r.Currency,
				Description: r.Description,
			})
		}
		ok, used := matchResidualSubset(c.bookedAt, c.gapStart, c.currency, c.residual, existing)
		if !ok {
			continue
		}
		for _, idx := range used {
			consumed[existing[idx].ID] = struct{}{}
		}
		if err := q.VoidTransaction(ctx, dbq.VoidTransactionParams{
			ID:          c.id,
			WorkspaceID: workspaceID,
		}); err != nil {
			return fmt.Errorf("void synthetic %s: %w", c.id, err)
		}
	}
	return nil
}
