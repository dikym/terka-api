package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/db"
)

const day = 24 * time.Hour

func sessions(t *testing.T) (*auth.Sessions, *db.Auth, *clock) {
	t.Helper()
	_, a := pools(t)
	c := newClock()
	return &auth.Sessions{Auth: a, Now: c.Now}, a, c
}

func signIn(t *testing.T, s *auth.Sessions, user uuid.UUID, method string) (string, auth.Session) {
	t.Helper()
	var token string
	var sess auth.Session
	err := s.Auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		var err error
		token, sess, err = s.Create(context.Background(), tx, user, method)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return token, sess
}

func TestCreatedSessionCanBeLookedUp(t *testing.T) {
	s, a, _ := sessions(t)
	user := createUser(t, a, "ani@test.terka")
	token, created := signIn(t, s, user, "email")
	got, err := s.Lookup(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	want := auth.Session{ID: created.ID, UserID: user, Email: "ani@test.terka", Method: "email"}
	if got != want || created != want {
		t.Fatalf("lookup = %+v, created = %+v, want %+v", got, created, want)
	}
}

func TestStoredTokenIsAHash(t *testing.T) {
	s, a, _ := sessions(t)
	token, sess := signIn(t, s, createUser(t, a, "ani@test.terka"), "google")
	var stored []byte
	query(t, a, "select token_hash from auth.sessions where id = $1", []any{sess.ID}, &stored)
	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(stored, want[:]) || bytes.Contains(stored, []byte(token)) {
		t.Fatalf("stored %x, want SHA-256 of the token", stored)
	}
}

func TestUnknownTokenIsSignedOut(t *testing.T) {
	s, _, _ := sessions(t)
	for _, token := range []string{"", "x", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := s.Lookup(context.Background(), token); !errors.Is(err, auth.ErrSignedOut) {
			t.Errorf("%q: err = %v, want ErrSignedOut", token, err)
		}
	}
}

func TestIdleSessionExpiresAfter90Days(t *testing.T) {
	s, a, c := sessions(t)
	token, _ := signIn(t, s, createUser(t, a, "ani@test.terka"), "email")
	c.Advance(90 * day)
	if _, err := s.Lookup(context.Background(), token); err != nil {
		t.Fatalf("at exactly 90 idle days: %v", err)
	}
	// That lookup counted as use; idle again for 90 days and a second.
	c.Advance(90*day + time.Second)
	if _, err := s.Lookup(context.Background(), token); !errors.Is(err, auth.ErrSignedOut) {
		t.Fatalf("after 90 idle days: err = %v, want ErrSignedOut", err)
	}
}

func TestUsedSessionStaysAliveButNotPast400Days(t *testing.T) {
	s, a, c := sessions(t)
	token, _ := signIn(t, s, createUser(t, a, "ani@test.terka"), "email")
	for elapsed := 30 * day; elapsed <= 390*day; elapsed += 30 * day {
		c.Advance(30 * day)
		if _, err := s.Lookup(context.Background(), token); err != nil {
			t.Fatalf("used every 30 days, refused at day %d: %v", elapsed/day, err)
		}
	}
	c.Advance(10*day + time.Second) // 400 days and a second since sign-in
	if _, err := s.Lookup(context.Background(), token); !errors.Is(err, auth.ErrSignedOut) {
		t.Fatalf("past 400 days: err = %v, want ErrSignedOut", err)
	}
}

func TestLastUsedIsWrittenAtMostEvery10Minutes(t *testing.T) {
	s, a, c := sessions(t)
	token, sess := signIn(t, s, createUser(t, a, "ani@test.terka"), "email")
	lastUsed := func() time.Time {
		var at time.Time
		query(t, a, "select last_used_at from auth.sessions where id = $1", []any{sess.ID}, &at)
		return at.UTC()
	}
	start := c.Now()
	c.Advance(5 * time.Minute)
	if _, err := s.Lookup(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(); !got.Equal(start) {
		t.Fatalf("after 5 minutes last_used_at = %v, want it unchanged at %v", got, start)
	}
	c.Advance(6 * time.Minute)
	if _, err := s.Lookup(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(); !got.Equal(c.Now()) {
		t.Fatalf("after 11 minutes last_used_at = %v, want %v", got, c.Now())
	}
}

func TestRevokedSessionIsSignedOut(t *testing.T) {
	s, a, _ := sessions(t)
	token, _ := signIn(t, s, createUser(t, a, "ani@test.terka"), "email")
	if err := s.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), token); !errors.Is(err, auth.ErrSignedOut) {
		t.Fatalf("err = %v, want ErrSignedOut", err)
	}
	if err := s.Revoke(context.Background(), token); err != nil {
		t.Fatalf("revoking twice: %v", err)
	}
}

func TestCleanupRemovesOnlyExpiredRows(t *testing.T) {
	s, a, c := sessions(t)
	user := createUser(t, a, "ani@test.terka")
	old, _ := signIn(t, s, user, "email")
	c.Advance(91 * day)
	fresh, _ := signIn(t, s, user, "google")
	removed, err := s.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var left int
	query(t, a, "select count(*) from auth.sessions", nil, &left)
	if removed != 1 || left != 1 {
		t.Fatalf("removed %d, left %d; want 1 and 1", removed, left)
	}
	if _, err := s.Lookup(context.Background(), fresh); err != nil {
		t.Fatalf("the fresh session went: %v", err)
	}
	_ = old
}
