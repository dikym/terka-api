package db_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/dbtest"
	"github.com/dikym/terka-api/server/internal/migrate"
	"github.com/dikym/terka-api/server/migrations"
)

func open(t *testing.T, maxConns int) (*db.App, *db.Auth) {
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
	app, err := db.OpenApp(ctx, fmt.Sprintf("%s?pool_max_conns=%d", d.AppURL, maxConns))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	auth, err := db.OpenAuth(ctx, fmt.Sprintf("%s?pool_max_conns=%d", d.AuthURL, maxConns))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(auth.Close)
	return app, auth
}

func caller(ctx context.Context, tx pgx.Tx) (uid, session *string, err error) {
	err = tx.QueryRow(ctx, "select auth.uid()::text, auth.jwt() ->> 'session_id'").Scan(&uid, &session)
	return uid, session, err
}

func TestAsUserSetsTheCallerForOneTransaction(t *testing.T) {
	app, _ := open(t, 1)
	ctx := context.Background()
	c := db.Claims{UserID: uuid.New(), SessionID: uuid.New()}
	err := app.AsUser(ctx, c, func(tx pgx.Tx) error {
		uid, session, err := caller(ctx, tx)
		if err != nil {
			return err
		}
		if uid == nil || *uid != c.UserID.String() || session == nil || *session != c.SessionID.String() {
			t.Errorf("inside: uid=%v session=%v, want %s %s", uid, session, c.UserID, c.SessionID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// One connection in the pool: the next transaction runs on the same one.
	err = app.AsAnon(ctx, func(tx pgx.Tx) error {
		uid, session, err := caller(ctx, tx)
		if uid != nil || session != nil {
			t.Errorf("next transaction still sees uid=%v session=%v", uid, session)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailedTransactionDoesNotLeakTheCaller(t *testing.T) {
	app, _ := open(t, 1)
	ctx := context.Background()
	boom := errors.New("boom")
	err := app.AsUser(ctx, db.Claims{UserID: uuid.New(), SessionID: uuid.New()}, func(pgx.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the callback's error", err)
	}
	err = app.AsAnon(ctx, func(tx pgx.Tx) error {
		uid, _, err := caller(ctx, tx)
		if uid != nil {
			t.Errorf("after a failed transaction auth.uid() = %s, want null", *uid)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestParallelUsersOnlySeeThemselves(t *testing.T) {
	app, _ := open(t, 4)
	ctx := context.Background()
	users := make([]db.Claims, 10)
	for i := range users {
		users[i] = db.Claims{UserID: uuid.New(), SessionID: uuid.New()}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		c := users[i%len(users)]
		wg.Go(func() {
			errs <- app.AsUser(ctx, c, func(tx pgx.Tx) error {
				uid, _, err := caller(ctx, tx)
				if err != nil {
					return err
				}
				if uid == nil || *uid != c.UserID.String() {
					return fmt.Errorf("user %s saw %v", c.UserID, uid)
				}
				return nil
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestAsAnonHasNoCaller(t *testing.T) {
	app, _ := open(t, 1)
	ctx := context.Background()
	err := app.AsAnon(ctx, func(tx pgx.Tx) error {
		var role string
		if err := tx.QueryRow(ctx, "select current_user").Scan(&role); err != nil {
			return err
		}
		if role != "anon" {
			t.Errorf("current_user = %s, want anon", role)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAsAuthCannotReadUserTablesAsAUser(t *testing.T) {
	_, auth := open(t, 1)
	ctx := context.Background()
	err := auth.AsAuth(ctx, func(tx pgx.Tx) error {
		uid, _, err := caller(ctx, tx)
		if err != nil {
			return err
		}
		if uid != nil {
			t.Errorf("auth.uid() = %s in an auth transaction, want null", *uid)
		}
		_, err = tx.Exec(ctx, "select 1 from public.review_logs")
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("reading review_logs: err = %v, want permission denied", err)
	}
}

func TestAsAuthCanUseTheAuthTables(t *testing.T) {
	_, auth := open(t, 1)
	ctx := context.Background()
	err := auth.AsAuth(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select count(*) from auth.sessions")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPingReachesTheDatabase(t *testing.T) {
	app, auth := open(t, 1)
	if err := app.Ping(context.Background()); err != nil {
		t.Errorf("app ping: %v", err)
	}
	if err := auth.Ping(context.Background()); err != nil {
		t.Errorf("auth ping: %v", err)
	}
}
