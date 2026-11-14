// Package server assembles the routes and middleware of the terka binary.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/dikym/terka-api/server/internal/httpx"
)

type Deps struct {
	Version string
	Env     string
	Ping    func(context.Context) error
	Logger  *slog.Logger
	// Routes mounts the API endpoints next to the health checks.
	Routes func(mux *http.ServeMux)
}

const pingTimeout = 2 * time.Second

func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", httpx.Handle(func(w http.ResponseWriter, _ *http.Request) error {
		return httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok", "version": d.Version})
	}, d.Logger))
	mux.Handle("GET /readyz", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
		defer cancel()
		if err := d.Ping(ctx); err != nil {
			d.Logger.WarnContext(r.Context(), "database is not reachable", "error", err)
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "not_ready", Message: "database is not reachable"}
		}
		return httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}, d.Logger))
	// The web build is served by its own container; anything else reaching the API is unknown.
	mux.Handle("/", httpx.Handle(func(http.ResponseWriter, *http.Request) error {
		return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such endpoint"}
	}, d.Logger))
	if d.Routes != nil {
		d.Routes(mux)
	}
	// Browsers say where a request comes from; a write from another site is refused before any
	// handler sees it, so a session cookie can never be spent by a page the user did not open.
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(httpx.Handle(func(http.ResponseWriter, *http.Request) error {
		return &httpx.Error{Status: http.StatusForbidden, Code: "cross_origin", Message: "cross-origin request refused"}
	}, d.Logger))
	guarded := protection.Handler(mux)
	return httpx.Recover(accessLog(securityHeaders(guarded, d.Env), d.Logger), d.Logger)
}

func securityHeaders(next http.Handler, env string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		if env != "production" {
			h.Set("X-Robots-Tag", "noindex")
		}
		if env != "local" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the real writer, which streaming responses need.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func accessLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// The route pattern, not the path: paths can carry ids and would explode metric labels later.
		log.InfoContext(r.Context(), "request",
			"method", r.Method, "route", r.Pattern, "status", sw.status, "duration_ms", time.Since(start).Milliseconds())
	})
}
