package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// A key unused this long has refilled completely, so forgetting it changes nothing.
const limiterIdle = 2 * time.Hour

// Limiter is an in-memory token bucket per key, refilling perHour tokens an hour. It lives in one
// process, which is all the single API container needs.
type Limiter struct {
	mu        sync.Mutex
	every     rate.Limit
	burst     int
	keys      map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewLimiter(perHour int) *Limiter {
	return &Limiter{
		every: rate.Every(time.Hour / time.Duration(perHour)),
		burst: perHour,
		keys:  map[string]*bucket{},
	}
}

func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= limiterIdle {
		for k, b := range l.keys {
			if now.Sub(b.lastSeen) >= limiterIdle {
				delete(l.keys, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.keys[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(l.every, l.burst)}
		l.keys[key] = b
	}
	b.lastSeen = now
	return b.limiter.AllowN(now, 1)
}
