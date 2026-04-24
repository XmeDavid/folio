package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequireTenant_missingHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner handler should not be called")
	})).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	var body ErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "tenant_required" {
		t.Errorf("want code tenant_required, got %q", body.Code)
	}
}

func TestRequireTenant_invalidTenant(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "not-a-uuid")
	RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner should not be called")
	})).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestRequireTenant_setsContext(t *testing.T) {
	want := uuid.MustParse("0192c31f-0000-7000-8000-000000000000")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", want.String())
	called := false
	RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		got, ok := TenantIDFrom(r.Context())
		if !ok {
			t.Fatal("tenantID missing from context")
		}
		if got != want {
			t.Errorf("want %s, got %s", want, got)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rr, req)
	if !called {
		t.Fatal("inner handler not called")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", rr.Code)
	}
}

func TestWriteServiceError(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteServiceError(rr, NewValidationError("bad input"))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rr.Code)
		}
	})
	t.Run("not found", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteServiceError(rr, NewNotFoundError("thing"))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", rr.Code)
		}
	})
}
