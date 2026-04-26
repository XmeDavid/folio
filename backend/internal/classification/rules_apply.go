package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// RuleApplyResult is returned by ApplyRulesToTransaction. When a rule
// matches, RuleID identifies it and Transaction carries the post-apply
// read-model. When no rule matches, Matched is false and Transaction is
// the unchanged row (for convenience).
type RuleApplyResult struct {
	Matched     bool                `json:"matched"`
	RuleID      *uuid.UUID          `json:"ruleId,omitempty"`
	Transaction *AppliedTransaction `json:"transaction,omitempty"`
}

// AppliedTransaction is the post-apply transaction shape. Wire shape mirrors
// the openapi Transaction schema so the client can cache by the same key.
// We re-declare the type rather than import transactions.Transaction to
// avoid a cross-package cycle (classification already writes transaction
// tags and now category/merchant/count_as_expense directly).
type AppliedTransaction struct {
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

// transactionSnapshot is the read-shape used internally by the rule engine.
// It carries only the fields rules match on or write to, plus the UUID.
type transactionSnapshot struct {
	ID              uuid.UUID
	WorkspaceID        uuid.UUID
	AccountID       uuid.UUID
	Amount          decimal.Decimal
	CounterpartyRaw *string
	Description     *string
	MerchantID      *uuid.UUID
	CategoryID      *uuid.UUID
	CountAsExpense  *bool
}

// RuleMatches is the pure matching predicate used by the engine. Exported
// so unit tests can verify the DSL semantics without a database.
func RuleMatches(r *Rule, tx *transactionSnapshot) bool {
	w := r.When
	if w.AccountID != nil && *w.AccountID != tx.AccountID {
		return false
	}
	if w.MerchantID != nil {
		if tx.MerchantID == nil || *w.MerchantID != *tx.MerchantID {
			return false
		}
	}
	if w.CounterpartyContains != nil {
		if tx.CounterpartyRaw == nil {
			return false
		}
		hay := strings.ToLower(*tx.CounterpartyRaw)
		if !strings.Contains(hay, *w.CounterpartyContains) {
			return false
		}
	}
	if w.DescriptionContains != nil {
		if tx.Description == nil {
			return false
		}
		hay := strings.ToLower(*tx.Description)
		if !strings.Contains(hay, *w.DescriptionContains) {
			return false
		}
	}
	if w.AmountMin != nil {
		minD, err := decimal.NewFromString(*w.AmountMin)
		if err != nil {
			return false
		}
		if tx.Amount.LessThan(minD) {
			return false
		}
	}
	if w.AmountMax != nil {
		maxD, err := decimal.NewFromString(*w.AmountMax)
		if err != nil {
			return false
		}
		if tx.Amount.GreaterThan(maxD) {
			return false
		}
	}
	if w.AmountSign != nil {
		switch *w.AmountSign {
		case "debit":
			if !tx.Amount.IsNegative() {
				return false
			}
		case "credit":
			if !tx.Amount.IsPositive() {
				return false
			}
		}
	}
	return true
}

// ApplyRulesToTransaction evaluates enabled rules in priority ASC, created_at
// ASC order against the transaction and applies the first match. Manual
// overrides win: category_id and merchant_id are written only when currently
// null, count_as_expense only when currently null; tags are always added
// idempotently. last_matched_at is stamped on the matched rule.
//
// The whole flow runs in a single DB transaction so a partial apply can't
// leak (e.g. tags inserted without the field updates).
func (s *Service) ApplyRulesToTransaction(ctx context.Context, workspaceID, transactionID uuid.UUID) (*RuleApplyResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	snap, err := loadTransactionSnapshot(ctx, tx, workspaceID, transactionID)
	if err != nil {
		return nil, err
	}

	rules, err := loadEnabledRules(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}

	var matched *Rule
	for i := range rules {
		if RuleMatches(&rules[i], snap) {
			matched = &rules[i]
			break
		}
	}

	if matched == nil {
		// Nothing to do; return the current transaction view.
		applied, err := loadAppliedTransaction(ctx, tx, workspaceID, transactionID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return &RuleApplyResult{Matched: false, Transaction: applied}, nil
	}

	if err := applyThenToTransaction(ctx, tx, workspaceID, snap, matched.Then); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		update categorization_rules set last_matched_at = now()
		where workspace_id = $1 and id = $2
	`, workspaceID, matched.ID); err != nil {
		return nil, fmt.Errorf("stamp last_matched_at: %w", err)
	}

	applied, err := loadAppliedTransaction(ctx, tx, workspaceID, transactionID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	ruleID := matched.ID
	return &RuleApplyResult{Matched: true, RuleID: &ruleID, Transaction: applied}, nil
}

func loadTransactionSnapshot(ctx context.Context, tx pgx.Tx, workspaceID, transactionID uuid.UUID) (*transactionSnapshot, error) {
	var snap transactionSnapshot
	var amountStr string
	err := tx.QueryRow(ctx, `
		select id, workspace_id, account_id, amount::text,
		       counterparty_raw, description, merchant_id, category_id, count_as_expense
		from transactions
		where workspace_id = $1 and id = $2
	`, workspaceID, transactionID).Scan(
		&snap.ID, &snap.WorkspaceID, &snap.AccountID, &amountStr,
		&snap.CounterpartyRaw, &snap.Description,
		&snap.MerchantID, &snap.CategoryID, &snap.CountAsExpense,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("transaction")
		}
		return nil, fmt.Errorf("load transaction: %w", err)
	}
	d, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("parse amount %q: %w", amountStr, err)
	}
	snap.Amount = d
	return &snap, nil
}

func loadEnabledRules(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) ([]Rule, error) {
	rows, err := tx.Query(ctx,
		`select `+rulesCols+` from categorization_rules
		 where workspace_id = $1 and enabled = true
		 order by priority asc, created_at asc`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()
	out := make([]Rule, 0)
	for rows.Next() {
		var r Rule
		if err := scanRule(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// applyThenToTransaction writes the rule's then-clause to the transaction
// row and its tag set. Manual overrides win: we only write category_id,
// merchant_id, and count_as_expense when those fields are currently NULL.
// Tags are always added idempotently via ON CONFLICT.
func applyThenToTransaction(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, snap *transactionSnapshot, then RuleThen) error {
	sets := make([]string, 0, 3)
	args := []any{workspaceID, snap.ID}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if then.CategoryID != nil && snap.CategoryID == nil {
		sets = append(sets, "category_id = "+next(*then.CategoryID))
	}
	if then.MerchantID != nil && snap.MerchantID == nil {
		sets = append(sets, "merchant_id = "+next(*then.MerchantID))
	}
	if then.CountAsExpenseSet && snap.CountAsExpense == nil {
		if then.CountAsExpense == nil {
			// Rule explicitly sets null — no-op when already null.
		} else {
			sets = append(sets, "count_as_expense = "+next(*then.CountAsExpense))
		}
	}

	if len(sets) > 0 {
		q := fmt.Sprintf(`update transactions set %s
			where workspace_id = $1 and id = $2`, strings.Join(sets, ", "))
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return mapInsertErrorForApply(err)
		}
	}

	for _, tagID := range then.AddTagIDs {
		if _, err := tx.Exec(ctx, `
			insert into transaction_tags (transaction_id, tag_id, workspace_id)
			values ($1, $2, $3)
			on conflict (transaction_id, tag_id) do nothing
		`, snap.ID, tagID, workspaceID); err != nil {
			return mapInsertErrorForApply(err)
		}
	}
	return nil
}

// mapInsertErrorForApply surfaces FK/check violations as 400s just like the
// rest of the service — same codes as mapWriteError, but we keep it separate
// to hang the apply-specific resource label on.
func mapInsertErrorForApply(err error) error {
	return mapWriteError("categorization_rule_apply", err)
}

func loadAppliedTransaction(ctx context.Context, tx pgx.Tx, workspaceID, id uuid.UUID) (*AppliedTransaction, error) {
	var out AppliedTransaction
	err := tx.QueryRow(ctx, `
		select id, workspace_id, account_id, status::text, booked_at, value_at, posted_at,
		       amount::text, currency, original_amount::text, original_currency::text,
		       merchant_id, category_id, counterparty_raw, description, notes,
		       count_as_expense, created_at, updated_at
		from transactions
		where workspace_id = $1 and id = $2
	`, workspaceID, id).Scan(
		&out.ID, &out.WorkspaceID, &out.AccountID, &out.Status, &out.BookedAt,
		&out.ValueAt, &out.PostedAt,
		&out.Amount, &out.Currency, &out.OriginalAmount, &out.OriginalCurrency,
		&out.MerchantID, &out.CategoryID, &out.CounterpartyRaw, &out.Description,
		&out.Notes, &out.CountAsExpense, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("transaction")
		}
		return nil, fmt.Errorf("load applied transaction: %w", err)
	}
	return &out, nil
}
