package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// MerchantAlias is the wire representation of a merchant_aliases row.
// IsRegex is always false in v1 — the column is preserved in the schema
// for future regex-pattern support but is not exposed on the API
// (json:"-"). The field stays on the struct so the DB scan still works.
type MerchantAlias struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspaceId"`
	MerchantID  uuid.UUID `json:"merchantId"`
	RawPattern  string    `json:"rawPattern"`
	IsRegex     bool      `json:"-"`
	CreatedAt   time.Time `json:"createdAt"`
}

const aliasCols = `id, workspace_id, merchant_id, raw_pattern, is_regex, created_at`

func scanAlias(r interface{ Scan(dest ...any) error }, a *MerchantAlias) error {
	return r.Scan(&a.ID, &a.WorkspaceID, &a.MerchantID, &a.RawPattern, &a.IsRegex, &a.CreatedAt)
}

// AddAlias inserts a new merchant_aliases row pointing rawPattern → merchantID.
// Trims whitespace; rejects empty input. Returns:
//   - 404 if merchantID is unknown in workspaceID
//   - ValidationError ("alias already mapped...") if rawPattern is already
//     mapped in the workspace (the unique (workspace_id, raw_pattern) enforces
//     this). The classification package's local convention is to surface
//     duplicate-key violations as ValidationError; see mapWriteError in
//     service.go.
func (s *Service) AddAlias(ctx context.Context, workspaceID, merchantID uuid.UUID, rawPattern string) (*MerchantAlias, error) {
	rawPattern = strings.TrimSpace(rawPattern)
	if rawPattern == "" {
		return nil, httpx.NewValidationError("rawPattern is required")
	}
	if err := s.assertMerchantBelongs(ctx, workspaceID, merchantID); err != nil {
		return nil, err
	}
	id := uuidx.New()
	row := s.pool.QueryRow(ctx,
		`insert into merchant_aliases (id, workspace_id, merchant_id, raw_pattern)
		 values ($1, $2, $3, $4)
		 returning `+aliasCols,
		id, workspaceID, merchantID, rawPattern,
	)
	var a MerchantAlias
	if err := scanAlias(row, &a); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, httpx.NewValidationError("raw pattern is already mapped to a merchant in this workspace")
		}
		return nil, fmt.Errorf("insert alias: %w", err)
	}
	return &a, nil
}

// ListAliases returns all aliases for a merchant scoped to workspaceID,
// ordered by created_at ASC. Validates the merchant belongs to the workspace
// (returns 404 if not).
func (s *Service) ListAliases(ctx context.Context, workspaceID, merchantID uuid.UUID) ([]MerchantAlias, error) {
	if err := s.assertMerchantBelongs(ctx, workspaceID, merchantID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`select `+aliasCols+` from merchant_aliases
		 where workspace_id = $1 and merchant_id = $2
		 order by created_at`,
		workspaceID, merchantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	out := make([]MerchantAlias, 0)
	for rows.Next() {
		var a MerchantAlias
		if err := scanAlias(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RemoveAlias deletes the alias by aliasID, scoped to workspaceID and
// merchantID. Returns 404 if no row matches.
func (s *Service) RemoveAlias(ctx context.Context, workspaceID, merchantID, aliasID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`delete from merchant_aliases
		 where workspace_id = $1 and merchant_id = $2 and id = $3`,
		workspaceID, merchantID, aliasID,
	)
	if err != nil {
		return fmt.Errorf("delete alias: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("alias")
	}
	return nil
}

// assertMerchantBelongs returns 404 if merchantID is not in workspaceID.
func (s *Service) assertMerchantBelongs(ctx context.Context, workspaceID, merchantID uuid.UUID) error {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`select true from merchants where workspace_id = $1 and id = $2`,
		workspaceID, merchantID,
	).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewNotFoundError("merchant")
	}
	return err
}
