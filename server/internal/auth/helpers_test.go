package auth_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/dbtest"
	"github.com/dikym/terka-api/server/internal/migrate"
	"github.com/dikym/terka-api/server/migrations"
)

func pools(t *testing.T) (*db.App, *db.Auth) {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	owner, err := pgx.Connect(ctx, d.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	if _, err := migrate.Apply(ctx, owner, migrations.Files); err != nil {
		t.Fatal(err)
	}
	app, err := db.OpenApp(ctx, d.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	auth, err := db.OpenAuth(ctx, d.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auth.Close)
	return app, auth
}

func createUser(t *testing.T, a *db.Auth, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := a.AsAuth(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			"insert into auth.users (email, email_verified_at) values ($1, now()) returning id", email).Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func query(t *testing.T, a *db.Auth, sql string, args []any, dest ...any) {
	t.Helper()
	err := a.AsAuth(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), sql, args...).Scan(dest...)
	})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}
