package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/dikym/terka-go/server/internal/web"
)

var files = fstest.MapFS{
	"index.html":                 {Data: []byte("home")},
	"sesi.html":                  {Data: []byte("sesi")},
	"auth/callback.html":         {Data: []byte("callback")},
	"404.html":                   {Data: []byte("missing")},
	"plates.json":                {Data: []byte(`{"regions":[]}`)},
	"service-worker.js":          {Data: []byte("// worker")},
	"optimizer/fsrs.wasm":        {Data: []byte("\x00asm")},
	"manifest.webmanifest":       {Data: []byte("{}")},
	"_app/immutable/a.js":        {Data: []byte("export {}")},
	"_app/immutable/assets/b.js": {Data: []byte("export {}")},
}

const (
	revalidate = "no-cache"
	immutable  = "public, max-age=31536000, immutable"
)

func request(method, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://terka.test/", nil)
	// Set raw so paths a real client would have cleaned still reach the handler as written.
	req.URL.Path, req.URL.RawQuery = splitQuery(target)
	for key, value := range header {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	web.Handler(files, "v1").ServeHTTP(rec, req)
	return rec
}

func splitQuery(target string) (string, string) {
	for i := range target {
		if target[i] == '?' {
			return target[:i], target[i+1:]
		}
	}
	return target, ""
}

func TestServesBuildFiles(t *testing.T) {
	cases := []struct {
		target string
		status int
		body   string
		cache  string
	}{
		{"/", 200, "home", revalidate},
		{"/sesi", 200, "sesi", revalidate},
		{"/sesi.html", 200, "sesi", revalidate},
		{"/auth/callback?code=1", 200, "callback", revalidate},
		{"/plates.json", 200, `{"regions":[]}`, revalidate},
		{"/sw.js", 200, "// worker", revalidate},
		{"/_app/immutable/a.js", 200, "export {}", immutable},
		{"/_app/immutable/assets/b.js", 200, "export {}", immutable},
	}
	for _, c := range cases {
		rec := request("GET", c.target, nil)
		if rec.Code != c.status || rec.Body.String() != c.body {
			t.Errorf("%s: got %d %q, want %d %q", c.target, rec.Code, rec.Body.String(), c.status, c.body)
		}
		if got := rec.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("%s: Cache-Control = %q, want %q", c.target, got, c.cache)
		}
	}
}

func TestUnknownOrUnsafePathsGetTheNotFoundPage(t *testing.T) {
	for _, target := range []string{"/nope", "/../404.html", "/sesi/../../etc/passwd", "/_app", "/_app/immutable", "/auth"} {
		rec := request("GET", target, nil)
		if rec.Code != 404 || rec.Body.String() != "missing" {
			t.Errorf("%s: got %d %q, want 404 \"missing\"", target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != revalidate {
			t.Errorf("%s: Cache-Control = %q, want %q", target, got, revalidate)
		}
	}
}

func TestTrailingSlashRedirectsToTheCleanPath(t *testing.T) {
	rec := request("GET", "/sesi/?x=1", nil)
	if rec.Code != 308 || rec.Header().Get("Location") != "/sesi?x=1" {
		t.Fatalf("got %d Location %q, want 308 /sesi?x=1", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRevalidatedFilesCarryTheVersionAsETag(t *testing.T) {
	rec := request("GET", "/plates.json", nil)
	if got := rec.Header().Get("ETag"); got != `"v1"` {
		t.Fatalf("ETag = %q, want \"v1\"", got)
	}
	rec = request("GET", "/plates.json", map[string]string{"If-None-Match": `"v1"`})
	if rec.Code != 304 || rec.Body.Len() != 0 {
		t.Fatalf("matching ETag: got %d with %d bytes, want 304 and no body", rec.Code, rec.Body.Len())
	}
	rec = request("GET", "/plates.json", map[string]string{"If-None-Match": `"v0"`})
	if rec.Code != 200 {
		t.Fatalf("stale ETag: got %d, want 200", rec.Code)
	}
}

func TestContentTypes(t *testing.T) {
	for target, want := range map[string]string{
		"/":                     "text/html; charset=utf-8",
		"/sesi":                 "text/html; charset=utf-8",
		"/plates.json":          "application/json",
		"/sw.js":                "text/javascript; charset=utf-8",
		"/optimizer/fsrs.wasm":  "application/wasm",
		"/manifest.webmanifest": "application/manifest+json",
	} {
		if got := request("GET", target, nil).Header().Get("Content-Type"); got != want {
			t.Errorf("%s: Content-Type = %q, want %q", target, got, want)
		}
	}
}

func TestOnlyReadsAreAllowed(t *testing.T) {
	rec := request("HEAD", "/", nil)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: got %d with %d bytes, want 200 and no body", rec.Code, rec.Body.Len())
	}
	rec = request("POST", "/", nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: got %d Allow %q, want 405 \"GET, HEAD\"", rec.Code, rec.Header().Get("Allow"))
	}
}
