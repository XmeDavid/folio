package admin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

type workspaceCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type WorkspaceDetail struct {
	Workspace         identity.Workspace `json:"workspace"`
	MemberCount    int             `json:"memberCount"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
	LastActivityAt *time.Time      `json:"lastActivityAt,omitempty"`
}

func (s *Service) ListWorkspaces(ctx context.Context, filter WorkspaceListFilter) ([]identity.Workspace, Pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	var cur workspaceCursor
	if filter.Cursor != "" {
		if err := decodeCursor(filter.Cursor, &cur); err != nil {
			return nil, Pagination{}, httpx.NewValidationError("invalid cursor")
		}
	}
	search := strings.TrimSpace(filter.Search)
	like := "%" + search + "%"
	rows, err := dbq.New(s.pool).AdminListWorkspaces(ctx, dbq.AdminListWorkspacesParams{
		IncludeDeleted:  filter.IncludeDeleted,
		Search:          search,
		SearchPattern:   like,
		CursorCreatedAt: nullTimePtr(cur.CreatedAt),
		CursorID:        nullUUIDPtr(cur.ID),
		QueryLimit:      int32(filter.Limit + 1),
	})
	if err != nil {
		return nil, Pagination{}, err
	}

	out := make([]identity.Workspace, 0, filter.Limit)
	for _, r := range rows {
		out = append(out, identity.Workspace{
			ID:             r.ID,
			Name:           r.Name,
			Slug:           r.Slug,
			BaseCurrency:   r.BaseCurrency,
			CycleAnchorDay: int(r.CycleAnchorDay),
			Locale:         r.Locale,
			Timezone:       r.Timezone,
			DeletedAt:      r.DeletedAt,
			CreatedAt:      r.CreatedAt,
		})
	}
	return pageWorkspaces(out, filter.Limit)
}

func (s *Service) WorkspaceDetail(ctx context.Context, workspaceID uuid.UUID, actorUserID uuid.UUID) (WorkspaceDetail, error) {
	var d WorkspaceDetail
	q := dbq.New(s.pool)
	row, err := q.AdminGetWorkspaceByID(ctx, workspaceID)
	if errorsIsNoRows(err) {
		return d, httpx.NewNotFoundError("workspace")
	}
	if err != nil {
		return d, err
	}
	d.Workspace = identity.Workspace{
		ID:             row.ID,
		Name:           row.Name,
		Slug:           row.Slug,
		BaseCurrency:   row.BaseCurrency,
		CycleAnchorDay: int(row.CycleAnchorDay),
		Locale:         row.Locale,
		Timezone:       row.Timezone,
		DeletedAt:      row.DeletedAt,
		CreatedAt:      row.CreatedAt,
	}
	d.DeletedAt = d.Workspace.DeletedAt

	memberCount, err := q.AdminCountWorkspaceMembers(ctx, workspaceID)
	if err != nil {
		return d, err
	}
	d.MemberCount = int(memberCount)

	// max(occurred_at) returns NULL when no rows match; keep as hand-written
	// query because sqlc cannot express a nullable aggregate scalar cleanly.
	if err := s.pool.QueryRow(ctx, `select max(occurred_at) from audit_events where workspace_id = $1`, workspaceID).Scan(&d.LastActivityAt); err != nil {
		return d, err
	}

	if err := s.writeAdminAuditRow(ctx, "admin.viewed_workspace", actorUserID, "workspace", workspaceID, nil, nil); err != nil {
		slog.Default().Warn("admin.audit_write_failed", "op", "admin.viewed_workspace", "err", err)
	}
	return d, nil
}

func pageWorkspaces(rows []identity.Workspace, limit int) ([]identity.Workspace, Pagination, error) {
	p := Pagination{Limit: limit}
	if len(rows) <= limit {
		return rows, p, nil
	}
	spill := rows[limit]
	c, err := encodeCursor(workspaceCursor{CreatedAt: spill.CreatedAt, ID: spill.ID})
	if err != nil {
		return nil, Pagination{}, err
	}
	p.NextCursor = &c
	return rows[:limit], p, nil
}

// nullTimePtr returns nil for the zero time (so pgx writes SQL NULL).
func nullTimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// nullUUIDPtr returns nil for the zero UUID (so pgx writes SQL NULL).
func nullUUIDPtr(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// nullTime returns nil for the zero time (so pgx writes SQL NULL in hand-written queries).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// nullUUID returns nil for the zero UUID (so pgx writes SQL NULL in hand-written queries).
func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func errorsIsNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
