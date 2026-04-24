package admin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

type userCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type MembershipSummary struct {
	TenantID   uuid.UUID `json:"tenantId"`
	TenantName string    `json:"tenantName"`
	TenantSlug string    `json:"tenantSlug"`
	Role       string    `json:"role"`
	JoinedAt   time.Time `json:"joinedAt"`
}

type SessionSummary struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	UserAgent  string    `json:"userAgent"`
	IP         *string   `json:"ip,omitempty"`
}

type PasskeySummary struct {
	ID        uuid.UUID `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

type MFASummary struct {
	Passkeys               []PasskeySummary `json:"passkeys"`
	TOTPEnabled            bool             `json:"totpEnabled"`
	RecoveryCodesRemaining int              `json:"recoveryCodesRemaining"`
}

type UserDetail struct {
	User           identity.User       `json:"user"`
	Memberships    []MembershipSummary `json:"memberships"`
	ActiveSessions []SessionSummary    `json:"activeSessions"`
	MFA            MFASummary          `json:"mfa"`
	LastLoginAt    *time.Time          `json:"lastLoginAt,omitempty"`
}

func (s *Service) ListUsers(ctx context.Context, filter UserListFilter) ([]identity.User, Pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	var cur userCursor
	if filter.Cursor != "" {
		if err := decodeCursor(filter.Cursor, &cur); err != nil {
			return nil, Pagination{}, httpx.NewValidationError("invalid cursor")
		}
	}
	search := strings.TrimSpace(filter.Search)
	rows, err := s.pool.Query(ctx, `
		select id, email::text, display_name, email_verified_at, is_admin, last_tenant_id, created_at
		from users
		where ($1::text = '' or email::text ilike $2 or display_name ilike $2 or id::text ilike $2)
		  and (not $3::bool or is_admin = true)
		  and ($4::timestamptz is null or (created_at, id) < ($4, $5))
		order by created_at desc, id desc
		limit $6
	`, search, "%"+search+"%", filter.IsAdminOnly, nullTime(cur.CreatedAt), nullUUID(cur.ID), filter.Limit+1)
	if err != nil {
		return nil, Pagination{}, err
	}
	defer rows.Close()
	users := make([]identity.User, 0, filter.Limit)
	for rows.Next() {
		var u identity.User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.EmailVerifiedAt, &u.IsAdmin, &u.LastTenantID, &u.CreatedAt); err != nil {
			return nil, Pagination{}, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, Pagination{}, err
	}
	return pageUsers(users, filter.Limit)
}

func (s *Service) UserDetail(ctx context.Context, userID uuid.UUID, actorUserID uuid.UUID) (UserDetail, error) {
	var d UserDetail
	err := s.pool.QueryRow(ctx, `
		select id, email::text, display_name, email_verified_at, is_admin, last_tenant_id, created_at, last_login_at
		from users where id = $1
	`, userID).Scan(&d.User.ID, &d.User.Email, &d.User.DisplayName, &d.User.EmailVerifiedAt, &d.User.IsAdmin, &d.User.LastTenantID, &d.User.CreatedAt, &d.LastLoginAt)
	if errorsIsNoRows(err) {
		return d, httpx.NewNotFoundError("user")
	}
	if err != nil {
		return d, err
	}
	if err := s.loadUserDetailChildren(ctx, userID, &d); err != nil {
		return d, err
	}
	if err := s.writeAdminAuditRow(ctx, "admin.viewed_user", actorUserID, "user", userID, nil, nil); err != nil {
		slog.Default().Warn("admin.audit_write_failed", "op", "admin.viewed_user", "err", err)
	}
	return d, nil
}

func (s *Service) loadUserDetailChildren(ctx context.Context, userID uuid.UUID, d *UserDetail) error {
	rows, err := s.pool.Query(ctx, `
		select t.id, t.name, t.slug::text, tm.role::text, tm.created_at
		from tenant_memberships tm join tenants t on t.id = tm.tenant_id
		where tm.user_id = $1 order by tm.created_at desc
	`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m MembershipSummary
		if err := rows.Scan(&m.TenantID, &m.TenantName, &m.TenantSlug, &m.Role, &m.JoinedAt); err != nil {
			return err
		}
		d.Memberships = append(d.Memberships, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	srows, err := s.pool.Query(ctx, `
		select id, created_at, last_seen_at, coalesce(user_agent, ''), host(ip)
		from sessions where user_id = $1 and expires_at > now()
		order by last_seen_at desc
	`, userID)
	if err != nil {
		return err
	}
	defer srows.Close()
	for srows.Next() {
		var sess SessionSummary
		if err := srows.Scan(&sess.ID, &sess.CreatedAt, &sess.LastSeenAt, &sess.UserAgent, &sess.IP); err != nil {
			return err
		}
		d.ActiveSessions = append(d.ActiveSessions, sess)
	}
	if err := srows.Err(); err != nil {
		return err
	}
	prows, err := s.pool.Query(ctx, `select id, coalesce(label, ''), created_at from webauthn_credentials where user_id = $1 order by created_at desc`, userID)
	if err != nil {
		return err
	}
	defer prows.Close()
	for prows.Next() {
		var p PasskeySummary
		if err := prows.Scan(&p.ID, &p.Label, &p.CreatedAt); err != nil {
			return err
		}
		d.MFA.Passkeys = append(d.MFA.Passkeys, p)
	}
	if err := prows.Err(); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from totp_credentials where user_id = $1 and verified_at is not null)`, userID).Scan(&d.MFA.TOTPEnabled); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `select count(*) from auth_recovery_codes where user_id = $1 and consumed_at is null`, userID).Scan(&d.MFA.RecoveryCodesRemaining)
}

func pageUsers(rows []identity.User, limit int) ([]identity.User, Pagination, error) {
	p := Pagination{Limit: limit}
	if len(rows) <= limit {
		return rows, p, nil
	}
	spill := rows[limit]
	c, err := encodeCursor(userCursor{CreatedAt: spill.CreatedAt, ID: spill.ID})
	if err != nil {
		return nil, Pagination{}, err
	}
	p.NextCursor = &c
	return rows[:limit], p, nil
}
