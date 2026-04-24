package auth

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

// tokenBucket is a simple fixed-window limiter keyed by string (IP, email).
// Each key gets cap takes per window; the counter resets at window end.
// Safe for concurrent use.
type tokenBucket struct {
	mu       sync.Mutex
	cap      int
	window   time.Duration
	counters map[string]*bucketCount
	now      func() time.Time
}

type bucketCount struct {
	count   int
	resetAt time.Time
}

func newTokenBucket(cap int, window time.Duration) *tokenBucket {
	return &tokenBucket{cap: cap, window: window, counters: map[string]*bucketCount{}, now: time.Now}
}

// take reports whether the key has budget remaining. Returns false when the
// cap is hit; true and increments otherwise.
func (b *tokenBucket) take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()

	// Lazy eviction: when the map has grown past a threshold, sweep once.
	// Cheap amortised cost; prevents unbounded growth from high-cardinality IPs.
	if len(b.counters) > 1024 {
		for k, c := range b.counters {
			if now.After(c.resetAt) {
				delete(b.counters, k)
			}
		}
	}

	c := b.counters[key]
	if c == nil || now.After(c.resetAt) {
		c = &bucketCount{count: 0, resetAt: now.Add(b.window)}
		b.counters[key] = c
	}
	if c.count >= b.cap {
		return false
	}
	c.count++
	return true
}

// ipFromRequest returns the best-effort client IP for the request.
// chimw.RealIP runs globally and has already normalized r.RemoteAddr
// from any trusted X-Forwarded-For. Trust it.
func ipFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimitByIP is a per-IP middleware: cap takes per window, 429 when exceeded.
func RateLimitByIP(cap int, window time.Duration) func(http.Handler) http.Handler {
	b := newTokenBucket(cap, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !b.take(ipFromRequest(r)) {
				httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
