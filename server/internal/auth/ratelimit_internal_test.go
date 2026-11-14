package auth

import (
	"testing"
	"time"
)

func TestLimiterForgetsKeysIdleForTwoHours(t *testing.T) {
	l := NewLimiter(10)
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	l.Allow("203.0.113.7", now)
	l.Allow("198.51.100.1", now.Add(time.Hour))
	l.Allow("192.0.2.1", now.Add(2*time.Hour))
	if _, kept := l.keys["203.0.113.7"]; kept || len(l.keys) != 2 {
		t.Fatalf("keys = %v, want only the two seen within two hours", l.keys)
	}
}
