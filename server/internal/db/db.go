// Package db is the only way into Postgres. Every transaction is opened here, and it opens as a
// role: a signed-in user, an anonymous visitor, or the sign-in flow. Nothing else sets a role or the
// caller's claims, so the caller of one request can never stay attached to a pooled connection.
package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Claims identify the caller of one transaction, as auth.uid() and auth.jwt() report them.
type Claims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

// App is the pool of the server's login for users; it may become anon or authenticated only.
type App struct{ pool *pgxpool.Pool }

// Auth is the pool of the server's login for sign-in; it may become terka_auth only.
type Auth struct{ pool *pgxpool.Pool }

func open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	// The pool dials lazily, so a database that is down at start does not stop the server.
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database settings: %w", err)
	}
	// Query parameters stay out of spans: the claims of set_config carry user and session ids.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database settings: %w", err)
	}
	return pool, nil
}

func OpenApp(ctx context.Context, url string) (*App, error) {
	pool, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	return &App{pool: pool}, nil
}

func OpenAuth(ctx context.Context, url string) (*Auth, error) {
	pool, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Auth{pool: pool}, nil
}

func (a *App) Ping(ctx context.Context) error  { return a.pool.Ping(ctx) }
func (a *Auth) Ping(ctx context.Context) error { return a.pool.Ping(ctx) }
func (a *App) Close()                          { a.pool.Close() }
func (a *Auth) Close()                         { a.pool.Close() }

// AsUser runs fn as the given signed-in user, with row level security applying to them.
func (a *App) AsUser(ctx context.Context, c Claims, fn func(pgx.Tx) error) error {
	claims, err := json.Marshal(map[string]string{
		"sub":        c.UserID.String(),
		"session_id": c.SessionID.String(),
		"role":       "authenticated",
	})
	if err != nil {
		return err
	}
	return inRole(ctx, a.pool, "authenticated", string(claims), fn)
}

func (a *App) AsAnon(ctx context.Context, fn func(pgx.Tx) error) error {
	return inRole(ctx, a.pool, "anon", "", fn)
}

// AsAuth runs fn as the sign-in flow, which may read and write the auth tables and nothing else.
func (a *Auth) AsAuth(ctx context.Context, fn func(pgx.Tx) error) error {
	return inRole(ctx, a.pool, "terka_auth", "", fn)
}

// Both settings are local to the transaction, so they end with it whether it commits or not.
func inRole(ctx context.Context, pool *pgxpool.Pool, role, claims string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// The role name is one of three constants above, never input.
		if _, err := tx.Exec(ctx, "set local role "+role); err != nil {
			return fmt.Errorf("set role %s: %w", role, err)
		}
		if _, err := tx.Exec(ctx, "select set_config('request.jwt.claims', $1, true)", claims); err != nil {
			return fmt.Errorf("set claims: %w", err)
		}
		return fn(tx)
	})
}
