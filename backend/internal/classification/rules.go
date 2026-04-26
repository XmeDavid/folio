package classification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Rule is the read-model for a categorization rule. When and Then carry the
// validated DSL; unknown keys are rejected at the service boundary so stored
// JSON always round-trips.
type Rule struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID      uuid.UUID  `json:"workspaceId"`
	Priority      int        `json:"priority"`
	When          RuleWhen   `json:"when"`
	Then          RuleThen   `json:"then"`
	Enabled       bool       `json:"enabled"`
	LastMatchedAt *time.Time `json:"lastMatchedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// RuleWhen is the v1 match condition DSL. At least one field must be set.
// All fields are ANDed together. Amounts are inclusive. amountSign is one
// of "debit" (amount < 0) or "credit" (amount > 0).
type RuleWhen struct {
	AccountID            *uuid.UUID `json:"accountId,omitempty"`
	MerchantID           *uuid.UUID `json:"merchantId,omitempty"`
	CounterpartyContains *string    `json:"counterpartyContains,omitempty"`
	DescriptionContains  *string    `json:"descriptionContains,omitempty"`
	AmountMin            *string    `json:"amountMin,omitempty"`
	AmountMax            *string    `json:"amountMax,omitempty"`
	AmountSign           *string    `json:"amountSign,omitempty"`
}

// RuleThen is the v1 action DSL. At least one field must be set. countAsExpense
// supports three wire states: absent (don't touch), true/false (set), null
// (explicitly clear on apply). countAsExpenseSet distinguishes "absent" from
// "present-null" after JSON parsing. Stored as countAsExpense key in jsonb —
// omitted when !Set, set to null when Set && Value==nil, to bool otherwise.
type RuleThen struct {
	CategoryID        *uuid.UUID  `json:"categoryId,omitempty"`
	MerchantID        *uuid.UUID  `json:"merchantId,omitempty"`
	AddTagIDs         []uuid.UUID `json:"addTagIds,omitempty"`
	CountAsExpenseSet bool        `json:"-"`
	CountAsExpense    *bool       `json:"-"`
}

// MarshalJSON emits countAsExpense only when Set; null or bool depending on
// CountAsExpense. AddTagIDs is omitted when empty.
func (t RuleThen) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if t.CategoryID != nil {
		out["categoryId"] = t.CategoryID
	}
	if t.MerchantID != nil {
		out["merchantId"] = t.MerchantID
	}
	if len(t.AddTagIDs) > 0 {
		out["addTagIds"] = t.AddTagIDs
	}
	if t.CountAsExpenseSet {
		if t.CountAsExpense == nil {
			out["countAsExpense"] = nil
		} else {
			out["countAsExpense"] = *t.CountAsExpense
		}
	}
	return json.Marshal(out)
}

// RuleCreateInput is the raw payload from the HTTP layer. Priority defaults
// to 1000 when omitted. Enabled defaults to true.
type RuleCreateInput struct {
	Priority *int
	Enabled  *bool
	When     json.RawMessage
	Then     json.RawMessage
}

// RulePatchInput is the PATCH payload. Any absent field is left unchanged.
// When/Then can be replaced wholesale (no partial merge).
type RulePatchInput struct {
	Priority *int
	Enabled  *bool
	When     json.RawMessage
	Then     json.RawMessage
}

// ---- DSL normalization -----------------------------------------------------

// normalizeWhen parses and validates the when JSON. Unknown keys are
// rejected so we don't silently drop typos. Returns the normalized struct
// and trimmed/lowered contains strings (matching helpers rely on lower-case).
func normalizeWhen(raw json.RawMessage) (RuleWhen, error) {
	var out RuleWhen
	if len(raw) == 0 {
		return out, httpx.NewValidationError("when is required")
	}
	// Reject unknown keys.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var w whenWire
	if err := dec.Decode(&w); err != nil {
		return out, httpx.NewValidationError(fmt.Sprintf("when: %s", err.Error()))
	}

	count := 0

	if w.AccountID != nil {
		s := strings.TrimSpace(*w.AccountID)
		if s == "" {
			return out, httpx.NewValidationError("when.accountId cannot be empty")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return out, httpx.NewValidationError("when.accountId must be a UUID")
		}
		out.AccountID = &id
		count++
	}
	if w.MerchantID != nil {
		s := strings.TrimSpace(*w.MerchantID)
		if s == "" {
			return out, httpx.NewValidationError("when.merchantId cannot be empty")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return out, httpx.NewValidationError("when.merchantId must be a UUID")
		}
		out.MerchantID = &id
		count++
	}
	if w.CounterpartyContains != nil {
		s := strings.TrimSpace(*w.CounterpartyContains)
		if s == "" {
			return out, httpx.NewValidationError("when.counterpartyContains cannot be empty")
		}
		lower := strings.ToLower(s)
		out.CounterpartyContains = &lower
		count++
	}
	if w.DescriptionContains != nil {
		s := strings.TrimSpace(*w.DescriptionContains)
		if s == "" {
			return out, httpx.NewValidationError("when.descriptionContains cannot be empty")
		}
		lower := strings.ToLower(s)
		out.DescriptionContains = &lower
		count++
	}

	var minD, maxD *decimal.Decimal
	if w.AmountMin != nil {
		d, err := decimal.NewFromString(strings.TrimSpace(*w.AmountMin))
		if err != nil {
			return out, httpx.NewValidationError("when.amountMin must be a decimal string")
		}
		s := d.String()
		out.AmountMin = &s
		minD = &d
		count++
	}
	if w.AmountMax != nil {
		d, err := decimal.NewFromString(strings.TrimSpace(*w.AmountMax))
		if err != nil {
			return out, httpx.NewValidationError("when.amountMax must be a decimal string")
		}
		s := d.String()
		out.AmountMax = &s
		maxD = &d
		count++
	}
	if minD != nil && maxD != nil && maxD.LessThan(*minD) {
		return out, httpx.NewValidationError("when.amountMin must be <= amountMax")
	}

	if w.AmountSign != nil {
		s := strings.ToLower(strings.TrimSpace(*w.AmountSign))
		if s != "debit" && s != "credit" {
			return out, httpx.NewValidationError("when.amountSign must be 'debit' or 'credit'")
		}
		out.AmountSign = &s
		count++
	}

	if count == 0 {
		return out, httpx.NewValidationError("when requires at least one condition")
	}
	return out, nil
}

type whenWire struct {
	AccountID            *string `json:"accountId"`
	MerchantID           *string `json:"merchantId"`
	CounterpartyContains *string `json:"counterpartyContains"`
	DescriptionContains  *string `json:"descriptionContains"`
	AmountMin            *string `json:"amountMin"`
	AmountMax            *string `json:"amountMax"`
	AmountSign           *string `json:"amountSign"`
}

type thenWire struct {
	CategoryID     *string         `json:"categoryId"`
	MerchantID     *string         `json:"merchantId"`
	AddTagIDs      []string        `json:"addTagIds"`
	CountAsExpense json.RawMessage `json:"countAsExpense"`
}

// normalizeThen parses and validates the then JSON. countAsExpense uses
// *json.RawMessage so "absent" (nil) differs from "explicit null" (non-nil,
// contents "null").
func normalizeThen(raw json.RawMessage) (RuleThen, error) {
	var out RuleThen
	if len(raw) == 0 {
		return out, httpx.NewValidationError("then is required")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var t thenWire
	if err := dec.Decode(&t); err != nil {
		return out, httpx.NewValidationError(fmt.Sprintf("then: %s", err.Error()))
	}

	count := 0
	if t.CategoryID != nil {
		s := strings.TrimSpace(*t.CategoryID)
		if s == "" {
			return out, httpx.NewValidationError("then.categoryId cannot be empty")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return out, httpx.NewValidationError("then.categoryId must be a UUID")
		}
		out.CategoryID = &id
		count++
	}
	if t.MerchantID != nil {
		s := strings.TrimSpace(*t.MerchantID)
		if s == "" {
			return out, httpx.NewValidationError("then.merchantId cannot be empty")
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return out, httpx.NewValidationError("then.merchantId must be a UUID")
		}
		out.MerchantID = &id
		count++
	}
	if t.AddTagIDs != nil {
		if len(t.AddTagIDs) == 0 {
			return out, httpx.NewValidationError("then.addTagIds cannot be empty when provided")
		}
		seen := make(map[uuid.UUID]struct{}, len(t.AddTagIDs))
		ids := make([]uuid.UUID, 0, len(t.AddTagIDs))
		for _, raw := range t.AddTagIDs {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil {
				return out, httpx.NewValidationError("then.addTagIds entries must be UUIDs")
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		out.AddTagIDs = ids
		count++
	}
	if len(t.CountAsExpense) > 0 {
		out.CountAsExpenseSet = true
		trimmed := strings.TrimSpace(string(t.CountAsExpense))
		if trimmed != "null" {
			var b bool
			if err := json.Unmarshal(t.CountAsExpense, &b); err != nil {
				return out, httpx.NewValidationError("then.countAsExpense must be boolean or null")
			}
			out.CountAsExpense = &b
		}
		count++
	}
	if count == 0 {
		return out, httpx.NewValidationError("then requires at least one action")
	}
	return out, nil
}

// ---- persistence -----------------------------------------------------------

const rulesCols = `
	id, workspace_id, priority, when_jsonb, then_jsonb, enabled,
	last_matched_at, created_at, updated_at
`

func scanRule(r interface{ Scan(dest ...any) error }, out *Rule) error {
	var whenBytes, thenBytes []byte
	if err := r.Scan(
		&out.ID, &out.WorkspaceID, &out.Priority, &whenBytes, &thenBytes,
		&out.Enabled, &out.LastMatchedAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return err
	}
	// Stored JSON is the same shape we validated; unmarshal directly.
	if err := unmarshalWhen(whenBytes, &out.When); err != nil {
		return fmt.Errorf("decode when_jsonb: %w", err)
	}
	if err := unmarshalThen(thenBytes, &out.Then); err != nil {
		return fmt.Errorf("decode then_jsonb: %w", err)
	}
	return nil
}

// unmarshalWhen is tolerant of legacy rows: it doesn't enforce "at least one
// condition" on read (validation runs on write). It does parse UUIDs/decimals
// so the returned RuleWhen matches the shape produced by normalizeWhen.
func unmarshalWhen(b []byte, out *RuleWhen) error {
	if len(b) == 0 {
		return nil
	}
	var w whenWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	if w.AccountID != nil {
		id, err := uuid.Parse(*w.AccountID)
		if err != nil {
			return fmt.Errorf("accountId: %w", err)
		}
		out.AccountID = &id
	}
	if w.MerchantID != nil {
		id, err := uuid.Parse(*w.MerchantID)
		if err != nil {
			return fmt.Errorf("merchantId: %w", err)
		}
		out.MerchantID = &id
	}
	if w.CounterpartyContains != nil {
		s := strings.ToLower(*w.CounterpartyContains)
		out.CounterpartyContains = &s
	}
	if w.DescriptionContains != nil {
		s := strings.ToLower(*w.DescriptionContains)
		out.DescriptionContains = &s
	}
	if w.AmountMin != nil {
		s := *w.AmountMin
		out.AmountMin = &s
	}
	if w.AmountMax != nil {
		s := *w.AmountMax
		out.AmountMax = &s
	}
	if w.AmountSign != nil {
		s := strings.ToLower(*w.AmountSign)
		out.AmountSign = &s
	}
	return nil
}

func unmarshalThen(b []byte, out *RuleThen) error {
	if len(b) == 0 {
		return nil
	}
	var t thenWire
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	if t.CategoryID != nil {
		id, err := uuid.Parse(*t.CategoryID)
		if err != nil {
			return fmt.Errorf("categoryId: %w", err)
		}
		out.CategoryID = &id
	}
	if t.MerchantID != nil {
		id, err := uuid.Parse(*t.MerchantID)
		if err != nil {
			return fmt.Errorf("merchantId: %w", err)
		}
		out.MerchantID = &id
	}
	if len(t.AddTagIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(t.AddTagIDs))
		for _, raw := range t.AddTagIDs {
			id, err := uuid.Parse(raw)
			if err != nil {
				return fmt.Errorf("addTagIds: %w", err)
			}
			ids = append(ids, id)
		}
		out.AddTagIDs = ids
	}
	if len(t.CountAsExpense) > 0 {
		out.CountAsExpenseSet = true
		trimmed := strings.TrimSpace(string(t.CountAsExpense))
		if trimmed != "null" {
			var b2 bool
			if err := json.Unmarshal(t.CountAsExpense, &b2); err != nil {
				return fmt.Errorf("countAsExpense: %w", err)
			}
			out.CountAsExpense = &b2
		}
	}
	return nil
}

// marshalWhenForStore produces the jsonb payload we want stored. We
// re-serialise from the normalized struct rather than echo the request body
// so stored JSON is always canonical (lowered contains strings, stringified
// uuids/decimals, no whitespace).
func marshalWhenForStore(w RuleWhen) ([]byte, error) {
	out := map[string]any{}
	if w.AccountID != nil {
		out["accountId"] = w.AccountID.String()
	}
	if w.MerchantID != nil {
		out["merchantId"] = w.MerchantID.String()
	}
	if w.CounterpartyContains != nil {
		out["counterpartyContains"] = *w.CounterpartyContains
	}
	if w.DescriptionContains != nil {
		out["descriptionContains"] = *w.DescriptionContains
	}
	if w.AmountMin != nil {
		out["amountMin"] = *w.AmountMin
	}
	if w.AmountMax != nil {
		out["amountMax"] = *w.AmountMax
	}
	if w.AmountSign != nil {
		out["amountSign"] = *w.AmountSign
	}
	return json.Marshal(out)
}

func marshalThenForStore(t RuleThen) ([]byte, error) {
	out := map[string]any{}
	if t.CategoryID != nil {
		out["categoryId"] = t.CategoryID.String()
	}
	if t.MerchantID != nil {
		out["merchantId"] = t.MerchantID.String()
	}
	if len(t.AddTagIDs) > 0 {
		ids := make([]string, 0, len(t.AddTagIDs))
		for _, id := range t.AddTagIDs {
			ids = append(ids, id.String())
		}
		out["addTagIds"] = ids
	}
	if t.CountAsExpenseSet {
		if t.CountAsExpense == nil {
			out["countAsExpense"] = nil
		} else {
			out["countAsExpense"] = *t.CountAsExpense
		}
	}
	return json.Marshal(out)
}

// ---- reference validation --------------------------------------------------

// validateWhenReferences asserts that referenced entities belong to the workspace.
// Keeps the HTTP surface returning clean 400s instead of surfacing FK errors
// on the first matching transaction.
func (s *Service) validateWhenReferences(ctx context.Context, workspaceID uuid.UUID, w RuleWhen) error {
	if w.AccountID != nil {
		if err := s.assertAccountExists(ctx, workspaceID, *w.AccountID); err != nil {
			return err
		}
	}
	if w.MerchantID != nil {
		if err := s.assertMerchantExists(ctx, workspaceID, *w.MerchantID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateThenReferences(ctx context.Context, workspaceID uuid.UUID, t RuleThen) error {
	if t.CategoryID != nil {
		if err := s.assertCategoryExists(ctx, workspaceID, *t.CategoryID); err != nil {
			return err
		}
	}
	if t.MerchantID != nil {
		if err := s.assertMerchantExists(ctx, workspaceID, *t.MerchantID); err != nil {
			return err
		}
	}
	for _, id := range t.AddTagIDs {
		if err := s.assertTagExists(ctx, workspaceID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) assertAccountExists(ctx context.Context, workspaceID, id uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from accounts where workspace_id = $1 and id = $2`,
		workspaceID, id).Scan(&ok)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewValidationError("referenced account does not exist for this workspace")
		}
		return fmt.Errorf("check account: %w", err)
	}
	return nil
}

func (s *Service) assertMerchantExists(ctx context.Context, workspaceID, id uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from merchants where workspace_id = $1 and id = $2`,
		workspaceID, id).Scan(&ok)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewValidationError("referenced merchant does not exist for this workspace")
		}
		return fmt.Errorf("check merchant: %w", err)
	}
	return nil
}

// ---- CRUD ------------------------------------------------------------------

// CreateRule validates the DSL, resolves references, then inserts the rule.
func (s *Service) CreateRule(ctx context.Context, workspaceID uuid.UUID, raw RuleCreateInput) (*Rule, error) {
	when, err := normalizeWhen(raw.When)
	if err != nil {
		return nil, err
	}
	then, err := normalizeThen(raw.Then)
	if err != nil {
		return nil, err
	}
	if err := s.validateWhenReferences(ctx, workspaceID, when); err != nil {
		return nil, err
	}
	if err := s.validateThenReferences(ctx, workspaceID, then); err != nil {
		return nil, err
	}

	priority := 1000
	if raw.Priority != nil {
		priority = *raw.Priority
	}
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}

	whenBytes, err := marshalWhenForStore(when)
	if err != nil {
		return nil, fmt.Errorf("marshal when: %w", err)
	}
	thenBytes, err := marshalThenForStore(then)
	if err != nil {
		return nil, fmt.Errorf("marshal then: %w", err)
	}

	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into categorization_rules (
			id, workspace_id, priority, when_jsonb, then_jsonb, enabled
		) values ($1, $2, $3, $4, $5, $6)
		returning `+rulesCols,
		id, workspaceID, priority, whenBytes, thenBytes, enabled,
	)
	var out Rule
	if err := scanRule(row, &out); err != nil {
		return nil, mapWriteError("categorization_rule", err)
	}
	return &out, nil
}

// RuleListFilter bounds GET /categorization-rules. Enabled is an optional
// "enabled=true/false" filter.
type RuleListFilter struct {
	Enabled *bool
}

// ListRules returns rules for workspaceID ordered by priority ASC, created_at ASC.
// When f.Enabled is non-nil, filters to only rules with that enabled value.
func (s *Service) ListRules(ctx context.Context, workspaceID uuid.UUID, f RuleListFilter) ([]Rule, error) {
	args := []any{workspaceID}
	q := `select ` + rulesCols + ` from categorization_rules where workspace_id = $1`
	if f.Enabled != nil {
		args = append(args, *f.Enabled)
		q += ` and enabled = $2`
	}
	q += ` order by priority asc, created_at asc`

	rows, err := s.pool.Query(ctx, q, args...)
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

// GetRule fetches a single rule.
func (s *Service) GetRule(ctx context.Context, workspaceID, id uuid.UUID) (*Rule, error) {
	row := s.pool.QueryRow(ctx,
		`select `+rulesCols+` from categorization_rules where workspace_id = $1 and id = $2`,
		workspaceID, id)
	var r Rule
	if err := scanRule(row, &r); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("categorization_rule")
		}
		return nil, err
	}
	return &r, nil
}

// UpdateRule applies the patch and returns the updated row. when/then replace
// wholesale; partial merge is intentionally out of scope.
func (s *Service) UpdateRule(ctx context.Context, workspaceID, id uuid.UUID, raw RulePatchInput) (*Rule, error) {
	sets := make([]string, 0, 4)
	args := []any{workspaceID, id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if raw.Priority != nil {
		sets = append(sets, "priority = "+next(*raw.Priority))
	}
	if raw.Enabled != nil {
		sets = append(sets, "enabled = "+next(*raw.Enabled))
	}
	if len(raw.When) > 0 {
		when, err := normalizeWhen(raw.When)
		if err != nil {
			return nil, err
		}
		if err := s.validateWhenReferences(ctx, workspaceID, when); err != nil {
			return nil, err
		}
		b, err := marshalWhenForStore(when)
		if err != nil {
			return nil, fmt.Errorf("marshal when: %w", err)
		}
		sets = append(sets, "when_jsonb = "+next(b))
	}
	if len(raw.Then) > 0 {
		then, err := normalizeThen(raw.Then)
		if err != nil {
			return nil, err
		}
		if err := s.validateThenReferences(ctx, workspaceID, then); err != nil {
			return nil, err
		}
		b, err := marshalThenForStore(then)
		if err != nil {
			return nil, fmt.Errorf("marshal then: %w", err)
		}
		sets = append(sets, "then_jsonb = "+next(b))
	}

	if len(sets) == 0 {
		return s.GetRule(ctx, workspaceID, id)
	}

	q := fmt.Sprintf(`
		update categorization_rules set %s
		where workspace_id = $1 and id = $2
		returning %s
	`, strings.Join(sets, ", "), rulesCols)

	row := s.pool.QueryRow(ctx, q, args...)
	var r Rule
	if err := scanRule(row, &r); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("categorization_rule")
		}
		return nil, mapWriteError("categorization_rule", err)
	}
	return &r, nil
}

// DeleteRule hard-deletes the rule. The audit table records the deletion via
// triggers (cross-cutting; not wired here).
func (s *Service) DeleteRule(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`delete from categorization_rules where workspace_id = $1 and id = $2`,
		workspaceID, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("categorization_rule")
	}
	return nil
}
