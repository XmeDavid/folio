package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmedavid/folio/backend/internal/identity"
)

func TestRequireRole_allows(t *testing.T) {
	mid := RequireRole(identity.RoleOwner)
	called := false
	h := mid(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/", nil).WithContext(WithRole(context.Background(), identity.RoleOwner))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("handler should have run")
	}
}

func TestRequireRole_denies(t *testing.T) {
	mid := RequireRole(identity.RoleOwner)
	h := mid(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("GET", "/", nil).WithContext(WithRole(context.Background(), identity.RoleMember))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}
