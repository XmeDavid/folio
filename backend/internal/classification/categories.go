package classification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// Category is the read-model returned by the API.
type Category struct {
	ID         uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspaceId"`
	ParentID   *uuid.UUID `json:"parentId,omitempty"`
	Name       string     `json:"name"`
	Color      *string    `json:"color,omitempty"`
	SortOrder  int        `json:"sortOrder"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// CategoryCreateInput is the validated input to CreateCategory.
type CategoryCreateInput struct {
	ParentID  *uuid.UUID
	Name      string
	Color     *string
	SortOrder *int
}

func (in CategoryCreateInput) normalize() (CategoryCreateInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, httpx.NewValidationError("name is required")
	}
	return in, nil
}

// CategoryPatchInput is the validated input to UpdateCategory. Pointer fields
// are "absent" when nil. For parentId, an empty string means "clear to NULL"
// (promote to root). For color, an empty string clears it.
type CategoryPatchInput struct {
	ParentID  *string
	Name      *string
	Color     *string
	SortOrder *int
	Archived  *bool
}

type categoryPatchNormalized struct {
	parentIDSet  bool
	parentIDNull bool
	parentID     uuid.UUID
	nameSet      bool
	name         string
	colorSet     bool
	colorNull    bool
	color        string
	sortOrderSet bool
	sortOrder    int
	archivedSet  bool
	archived     bool
}

func (in CategoryPatchInput) normalize() (categoryPatchNormalized, error) {
	var out categoryPatchNormalized

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return out, httpx.NewValidationError("name cannot be empty")
		}
		out.nameSet = true
		out.name = name
	}
	if in.ParentID != nil {
		raw := strings.TrimSpace(*in.ParentID)
		out.parentIDSet = true
		if raw == "" {
			out.parentIDNull = true
		} else {
			id, err := uuid.Parse(raw)
			if err != nil {
				return out, httpx.NewValidationError("parentId must be a UUID")
			}
			out.parentID = id
		}
	}
	if in.Color != nil {
		out.colorSet = true
		if *in.Color == "" {
			out.colorNull = true
		} else {
			out.color = *in.Color
		}
	}
	if in.SortOrder != nil {
		out.sortOrderSet = true
		out.sortOrder = *in.SortOrder
	}
	if in.Archived != nil {
		out.archivedSet = true
		out.archived = *in.Archived
	}
	return out, nil
}

const categoryCols = `
	id, workspace_id, parent_id, name, color, sort_order, archived_at, created_at, updated_at
`

type categoryRow interface {
	Scan(dest ...any) error
}

func scanCategory(r categoryRow, c *Category) error {
	return r.Scan(
		&c.ID, &c.WorkspaceID, &c.ParentID, &c.Name, &c.Color, &c.SortOrder,
		&c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt,
	)
}

func categoryFromRow(r dbq.Category) Category {
	return Category{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		ParentID:    r.ParentID,
		Name:        r.Name,
		Color:       r.Color,
		SortOrder:   int(r.SortOrder),
		ArchivedAt:  r.ArchivedAt,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// CreateCategory inserts a category for workspaceID and returns it.
func (s *Service) CreateCategory(ctx context.Context, workspaceID uuid.UUID, raw CategoryCreateInput) (*Category, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	// Pre-validate parentId belongs to workspace so we can return a clean 400
	// (the composite FK would otherwise surface as a generic write error).
	if in.ParentID != nil {
		if err := s.assertCategoryExists(ctx, workspaceID, *in.ParentID); err != nil {
			return nil, err
		}
	}

	id := uuidx.New()
	var sortOrder *int32
	if in.SortOrder != nil {
		v := int32(*in.SortOrder)
		sortOrder = &v
	}
	row, err := dbq.New(s.pool).InsertCategory(ctx, dbq.InsertCategoryParams{
		ID:          id,
		WorkspaceID: workspaceID,
		ParentID:    in.ParentID,
		Name:        in.Name,
		Color:       in.Color,
		SortOrder:   sortOrder,
	})
	if err != nil {
		return nil, mapWriteError("category", err)
	}
	c := categoryFromRow(row)
	return &c, nil
}

// assertCategoryExists returns a clean validation/not-found error when
// categoryID is missing for workspaceID. Used by Create/Update to pre-validate
// parentId and by the merchants path to pre-validate defaultCategoryId.
func (s *Service) assertCategoryExists(ctx context.Context, workspaceID, categoryID uuid.UUID) error {
	_, err := dbq.New(s.pool).CategoryExists(ctx, dbq.CategoryExistsParams{
		WorkspaceID: workspaceID,
		ID:          categoryID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewValidationError("referenced category does not exist for this workspace")
		}
		return fmt.Errorf("check category: %w", err)
	}
	return nil
}

// ListCategories returns categories for workspaceID. Archived rows are excluded
// unless includeArchived is true.
func (s *Service) ListCategories(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]Category, error) {
	q := `select ` + categoryCols + ` from categories where workspace_id = $1`
	if !includeArchived {
		q += ` and archived_at is null`
	}
	q += ` order by sort_order, name`

	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()
	out := make([]Category, 0)
	for rows.Next() {
		var c Category
		if err := scanCategory(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetCategory returns a single category scoped to workspaceID.
func (s *Service) GetCategory(ctx context.Context, workspaceID, id uuid.UUID) (*Category, error) {
	row, err := dbq.New(s.pool).GetCategory(ctx, dbq.GetCategoryParams{
		WorkspaceID: workspaceID,
		ID:          id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("category")
		}
		return nil, err
	}
	c := categoryFromRow(row)
	return &c, nil
}

// UpdateCategory applies a PATCH to a category and returns the result.
func (s *Service) UpdateCategory(ctx context.Context, workspaceID, id uuid.UUID, raw CategoryPatchInput) (*Category, error) {
	p, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	// Pre-validate parentId belongs to workspace (and not the category itself)
	// before hitting the DB, so the API returns a clean 400.
	if p.parentIDSet && !p.parentIDNull {
		if p.parentID == id {
			return nil, httpx.NewValidationError("category cannot be its own parent")
		}
		if err := s.assertCategoryExists(ctx, workspaceID, p.parentID); err != nil {
			return nil, err
		}
	}

	sets := make([]string, 0, 6)
	args := []any{workspaceID, id}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if p.nameSet {
		sets = append(sets, "name = "+next(p.name))
	}
	if p.parentIDSet {
		if p.parentIDNull {
			sets = append(sets, "parent_id = null")
		} else {
			sets = append(sets, "parent_id = "+next(p.parentID))
		}
	}
	if p.colorSet {
		if p.colorNull {
			sets = append(sets, "color = null")
		} else {
			sets = append(sets, "color = "+next(p.color))
		}
	}
	if p.sortOrderSet {
		sets = append(sets, "sort_order = "+next(p.sortOrder))
	}
	if p.archivedSet {
		if p.archived {
			sets = append(sets, "archived_at = "+next(s.now().UTC()))
		} else {
			sets = append(sets, "archived_at = null")
		}
	}

	if len(sets) == 0 {
		return s.GetCategory(ctx, workspaceID, id)
	}

	q := fmt.Sprintf(`
		update categories set %s
		where workspace_id = $1 and id = $2
		returning %s
	`, strings.Join(sets, ", "), categoryCols)

	row := s.pool.QueryRow(ctx, q, args...)
	var c Category
	if err := scanCategory(row, &c); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("category")
		}
		return nil, mapWriteError("category", err)
	}
	return &c, nil
}

// ArchiveCategory sets archived_at = now() for the category, idempotently.
// Returns NotFoundError only when the row does not exist for workspaceID.
func (s *Service) ArchiveCategory(ctx context.Context, workspaceID, id uuid.UUID) error {
	now := s.now().UTC()
	n, err := dbq.New(s.pool).ArchiveCategory(ctx, dbq.ArchiveCategoryParams{
		Now:         &now,
		WorkspaceID: workspaceID,
		ID:          id,
	})
	if err != nil {
		return fmt.Errorf("archive category: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("category")
	}
	return nil
}
