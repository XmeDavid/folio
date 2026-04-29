package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// AttachByRaw resolves the counterparty_raw string to a Merchant for
// workspaceID, creating one on first sight. Returns nil if raw is empty
// or only whitespace. Archived merchants and their aliases are ignored.
//
// Concurrency: relies on the partial unique index
// merchants_active_canonical_name_uniq for create-on-conflict idempotency.
func (s *Service) AttachByRaw(ctx context.Context, workspaceID uuid.UUID, raw string) (*Merchant, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if m, err := s.findMerchantByCanonical(ctx, workspaceID, raw); err != nil {
		return nil, err
	} else if m != nil {
		return m, nil
	}

	if m, err := s.findMerchantByAlias(ctx, workspaceID, raw); err != nil {
		return nil, err
	} else if m != nil {
		return m, nil
	}

	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into merchants (id, workspace_id, canonical_name)
		values ($1, $2, $3)
		on conflict (workspace_id, canonical_name) where archived_at is null
		do nothing
		returning `+merchantCols,
		id, workspaceID, raw,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, mapWriteError("merchant", err)
		}
		// Lost the race; re-resolve.
		again, err := s.findMerchantByCanonical(ctx, workspaceID, raw)
		if err != nil {
			return nil, err
		}
		if again == nil {
			return nil, fmt.Errorf("attach_by_raw: lost-race resolve returned no merchant for %q", raw)
		}
		return again, nil
	}
	return &m, nil
}

func (s *Service) findMerchantByCanonical(ctx context.Context, workspaceID uuid.UUID, raw string) (*Merchant, error) {
	row := s.pool.QueryRow(ctx,
		`select `+merchantCols+`
		 from merchants
		 where workspace_id = $1 and canonical_name = $2 and archived_at is null`,
		workspaceID, raw,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup merchant by canonical_name: %w", err)
	}
	return &m, nil
}

func (s *Service) findMerchantByAlias(ctx context.Context, workspaceID uuid.UUID, raw string) (*Merchant, error) {
	row := s.pool.QueryRow(ctx,
		`select m.id, m.workspace_id, m.canonical_name, m.logo_url, m.default_category_id,
		        m.industry, m.website, m.notes, m.archived_at, m.created_at, m.updated_at
		 from merchants m
		 join merchant_aliases a on a.merchant_id = m.id and a.workspace_id = m.workspace_id
		 where a.workspace_id = $1 and a.raw_pattern = $2 and m.archived_at is null`,
		workspaceID, raw,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lookup merchant by alias: %w", err)
	}
	return &m, nil
}
