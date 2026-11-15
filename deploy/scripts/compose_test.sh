#!/usr/bin/env bash
# Checks how compose.api.yaml and compose.web.yaml hand settings to each service: the serving API never receives
# the schema owner's credentials, which bypass row level security.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

work=$(mktemp -d)
trap 'rm -r "$work"' EXIT
mkdir -p "$work/staging"
cp "$repo/deploy/compose.api.yaml" "$repo/deploy/compose.web.yaml" "$work/"
# What the Ansible templates write, with dummy values.
printf 'TERKA_ENV=staging\nTERKA_DATABASE_URL=postgres://app@postgres/terka_staging\n' >"$work/staging/.env"
printf 'TERKA_MIGRATE_DATABASE_URL=postgres://owner@postgres/terka_staging\n' >"$work/staging/migrate.env"

config=$(TERKA_ENV_NAME=staging TERKA_IMAGE=ghcr.io/dikym/terka-api:test \
	docker compose -f "$work/compose.api.yaml" --profile tools config --format json)
web_config=$(TERKA_ENV_NAME=staging TERKA_IMAGE=ghcr.io/dikym/terka-web:test \
	docker compose -f "$work/compose.web.yaml" config --format json)
env_of() { jq -r --arg s "$1" '.services[$s].environment // {} | keys[]' <<<"$config"; }

if ! env_of api | grep -qx TERKA_MIGRATE_DATABASE_URL; then
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

# The server refuses to start outside local without these, so the template must write every one.
missing=""
while read -r name; do
	grep -q "^$name=" "$repo/deploy/ansible/roles/terka/templates/env.j2" || missing="$missing $name"
done < <(sed -n '/^var required = \[\]string{/,/^}/p' "$repo/server/internal/config/config.go" | grep -oE 'TERKA_[A-Z_]+')
if [ -z "$missing" ]; then
	pass "the app settings template writes every setting the server requires"
else
	fail "the app settings template writes every setting the server requires" "missing:$missing"
fi

grace=$(jq -r '.services.api.stop_grace_period' <<<"$config")
if [ "$grace" = 25s ]; then
	pass "the api gets 25 seconds to finish its requests before it is killed"
else
	fail "the api gets 25 seconds to finish its requests before it is killed" "$grace"
fi

# deploy.sh admits a container to the edge only once it is healthy; an alias here would admit it early.
edge=$(jq -s -c '[.[].services[] | .networks // {} | to_entries[] | select(.key == "terka-edge" or .value.aliases != null)]' \
	<<<"$config$web_config")
if [ "$edge" = '[]' ]; then
	pass "compose leaves the edge alias to the deploy script"
else
	fail "compose leaves the edge alias to the deploy script" "$edge"
fi
web=$(jq -c '.services.web | {env: .environment, ro: .read_only, mem: .mem_limit}' <<<"$web_config")
if [ "$web" = '{"env":{"TERKA_ENV":"staging"},"ro":true,"mem":"33554432"}' ]; then
	pass "the web container gets only its environment name, read-only, in 32 MiB"
else
	fail "the web container gets only its environment name, read-only, in 32 MiB" "$web"
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
