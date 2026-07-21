package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-key sliding-window rate limiter used to slow credential
// stuffing on authentication endpoints. In-memory by design: limits are
// per-instance and advisory, the real defense is argon2id + lockout-free
// constant-time verification.
type Limiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string][]time.Time
	sweep   time.Time
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string][]time.Time),
		sweep:   time.Now(),
	}
}

// Allow records an attempt for key and reports whether it is within limits.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.sweep) > l.window {
		for k, ts := range l.buckets {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}

	ts := l.buckets[key]
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.buckets[key] = kept
		return false
	}
	l.buckets[key] = append(kept, now)
	return true
}
