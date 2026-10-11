// Package ratelimit is a small in-memory sliding-window rate limiter.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows at most limit events per key within window.
type Limiter struct {
	limit  int
	window time.Duration

	mu    sync.Mutex
	hits  map[string][]time.Time
	sweep time.Time
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, hits: map[string][]time.Time{}, sweep: time.Now()}
}

// Allow records an event for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Forget the keys gone quiet, once per window, so the map does not grow
	// for ever.
	if now.Sub(l.sweep) > l.window {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > l.window {
				delete(l.hits, k)
			}
		}

		l.sweep = now
	}

	ts := l.hits[key]

	cut := 0
	for cut < len(ts) && now.Sub(ts[cut]) > l.window {
		cut++
	}

	ts = ts[cut:]

	if len(ts) >= l.limit {
		l.hits[key] = ts

		return false
	}

	l.hits[key] = append(ts, now)

	return true
}
