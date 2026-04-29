package bankimport

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// fixtureSavingsMultiCurrency exercises every event type the savings-
// statement export emits — BUY, SELL, Interest PAID, Service Fee Charged,
// Interest Reinvested, Interest WITHDRAWN — across two currency sections
// with their own header rows. Sums are deliberately small and easy to
// verify by eye in the assertions below.
func fixtureSavingsMultiCurrency() string {
	return strings.Join([]string{
		`Date,Description,"Value, USD","Value, EUR",FX Rate,Price per share,Quantity of shares`,
		`"23/08/2024, 12:00:00",BUY USD Class R IE000H9J0QX4,"100,00","90,00",0.9000,1.00,"100,00"`,
		`"24/08/2024, 02:00:00",Service Fee Charged USD Class IE000H9J0QX4,"-0,01","-0,009",0.9000,,`,
		`"24/08/2024, 02:00:00",Interest PAID USD Class R IE000H9J0QX4,"0,05","0,045",0.9000,,`,
		`"05/09/2024, 12:00:00",Interest Reinvested Class R USD IE000H9J0QX4,"-0,04","-0,036",0.9000,,`,
		`"06/09/2024, 12:00:00",BUY USD Class R IE000H9J0QX4,"0,04","0,036",0.9000,1.00,"0,04"`,
		`"15/10/2024, 12:00:00",Interest WITHDRAWN USD Class R IE000H9J0QX4,"-0,02","-0,018",0.9000,,`,
		`"01/11/2024, 12:00:00",SELL USD Class R IE000H9J0QX4,"-50,00","-45,00",0.9000,1.00,"50,00"`,
		`Date,Description,"Value, GBP","Value, EUR",FX Rate,Price per share,Quantity of shares`,
		`"15/01/2025, 12:00:00",BUY GBP Class R IE0002RUHW32,"1 000,19","1 200,00",1.2000,1.00,"1 000,19"`,
		`"16/01/2025, 02:00:00",Interest PAID GBP Class R IE0002RUHW32,"0,10","0,12",1.2000,,`,
		`"16/01/2025, 02:00:00",Service Fee Charged GBP Class IE0002RUHW32,"-0,02","-0,024",1.2000,,`,
	}, "\n")
}

func TestParseRevolutSavingsStatementBasics(t *testing.T) {
	parsed, err := Parse(fixtureSavingsMultiCurrency())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Profile != "revolut_savings_statement" {
		t.Fatalf("profile = %q, want revolut_savings_statement", parsed.Profile)
	}
	if parsed.Institution != "Revolut" {
		t.Fatalf("institution = %q, want Revolut", parsed.Institution)
	}

	var usdSum, gbpSum decimal.Decimal
	usdOps := map[string]int{}
	gbpOps := map[string]int{}
	for _, tx := range parsed.Transactions {
		switch tx.Currency {
		case "USD":
			usdSum = usdSum.Add(tx.Amount)
			usdOps[tx.Raw["op"]]++
		case "GBP":
			gbpSum = gbpSum.Add(tx.Amount)
			gbpOps[tx.Raw["op"]]++
		default:
			t.Fatalf("unexpected currency %q", tx.Currency)
		}
		if tx.AccountHint != "Flexible Cash Funds" {
			t.Errorf("AccountHint = %q, want Flexible Cash Funds", tx.AccountHint)
		}
		if tx.KindHint != "brokerage" {
			t.Errorf("KindHint = %q, want brokerage", tx.KindHint)
		}
	}

	// USD: 100 - 0.01 + 0.05 - 0.04 + 0.04 - 0.02 - 50 = 50.02
	wantUSD := decimal.RequireFromString("50.02")
	if !usdSum.Equal(wantUSD) {
		t.Errorf("USD sum = %s, want %s", usdSum, wantUSD)
	}
	wantUSDOps := map[string]int{"buy": 2, "service_fee": 1, "interest_paid": 1, "interest_reinvested": 1, "interest_withdrawn": 1, "sell": 1}
	for op, want := range wantUSDOps {
		if usdOps[op] != want {
			t.Errorf("USD op %s count = %d, want %d", op, usdOps[op], want)
		}
	}

	// GBP: 1000.19 + 0.10 - 0.02 = 1000.27 (locks in NBSP/space thousand separator)
	wantGBP := decimal.RequireFromString("1000.27")
	if !gbpSum.Equal(wantGBP) {
		t.Errorf("GBP sum = %s, want %s", gbpSum, wantGBP)
	}
	wantGBPOps := map[string]int{"buy": 1, "interest_paid": 1, "service_fee": 1}
	for op, want := range wantGBPOps {
		if gbpOps[op] != want {
			t.Errorf("GBP op %s count = %d, want %d", op, gbpOps[op], want)
		}
	}
}

// TestSavingsStatementOpDescriptionsStable pins the canonical descriptions
// the parser emits for each op kind. These power fingerprint dedup on
// re-imports, so changes here are observable across import batches.
func TestSavingsStatementOpDescriptionsStable(t *testing.T) {
	parsed, err := Parse(fixtureSavingsMultiCurrency())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{
		"buy":                 "Buy USD fund shares (IE000H9J0QX4)",
		"sell":                "Sell USD fund shares (IE000H9J0QX4)",
		"interest_paid":       "Interest paid - Flexible Cash Funds (IE000H9J0QX4)",
		"service_fee":         "Service fee - Flexible Cash Funds (IE000H9J0QX4)",
		"interest_reinvested": "Interest reinvested - Flexible Cash Funds (IE000H9J0QX4)",
		"interest_withdrawn":  "Interest withdrawn - Flexible Cash Funds (IE000H9J0QX4)",
	}
	got := map[string]string{}
	for _, tx := range parsed.Transactions {
		if tx.Currency != "USD" {
			continue
		}
		op := tx.Raw["op"]
		if _, seen := got[op]; seen {
			continue
		}
		if tx.Description == nil {
			t.Fatalf("op %s has nil description", op)
		}
		got[op] = *tx.Description
	}
	for op, w := range want {
		if got[op] != w {
			t.Errorf("op %s: description = %q, want %q", op, got[op], w)
		}
	}
}

// TestSavingsStatementDetectionRejectsLookalikes guards against grabbing
// CSVs whose first column happens to start with "Date,Description,Value".
// The parser dispatcher only routes here when the structural markers
// (FX Rate, Price per share, Quantity of shares) are also present.
func TestSavingsStatementDetectionRejectsLookalikes(t *testing.T) {
	cases := []struct {
		name      string
		firstLine string
		want      bool
	}{
		{"savings-statement", `Date,Description,"Value, USD","Value, EUR",FX Rate,Price per share,Quantity of shares`, true},
		{"gbp-primary", `Date,Description,"Value, GBP","Value, EUR",FX Rate,Price per share,Quantity of shares`, true},
		{"missing-fx-rate", `Date,Description,"Value, USD","Value, EUR",Price per share,Quantity of shares`, false},
		{"some-other-csv", `Date,Description,Value,Notes`, false},
		{"banking-export", `Tipo,Produto,Data de início,Data de Conclusão,Descrição,Montante,Comissão,Moeda,Estado,Saldo`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRevolutSavingsStatement(tc.firstLine); got != tc.want {
				t.Fatalf("isRevolutSavingsStatement(%q) = %v, want %v", tc.firstLine, got, tc.want)
			}
		})
	}
}
