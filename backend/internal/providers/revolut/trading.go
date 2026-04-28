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
//
// MERGER - STOCK rows come in pairs for stock-for-stock deals: one with a
// negative quantity on the disappearing ticker and one with a positive
// quantity on the new ticker, both at the same near-instant timestamp and
// USD 0 cash. The parser buffers these rows and, when it finds a same-second
// opposite-sign pair, emits a SELL of the old ticker at average cost (so
// realized P&L on the merger is zero) and a BUY of the new ticker priced so
// the prior position's cost basis transfers cleanly. Unpaired rows
// (timestamps too far apart or only one side present in the file) fall back
// to a price-0 synthetic trade — quantity tracks but cost basis is lost,
// matching the best we can do without the other side.
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
	runningQty := make(map[string]decimal.Decimal)
	runningCost := make(map[string]decimal.Decimal)
	pendingMergers := make([]pendingMerger, 0, 4)
	emit := func(ev importevent.Event) {
		out = append(out, ev)
		updateRunning(runningQty, runningCost, ev)
	}
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
			ev, ok := mapStockSplitRow(row, idx, out, runningQty, runningCost)
			if !ok {
				continue
			}
			emit(ev)
		case typ == "POSITION CLOSURE":
			ev, ok := mapPositionClosureRow(row, idx, runningQty)
			if !ok {
				continue
			}
			emit(ev)
		case typ == "MERGER - STOCK":
			pm, ok := parseMergerRow(row, idx)
			if !ok {
				continue
			}
			matched := -1
			for i, other := range pendingMergers {
				if mergerPairEligible(pm, other) {
					matched = i
					break
				}
			}
			if matched < 0 {
				pendingMergers = append(pendingMergers, pm)
				continue
			}
			other := pendingMergers[matched]
			pendingMergers = append(pendingMergers[:matched], pendingMergers[matched+1:]...)
			sellEv, buyEv := buildMergerPair(pm, other, runningQty, runningCost)
			// Emit SELL first so the old ticker's cost basis is consumed
			// before the new ticker picks it up; same instant on the wire.
			emit(sellEv)
			emit(buyEv)
		case strings.HasPrefix(typ, "TRANSFER FROM "):
			ev, ok := mapTransferRow(row, idx, runningQty)
			if !ok {
				continue
			}
			emit(ev)
		default:
			ev, ok := mapTradingRow(row, idx)
			if !ok {
				continue
			}
			emit(ev)
		}
	}
	// Anything left in pendingMergers never found a partner — emit each as a
	// synthetic price-0 trade so quantity at least tracks.
	for _, pm := range pendingMergers {
		emit(mergerFallbackEvent(pm))
	}
	return &ParseResult{Events: out}, nil
}

// updateRunning keeps a per-ticker running quantity *and* total cost basis.
// Quantity tracking lets STOCK SPLIT rows locate renamed positions and
// POSITION CLOSURE rows close out the right size. Cost tracking feeds the
// MERGER - STOCK pairing path so the old ticker's cost basis can transfer
// cleanly into the new ticker's lots. Cost is reduced pro-rata on sells
// using running average cost — close enough for what the import path needs;
// the engine itself still does FIFO on persisted lots.
func updateRunning(qty, cost map[string]decimal.Decimal, ev importevent.Event) {
	if ev.Kind != importevent.Trade || ev.Symbol == "" {
		return
	}
	sym := ev.Symbol
	eventCost := ev.Price.Mul(ev.Quantity).Add(ev.Fee)
	switch ev.TradeSide {
	case "buy":
		qty[sym] = qty[sym].Add(ev.Quantity)
		cost[sym] = cost[sym].Add(eventCost)
	case "sell":
		priorQty := qty[sym]
		priorCost := cost[sym]
		if priorQty.GreaterThan(decimal.Zero) {
			avg := priorCost.Div(priorQty)
			consumed := avg.Mul(ev.Quantity)
			next := priorCost.Sub(consumed)
			if next.IsNegative() {
				next = decimal.Zero
			}
			cost[sym] = next
		}
		qty[sym] = priorQty.Sub(ev.Quantity)
	}
}

// mapStockSplitRow converts a STOCK SPLIT row into a synthetic adjustment
// trade and, when the split row's ticker has no prior position, attempts to
// detect a same-event symbol rename by matching the delta against running
// quantities of previously-seen tickers. Returns false when the row carries
// no usable quantity.
func mapStockSplitRow(row []string, idx map[string]int, out []importevent.Event, runningQty, runningCost map[string]decimal.Decimal) (importevent.Event, bool) {
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
	if cur, ok := runningQty[ticker]; (!ok || cur.IsZero()) && delta.IsNegative() {
		absDelta := delta.Abs()
		var bestSym string
		var bestQty decimal.Decimal
		bestRemainderRatio := decimal.NewFromFloat(0.10) // ≤10% leftover
		for sym, qty := range runningQty {
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
			delete(runningQty, bestSym)
			runningQty[ticker] = bestQty
			if c, ok := runningCost[bestSym]; ok {
				delete(runningCost, bestSym)
				runningCost[ticker] = c
			}
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
func mapPositionClosureRow(row []string, idx map[string]int, runningQty map[string]decimal.Decimal) (importevent.Event, bool) {
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
		qty = runningQty[ticker]
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
// (synthetic SELL). Price is 0 since the row carries no consideration.
//
// In practice Revolut's 2023 EU migration emits a single consolidated CSV
// per user that contains both the pre-migration buys/sells (in the old
// entity) and the inbound TRANSFER row marking the move into the new
// entity. The prior rows already built up the running position, so adding
// another synthetic BUY for the same shares would double-count them. When
// the row's positive quantity is already covered by the running position
// for that ticker, the transfer is treated as a no-op — the shares simply
// continue in the new entity. A positive transfer beyond the running
// position (e.g. importing only the receiving entity's CSV with no prior
// history) still emits the BUY so quantity tracks. The negative-quantity
// SELL path is unchanged since Revolut's actual exports don't use it for
// the migration and the symmetric removal isn't subject to the same
// double-count.
func mapTransferRow(row []string, idx map[string]int, runningQty map[string]decimal.Decimal) (importevent.Event, bool) {
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
	if !qty.IsNegative() {
		if prior, ok := runningQty[ticker]; ok && prior.GreaterThanOrEqual(qty) {
			return importevent.Event{}, false
		}
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

// pendingMerger holds a parsed but unemitted MERGER - STOCK row while the
// parser waits for its opposite-sign partner.
type pendingMerger struct {
	time     time.Time
	ticker   string
	quantity decimal.Decimal // signed: negative = old ticker, positive = new ticker
	currency string
}

// mergerPairWindow is the maximum timestamp gap we accept when pairing a
// MERGER - STOCK row with its counterpart. Revolut emits both sides within
// well under a second; one second comfortably tolerates clock skew without
// pairing unrelated rows.
const mergerPairWindow = time.Second

// parseMergerRow extracts the fields the merger pairing path needs. Returns
// false on missing ticker / unparseable date / zero quantity.
func parseMergerRow(row []string, idx map[string]int) (pendingMerger, bool) {
	t, err := parseRevolutDate(get(row, idx["Date"]))
	if err != nil {
		return pendingMerger{}, false
	}
	ticker := strings.ToUpper(strings.TrimSpace(get(row, idx["Ticker"])))
	if ticker == "" {
		return pendingMerger{}, false
	}
	qty, err := decimal.NewFromString(strings.TrimSpace(get(row, idx["Quantity"])))
	if err != nil || qty.IsZero() {
		return pendingMerger{}, false
	}
	currency := strings.ToUpper(strings.TrimSpace(get(row, idx["Currency"])))
	if currency == "" {
		currency = "USD"
	}
	return pendingMerger{time: t, ticker: ticker, quantity: qty, currency: currency}, true
}

// mergerPairEligible reports whether two pending merger rows look like the
// two sides of the same deal: opposite-sign quantities, distinct tickers,
// timestamps within the pairing window.
func mergerPairEligible(a, b pendingMerger) bool {
	if a.ticker == b.ticker {
		return false
	}
	if a.quantity.IsNegative() == b.quantity.IsNegative() {
		return false
	}
	gap := a.time.Sub(b.time)
	if gap < 0 {
		gap = -gap
	}
	return gap <= mergerPairWindow
}

// buildMergerPair turns a paired (negative, positive) MERGER - STOCK into
// two synthetic trades that transfer cost basis from the disappearing
// ticker into the new one. The SELL on the old ticker is priced at average
// cost so realized P&L on the merger itself is zero (matching the
// tax-correct treatment of a stock-for-stock deal); the BUY on the new
// ticker is priced so total cost basis carries over. When the parser has
// no prior position info for the old ticker (partial CSV import), both
// legs fall back to price 0 — quantity still tracks but cost basis is lost.
func buildMergerPair(a, b pendingMerger, runningQty, runningCost map[string]decimal.Decimal) (importevent.Event, importevent.Event) {
	neg, pos := a, b
	if pos.quantity.IsNegative() {
		neg, pos = b, a
	}
	sellQty := neg.quantity.Abs()
	buyQty := pos.quantity.Abs()

	priorQty := runningQty[neg.ticker]
	priorCost := runningCost[neg.ticker]
	transferred := decimal.Zero
	if priorQty.GreaterThan(decimal.Zero) && priorCost.GreaterThan(decimal.Zero) {
		ratio := sellQty.Div(priorQty)
		one := decimal.NewFromInt(1)
		if ratio.GreaterThan(one) {
			ratio = one
		}
		transferred = priorCost.Mul(ratio)
	}

	sellPrice := decimal.Zero
	buyPrice := decimal.Zero
	if !sellQty.IsZero() && !transferred.IsZero() {
		sellPrice = transferred.Div(sellQty)
	}
	if !buyQty.IsZero() && !transferred.IsZero() {
		buyPrice = transferred.Div(buyQty)
	}

	sellEv := importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: "sell",
		Symbol:    neg.ticker,
		Date:      neg.time,
		Quantity:  sellQty,
		Price:     sellPrice,
		Fee:       decimal.Zero,
		Currency:  neg.currency,
	}
	buyEv := importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: "buy",
		Symbol:    pos.ticker,
		Date:      pos.time,
		Quantity:  buyQty,
		Price:     buyPrice,
		Fee:       decimal.Zero,
		Currency:  pos.currency,
	}
	return sellEv, buyEv
}

// mergerFallbackEvent emits an unpaired merger row as a price-0 synthetic
// trade. Quantity tracks but cost basis is lost — best-effort when the
// counterpart is missing from the file.
func mergerFallbackEvent(pm pendingMerger) importevent.Event {
	side := "buy"
	if pm.quantity.IsNegative() {
		side = "sell"
	}
	return importevent.Event{
		Kind:      importevent.Trade,
		TradeSide: side,
		Symbol:    pm.ticker,
		Date:      pm.time,
		Quantity:  pm.quantity.Abs(),
		Price:     decimal.Zero,
		Fee:       decimal.Zero,
		Currency:  pm.currency,
	}
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
	case "BUY", "BUY - MARKET", "BUY - LIMIT":
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
