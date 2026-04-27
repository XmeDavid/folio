// Package ibkr parses Interactive Brokers exports into canonical investment
// events. Two input shapes are supported:
//
//   - "JSON" — a small wrapper format the legacy app introduced for
//     pre-cleaned buy-only data: { account, currency, transactions: [...] }.
//   - "Activity CSV" — the standard multi-section CSV export IBKR Flex
//     produces. The parser scans for `Trades,Data,Order,Stocks,...` rows
//     and emits one event per execution; "Account Information,Data,Base
//     Currency,..." anchors the report's base currency.
//
// Forex executions, fees, and other sections are ignored for v1; they will
// land via dedicated event kinds when the importer gains those domain hooks.
package ibkr

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/investments/importevent"
)

// symbolFromDividendDesc captures the leading "TICKER(ISIN) ..." that IBKR
// uses on dividend / withholding-tax descriptions. Group 1 is the symbol,
// group 2 the optional ISIN.
var symbolFromDividendDesc = regexp.MustCompile(`^\s*([A-Z][A-Z0-9.]*)\(([A-Z]{2}[A-Z0-9]{9}\d)?\)`)
var perShareFromDividendDesc = regexp.MustCompile(`(?i)([A-Z]{3})\s+([0-9]+(?:\.[0-9]+)?)\s+per\s+share`)

// Format identifies the detected input shape.
type Format string

const (
	FormatJSON        Format = "json"
	FormatActivityCSV Format = "activity_csv"
)

// ParseResult is what the importer hands to the investments service.
type ParseResult struct {
	BaseCurrency string
	Format       Format
	Events       []importevent.Event
}

type ibkrJSONFile struct {
	Account      string             `json:"account"`
	Currency     string             `json:"currency"`
	Transactions []ibkrJSONTradeRow `json:"transactions"`
}

type ibkrJSONTradeRow struct {
	Date               string  `json:"date"`
	Symbol             string  `json:"symbol"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	TradeAmountDebited float64 `json:"trade_amount_debited"`
	Commission         float64 `json:"commission"`
}

// Parse routes content to the right parser based on a leading-byte sniff.
func Parse(content []byte) (*ParseResult, error) {
	trimmed := strings.TrimLeft(string(content), " \t\r\n\uFEFF")
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return parseJSON([]byte(trimmed))
	}
	return parseActivityCSV(content)
}

func parseJSON(content []byte) (*ParseResult, error) {
	var data ibkrJSONFile
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("ibkr json: %w", err)
	}
	if data.Currency == "" {
		data.Currency = "USD"
	}
	currency := strings.ToUpper(strings.TrimSpace(data.Currency))
	out := make([]importevent.Event, 0, len(data.Transactions))
	for _, row := range data.Transactions {
		if row.Symbol == "" || row.Quantity <= 0 {
			continue
		}
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			continue
		}
		out = append(out, importevent.Event{
			Kind:      importevent.Trade,
			TradeSide: "buy",
			Symbol:    strings.ToUpper(strings.TrimSpace(row.Symbol)),
			Date:      date,
			Quantity:  decimal.NewFromFloat(row.Quantity),
			Price:     decimal.NewFromFloat(row.UnitPrice),
			Fee:       decimal.NewFromFloat(absFloat(row.Commission)),
			Currency:  currency,
		})
	}
	return &ParseResult{BaseCurrency: currency, Format: FormatJSON, Events: out}, nil
}

// dividendKey groups dividend rows with their matching withholding-tax row.
// IBKR emits both in separate sections but with identical (date, symbol,
// currency) triples, so this composite is enough to pair them.
type dividendKey struct {
	Date     string
	Symbol   string
	Currency string
}

func parseActivityCSV(content []byte) (*ParseResult, error) {
	r := csv.NewReader(strings.NewReader(string(content)))
	r.FieldsPerRecord = -1 // IBKR rows have inconsistent widths
	r.TrimLeadingSpace = true

	baseCurrency := "USD"
	out := make([]importevent.Event, 0, 64)
	dividends := make(map[dividendKey]*importevent.Event)

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// IBKR files routinely contain malformed rows; skip and continue.
			if errors.Is(err, csv.ErrFieldCount) {
				continue
			}
			return nil, fmt.Errorf("ibkr csv read: %w", err)
		}
		if len(row) < 4 {
			continue
		}
		section := row[0]
		rowType := row[1]

		// "Account Information,Data,Base Currency,CHF"
		if section == "Account Information" && rowType == "Data" &&
			len(row) >= 4 && row[2] == "Base Currency" {
			if c := strings.TrimSpace(row[3]); c != "" {
				baseCurrency = strings.ToUpper(c)
			}
			continue
		}

		// "Trades,Data,Order,Stocks,USD,GOOGL,"2025-12-03, 10:53:11",1,319.29,...
		if section == "Trades" && rowType == "Data" && len(row) >= 12 &&
			row[2] == "Order" && (row[3] == "Stocks") {
			currency := strings.ToUpper(strings.TrimSpace(row[4]))
			if currency == "" {
				currency = baseCurrency
			}
			symbol := strings.ToUpper(strings.TrimSpace(row[5]))
			dateRaw := row[6]
			quantityRaw := parseFloat(row[7])
			tradePrice := parseFloat(row[8])
			proceeds := parseFloat(row[10])
			commFee := parseFloat(row[11])

			if symbol == "" || dateRaw == "" || quantityRaw == 0 || proceeds == 0 {
				continue
			}
			side := "buy"
			if proceeds > 0 {
				side = "sell"
			}
			d, err := parseIBKRDateTime(dateRaw)
			if err != nil {
				continue
			}
			out = append(out, importevent.Event{
				Kind:      importevent.Trade,
				TradeSide: side,
				Symbol:    symbol,
				Date:      d,
				Quantity:  decimal.NewFromFloat(absFloat(quantityRaw)),
				Price:     decimal.NewFromFloat(absFloat(tradePrice)),
				Fee:       decimal.NewFromFloat(absFloat(commFee)),
				Currency:  currency,
			})
			continue
		}

		// "Dividends,Data,USD,2025-12-15,GOOGL(US02079K3059) Cash Dividend ...,0.21"
		if section == "Dividends" && rowType == "Data" && len(row) >= 6 {
			currency, dateRaw, desc, amount := strings.ToUpper(strings.TrimSpace(row[2])), row[3], row[4], parseFloat(row[5])
			if dateRaw == "" || strings.HasPrefix(strings.ToLower(desc), "total") {
				continue
			}
			symbol, isin := dividendSymbolFromDesc(desc)
			if symbol == "" {
				continue
			}
			d, err := time.Parse("2006-01-02", strings.TrimSpace(dateRaw))
			if err != nil {
				continue
			}
			key := dividendKey{Date: dateRaw, Symbol: symbol, Currency: currency}
			ev := dividends[key]
			if ev == nil {
				ev = &importevent.Event{
					Kind:     importevent.Dividend,
					Symbol:   symbol,
					ISIN:     isin,
					Date:     d,
					Currency: currency,
				}
				dividends[key] = ev
			}
			ev.AmountTotal = ev.AmountTotal.Add(decimal.NewFromFloat(absFloat(amount)))
			ev.AmountPerUnit = perShareFromDesc(desc)
			continue
		}

		// "Withholding Tax,Data,USD,2025-12-15,GOOGL(...) - US Tax,-0.03,"
		if section == "Withholding Tax" && rowType == "Data" && len(row) >= 6 {
			currency, dateRaw, desc, amount := strings.ToUpper(strings.TrimSpace(row[2])), row[3], row[4], parseFloat(row[5])
			if dateRaw == "" || strings.HasPrefix(strings.ToLower(desc), "total") {
				continue
			}
			symbol, _ := dividendSymbolFromDesc(desc)
			if symbol == "" {
				continue
			}
			key := dividendKey{Date: dateRaw, Symbol: symbol, Currency: currency}
			ev := dividends[key]
			if ev == nil {
				// Tax row arrived before the dividend; create a stub so the
				// pairing still happens when the dividend row lands later.
				d, err := time.Parse("2006-01-02", strings.TrimSpace(dateRaw))
				if err != nil {
					continue
				}
				ev = &importevent.Event{
					Kind:     importevent.Dividend,
					Symbol:   symbol,
					Date:     d,
					Currency: currency,
				}
				dividends[key] = ev
			}
			ev.TaxWithheld = ev.TaxWithheld.Add(decimal.NewFromFloat(absFloat(amount)))
			continue
		}
	}

	for _, ev := range dividends {
		// Cap tax_withheld at total_amount; the schema check enforces it.
		if ev.TaxWithheld.GreaterThan(ev.AmountTotal) {
			ev.TaxWithheld = ev.AmountTotal
		}
		out = append(out, *ev)
	}

	return &ParseResult{BaseCurrency: baseCurrency, Format: FormatActivityCSV, Events: out}, nil
}

// dividendSymbolFromDesc extracts (symbol, isin) from an IBKR dividend or
// withholding-tax description. ISIN may be empty on legacy rows.
func dividendSymbolFromDesc(desc string) (string, string) {
	m := symbolFromDividendDesc.FindStringSubmatch(desc)
	if len(m) < 2 {
		return "", ""
	}
	isin := ""
	if len(m) >= 3 {
		isin = m[2]
	}
	return strings.ToUpper(m[1]), isin
}

// perShareFromDesc extracts the "USD 0.21 per Share" amount from a dividend
// description, returning Zero when not present (some Payment-in-Lieu rows
// don't carry it).
func perShareFromDesc(desc string) decimal.Decimal {
	m := perShareFromDividendDesc.FindStringSubmatch(desc)
	if len(m) < 3 {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(m[2])
	if err != nil {
		return decimal.Zero
	}
	return d
}

func parseIBKRDateTime(raw string) (time.Time, error) {
	// "YYYY-MM-DD, HH:mm:ss" or just "YYYY-MM-DD"
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse("2006-01-02, 15:04:05", raw); err == nil {
		return t.UTC(), nil
	}
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return time.Parse("2006-01-02", raw)
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	if err != nil {
		return 0
	}
	return v
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
