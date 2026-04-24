package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCSRF_safeMethods(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("GET should pass through; got %d", rec.Code)
	}
}

func TestCSRF_rejectsBadOrigin(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("X-Folio-Request", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestCSRF_requiresHeader(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not run")
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestCSRF_allows(t *testing.T) {
	h := CSRF([]string{"http://localhost:3000"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-Folio-Request", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("code = %d, want 204", rec.Code)
	}
}
