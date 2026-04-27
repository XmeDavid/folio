package investments

import (
	"os"
	"testing"
)

// TestDetectAndParse_RealIBKRFile is the end-to-end check against the user's
// actual broker export. Skipped when the file isn't present (e.g. on CI).
// This is the test that pins down "the smart-import dispatcher recognises my
// real IBKR file" — independent of the synthesised cases above.
func TestDetectAndParse_RealIBKRFile(t *testing.T) {
	const path = "/Users/xmedavid/dev/folio/legacy/data/ibkr_feb.csv"
	body, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("sample file %s not present: %v", path, err)
	}
	src, events, base, err := detectAndParse(body)
	if err != nil {
		t.Fatalf("detectAndParse: %v", err)
	}
	if src != "ibkr" {
		t.Fatalf("source = %q, want ibkr (first 16 bytes: % x)", src, body[:16])
	}
	if base == "" {
		t.Fatal("baseCurrency should be detected")
	}
	if len(events) == 0 {
		t.Fatal("expected events from real file")
	}
	var trades, dividends int
	for _, ev := range events {
		switch ev.Kind {
		case "trade":
			trades++
		case "dividend":
			dividends++
		}
	}
	t.Logf("real-file: source=%s base=%s events=%d (trades=%d dividends=%d)",
		src, base, len(events), trades, dividends)
	if trades == 0 {
		t.Fatal("expected some trades from real file")
	}
}
