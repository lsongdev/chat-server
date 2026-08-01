package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

type rateBucket struct {
	started time.Time
	seen    time.Time
	count   int
}

type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
	calls   uint64
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string]rateBucket)}
}

func (l *RateLimiter) Allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls%1024 == 0 {
		for candidate, bucket := range l.buckets {
			if now.Sub(bucket.seen) > 2*window {
				delete(l.buckets, candidate)
			}
		}
	}
	bucket := l.buckets[key]
	if bucket.started.IsZero() || now.Sub(bucket.started) >= window {
		l.buckets[key] = rateBucket{started: now, seen: now, count: 1}
		return true, 0
	}
	bucket.seen = now
	if bucket.count >= limit {
		l.buckets[key] = bucket
		return false, window - now.Sub(bucket.started)
	}
	bucket.count++
	l.buckets[key] = bucket
	return true, 0
}

func (l *RateLimiter) LoginMiddleware(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := l.Allow("login:"+clientIP(r, cfg.TrustProxyHeaders), 20, 10*time.Minute)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
			writeProblem(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) MutationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := currentUser(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "authentication_required", "sign in to continue")
			return
		}
		allowed, retryAfter := l.Allow("mutation:"+user.ID.String(), 300, time.Minute)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
			writeProblem(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
