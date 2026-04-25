// Package bankimport parses account-specific bank export files and applies
// them to Folio's ledger with provenance and duplicate detection.
package bankimport

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const dateOnly = "2006-01-02"

type ParsedFile struct {
	Profile      string
	Institution  string
	AccountHint  string
	Currency     string
	DateFrom     *time.Time
	DateTo       *time.Time
	Transactions []ParsedTransaction
	Warnings     []string
}

type ParsedTransaction struct {
	BookedAt        time.Time
	ValueAt         *time.Time
	PostedAt        *time.Time
	Amount          decimal.Decimal
	Currency        string
	CounterpartyRaw *string
	Description     *string
	ExternalID      string
	Raw             map[string]string
}

type Preview struct {
	Profile              string            `json:"profile"`
	Institution          string            `json:"institution,omitempty"`
	AccountHint          string            `json:"accountHint,omitempty"`
	SuggestedName        string            `json:"suggestedName,omitempty"`
	SuggestedKind        string            `json:"suggestedKind,omitempty"`
	SuggestedCurrency    string            `json:"suggestedCurrency,omitempty"`
	SuggestedOpenDate    string            `json:"suggestedOpenDate,omitempty"`
	TransactionCount     int               `json:"transactionCount"`
	DateFrom             string            `json:"dateFrom,omitempty"`
	DateTo               string            `json:"dateTo,omitempty"`
	SampleTransactions   []PreviewRow      `json:"sampleTransactions"`
	Warnings             []string          `json:"warnings,omitempty"`
	FileToken            string            `json:"fileToken"`
	FileName             string            `json:"fileName,omitempty"`
	FileHash             string            `json:"fileHash"`
	ExistingAccountID    *uuid.UUID        `json:"existingAccountId,omitempty"`
	DuplicateCount       int               `json:"duplicateCount"`
	ConflictCount        int               `json:"conflictCount"`
	ImportableCount      int               `json:"importableCount"`
	ConflictTransactions []ConflictPreview `json:"conflictTransactions,omitempty"`
	CurrencyGroups       []CurrencyGroup   `json:"currencyGroups,omitempty"`
}

type PreviewRow struct {
	BookedAt    string `json:"bookedAt"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
}

type ConflictPreview struct {
	Incoming PreviewRow `json:"incoming"`
	Existing PreviewRow `json:"existing"`
}

type CurrencyGroup struct {
	Currency             string             `json:"currency"`
	SuggestedName        string             `json:"suggestedName"`
	SuggestedKind        string             `json:"suggestedKind"`
	SuggestedOpenDate    string             `json:"suggestedOpenDate,omitempty"`
	TransactionCount     int                `json:"transactionCount"`
	DateFrom             string             `json:"dateFrom,omitempty"`
	DateTo               string             `json:"dateTo,omitempty"`
	ExistingAccountID    *uuid.UUID         `json:"existingAccountId,omitempty"`
	ExistingAccountName  string             `json:"existingAccountName,omitempty"`
	CandidateAccounts    []AccountCandidate `json:"candidateAccounts,omitempty"`
	Action               string             `json:"action"`
	ImportableCount      int                `json:"importableCount"`
	DuplicateCount       int                `json:"duplicateCount"`
	ConflictCount        int                `json:"conflictCount"`
	SampleTransactions   []PreviewRow       `json:"sampleTransactions"`
	ConflictTransactions []ConflictPreview  `json:"conflictTransactions,omitempty"`
}

type AccountCandidate struct {
	ID                   uuid.UUID         `json:"id"`
	Name                 string            `json:"name"`
	Currency             string            `json:"currency"`
	Institution          string            `json:"institution,omitempty"`
	ImportableCount      int               `json:"importableCount"`
	DuplicateCount       int               `json:"duplicateCount"`
	ConflictCount        int               `json:"conflictCount"`
	ConflictTransactions []ConflictPreview `json:"conflictTransactions,omitempty"`
}

type ApplyPlanInput struct {
	FileToken string           `json:"fileToken"`
	Groups    []ApplyPlanGroup `json:"groups"`
}

type ApplyPlanGroup struct {
	Currency           string     `json:"currency"`
	Action             string     `json:"action"`
	AccountID          *uuid.UUID `json:"accountId,omitempty"`
	Name               string     `json:"name,omitempty"`
	Kind               string     `json:"kind,omitempty"`
	OpenDate           string     `json:"openDate,omitempty"`
	OpeningBalance     string     `json:"openingBalance,omitempty"`
	OpeningBalanceDate string     `json:"openingBalanceDate,omitempty"`
}

type ApplyResult struct {
	BatchID        uuid.UUID         `json:"batchId"`
	InsertedCount  int               `json:"insertedCount"`
	DuplicateCount int               `json:"duplicateCount"`
	ConflictCount  int               `json:"conflictCount"`
	TransactionIDs []uuid.UUID       `json:"transactionIds"`
	Conflicts      []ConflictPreview `json:"conflicts,omitempty"`
}

type previewPayload struct {
	FileName string `json:"fileName"`
	FileHash string `json:"fileHash"`
	Content  string `json:"content"`
}
