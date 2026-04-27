package investments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// CorporateAction is the read-model for a row in corporate_actions.
type CorporateAction struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   *uuid.UUID `json:"workspaceId,omitempty"`
	AccountID     *uuid.UUID `json:"accountId,omitempty"`
	InstrumentID  uuid.UUID  `json:"instrumentId"`
	Symbol        string     `json:"symbol"`
	Kind          string     `json:"kind"`
	EffectiveDate time.Time  `json:"effectiveDate"`
	Payload       any        `json:"payload"`
	AppliedAt     *time.Time `json:"appliedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// validCorporateKind mirrors the corporate_action_kind enum in the schema.
var validCorporateKind = map[string]bool{
	"split":              true,
	"reverse_split":      true,
	"merger":             true,
	"spinoff":            true,
	"delisting":          true,
	"symbol_change":      true,
	"cash_distribution":  true,
	"stock_distribution": true,
}

// CorporateActionInput is the validated input to CreateCorporateAction.
type CorporateActionInput struct {
	AccountID     *uuid.UUID
	InstrumentID  uuid.UUID
	Kind          string
	EffectiveDate time.Time
	// Common payload fields. Only the ones the kind cares about are used.
	Factor    decimal.Decimal // splits / reverse_splits — see comment below
	Amount    decimal.Decimal // cash distributions / delisting cash total
	NewSymbol string          // symbol_change
}

// CreateCorporateAction inserts a row in corporate_actions and replays the
// affected (account, instrument) position. For workspace-scoped accounts the
// caller can scope to a specific account; otherwise the action is workspace-
// wide on a single instrument and applies to every account holding it.
//
// Factor convention for splits/reverse_splits: factor is the multiplier you
// apply to a held quantity. A 4-for-1 forward split → factor 4 (your 10
// shares become 40). A 1-for-50 reverse split → factor 0.02 (your 100
// shares become 2). Cost basis per unit is divided by factor in the replay
// engine so total cost basis is preserved.
func (s *Service) CreateCorporateAction(ctx context.Context, workspaceID uuid.UUID, raw CorporateActionInput) (*CorporateAction, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	if in.AccountID != nil {
		if err := s.ensureInvestmentAccount(ctx, workspaceID, *in.AccountID); err != nil {
			return nil, err
		}
	}

	payload, err := buildCorporateActionPayload(in)
	if err != nil {
		return nil, err
	}

	id := uuidx.New()
	q := dbq.New(s.pool)
	row, err := q.InsertCorporateAction(ctx, dbq.InsertCorporateActionParams{
		ID:            id,
		WorkspaceID:   &workspaceID,
		AccountID:     in.AccountID,
		InstrumentID:  in.InstrumentID,
		Kind:          dbq.CorporateActionKind(in.Kind),
		EffectiveDate: in.EffectiveDate,
		Payload:       payload,
	})
	if err != nil {
		return nil, mapWriteError(err)
	}

	// Get symbol for the response.
	instRow, _ := q.GetInstrumentByID(ctx, in.InstrumentID)

	ca := &CorporateAction{
		ID: row.ID, WorkspaceID: row.WorkspaceID, AccountID: row.AccountID,
		InstrumentID: row.InstrumentID, Symbol: instRow.Symbol,
		Kind: row.Kind, EffectiveDate: row.EffectiveDate,
		AppliedAt: row.AppliedAt, CreatedAt: row.CreatedAt,
	}
	if len(row.Payload) > 0 {
		var v any
		if err := json.Unmarshal(row.Payload, &v); err == nil {
			ca.Payload = v
		}
	}

	// Replay every position that touched this instrument so the cache reflects
	// the new state. When AccountID is set, only that pair is touched;
	// otherwise we walk every account that holds the instrument.
	if in.AccountID != nil {
		if err := s.RefreshPosition(ctx, workspaceID, *in.AccountID, in.InstrumentID); err != nil {
			return ca, fmt.Errorf("refresh position: %w", err)
		}
	} else {
		aids, err := q.ListPositionAccountsForInstrument(ctx, dbq.ListPositionAccountsForInstrumentParams{
			WorkspaceID:  workspaceID,
			InstrumentID: in.InstrumentID,
		})
		if err == nil {
			for _, aid := range aids {
				_ = s.RefreshPosition(ctx, workspaceID, aid, in.InstrumentID)
			}
		}
	}
	return ca, nil
}

// DeleteCorporateAction removes a row and replays affected positions.
func (s *Service) DeleteCorporateAction(ctx context.Context, workspaceID, actionID uuid.UUID) error {
	q := dbq.New(s.pool)
	row, err := q.GetCorporateActionForDelete(ctx, dbq.GetCorporateActionForDeleteParams{
		ID:          actionID,
		WorkspaceID: &workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("corporate action")
		}
		return err
	}
	// We only let workspace-scoped users delete workspace-scoped rows.
	affected, err := q.DeleteCorporateAction(ctx, dbq.DeleteCorporateActionParams{
		ID:          actionID,
		WorkspaceID: &workspaceID,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewNotFoundError("corporate action")
	}
	if row.AccountID != nil {
		return s.RefreshPosition(ctx, workspaceID, *row.AccountID, row.InstrumentID)
	}
	aids, err := q.ListPositionAccountsForInstrument(ctx, dbq.ListPositionAccountsForInstrumentParams{
		WorkspaceID:  workspaceID,
		InstrumentID: row.InstrumentID,
	})
	if err == nil {
		for _, aid := range aids {
			_ = s.RefreshPosition(ctx, workspaceID, aid, row.InstrumentID)
		}
	}
	return nil
}

// ListCorporateActions returns workspace-scoped + global corporate-action
// rows for an instrument.
func (s *Service) ListCorporateActions(ctx context.Context, workspaceID, instrumentID uuid.UUID) ([]CorporateAction, error) {
	rows, err := dbq.New(s.pool).ListCorporateActions(ctx, dbq.ListCorporateActionsParams{
		InstrumentID: instrumentID,
		WorkspaceID:  &workspaceID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]CorporateAction, 0, len(rows))
	for _, r := range rows {
		ca := CorporateAction{
			ID: r.ID, WorkspaceID: r.WorkspaceID, AccountID: r.AccountID,
			InstrumentID: r.InstrumentID, Symbol: r.Symbol,
			Kind: r.Kind, EffectiveDate: r.EffectiveDate,
			AppliedAt: r.AppliedAt, CreatedAt: r.CreatedAt,
		}
		if len(r.Payload) > 0 {
			var v any
			if err := json.Unmarshal(r.Payload, &v); err == nil {
				ca.Payload = v
			}
		}
		out = append(out, ca)
	}
	return out, nil
}

func (in CorporateActionInput) normalize() (CorporateActionInput, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if !validCorporateKind[in.Kind] {
		return in, httpx.NewValidationError(
			"kind must be one of: split, reverse_split, merger, spinoff, delisting, symbol_change, cash_distribution, stock_distribution")
	}
	if in.EffectiveDate.IsZero() {
		return in, httpx.NewValidationError("effectiveDate is required")
	}
	if in.InstrumentID == uuid.Nil {
		return in, httpx.NewValidationError("instrumentId is required")
	}
	switch in.Kind {
	case "split", "reverse_split":
		if in.Factor.LessThanOrEqual(decimal.Zero) {
			return in, httpx.NewValidationError(
				"factor must be > 0 (e.g. 4 for a 4-for-1 forward split, 0.02 for a 1-for-50 reverse split)")
		}
	case "cash_distribution", "delisting":
		if in.Amount.IsNegative() {
			return in, httpx.NewValidationError("amount must be >= 0")
		}
	case "symbol_change":
		if strings.TrimSpace(in.NewSymbol) == "" {
			return in, httpx.NewValidationError("newSymbol is required for symbol_change")
		}
	}
	return in, nil
}

func buildCorporateActionPayload(in CorporateActionInput) ([]byte, error) {
	body := map[string]any{}
	switch in.Kind {
	case "split", "reverse_split":
		body["factor"] = in.Factor.String()
	case "cash_distribution":
		body["amount"] = in.Amount.String()
	case "delisting":
		body["cash_total"] = in.Amount.String()
	case "symbol_change":
		body["new_symbol"] = strings.ToUpper(strings.TrimSpace(in.NewSymbol))
	}
	return json.Marshal(body)
}
