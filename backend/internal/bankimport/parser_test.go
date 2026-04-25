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

func TestParseRevolutConsolidatedCryptoSection(t *testing.T) {
	// Compact synthetic Crypto section covering all six sub-tables. Each
	// row exercises one op kind so the test pins down: signed amount,
	// currency=symbol, AccountHint="Crypto <SYMBOL>", KindHint=crypto_wallet,
	// description preserves op semantics, and the Raw map carries the
	// "op" tag the trade-pipeline pass will read later.
	content := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Conta Pessoal (EUR)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo`,
		`15/05/2019,Conversão cambial para BTC,Câmbio,"-2,00€","8,00€"`,
		`Total,,,"-2,00€"`,
		`---------,,,,,,,,`,
		`"Cripto Extratos de operações",,,,,,,,`,
		`"Extrato de operação (apenas vendas)",,,,,,,,`,
		`"Data (da venda, da compra)","Descrição e símbolo","Idade das unidades","Unidades vendidas","Preço unitário (Data de venda, na Data de compra)","Valor (da venda, da compra)","Ganhos de capital","Comissões"`,
		`"15.06.19, 15.05.19",BTC,1 month,"0,00027215","+ 7679,59€","+ 2,09€","0,08€","0,00€"`,
		`"Extrato de operação (apenas aquisições)",,,,,,,,`,
		`"Data de compra","Descrição e símbolo","Unidades compradas","Preço unitário","Valor de compra","Comissões"`,
		`"15.05.19",BTC,"0,00027215","7348,89€","2,00€","0,00€"`,
		`"Extrato de operação (apenas depósitos)",,,,,,,,`,
		`"Data do depósito","Descrição e símbolo","Unidades depositadas","Preço unitário","Valor de compra","Comissões"`,
		`"26.01.24",BTC,"0,0066141","39052,93€","258,30€","0,00€"`,
		`"Extrato de operação (apenas levantamentos)",,,,,,,,`,
		`"Data de levantamento","Descrição e símbolo","Unidades retiradas","Comissões"`,
		`"04.07.24",SOL,"0,02429","1,25€"`,
		`"Extrato de operação (apenas aquisições através de Learn & Earn)",,,,,,,,`,
		`"Data do recibo","Descrição e símbolo","Unidades recebidas","Preço unitário","Valor recebido","Comissões"`,
		`"28.01.25",LMWR,"0,57948523","0,17€","0,10€","0,00€"`,
		`"Extrato de operação (apenas aquisições por Staking)",,,,,,,,`,
		`"Data do recibo","Descrição e símbolo","Unidades recebidas","Preço unitário","Valor recebido","Comissões"`,
		`"16.09.24",SOL,"0,000032","0,00€","0,00€","0,01€"`,
		``,
	}, "\n")

	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	type want struct {
		hint     string
		currency string
		amount   string
		op       string
		desc     string
	}
	// Sorted by booked date (stable sort preserves insertion order on ties).
	expected := []want{
		{"Conta Pessoal", "EUR", "-2", "", "Conversão cambial para BTC"},
		{"Crypto BTC", "BTC", "0.00027215", "buy", "Buy BTC"},
		{"Crypto BTC", "BTC", "-0.00027215", "sell", "Sell BTC"},
		{"Crypto BTC", "BTC", "0.0066141", "deposit", "Deposit BTC"},
		{"Crypto SOL", "SOL", "-0.02429", "withdrawal", "Withdrawal SOL"},
		{"Crypto SOL", "SOL", "0.000032", "staking", "Staking reward SOL"},
		{"Crypto LMWR", "LMWR", "0.57948523", "learn_and_earn", "Learn & Earn reward LMWR"},
	}
	if len(parsed.Transactions) != len(expected) {
		t.Fatalf("expected %d txs, got %d", len(expected), len(parsed.Transactions))
	}
	for i, w := range expected {
		got := parsed.Transactions[i]
		if got.AccountHint != w.hint {
			t.Errorf("[%d] account hint = %q, want %q", i, got.AccountHint, w.hint)
		}
		if got.Currency != w.currency {
			t.Errorf("[%d] currency = %q, want %q", i, got.Currency, w.currency)
		}
		if !got.Amount.Equal(decimal.RequireFromString(w.amount)) {
			t.Errorf("[%d] amount = %s, want %s", i, got.Amount, w.amount)
		}
		if w.op != "" {
			if got.KindHint != "crypto_wallet" {
				t.Errorf("[%d] kind hint = %q, want crypto_wallet", i, got.KindHint)
			}
			if op := got.Raw["op"]; op != w.op {
				t.Errorf("[%d] raw op = %q, want %q", i, op, w.op)
			}
			if got.Description == nil || *got.Description != w.desc {
				t.Errorf("[%d] desc = %v, want %q", i, got.Description, w.desc)
			}
		} else if got.KindHint != "" {
			t.Errorf("[%d] non-crypto row should have empty KindHint, got %q", i, got.KindHint)
		}
	}
}

func TestParseRevolutConsolidatedMMFSection(t *testing.T) {
	// MMF section is interest-only; emits one positive-amount transaction
	// per non-zero interest day in a per-currency brokerage account.
	content := strings.Join([]string{
		`"Contas-correntes Extratos de operações",,,,,,,,`,
		`"Conta Pessoal (EUR)",,,,,,,,`,
		`"Extrato de operações",,,,,,,,`,
		`Data,Descrição,Categoria,Dinheiro a entrar/sair,Saldo`,
		`01/01/2024,Salary,Carregar,"100,00€","100,00€"`,
		`Total,,,"100,00€"`,
		`---------,,,,,,,,`,
		`"Fundos Monetários Flexíveis Extratos de operações",,,,,,,,`,
		`"Fundos Monetários Flexíveis  (EUR)",,,,,,,,`,
		`"Transaction statement (only returns)",,,,,,,,`,
		`Data,Descrição,"Juros líquidos","Imposto retido","Outros impostos","Comissões de serviço","Juros líquidos distribuídos e levantados"`,
		`24/08/2024,Interest earned - Flexible Cash Funds,"0,01€","0,00€","0,00€","0,00€","0,01€"`,
		`25/08/2024,Interest earned - Flexible Cash Funds,"0,02€","0,00€","0,00€","0,00€","0,02€"`,
		`26/08/2024,Interest earned - Flexible Cash Funds,"0,00€","0,00€","0,00€","0,00€","0,00€"`,
		`Total,,,"0,03€","0,00€","0,00€","0,00€"`,
		`---------,,,,,,,,`,
		`"Fundos Monetários Flexíveis  (USD)",,,,,,,,`,
		`"Transaction statement (only returns)",,,,,,,,`,
		`Data,Descrição,"Juros líquidos","Imposto retido","Outros impostos","Comissões de serviço","Juros líquidos distribuídos e levantados"`,
		`01/09/2024,Interest earned - Flexible Cash Funds,"0,05$","0,00$","0,00$","0,00$","0,05$"`,
		``,
	}, "\n")

	parsed, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	mmfByCurrency := map[string]decimal.Decimal{}
	mmfCount := 0
	for _, tx := range parsed.Transactions {
		if tx.AccountHint != "Flexible Cash Funds" {
			continue
		}
		if tx.KindHint != "brokerage" {
			t.Errorf("MMF tx kind hint = %q, want brokerage", tx.KindHint)
		}
		if tx.Raw["op"] != "interest" {
			t.Errorf("MMF tx op = %q, want interest", tx.Raw["op"])
		}
		mmfByCurrency[tx.Currency] = mmfByCurrency[tx.Currency].Add(tx.Amount)
		mmfCount++
	}
	if mmfCount != 3 {
		t.Fatalf("expected 3 MMF txs (zero-day skipped), got %d", mmfCount)
	}
	if got := mmfByCurrency["EUR"]; !got.Equal(decimal.RequireFromString("0.03")) {
		t.Errorf("EUR MMF total = %s, want 0.03", got)
	}
	if got := mmfByCurrency["USD"]; !got.Equal(decimal.RequireFromString("0.05")) {
		t.Errorf("USD MMF total = %s, want 0.05", got)
	}
	hasMMFWarning := false
	for _, w := range parsed.Warnings {
		if strings.Contains(w, "Flexible Cash Funds") {
			hasMMFWarning = true
		}
	}
	if !hasMMFWarning {
		t.Errorf("expected MMF warning about partial data, got %+v", parsed.Warnings)
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
