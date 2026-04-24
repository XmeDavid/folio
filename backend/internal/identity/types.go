package identity

import (
	"time"

	"github.com/google/uuid"
)

// Role is the per-tenant membership role. Matches the Postgres `tenant_role` enum.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleMember }

// Tenant is the wire/read-model shape of a tenant row.
type Tenant struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	BaseCurrency   string     `json:"baseCurrency"`
	CycleAnchorDay int        `json:"cycleAnchorDay"`
	Locale         string     `json:"locale"`
	Timezone       string     `json:"timezone"`
	DeletedAt      *time.Time `json:"deletedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// User is the read-model shape of a user row.
type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"displayName"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	IsAdmin         bool       `json:"isAdmin"`
	LastTenantID    *uuid.UUID `json:"lastTenantId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Membership is a (tenant, user, role) triple.
type Membership struct {
	TenantID  uuid.UUID `json:"tenantId"`
	UserID    uuid.UUID `json:"userId"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// MemberWithUser is a Membership enriched with the user's display fields.
// Returned by Service.ListMembers so the tenant members page in Plan 2
// renders email + name in one round-trip.
type MemberWithUser struct {
	Membership
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// TenantWithRole attaches the caller's role on a tenant for the /me response.
type TenantWithRole struct {
	Tenant
	Role Role `json:"role"`
}

// Invite is defined here so plan 2 doesn't have to change the types file.
// Plan 1 leaves it unused.
type Invite struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        uuid.UUID  `json:"tenantId"`
	Email           string     `json:"email"`
	Role            Role       `json:"role"`
	InvitedByUserID uuid.UUID  `json:"invitedByUserId"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	AcceptedAt      *time.Time `json:"acceptedAt,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}
