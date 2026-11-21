package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/server"
	"github.com/dikym/terka-api/server/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestCrossOriginRefusalIsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/x", strings.NewReader("{}"))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	server.New(deps("local", up)).ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != "application/json" ||
		!strings.Contains(body(rec), `"code":"cross_origin"`) {
		t.Fatalf("got %d %q %s", rec.Code, rec.Header().Get("Content-Type"), body(rec))
	}
}

func observe(t *testing.T) (*tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	reader := sdkmetric.NewManualReader()
	prevTP, prevMP := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(telemetry.NewTracerProvider(nil, sdktrace.WithSyncer(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetMeterProvider(prevMP)
	})
	return spans, reader
}

func onlySpan(t *testing.T, spans *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()
	got := spans.GetSpans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	return got[0]
}

func attr(s tracetest.SpanStub, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestSpanIsNamedAfterTheRoutePattern(t *testing.T) {
	spans, _ := observe(t)
	get(server.New(deps("local", up)), "/readyz")
	s := onlySpan(t, spans)
	route, _ := attr(s, "http.route")
	if s.Name != "GET /readyz" || route.AsString() != "GET /readyz" {
		t.Fatalf("name %q, http.route %q", s.Name, route.AsString())
	}
}

func TestUnknownPathsShareOneRouteLabel(t *testing.T) {
	spans, _ := observe(t)
	h := server.New(deps("local", up))
	get(h, "/wp-login.php")
	get(h, "/x/123")
	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2", len(got))
	}
	for _, s := range got {
		if route, _ := attr(s, "http.route"); route.AsString() != "/" {
			t.Fatalf("http.route %q, want /", route.AsString())
		}
	}
}

func TestCrossOriginRefusalIsLabelledUnmatched(t *testing.T) {
	spans, _ := observe(t)
	req := httptest.NewRequest("POST", "/readyz", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	server.New(deps("local", up)).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("got %d, want 403", rec.Code)
	}
	if route, _ := attr(onlySpan(t, spans), "http.route"); route.AsString() != "unmatched" {
		t.Fatalf("http.route %q, want unmatched", route.AsString())
	}
}

func TestRequestDurationMetricCarriesTheRoute(t *testing.T) {
	_, reader := observe(t)
	get(server.New(deps("local", up)), "/healthz")
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			h, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "http.server.request.duration" || !ok {
				continue
			}
			for _, dp := range h.DataPoints {
				if v, ok := dp.Attributes.Value("http.route"); ok && v.AsString() == "/healthz" {
					return
				}
			}
		}
	}
	t.Fatal("no http.server.request.duration point with http.route /healthz")
}

func leaksHost(kv attribute.KeyValue) bool {
	v := fmt.Sprint(kv.Value.AsInterface())
	return strings.Contains(v, "attacker-123") || strings.Contains(v, "4444")
}

func TestHostHeaderDoesNotBecomeALabel(t *testing.T) {
	spans, reader := observe(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Host = "attacker-123.example:4444"
	server.New(deps("local", up)).ServeHTTP(httptest.NewRecorder(), req)

	for _, kv := range onlySpan(t, spans).Attributes {
		if leaksHost(kv) {
			t.Fatalf("span attribute %s=%s carries the host header", kv.Key, kv.Value.AsInterface())
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			h, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "http.server.request.duration" || !ok {
				continue
			}
			for _, dp := range h.DataPoints {
				seen = true
				for _, kv := range dp.Attributes.ToSlice() {
					if leaksHost(kv) {
						t.Fatalf("metric attribute %s=%s carries the host header", kv.Key, kv.Value.AsInterface())
					}
				}
			}
		}
	}
	if !seen {
		t.Fatal("no http.server.request.duration data point")
	}
}

func TestPanicIsRecordedAsA500Span(t *testing.T) {
	spans, _ := observe(t)
	d := deps("local", up)
	d.Routes = func(mux *http.ServeMux) {
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	}
	rec := get(server.New(d), "/boom")
	if rec.Code != 500 {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	if code, _ := attr(onlySpan(t, spans), "http.response.status_code"); code.AsInt64() != 500 {
		t.Fatalf("status code attribute %d, want 500", code.AsInt64())
	}
}

func TestIncomingTraceparentIsIgnored(t *testing.T) {
	spans, _ := observe(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	server.New(deps("local", up)).ServeHTTP(httptest.NewRecorder(), req)
	if id := onlySpan(t, spans).SpanContext.TraceID().String(); id == "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("span joined the caller's trace")
	}
}

func TestQueryStringNeverReachesTheSpan(t *testing.T) {
	spans, _ := observe(t)
	get(server.New(deps("local", up)), "/healthz?code=abc&state=xyz")
	s := onlySpan(t, spans)
	if q, ok := attr(s, "url.query"); ok && q.AsString() != "redacted" {
		t.Fatalf("url.query %q", q.AsString())
	}
	for _, kv := range s.Attributes {
		if v := kv.Value.String(); strings.Contains(v, "abc") || strings.Contains(v, "xyz") {
			t.Fatalf("attribute %s leaks the query: %s", kv.Key, v)
		}
	}
}

func TestAccessLogCarriesTheRequestTraceID(t *testing.T) {
	spans, _ := observe(t)
	var buf bytes.Buffer
	d := deps("local", up)
	d.Logger = slog.New(telemetry.LogHandler(slog.NewJSONHandler(&buf, nil)))
	get(server.New(d), "/healthz")
	var line struct {
		Msg     string `json:"msg"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatal(err)
	}
	if want := onlySpan(t, spans).SpanContext.TraceID().String(); line.Msg != "request" || line.TraceID != want {
		t.Fatalf("log line %+v, want trace_id %s", line, want)
	}
}
