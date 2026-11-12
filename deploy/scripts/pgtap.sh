#!/usr/bin/env bash
# Runs the pgTAP suite against a fresh database on the dev Postgres (deploy/compose.dev.yaml).
# The schema is applied as the superuser on purpose: tests.as_owner() switches to role postgres
# and expects it to own the tables.
set -euo pipefail
cd "$(dirname "$0")/../.."

db=terka_pgtap
base=${TERKA_TEST_DATABASE_URL:-postgres://postgres:postgres@localhost:5433/postgres}
compose=(docker compose -f deploy/compose.dev.yaml)

psql() { "${compose[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 -U postgres "$@"; }

psql -d postgres -c "drop database if exists $db with (force)" -c "create database $db"
# The helper file installs pgTAP into schema extensions, as on Supabase, and tests call it after
# switching to anon or authenticated.
psql -d "$db" -c "create schema extensions" -c "grant usage on schema extensions to anon, authenticated" \
	-c "alter database $db set search_path = \"\$user\", public, extensions"
TERKA_MIGRATE_DATABASE_URL="${base%/*}/$db" go -C server run ./cmd/terka migrate >/dev/null
# The helper file commits its functions; every test file rolls back.
"${compose[@]}" exec -T postgres sh -c "cd /tests && pg_prove -U postgres -d $db 00000_helpers.sql *.test.sql"
