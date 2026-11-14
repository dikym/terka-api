// Package dbtest gives each test its own throwaway database on the cluster named by
// TERKA_TEST_DATABASE_URL, with the same role layout the servers use.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// DB holds connection strings for one test database: OwnerURL connects as the role that owns the
// schema (not a superuser), AppURL as the server's login for users, AuthURL as its login for sign-in.
type DB struct {
	OwnerURL string
	AppURL   string
	AuthURL  string
}

const ensureGroupRoles = `
do $$
begin
	create role anon nologin;
exception when duplicate_object or unique_violation then null;
end $$;
do $$
begin
	create role authenticated nologin;
exception when duplicate_object or unique_violation then null;
end $$;
do $$
begin
	create role terka_auth nologin;
exception when duplicate_object or unique_violation then null;
end $$;`

// New fails the test, rather than skipping it, when no test cluster is configured: a database
// test that silently skips looks the same as one that passed.
func New(t *testing.T) DB {
	t.Helper()
	admin := testURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Fatalf("dbtest: connect to TERKA_TEST_DATABASE_URL: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	suffix := randomSuffix(t)
	name := "t_" + suffix
	owner, app, auth := name+"_owner", name+"_app", name+"_auth"
	password := randomSuffix(t)

	// Roles are cluster-wide, so every name here is unique to this test and dropped with it.
	statements := []string{
		ensureGroupRoles,
		"create role " + owner + " login password '" + password + "'",
		"create role " + app + " login noinherit password '" + password + "'",
		"grant anon, authenticated to " + app,
		"create role " + auth + " login noinherit password '" + password + "'",
		"grant terka_auth to " + auth,
		"create database " + name + " owner " + owner,
		"revoke connect on database " + name + " from public",
		"grant connect on database " + name + " to " + app + ", " + auth,
	}
	for _, sql := range statements {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatalf("dbtest: %s: %v", sql, err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin.String())
		if err != nil {
			t.Errorf("dbtest cleanup: %v", err)
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		for _, sql := range []string{
			"drop database if exists " + name + " with (force)",
			"drop role if exists " + app,
			"drop role if exists " + auth,
			"drop role if exists " + owner,
		} {
			if _, err := conn.Exec(ctx, sql); err != nil {
				t.Errorf("dbtest cleanup: %s: %v", sql, err)
			}
		}
	})

	return DB{
		OwnerURL: withLogin(admin, owner, password, name),
		AppURL:   withLogin(admin, app, password, name),
		AuthURL:  withLogin(admin, auth, password, name),
	}
}

func testURL(t *testing.T) *url.URL {
	t.Helper()
	raw := os.Getenv("TERKA_TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("dbtest: TERKA_TEST_DATABASE_URL is not set; start the dev database with " +
			"`docker compose -f deploy/compose.dev.yaml up -d --wait postgres` and point it at " +
			"postgres://postgres:postgres@localhost:5433/postgres")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("dbtest: TERKA_TEST_DATABASE_URL: %v", err)
	}
	return parsed
}

func withLogin(base *url.URL, user, password, database string) string {
	next := *base
	next.User = url.UserPassword(user, password)
	next.Path = "/" + database
	return next.String()
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
