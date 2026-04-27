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
	Factor   decimal.Decimal // splits / reverse_splits — see comment below
	Amount   decimal.Decimal // cash distributions / delisting cash total
	NewSymbol string         // symbol_change
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
	row := s.pool.QueryRow(ctx, `
		insert into corporate_actions (
			id, workspace_id, account_id, instrument_id,
			kind, effective_date, payload
		) values (
			$1, $2, $3, $4,
			$5::corporate_action_kind, $6, $7::jsonb
		)
		returning `+corpActionCols, id, workspaceID, in.AccountID, in.InstrumentID,
		in.Kind, in.EffectiveDate, string(payload))
	ca, err := scanCorpAction(row)
	if err != nil {
		return nil, mapWriteError(err)
	}

	// Replay every position that touched this instrument so the cache reflects
	// the new state. When AccountID is set, only that pair is touched;
	// otherwise we walk every account that holds the instrument.
	if in.AccountID != nil {
		if err := s.RefreshPosition(ctx, workspaceID, *in.AccountID, in.InstrumentID); err != nil {
			return ca, fmt.Errorf("refresh position: %w", err)
		}
	} else {
		rows, err := s.pool.Query(ctx, `
			select distinct account_id from investment_positions
			where workspace_id = $1 and instrument_id = $2
		`, workspaceID, in.InstrumentID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var aid uuid.UUID
				if err := rows.Scan(&aid); err == nil {
					_ = s.RefreshPosition(ctx, workspaceID, aid, in.InstrumentID)
				}
			}
		}
	}
	return ca, nil
}

// DeleteCorporateAction removes a row and replays affected positions.
func (s *Service) DeleteCorporateAction(ctx context.Context, workspaceID, actionID uuid.UUID) error {
	var (
		instrumentID uuid.UUID
		accountID    *uuid.UUID
	)
	err := s.pool.QueryRow(ctx, `
		select instrument_id, account_id from corporate_actions
		where id = $1 and (workspace_id = $2 or workspace_id is null)
	`, actionID, workspaceID).Scan(&instrumentID, &accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewNotFoundError("corporate action")
		}
		return err
	}
	// We only let workspace-scoped users delete workspace-scoped rows.
	tag, err := s.pool.Exec(ctx, `
		delete from corporate_actions
		where id = $1 and workspace_id = $2
	`, actionID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("corporate action")
	}
	if accountID != nil {
		return s.RefreshPosition(ctx, workspaceID, *accountID, instrumentID)
	}
	rows, err := s.pool.Query(ctx, `
		select distinct account_id from investment_positions
		where workspace_id = $1 and instrument_id = $2
	`, workspaceID, instrumentID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var aid uuid.UUID
			if err := rows.Scan(&aid); err == nil {
				_ = s.RefreshPosition(ctx, workspaceID, aid, instrumentID)
			}
		}
	}
	return nil
}

// ListCorporateActions returns workspace-scoped + global corporate-action
// rows for an instrument.
func (s *Service) ListCorporateActions(ctx context.Context, workspaceID, instrumentID uuid.UUID) ([]CorporateAction, error) {
	rows, err := s.pool.Query(ctx, `
		select `+corpActionCols+`
		from corporate_actions ca
		join instruments i on i.id = ca.instrument_id
		where ca.instrument_id = $1
		  and (ca.workspace_id is null or ca.workspace_id = $2)
		order by ca.effective_date desc, ca.created_at desc
	`, instrumentID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CorporateAction, 0)
	for rows.Next() {
		ca, err := scanCorpAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ca)
	}
	return out, rows.Err()
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

const corpActionCols = `
	ca.id, ca.workspace_id, ca.account_id, ca.instrument_id, i.symbol,
	ca.kind::text, ca.effective_date, ca.payload, ca.applied_at, ca.created_at
`

func scanCorpAction(row rowScanner) (*CorporateAction, error) {
	var (
		ca      CorporateAction
		payload []byte
	)
	if err := row.Scan(
		&ca.ID, &ca.WorkspaceID, &ca.AccountID, &ca.InstrumentID, &ca.Symbol,
		&ca.Kind, &ca.EffectiveDate, &payload, &ca.AppliedAt, &ca.CreatedAt,
	); err != nil {
		return nil, err
	}
	if len(payload) > 0 {
		var v any
		if err := json.Unmarshal(payload, &v); err == nil {
			ca.Payload = v
		}
	}
	return &ca, nil
}
