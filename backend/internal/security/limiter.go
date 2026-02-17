package security

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type keyLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type Limiter struct {
	mu           sync.Mutex
	entries      map[string]*keyLimiter
	rateLimit    rate.Limit
	burst        int
	ttl          time.Duration
	cleanupEvery time.Duration
	lastCleanup  time.Time
}

func NewLimiter(events int, per time.Duration, burst int, ttl time.Duration) *Limiter {
	if events <= 0 {
		events = 1
	}
	if per <= 0 {
		per = time.Second
	}
	if burst <= 0 {
		burst = events
	}
	if ttl <= 0 {
		ttl = 10 * per
	}

	cleanupEvery := ttl / 2
	if cleanupEvery <= 0 {
		cleanupEvery = per
	}

	return &Limiter{
		entries:      make(map[string]*keyLimiter),
		rateLimit:    rate.Limit(float64(events) / per.Seconds()),
		burst:        burst,
		ttl:          ttl,
		cleanupEvery: cleanupEvery,
		lastCleanup:  time.Now(),
	}
}

func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}

	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		normalizedKey = "global"
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupStale(now)

	entry, ok := l.entries[normalizedKey]
	if !ok {
		entry = &keyLimiter{
			limiter:  rate.NewLimiter(l.rateLimit, l.burst),
			lastSeen: now,
		}
		l.entries[normalizedKey] = entry
	}
	entry.lastSeen = now

	return entry.limiter.Allow()
}

func (l *Limiter) cleanupStale(now time.Time) {
	if now.Sub(l.lastCleanup) < l.cleanupEvery {
		return
	}
	for key, entry := range l.entries {
		if now.Sub(entry.lastSeen) > l.ttl {
			delete(l.entries, key)
		}
	}
	l.lastCleanup = now
}
