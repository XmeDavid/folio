package bankimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestParseRevolutBankingSample(t *testing.T) {
	content := readLegacySample(t, "account-statement.csv")
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Profile != "revolut_banking_csv" {
		t.Fatalf("profile = %q", parsed.Profile)
	}
	if parsed.Institution != "Revolut" {
		t.Fatalf("institution = %q", parsed.Institution)
	}
	if len(parsed.Transactions) == 0 {
		t.Fatal("expected transactions")
	}
	first := parsed.Transactions[0]
	if got := first.BookedAt.Format(dateOnly); got != "2019-04-18" {
		t.Fatalf("first booked date = %s", got)
	}
	// Expect the first CONCLUÍDA top-up of €10. The earlier REVERTIDA row
	// must be filtered out — it never actually settled.
	if !first.Amount.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("first amount = %s", first.Amount)
	}
	if first.Currency != "EUR" {
		t.Fatalf("first currency = %s", first.Currency)
	}
	if first.ExternalID == "" {
		t.Fatal("expected deterministic external id")
	}
	if parsed.DateFrom == nil || parsed.DateFrom.Format(dateOnly) != "2019-04-18" {
		t.Fatalf("date from = %#v", parsed.DateFrom)
	}
	// REVERTIDA rows must never reach the importable set.
	for _, tx := range parsed.Transactions {
		if status := tx.Raw["Estado"]; status == "REVERTIDA" {
			t.Fatalf("REVERTIDA tx leaked into output: %+v", tx)
		}
	}
}

func TestParseRevolutBankingFeeIsSubtracted(t *testing.T) {
	// One row, one CHF→EUR conversion that charged a 0.17 EUR fee. After
	// the fix, Montante is reduced by Comissão so the cash hit on the
	// account matches Revolut's own Saldo column.
	content := "Tipo,Produto,Data de início,Data de Conclusão,Descrição,Montante,Comissão,Moeda,Estado,Saldo\n" +
		"Câmbio,Atual,2021-08-29 12:37:08,2021-08-29 12:37:08,Conversão cambial para EUR,16.94,0.17,EUR,CONCLUÍDA,16.77\n"
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Transactions) != 1 {
		t.Fatalf("transactions = %d", len(parsed.Transactions))
	}
	got := parsed.Transactions[0].Amount
	want := decimal.RequireFromString("16.77")
	if !got.Equal(want) {
		t.Fatalf("amount = %s, want %s (Montante - Comissão)", got, want)
	}
}

func TestParseRevolutBankingSkipsPoupancas(t *testing.T) {
	content := "Tipo,Produto,Data de início,Data de Conclusão,Descrição,Montante,Comissão,Moeda,Estado,Saldo\n" +
		"Carregamento,Atual,2025-03-25 13:41:36,2025-03-25 13:41:36,Salary,2000.00,0.00,CHF,CONCLUÍDA,2000.00\n" +
		"Transferência,Poupanças,2025-03-25 13:41:36,2025-03-25 13:41:36,Carregamento de subconta CHF Dízimo de CHF,200.00,0.00,CHF,CONCLUÍDA,200.00\n"
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Transactions) != 1 {
		t.Fatalf("expected only the Atual row, got %d", len(parsed.Transactions))
	}
	if got := parsed.Transactions[0].Currency; got != "CHF" {
		t.Fatalf("currency = %s", got)
	}
	hasPocketWarning := false
	for _, w := range parsed.Warnings {
		if strings.Contains(w, "pocket") {
			hasPocketWarning = true
		}
	}
	if !hasPocketWarning {
		t.Fatalf("expected a pocket-skipped warning, got %+v", parsed.Warnings)
	}
}

func TestRevolutBankingAndConsolidatedShareFingerprint(t *testing.T) {
	// Same logical transaction (-1.65 CHF Migros on 2025-05-07) parsed from
	// both export formats must produce the same external_id so cross-file
	// uploads dedupe via duplicateBySource.
	banking := "Tipo,Produto,Data de início,Data de Conclusão,Descrição,Montante,Comissão,Moeda,Estado,Saldo\n" +
		"Pagamento com cartão,Atual,2025-05-07 12:07:38,2025-05-07 12:07:38,Migros,-1.65,0.00,CHF,CONCLUÍDA,98.35\n"
	consolidated := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Conta Pessoal (CHF)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo,Imposto retido,Outros impostos,Comissões`,
		`07/05/2025,Migros,Outros,"-1,65 CHF (-1,77€)","98,35 CHF (105,02€)","0,00 CHF (0,00€)","0,00 CHF (0,00€)","0,00 CHF (0,00€)"`,
		`Total,,,"-1,65 CHF",,"0,00 CHF","0,00 CHF","0,00 CHF"`,
		`---------,,,,,,,,`,
		``,
	}, "\n")
	bp, err := Parse(banking)
	if err != nil {
		t.Fatalf("banking Parse: %v", err)
	}
	cp, err := Parse(consolidated)
	if err != nil {
		t.Fatalf("consolidated Parse: %v", err)
	}
	if len(bp.Transactions) != 1 || len(cp.Transactions) != 1 {
		t.Fatalf("unexpected counts: banking=%d consolidated=%d", len(bp.Transactions), len(cp.Transactions))
	}
	if bp.Transactions[0].ExternalID != cp.Transactions[0].ExternalID {
		t.Fatalf("external IDs differ — cross-file dedup broken:\n  banking      = %s\n  consolidated = %s",
			bp.Transactions[0].ExternalID, cp.Transactions[0].ExternalID)
	}
}

func TestParseRevolutConsolidatedEmitsPockets(t *testing.T) {
	content := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Conta Pessoal (CHF)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo,Imposto retido,Outros impostos,Comissões`,
		`07/05/2025,Migros,Outros,"-1,65 CHF","98,35 CHF","0,00 CHF","0,00 CHF","0,00 CHF"`,
		`Total,,,"-1,65 CHF",,"0,00 CHF","0,00 CHF","0,00 CHF"`,
		`---------,,,,,,,,`,
		`"Travel - 200 (CHF)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo`,
		`25/04/2026,"Carregamento de subconta CHF Travel - 200 de CHF",Outros,"200,00 CHF","200,00 CHF"`,
		`Total,,,"200,00 CHF"`,
		`---------,,,,,,,,`,
		``,
	}, "\n")
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Profile != "revolut_consolidated_v2" {
		t.Fatalf("profile = %s", parsed.Profile)
	}
	if len(parsed.Transactions) != 2 {
		t.Fatalf("expected one Conta Pessoal + one pocket row, got %d", len(parsed.Transactions))
	}
	hints := map[string]decimal.Decimal{}
	for _, tx := range parsed.Transactions {
		hints[tx.AccountHint] = tx.Amount
	}
	if got, ok := hints["Conta Pessoal"]; !ok || !got.Equal(decimal.RequireFromString("-1.65")) {
		t.Fatalf("Conta Pessoal hint missing or wrong: %v", hints)
	}
	if got, ok := hints["Travel - 200"]; !ok || !got.Equal(decimal.RequireFromString("200")) {
		t.Fatalf("Travel pocket hint missing or wrong: %v", hints)
	}
}

func TestParseRevolutConsolidatedHandlesThousandSeparators(t *testing.T) {
	// pt-locale Revolut exports use U+00A0 (non-breaking space) as the
	// thousand separator. Earlier versions of the parser stopped at the
	// NBSP and lost the integer part — silently dropping ~999 from a
	// "1 063,95 CHF" row and skewing the section's net by thousands.
	content := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Conta Pessoal (CHF)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo,Imposto retido,Outros impostos,Comissões`,
		`17/06/2025,ecofort,Comerciante,"-1 063,95 CHF (-1 066,18€)","38,11 CHF (39,11€)","0,00 CHF","0,00 CHF","0,00 CHF"`,
		`18/06/2025,Salary,Carregar,"5 000,00 CHF","5 038,11 CHF","0,00 CHF","0,00 CHF","0,00 CHF"`,
		`Total,,,"3 936,05 CHF",,"0,00 CHF","0,00 CHF","0,00 CHF"`,
		`---------,,,,,,,,`,
		``,
	}, "\n")
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Transactions) != 2 {
		t.Fatalf("expected 2 txs, got %d", len(parsed.Transactions))
	}
	got1 := parsed.Transactions[0].Amount
	want1 := decimal.RequireFromString("-1063.95")
	if !got1.Equal(want1) {
		t.Errorf("ecofort amount = %s, want %s", got1, want1)
	}
	got2 := parsed.Transactions[1].Amount
	want2 := decimal.RequireFromString("5000")
	if !got2.Equal(want2) {
		t.Errorf("salary amount = %s, want %s", got2, want2)
	}
}

func TestParseRevolutConsolidatedDisambiguatesDuplicatePockets(t *testing.T) {
	content := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Vectr (EUR)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo`,
		`01/01/2025,"Carregamento de subconta",Outros,"50,00€","50,00€"`,
		`Total,,,"50,00€"`,
		`---------,,,,,,,,`,
		`"Vectr (EUR)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo`,
		`01/02/2025,"Carregamento de subconta",Outros,"75,00€","75,00€"`,
		`Total,,,"75,00€"`,
		`---------,,,,,,,,`,
		``,
	}, "\n")
	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Transactions) != 2 {
		t.Fatalf("expected 2 txs, got %d", len(parsed.Transactions))
	}
	seen := map[string]bool{}
	for _, tx := range parsed.Transactions {
		seen[tx.AccountHint] = true
	}
	if !seen["Vectr #1"] || !seen["Vectr #2"] {
		t.Fatalf("expected disambiguated hints Vectr #1 and Vectr #2, got %v", seen)
	}
}

func TestClassifyDuplicatesAndConflicts(t *testing.T) {
	parsed := ParsedFile{
		Profile:  "test",
		Currency: "CHF",
		Transactions: []ParsedTransaction{
			{
				BookedAt:    mustDate(t, "2026-01-10"),
				Amount:      decimal.RequireFromString("-12.30"),
				Currency:    "CHF",
				Description: strPtr("COOP 123"),
				ExternalID:  "same-source",
			},
			{
				BookedAt:    mustDate(t, "2026-01-11"),
				Amount:      decimal.RequireFromString("-9.90"),
				Currency:    "CHF",
				Description: strPtr("Migros"),
				ExternalID:  "new-source-conflict",
			},
			{
				BookedAt:    mustDate(t, "2026-01-12"),
				Amount:      decimal.RequireFromString("-5"),
				Currency:    "CHF",
				Description: strPtr("Bakery"),
				ExternalID:  "new",
			},
		},
	}
	existingSource := "same-source"
	existing := []existingTx{
		{
			BookedAt:    mustDate(t, "2026-01-10"),
			Amount:      decimal.RequireFromString("-12.30"),
			Currency:    "CHF",
			Description: "COOP 123",
			SourceID:    &existingSource,
		},
		{
			BookedAt:    mustDate(t, "2026-01-11"),
			Amount:      decimal.RequireFromString("-9.90"),
			Currency:    "CHF",
			Description: "MIGROS OLD TEXT",
		},
	}

	got := classify(parsed, existing)
	if len(got.duplicates) != 1 {
		t.Fatalf("duplicates = %d", len(got.duplicates))
	}
	if len(got.conflicts) != 1 {
		t.Fatalf("conflicts = %d", len(got.conflicts))
	}
	if len(got.importable) != 1 {
		t.Fatalf("importable = %d", len(got.importable))
	}
}

func TestParsedForGroupFiltersByAccountHint(t *testing.T) {
	mk := func(hint, cur, amt string) ParsedTransaction {
		return ParsedTransaction{
			BookedAt:    mustDate(t, "2026-01-01"),
			Amount:      decimal.RequireFromString(amt),
			Currency:    cur,
			Description: strPtr("test"),
			AccountHint: hint,
		}
	}
	parsed := ParsedFile{
		Profile:  "revolut_consolidated_v2",
		Currency: "CHF",
		Transactions: []ParsedTransaction{
			mk("Conta Pessoal", "CHF", "-1.00"),
			mk("Travel - 200", "CHF", "200.00"),
			mk("Food - 400", "CHF", "400.00"),
			mk("Travel - 200", "EUR", "10.00"),
		},
	}

	// Filter by (CHF, Travel - 200) — only the matching pocket survives.
	got := parsedForGroup(parsed, "CHF", "Travel - 200")
	if len(got.Transactions) != 1 {
		t.Fatalf("travel pocket: got %d txs, want 1", len(got.Transactions))
	}
	if !got.Transactions[0].Amount.Equal(decimal.RequireFromString("200")) {
		t.Fatalf("travel amount = %s", got.Transactions[0].Amount)
	}

	// Empty SourceKey → legacy behaviour (every CHF tx).
	all := parsedForGroup(parsed, "CHF", "")
	if len(all.Transactions) != 3 {
		t.Fatalf("legacy filter: got %d txs, want 3", len(all.Transactions))
	}
}

func TestBestImportCandidate(t *testing.T) {
	mk := func(name string, dup int) AccountCandidate {
		return AccountCandidate{Name: name, DuplicateCount: dup}
	}
	cases := []struct {
		name      string
		cands     []AccountCandidate
		incoming  int
		wantMatch bool
		wantName  string
	}{
		{"none", nil, 100, false, ""},
		{"single candidate, always pick", []AccountCandidate{mk("Solo", 0)}, 100, true, "Solo"},
		{"two with zero overlap, no auto-pick", []AccountCandidate{mk("A", 0), mk("B", 0)}, 100, false, ""},
		{"clear winner via absolute threshold", []AccountCandidate{mk("Bigger", 50), mk("Smaller", 5)}, 100, true, "Bigger"},
		{"40% threshold, picks even with low absolute", []AccountCandidate{mk("Best", 4), mk("None", 0)}, 5, true, "Best"},
		{"below both thresholds, no auto-pick", []AccountCandidate{mk("Maybe", 3), mk("Other", 0)}, 1000, false, ""},
	}
	for _, c := range cases {
		// candidates input is expected to already be sorted; do it here for clarity.
		got, ok := bestImportCandidate(c.cands, c.incoming)
		if ok != c.wantMatch {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.wantMatch)
		}
		if c.wantMatch && got.Name != c.wantName {
			t.Errorf("%s: name = %q, want %q", c.name, got.Name, c.wantName)
		}
	}
}

func TestSuggestedNameForGroup(t *testing.T) {
	cases := []struct {
		institution string
		k           groupKey
		want        string
	}{
		{"Revolut", groupKey{currency: "CHF"}, "Revolut CHF"},
		{"Revolut", groupKey{currency: "CHF", sourceKey: "Conta Pessoal"}, "Revolut Conta Pessoal CHF"},
		{"Revolut", groupKey{currency: "CHF", sourceKey: "Travel - 200"}, "Revolut Travel - 200 CHF"},
		{"", groupKey{currency: "CHF"}, "CHF"},
		{"", groupKey{}, "Imported account"},
	}
	for _, c := range cases {
		got := suggestedNameForGroup(c.institution, c.k)
		if got != c.want {
			t.Errorf("suggestedNameForGroup(%q, %+v) = %q, want %q", c.institution, c.k, got, c.want)
		}
	}
}

func readLegacySample(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "legacy", "data", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	return string(b)
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateOnly, s)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return d
}

func strPtr(s string) *string { return &s }
