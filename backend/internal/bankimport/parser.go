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
	case isRevolutConsolidatedV2(firstLine, normalized):
		return parseRevolutConsolidatedV2(normalized)
	case strings.Contains(normalized, "Date;Type of transaction;Notification text;"):
		return parsePostFinance(normalized)
	default:
		return ParsedFile{}, httpx.NewValidationError("unsupported bank export format")
	}
}

func isRevolutConsolidatedV2(firstLine, content string) bool {
	stripped := strings.Trim(firstLine, `"`)
	if stripped == "Current Accounts Summaries" || stripped == "Contas-correntes Resumos" {
		return true
	}
	return strings.Contains(content, "Contas-correntes Extratos de operações") ||
		strings.Contains(content, "Current Accounts Transaction statements")
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
	var skipped, pocketSkipped int
	for _, row := range rows {
		product := strings.TrimSpace(row["Produto"])
		if product == "Investimentos" || product == "Depósito" {
			skipped++
			continue
		}
		// Poupanças = Revolut sub-accounts/pockets. They live in their own
		// accounts in Folio. Mixing them into the current account inflates
		// the main balance, so skip them and tell the user how to import
		// pockets properly.
		if product == "Poupanças" {
			pocketSkipped++
			continue
		}
		status := strings.TrimSpace(row["Estado"])
		// Only CONCLUÍDA rows actually settled. REVERTIDA = reversed/declined,
		// the money never moved (Saldo column is empty for these).
		if status != "" && status != "CONCLUÍDA" {
			skipped++
			continue
		}
		amount, err := decimal.NewFromString(strings.TrimSpace(row["Montante"]))
		if err != nil {
			return ParsedFile{}, httpx.NewValidationError("Revolut row has invalid Montante")
		}
		fee := decimal.Zero
		if rawFee := strings.TrimSpace(row["Comissão"]); rawFee != "" {
			fee, err = decimal.NewFromString(rawFee)
			if err != nil {
				return ParsedFile{}, httpx.NewValidationError("Revolut row has invalid Comissão")
			}
		}
		// Revolut's Saldo column is Montante - Comissão. The cash impact on
		// the wallet is the net amount, so subtract the fee here. Without
		// this, balances drift by the cumulative fee total.
		amount = amount.Sub(fee)
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
	if pocketSkipped > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Skipped %d pocket (Poupanças) rows. Export the consolidated statement to import pockets as separate accounts.", pocketSkipped))
	}
	finalizeParsed(&out)
	return out, nil
}

// parseRevolutConsolidatedV2 parses Revolut's "consolidated statement v2"
// export. The file groups transactions per account (main currencies +
// pockets + flexible cash funds + investments + crypto + commodities).
//
// Current scope: every "Contas-correntes" / "Current Accounts" section is
// emitted as a separate account in Folio. That covers the multi-currency
// main account (Conta Pessoal / Personal Account) AND every pocket / sub-
// account. Pockets become their own top-level Folio accounts (Option A in
// the design notes); aggregation across pockets is left to UI-level views.
//
// Other product types (Flexible Cash Funds / Investment Services / Crypto /
// Commodities) are still skipped here — they map onto different Folio
// account kinds and are tracked separately in Phase 2.
func parseRevolutConsolidatedV2(content string) (ParsedFile, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return ParsedFile{}, httpx.NewValidationError("could not parse Revolut consolidated CSV")
	}
	out := ParsedFile{
		Profile:     "revolut_consolidated_v2",
		Institution: "Revolut",
	}
	lang := detectConsolidatedLang(records)
	txAnchor := consolidatedTxAnchor(lang)
	startIdx := -1
	for i, row := range records {
		if len(row) == 0 {
			continue
		}
		if strings.TrimSpace(row[0]) == txAnchor {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return ParsedFile{}, httpx.NewValidationError("Revolut consolidated CSV is missing the transactions section")
	}
	// Walk sections within the current-accounts transactions block, stopping
	// at the next top-level category (Flexible Cash Funds, Investments, etc.).
	endIdx := len(records)
	for i := startIdx + 1; i < len(records); i++ {
		first := strings.TrimSpace(firstField(records[i]))
		if first == "" {
			continue
		}
		if isConsolidatedTopLevelHeader(first, lang) && first != txAnchor {
			endIdx = i
			break
		}
	}

	sections := splitConsolidatedSections(records[startIdx+1 : endIdx])
	disambiguateSectionHints(sections)
	skippedRows := 0
	emptyPockets := 0
	for _, sec := range sections {
		if len(sec.dataRows) == 0 {
			emptyPockets++
			continue
		}
		for _, row := range sec.dataRows {
			tx, ok, rowErr := parseConsolidatedTxRow(row, sec.accountHint, sec.currency, sec.columns, lang)
			if rowErr != nil {
				return ParsedFile{}, rowErr
			}
			if !ok {
				skippedRows++
				continue
			}
			out.Transactions = append(out.Transactions, tx)
		}
	}
	if skippedRows > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Skipped %d unparseable Revolut consolidated rows.", skippedRows))
	}
	if emptyPockets > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Skipped %d empty / closed pocket sections.", emptyPockets))
	}
	finalizeParsed(&out)
	return out, nil
}

// disambiguateSectionHints assigns a unique AccountHint to each section.
// When a (name, currency) pair appears more than once (e.g. Vectr (EUR)
// closed and reopened), later occurrences get a "#N" suffix so they map
// to distinct Folio accounts.
func disambiguateSectionHints(sections []consolidatedSection) {
	type key struct{ name, currency string }
	seen := map[key]int{}
	for i := range sections {
		k := key{sections[i].name, sections[i].currency}
		seen[k]++
	}
	idx := map[key]int{}
	for i := range sections {
		k := key{sections[i].name, sections[i].currency}
		if seen[k] > 1 {
			idx[k]++
			sections[i].accountHint = fmt.Sprintf("%s #%d", sections[i].name, idx[k])
		} else {
			sections[i].accountHint = sections[i].name
		}
	}
}

type consolidatedSection struct {
	name        string
	currency    string
	accountHint string
	columns     map[string]int
	dataRows    [][]string
}

var consolidatedSectionHeader = regexp.MustCompile(`^(.+) \(([A-Z]{3})\)$`)

func splitConsolidatedSections(rows [][]string) []consolidatedSection {
	var sections []consolidatedSection
	var cur *consolidatedSection
	for _, row := range rows {
		first := strings.TrimSpace(firstField(row))
		if first == "" {
			continue
		}
		if first == "---------" {
			if cur != nil {
				sections = append(sections, *cur)
				cur = nil
			}
			continue
		}
		if m := consolidatedSectionHeader.FindStringSubmatch(first); m != nil && allBlank(row[1:]) {
			if cur != nil {
				sections = append(sections, *cur)
			}
			cur = &consolidatedSection{name: m[1], currency: m[2]}
			continue
		}
		if cur == nil {
			continue
		}
		// Skip the inner sub-headers ("Extrato de operações" / "Transaction statement"
		// / "Total" rows). The column header row populates cur.columns.
		if isConsolidatedTxSubheader(first) {
			continue
		}
		if cur.columns == nil {
			if cols := buildConsolidatedColumns(row); cols != nil {
				cur.columns = cols
				continue
			}
		}
		if first == "Total" {
			continue
		}
		cur.dataRows = append(cur.dataRows, row)
	}
	if cur != nil {
		sections = append(sections, *cur)
	}
	return sections
}

func isConsolidatedTxSubheader(s string) bool {
	switch s {
	case "Extrato de operações", "Transaction statement":
		return true
	}
	return false
}

// buildConsolidatedColumns recognises the column header row of a current-account
// transaction table and returns a name→index map. The known schemas are:
//
//	[Data, Descrição, Categoria, Dinheiro a entrar/sair, Saldo, Imposto retido, Outros impostos, Comissões]
//	[Date, Description, Category, Money in/out, Balance, Tax withheld, Other taxes, Fees]
func buildConsolidatedColumns(row []string) map[string]int {
	if len(row) < 4 {
		return nil
	}
	cols := map[string]int{}
	for i, c := range row {
		switch strings.TrimSpace(c) {
		case "Data", "Date":
			cols["date"] = i
		case "Descrição", "Description":
			cols["description"] = i
		case "Categoria", "Category":
			cols["category"] = i
		case "Dinheiro a entrar/sair", "Money in/out":
			cols["amount"] = i
		case "Saldo", "Balance":
			cols["balance"] = i
		case "Comissões", "Fees":
			cols["fees"] = i
		}
	}
	if _, ok := cols["date"]; !ok {
		return nil
	}
	if _, ok := cols["amount"]; !ok {
		return nil
	}
	return cols
}

func parseConsolidatedTxRow(row []string, sectionName, sectionCurrency string, cols map[string]int, lang consolidatedLang) (ParsedTransaction, bool, error) {
	if cols == nil {
		return ParsedTransaction{}, false, nil
	}
	dateRaw := safeField(row, cols["date"])
	if strings.TrimSpace(dateRaw) == "" {
		return ParsedTransaction{}, false, nil
	}
	bookedAt, err := parseConsolidatedDate(dateRaw, lang)
	if err != nil {
		// Soft-skip undecodable date rows (e.g. stray subtotal rows).
		return ParsedTransaction{}, false, nil
	}
	amountRaw := safeField(row, cols["amount"])
	amount, _, err := parseConsolidatedMoney(amountRaw, lang)
	if err != nil {
		return ParsedTransaction{}, false, fmt.Errorf("parse consolidated amount %q: %w", amountRaw, err)
	}
	desc := cleanString(safeField(row, cols["description"]))
	raw := map[string]string{
		"section":  sectionName,
		"currency": sectionCurrency,
	}
	for name, idx := range cols {
		raw[name] = safeField(row, idx)
	}
	return ParsedTransaction{
		BookedAt:        bookedAt,
		ValueAt:         nil,
		PostedAt:        nil,
		Amount:          amount,
		Currency:        sectionCurrency,
		CounterpartyRaw: desc,
		Description:     desc,
		AccountHint:     sectionName,
		Raw:             raw,
	}, true, nil
}

type consolidatedLang int

const (
	consolidatedLangPT consolidatedLang = iota
	consolidatedLangEN
)

func detectConsolidatedLang(records [][]string) consolidatedLang {
	for _, row := range records {
		first := strings.TrimSpace(firstField(row))
		if first == "Extrato de operações" || first == "Contas-correntes Resumos" {
			return consolidatedLangPT
		}
		if first == "Transaction statement" || first == "Current Accounts Summaries" {
			return consolidatedLangEN
		}
	}
	return consolidatedLangPT
}

func consolidatedTxAnchor(lang consolidatedLang) string {
	if lang == consolidatedLangEN {
		return "Current Accounts Transaction statements"
	}
	return "Contas-correntes Extratos de operações"
}

func isConsolidatedTopLevelHeader(s string, lang consolidatedLang) bool {
	switch s {
	case "Fundos Monetários Flexíveis Extratos de operações",
		"Investment Services Extratos de operações",
		"Cripto Extratos de operações",
		"Bem Extratos de operações",
		"Glossário":
		return true
	case "Flexible Cash Funds Transaction statements",
		"Investment Services Transaction statements",
		"Crypto Transaction statements",
		"Commodities Transaction statements",
		"Glossary":
		return true
	}
	return false
}

// parseConsolidatedDate accepts the export's localized date forms.
func parseConsolidatedDate(raw string, lang consolidatedLang) (time.Time, error) {
	s := strings.TrimSpace(raw)
	layouts := []string{"02/01/2006", "Jan 2, 2006"}
	if lang == consolidatedLangEN {
		layouts = []string{"Jan 2, 2006", "02/01/2006"}
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return datePart(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

// parseConsolidatedMoney accepts the export's amount forms:
//
//	"-378,56 CHF (-413,02€)"   pt-pt non-EUR section, comma decimal
//	"16,77€"                    pt-pt EUR section
//	"20,00$ (16,97€)"           pt-pt USD section
//	"-32.52 CHF"                en section, period decimal
//	"27.38 CHF"                 en section
//
// Returns the signed amount and the trimmed currency suffix (which the caller
// generally ignores in favour of the section currency).
var consolidatedMoneyHead = regexp.MustCompile(`^\s*(-?[\d.,]+)\s*([A-Za-z€$£¥]*)`)

func parseConsolidatedMoney(raw string, lang consolidatedLang) (decimal.Decimal, string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return decimal.Zero, "", nil
	}
	m := consolidatedMoneyHead.FindStringSubmatch(s)
	if m == nil {
		return decimal.Zero, "", fmt.Errorf("invalid money %q", raw)
	}
	d, err := parseLocalizedDecimal(m[1], lang)
	if err != nil {
		return decimal.Zero, "", err
	}
	return d, strings.TrimSpace(m[2]), nil
}

// parseLocalizedDecimal handles "1.234,56" (pt) and "1,234.56" (en). When only
// one separator is present, treat it as the decimal point in the file's locale.
func parseLocalizedDecimal(s string, lang consolidatedLang) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	hasDot := strings.Contains(s, ".")
	hasComma := strings.Contains(s, ",")
	switch {
	case hasDot && hasComma:
		// Whichever is rightmost is the decimal mark.
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasComma:
		// Single comma — treat as decimal in pt; thousands in en.
		if lang == consolidatedLangPT {
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	}
	return decimal.NewFromString(s)
}

func firstField(row []string) string {
	if len(row) == 0 {
		return ""
	}
	return row[0]
}

func safeField(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

func allBlank(values []string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
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
	finalizeParsedRange(p, true)
}

func finalizeParsedRange(p *ParsedFile, assignExternalIDs bool) {
	if len(p.Transactions) == 0 {
		return
	}
	p.Currency = ""
	p.DateFrom = nil
	p.DateTo = nil
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
		if !assignExternalIDs {
			continue
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

// parsedForCurrency selects every transaction matching the given currency,
// regardless of which section/pocket it came from. Used by the legacy single-
// account import path (preview/apply against one accountId).
func parsedForCurrency(p ParsedFile, currency string) ParsedFile {
	return parsedForGroup(p, currency, "")
}

// parsedForGroup narrows a parsed file to a single import group: same
// currency AND (when sourceKey is non-empty) same per-tx AccountHint. Empty
// sourceKey preserves the "all txs of this currency" behaviour for banking-
// style exports that only emit one logical account per currency.
func parsedForGroup(p ParsedFile, currency, sourceKey string) ParsedFile {
	out := ParsedFile{
		Profile:     p.Profile,
		Institution: p.Institution,
		AccountHint: p.AccountHint,
		Currency:    currency,
		Warnings:    append([]string(nil), p.Warnings...),
	}
	for _, tx := range p.Transactions {
		if tx.Currency != currency {
			continue
		}
		if sourceKey != "" && tx.AccountHint != sourceKey {
			continue
		}
		out.Transactions = append(out.Transactions, tx)
	}
	finalizeParsedRange(&out, false)
	return out
}

func fingerprintSource(profile string, tx ParsedTransaction) string {
	// Use the institution as the dedup namespace, not the profile, so the
	// same logical transaction parsed from two different export formats
	// (e.g. revolut_banking_csv vs revolut_consolidated_v2) produces the
	// same external_id and `duplicateBySource` catches re-uploads across
	// formats.
	return strings.Join([]string{
		fingerprintNamespace(profile),
		tx.BookedAt.Format(dateOnly),
		tx.Amount.String(),
		tx.Currency,
		normalizeText(valueOf(tx.Description)),
	}, "|")
}

func fingerprintNamespace(profile string) string {
	switch {
	case strings.HasPrefix(profile, "revolut_"):
		return "revolut"
	default:
		return profile
	}
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
