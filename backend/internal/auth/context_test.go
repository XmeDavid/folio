package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/xmedavid/folio/backend/internal/identity"
)

func TestContextRoundtrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithUser(ctx, identity.User{ID: uuid.New(), Email: "a@b.com"})
	if u, ok := UserFromCtx(ctx); !ok || u.Email != "a@b.com" {
		t.Fatalf("UserFromCtx: %+v %v", u, ok)
	}
	ctx = WithTenant(ctx, identity.Tenant{ID: uuid.New(), Name: "T"})
	if tn, _ := TenantFromCtx(ctx); tn.Name != "T" {
		t.Fatalf("TenantFromCtx: %+v", tn)
	}
	ctx = WithRole(ctx, identity.RoleOwner)
	if r, _ := RoleFromCtx(ctx); r != identity.RoleOwner {
		t.Fatalf("RoleFromCtx: %v", r)
	}
}
