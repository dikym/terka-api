// Command terka is the Terka server: one binary, picked apart by subcommand.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/config"
	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/migrate"
	"github.com/dikym/terka-api/server/internal/server"
	"github.com/dikym/terka-api/server/migrations"
)

// Set at build time with -ldflags "-X main.version=<sha>".
var version = "dev"

const usage = `usage: terka <command>

commands:
  serve     run the HTTP server
  migrate   apply the embedded schema migrations
  health    exit 0 when the local server is ready
  smoke     check the session flow against the local server
  version   print the build version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// A failed write to the terminal has nowhere better to be reported.
func tell(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		tell(stderr, "%s", usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(ctx, getenv, stderr)
	case "migrate":
		return migrateSchema(ctx, getenv, stdout, stderr)
	case "health":
		return health(ctx, getenv)
	case "smoke":
		return smoke(ctx, getenv, stderr)
	case "version":
		tell(stdout, "%s\n", version)
		return 0
	default:
		tell(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func serve(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		tell(stderr, "%v\n", err)
		return 1
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))

	deps := server.Deps{Version: version, Env: cfg.Env, Logger: log,
		Ping: func(context.Context) error { return errors.New("no database configured") }}
	// Local development may run without a database; everywhere else config requires both URLs.
	if cfg.DatabaseURL != "" && cfg.AuthDatabaseURL != "" {
		app, err := db.OpenApp(ctx, cfg.DatabaseURL)
		if err != nil {
			tell(stderr, "TERKA_DATABASE_URL: %v\n", err)
			return 1
		}
		defer app.Close()
		authDB, err := db.OpenAuth(ctx, cfg.AuthDatabaseURL)
		if err != nil {
			tell(stderr, "TERKA_AUTH_DATABASE_URL: %v\n", err)
			return 1
		}
		defer authDB.Close()
		deps.Ping = func(ctx context.Context) error { return errors.Join(app.Ping(ctx), authDB.Ping(ctx)) }

		handlers, err := signIn(ctx, cfg, app, authDB, log)
		if err != nil {
			tell(stderr, "%v\n", err)
			return 1
		}
		deps.Routes = handlers.Mount
		go cleanup(ctx, handlers.Sessions, log, cleanupEvery)
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		tell(stderr, "%v\n", err)
		return 1
	}
	srv := &http.Server{
		Handler:           server.New(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Info("listening", "addr", listener.Addr().String(), "env", cfg.Env, "version", version)

	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(listener) }()
	select {
	case err := <-failed:
		log.Error("server stopped", "error", err)
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Error("shutdown did not finish cleanly", "error", err)
		return 1
	}
	return 0
}

func migrateSchema(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	url := getenv("TERKA_MIGRATE_DATABASE_URL")
	if url == "" {
		tell(stderr, "TERKA_MIGRATE_DATABASE_URL is required\n")
		return 1
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		tell(stderr, "connect: %v\n", err)
		return 1
	}
	defer func() { _ = conn.Close(context.Background()) }()
	applied, err := migrate.Apply(ctx, conn, migrations.Files)
	for _, version := range applied {
		tell(stdout, "applied %s\n", version)
	}
	if err != nil {
		tell(stderr, "migrate: %v\n", err)
		return 1
	}
	if len(applied) == 0 {
		tell(stdout, "schema is up to date\n")
	}
	return 0
}

func health(ctx context.Context, getenv func(string) string) int {
	addr := getenv("TERKA_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/readyz", nil)
	if err != nil {
		return 1
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// smoke is run by deploy.sh inside a new container that is healthy but not yet reachable from the
// edge; a failure keeps it out of traffic.
func smoke(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		tell(stderr, "smoke: %v\n", err)
		return 1
	}
	if cfg.AuthDatabaseURL == "" {
		tell(stderr, "smoke: TERKA_AUTH_DATABASE_URL is required\n")
		return 1
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		tell(stderr, "smoke: TERKA_ADDR: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	authDB, err := db.OpenAuth(ctx, cfg.AuthDatabaseURL)
	if err != nil {
		tell(stderr, "smoke: TERKA_AUTH_DATABASE_URL: %v\n", err)
		return 1
	}
	defer authDB.Close()
	sessions := &auth.Sessions{Auth: authDB, Now: time.Now}
	if err := auth.Smoke(ctx, sessions, "http://127.0.0.1:"+port, &http.Client{}); err != nil {
		tell(stderr, "smoke: %v\n", err)
		return 1
	}
	return 0
}

func signIn(ctx context.Context, cfg config.Config, app *db.App, authDB *db.Auth, log *slog.Logger) (*auth.Handlers, error) {
	keys, err := auth.DeriveKeys(cfg.AuthKey)
	if err != nil {
		return nil, err
	}
	mailer, err := auth.NewSMTPMailer(cfg.SMTPURL, cfg.MailFrom, cfg.Env == "local")
	if err != nil {
		return nil, err
	}
	now := time.Now
	sessions := &auth.Sessions{Auth: authDB, Now: now}
	h := &auth.Handlers{
		Codes: &auth.EmailCodes{
			Auth: authDB, Sessions: sessions, Keys: keys, Mailer: mailer,
			Turnstile: auth.NewTurnstile(cfg.TurnstileSecret, cfg.TurnstileHostnames, cfg.TurnstileEndpoint, nil),
			PublicURL: cfg.PublicURL, Now: now, Log: log,
		},
		Sessions: sessions, Auth: authDB, App: app,
		Env: cfg.Env, TrustCloudflare: cfg.TrustCloudflare, Log: log, Now: now,
	}
	if cfg.GoogleClientID != "" {
		h.Google = auth.NewGoogle(ctx, auth.GoogleProvider, cfg.GoogleClientID, cfg.GoogleClientSecret,
			cfg.PublicURL+"/api/auth/google/callback", keys, authDB, sessions, now)
	}
	return h, nil
}

// Expired sessions, codes and failures are swept on this beat; tests shorten it.
var cleanupEvery = time.Hour

func cleanup(ctx context.Context, sessions *auth.Sessions, log *slog.Logger, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			removed, err := sessions.Cleanup(ctx)
			if err != nil {
				log.WarnContext(ctx, "cleanup failed", "error", err)
				continue
			}
			log.InfoContext(ctx, "cleanup", "sessions_removed", removed)
		}
	}
}
