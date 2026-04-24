package auth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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
	cases := []struct{ name, remote, want string }{
		{"remote with port", "10.0.0.1:5000", "10.0.0.1"},
		{"remote without port", "10.0.0.1", "10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remote
			if got := ipFromRequest(req); got != tc.want {
				t.Errorf("ipFromRequest() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTokenBucket_lazyEviction(t *testing.T) {
	b := newTokenBucket(1, 10*time.Millisecond)
	base := time.Now()
	// Seed >1024 expired entries directly (bypass take() to avoid triggering
	// the sweep during setup).
	for i := 0; i < 2000; i++ {
		b.counters[strconv.Itoa(i)] = &bucketCount{count: 1, resetAt: base.Add(10 * time.Millisecond)}
	}
	before := len(b.counters)
	if before <= 1024 {
		t.Fatalf("expected >1024 seeded entries, got %d", before)
	}
	// Advance the bucket's clock past all resetAt values so entries are expired.
	b.now = func() time.Time { return base.Add(time.Hour) }
	// Any take() call should trigger the sweep.
	b.take("trigger")
	after := len(b.counters)
	// Post-sweep: nearly everything should be gone; only the "trigger" entry
	// should remain (and maybe a couple of racers). Accept any significant drop.
	if after >= before/2 {
		t.Fatalf("expected sweep to evict most expired entries; before=%d after=%d", before, after)
	}
}
