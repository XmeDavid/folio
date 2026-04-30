// Package transfers owns cross-account transfer-pair detection,
// candidate suggestions for ambiguous matches, and manual pair / unpair
// lifecycle operations. Pairing is data-only via transfer_matches; nothing
// about the underlying transactions changes.
package transfers

import (
	"time"

	"github.com/google/uuid"
)

// TransferMatch is the wire shape of a transfer_matches row.
type TransferMatch struct {
	ID                       uuid.UUID  `json:"id"`
	WorkspaceID              uuid.UUID  `json:"workspaceId"`
	SourceTransactionID      uuid.UUID  `json:"sourceTransactionId"`
	DestinationTransactionID *uuid.UUID `json:"destinationTransactionId,omitempty"`
	FXRate                   *string    `json:"fxRate,omitempty"`
	FeeAmount                *string    `json:"feeAmount,omitempty"`
	FeeCurrency              *string    `json:"feeCurrency,omitempty"`
	ToleranceNote            *string    `json:"toleranceNote,omitempty"`
	Provenance               string     `json:"provenance"`
	MatchedByUserID          *uuid.UUID `json:"matchedByUserId,omitempty"`
	MatchedAt                time.Time  `json:"matchedAt"`
	CreatedAt                time.Time  `json:"createdAt"`
}

// TransferCandidate is the wire shape of a transfer_match_candidates row.
type TransferCandidate struct {
	ID                      uuid.UUID   `json:"id"`
	WorkspaceID             uuid.UUID   `json:"workspaceId"`
	SourceTransactionID     uuid.UUID   `json:"sourceTransactionId"`
	CandidateDestinationIDs []uuid.UUID `json:"candidateDestinationIds"`
	Status                  string      `json:"status"`
	SuggestedAt             time.Time   `json:"suggestedAt"`
	ResolvedAt              *time.Time  `json:"resolvedAt,omitempty"`
	ResolvedByUserID        *uuid.UUID  `json:"resolvedByUserId,omitempty"`
}

// DetectScope bounds which transactions act as the LEFT side of pairing
// in a single DetectAndPair call. The candidate-search query always
// ranges over the entire workspace's unpaired transactions.
type DetectScope struct {
	// All=true => scan every unpaired transaction in the workspace.
	All bool
	// TransactionIDs => scan only these as left sides (used after import).
	TransactionIDs []uuid.UUID
}

// DetectResult captures counts from a single detector pass.
type DetectResult struct {
	Tier1Paired    int `json:"tier1Paired"`
	Tier2Paired    int `json:"tier2Paired"`
	Tier3Suggested int `json:"tier3Suggested"`
}

// ManualPairInput drives the manual-pair endpoint.
type ManualPairInput struct {
	SourceID      uuid.UUID
	DestinationID *uuid.UUID // nil => outbound-to-external
	FeeAmount     *string
	FeeCurrency   *string
	ToleranceNote *string
}
