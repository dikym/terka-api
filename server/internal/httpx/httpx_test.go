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

type payload struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func decode(body string) (payload, error) {
	var p payload
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	err := httpx.DecodeJSON(httptest.NewRecorder(), r, &p)
	return p, err
}

func assertBadRequest(t *testing.T, name string, err error) {
	t.Helper()
	var shown *httpx.Error
	if !errors.As(err, &shown) || shown.Status != 400 || shown.Code != "bad_request" {
		t.Errorf("%s: err = %v, want a 400 bad_request", name, err)
	}
}

func TestDecodeJSONReadsAnObject(t *testing.T) {
	p, err := decode(`{"email":"a@b.co","code":"123456"}`)
	if err != nil || p.Email != "a@b.co" || p.Code != "123456" {
		t.Fatalf("got %+v, %v", p, err)
	}
}

func TestDecodeJSONRejectsLargeBody(t *testing.T) {
	_, err := decode(`{"email":"` + strings.Repeat("a", 8*1024) + `"}`)
	assertBadRequest(t, "body over 8 KiB", err)
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	_, err := decode(`{"email":"a@b.co","password":"x"}`)
	assertBadRequest(t, "unknown field", err)
}

func TestDecodeJSONRejectsTwoValues(t *testing.T) {
	_, err := decode(`{"email":"a@b.co"}{"email":"c@d.co"}`)
	assertBadRequest(t, "two objects", err)
}

func TestDecodeJSONRejectsWrongTypes(t *testing.T) {
	for _, body := range []string{`{"email":1}`, `[]`, `"x"`, `null`, ``, `{`, `{"email":"a",}`} {
		_, err := decode(body)
		assertBadRequest(t, body, err)
	}
}

func FuzzDecodeJSON(f *testing.F) {
	for _, seed := range []string{`{}`, `{"email":"a"}`, `{"email":"a"}{}`, `[`, `{"a":1}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		_, err := decode(body)
		var shown *httpx.Error
		if err != nil && !errors.As(err, &shown) {
			t.Fatalf("%q: error %v is not a client error", body, err)
		}
	})
}

func TestClientIPIgnoresCloudflareHeaderUnlessTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	r.Header.Set("CF-Connecting-IP", "198.51.100.1")
	if got := httpx.ClientIP(r, false); got != "203.0.113.7" {
		t.Fatalf("untrusted: got %q, want the connection address", got)
	}
}

func TestClientIPUsesCloudflareHeaderWhenTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:51234"
	r.Header.Set("CF-Connecting-IP", "2001:db8::1")
	if got := httpx.ClientIP(r, true); got != "2001:db8::1" {
		t.Fatalf("trusted: got %q, want the Cloudflare address", got)
	}
	r.Header.Set("CF-Connecting-IP", "not an address")
	if got := httpx.ClientIP(r, true); got != "172.18.0.5" {
		t.Fatalf("trusted with a malformed header: got %q, want the connection address", got)
	}
}
