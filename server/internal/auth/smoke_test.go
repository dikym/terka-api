package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/db"
)

const smokeSessions = `select count(*) from auth.sessions join auth.users on users.id = sessions.user_id
	where users.email = $1`

func countSmokeSessions(t *testing.T, a *db.Auth) int {
	t.Helper()
	var n int
	query(t, a, smokeSessions, []any{auth.SmokeEmail}, &n)
	return n
}

func TestSmokePassesAgainstTheServer(t *testing.T) {
	st := newStack(t, false)
	srv := httptest.NewServer(st.handler)
	defer srv.Close()
	if err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if n := countSmokeSessions(t, st.auth); n != 0 {
		t.Fatalf("%d smoke sessions left, want 0", n)
	}
}

func TestSmokeRunTwiceKeepsOneUser(t *testing.T) {
	st := newStack(t, false)
	srv := httptest.NewServer(st.handler)
	defer srv.Close()
	for range 2 {
		if err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client()); err != nil {
			t.Fatal(err)
		}
	}
	var users int
	query(t, st.auth, "select count(*) from auth.users where email = $1", []any{auth.SmokeEmail}, &users)
	if users != 1 {
		t.Fatalf("%d smoke users, want 1", users)
	}
}

func TestSmokeSessionLivesFiveMinutes(t *testing.T) {
	st := newStack(t, false)
	var (
		mu             sync.Mutex
		idle, absolute float64
		method         string
		seen           bool
		lookupErr      error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("__Host-terka_session"); err == nil {
			mu.Lock()
			seen = true
			lookupErr = st.auth.AsAuth(r.Context(), func(tx pgx.Tx) error {
				return tx.QueryRow(r.Context(), `select extract(epoch from sessions.expires_at - sessions.created_at),
					extract(epoch from sessions.absolute_expires_at - sessions.created_at), sessions.method
					from auth.sessions join auth.users on users.id = sessions.user_id where users.email = $1`,
					auth.SmokeEmail).Scan(&idle, &absolute, &method)
			})
			mu.Unlock()
		}
		st.handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	if err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen || lookupErr != nil {
		t.Fatalf("session seen %v, lookup %v", seen, lookupErr)
	}
	if idle != 300 || absolute != 300 || method != "email" {
		t.Fatalf("idle %vs, absolute %vs, method %q; want 300s, 300s, email", idle, absolute, method)
	}
}

func TestSmokeFailsWhenTheServerDoesNotKnowTheSession(t *testing.T) {
	st, other := newStack(t, false), newStack(t, false)
	srv := httptest.NewServer(other.handler)
	defer srv.Close()
	err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want one naming 401", err)
	}
	if n := countSmokeSessions(t, st.auth); n != 0 {
		t.Fatalf("%d smoke sessions left after a failure, want 0", n)
	}
}

func TestSmokeFailsWhenNothingListens(t *testing.T) {
	st := newStack(t, false)
	if err := auth.Smoke(context.Background(), st.sessions, "http://127.0.0.1:1", http.DefaultClient); err == nil {
		t.Fatal("err = nil, want a refused connection")
	}
	if n := countSmokeSessions(t, st.auth); n != 0 {
		t.Fatalf("%d smoke sessions left after a failure, want 0", n)
	}
}

func TestSmokeFailsWhenTheSessionCannotBeRevoked(t *testing.T) {
	st := newStack(t, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The signed-out request comes last; without a pool, only the revoke after it can fail.
		if _, err := r.Cookie("__Host-terka_session"); err != nil {
			st.auth.Close()
		}
		st.handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "revoke") {
		t.Fatalf("err = %v, want one naming the revoke", err)
	}
}

func TestSmokeRevokesEvenWhenItRunsOutOfTime(t *testing.T) {
	st := newStack(t, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := auth.Smoke(ctx, st.sessions, srv.URL, srv.Client()); err == nil {
		t.Fatal("err = nil, want the deadline")
	}
	if n := countSmokeSessions(t, st.auth); n != 0 {
		t.Fatalf("%d smoke sessions left after the deadline, want 0", n)
	}
}
