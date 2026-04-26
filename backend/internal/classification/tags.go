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

// Tag is the read-model returned by the API.
type Tag struct {
	ID         uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspaceId"`
	Name       string     `json:"name"`
	Color      *string    `json:"color,omitempty"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// TagCreateInput is the validated input to CreateTag.
type TagCreateInput struct {
	Name  string
	Color *string
}

func (in TagCreateInput) normalize() (TagCreateInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, httpx.NewValidationError("name is required")
	}
	return in, nil
}

// TagPatchInput is the validated input to UpdateTag. color clears on empty string.
type TagPatchInput struct {
	Name     *string
	Color    *string
	Archived *bool
}

type tagPatchNormalized struct {
	nameSet     bool
	name        string
	colorSet    bool
	colorNull   bool
	color       string
	archivedSet bool
	archived    bool
}

func (in TagPatchInput) normalize() (tagPatchNormalized, error) {
	var out tagPatchNormalized

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return out, httpx.NewValidationError("name cannot be empty")
		}
		out.nameSet = true
		out.name = name
	}
	if in.Color != nil {
		out.colorSet = true
		if *in.Color == "" {
			out.colorNull = true
		} else {
			out.color = *in.Color
		}
	}
	if in.Archived != nil {
		out.archivedSet = true
		out.archived = *in.Archived
	}
	return out, nil
}

const tagCols = `
	id, workspace_id, name, color, archived_at, created_at, updated_at
`

func scanTag(r interface{ Scan(dest ...any) error }, t *Tag) error {
	return r.Scan(
		&t.ID, &t.WorkspaceID, &t.Name, &t.Color, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt,
	)
}

// CreateTag inserts a tag for workspaceID and returns it.
func (s *Service) CreateTag(ctx context.Context, workspaceID uuid.UUID, raw TagCreateInput) (*Tag, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}
	id := uuidx.New()
	row := s.pool.QueryRow(ctx, `
		insert into tags (id, workspace_id, name, color)
		values ($1, $2, $3, $4)
		returning `+tagCols,
		id, workspaceID, in.Name, in.Color,
	)
	var t Tag
	if err := scanTag(row, &t); err != nil {
		return nil, mapWriteError("tag", err)
	}
	return &t, nil
}

// ListTags returns tags for workspaceID. Archived rows are excluded unless
// includeArchived is true.
func (s *Service) ListTags(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]Tag, error) {
	q := `select ` + tagCols + ` from tags where workspace_id = $1`
	if !includeArchived {
		q += ` and archived_at is null`
	}
	q += ` order by name`

	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()
	out := make([]Tag, 0)
	for rows.Next() {
		var t Tag
		if err := scanTag(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTag returns a single tag scoped to workspaceID.
func (s *Service) GetTag(ctx context.Context, workspaceID, id uuid.UUID) (*Tag, error) {
	row := s.pool.QueryRow(ctx,
		`select `+tagCols+` from tags where workspace_id = $1 and id = $2`,
		workspaceID, id)
	var t Tag
	if err := scanTag(row, &t); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("tag")
		}
		return nil, err
	}
	return &t, nil
}

// UpdateTag applies a PATCH and returns the result.
func (s *Service) UpdateTag(ctx context.Context, workspaceID, id uuid.UUID, raw TagPatchInput) (*Tag, error) {
	p, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	sets := make([]string, 0, 3)
	args := []any{workspaceID, id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if p.nameSet {
		sets = append(sets, "name = "+next(p.name))
	}
	if p.colorSet {
		if p.colorNull {
			sets = append(sets, "color = null")
		} else {
			sets = append(sets, "color = "+next(p.color))
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
		return s.GetTag(ctx, workspaceID, id)
	}

	q := fmt.Sprintf(`
		update tags set %s
		where workspace_id = $1 and id = $2
		returning %s
	`, strings.Join(sets, ", "), tagCols)

	row := s.pool.QueryRow(ctx, q, args...)
	var t Tag
	if err := scanTag(row, &t); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("tag")
		}
		return nil, mapWriteError("tag", err)
	}
	return &t, nil
}

// ArchiveTag sets archived_at = now() idempotently.
func (s *Service) ArchiveTag(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		update tags
		set archived_at = coalesce(archived_at, $3)
		where workspace_id = $1 and id = $2
	`, workspaceID, id, s.now().UTC())
	if err != nil {
		return fmt.Errorf("archive tag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("tag")
	}
	return nil
}
