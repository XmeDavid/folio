// Package revolut parses Revolut exports into canonical investment events.
// This file handles the Revolut Trading CSV (US-style brokerage account):
// header columns Date, Ticker, Type, Quantity, Price per share, Total Amount,
// Currency, FX Rate. Type strings come from Revolut as e.g. "BUY - MARKET",
// "SELL - LIMIT", "DIVIDEND", "DIVIDEND TAX (CORRECTION)", "STOCK SPLIT",
// "POSITION CLOSURE", "CUSTODY FEE", "ROBO MANAGEMENT FEE". The parser maps
// the trade-shaped rows into ImportEvents and computes implicit commissions
// from `(quantity*price - totalAmount)` since Revolut bakes regulatory fees
// into Total Amount instead of breaking them out.
//
// STOCK SPLIT rows in this export carry a quantity *delta* (e.g. -198 for a
// reverse split that consolidates 200 shares into 2) with no cash movement
// (Total Amount = USD 0). They are emitted as synthetic trades at price 0 so
// the held quantity tracks the adjustment. Revolut frequently switches the
// ticker on the split row when the issuer is renamed at the same time
// (CHK → CHKAQ during bankruptcy is the canonical example): when the split
// ticker has no prior position but another ticker's running quantity is
// approximately wiped out by the delta, prior events for that other ticker
// are renamed to the split ticker so the resulting position lines up.
//
// POSITION CLOSURE rows arrive when the broker force-resolves a delisted /
// dead instrument and credits residual cash. Quantity is usually blank, so
// the closure is emitted as a synthetic SELL of the running held quantity at
// a per-unit price derived from the cash credit. Cash-only "TRANSFER FROM
// ... TO ..." rows (no ticker) are skipped, but ticker-bearing transfers
// between Revolut entities are emitted as synthetic trades at price 0 with
// the side driven by the sign of Quantity, so the held position follows the
// shares across the move.
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
	running := make(map[string]decimal.Decimal)
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
		typ := strings.ToUpper(strings.TrimSpace(get(row, idx["Type"])))
		switch {
		case typ == "STOCK SPLIT":
			ev, ok := mapStockSplitRow(row, idx, out, running)
			if !ok {
				continue
			}
			out = append(out, ev)
			updateRunning(running, ev)
		case typ == "POSITION CLOSURE":
			ev, ok := mapPositionClosureRow(row, idx, running)
			if !ok {
				continue
			}
			out = append(out, ev)
			updateRunning(running, ev)
		case strings.HasPrefix(typ, "TRANSFER FROM "):
			ev, ok := mapTransferRow(row, idx)
			if !ok {
				continue
			}
			out = append(out, ev)
			updateRunning(running, ev)
		default:
			ev, ok := mapTradingRow(row, idx)
			if !ok {
				continue
			}
			out = append(out, ev)
			updateRunning(running, ev)
		}
	}
	return &ParseResult{Events: out}, nil
}

// updateRunning keeps a per-ticker running quantity so STOCK SPLIT rows that
// reference a renamed ticker can locate the original position.
func updateRunning(running map[string]decimal.Decimal, ev importevent.Event) {
	if ev.Kind != importevent.Trade || ev.Symbol == "" {
		return
	}
	cur := running[ev.Symbol]
	switch ev.TradeSide {
	case "buy":
		running[ev.Symbol] = cur.Add(ev.Quantity)
	case "sell":
		running[ev.Symbol] = cur.Sub(ev.Quantity)
	}
}

// mapStockSplitRow converts a STOCK SPLIT row into a synthetic adjustment
// trade and, when the split row's ticker has no prior position, attempts to
// detect a same-event symbol rename by matching the delta against running
// quantities of previously-seen tickers. Returns false when the row carries
// no usable quantity.
func mapStockSplitRow(row []string, idx map[string]int, out []importevent.Event, running map[string]decimal.Decimal) (importevent.Event, bool) {
	t, err := parseRevolutDate(get(row, idx["Date"]))
	if err != nil {
		return importevent.Event{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(get(row, idx["Ticker"])))
	if ticker == "" {
		return importevent.Event{}, false
	}
	delta, err := decimal.NewFromString(strings.TrimSpace(get(row, idx["Quantity"])))
	if err != nil || delta.IsZero() {
		return importevent.Event{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(get(row, idx["Currency"])))
	if currency == "" {
		currency = "USD"
	}

	// Rename detection: only when the split ticker is unseen, the delta is
	// negative (reverse-split shape), and exactly one prior ticker's running
	// quantity is approximately wiped out by the delta. Matching prior trades
	// are reassigned to the split ticker so the synthetic adjustment lands on
	// the position it is actually consolidating.
	if cur, ok := running[ticker]; (!ok || cur.IsZero()) && delta.IsNegative() {
		absDelta := delta.Abs()
		var bestSym string
		var bestQty decimal.Decimal
		bestRemainderRatio := decimal.NewFromFloat(0.10) // ≤10% leftover
		for sym, qty := range running {
			if sym == ticker || qty.LessThanOrEqual(decimal.Zero) {
				continue
			}
			if qty.LessThan(absDelta) {
				continue
			}
			leftover := qty.Add(delta).Abs()
			ratio := leftover.Div(qty)
			if ratio.LessThanOrEqual(bestRemainderRatio) {
				bestSym = sym
				bestQty = qty
				bestRemainderRatio = ratio
			}
		}
		if bestSym != "" {
			for i := range out {
				if out[i].Symbol == bestSym {
					out[i].Symbol = ticker
				}
			}
			delete(running, bestSym)
			running[ticker] = bestQty
		}
	}

	side := "buy"
	if delta.IsNegative() {
		side = "sell"
	}
	return importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: side,
		Symbol:    ticker,
		Date:      t,
		Quantity:  delta.Abs(),
		Price:     decimal.Zero,
		Fee:       decimal.Zero,
		Currency:  currency,
	}, true
}

// mapPositionClosureRow handles a Revolut "POSITION CLOSURE" row, which the
// broker emits when a delisted/dead instrument is force-resolved and the
// residual cash credited. Quantity is typically blank, so the closure is
// expressed as a synthetic SELL of the running held quantity at a per-unit
// price derived from the cash credit. Rows with no held quantity are skipped
// — there is nothing to close out — to avoid inserting a phantom sell.
func mapPositionClosureRow(row []string, idx map[string]int, running map[string]decimal.Decimal) (importevent.Event, bool) {
	t, err := parseRevolutDate(get(row, idx["Date"]))
	if err != nil {
		return importevent.Event{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(get(row, idx["Ticker"])))
	if ticker == "" {
		return importevent.Event{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(get(row, idx["Currency"])))
	if currency == "" {
		currency = "USD"
	}
	totalAmount := parseRevolutAmount(get(row, idx["Total Amount"])).Abs()
	qty, _ := decimal.NewFromString(strings.TrimSpace(get(row, idx["Quantity"])))
	qty = qty.Abs()
	if qty.IsZero() {
		qty = running[ticker]
	}
	if qty.LessThanOrEqual(decimal.Zero) {
		return importevent.Event{}, false
	}
	perUnit := decimal.Zero
	if !totalAmount.IsZero() {
		perUnit = totalAmount.Div(qty)
	}
	return importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: "sell",
		Symbol:    ticker,
		Date:      t,
		Quantity:  qty,
		Price:     perUnit,
		Fee:       decimal.Zero,
		Currency:  currency,
	}, true
}

// mapTransferRow handles ticker-bearing "TRANSFER FROM ... TO ..." rows that
// move shares between Revolut entities (e.g. Revolut Trading Ltd ->
// Revolut Securities Europe UAB). Cash-only transfers have an empty ticker
// and never reach this path. Sign of Quantity drives direction: positive for
// the receiving side (synthetic BUY), negative for the sending side
// (synthetic SELL). Price is 0 since the row carries no consideration; the
// original cost basis cannot be reconstructed from a single-account export
// and stays for the user to reconcile if both sides are imported.
func mapTransferRow(row []string, idx map[string]int) (importevent.Event, bool) {
	t, err := parseRevolutDate(get(row, idx["Date"]))
	if err != nil {
		return importevent.Event{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(get(row, idx["Ticker"])))
	if ticker == "" {
		return importevent.Event{}, false
	}
	qty, err := decimal.NewFromString(strings.TrimSpace(get(row, idx["Quantity"])))
	if err != nil || qty.IsZero() {
		return importevent.Event{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(get(row, idx["Currency"])))
	if currency == "" {
		currency = "USD"
	}
	side := "buy"
	if qty.IsNegative() {
		side = "sell"
	}
	return importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: side,
		Symbol:    ticker,
		Date:      t,
		Quantity:  qty.Abs(),
		Price:     decimal.Zero,
		Fee:       decimal.Zero,
		Currency:  currency,
	}, true
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
	case "SELL", "SELL - MARKET", "SELL - LIMIT":
		return true
	}
	return false
}
