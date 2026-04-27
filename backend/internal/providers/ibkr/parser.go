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
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/investments/importevent"
)

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

func parseActivityCSV(content []byte) (*ParseResult, error) {
	r := csv.NewReader(strings.NewReader(string(content)))
	r.FieldsPerRecord = -1 // IBKR rows have inconsistent widths
	r.TrimLeadingSpace = true

	baseCurrency := "USD"
	out := make([]importevent.Event, 0, 64)

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
		}
	}
	return &ParseResult{BaseCurrency: baseCurrency, Format: FormatActivityCSV, Events: out}, nil
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
