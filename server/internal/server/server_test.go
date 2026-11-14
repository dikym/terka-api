package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/server"
)

func deps(env string, ping func(context.Context) error) server.Deps {
	return server.Deps{
		Version: "abc",
		Env:     env,
		Ping:    ping,
		Logger:  slog.New(slog.DiscardHandler),
	}
}

func down(context.Context) error { return errors.New("connection refused") }
func up(context.Context) error   { return nil }

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func body(rec *httptest.ResponseRecorder) string { return strings.TrimSpace(rec.Body.String()) }

func TestHealthzIgnoresDatabase(t *testing.T) {
	rec := get(server.New(deps("local", down)), "/healthz")
	if rec.Code != 200 || body(rec) != `{"status":"ok","version":"abc"}` {
		t.Fatalf("got %d %s", rec.Code, body(rec))
	}
}

func TestReadyzReportsDatabase(t *testing.T) {
	rec := get(server.New(deps("local", down)), "/readyz")
	if rec.Code != 503 || !strings.Contains(body(rec), `"code":"not_ready"`) {
		t.Fatalf("database down: got %d %s", rec.Code, body(rec))
	}
	if strings.Contains(body(rec), "connection refused") {
		t.Fatalf("readyz leaks the database error: %s", body(rec))
	}
	rec = get(server.New(deps("local", up)), "/readyz")
	if rec.Code != 200 {
		t.Fatalf("database up: got %d %s", rec.Code, body(rec))
	}
}

func TestReadyzBoundsThePing(t *testing.T) {
	var remaining time.Duration
	var bounded bool
	get(server.New(deps("local", func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		bounded, remaining = ok, time.Until(deadline)
		return nil
	})), "/readyz")
	if !bounded || remaining > 2*time.Second {
		t.Fatalf("ping deadline: set=%v remaining=%v, want at most 2s", bounded, remaining)
	}
}

func TestUnknownAPIPathIsJSON(t *testing.T) {
	rec := get(server.New(deps("local", up)), "/api/nope")
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(body(rec), `"code":"not_found"`) {
		t.Fatalf("body = %s", body(rec))
	}
}

func TestEveryOtherPathIsJSONNotFound(t *testing.T) {
	for _, path := range []string{"/", "/sesi", "/api", "/sw.js"} {
		rec := get(server.New(deps("local", up)), path)
		if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" ||
			!strings.Contains(body(rec), `"code":"not_found"`) {
			t.Errorf("%s: got %d %q %s", path, rec.Code, rec.Header().Get("Content-Type"), body(rec))
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/healthz", "/api/nope", "/sesi"} {
		h := get(server.New(deps("production", up)), path).Header()
		want := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
			"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
		}
		for key, value := range want {
			if h.Get(key) != value {
				t.Errorf("%s: %s = %q, want %q", path, key, h.Get(key), value)
			}
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s: CSP = %q", path, h.Get("Content-Security-Policy"))
		}
	}
}

func TestNonProductionIsNotIndexed(t *testing.T) {
	if got := get(server.New(deps("staging", up)), "/").Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("staging: X-Robots-Tag = %q, want noindex", got)
	}
	if got := get(server.New(deps("production", up)), "/").Header().Get("X-Robots-Tag"); got != "" {
		t.Fatalf("production: X-Robots-Tag = %q, want none", got)
	}
}

func TestHSTSOutsideLocal(t *testing.T) {
	for env, want := range map[string]string{
		"local":      "",
		"staging":    "max-age=31536000",
		"production": "max-age=31536000",
	} {
		if got := get(server.New(deps(env, up)), "/").Header().Get("Strict-Transport-Security"); got != want {
			t.Errorf("%s: Strict-Transport-Security = %q, want %q", env, got, want)
		}
	}
}

func TestCrossOriginWritesAreRefused(t *testing.T) {
	d := deps("local", up)
	d.Routes = func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/x", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	h := server.New(d)
	for site, wantRefused := range map[string]bool{"cross-site": true, "same-origin": false} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
		r.Header.Set("Sec-Fetch-Site", site)
		h.ServeHTTP(rec, r)
		if refused := rec.Code == http.StatusForbidden; refused != wantRefused {
			t.Errorf("Sec-Fetch-Site %s: got %d, refused = %v, want %v", site, rec.Code, refused, wantRefused)
		}
	}
}

func TestRoutesAreMounted(t *testing.T) {
	d := deps("local", up)
	d.Routes = func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) })
	}
	if rec := get(server.New(d), "/api/hello"); rec.Code != 200 || body(rec) != "hi" {
		t.Fatalf("got %d %s", rec.Code, body(rec))
	}
}
