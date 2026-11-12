package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
		"TERKA_ENV":          "staging",
		"TERKA_DATABASE_URL": "postgres://u:p@127.0.0.1:1/terka",
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
