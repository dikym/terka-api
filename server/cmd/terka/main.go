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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dikym/terka-api/server/internal/config"
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
	if cfg.DatabaseURL == "" && cfg.Env != "local" {
		tell(stderr, "TERKA_DATABASE_URL is required outside the local environment\n")
		return 1
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))

	ping := func(context.Context) error { return errors.New("no database configured") }
	if cfg.DatabaseURL != "" {
		// The pool dials lazily, so a database that is down at start does not stop the server.
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			tell(stderr, "TERKA_DATABASE_URL: %v\n", err)
			return 1
		}
		defer pool.Close()
		ping = pool.Ping
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		tell(stderr, "%v\n", err)
		return 1
	}
	srv := &http.Server{
		Handler: server.New(server.Deps{
			Version: version,
			Env:     cfg.Env,
			Ping:    ping,
			Logger:  log,
		}),
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
