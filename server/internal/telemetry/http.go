package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// HTTP traces and measures each request. The route is read after next returns because only then
// has ServeMux set the pattern on the request; the label stays bounded to the registered patterns.
// otelhttp labels metrics with the pattern minus its method itself, so the labeler only matters
// for requests that never reached the mux. The fixed server name keeps the Host header, which the
// caller controls, out of server.address and server.port. The :80 stops otelhttp from falling back
// to the Host port, and port 80 is omitted from the attributes.
func HTTP(next http.Handler) http.Handler {
	return otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		ctx := r.Context()
		span := trace.SpanFromContext(ctx)
		span.SetName(route)
		span.SetAttributes(semconv.HTTPRoute(route))
		if l, ok := otelhttp.LabelerFromContext(ctx); ok {
			l.Add(semconv.HTTPRoute(route))
		}
	}), "http.server", otelhttp.WithServerName("terka-api:80"))
}
