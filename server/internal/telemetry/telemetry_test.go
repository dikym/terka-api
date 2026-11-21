package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/dikym/terka-api/server/internal/telemetry"
)

func TestResourceCarriesInstanceID(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Skip("hostname unavailable")
	}
	got, ok := telemetry.Resource(telemetry.Options{Service: "terka-api"}).Set().Value("service.instance.id")
	if !ok || got.AsString() == "" || got.AsString() != host {
		t.Fatalf("service.instance.id = %q (found %v), want %q", got.AsString(), ok, host)
	}
}

func TestSetupWithoutEndpointInstallsNothing(t *testing.T) {
	before := otel.GetTracerProvider()
	shutdown, err := telemetry.Setup(context.Background(), telemetry.Options{Service: "terka-api"}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if otel.GetTracerProvider() != before {
		t.Fatal("tracer provider was replaced")
	}
}

func logLine(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	slog.New(telemetry.LogHandler(slog.NewJSONHandler(&buf, nil))).InfoContext(ctx, "hi")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLogHandlerAddsTraceIDsInsideASpan(t *testing.T) {
	tp := telemetry.NewTracerProvider(nil, sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	ctx, span := tp.Tracer("t").Start(context.Background(), "x")
	defer span.End()
	got := logLine(t, ctx)
	if got["trace_id"] != span.SpanContext().TraceID().String() {
		t.Fatalf("trace_id = %v", got["trace_id"])
	}
	if got["span_id"] != span.SpanContext().SpanID().String() {
		t.Fatalf("span_id = %v", got["span_id"])
	}
}

func TestLogHandlerLeavesLinesOutsideASpanAlone(t *testing.T) {
	got := logLine(t, context.Background())
	if _, ok := got["trace_id"]; ok {
		t.Fatalf("unexpected trace_id in %v", got)
	}
}

func TestQueryStringIsRedactedOnEverySpan(t *testing.T) {
	mem := tracetest.NewInMemoryExporter()
	tp := telemetry.NewTracerProvider(nil, sdktrace.WithSyncer(mem))
	_, span := tp.Tracer("t").Start(context.Background(), "x",
		oteltrace.WithAttributes(attribute.String("url.query", "code=abc&state=xyz")))
	span.End()
	spans := mem.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	found := false
	for _, kv := range spans[0].Attributes {
		if kv.Key == "url.query" {
			found = true
			if kv.Value.AsString() != "redacted" {
				t.Fatalf("url.query = %q", kv.Value.AsString())
			}
		}
	}
	if !found {
		t.Fatal("url.query attribute missing")
	}
}

func TestDatabaseQueryDurationIsInMilliseconds(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	tracer := otelpgx.NewTracer(otelpgx.WithMeterProvider(telemetry.NewMeterProvider(sdkmetric.WithReader(reader))))
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "select 1"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "db.client.operation.duration" {
				if m.Unit != "ms" {
					t.Fatalf("unit %q, want ms", m.Unit)
				}
				return
			}
		}
	}
	t.Fatal("no db.client.operation.duration metric")
}
