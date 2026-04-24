package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "abc123")
	resp := rec.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookieName {
		t.Errorf("name = %q, want %q", c.Name, sessionCookieName)
	}
	if c.Value != "abc123" {
		t.Errorf("value = %q, want abc123", c.Value)
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly")
	}
	if !c.Secure {
		t.Error("expected Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("expected SameSite=Lax")
	}
	if c.Path != "/" {
		t.Error("expected Path=/")
	}
}

func TestClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	ClearSessionCookie(rec)
	h := rec.Header().Get("Set-Cookie")
	if !strings.Contains(h, "Max-Age=0") {
		t.Errorf("expected Max-Age=0 (from MaxAge=-1), got %q", h)
	}
}
