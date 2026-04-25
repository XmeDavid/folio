package bankimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

type importTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

func (s *Service) Preview(ctx context.Context, tenantID uuid.UUID, fileName string, r io.Reader, accountID *uuid.UUID) (*Preview, error) {
	parsed, fileHash, token, err := parseUpload(fileName, r)
	if err != nil {
		return nil, err
	}
	return s.buildPreview(ctx, tenantID, parsed, fileName, fileHash, token, accountID)
}

func (s *Service) Apply(ctx context.Context, tenantID, accountID, userID uuid.UUID, token, currency string) (*ApplyResult, error) {
	payload, err := parseToken(token)
	if err != nil {
		return nil, err
	}
	contentBytes, err := decodeContent(payload.Content)
	if err != nil {
		return nil, err
	}
	parsed, err := Parse(string(contentBytes))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(currency) != "" {
		parsed = parsedForCurrency(parsed, strings.ToUpper(strings.TrimSpace(currency)))
	}
	if len(parsed.Transactions) == 0 {
		return nil, httpx.NewValidationError("file contains no importable transactions")
	}
	accountCurrency, err := s.accountCurrency(ctx, tenantID, accountID)
	if err != nil {
		return nil, err
	}
	for _, row := range parsed.Transactions {
		if row.Currency != accountCurrency {
			return nil, httpx.NewValidationError(fmt.Sprintf("file currency %s does not match account currency %s", row.Currency, accountCurrency))
		}
	}

	existing, err := s.loadExisting(ctx, tenantID, accountID, parsed)
	if err != nil {
		return nil, err
	}
	classified := classify(parsed, existing)

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
	_, err = tx.Exec(ctx, `
		insert into import_batches (
			id, tenant_id, source_kind, file_name, file_hash, status,
			summary, created_by_user_id, started_at, finished_at
		) values (
			$1, $2, 'file_upload', $3, $4, 'applied',
			$5::jsonb, $6, $7, $7
		)
	`, batchID, tenantID, payload.FileName, payload.FileHash, string(summary), userID, s.now())
	if err != nil {
		return nil, fmt.Errorf("insert import batch: %w", err)
	}

	inserted := make([]uuid.UUID, 0, len(classified.importable))
	for _, incoming := range classified.importable {
		id := uuidx.New()
		rawJSON, _ := json.Marshal(incoming.Raw)
		_, err = tx.Exec(ctx, `
			insert into transactions (
				id, tenant_id, account_id, status, booked_at, value_at, posted_at,
				amount, currency, counterparty_raw, description, raw
			) values (
				$1, $2, $3, 'posted', $4, $5, $6,
				$7::numeric, $8, $9, $10, $11::jsonb
			)
		`, id, tenantID, accountID, incoming.BookedAt, incoming.ValueAt, incoming.PostedAt,
			incoming.Amount.String(), incoming.Currency, incoming.CounterpartyRaw, incoming.Description, string(rawJSON))
		if err != nil {
			return nil, fmt.Errorf("insert transaction: %w", err)
		}
		_, err = tx.Exec(ctx, `
			insert into source_refs (
				id, tenant_id, entity_type, entity_id, provider,
				import_batch_id, external_id, raw_payload, observed_at
			) values (
				$1, $2, 'transaction', $3, $4,
				$5, $6, $7::jsonb, $8
			)
		`, uuidx.New(), tenantID, id, incomingProvider(parsed.Profile), batchID,
			incoming.ExternalID, string(rawJSON), s.now())
		if err != nil {
			return nil, fmt.Errorf("insert source ref: %w", err)
		}
		inserted = append(inserted, id)
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

func (s *Service) ApplyPlan(ctx context.Context, tenantID, userID uuid.UUID, in ApplyPlanInput) (*ApplyResult, error) {
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
	parsed, err := Parse(string(contentBytes))
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
			accountCurrency, err := s.accountCurrency(ctx, tenantID, *group.AccountID)
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
			existing, err = s.loadExisting(ctx, tenantID, accountID, sub)
			if err != nil {
				return nil, err
			}
		}
		planned = append(planned, plannedGroup{
			in:         group,
			parsed:     sub,
			accountID:  accountID,
			classified: classify(sub, existing),
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
	_, err = tx.Exec(ctx, `
		insert into import_batches (
			id, tenant_id, source_kind, file_name, file_hash, status,
			summary, created_by_user_id, started_at, finished_at
		) values (
			$1, $2, 'file_upload', $3, $4, 'applied',
			$5::jsonb, $6, $7, $7
		)
	`, batchID, tenantID, payload.FileName, payload.FileHash, string(summary), userID, now)
	if err != nil {
		return nil, fmt.Errorf("insert import batch: %w", err)
	}

	out := &ApplyResult{BatchID: batchID}
	for _, group := range planned {
		accountID := group.accountID
		if accountID == uuid.Nil {
			accountID, err = s.createImportAccountTx(ctx, tx, tenantID, parsed.Institution, group.in)
			if err != nil {
				return nil, err
			}
		} else if group.in.Reactivate {
			// Opt-in reactivate: clear archived_at when the user explicitly
			// asked to resurface this account. No-op for non-archived rows.
			// updated_at is set by trigger. Without this opt-in, a re-import
			// merges new transactions into an archived account silently —
			// archive is a UI-only hide flag, not a write barrier.
			if _, err := tx.Exec(ctx, `
				update accounts
				set archived_at = null
				where tenant_id = $1 and id = $2 and archived_at is not null
			`, tenantID, accountID); err != nil {
				return nil, fmt.Errorf("unarchive import account: %w", err)
			}
		}
		ids, err := s.insertImportableTx(ctx, tx, tenantID, accountID, batchID, incomingProvider(group.parsed.Profile), group.classified.importable)
		if err != nil {
			return nil, err
		}
		out.InsertedCount += len(ids)
		out.DuplicateCount += len(group.classified.duplicates)
		out.ConflictCount += len(group.classified.conflicts)
		out.TransactionIDs = append(out.TransactionIDs, ids...)
		out.Conflicts = append(out.Conflicts, group.classified.conflicts...)
		if group.parsed.DateFrom != nil && group.parsed.DateTo != nil {
			if err := s.retireExplainedSynthetics(ctx, tx, tenantID, accountID, *group.parsed.DateFrom, *group.parsed.DateTo); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import plan: %w", err)
	}
	return out, nil
}

func (s *Service) createImportAccountTx(ctx context.Context, tx importTx, tenantID uuid.UUID, institution string, group ApplyPlanGroup) (uuid.UUID, error) {
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
	_, err := tx.Exec(ctx, `
		insert into accounts (
			id, tenant_id, name, kind, currency, institution,
			open_date, opening_balance, opening_balance_date,
			include_in_networth, include_in_savings_rate
		) values (
			$1, $2, $3, $4::account_kind, $5, $6,
			$7, $8::numeric, $9, true, $10
		)
	`, accountID, tenantID, strings.TrimSpace(group.Name), strings.TrimSpace(group.Kind), strings.ToUpper(strings.TrimSpace(group.Currency)), instPtr,
		openDate, strings.TrimSpace(group.OpeningBalance), openingBalanceDate, includeInSavingsRate)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert import account: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into account_balance_snapshots (
			id, tenant_id, account_id, as_of, balance, currency, source
		) values (
			$1, $2, $3, $4, $5::numeric, $6, 'opening'
		)
	`, snapshotID, tenantID, accountID, openingTS, strings.TrimSpace(group.OpeningBalance), strings.ToUpper(strings.TrimSpace(group.Currency)))
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert import opening snapshot: %w", err)
	}
	return accountID, nil
}

func (s *Service) insertImportableTx(ctx context.Context, tx importTx, tenantID, accountID, batchID uuid.UUID, provider string, rows []ParsedTransaction) ([]uuid.UUID, error) {
	inserted := make([]uuid.UUID, 0, len(rows))
	for _, incoming := range rows {
		id := uuidx.New()
		rawJSON, _ := json.Marshal(incoming.Raw)
		_, err := tx.Exec(ctx, `
			insert into transactions (
				id, tenant_id, account_id, status, booked_at, value_at, posted_at,
				amount, currency, counterparty_raw, description, raw
			) values (
				$1, $2, $3, 'posted', $4, $5, $6,
				$7::numeric, $8, $9, $10, $11::jsonb
			)
		`, id, tenantID, accountID, incoming.BookedAt, incoming.ValueAt, incoming.PostedAt,
			incoming.Amount.String(), incoming.Currency, incoming.CounterpartyRaw, incoming.Description, string(rawJSON))
		if err != nil {
			return nil, fmt.Errorf("insert transaction: %w", err)
		}
		_, err = tx.Exec(ctx, `
			insert into source_refs (
				id, tenant_id, entity_type, entity_id, provider,
				import_batch_id, external_id, raw_payload, observed_at
			) values (
				$1, $2, 'transaction', $3, $4,
				$5, $6, $7::jsonb, $8
			)
		`, uuidx.New(), tenantID, id, provider, batchID, incoming.ExternalID, string(rawJSON), s.now())
		if err != nil {
			return nil, fmt.Errorf("insert source ref: %w", err)
		}
		inserted = append(inserted, id)
	}
	return inserted, nil
}

func (s *Service) buildPreview(ctx context.Context, tenantID uuid.UUID, parsed ParsedFile, fileName, fileHash, token string, accountID *uuid.UUID) (*Preview, error) {
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
		accountCurrency, err := s.accountCurrency(ctx, tenantID, *accountID)
		if err != nil {
			return nil, err
		}
		if parsed.Currency != "" && parsed.Currency != accountCurrency {
			return nil, httpx.NewValidationError(fmt.Sprintf("file currency %s does not match account currency %s", parsed.Currency, accountCurrency))
		}
		existing, err := s.loadExisting(ctx, tenantID, *accountID, parsed)
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
	groups, err := s.currencyGroups(ctx, tenantID, parsed)
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

func (s *Service) currencyGroups(ctx context.Context, tenantID uuid.UUID, parsed ParsedFile) ([]CurrencyGroup, error) {
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

	accounts, err := s.loadImportAccountMatches(ctx, tenantID)
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
		candidates := importCandidates(accounts, parsed.Institution, k.currency)
		for i := range candidates {
			existing, err := s.loadExisting(ctx, tenantID, candidates[i].ID, sub)
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

func (s *Service) loadImportAccountMatches(ctx context.Context, tenantID uuid.UUID) ([]importAccountMatch, error) {
	// Archived accounts are kept in the candidate set so re-importing the
	// same file matches the account the user already imported into instead
	// of silently creating a duplicate. The wizard surfaces the archived
	// state and the apply path unarchives the account before importing.
	rows, err := s.pool.Query(ctx, `
		select id, name, currency, institution, archived_at
		from accounts
		where tenant_id = $1
		order by name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load import account matches: %w", err)
	}
	defer rows.Close()

	out := []importAccountMatch{}
	for rows.Next() {
		var a importAccountMatch
		var archivedAt *time.Time
		if err := rows.Scan(&a.ID, &a.Name, &a.Currency, &a.Institution, &archivedAt); err != nil {
			return nil, fmt.Errorf("scan import account match: %w", err)
		}
		a.Archived = archivedAt != nil
		out = append(out, a)
	}
	return out, rows.Err()
}

func importCandidates(accounts []importAccountMatch, institution, currency string) []AccountCandidate {
	var matches []AccountCandidate
	for _, account := range accounts {
		if account.Currency != currency {
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

func (s *Service) accountCurrency(ctx context.Context, tenantID, accountID uuid.UUID) (string, error) {
	var currency string
	err := s.pool.QueryRow(ctx, `select currency from accounts where tenant_id = $1 and id = $2`, tenantID, accountID).Scan(&currency)
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

func (s *Service) loadExisting(ctx context.Context, tenantID, accountID uuid.UUID, parsed ParsedFile) ([]existingTx, error) {
	if parsed.DateFrom == nil || parsed.DateTo == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select t.id, t.booked_at, t.posted_at, t.amount::text, t.currency,
		       coalesce(t.description, t.counterparty_raw, ''),
		       sr.external_id,
		       coalesce(t.raw->>'synthetic' = 'balance_reconcile', false)
		from transactions t
		left join source_refs sr
		  on sr.tenant_id = t.tenant_id
		 and sr.entity_type = 'transaction'
		 and sr.entity_id = t.id
		 and sr.provider = $6
		where t.tenant_id = $1
		  and t.account_id = $2
		  and t.booked_at between $3 and $4
		  and t.status <> 'voided'
		  and t.currency = $5
	`, tenantID, accountID, *parsed.DateFrom, *parsed.DateTo, parsed.Currency, incomingProvider(parsed.Profile))
	if err != nil {
		return nil, fmt.Errorf("query existing transactions: %w", err)
	}
	defer rows.Close()
	out := []existingTx{}
	for rows.Next() {
		var e existingTx
		var amount string
		if err := rows.Scan(&e.ID, &e.BookedAt, &e.PostedAt, &amount, &e.Currency, &e.Description, &e.SourceID, &e.Synthetic); err != nil {
			return nil, err
		}
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return nil, err
		}
		e.Amount = d
		out = append(out, e)
	}
	return out, rows.Err()
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
	return residualExplainedByExisting(incoming.BookedAt, incoming.Currency, residual, real)
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
		if !fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue
		}
		if incomingDesc != "" && normalizeDescription(e.Description) != incomingDesc {
			return previewConflict("description_mismatch", incoming, e), true
		}
	}
	// Pass 2: review-window drift on amount+currency, regardless of description.
	for _, e := range existing {
		if fuzzyStableMatch(incoming, e, autoDedupDays) {
			continue // already handled
		}
		if fuzzyStableMatch(incoming, e, reviewDedupDays) {
			return previewConflict("date_drift", incoming, e), true
		}
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
func (s *Service) retireExplainedSynthetics(ctx context.Context, tx importTx, tenantID, accountID uuid.UUID, dateFrom, dateTo time.Time) error {
	rows, err := tx.Query(ctx, `
		select t.id, t.booked_at, t.posted_at, t.amount::text, t.currency,
		       coalesce(t.raw->>'synthetic_residual', t.amount::text)
		from transactions t
		where t.tenant_id = $1
		  and t.account_id = $2
		  and t.status = 'posted'
		  and t.raw->>'synthetic' = 'balance_reconcile'
		  and t.booked_at between $3 - interval '7 days' and $4 + interval '7 days'
	`, tenantID, accountID, dateFrom, dateTo)
	if err != nil {
		return fmt.Errorf("scan synthetic rows: %w", err)
	}
	type synthCandidate struct {
		id       uuid.UUID
		bookedAt time.Time
		currency string
		residual decimal.Decimal
	}
	var candidates []synthCandidate
	for rows.Next() {
		var c synthCandidate
		var amount, residualStr string
		var posted *time.Time
		if err := rows.Scan(&c.id, &c.bookedAt, &posted, &amount, &c.currency, &residualStr); err != nil {
			rows.Close()
			return fmt.Errorf("scan synthetic row: %w", err)
		}
		residual, err := decimal.NewFromString(residualStr)
		if err != nil {
			continue
		}
		c.residual = residual
		candidates = append(candidates, c)
	}
	rows.Close()
	if rowsErr := rows.Err(); rowsErr != nil {
		return rowsErr
	}
	if len(candidates) == 0 {
		return nil
	}
	for _, c := range candidates {
		from := c.bookedAt.Add(-time.Duration(reviewDedupDays) * 24 * time.Hour)
		to := c.bookedAt.Add(time.Duration(reviewDedupDays) * 24 * time.Hour)
		realRows, err := tx.Query(ctx, `
			select id, booked_at, posted_at, amount::text, currency,
			       coalesce(description, counterparty_raw, '')
			from transactions
			where tenant_id = $1
			  and account_id = $2
			  and status = 'posted'
			  and currency = $3
			  and booked_at between $4 and $5
			  and coalesce(raw->>'synthetic', '') <> 'balance_reconcile'
			  and id <> $6
		`, tenantID, accountID, c.currency, from, to, c.id)
		if err != nil {
			return fmt.Errorf("scan real rows for synthetic: %w", err)
		}
		var existing []existingTx
		for realRows.Next() {
			var e existingTx
			var amount string
			if err := realRows.Scan(&e.ID, &e.BookedAt, &e.PostedAt, &amount, &e.Currency, &e.Description); err != nil {
				realRows.Close()
				return err
			}
			d, err := decimal.NewFromString(amount)
			if err != nil {
				continue
			}
			e.Amount = d
			existing = append(existing, e)
		}
		realRows.Close()
		if !residualExplainedByExisting(c.bookedAt, c.currency, c.residual, existing) {
			continue
		}
		if _, err := tx.Exec(ctx, `update transactions set status = 'voided' where id = $1 and tenant_id = $2`, c.id, tenantID); err != nil {
			return fmt.Errorf("void synthetic %s: %w", c.id, err)
		}
	}
	return nil
}
