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
		// Surface the first line back to the user so debugging an
		// unrecognised export doesn't require server logs.
		preview := firstLine
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		return ParsedFile{}, httpx.NewValidationError(fmt.Sprintf("unsupported bank export format (first line: %q)", preview))
	}
}

func isRevolutConsolidatedV2(firstLine, content string) bool {
	if headerEqAny(firstCellOfLine(firstLine), "Current Accounts Summaries", "Contas-correntes Resumos") {
		return true
	}
	lc := strings.ToLower(content)
	return strings.Contains(lc, strings.ToLower("Contas-correntes Extratos de operações")) ||
		strings.Contains(lc, strings.ToLower("Current Accounts Transaction Statements"))
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// firstCellOfLine returns the first CSV cell of a single line, with quotes
// and surrounding whitespace stripped. Revolut header rows pad with empty
// trailing columns, so the raw line looks like `"Header",,,,,,` and naive
// quote-trimming leaves the trailing commas behind. CSV-parsing the line
// gives the actual cell value.
func firstCellOfLine(line string) string {
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	rec, err := r.Read()
	if err != nil || len(rec) == 0 {
		return strings.TrimSpace(strings.Trim(line, `"`))
	}
	return strings.TrimSpace(rec[0])
}

// headerEq compares Revolut section labels case-insensitively after
// trimming whitespace. Casing has drifted between language exports
// ("Statements" vs "statements"); this absorbs that drift without forcing
// us to track every variant by hand.
func headerEq(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func headerEqAny(s string, options ...string) bool {
	t := strings.TrimSpace(s)
	for _, o := range options {
		if strings.EqualFold(t, o) {
			return true
		}
	}
	return false
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
		if headerEq(row[0], txAnchor) {
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
		if isConsolidatedTopLevelHeader(first, lang) && !headerEq(first, txAnchor) {
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

	// Crypto section: a single top-level block with six sub-tables (Sells /
	// Acquisitions / Deposits / Withdrawals / Learn & Earn / Staking).
	// Each emits one ParsedTransaction per row in its asset's native
	// currency (BTC, SOL, …), tagged with KindHint=crypto_wallet so the
	// importer creates per-asset wallet accounts.
	cryptoTxs, cryptoSkipped, err := parseConsolidatedCryptoSection(records, lang)
	if err != nil {
		return ParsedFile{}, err
	}
	out.Transactions = append(out.Transactions, cryptoTxs...)
	skippedRows += cryptoSkipped

	// Flexible Cash Funds (Money Market Funds) section: one sub-table per
	// currency (EUR / GBP / USD), each containing only daily interest
	// payments. Buy/sell of fund shares is NOT in this file — those live
	// in the standalone savings-statement.csv. Importing the consolidated
	// alone gives accurate interest-income history but the fund balance
	// will only reflect cumulative interest, not actual share value.
	mmfTxs, mmfSkipped, err := parseConsolidatedMMFSection(records, lang)
	if err != nil {
		return ParsedFile{}, err
	}
	out.Transactions = append(out.Transactions, mmfTxs...)
	skippedRows += mmfSkipped
	if len(mmfTxs) > 0 {
		out.Warnings = append(out.Warnings, "Imported Flexible Cash Funds interest only. Fund share buy/sell history is not in the consolidated export — for full position tracking, also import the dedicated savings statement.")
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

// cryptoOpKind identifies which sub-table of the Cripto section a row came
// from. Drives the description, sign of the amount, and downstream income
// categorisation when the trade pipeline lands.
type cryptoOpKind string

const (
	cryptoOpBuy        cryptoOpKind = "buy"
	cryptoOpSell       cryptoOpKind = "sell"
	cryptoOpDeposit    cryptoOpKind = "deposit"
	cryptoOpWithdrawal cryptoOpKind = "withdrawal"
	cryptoOpLearnEarn  cryptoOpKind = "learn_and_earn"
	cryptoOpStaking    cryptoOpKind = "staking"
)

// parseConsolidatedCryptoSection finds the Cripto / Crypto block in the
// records and walks each of its six sub-tables.
func parseConsolidatedCryptoSection(records [][]string, lang consolidatedLang) ([]ParsedTransaction, int, error) {
	startIdx := -1
	header := "Cripto Extratos de operações"
	if lang == consolidatedLangEN {
		header = "Crypto Transaction statements"
	}
	for i, row := range records {
		if headerEq(firstField(row), header) {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return nil, 0, nil
	}
	// Stop at next top-level category or end-of-file.
	endIdx := len(records)
	for i := startIdx + 1; i < len(records); i++ {
		first := strings.TrimSpace(firstField(records[i]))
		if first == "" {
			continue
		}
		if isConsolidatedTopLevelHeader(first, lang) && !headerEq(first, header) {
			endIdx = i
			break
		}
	}

	var out []ParsedTransaction
	skipped := 0
	var currentOp cryptoOpKind
	var columns []string
	expectColumns := false
	for i := startIdx + 1; i < endIdx; i++ {
		row := records[i]
		first := strings.TrimSpace(firstField(row))
		if first == "" {
			continue
		}
		if first == "Total" || strings.HasPrefix(first, "---") {
			continue
		}
		if op, ok := matchCryptoSubsectionHeader(first); ok {
			currentOp = op
			columns = nil
			expectColumns = true
			continue
		}
		if expectColumns {
			columns = make([]string, len(row))
			for j, c := range row {
				columns[j] = strings.TrimSpace(c)
			}
			expectColumns = false
			continue
		}
		if currentOp == "" || columns == nil {
			continue
		}
		tx, ok, parseErr := parseCryptoRow(row, columns, currentOp, lang)
		if parseErr != nil {
			return nil, 0, parseErr
		}
		if !ok {
			skipped++
			continue
		}
		out = append(out, tx)
	}
	return out, skipped, nil
}

func matchCryptoSubsectionHeader(s string) (cryptoOpKind, bool) {
	// Both "through" (older EN export) and "via" (current EN export) are
	// accepted for Learn & Earn / Staking, since Revolut quietly switched
	// the wording. Case-insensitive equality covers Statement(s) drift.
	switch {
	case headerEqAny(s,
		"Extrato de operação (apenas vendas)",
		"Transaction statement (only sales)"):
		return cryptoOpSell, true
	case headerEqAny(s,
		"Extrato de operação (apenas aquisições)",
		"Transaction statement (only acquisitions)"):
		return cryptoOpBuy, true
	case headerEqAny(s,
		"Extrato de operação (apenas depósitos)",
		"Transaction statement (only deposits)"):
		return cryptoOpDeposit, true
	case headerEqAny(s,
		"Extrato de operação (apenas levantamentos)",
		"Transaction statement (only withdrawals)"):
		return cryptoOpWithdrawal, true
	case headerEqAny(s,
		"Extrato de operação (apenas aquisições através de Learn & Earn)",
		"Transaction statement (only acquisitions through Learn & Earn)",
		"Transaction statement (only acquisitions via Learn & Earn)"):
		return cryptoOpLearnEarn, true
	case headerEqAny(s,
		"Extrato de operação (apenas aquisições por Staking)",
		"Transaction statement (only acquisitions through Staking)",
		"Transaction statement (only acquisitions via Staking)"):
		return cryptoOpStaking, true
	}
	return "", false
}

// parseCryptoRow turns a single Cripto sub-table row into a ParsedTransaction.
// The crypto section's date format is dd.mm.yy (different from the dd/mm/yyyy
// used in current accounts). Sells pack two dates (`sale, buy`); we take the
// sale date.
//
// The Raw map carries trade-level details (price per unit, EUR value, fees,
// realised gain / age for sells, op kind) so the trade-pipeline pass can
// populate `instruments` / `investment_trades` / `investment_lots` without
// re-parsing the file.
func parseCryptoRow(row, columns []string, op cryptoOpKind, lang consolidatedLang) (ParsedTransaction, bool, error) {
	col := func(name string) string {
		for i, c := range columns {
			if c == name && i < len(row) {
				return strings.TrimSpace(row[i])
			}
		}
		return ""
	}
	// Date column varies; gather any column whose label starts with "Data " /
	// "Date ". Sell rows pack "sale, buy" — sale date is the first.
	var dateRaw string
	for i, c := range columns {
		if (strings.HasPrefix(c, "Data") || strings.HasPrefix(c, "Date")) && i < len(row) {
			dateRaw = strings.TrimSpace(row[i])
			break
		}
	}
	if dateRaw == "" {
		return ParsedTransaction{}, false, nil
	}
	saleDateRaw := dateRaw
	if comma := strings.Index(dateRaw, ","); comma >= 0 {
		saleDateRaw = strings.TrimSpace(dateRaw[:comma])
	}
	bookedAt, err := parseCryptoDate(saleDateRaw)
	if err != nil {
		// Skip subtotal/comment rows that don't have a parseable date.
		return ParsedTransaction{}, false, nil
	}

	symbol := strings.ToUpper(strings.TrimSpace(firstNonBlank(col("Descrição e símbolo"), col("Description and symbol"))))
	if symbol == "" || !cryptoSymbolValid(symbol) {
		return ParsedTransaction{}, false, nil
	}

	// Find the units column based on op kind.
	var unitsRaw string
	for _, candidate := range cryptoUnitsColumnNames(op) {
		if v := col(candidate); v != "" {
			unitsRaw = v
			break
		}
	}
	units, _, err := parseConsolidatedMoney(unitsRaw, lang)
	if err != nil {
		return ParsedTransaction{}, false, nil
	}
	// Sell-like ops emit a negative amount on the wallet account; buy-like
	// ops are positive. Withdrawals also negative.
	signed := units
	switch op {
	case cryptoOpSell, cryptoOpWithdrawal:
		signed = units.Neg()
	}

	desc := cryptoDescription(op, symbol)
	descPtr := &desc

	raw := map[string]string{
		"section":  "Crypto",
		"op":       string(op),
		"symbol":   symbol,
		"currency": symbol,
		"date":     dateRaw,
	}
	for i, c := range columns {
		if c == "" || i >= len(row) {
			continue
		}
		raw[c] = row[i]
	}

	return ParsedTransaction{
		BookedAt:        bookedAt,
		Amount:          signed,
		Currency:        symbol,
		CounterpartyRaw: descPtr,
		Description:     descPtr,
		AccountHint:     "Crypto " + symbol,
		KindHint:        "crypto_wallet",
		Raw:             raw,
	}, true, nil
}

// cryptoSymbolValid mirrors the money_currency domain regex
// (^[A-Z0-9]{3,10}$). Symbols outside this range are skipped (we'd
// otherwise fail at INSERT time).
func cryptoSymbolValid(s string) bool {
	if len(s) < 3 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// cryptoUnitsColumnNames returns the candidate column labels carrying the
// quantity for a given op, in priority order. Each sub-table phrases the
// quantity differently (sold / bought / deposited / withdrawn / received).
func cryptoUnitsColumnNames(op cryptoOpKind) []string {
	switch op {
	case cryptoOpSell:
		return []string{"Unidades vendidas", "Units sold"}
	case cryptoOpBuy:
		return []string{"Unidades compradas", "Units bought"}
	case cryptoOpDeposit:
		return []string{"Unidades depositadas", "Units deposited"}
	case cryptoOpWithdrawal:
		return []string{"Unidades retiradas", "Units withdrawn"}
	case cryptoOpLearnEarn, cryptoOpStaking:
		return []string{"Unidades recebidas", "Units received"}
	}
	return nil
}

func cryptoDescription(op cryptoOpKind, symbol string) string {
	switch op {
	case cryptoOpBuy:
		return "Buy " + symbol
	case cryptoOpSell:
		return "Sell " + symbol
	case cryptoOpDeposit:
		return "Deposit " + symbol
	case cryptoOpWithdrawal:
		return "Withdrawal " + symbol
	case cryptoOpLearnEarn:
		return "Learn & Earn reward " + symbol
	case cryptoOpStaking:
		return "Staking reward " + symbol
	}
	return symbol
}

// parseCryptoDate accepts dd.mm.yy (the crypto section's local format).
func parseCryptoDate(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	for _, layout := range []string{"02.01.06", "02.01.2006"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return datePart(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid crypto date %q", s)
}

// parseConsolidatedMMFSection walks the "Fundos Monetários Flexíveis" /
// "Flexible Cash Funds" block. The consolidated export only contains
// interest payments here — fund-share buys and sells live in the
// standalone savings statement, not in this file. We emit each interest
// payment as a positive-amount transaction in a per-currency brokerage
// account, leaving the position pipeline (instruments / trades / lots)
// to be wired in when the savings-statement parser ships.
func parseConsolidatedMMFSection(records [][]string, lang consolidatedLang) ([]ParsedTransaction, int, error) {
	header := "Fundos Monetários Flexíveis Extratos de operações"
	if lang == consolidatedLangEN {
		header = "Flexible Cash Funds Transaction statements"
	}
	startIdx := -1
	for i, row := range records {
		if headerEq(firstField(row), header) {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return nil, 0, nil
	}
	endIdx := len(records)
	for i := startIdx + 1; i < len(records); i++ {
		first := strings.TrimSpace(firstField(records[i]))
		if first == "" {
			continue
		}
		if isConsolidatedTopLevelHeader(first, lang) && !headerEq(first, header) {
			endIdx = i
			break
		}
	}

	var out []ParsedTransaction
	skipped := 0
	var currency string
	var columns []string
	expectColumns := false
	subHeaderPat := regexp.MustCompile(`\s*\(([A-Z]{3})\)\s*$`)
	for i := startIdx + 1; i < endIdx; i++ {
		row := records[i]
		first := strings.TrimSpace(firstField(row))
		if first == "" {
			continue
		}
		if first == "Total" || strings.HasPrefix(first, "---") {
			continue
		}
		// Sub-section header: "Fundos Monetários Flexíveis  (EUR)" / "(GBP)" / "(USD)"
		if m := subHeaderPat.FindStringSubmatch(first); m != nil && allBlank(row[1:]) {
			currency = m[1]
			columns = nil
			continue
		}
		// Column header: "Transaction statement (only returns)" then a row of column labels.
		if headerEqAny(first,
			"Transaction statement (only returns)",
			"Extrato de operações (apenas rendimentos)") {
			expectColumns = true
			continue
		}
		if expectColumns {
			columns = make([]string, len(row))
			for j, c := range row {
				columns[j] = strings.TrimSpace(c)
			}
			expectColumns = false
			continue
		}
		if currency == "" || columns == nil {
			continue
		}
		tx, ok, parseErr := parseMMFRow(row, columns, currency, lang)
		if parseErr != nil {
			return nil, 0, parseErr
		}
		if !ok {
			skipped++
			continue
		}
		out = append(out, tx)
	}
	return out, skipped, nil
}

// parseMMFRow turns a single Flexible Cash Funds interest row into a
// positive-amount transaction in the per-currency brokerage account.
// Columns we care about:
//
//	Data | Descrição | Juros líquidos | Imposto retido | Outros impostos | Comissões de serviço | Juros líquidos distribuídos e levantados
//
// "Juros líquidos" is net interest after fees and is the cash impact on
// the fund. "distribuídos e levantados" mirrors that value for fully
// distributed funds; we use the simpler "Juros líquidos" column.
func parseMMFRow(row, columns []string, currency string, lang consolidatedLang) (ParsedTransaction, bool, error) {
	col := func(name string) string {
		for i, c := range columns {
			if c == name && i < len(row) {
				return strings.TrimSpace(row[i])
			}
		}
		return ""
	}
	dateRaw := col("Data")
	if dateRaw == "" {
		dateRaw = col("Date")
	}
	if dateRaw == "" {
		return ParsedTransaction{}, false, nil
	}
	bookedAt, err := parseConsolidatedDate(dateRaw, lang)
	if err != nil {
		return ParsedTransaction{}, false, nil
	}
	amountRaw := firstNonBlank(col("Juros líquidos"), col("Net interest"))
	if amountRaw == "" {
		return ParsedTransaction{}, false, nil
	}
	amount, _, err := parseConsolidatedMoney(amountRaw, lang)
	if err != nil {
		return ParsedTransaction{}, false, fmt.Errorf("parse MMF amount %q: %w", amountRaw, err)
	}
	if amount.IsZero() {
		// Skip zero-interest rows (sub-cent days where Revolut reports 0).
		return ParsedTransaction{}, false, nil
	}
	desc := firstNonBlank(col("Descrição"), col("Description"), "Interest earned - Flexible Cash Funds")
	descPtr := &desc

	raw := map[string]string{
		"section":  "Flexible Cash Funds",
		"op":       "interest",
		"currency": currency,
	}
	for i, c := range columns {
		if c == "" || i >= len(row) {
			continue
		}
		raw[c] = row[i]
	}
	return ParsedTransaction{
		BookedAt:        bookedAt,
		Amount:          amount,
		Currency:        currency,
		CounterpartyRaw: descPtr,
		Description:     descPtr,
		AccountHint:     "Flexible Cash Funds",
		KindHint:        "brokerage",
		Raw:             raw,
	}, true, nil
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
	return headerEqAny(s, "Extrato de operações", "Transaction statement")
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
		first := firstField(row)
		if headerEqAny(first, "Extrato de operações", "Contas-correntes Resumos") {
			return consolidatedLangPT
		}
		if headerEqAny(first, "Transaction statement", "Current Accounts Summaries") {
			return consolidatedLangEN
		}
	}
	return consolidatedLangPT
}

func consolidatedTxAnchor(lang consolidatedLang) string {
	if lang == consolidatedLangEN {
		return "Current Accounts Transaction Statements"
	}
	return "Contas-correntes Extratos de operações"
}

// isConsolidatedTopLevelHeader matches the section banners that delimit each
// asset class in the consolidated v2 export. Comparisons are case-insensitive
// (Revolut renamed "Statements"→"statements"→"Statements" across versions)
// and accept both "Commodities" and "Commodity" since the EN export uses
// the latter while older fixtures use the former.
func isConsolidatedTopLevelHeader(s string, lang consolidatedLang) bool {
	return headerEqAny(s,
		// PT
		"Fundos Monetários Flexíveis Extratos de operações",
		"Investment Services Extratos de operações",
		"Cripto Extratos de operações",
		"Bem Extratos de operações",
		"Glossário",
		// EN
		"Flexible Cash Funds Transaction Statements",
		"Investment Services Transaction Statements",
		"Crypto Transaction Statements",
		"Commodities Transaction Statements",
		"Commodity Transaction Statements",
		"Glossary",
	)
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
//	"-378,56 CHF (-413,02€)"          pt-pt non-EUR section, comma decimal
//	"16,77€"                           pt-pt EUR section
//	"20,00$ (16,97€)"                  pt-pt USD section
//	"-32.52 CHF"                       en section, period decimal
//	"27.38 CHF"                        en section
//	"1 063,95 CHF"                pt-pt with NBSP thousands separator (≥1000)
//	"-2 585,94 CHF (-2 750€)"     same, negative
//
// Returns the signed amount and the trimmed currency suffix (which the caller
// generally ignores in favour of the section currency).
var consolidatedMoneyHead = regexp.MustCompile(`^(-?[\d.,]+)([A-Za-z€$£¥]*)`)

func parseConsolidatedMoney(raw string, lang consolidatedLang) (decimal.Decimal, string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return decimal.Zero, "", nil
	}
	// Revolut's pt-locale exports use U+00A0 (non-breaking space) as a
	// thousand separator, e.g. "1 063,95 CHF". Without this strip the
	// regex stops at the NBSP and parses the value as just "1". Strip ALL
	// whitespace (NBSP + ASCII) up to the currency code so the numeric
	// portion is one token.
	stripped := stripWhitespace(s)
	// EN exports add a parenthesized cross-currency display when the txn
	// currency differs from the section currency: "$5.53 (5.51 CHF)" or
	// "€10.00 (11.38 CHF)". The section currency is always derivable from
	// upstream context, so we drop everything from the open-paren onwards.
	if i := strings.Index(stripped, "("); i >= 0 {
		stripped = strings.TrimSpace(stripped[:i])
	}
	// EN exports prefix the currency symbol ("$0.05", "-$0.01", "€10.00")
	// while pt-pt exports suffix it ("16,77€"). Detect a leading sign and
	// optional currency symbol so the numeric regex can match the digits.
	sign := ""
	if strings.HasPrefix(stripped, "-") {
		sign = "-"
		stripped = stripped[1:]
	}
	leadCurrency := ""
	for _, sym := range []string{"€", "$", "£", "¥"} {
		if strings.HasPrefix(stripped, sym) {
			leadCurrency = sym
			stripped = strings.TrimPrefix(stripped, sym)
			break
		}
	}
	m := consolidatedMoneyHead.FindStringSubmatch(stripped)
	if m == nil {
		return decimal.Zero, "", fmt.Errorf("invalid money %q", raw)
	}
	d, err := parseLocalizedDecimal(sign+m[1], lang)
	if err != nil {
		return decimal.Zero, "", err
	}
	cur := m[2]
	if cur == "" {
		cur = leadCurrency
	}
	return d, cur, nil
}

// stripWhitespace removes ASCII space, tab, and U+00A0 (non-breaking space).
// We don't use strings.Map(unicode.IsSpace, …) because we want to drop NBSP
// even though some encodings of "is whitespace" exclude it.
func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == ' ' || r == '\t' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
