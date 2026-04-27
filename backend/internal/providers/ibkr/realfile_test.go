package ibkr

import (
	"os"
	"testing"
)

// TestParseRealActivityCSV exercises the parser against a real Activity
// Statement export checked into the legacy app's data dir. The test is
// best-effort — it only runs when the file is present so CI on a fresh
// checkout doesn't fail. Treat the assertions as smoke checks: the file
// must produce some trades and at least one dividend event with non-zero
// withholding tax.
func TestParseRealActivityCSV(t *testing.T) {
	const path = "/Users/xmedavid/dev/folio/legacy/data/ibkr_feb.csv"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("sample file %s not present: %v", path, err)
	}
	res, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != FormatActivityCSV {
		t.Fatalf("format = %s, want activity_csv", res.Format)
	}
	if res.BaseCurrency == "" {
		t.Fatal("baseCurrency should be detected")
	}
	var trades, dividends, withTax int
	for _, ev := range res.Events {
		switch ev.Kind {
		case "trade":
			trades++
		case "dividend":
			dividends++
			if !ev.TaxWithheld.IsZero() {
				withTax++
			}
		}
	}
	if trades == 0 {
		t.Fatal("expected some trades parsed from real file")
	}
	if dividends == 0 {
		t.Fatal("expected some dividends parsed from real file")
	}
	t.Logf("base=%s trades=%d dividends=%d (with withholding=%d)",
		res.BaseCurrency, trades, dividends, withTax)
}
