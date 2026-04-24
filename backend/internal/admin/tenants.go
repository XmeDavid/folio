package admin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

type tenantCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type TenantDetail struct {
	Tenant         identity.Tenant `json:"tenant"`
	MemberCount    int             `json:"memberCount"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
	LastActivityAt *time.Time      `json:"lastActivityAt,omitempty"`
}

func (s *Service) ListTenants(ctx context.Context, filter TenantListFilter) ([]identity.Tenant, Pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	var cur tenantCursor
	if filter.Cursor != "" {
		if err := decodeCursor(filter.Cursor, &cur); err != nil {
			return nil, Pagination{}, httpx.NewValidationError("invalid cursor")
		}
	}
	search := strings.TrimSpace(filter.Search)
	like := "%" + search + "%"
	rows, err := s.pool.Query(ctx, `
		select id, name, slug::text, base_currency::text, cycle_anchor_day, locale, timezone, deleted_at, created_at
		from tenants
		where ($1::bool or deleted_at is null)
		  and ($2::text = '' or name ilike $3 or slug::text ilike $3 or id::text ilike $3)
		  and ($4::timestamptz is null or (created_at, id) < ($4, $5))
		order by created_at desc, id desc
		limit $6
	`, filter.IncludeDeleted, search, like, nullTime(cur.CreatedAt), nullUUID(cur.ID), filter.Limit+1)
	if err != nil {
		return nil, Pagination{}, err
	}
	defer rows.Close()

	out := make([]identity.Tenant, 0, filter.Limit)
	for rows.Next() {
		var t identity.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.BaseCurrency, &t.CycleAnchorDay, &t.Locale, &t.Timezone, &t.DeletedAt, &t.CreatedAt); err != nil {
			return nil, Pagination{}, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, Pagination{}, err
	}
	return pageTenants(out, filter.Limit)
}

func (s *Service) TenantDetail(ctx context.Context, tenantID uuid.UUID, actorUserID uuid.UUID) (TenantDetail, error) {
	var d TenantDetail
	err := s.pool.QueryRow(ctx, `
		select id, name, slug::text, base_currency::text, cycle_anchor_day, locale, timezone, deleted_at, created_at
		from tenants where id = $1
	`, tenantID).Scan(&d.Tenant.ID, &d.Tenant.Name, &d.Tenant.Slug, &d.Tenant.BaseCurrency, &d.Tenant.CycleAnchorDay, &d.Tenant.Locale, &d.Tenant.Timezone, &d.Tenant.DeletedAt, &d.Tenant.CreatedAt)
	if errorsIsNoRows(err) {
		return d, httpx.NewNotFoundError("tenant")
	}
	if err != nil {
		return d, err
	}
	d.DeletedAt = d.Tenant.DeletedAt
	if err := s.pool.QueryRow(ctx, `select count(*) from tenant_memberships where tenant_id = $1`, tenantID).Scan(&d.MemberCount); err != nil {
		return d, err
	}
	if err := s.pool.QueryRow(ctx, `select max(occurred_at) from audit_events where tenant_id = $1`, tenantID).Scan(&d.LastActivityAt); err != nil {
		return d, err
	}
	if err := s.writeAdminAuditRow(ctx, "admin.viewed_tenant", actorUserID, "tenant", tenantID, nil, nil); err != nil {
		slog.Default().Warn("admin.audit_write_failed", "op", "admin.viewed_tenant", "err", err)
	}
	return d, nil
}

func pageTenants(rows []identity.Tenant, limit int) ([]identity.Tenant, Pagination, error) {
	p := Pagination{Limit: limit}
	if len(rows) <= limit {
		return rows, p, nil
	}
	spill := rows[limit]
	c, err := encodeCursor(tenantCursor{CreatedAt: spill.CreatedAt, ID: spill.ID})
	if err != nil {
		return nil, Pagination{}, err
	}
	p.NextCursor = &c
	return rows[:limit], p, nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func errorsIsNoRows(err error) bool {
	return err == pgx.ErrNoRows
}
