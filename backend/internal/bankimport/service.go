package bankimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

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

func (s *Service) Apply(ctx context.Context, tenantID, accountID, userID uuid.UUID, token string) (*ApplyResult, error) {
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
	}
	return p, nil
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
	Amount      decimal.Decimal
	Currency    string
	Description string
	SourceID    *string
}

func (s *Service) loadExisting(ctx context.Context, tenantID, accountID uuid.UUID, parsed ParsedFile) ([]existingTx, error) {
	if parsed.DateFrom == nil || parsed.DateTo == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		select t.id, t.booked_at, t.amount::text, t.currency,
		       coalesce(t.description, t.counterparty_raw, ''),
		       sr.external_id
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
		if err := rows.Scan(&e.ID, &e.BookedAt, &amount, &e.Currency, &e.Description, &e.SourceID); err != nil {
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

func duplicateBySource(incoming ParsedTransaction, existing []existingTx) bool {
	for _, e := range existing {
		if e.SourceID != nil && *e.SourceID == incoming.ExternalID {
			return true
		}
	}
	return false
}

func duplicateByFingerprint(incoming ParsedTransaction, existing []existingTx) bool {
	for _, e := range existing {
		if stableMatch(incoming, e) && normalizeText(valueOf(incoming.Description)) == normalizeText(e.Description) {
			return true
		}
	}
	return false
}

func conflictByStableFields(incoming ParsedTransaction, existing []existingTx) (ConflictPreview, bool) {
	for _, e := range existing {
		if stableMatch(incoming, e) {
			return ConflictPreview{
				Incoming: previewRow(incoming),
				Existing: PreviewRow{
					BookedAt:    e.BookedAt.Format(dateOnly),
					Amount:      e.Amount.String(),
					Currency:    e.Currency,
					Description: e.Description,
				},
			}, true
		}
	}
	return ConflictPreview{}, false
}

func stableMatch(incoming ParsedTransaction, existing existingTx) bool {
	return incoming.BookedAt.Equal(existing.BookedAt) &&
		incoming.Currency == existing.Currency &&
		incoming.Amount.Equal(existing.Amount)
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
