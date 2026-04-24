package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenBucket_allowsUnderBudget(t *testing.T) {
	b := newTokenBucket(3, time.Hour)
	for i := 0; i < 3; i++ {
		if !b.take("k") {
			t.Fatalf("take %d should succeed", i)
		}
	}
	if b.take("k") {
		t.Fatalf("4th take on same key should fail")
	}
	if !b.take("other") {
		t.Fatalf("different key should succeed")
	}
}

func TestTokenBucket_refills(t *testing.T) {
	b := newTokenBucket(1, 10*time.Millisecond)
	b.take("k")
	if b.take("k") {
		t.Fatalf("second take should fail immediately")
	}
	time.Sleep(12 * time.Millisecond)
	if !b.take("k") {
		t.Fatalf("after refill, take should succeed")
	}
}

func TestRateLimitByIP(t *testing.T) {
	mw := RateLimitByIP(2, time.Hour)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("call %d code = %d", i, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd call code = %d, want 429", rec.Code)
	}
}

func TestIpFromRequest(t *testing.T) {
	cases := []struct{ name, xff, remote, want string }{
		{"xff first entry", "1.2.3.4, 5.6.7.8", "10.0.0.1:5000", "1.2.3.4"},
		{"xff single", "1.2.3.4", "10.0.0.1:5000", "1.2.3.4"},
		{"remote fallback", "", "10.0.0.1:5000", "10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			req.RemoteAddr = tc.remote
			if got := ipFromRequest(req); got != tc.want {
				t.Errorf("ipFromRequest() = %q, want %q", got, tc.want)
			}
		})
	}
}
