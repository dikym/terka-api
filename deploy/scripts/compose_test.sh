#!/usr/bin/env bash
# Checks how compose.app.yaml hands settings to each service: the serving app must never receive
# the schema owner's credentials, which bypass row level security.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1"
	failures=$((failures + 1))
}

work=$(mktemp -d)
trap 'rm -r "$work"' EXIT
mkdir -p "$work/staging"
cp "$repo/deploy/compose.app.yaml" "$work/"
# What the Ansible templates write, with dummy values.
printf 'TERKA_ENV=staging\nTERKA_DATABASE_URL=postgres://app@postgres/terka_staging\n' >"$work/staging/.env"
printf 'TERKA_MIGRATE_DATABASE_URL=postgres://owner@postgres/terka_staging\n' >"$work/staging/migrate.env"

config=$(TERKA_ENV_NAME=staging TERKA_IMAGE=ghcr.io/dikym/terka-go:test \
	docker compose -f "$work/compose.app.yaml" --profile tools config --format json)
env_of() { jq -r --arg s "$1" '.services[$s].environment | keys[]' <<<"$config"; }

if ! env_of app | grep -qx TERKA_MIGRATE_DATABASE_URL; then
	pass "the app does not receive the schema owner's credentials"
else
	fail "the app does not receive the schema owner's credentials"
fi
if env_of migrate | grep -qx TERKA_MIGRATE_DATABASE_URL; then
	pass "the migration job receives the schema owner's credentials"
else
	fail "the migration job receives the schema owner's credentials"
fi
if ! grep -q TERKA_MIGRATE_DATABASE_URL "$repo/deploy/ansible/roles/terka/templates/env.j2"; then
	pass "the app settings template holds no owner credentials"
else
	fail "the app settings template holds no owner credentials"
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
