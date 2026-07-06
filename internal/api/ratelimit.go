package api

import (
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a simple fixed-window per-key limiter.
type rateLimiter struct {
	mu     sync.Mutex
	m      map[string]*rlEntry
	max    int
	window time.Duration
}

type rlEntry struct {
	count int
	reset int64
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{m: make(map[string]*rlEntry), max: max, window: window}
}

func (rl *rateLimiter) allow(key string) bool {
	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e := rl.m[key]
	if e == nil || now >= e.reset {
		rl.m[key] = &rlEntry{count: 1, reset: now + int64(rl.window.Seconds())}
		return true
	}
	if e.count >= rl.max {
		return false
	}
	e.count++
	return true
}

func (rl *rateLimiter) sweep() {
	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, e := range rl.m {
		if now >= e.reset {
			delete(rl.m, k)
		}
	}
}

// limit is middleware that throttles requests per client IP.
func (a *API) limit(rl *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.allow(clientIP(r)) {
				fail(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
