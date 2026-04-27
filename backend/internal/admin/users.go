package admin

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/identity"
)

type userCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

type MembershipSummary struct {
	WorkspaceID   uuid.UUID `json:"workspaceId"`
	WorkspaceName string    `json:"workspaceName"`
	WorkspaceSlug string    `json:"workspaceSlug"`
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
	rows, err := dbq.New(s.pool).AdminListUsers(ctx, dbq.AdminListUsersParams{
		Search:          search,
		SearchPattern:   "%" + search + "%",
		IsAdminOnly:     filter.IsAdminOnly,
		CursorCreatedAt: nullTimePtr(cur.CreatedAt),
		CursorID:        nullUUIDPtr(cur.ID),
		QueryLimit:      int32(filter.Limit + 1),
	})
	if err != nil {
		return nil, Pagination{}, err
	}
	users := make([]identity.User, 0, filter.Limit)
	for _, r := range rows {
		users = append(users, identity.User{
			ID:              r.ID,
			Email:           r.Email,
			DisplayName:     r.DisplayName,
			EmailVerifiedAt: r.EmailVerifiedAt,
			IsAdmin:         r.IsAdmin,
			LastWorkspaceID: r.LastWorkspaceID,
			CreatedAt:       r.CreatedAt,
		})
	}
	return pageUsers(users, filter.Limit)
}

func (s *Service) UserDetail(ctx context.Context, userID uuid.UUID, actorUserID uuid.UUID) (UserDetail, error) {
	var d UserDetail
	q := dbq.New(s.pool)
	row, err := q.AdminGetUserDetail(ctx, userID)
	if errorsIsNoRows(err) {
		return d, httpx.NewNotFoundError("user")
	}
	if err != nil {
		return d, err
	}
	d.User = identity.User{
		ID:              row.ID,
		Email:           row.Email,
		DisplayName:     row.DisplayName,
		EmailVerifiedAt: row.EmailVerifiedAt,
		IsAdmin:         row.IsAdmin,
		LastWorkspaceID: row.LastWorkspaceID,
		CreatedAt:       row.CreatedAt,
	}
	d.LastLoginAt = row.LastLoginAt
	if err := s.loadUserDetailChildren(ctx, q, userID, &d); err != nil {
		return d, err
	}
	if err := s.writeAdminAuditRow(ctx, "admin.viewed_user", actorUserID, "user", userID, nil, nil); err != nil {
		slog.Default().Warn("admin.audit_write_failed", "op", "admin.viewed_user", "err", err)
	}
	return d, nil
}

func (s *Service) loadUserDetailChildren(ctx context.Context, q *dbq.Queries, userID uuid.UUID, d *UserDetail) error {
	mRows, err := q.AdminListUserMemberships(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range mRows {
		d.Memberships = append(d.Memberships, MembershipSummary{
			WorkspaceID:   r.WorkspaceID,
			WorkspaceName: r.WorkspaceName,
			WorkspaceSlug: r.WorkspaceSlug,
			Role:          r.Role,
			JoinedAt:      r.JoinedAt,
		})
	}

	sRows, err := q.AdminListUserActiveSessions(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range sRows {
		sess := SessionSummary{
			ID:         r.ID,
			CreatedAt:  r.CreatedAt,
			LastSeenAt: r.LastSeenAt,
			UserAgent:  r.UserAgent,
		}
		if r.Ip != "" {
			v := r.Ip
			sess.IP = &v
		}
		d.ActiveSessions = append(d.ActiveSessions, sess)
	}

	pRows, err := q.AdminListUserPasskeys(ctx, userID)
	if err != nil {
		return err
	}
	for _, r := range pRows {
		d.MFA.Passkeys = append(d.MFA.Passkeys, PasskeySummary{
			ID:        r.ID,
			Label:     r.Label,
			CreatedAt: r.CreatedAt,
		})
	}

	hasTOTP, err := q.AdminUserHasTOTP(ctx, userID)
	if err != nil {
		return err
	}
	d.MFA.TOTPEnabled = hasTOTP

	recoveryCount, err := q.AdminCountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return err
	}
	d.MFA.RecoveryCodesRemaining = int(recoveryCount)
	return nil
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
