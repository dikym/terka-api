package auth_test

import (
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/auth"
)

var start = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func TestLimiterAllowsUpToTheHourlyLimit(t *testing.T) {
	l := auth.NewLimiter(10)
	for i := range 10 {
		if !l.Allow("203.0.113.7", start) {
			t.Fatalf("request %d refused, want the first 10 allowed", i+1)
		}
	}
	if l.Allow("203.0.113.7", start) {
		t.Fatal("request 11 allowed within the same hour")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l := auth.NewLimiter(10)
	for range 10 {
		l.Allow("203.0.113.7", start)
	}
	if l.Allow("203.0.113.7", start.Add(5*time.Minute)) {
		t.Fatal("allowed after 5 minutes, want one request back only after 6")
	}
	if !l.Allow("203.0.113.7", start.Add(6*time.Minute)) {
		t.Fatal("refused after 6 minutes, want one request back")
	}
	for range 10 {
		l.Allow("203.0.113.7", start.Add(6*time.Minute))
	}
	later := start.Add(3 * time.Hour)
	for i := range 10 {
		if !l.Allow("203.0.113.7", later) {
			t.Fatalf("after 3 hours request %d refused, want a full hour's worth", i+1)
		}
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := auth.NewLimiter(1)
	if !l.Allow("203.0.113.7", start) || l.Allow("203.0.113.7", start) {
		t.Fatal("first key: want one allowed then refused")
	}
	if !l.Allow("198.51.100.1", start) {
		t.Fatal("second key refused because of the first")
	}
}

func TestIPKeyGroupsIPv6ByItsSlash64(t *testing.T) {
	same := auth.IPKey("2001:db8:1:2:aaaa::1")
	if auth.IPKey("2001:db8:1:2:bbbb:cccc:dddd:9") != same {
		t.Fatal("two addresses in one /64 got different keys")
	}
	if auth.IPKey("2001:db8:1:3::1") == same {
		t.Fatal("two /64 networks share a key")
	}
	for in, want := range map[string]string{
		"203.0.113.7":        "203.0.113.7",
		"::ffff:203.0.113.7": "203.0.113.7",
		"not an address":     "not an address",
	} {
		if got := auth.IPKey(in); got != want {
			t.Errorf("IPKey(%q) = %q, want %q", in, got, want)
		}
	}
}
