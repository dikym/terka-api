package httpx_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dikym/terka-go/server/internal/httpx"
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
