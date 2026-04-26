package classification

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

func assertValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var verr *httpx.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

// ---- normalizeWhen ---------------------------------------------------------

func TestNormalizeWhen_requiresAtLeastOne(t *testing.T) {
	_, err := normalizeWhen(json.RawMessage(`{}`))
	assertValidationError(t, err)
}

func TestNormalizeWhen_acceptsMinimalAccountId(t *testing.T) {
	id := uuid.New()
	raw := json.RawMessage(`{"accountId":"` + id.String() + `"}`)
	out, err := normalizeWhen(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.AccountID == nil || *out.AccountID != id {
		t.Errorf("AccountID = %v, want %v", out.AccountID, id)
	}
}

func TestNormalizeWhen_rejectsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"accountId":"` + uuid.NewString() + `","bogus":true}`)
	_, err := normalizeWhen(raw)
	assertValidationError(t, err)
}

func TestNormalizeWhen_lowersContains(t *testing.T) {
	raw := json.RawMessage(`{"counterpartyContains":"  MIGROS  "}`)
	out, err := normalizeWhen(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.CounterpartyContains == nil || *out.CounterpartyContains != "migros" {
		t.Errorf("CounterpartyContains = %v, want migros", out.CounterpartyContains)
	}
}

func TestNormalizeWhen_emptyContainsRejected(t *testing.T) {
	raw := json.RawMessage(`{"counterpartyContains":"   "}`)
	_, err := normalizeWhen(raw)
	assertValidationError(t, err)
}

func TestNormalizeWhen_amountBoundsOrder(t *testing.T) {
	raw := json.RawMessage(`{"amountMin":"50","amountMax":"10"}`)
	_, err := normalizeWhen(raw)
	assertValidationError(t, err)
}

func TestNormalizeWhen_amountBoundsEqualOK(t *testing.T) {
	raw := json.RawMessage(`{"amountMin":"10.50","amountMax":"10.50"}`)
	out, err := normalizeWhen(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.AmountMin == nil || *out.AmountMin != "10.5" {
		t.Errorf("AmountMin = %v", out.AmountMin)
	}
}

func TestNormalizeWhen_badDecimal(t *testing.T) {
	raw := json.RawMessage(`{"amountMin":"abc"}`)
	_, err := normalizeWhen(raw)
	assertValidationError(t, err)
}

func TestNormalizeWhen_amountSignValid(t *testing.T) {
	for _, sign := range []string{"debit", "credit"} {
		t.Run(sign, func(t *testing.T) {
			raw := json.RawMessage(`{"amountSign":"` + sign + `"}`)
			out, err := normalizeWhen(raw)
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if out.AmountSign == nil || *out.AmountSign != sign {
				t.Errorf("AmountSign = %v", out.AmountSign)
			}
		})
	}
}

func TestNormalizeWhen_amountSignInvalid(t *testing.T) {
	_, err := normalizeWhen(json.RawMessage(`{"amountSign":"both"}`))
	assertValidationError(t, err)
}

func TestNormalizeWhen_uuidRejected(t *testing.T) {
	_, err := normalizeWhen(json.RawMessage(`{"accountId":"not-a-uuid"}`))
	assertValidationError(t, err)
}

// ---- normalizeThen ---------------------------------------------------------

func TestNormalizeThen_requiresAtLeastOne(t *testing.T) {
	_, err := normalizeThen(json.RawMessage(`{}`))
	assertValidationError(t, err)
}

func TestNormalizeThen_emptyAddTagIDsRejected(t *testing.T) {
	_, err := normalizeThen(json.RawMessage(`{"addTagIds":[]}`))
	assertValidationError(t, err)
}

func TestNormalizeThen_addTagIDsDedup(t *testing.T) {
	id := uuid.NewString()
	raw := json.RawMessage(`{"addTagIds":["` + id + `","` + id + `"]}`)
	out, err := normalizeThen(raw)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(out.AddTagIDs) != 1 {
		t.Errorf("addTagIds not deduped: %v", out.AddTagIDs)
	}
}

func TestNormalizeThen_countAsExpenseStates(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		set       bool
		wantNull  bool
		wantValue bool
	}{
		{"absent", `{"categoryId":"` + uuid.NewString() + `"}`, false, false, false},
		{"null", `{"countAsExpense":null}`, true, true, false},
		{"true", `{"countAsExpense":true}`, true, false, true},
		{"false", `{"countAsExpense":false}`, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := normalizeThen(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if out.CountAsExpenseSet != tc.set {
				t.Errorf("CountAsExpenseSet = %v, want %v", out.CountAsExpenseSet, tc.set)
			}
			if tc.wantNull && out.CountAsExpense != nil {
				t.Errorf("CountAsExpense = %v, want nil", out.CountAsExpense)
			}
			if !tc.wantNull && out.CountAsExpenseSet && (out.CountAsExpense == nil || *out.CountAsExpense != tc.wantValue) {
				t.Errorf("CountAsExpense = %v, want %v", out.CountAsExpense, tc.wantValue)
			}
		})
	}
}

func TestNormalizeThen_countAsExpenseInvalid(t *testing.T) {
	_, err := normalizeThen(json.RawMessage(`{"countAsExpense":"yes"}`))
	assertValidationError(t, err)
}

func TestNormalizeThen_unknownFieldRejected(t *testing.T) {
	raw := json.RawMessage(`{"categoryId":"` + uuid.NewString() + `","nope":1}`)
	_, err := normalizeThen(raw)
	assertValidationError(t, err)
}

// ---- marshalling round-trip ------------------------------------------------

func TestMarshalWhenThenRoundTrip(t *testing.T) {
	acc := uuid.New()
	whenRaw := json.RawMessage(`{"accountId":"` + acc.String() + `","amountMin":"-100.50","amountSign":"debit","counterpartyContains":"Swisscom"}`)
	when, err := normalizeWhen(whenRaw)
	if err != nil {
		t.Fatalf("normalize when: %v", err)
	}
	wb, err := marshalWhenForStore(when)
	if err != nil {
		t.Fatalf("marshal when: %v", err)
	}
	var round RuleWhen
	if err := unmarshalWhen(wb, &round); err != nil {
		t.Fatalf("unmarshal when: %v", err)
	}
	if round.AccountID == nil || *round.AccountID != acc {
		t.Errorf("accountId round-trip failed")
	}
	if round.CounterpartyContains == nil || *round.CounterpartyContains != "swisscom" {
		t.Errorf("counterpartyContains should be lowercased on store")
	}

	cat := uuid.New()
	thenRaw := json.RawMessage(`{"categoryId":"` + cat.String() + `","countAsExpense":null}`)
	then, err := normalizeThen(thenRaw)
	if err != nil {
		t.Fatalf("normalize then: %v", err)
	}
	tb, err := marshalThenForStore(then)
	if err != nil {
		t.Fatalf("marshal then: %v", err)
	}
	var round2 RuleThen
	if err := unmarshalThen(tb, &round2); err != nil {
		t.Fatalf("unmarshal then: %v", err)
	}
	if round2.CategoryID == nil || *round2.CategoryID != cat {
		t.Errorf("categoryId round-trip failed")
	}
	if !round2.CountAsExpenseSet || round2.CountAsExpense != nil {
		t.Errorf("countAsExpense null round-trip failed: set=%v value=%v", round2.CountAsExpenseSet, round2.CountAsExpense)
	}
}

// ---- RuleMatches -----------------------------------------------------------

func snap(txMods ...func(*transactionSnapshot)) *transactionSnapshot {
	s := &transactionSnapshot{
		ID:        uuid.New(),
		WorkspaceID:  uuid.New(),
		AccountID: uuid.New(),
		Amount:    decimal.NewFromFloat(-42.50),
	}
	cp := "Migros Zürich"
	s.CounterpartyRaw = &cp
	desc := "Groceries weekly shop"
	s.Description = &desc
	for _, m := range txMods {
		m(s)
	}
	return s
}

func ruleFromRaw(t *testing.T, whenStr, thenStr string) *Rule {
	t.Helper()
	w, err := normalizeWhen(json.RawMessage(whenStr))
	if err != nil {
		t.Fatalf("normalize when: %v", err)
	}
	th, err := normalizeThen(json.RawMessage(thenStr))
	if err != nil {
		t.Fatalf("normalize then: %v", err)
	}
	return &Rule{When: w, Then: th, Enabled: true, Priority: 100}
}

func TestRuleMatches_counterpartyCaseInsensitive(t *testing.T) {
	s := snap()
	r := ruleFromRaw(t, `{"counterpartyContains":"migros"}`, `{"countAsExpense":true}`)
	if !RuleMatches(r, s) {
		t.Error("expected match on lowered substring")
	}
}

func TestRuleMatches_descriptionMissing(t *testing.T) {
	s := snap(func(s *transactionSnapshot) { s.Description = nil })
	r := ruleFromRaw(t, `{"descriptionContains":"weekly"}`, `{"countAsExpense":true}`)
	if RuleMatches(r, s) {
		t.Error("should not match when description is null")
	}
}

func TestRuleMatches_accountID(t *testing.T) {
	other := uuid.New()
	s := snap()
	rSame := ruleFromRaw(t, `{"accountId":"`+s.AccountID.String()+`"}`, `{"countAsExpense":true}`)
	if !RuleMatches(rSame, s) {
		t.Error("expected match for same accountId")
	}
	rOther := ruleFromRaw(t, `{"accountId":"`+other.String()+`"}`, `{"countAsExpense":true}`)
	if RuleMatches(rOther, s) {
		t.Error("expected no match for other accountId")
	}
}

func TestRuleMatches_merchantIDNullWhenRequired(t *testing.T) {
	s := snap(func(s *transactionSnapshot) { s.MerchantID = nil })
	r := ruleFromRaw(t, `{"merchantId":"`+uuid.NewString()+`"}`, `{"countAsExpense":true}`)
	if RuleMatches(r, s) {
		t.Error("rule requires merchantId but tx has none")
	}
}

func TestRuleMatches_amountBounds(t *testing.T) {
	// tx amount is -42.50
	s := snap()
	cases := []struct {
		name string
		when string
		want bool
	}{
		{"min ok", `{"amountMin":"-100"}`, true},
		{"min too high", `{"amountMin":"-40"}`, false},
		{"max ok", `{"amountMax":"0"}`, true},
		{"max too low", `{"amountMax":"-100"}`, false},
		{"range ok", `{"amountMin":"-100","amountMax":"-10"}`, true},
		{"range miss", `{"amountMin":"-40","amountMax":"-10"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ruleFromRaw(t, tc.when, `{"countAsExpense":true}`)
			if got := RuleMatches(r, s); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRuleMatches_amountSign(t *testing.T) {
	debit := snap()
	credit := snap(func(s *transactionSnapshot) { s.Amount = decimal.NewFromFloat(200) })
	zero := snap(func(s *transactionSnapshot) { s.Amount = decimal.NewFromInt(0) })

	rDebit := ruleFromRaw(t, `{"amountSign":"debit"}`, `{"countAsExpense":true}`)
	rCredit := ruleFromRaw(t, `{"amountSign":"credit"}`, `{"countAsExpense":true}`)

	if !RuleMatches(rDebit, debit) {
		t.Error("debit rule should match negative amount")
	}
	if RuleMatches(rDebit, credit) {
		t.Error("debit rule should not match positive amount")
	}
	if RuleMatches(rDebit, zero) {
		t.Error("debit rule should not match zero")
	}
	if !RuleMatches(rCredit, credit) {
		t.Error("credit rule should match positive amount")
	}
	if RuleMatches(rCredit, zero) {
		t.Error("credit rule should not match zero")
	}
}

func TestRuleMatches_allClausesAnded(t *testing.T) {
	s := snap()
	// Matches counterparty + amountSign but not accountId.
	other := uuid.New()
	r := ruleFromRaw(t,
		`{"accountId":"`+other.String()+`","counterpartyContains":"migros","amountSign":"debit"}`,
		`{"countAsExpense":true}`)
	if RuleMatches(r, s) {
		t.Error("expected no match: accountId mismatch breaks AND")
	}
}

// ---- list filter parsing ---------------------------------------------------

func TestRuleThen_JSONMarshallingOmitsAbsent(t *testing.T) {
	cat := uuid.New()
	then, err := normalizeThen(json.RawMessage(`{"categoryId":"` + cat.String() + `"}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	b, err := json.Marshal(then)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytesContains(b, `"countAsExpense"`) {
		t.Errorf("absent countAsExpense should be omitted, got %s", string(b))
	}
	if !bytesContains(b, `"categoryId"`) {
		t.Errorf("categoryId missing, got %s", string(b))
	}
}

func TestRuleThen_JSONMarshallingPreservesExplicitNull(t *testing.T) {
	then, err := normalizeThen(json.RawMessage(`{"countAsExpense":null}`))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	b, err := json.Marshal(then)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytesContains(b, `"countAsExpense":null`) {
		t.Errorf("explicit null lost, got %s", string(b))
	}
}

func bytesContains(b []byte, sub string) bool {
	return len(b) >= len(sub) && stringIndex(string(b), sub) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
