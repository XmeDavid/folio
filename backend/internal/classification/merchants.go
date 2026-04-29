package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Merchant is the read-model returned by the API.
type Merchant struct {
	ID                uuid.UUID  `json:"id"`
	WorkspaceID          uuid.UUID  `json:"workspaceId"`
	CanonicalName     string     `json:"canonicalName"`
	LogoURL           *string    `json:"logoUrl,omitempty"`
	DefaultCategoryID *uuid.UUID `json:"defaultCategoryId,omitempty"`
	Industry          *string    `json:"industry,omitempty"`
	Website           *string    `json:"website,omitempty"`
	Notes             *string    `json:"notes,omitempty"`
	ArchivedAt        *time.Time `json:"archivedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// MerchantCreateInput is the validated input to CreateMerchant.
type MerchantCreateInput struct {
	CanonicalName     string
	LogoURL           *string
	DefaultCategoryID *uuid.UUID
	Industry          *string
	Website           *string
	Notes             *string
}

func (in MerchantCreateInput) normalize() (MerchantCreateInput, error) {
	in.CanonicalName = strings.TrimSpace(in.CanonicalName)
	if in.CanonicalName == "" {
		return in, httpx.NewValidationError("canonicalName is required")
	}
	return in, nil
}

// MerchantPatchInput is the validated input to UpdateMerchant. Nullable
// string fields clear on empty string; defaultCategoryId clears on empty
// string too.
type MerchantPatchInput struct {
	CanonicalName     *string
	LogoURL           *string
	DefaultCategoryID *string
	Industry          *string
	Website           *string
	Notes             *string
	Archived          *bool
}

type merchantPatchNormalized struct {
	canonicalNameSet      bool
	canonicalName         string
	logoURLSet            bool
	logoURLNull           bool
	logoURL               string
	defaultCategoryIDSet  bool
	defaultCategoryIDNull bool
	defaultCategoryID     uuid.UUID
	industrySet           bool
	industryNull          bool
	industry              string
	websiteSet            bool
	websiteNull           bool
	website               string
	notesSet              bool
	notesNull             bool
	notes                 string
	archivedSet           bool
	archived              bool
}

func (in MerchantPatchInput) normalize() (merchantPatchNormalized, error) {
	var out merchantPatchNormalized

	if in.CanonicalName != nil {
		name := strings.TrimSpace(*in.CanonicalName)
		if name == "" {
			return out, httpx.NewValidationError("canonicalName cannot be empty")
		}
		out.canonicalNameSet = true
		out.canonicalName = name
	}
	if in.LogoURL != nil {
		out.logoURLSet = true
		if *in.LogoURL == "" {
			out.logoURLNull = true
		} else {
			out.logoURL = *in.LogoURL
		}
	}
	if in.DefaultCategoryID != nil {
		raw := strings.TrimSpace(*in.DefaultCategoryID)
		out.defaultCategoryIDSet = true
		if raw == "" {
			out.defaultCategoryIDNull = true
		} else {
			id, err := uuid.Parse(raw)
			if err != nil {
				return out, httpx.NewValidationError("defaultCategoryId must be a UUID")
			}
			out.defaultCategoryID = id
		}
	}
	if in.Industry != nil {
		out.industrySet = true
		if *in.Industry == "" {
			out.industryNull = true
		} else {
			out.industry = *in.Industry
		}
	}
	if in.Website != nil {
		out.websiteSet = true
		if *in.Website == "" {
			out.websiteNull = true
		} else {
			out.website = *in.Website
		}
	}
	if in.Notes != nil {
		out.notesSet = true
		if *in.Notes == "" {
			out.notesNull = true
		} else {
			out.notes = *in.Notes
		}
	}
	if in.Archived != nil {
		out.archivedSet = true
		out.archived = *in.Archived
	}
	return out, nil
}

const merchantCols = `
	id, workspace_id, canonical_name, logo_url, default_category_id,
	industry, website, notes, archived_at, created_at, updated_at
`

// merchantColsM is the same column list as merchantCols, prefixed with the
// "m." table alias for use in joins. Keep this in sync with merchantCols
// and scanMerchant: the column count and order must match exactly.
const merchantColsM = `
	m.id, m.workspace_id, m.canonical_name, m.logo_url, m.default_category_id,
	m.industry, m.website, m.notes, m.archived_at, m.created_at, m.updated_at
`

func scanMerchant(r interface{ Scan(dest ...any) error }, m *Merchant) error {
	return r.Scan(
		&m.ID, &m.WorkspaceID, &m.CanonicalName, &m.LogoURL, &m.DefaultCategoryID,
		&m.Industry, &m.Website, &m.Notes, &m.ArchivedAt, &m.CreatedAt, &m.UpdatedAt,
	)
}

// CreateMerchant inserts a merchant for workspaceID and returns it.
func (s *Service) CreateMerchant(ctx context.Context, workspaceID uuid.UUID, raw MerchantCreateInput) (*Merchant, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	if in.DefaultCategoryID != nil {
		if err := s.assertCategoryExists(ctx, workspaceID, *in.DefaultCategoryID); err != nil {
			return nil, err
		}
	}

	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into merchants (
			id, workspace_id, canonical_name, logo_url, default_category_id,
			industry, website, notes
		) values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning `+merchantCols,
		id, workspaceID, in.CanonicalName, in.LogoURL, in.DefaultCategoryID,
		in.Industry, in.Website, in.Notes,
	)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		return nil, mapWriteError("merchant", err)
	}
	return &m, nil
}

// ListMerchants returns merchants for workspaceID. Archived rows are excluded
// unless includeArchived is true.
func (s *Service) ListMerchants(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]Merchant, error) {
	q := `select ` + merchantCols + ` from merchants where workspace_id = $1`
	if !includeArchived {
		q += ` and archived_at is null`
	}
	q += ` order by canonical_name`

	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query merchants: %w", err)
	}
	defer rows.Close()
	out := make([]Merchant, 0)
	for rows.Next() {
		var m Merchant
		if err := scanMerchant(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetMerchant returns a single merchant scoped to workspaceID.
func (s *Service) GetMerchant(ctx context.Context, workspaceID, id uuid.UUID) (*Merchant, error) {
	row := s.pool.QueryRow(ctx,
		`select `+merchantCols+` from merchants where workspace_id = $1 and id = $2`,
		workspaceID, id)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("merchant")
		}
		return nil, err
	}
	return &m, nil
}

// UpdateMerchant applies a PATCH and returns the result.
func (s *Service) UpdateMerchant(ctx context.Context, workspaceID, id uuid.UUID, raw MerchantPatchInput) (*Merchant, error) {
	p, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	if p.defaultCategoryIDSet && !p.defaultCategoryIDNull {
		if err := s.assertCategoryExists(ctx, workspaceID, p.defaultCategoryID); err != nil {
			return nil, err
		}
	}

	sets := make([]string, 0, 8)
	args := []any{workspaceID, id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if p.canonicalNameSet {
		sets = append(sets, "canonical_name = "+next(p.canonicalName))
	}
	if p.logoURLSet {
		if p.logoURLNull {
			sets = append(sets, "logo_url = null")
		} else {
			sets = append(sets, "logo_url = "+next(p.logoURL))
		}
	}
	if p.defaultCategoryIDSet {
		if p.defaultCategoryIDNull {
			sets = append(sets, "default_category_id = null")
		} else {
			sets = append(sets, "default_category_id = "+next(p.defaultCategoryID))
		}
	}
	if p.industrySet {
		if p.industryNull {
			sets = append(sets, "industry = null")
		} else {
			sets = append(sets, "industry = "+next(p.industry))
		}
	}
	if p.websiteSet {
		if p.websiteNull {
			sets = append(sets, "website = null")
		} else {
			sets = append(sets, "website = "+next(p.website))
		}
	}
	if p.notesSet {
		if p.notesNull {
			sets = append(sets, "notes = null")
		} else {
			sets = append(sets, "notes = "+next(p.notes))
		}
	}
	if p.archivedSet {
		if p.archived {
			sets = append(sets, "archived_at = "+next(s.now().UTC()))
		} else {
			sets = append(sets, "archived_at = null")
		}
	}

	if len(sets) == 0 {
		return s.GetMerchant(ctx, workspaceID, id)
	}

	q := fmt.Sprintf(`
		update merchants set %s
		where workspace_id = $1 and id = $2
		returning %s
	`, strings.Join(sets, ", "), merchantCols)

	row := s.pool.QueryRow(ctx, q, args...)
	var m Merchant
	if err := scanMerchant(row, &m); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("merchant")
		}
		return nil, mapWriteError("merchant", err)
	}
	return &m, nil
}

// ArchiveMerchant sets archived_at = now() idempotently.
func (s *Service) ArchiveMerchant(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		update merchants
		set archived_at = coalesce(archived_at, $3)
		where workspace_id = $1 and id = $2
	`, workspaceID, id, s.now().UTC())
	if err != nil {
		return fmt.Errorf("archive merchant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("merchant")
	}
	return nil
}
