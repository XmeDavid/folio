// Package transactions owns the Folio ledger transaction aggregate: inserting,
// reading, updating, and deleting simple (non-split) transactions. Splits via
// transaction_lines are intentionally out of scope for this slice.
//
// Balance is not cached on accounts; the accounts package derives it by taking
// the latest account_balance_snapshots row and adding the sum of transactions
// booked on or after that snapshot's date (see accounts/service.go). Transaction
// writes here therefore affect account balance immediately on the next read.
package transactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/classification"
	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// decimalToNumeric converts a decimal.Decimal to pgtype.Numeric for sqlc params.
func decimalToNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// nilableString converts an empty string to nil for nullable text columns.
func nilableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// DefaultListLimit and MaxListLimit bound the GET /transactions page size.
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// validStatuses mirrors the transaction_status enum in db/migrations.
var validStatuses = map[string]bool{
	"draft":      true,
	"posted":     true,
	"reconciled": true,
	"voided":     true,
}

// IsValidStatus returns true when s is a recognised transaction_status.
func IsValidStatus(s string) bool { return validStatuses[s] }

// dateOnly is the wire format for date-only fields.
const dateOnly = "2006-01-02"

// Transaction is the read-model representation returned by the API.
type Transaction struct {
	ID               uuid.UUID  `json:"id"`
	WorkspaceID         uuid.UUID  `json:"workspaceId"`
	AccountID        uuid.UUID  `json:"accountId"`
	Status           string     `json:"status"`
	BookedAt         time.Time  `json:"bookedAt"`
	ValueAt          *time.Time `json:"valueAt,omitempty"`
	PostedAt         *time.Time `json:"postedAt,omitempty"`
	Amount           string     `json:"amount"`
	Currency         string     `json:"currency"`
	OriginalAmount   *string    `json:"originalAmount,omitempty"`
	OriginalCurrency *string    `json:"originalCurrency,omitempty"`
	MerchantID       *uuid.UUID `json:"merchantId,omitempty"`
	CategoryID       *uuid.UUID `json:"categoryId,omitempty"`
	CounterpartyRaw  *string    `json:"counterpartyRaw,omitempty"`
	Description      *string    `json:"description,omitempty"`
	Notes            *string    `json:"notes,omitempty"`
	CountAsExpense   *bool      `json:"countAsExpense,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// CreateInput is the validated input to Create.
type CreateInput struct {
	AccountID       uuid.UUID
	Status          string
	BookedAt        time.Time
	ValueAt         *time.Time
	PostedAt        *time.Time
	Amount          decimal.Decimal
	Currency        string
	CategoryID      *uuid.UUID
	MerchantID      *uuid.UUID
	CounterpartyRaw *string
	Description     *string
	Notes           *string
	CountAsExpense  *bool
}

// normalize trims, validates, and applies defaults to a CreateInput. Pure
// function — tested without the database. It does NOT verify that account_id
// belongs to the workspace or that currency matches the account (Service.Create
// does that against live data).
func (in CreateInput) normalize() (CreateInput, error) {
	if in.AccountID == uuid.Nil {
		return in, httpx.NewValidationError("accountId is required")
	}
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status == "" {
		in.Status = "posted"
	}
	if !validStatuses[in.Status] {
		return in, httpx.NewValidationError(fmt.Sprintf("status %q is not a valid transaction_status", in.Status))
	}
	if in.BookedAt.IsZero() {
		return in, httpx.NewValidationError("bookedAt is required")
	}
	cur, err := money.ParseCurrency(in.Currency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.Currency = string(cur)
	return in, nil
}

// PatchInput is the validated input to Update. Pointer fields mean "absent"
// when nil. For string fields with database-nullable targets (description,
// notes, counterpartyRaw) and for uuid fields (categoryId, merchantId), an
// explicit empty string means "set to NULL". bookedAt is YYYY-MM-DD;
// valueAt is YYYY-MM-DD with "" to clear; postedAt is RFC3339 timestamp
// with "" to clear. accountId is not patchable.
type PatchInput struct {
	Status          *string
	BookedAt        *string
	ValueAt         *string
	PostedAt        *string
	Amount          *string
	Currency        *string
	CategoryID      *string
	MerchantID      *string
	CounterpartyRaw *string
	Description     *string
	Notes           *string
	// CountAsExpense uses json.RawMessage so the handler can distinguish
	// absent (len == 0) from explicit null from true/false. normalize()
	// parses the three states into countAsExpenseSet/countAsExpenseNull/
	// countAsExpenseValue at the service boundary.
	CountAsExpense json.RawMessage
}

type patchNormalized struct {
	statusSet          bool
	status             string
	bookedAtSet        bool
	bookedAt           time.Time
	valueAtSet         bool
	valueAtNull        bool
	valueAt            time.Time
	postedAtSet        bool
	postedAtNull       bool
	postedAt           time.Time
	amountSet          bool
	amount             decimal.Decimal
	currencySet        bool
	currency           string
	categoryIDSet      bool
	categoryIDNull     bool
	categoryID         uuid.UUID
	merchantIDSet      bool
	merchantIDNull     bool
	merchantID         uuid.UUID
	counterpartySet    bool
	counterpartyNull   bool
	counterparty       string
	descriptionSet     bool
	descriptionNull    bool
	description        string
	notesSet           bool
	notesNull          bool
	notes              string
	countAsExpenseSet  bool
	countAsExpenseNull bool
	countAsExpense     bool
}

func parseOptionalUUID(field string, raw *string) (bool, bool, uuid.UUID, error) {
	if raw == nil {
		return false, false, uuid.Nil, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return true, true, uuid.Nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return true, false, uuid.Nil, httpx.NewValidationError(field + " must be a UUID")
	}
	return true, false, id, nil
}

func parseOptionalDate(field string, raw *string, allowClear bool) (set bool, isNull bool, value time.Time, err error) {
	if raw == nil {
		return false, false, time.Time{}, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		if !allowClear {
			return true, false, time.Time{}, httpx.NewValidationError(field + " is required")
		}
		return true, true, time.Time{}, nil
	}
	t, e := time.Parse(dateOnly, s)
	if e != nil {
		return true, false, time.Time{}, httpx.NewValidationError(field + " must be YYYY-MM-DD")
	}
	return true, false, t, nil
}

func (in PatchInput) normalize() (patchNormalized, error) {
	var out patchNormalized

	if in.Status != nil {
		s := strings.ToLower(strings.TrimSpace(*in.Status))
		if !validStatuses[s] {
			return out, httpx.NewValidationError(fmt.Sprintf("status %q is not a valid transaction_status", s))
		}
		out.statusSet = true
		out.status = s
	}

	if set, isNull, t, err := parseOptionalDate("bookedAt", in.BookedAt, false); err != nil {
		return out, err
	} else if set {
		out.bookedAtSet = true
		_ = isNull
		out.bookedAt = t
	}

	if set, isNull, t, err := parseOptionalDate("valueAt", in.ValueAt, true); err != nil {
		return out, err
	} else if set {
		out.valueAtSet = true
		out.valueAtNull = isNull
		out.valueAt = t
	}

	if in.PostedAt != nil {
		s := strings.TrimSpace(*in.PostedAt)
		out.postedAtSet = true
		if s == "" {
			out.postedAtNull = true
		} else {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return out, httpx.NewValidationError("postedAt must be RFC3339 timestamp")
			}
			out.postedAt = t
		}
	}

	if in.Amount != nil {
		d, err := decimal.NewFromString(strings.TrimSpace(*in.Amount))
		if err != nil {
			return out, httpx.NewValidationError("amount must be a decimal string")
		}
		out.amountSet = true
		out.amount = d
	}

	if in.Currency != nil {
		cur, err := money.ParseCurrency(*in.Currency)
		if err != nil {
			return out, httpx.NewValidationError(err.Error())
		}
		out.currencySet = true
		out.currency = string(cur)
	}

	if set, isNull, id, err := parseOptionalUUID("categoryId", in.CategoryID); err != nil {
		return out, err
	} else if set {
		out.categoryIDSet = true
		out.categoryIDNull = isNull
		out.categoryID = id
	}

	if set, isNull, id, err := parseOptionalUUID("merchantId", in.MerchantID); err != nil {
		return out, err
	} else if set {
		out.merchantIDSet = true
		out.merchantIDNull = isNull
		out.merchantID = id
	}

	if in.CounterpartyRaw != nil {
		out.counterpartySet = true
		if *in.CounterpartyRaw == "" {
			out.counterpartyNull = true
		} else {
			out.counterparty = *in.CounterpartyRaw
		}
	}
	if in.Description != nil {
		out.descriptionSet = true
		if *in.Description == "" {
			out.descriptionNull = true
		} else {
			out.description = *in.Description
		}
	}
	if in.Notes != nil {
		out.notesSet = true
		if *in.Notes == "" {
			out.notesNull = true
		} else {
			out.notes = *in.Notes
		}
	}

	if len(in.CountAsExpense) > 0 {
		out.countAsExpenseSet = true
		trimmed := strings.TrimSpace(string(in.CountAsExpense))
		if trimmed == "null" {
			out.countAsExpenseNull = true
		} else {
			var b bool
			if err := json.Unmarshal(in.CountAsExpense, &b); err != nil {
				return out, httpx.NewValidationError("countAsExpense must be boolean or null")
			}
			out.countAsExpense = b
		}
	}

	return out, nil
}

// Service is the transactions service.
type Service struct {
	pool     *pgxpool.Pool
	classSvc *classification.Service
}

// NewService returns a Service backed by pool. The classification service is
// used by Create/Update to resolve a counterparty_raw to a merchant and to
// inherit a merchant's default category when the caller did not pass one.
func NewService(pool *pgxpool.Pool, classSvc *classification.Service) *Service {
	return &Service{pool: pool, classSvc: classSvc}
}

// Create inserts a new transaction. Returns 400 (ValidationError) when the
// transaction currency does not match the account currency and 404 when the
// account does not exist for the workspace.
func (s *Service) Create(ctx context.Context, workspaceID uuid.UUID, raw CreateInput) (*Transaction, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	q := dbq.New(s.pool)

	// Pre-check account + currency so we can return a clean 400/404 rather
	// than leaking the composite-FK or trigger exception text.
	accountCurrency, err := q.GetAccountCurrency(ctx, dbq.GetAccountCurrencyParams{
		WorkspaceID: workspaceID,
		ID:          in.AccountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account")
		}
		return nil, fmt.Errorf("load account: %w", err)
	}
	if in.Currency != accountCurrency {
		return nil, httpx.NewValidationError(fmt.Sprintf(
			"currency %s does not match account currency %s", in.Currency, accountCurrency))
	}

	// Resolve merchant from counterparty_raw when caller didn't pass one.
	// Manual override wins: if caller passed a merchantId we don't second-
	// guess them. If we resolve a merchant whose default_category_id is set
	// AND the caller also did not pass a categoryId, inherit the merchant's
	// default category as part of the same insert (avoids a second UPDATE).
	if s.classSvc != nil && in.MerchantID == nil && in.CounterpartyRaw != nil && strings.TrimSpace(*in.CounterpartyRaw) != "" {
		m, err := s.classSvc.AttachByRaw(ctx, workspaceID, *in.CounterpartyRaw)
		if err != nil {
			return nil, fmt.Errorf("attach merchant: %w", err)
		}
		if m != nil {
			mid := m.ID
			in.MerchantID = &mid
			if in.CategoryID == nil && m.DefaultCategoryID != nil {
				cid := *m.DefaultCategoryID
				in.CategoryID = &cid
			}
		}
	}

	id := uuidx.New()
	// We deliberately do not use q.InsertTransaction here because its
	// generated RETURNING row models original_amount/original_currency as
	// non-nullable strings, which fails to scan when those columns are NULL
	// (the common case for non-FX transactions). scanRow uses *string and
	// matches the same canonical transactionCols projection as Update/Get.
	insertSQL := `
		insert into transactions (
			id, workspace_id, account_id, status, booked_at, value_at, posted_at,
			amount, currency, merchant_id, category_id,
			counterparty_raw, description, notes, count_as_expense
		) values (
			$1, $2, $3, $4::transaction_status, $5, $6, $7,
			$8::numeric, $9, $10, $11,
			$12, $13, $14, $15
		)
		returning ` + transactionCols
	t, err := s.scanTransaction(ctx, s.pool, insertSQL,
		id, workspaceID, in.AccountID, in.Status, in.BookedAt, in.ValueAt, in.PostedAt,
		in.Amount.String(), in.Currency, in.MerchantID, in.CategoryID,
		in.CounterpartyRaw, in.Description, in.Notes, in.CountAsExpense,
	)
	if err != nil {
		return nil, mapInsertError(err)
	}
	return t, nil
}

// Get returns a single transaction by id, scoped to workspaceID.
//
// We use a hand-rolled SELECT instead of dbq.GetTransaction because the
// generated row models original_amount/original_currency as non-nullable
// strings, which fails to scan when those columns are NULL.
func (s *Service) Get(ctx context.Context, workspaceID, id uuid.UUID) (*Transaction, error) {
	t, err := s.scanTransaction(ctx, s.pool,
		`select `+transactionCols+` from transactions where workspace_id = $1 and id = $2`,
		workspaceID, id,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("transaction")
		}
		return nil, err
	}
	return t, nil
}

// ListFilter bounds the GET /transactions listing.
type ListFilter struct {
	AccountID     *uuid.UUID
	CategoryID    *uuid.UUID
	MerchantID    *uuid.UUID
	From          *time.Time
	To            *time.Time
	Status        *string
	Search        *string
	MinAmount     *decimal.Decimal
	MaxAmount     *decimal.Decimal
	Uncategorized bool
	// ExcludeInvestmentAccounts hides transactions booked on investment
	// (brokerage-kind) accounts. Those moves are surfaced in the Investments
	// tab; opting in keeps the regular ledger uncluttered.
	ExcludeInvestmentAccounts bool
	Limit                     int
	Offset                    int
}

// List returns transactions for workspaceID matching f. Ordered by booked_at desc.
func (s *Service) List(ctx context.Context, workspaceID uuid.UUID, f ListFilter) ([]Transaction, error) {
	if f.Limit <= 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit > MaxListLimit {
		f.Limit = MaxListLimit
	}

	args := []any{workspaceID}
	clauses := []string{"workspace_id = $1"}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.AccountID != nil {
		clauses = append(clauses, "account_id = "+next(*f.AccountID))
	}
	if f.CategoryID != nil {
		clauses = append(clauses, "category_id = "+next(*f.CategoryID))
	}
	if f.MerchantID != nil {
		clauses = append(clauses, "merchant_id = "+next(*f.MerchantID))
	}
	if f.From != nil {
		clauses = append(clauses, "booked_at >= "+next(*f.From))
	}
	if f.To != nil {
		clauses = append(clauses, "booked_at <= "+next(*f.To))
	}
	if f.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*f.Status))
		if !validStatuses[status] {
			return nil, httpx.NewValidationError(fmt.Sprintf("status %q is not a valid transaction_status", status))
		}
		clauses = append(clauses, "status = "+next(status)+"::transaction_status")
	}
	if f.Search != nil {
		search := strings.TrimSpace(*f.Search)
		if search != "" {
			needle := "%" + strings.ToLower(search) + "%"
			clauses = append(clauses, `(lower(coalesce(description, '')) like `+next(needle)+
				` or lower(coalesce(counterparty_raw, '')) like `+next(needle)+
				` or lower(coalesce(notes, '')) like `+next(needle)+`)`)
		}
	}
	if f.MinAmount != nil {
		clauses = append(clauses, "amount >= "+next(f.MinAmount.String())+"::numeric")
	}
	if f.MaxAmount != nil {
		clauses = append(clauses, "amount <= "+next(f.MaxAmount.String())+"::numeric")
	}
	if f.Uncategorized {
		// Uncategorized queue: transaction carries no category and has no
		// per-line classifications. Split transactions (which have lines)
		// are considered categorized even when transactions.category_id is
		// NULL, per spec §5.3.
		clauses = append(clauses,
			"category_id is null and not exists (select 1 from transaction_lines tl where tl.transaction_id = transactions.id)")
	}
	if f.ExcludeInvestmentAccounts {
		clauses = append(clauses,
			"not exists (select 1 from accounts a where a.id = transactions.account_id and a.kind = 'brokerage')")
	}
	limitPH := next(f.Limit)
	offsetPH := next(f.Offset)

	q := `select ` + transactionCols + ` from transactions where ` +
		strings.Join(clauses, " and ") +
		` order by booked_at desc, id desc limit ` + limitPH + ` offset ` + offsetPH

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}
	defer rows.Close()

	out := make([]Transaction, 0)
	for rows.Next() {
		var t Transaction
		if err := scanRow(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Update applies a PATCH to a transaction. Returns the updated Transaction.
// accountId is intentionally immutable in this slice.
func (s *Service) Update(ctx context.Context, workspaceID, id uuid.UUID, raw PatchInput) (*Transaction, error) {
	p, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	// Load the existing row to enforce cross-field invariants before hitting
	// the DB trigger (currency must match account currency).
	existing, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	if p.currencySet {
		accountCurrency, err := dbq.New(s.pool).GetAccountCurrency(ctx, dbq.GetAccountCurrencyParams{
			WorkspaceID: workspaceID,
			ID:          existing.AccountID,
		})
		if err != nil {
			return nil, fmt.Errorf("load account: %w", err)
		}
		if p.currency != accountCurrency {
			return nil, httpx.NewValidationError(fmt.Sprintf(
				"currency %s does not match account currency %s", p.currency, accountCurrency))
		}
	}

	// If the patch is setting a non-null merchant_id and not also setting a
	// category, fetch the merchant's default category and apply it when the
	// existing transaction has no category. This mirrors the import path's
	// "manual override wins" semantics: if the caller explicitly passed a
	// categoryId (even null) we don't auto-apply.
	var inheritedCategoryID *uuid.UUID
	if p.merchantIDSet && !p.merchantIDNull && !p.categoryIDSet && existing.CategoryID == nil {
		var defaultCategoryID *uuid.UUID
		err := s.pool.QueryRow(ctx,
			`select default_category_id from merchants where workspace_id = $1 and id = $2`,
			workspaceID, p.merchantID,
		).Scan(&defaultCategoryID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("read merchant default category: %w", err)
		}
		if defaultCategoryID != nil {
			inheritedCategoryID = defaultCategoryID
		}
	}

	sets := make([]string, 0, 12)
	args := []any{workspaceID, id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if p.statusSet {
		sets = append(sets, "status = "+next(p.status)+"::transaction_status")
	}
	if p.bookedAtSet {
		sets = append(sets, "booked_at = "+next(p.bookedAt))
	}
	if p.valueAtSet {
		if p.valueAtNull {
			sets = append(sets, "value_at = null")
		} else {
			sets = append(sets, "value_at = "+next(p.valueAt))
		}
	}
	if p.postedAtSet {
		if p.postedAtNull {
			sets = append(sets, "posted_at = null")
		} else {
			sets = append(sets, "posted_at = "+next(p.postedAt))
		}
	}
	if p.amountSet {
		sets = append(sets, "amount = "+next(p.amount.String())+"::numeric")
	}
	if p.currencySet {
		sets = append(sets, "currency = "+next(p.currency))
	}
	if p.categoryIDSet {
		if p.categoryIDNull {
			sets = append(sets, "category_id = null")
		} else {
			sets = append(sets, "category_id = "+next(p.categoryID))
		}
	}
	if p.merchantIDSet {
		if p.merchantIDNull {
			sets = append(sets, "merchant_id = null")
		} else {
			sets = append(sets, "merchant_id = "+next(p.merchantID))
			if inheritedCategoryID != nil {
				sets = append(sets, "category_id = "+next(*inheritedCategoryID))
			}
		}
	}
	if p.counterpartySet {
		if p.counterpartyNull {
			sets = append(sets, "counterparty_raw = null")
		} else {
			sets = append(sets, "counterparty_raw = "+next(p.counterparty))
		}
	}
	if p.descriptionSet {
		if p.descriptionNull {
			sets = append(sets, "description = null")
		} else {
			sets = append(sets, "description = "+next(p.description))
		}
	}
	if p.notesSet {
		if p.notesNull {
			sets = append(sets, "notes = null")
		} else {
			sets = append(sets, "notes = "+next(p.notes))
		}
	}
	if p.countAsExpenseSet {
		if p.countAsExpenseNull {
			sets = append(sets, "count_as_expense = null")
		} else {
			sets = append(sets, "count_as_expense = "+next(p.countAsExpense))
		}
	}

	if len(sets) == 0 {
		return existing, nil
	}

	q := fmt.Sprintf(`
		update transactions set %s
		where workspace_id = $1 and id = $2
		returning %s
	`, strings.Join(sets, ", "), transactionCols)

	t, err := s.scanTransaction(ctx, s.pool, q, args...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("transaction")
		}
		return nil, mapInsertError(err)
	}
	return t, nil
}

// Delete hard-deletes a transaction. Returns NotFoundError when no row
// matches (workspaceID, id).
func (s *Service) Delete(ctx context.Context, workspaceID, id uuid.UUID) error {
	n, err := dbq.New(s.pool).DeleteTransaction(ctx, dbq.DeleteTransactionParams{
		WorkspaceID: workspaceID,
		ID:          id,
	})
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("transaction")
	}
	return nil
}

// transactionCols is the canonical SELECT/RETURNING column list so that
// scanTransaction/scanRow can stay in lock-step.
const transactionCols = `
	id, workspace_id, account_id, status::text, booked_at, value_at, posted_at,
	amount::text, currency, original_amount::text, original_currency::text,
	merchant_id, category_id, counterparty_raw, description, notes,
	count_as_expense, created_at, updated_at
`

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) scanTransaction(ctx context.Context, q queryer, sql string, args ...any) (*Transaction, error) {
	row := q.QueryRow(ctx, sql, args...)
	var t Transaction
	if err := scanRow(row, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanRow(r rowScanner, t *Transaction) error {
	return r.Scan(
		&t.ID, &t.WorkspaceID, &t.AccountID, &t.Status,
		&t.BookedAt, &t.ValueAt, &t.PostedAt,
		&t.Amount, &t.Currency, &t.OriginalAmount, &t.OriginalCurrency,
		&t.MerchantID, &t.CategoryID,
		&t.CounterpartyRaw, &t.Description, &t.Notes,
		&t.CountAsExpense, &t.CreatedAt, &t.UpdatedAt,
	)
}

// mapInsertError translates known Postgres errors (FK violations, the
// currency-match trigger) into ValidationError so the HTTP layer emits 400.
func mapInsertError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503": // foreign_key_violation
			return httpx.NewValidationError("referenced entity does not exist for this workspace")
		case "23514": // check_violation
			return httpx.NewValidationError(pgErr.Message)
		case "P0001": // raise_exception from triggers (currency mismatch, leaf, etc.)
			return httpx.NewValidationError(pgErr.Message)
		}
	}
	return fmt.Errorf("transaction write: %w", err)
}

