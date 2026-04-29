package bankimport

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// isRevolutSavingsStatement sniffs the first line for Revolut's Flexible
// Cash Funds savings-statement CSV header. The export's first line is
// `Date,Description,"Value, X","Value, EUR",FX Rate,Price per share,
// Quantity of shares` where X is one of USD/GBP/EUR (the file's primary
// reporting currency). Subsequent currency sections drop in their own
// header rows mid-file.
func isRevolutSavingsStatement(firstLine string) bool {
	if !strings.HasPrefix(firstLine, "Date,Description,\"Value, ") {
		return false
	}
	// Cheap structural check — the column list is fixed by Revolut's export
	// schema and we don't want to swallow other CSVs that happen to start
	// with the same prefix.
	for _, marker := range []string{"FX Rate", "Price per share", "Quantity of shares"} {
		if !strings.Contains(firstLine, marker) {
			return false
		}
	}
	return true
}

// Per-section header layout. The savings-statement export groups
// transactions by the fund's quote currency. Each block has its own header
// row that names the native value column (`Value, USD`, `Value, GBP`,
// `Value, EUR`). Single-currency exports (e.g. EUR-only customers) skip
// the redundant "Value, EUR" companion column, so the column count varies
// between sections — we read each header dynamically instead of assuming
// the first one carries through.
type savingsSection struct {
	currency  string
	cols      map[string]int // logical-name → column index
	rows      [][]string
	startLine int // first data-row index in records (for error context)
}

// parseRevolutSavingsStatement walks the file's three currency sub-tables
// and emits one ParsedTransaction per real event row. Each section's first
// header decides the native currency for that block; later sections supply
// their own header. Events use AccountHint="Flexible Cash Funds" and
// KindHint="brokerage" so the importer creates a per-currency brokerage
// account that pairs with the Flexible Cash Funds account the consolidated
// MMF parser already emits — when both files are imported, the savings
// statement's per-event detail lands in the same account as the
// consolidated's net-interest summary.
//
// Sign convention preserved from the source file:
//
//   - BUY                  Value > 0  (cash converted into shares)
//   - SELL                 Value < 0  (shares redeemed back to cash)
//   - Interest PAID        Value > 0  (gross interest credited)
//   - Service Fee Charged  Value < 0  (fee debited)
//   - Interest Reinvested  Value < 0  (cash leg of reinvestment; matched
//     by an immediate BUY for the share leg)
//   - Interest WITHDRAWN   Value < 0  (interest cashed out to the main
//     account — leaves the fund)
//
// Summing all of a section's rows over the full statement period gives the
// fund balance change since the file's start date. Absolute balance still
// requires an opening-balance figure when the user first creates the Folio
// account, since this export only covers a finite window.
func parseRevolutSavingsStatement(content string) (ParsedFile, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return ParsedFile{}, httpx.NewValidationError("could not parse Revolut savings-statement CSV")
	}
	sections, err := splitSavingsSections(records)
	if err != nil {
		return ParsedFile{}, err
	}
	out := ParsedFile{
		Profile:     "revolut_savings_statement",
		Institution: "Revolut",
	}
	skipped := 0
	for _, sec := range sections {
		for _, row := range sec.rows {
			tx, ok, perr := parseSavingsRow(row, sec)
			if perr != nil {
				return ParsedFile{}, perr
			}
			if !ok {
				skipped++
				continue
			}
			out.Transactions = append(out.Transactions, tx)
		}
	}
	if skipped > 0 {
		out.Warnings = append(out.Warnings,
			fmt.Sprintf("Skipped %d unparseable savings-statement rows.", skipped))
	}
	out.Warnings = append(out.Warnings,
		"Imported Flexible Cash Funds events at full granularity. The fund's "+
			"absolute balance depends on the opening balance you set when "+
			"creating the Folio account — the export only covers events from "+
			"its start date onward.")
	finalizeParsed(&out)
	return out, nil
}

// splitSavingsSections walks the CSV rows, splitting on each header line.
// A header is identified by row[0] == "Date" and row[1] == "Description".
// The native currency is read from the third column's header text (e.g.
// "Value, USD" → "USD").
var savingsCurrencyRe = regexp.MustCompile(`Value,\s*([A-Z]{3})`)

func splitSavingsSections(records [][]string) ([]savingsSection, error) {
	var sections []savingsSection
	var cur *savingsSection
	for i, row := range records {
		if len(row) < 2 {
			continue
		}
		// Trim every cell so trailing whitespace doesn't break the header
		// detection or column lookups below.
		for j := range row {
			row[j] = strings.TrimSpace(row[j])
		}
		if row[0] == "Date" && row[1] == "Description" {
			if cur != nil {
				sections = append(sections, *cur)
			}
			cols, currency, ok := detectSavingsColumns(row)
			if !ok {
				return nil, httpx.NewValidationError(fmt.Sprintf(
					"savings-statement header at row %d is missing native value column", i+1))
			}
			cur = &savingsSection{
				currency:  currency,
				cols:      cols,
				startLine: i + 1,
			}
			continue
		}
		if cur == nil || row[0] == "" {
			continue
		}
		cur.rows = append(cur.rows, row)
	}
	if cur != nil {
		sections = append(sections, *cur)
	}
	if len(sections) == 0 {
		return nil, httpx.NewValidationError("savings-statement CSV had no sections")
	}
	return sections, nil
}

// detectSavingsColumns picks out the native-currency value column from the
// section header along with the supplementary (Description, Price, Quantity)
// columns. EUR-primary exports drop the "Value, EUR" duplicate so we can't
// blindly index by position — read each header by name.
func detectSavingsColumns(header []string) (map[string]int, string, bool) {
	cols := map[string]int{}
	currency := ""
	for i, raw := range header {
		c := strings.TrimSpace(raw)
		switch c {
		case "Date":
			cols["date"] = i
		case "Description":
			cols["description"] = i
		case "FX Rate":
			cols["fx_rate"] = i
		case "Price per share":
			cols["price"] = i
		case "Quantity of shares":
			cols["qty"] = i
		default:
			if m := savingsCurrencyRe.FindStringSubmatch(c); m != nil {
				if currency == "" {
					currency = m[1]
					cols["value"] = i
				}
				// Ignore the second "Value, EUR" companion column on USD/GBP-
				// primary exports — we work in the native currency only.
			}
		}
	}
	if _, ok := cols["date"]; !ok {
		return nil, "", false
	}
	if _, ok := cols["value"]; !ok {
		return nil, "", false
	}
	return cols, currency, true
}

// parseSavingsRow turns one statement row into a brokerage-account tx.
// Returns (zero, false, nil) on rows the parser should soft-skip (e.g.
// blank dates, malformed amounts) — those bump the skipped counter so
// the warning surfaces unexpected drops.
func parseSavingsRow(row []string, sec savingsSection) (ParsedTransaction, bool, error) {
	dateRaw := safeField(row, sec.cols["date"])
	if dateRaw == "" {
		return ParsedTransaction{}, false, nil
	}
	bookedAt, err := parseSavingsDate(dateRaw)
	if err != nil {
		return ParsedTransaction{}, false, nil
	}
	valueRaw := safeField(row, sec.cols["value"])
	if valueRaw == "" {
		return ParsedTransaction{}, false, nil
	}
	amount, err := parseSavingsDecimal(valueRaw)
	if err != nil {
		return ParsedTransaction{}, false, fmt.Errorf("parse savings amount %q: %w", valueRaw, err)
	}
	descRaw := safeField(row, sec.cols["description"])
	op, isin := classifySavingsAction(descRaw)
	if op == "" {
		return ParsedTransaction{}, false, nil
	}
	desc := savingsDescription(op, isin, sec.currency)
	descPtr := &desc
	raw := map[string]string{
		"section":  "Flexible Cash Funds",
		"currency": sec.currency,
		"op":       op,
		"isin":     isin,
	}
	if px := safeField(row, sec.cols["price"]); px != "" {
		raw["price_per_share"] = px
	}
	if qty := safeField(row, sec.cols["qty"]); qty != "" {
		raw["quantity"] = qty
	}
	if fx := safeField(row, sec.cols["fx_rate"]); fx != "" {
		raw["fx_rate"] = fx
	}
	raw["raw_description"] = descRaw
	return ParsedTransaction{
		BookedAt:    bookedAt,
		Amount:      amount,
		Currency:    sec.currency,
		Description: descPtr,
		AccountHint: "Flexible Cash Funds",
		KindHint:    "brokerage",
		Raw:         raw,
	}, true, nil
}

// classifySavingsAction extracts the action keyword and fund ISIN from the
// row's free-form Description. The export's text format is fixed by Revolut
// but mixes the action's words ("BUY", "SELL", "Interest PAID", …) with
// supporting tokens (currency, "Class R", and the fund ISIN). Returns op as
// a stable key the importer can use without re-parsing the original text.
//
// Match priority matters: "Interest Reinvested" must be checked before the
// generic "Interest" prefix, otherwise reinvested rows would tag as
// "interest" and skew the cash-vs-share-side accounting.
func classifySavingsAction(desc string) (op string, isin string) {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return "", ""
	}
	for _, tok := range strings.Fields(desc) {
		if isISIN(tok) {
			isin = tok
			break
		}
	}
	switch {
	case strings.HasPrefix(desc, "BUY"):
		op = "buy"
	case strings.HasPrefix(desc, "SELL"):
		op = "sell"
	case strings.Contains(desc, "Interest Reinvested"):
		op = "interest_reinvested"
	case strings.Contains(desc, "Interest WITHDRAWN"):
		op = "interest_withdrawn"
	case strings.Contains(desc, "Interest PAID"):
		op = "interest_paid"
	case strings.Contains(desc, "Service Fee Charged"):
		op = "service_fee"
	}
	return op, isin
}

// isISIN matches the standard ISIN shape: 2-letter country, 9-char
// alphanumeric body, 1 check digit. Conservative — Revolut's IDs all hit
// IE/US/LU/etc. registries, but we just want to distinguish the token from
// surrounding currency/class labels.
func isISIN(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i, r := range s {
		switch {
		case i < 2 && (r < 'A' || r > 'Z'):
			return false
		case i >= 2 && !(r >= '0' && r <= '9') && !(r >= 'A' && r <= 'Z'):
			return false
		}
	}
	return true
}

// savingsDescription returns the human-facing description for the imported
// row. We deliberately use a stable, terse phrasing instead of echoing
// Revolut's "BUY USD Class R IE000H9J0QX4" so the same logical event
// reads consistently across the three currency sections and so future UI
// changes (renaming "Service Fee Charged" → "Service fee", …) don't
// retroactively break fingerprint dedup.
func savingsDescription(op, isin, currency string) string {
	suffix := ""
	if isin != "" {
		suffix = " (" + isin + ")"
	}
	switch op {
	case "buy":
		return "Buy " + currency + " fund shares" + suffix
	case "sell":
		return "Sell " + currency + " fund shares" + suffix
	case "interest_paid":
		return "Interest paid - Flexible Cash Funds" + suffix
	case "service_fee":
		return "Service fee - Flexible Cash Funds" + suffix
	case "interest_reinvested":
		return "Interest reinvested - Flexible Cash Funds" + suffix
	case "interest_withdrawn":
		return "Interest withdrawn - Flexible Cash Funds" + suffix
	}
	return "Flexible Cash Funds activity" + suffix
}

// parseSavingsDate accepts the export's "dd/MM/yyyy, HH:MM:SS" stamp.
// Drops the time component (the importer keys off date-only).
func parseSavingsDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if comma := strings.Index(s, ","); comma >= 0 {
		s = strings.TrimSpace(s[:comma])
	}
	t, err := time.ParseInLocation("02/01/2006", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid savings-statement date %q", raw)
	}
	return datePart(t), nil
}

// parseSavingsDecimal handles the file's mixed-locale numbers: pt-pt rows
// use comma as the decimal mark ("0,0125") and ASCII or U+00A0 spaces as
// thousand separators ("1 000,19"), while the FX-rate column uses dots
// ("0.8488"). Strip whitespace first so the regex below sees a single
// number; without that step "-1 000,19" is mangled into garbage.
func parseSavingsDecimal(raw string) (decimal.Decimal, error) {
	s := stripWhitespace(strings.TrimSpace(raw))
	if s == "" {
		return decimal.Zero, nil
	}
	hasDot := strings.Contains(s, ".")
	hasComma := strings.Contains(s, ",")
	switch {
	case hasDot && hasComma:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasComma:
		s = strings.Replace(s, ",", ".", 1)
	}
	return decimal.NewFromString(s)
}
