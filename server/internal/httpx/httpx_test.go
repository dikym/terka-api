package httpx_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dikym/terka-api/server/internal/httpx"
)

func serve(h httpx.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	httpx.Handle(h, slog.New(slog.DiscardHandler)).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	return rec
}

const internalBody = `{"code":"internal","message":"internal error"}`

func TestHandleWritesErrorEnvelope(t *testing.T) {
	rec := serve(func(http.ResponseWriter, *http.Request) error {
		return &httpx.Error{Status: 409, Code: "TK006", Message: "the session is already closed"}
	})
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"code":"TK006","message":"the session is already closed"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestHandleHidesUnknownErrors(t *testing.T) {
	rec := serve(func(http.ResponseWriter, *http.Request) error {
		return errors.New("secret detail")
	})
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != internalBody {
		t.Fatalf("body = %s", got)
	}
}

func TestHandleRecoversPanics(t *testing.T) {
	rec := serve(func(http.ResponseWriter, *http.Request) error {
		panic("boom")
	})
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != internalBody {
		t.Fatalf("body = %s", got)
	}
}

// A CDN that caches by default must never serve a stale health check or API answer.
func TestJSONIsNeverCached(t *testing.T) {
	success := httptest.NewRecorder()
	if err := httpx.JSON(success, 200, map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	failure := serve(func(http.ResponseWriter, *http.Request) error {
		return &httpx.Error{Status: 503, Code: "not_ready", Message: "down"}
	})
	for name, rec := range map[string]*httptest.ResponseRecorder{"success": success, "error": failure} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

func TestJSONWritesStatusAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := httpx.JSON(rec, 201, map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"n":1}` {
		t.Fatalf("body = %s", got)
	}
}
