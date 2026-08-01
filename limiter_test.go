package main

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter()
	if allowed, _ := limiter.Allow("user", 2, time.Minute); !allowed {
		t.Fatal("first request was unexpectedly limited")
	}
	if allowed, _ := limiter.Allow("user", 2, time.Minute); !allowed {
		t.Fatal("second request was unexpectedly limited")
	}
	if allowed, retry := limiter.Allow("user", 2, time.Minute); allowed || retry <= 0 {
		t.Fatal("third request should have been limited")
	}
	if allowed, _ := limiter.Allow("other", 2, time.Minute); !allowed {
		t.Fatal("independent key shared a limit")
	}
}
