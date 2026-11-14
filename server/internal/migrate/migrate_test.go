package migrate_test

import (
	"context"
	"errors"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dikym/terka-api/server/internal/dbtest"
	"github.com/dikym/terka-api/server/internal/migrate"
	"github.com/dikym/terka-api/server/migrations"
)

func connect(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func sqlFiles(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func recorded(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), "select version from terka.schema_migrations order by version")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return versions
}

func TestApplyRunsFilesInNameOrder(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	files := sqlFiles(map[string]string{
		"2_b.sql": "create table b (a_id int references a (id));",
		"1_a.sql": "create table a (id int primary key);",
	})
	applied, err := migrate.Apply(context.Background(), conn, files)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(applied, []string{"1_a", "2_b"}) {
		t.Fatalf("applied = %v, want [1_a 2_b]", applied)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	files := sqlFiles(map[string]string{"1_a.sql": "create table a (id int);"})
	if _, err := migrate.Apply(context.Background(), conn, files); err != nil {
		t.Fatal(err)
	}
	applied, err := migrate.Apply(context.Background(), conn, files)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second run: applied = %v, err = %v; want nothing and no error", applied, err)
	}
}

func TestApplyRunsSeveralStatementsInOneFile(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	files := sqlFiles(map[string]string{"1_a.sql": `
		create table a (id int);
		create function a_count() returns bigint language sql as $$ select count(*) from a; $$;
		insert into a values (1), (2);`})
	if _, err := migrate.Apply(context.Background(), conn, files); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := conn.QueryRow(context.Background(), "select a_count()").Scan(&n); err != nil || n != 2 {
		t.Fatalf("a_count() = %d, err = %v; want 2", n, err)
	}
}

func TestApplyRollsBackAFailedFile(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	files := sqlFiles(map[string]string{
		"1_ok.sql":   "create table ok ();",
		"2_bad.sql":  "create table t1 (); select 1/0;",
		"3_late.sql": "create table late ();",
	})
	applied, err := migrate.Apply(context.Background(), conn, files)
	if err == nil || !strings.Contains(err.Error(), "2_bad.sql") {
		t.Fatalf("err = %v, want one naming 2_bad.sql", err)
	}
	if !slices.Equal(applied, []string{"1_ok"}) {
		t.Fatalf("applied = %v, want [1_ok]", applied)
	}
	var t1, late *string
	if err := conn.QueryRow(context.Background(), "select to_regclass('t1')::text, to_regclass('late')::text").Scan(&t1, &late); err != nil {
		t.Fatal(err)
	}
	if t1 != nil || late != nil {
		t.Fatalf("tables after a failed file: t1=%v late=%v, want neither", t1, late)
	}
	if got := recorded(t, conn); !slices.Equal(got, []string{"1_ok"}) {
		t.Fatalf("recorded = %v, want [1_ok]", got)
	}
}

func TestApplyIgnoresUnknownRecordedVersions(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	files := sqlFiles(map[string]string{"1_a.sql": "create table a ();"})
	if _, err := migrate.Apply(context.Background(), conn, files); err != nil {
		t.Fatal(err)
	}
	// What an older image finds after a rollback: a version recorded by a newer one.
	if _, err := conn.Exec(context.Background(), "insert into terka.schema_migrations (version) values ('9_future')"); err != nil {
		t.Fatal(err)
	}
	applied, err := migrate.Apply(context.Background(), conn, files)
	if err != nil || len(applied) != 0 {
		t.Fatalf("applied = %v, err = %v; want nothing and no error", applied, err)
	}
}

func TestApplySerializesConcurrentRuns(t *testing.T) {
	db := dbtest.New(t)
	files := sqlFiles(map[string]string{
		"1_a.sql": "create table a (); select pg_sleep(0.2);",
		"2_b.sql": "create table b ();",
		"3_c.sql": "create table c ();",
	})
	conns := []*pgx.Conn{connect(t, db.OwnerURL), connect(t, db.OwnerURL)}
	errs := make([]error, len(conns))
	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Go(func() { _, errs[i] = migrate.Apply(context.Background(), conn, files) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := recorded(t, conns[0]); !slices.Equal(got, []string{"1_a", "2_b", "3_c"}) {
		t.Fatalf("recorded = %v, want each version exactly once", got)
	}
}

func TestEmbeddedMigrationsApplyAsOwner(t *testing.T) {
	conn := connect(t, dbtest.New(t).OwnerURL)
	if _, err := migrate.Apply(context.Background(), conn, migrations.Files); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"select to_regclass('public.review_logs') is not null":                       "public.review_logs exists",
		"select to_regclass('auth.sessions') is not null":                            "auth.sessions exists",
		"select auth.uid() is null":                                                  "auth.uid() is null outside a request",
		"select count(*) = 0 from pg_namespace where nspname in ('cron','realtime')": "no Supabase-only schemas",
	}
	for query, what := range checks {
		var ok bool
		if err := conn.QueryRow(context.Background(), query).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: got %v, err = %v", what, ok, err)
		}
	}
}

func TestEmbeddedMigrationsCreateNoRoles(t *testing.T) {
	createRole := regexp.MustCompile(`(?i)\b(create|alter|drop)\s+role\b`)
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("embedded migrations: %v %v", names, err)
	}
	for _, name := range names {
		body, err := fs.ReadFile(migrations.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		if createRole.Match(body) {
			t.Errorf("%s manages roles; roles belong to the cluster, not to a migration", name)
		}
	}
}

func TestClaimsDriveAuthUidForOneTransaction(t *testing.T) {
	db := dbtest.New(t)
	if _, err := migrate.Apply(context.Background(), connect(t, db.OwnerURL), migrations.Files); err != nil {
		t.Fatal(err)
	}
	conn := connect(t, db.AppURL)
	ctx := context.Background()
	const user = "0192f0e1-0000-7000-8000-000000000001"
	const session = "0192f0e1-0000-7000-8000-000000000002"

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "set local role authenticated"); err != nil {
		t.Fatal(err)
	}
	claims := `{"sub":"` + user + `","session_id":"` + session + `","role":"authenticated"}`
	if _, err := tx.Exec(ctx, "select set_config('request.jwt.claims', $1, true)", claims); err != nil {
		t.Fatal(err)
	}
	var uid, sid string
	if err := tx.QueryRow(ctx, "select auth.uid()::text, auth.jwt() ->> 'session_id'").Scan(&uid, &sid); err != nil {
		t.Fatal(err)
	}
	if uid != user || sid != session {
		t.Fatalf("inside the transaction: uid=%s session=%s", uid, sid)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The next transaction on the same connection must not inherit the caller.
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "set local role authenticated"); err != nil {
		t.Fatal(err)
	}
	var leaked *string
	if err := tx.QueryRow(ctx, "select auth.uid()::text").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != nil {
		t.Fatalf("auth.uid() in the next transaction = %s, want null", *leaked)
	}
}

func TestAppRoleCannotChangeTheSchema(t *testing.T) {
	db := dbtest.New(t)
	if _, err := migrate.Apply(context.Background(), connect(t, db.OwnerURL), migrations.Files); err != nil {
		t.Fatal(err)
	}
	conn := connect(t, db.AppURL)
	if _, err := conn.Exec(context.Background(), "create table public.x ()"); err == nil {
		t.Fatal("the app role created a table; it must not own or change the schema")
	}
}

func TestAppLoginCannotBecomeTheAuthRole(t *testing.T) {
	db := dbtest.New(t)
	if _, err := migrate.Apply(context.Background(), connect(t, db.OwnerURL), migrations.Files); err != nil {
		t.Fatal(err)
	}
	// SQL smuggled into an app transaction must not be able to switch to the role that writes sessions.
	assertSetRoleDenied(t, connect(t, db.AppURL), "terka_auth")
}

func TestAuthLoginCannotBecomeAUser(t *testing.T) {
	db := dbtest.New(t)
	if _, err := migrate.Apply(context.Background(), connect(t, db.OwnerURL), migrations.Files); err != nil {
		t.Fatal(err)
	}
	assertSetRoleDenied(t, connect(t, db.AuthURL), "authenticated")
	assertSetRoleDenied(t, connect(t, db.AuthURL), "anon")
}

func assertSetRoleDenied(t *testing.T, conn *pgx.Conn, role string) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "set local role "+role)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("set role %s: err = %v, want permission denied (42501)", role, err)
	}
}
