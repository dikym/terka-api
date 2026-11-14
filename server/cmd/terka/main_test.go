package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/dbtest"
)

func TestMigrateCommandRequiresItsURL(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"migrate"}, env(nil), io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "TERKA_MIGRATE_DATABASE_URL") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestMigrateCommandAppliesTheEmbeddedSchemaOnce(t *testing.T) {
	db := dbtest.New(t)
	vars := env(map[string]string{"TERKA_MIGRATE_DATABASE_URL": db.OwnerURL})
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, vars, &stdout, &stderr); code != 0 {
		t.Fatalf("first run: code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "applied 00000000000000_platform") {
		t.Fatalf("first run did not report the platform migration: %s", stdout.String())
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"migrate"}, vars, &stdout, &stderr); code != 0 {
		t.Fatalf("second run: code = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "applied ") {
		t.Fatalf("second run applied something again: %s", stdout.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func startServe(t *testing.T, vars map[string]string) (string, context.CancelFunc, <-chan int) {
	t.Helper()
	vars["TERKA_ADDR"] = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	logs := &syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve"}, env(vars), io.Discard, logs) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(logs.String(), "\n") {
			var entry struct{ Msg, Addr string }
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "listening" {
				return "http://" + entry.Addr, cancel, done
			}
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited early with %d: %s", code, logs.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("serve never reported its address: %s", logs.String())
	return "", nil, nil
}

func status(t *testing.T, url string) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func TestServeStopsWhenContextEnds(t *testing.T) {
	base, stop, done := startServe(t, map[string]string{"TERKA_ENV": "local"})
	if got := status(t, base+"/healthz"); got != 200 {
		t.Fatalf("healthz = %d, want 200", got)
	}
	stop()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop within 5s")
	}
}

func TestServeStartsWithoutDatabase(t *testing.T) {
	base, _, _ := startServe(t, map[string]string{
		"TERKA_ENV":                  "staging",
		"TERKA_DATABASE_URL":         "postgres://u:p@127.0.0.1:1/terka",
		"TERKA_AUTH_DATABASE_URL":    "postgres://u:p@127.0.0.1:1/terka",
		"TERKA_PUBLIC_URL":           "https://staging.example.test",
		"TERKA_AUTH_KEY":             base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"TERKA_GOOGLE_CLIENT_ID":     "id",
		"TERKA_GOOGLE_CLIENT_SECRET": "secret",
		"TERKA_TURNSTILE_SECRET":     "secret",
		"TERKA_TURNSTILE_HOSTNAMES":  "staging.example.test",
		"TERKA_SMTP_URL":             "smtp+starttls://u:p@127.0.0.1:1",
		"TERKA_MAIL_FROM":            "Terka <no-reply@example.test>",
	})
	if got := status(t, base+"/healthz"); got != 200 {
		t.Fatalf("healthz = %d, want 200", got)
	}
	if got := status(t, base+"/readyz"); got != 503 {
		t.Fatalf("readyz = %d, want 503", got)
	}
}

func TestServeRequiresDatabaseURLOutsideLocal(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"serve"}, env(map[string]string{"TERKA_ENV": "staging"}), io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "TERKA_DATABASE_URL") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"nope"}, env(nil), io.Discard, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	for _, name := range []string{"serve", "health", "version"} {
		if !strings.Contains(stderr.String(), name) {
			t.Fatalf("usage does not list %q: %s", name, stderr.String())
		}
	}
}

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	var stdout bytes.Buffer
	code := run(context.Background(), []string{"version"}, env(nil), &stdout, io.Discard)
	if code != 0 || strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("code = %d, stdout = %q, want %q", code, stdout.String(), version)
	}
}

func TestHealthFollowsReadyz(t *testing.T) {
	for ready, want := range map[int]int{200: 0, 503: 1} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/readyz" {
				t.Errorf("health asked for %s, want /readyz", r.URL.Path)
			}
			w.WriteHeader(ready)
		}))
		_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		code := run(context.Background(), []string{"health"}, env(map[string]string{"TERKA_ADDR": ":" + port}), io.Discard, io.Discard)
		srv.Close()
		if code != want {
			t.Errorf("readyz %d: exit code = %d, want %d", ready, code, want)
		}
	}
}

func TestHealthFailsWhenNothingListens(t *testing.T) {
	code := run(context.Background(), []string{"health"}, env(map[string]string{"TERKA_ADDR": "127.0.0.1:1"}), io.Discard, io.Discard)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func migrated(t *testing.T) dbtest.DB {
	t.Helper()
	db := dbtest.New(t)
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, env(map[string]string{"TERKA_MIGRATE_DATABASE_URL": db.OwnerURL}), io.Discard, &stderr); code != 0 {
		t.Fatalf("migrate: %s", stderr.String())
	}
	return db
}

func TestServeMountsAuth(t *testing.T) {
	db := migrated(t)
	base, _, _ := startServe(t, map[string]string{
		"TERKA_ENV": "local", "TERKA_DATABASE_URL": db.AppURL, "TERKA_AUTH_DATABASE_URL": db.AuthURL,
	})
	res, err := http.Get(base + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 401 || !strings.Contains(string(body), `"code":"signed_out"`) {
		t.Fatalf("session = %d %s, want 401 signed_out", res.StatusCode, body)
	}
	if got := status(t, base+"/readyz"); got != 200 {
		t.Fatalf("readyz = %d, want 200 with both pools up", got)
	}
}

func TestReadyzChecksBothPools(t *testing.T) {
	db := migrated(t)
	base, _, _ := startServe(t, map[string]string{
		"TERKA_ENV": "local", "TERKA_DATABASE_URL": db.AppURL, "TERKA_AUTH_DATABASE_URL": "postgres://u:p@127.0.0.1:1/terka",
	})
	if got := status(t, base+"/readyz"); got != 503 {
		t.Fatalf("readyz = %d, want 503 while the auth pool is down", got)
	}
}

func TestCleanupRunsWhileServing(t *testing.T) {
	cleanupEvery = 20 * time.Millisecond
	t.Cleanup(func() { cleanupEvery = time.Hour })
	db := migrated(t)
	ctx := context.Background()
	owner, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()
	_, err = owner.Exec(ctx, `with u as (insert into auth.users (email) values ('ani@contoh.id') returning id)
		insert into auth.sessions (user_id, token_hash, expires_at, absolute_expires_at, method)
		select id, '\x01', now() - interval '1 day', now() + interval '1 day', 'email' from u`)
	if err != nil {
		t.Fatal(err)
	}
	startServe(t, map[string]string{"TERKA_ENV": "local", "TERKA_DATABASE_URL": db.AppURL, "TERKA_AUTH_DATABASE_URL": db.AuthURL})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var left int
		if err := owner.QueryRow(ctx, "select count(*) from auth.sessions").Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the expired session was never cleaned up")
}
