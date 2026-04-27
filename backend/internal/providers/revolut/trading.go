// Package revolut parses Revolut exports into canonical investment events.
// This file handles the Revolut Trading CSV (US-style brokerage account):
// header columns Date, Ticker, Type, Quantity, Price per share, Total Amount,
// Currency, FX Rate. Type strings come from Revolut as e.g. "BUY - MARKET",
// "SELL - LIMIT", "DIVIDEND", "DIVIDEND TAX (CORRECTION)", "STOCK SPLIT",
// "POSITION CLOSURE", "CUSTODY FEE", "ROBO MANAGEMENT FEE". The parser maps
// the trade-shaped rows into ImportEvents and computes implicit commissions
// from `(quantity*price - totalAmount)` since Revolut bakes regulatory fees
// into Total Amount instead of breaking them out.
package revolut

import (
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/investments/importevent"
)

// ParseResult is what the importer hands to the investments service.
type ParseResult struct {
	Events []importevent.Event
}

var amountStrip = regexp.MustCompile(`^[A-Z]{3}\s*`)

// ParseTradingCSV parses a Revolut Trading export.
func ParseTradingCSV(content []byte) (*ParseResult, error) {
	r := csv.NewReader(strings.NewReader(string(content)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := indexHeader(header)
	if idx["Date"] < 0 || idx["Type"] < 0 || idx["Total Amount"] < 0 {
		return nil, errors.New("revolut trading: missing required columns")
	}

	out := make([]importevent.Event, 0, 64)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, csv.ErrFieldCount) {
				continue
			}
			return nil, err
		}
		ev, ok := mapTradingRow(row, idx)
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	return &ParseResult{Events: out}, nil
}

func mapTradingRow(row []string, idx map[string]int) (importevent.Event, bool) {
	dateRaw := get(row, idx["Date"])
	t, err := parseRevolutDate(dateRaw)
	if err != nil {
		return importevent.Event{}, false
	}
	typ := strings.ToUpper(strings.TrimSpace(get(row, idx["Type"])))
	totalAmount := parseRevolutAmount(get(row, idx["Total Amount"]))
	currency := strings.ToUpper(strings.TrimSpace(get(row, idx["Currency"])))
	if currency == "" {
		currency = "USD"
	}
	ticker := strings.ToUpper(strings.TrimSpace(get(row, idx["Ticker"])))
	quantity, _ := decimal.NewFromString(strings.TrimSpace(get(row, idx["Quantity"])))
	pricePerShare := parseRevolutAmount(get(row, idx["Price per share"]))

	switch {
	case isBuyType(typ) && ticker != "":
		fee := implicitFee(quantity, pricePerShare, totalAmount)
		return importevent.Event{
			Kind:      importevent.Trade,
			TradeSide: "buy",
			Symbol:    ticker,
			Date:      t,
			Quantity:  quantity.Abs(),
			Price:     pricePerShare.Abs(),
			Fee:       fee,
			Currency:  currency,
		}, true
	case isSellType(typ) && ticker != "":
		fee := implicitFee(quantity, pricePerShare, totalAmount)
		return importevent.Event{
			Kind:      importevent.Trade,
			TradeSide: "sell",
			Symbol:    ticker,
			Date:      t,
			Quantity:  quantity.Abs(),
			Price:     pricePerShare.Abs(),
			Fee:       fee,
			Currency:  currency,
		}, true
	case (typ == "DIVIDEND" || typ == "DIVIDEND TAX (CORRECTION)") && ticker != "":
		amount := totalAmount.Abs()
		// DIVIDEND TAX corrections are negative — feed them through as a
		// withholding so net dividends stay accurate when both rows exist.
		taxWithheld := decimal.Zero
		if typ == "DIVIDEND TAX (CORRECTION)" {
			taxWithheld = amount
		}
		// Per-unit amount: only fill when quantity is present.
		perUnit := decimal.Zero
		if quantity.GreaterThan(decimal.Zero) {
			perUnit = amount.Div(quantity)
		}
		return importevent.Event{
			Kind:          importevent.Dividend,
			Symbol:        ticker,
			Date:          t,
			AmountTotal:   amount,
			AmountPerUnit: perUnit,
			TaxWithheld:   taxWithheld,
			Currency:      currency,
		}, true
	default:
		return importevent.Event{}, false
	}
}

func implicitFee(quantity, price, total decimal.Decimal) decimal.Decimal {
	if quantity.IsZero() || price.IsZero() {
		return decimal.Zero
	}
	expected := quantity.Mul(price)
	diff := total.Abs().Sub(expected.Abs()).Abs()
	threshold := decimal.NewFromFloat(0.005)
	if diff.LessThan(threshold) {
		return decimal.Zero
	}
	return diff
}

func parseRevolutAmount(raw string) decimal.Decimal {
	s := strings.TrimSpace(raw)
	if s == "" {
		return decimal.Zero
	}
	s = amountStrip.ReplaceAllString(s, "")
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return decimal.Zero
	}
	return d
}

func parseRevolutDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	// Try ISO-with-time first, then date-only, then Revolut's verbose form.
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("revolut: unparseable date")
}

func indexHeader(header []string) map[string]int {
	out := map[string]int{
		"Date": -1, "Ticker": -1, "Type": -1, "Quantity": -1,
		"Price per share": -1, "Total Amount": -1, "Currency": -1, "FX Rate": -1,
	}
	for i, raw := range header {
		key := strings.TrimSpace(raw)
		if _, ok := out[key]; ok {
			out[key] = i
		}
	}
	return out
}

func get(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

func isBuyType(t string) bool {
	switch t {
	case "BUY", "BUY - MARKET", "BUY - LIMIT", "MERGER - STOCK":
		return true
	}
	return false
}

func isSellType(t string) bool {
	switch t {
	case "SELL", "SELL - MARKET", "SELL - LIMIT", "POSITION CLOSURE":
		return true
	}
	return false
}
