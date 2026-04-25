package bankimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
)

const maxImportBytes = 5 << 20

func parseUpload(fileName string, r io.Reader) (ParsedFile, string, string, error) {
	var buf bytes.Buffer
	limited := io.LimitReader(r, maxImportBytes+1)
	n, err := buf.ReadFrom(limited)
	if err != nil {
		return ParsedFile{}, "", "", fmt.Errorf("read upload: %w", err)
	}
	if n > maxImportBytes {
		return ParsedFile{}, "", "", httpx.NewValidationError("import file is too large")
	}
	content := strings.TrimPrefix(buf.String(), "\uFEFF")
	hashBytes := sha256.Sum256(buf.Bytes())
	hash := hex.EncodeToString(hashBytes[:])

	parsed, err := Parse(content)
	if err != nil {
		return ParsedFile{}, "", "", err
	}
	payload := previewPayload{
		FileName: fileName,
		FileHash: hash,
		Content:  base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
	tokenBytes, err := json.Marshal(payload)
	if err != nil {
		return ParsedFile{}, "", "", err
	}
	return parsed, hash, base64.RawURLEncoding.EncodeToString(tokenBytes), nil
}

func parseToken(token string) (previewPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return previewPayload{}, httpx.NewValidationError("fileToken is invalid")
	}
	var p previewPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return previewPayload{}, httpx.NewValidationError("fileToken is invalid")
	}
	return p, nil
}

// Parse detects and parses a supported bank export.
func Parse(content string) (ParsedFile, error) {
	normalized := strings.TrimPrefix(content, "\uFEFF")
	firstLine := firstNonEmptyLine(normalized)
	switch {
	case strings.HasPrefix(firstLine, "Tipo,Produto,Data de"):
		return parseRevolutBanking(normalized)
	case strings.Contains(normalized, "Date;Type of transaction;Notification text;"):
		return parsePostFinance(normalized)
	default:
		return ParsedFile{}, httpx.NewValidationError("unsupported bank export format")
	}
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func parseRevolutBanking(content string) (ParsedFile, error) {
	records, err := csv.NewReader(strings.NewReader(content)).ReadAll()
	if err != nil {
		return ParsedFile{}, httpx.NewValidationError("could not parse Revolut CSV")
	}
	rows, err := recordsToMaps(records)
	if err != nil {
		return ParsedFile{}, err
	}
	out := ParsedFile{
		Profile:     "revolut_banking_csv",
		Institution: "Revolut",
	}
	var skipped int
	for _, row := range rows {
		product := strings.TrimSpace(row["Produto"])
		if product == "Investimentos" || product == "Depósito" {
			skipped++
			continue
		}
		status := strings.TrimSpace(row["Estado"])
		if status != "" && status != "CONCLUÍDA" && status != "REVERTIDA" {
			skipped++
			continue
		}
		amount, err := decimal.NewFromString(strings.TrimSpace(row["Montante"]))
		if err != nil {
			return ParsedFile{}, httpx.NewValidationError("Revolut row has invalid Montante")
		}
		currency, err := money.ParseCurrency(row["Moeda"])
		if err != nil {
			return ParsedFile{}, httpx.NewValidationError("Revolut row has invalid Moeda")
		}
		bookedAt, postedAt, err := parseRevolutTime(row["Data de início"])
		if err != nil {
			return ParsedFile{}, err
		}
		var completed *time.Time
		if strings.TrimSpace(row["Data de Conclusão"]) != "" {
			_, t, err := parseRevolutTime(row["Data de Conclusão"])
			if err != nil {
				return ParsedFile{}, err
			}
			completed = &t
		}
		desc := cleanString(row["Descrição"])
		out.Transactions = append(out.Transactions, ParsedTransaction{
			BookedAt:        bookedAt,
			ValueAt:         nil,
			PostedAt:        completedOr(postedAt, completed),
			Amount:          amount,
			Currency:        currency.String(),
			CounterpartyRaw: desc,
			Description:     desc,
			ExternalID:      "",
			Raw:             row,
		})
	}
	if skipped > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Skipped %d unsupported or pending Revolut rows.", skipped))
	}
	finalizeParsed(&out)
	return out, nil
}

func parseRevolutTime(raw string) (time.Time, time.Time, error) {
	s := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano} {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err == nil {
			return datePart(t), t, nil
		}
	}
	return time.Time{}, time.Time{}, httpx.NewValidationError("Revolut row has invalid date")
}

func parsePostFinance(content string) (ParsedFile, error) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	meta := parsePostFinanceMeta(normalized)
	lines := strings.Split(normalized, "\n")
	header := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "Date;Type of transaction;") {
			header = i
			break
		}
	}
	if header < 0 {
		return ParsedFile{}, httpx.NewValidationError("could not find PostFinance transaction header")
	}
	reader := csv.NewReader(strings.NewReader(strings.Join(lines[header:], "\n")))
	reader.Comma = ';'
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return ParsedFile{}, httpx.NewValidationError("could not parse PostFinance CSV")
	}
	rows, err := recordsToMaps(records)
	if err != nil {
		return ParsedFile{}, err
	}
	out := ParsedFile{
		Profile:     "postfinance_csv",
		Institution: "PostFinance",
		AccountHint: meta.account,
		Currency:    meta.currency,
	}
	for _, row := range rows {
		if strings.TrimSpace(row["Date"]) == "" {
			continue
		}
		bookedAt, err := parseSwissDate(row["Date"])
		if err != nil {
			continue
		}
		credit, err := parseOptionalDecimal(row["Credit in CHF"])
		if err != nil {
			return ParsedFile{}, httpx.NewValidationError("PostFinance row has invalid credit amount")
		}
		debit, err := parseOptionalDecimal(row["Debit in CHF"])
		if err != nil {
			return ParsedFile{}, httpx.NewValidationError("PostFinance row has invalid debit amount")
		}
		amount := credit.Add(debit)
		desc := cleanString(firstNonBlank(row["Notification text"], row["Type of transaction"]))
		out.Transactions = append(out.Transactions, ParsedTransaction{
			BookedAt:        bookedAt,
			ValueAt:         &bookedAt,
			Amount:          amount,
			Currency:        meta.currency,
			CounterpartyRaw: desc,
			Description:     desc,
			Raw:             row,
		})
	}
	finalizeParsed(&out)
	return out, nil
}

type postFinanceMeta struct {
	account  string
	currency string
}

func parsePostFinanceMeta(content string) postFinanceMeta {
	meta := postFinanceMeta{currency: "CHF"}
	for _, line := range strings.Split(content, "\n")[:min(8, len(strings.Split(content, "\n")))] {
		if m := regexp.MustCompile(`Account:;="?([^";]+)"?`).FindStringSubmatch(line); len(m) == 2 {
			meta.account = strings.TrimSpace(m[1])
		}
		if m := regexp.MustCompile(`Currency:;="?([^";]+)"?`).FindStringSubmatch(line); len(m) == 2 {
			if cur, err := money.ParseCurrency(m[1]); err == nil {
				meta.currency = cur.String()
			}
		}
	}
	return meta
}

func recordsToMaps(records [][]string) ([]map[string]string, error) {
	if len(records) < 1 {
		return nil, httpx.NewValidationError("CSV has no header")
	}
	header := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(header))
		for i, h := range header {
			if strings.TrimSpace(h) == "" {
				continue
			}
			if i < len(record) {
				row[strings.TrimSpace(h)] = strings.TrimSpace(record[i])
			} else {
				row[strings.TrimSpace(h)] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseSwissDate(raw string) (time.Time, error) {
	t, err := time.ParseInLocation("02.01.2006", strings.TrimSpace(raw), time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	return datePart(t), nil
}

func parseOptionalDecimal(raw string) (decimal.Decimal, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(strings.ReplaceAll(s, "'", ""))
}

func finalizeParsed(p *ParsedFile) {
	if len(p.Transactions) == 0 {
		return
	}
	sort.SliceStable(p.Transactions, func(i, j int) bool {
		return p.Transactions[i].BookedAt.Before(p.Transactions[j].BookedAt)
	})
	counts := map[string]int{}
	for i := range p.Transactions {
		tx := &p.Transactions[i]
		if p.Currency == "" {
			p.Currency = tx.Currency
		} else if p.Currency != tx.Currency {
			p.Warnings = appendOnce(p.Warnings, "File contains multiple currencies. Import into an account with the matching currency.")
		}
		if p.DateFrom == nil || tx.BookedAt.Before(*p.DateFrom) {
			d := tx.BookedAt
			p.DateFrom = &d
		}
		if p.DateTo == nil || tx.BookedAt.After(*p.DateTo) {
			d := tx.BookedAt
			p.DateTo = &d
		}
		base := fingerprintSource(p.Profile, *tx)
		counts[base]++
		if tx.ExternalID == "" {
			tx.ExternalID = base
			if counts[base] > 1 {
				tx.ExternalID = fmt.Sprintf("%s#%d", base, counts[base])
			}
		}
	}
}

func fingerprintSource(profile string, tx ParsedTransaction) string {
	return strings.Join([]string{
		profile,
		tx.BookedAt.Format(dateOnly),
		tx.Amount.String(),
		tx.Currency,
		normalizeText(valueOf(tx.Description)),
	}, "|")
}

func datePart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func completedOr(fallback time.Time, completed *time.Time) *time.Time {
	if completed != nil {
		return completed
	}
	return &fallback
}

func cleanString(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func valueOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func appendOnce(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
