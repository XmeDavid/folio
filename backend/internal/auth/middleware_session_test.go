package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSession_missingCookie(t *testing.T) {
	svc := &Service{}
	m := svc.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not run")
	}))
	req := httptest.NewRequest("GET", "/protected", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}
