#!/usr/bin/env bash
# Checks how compose.api.yaml and compose.web.yaml hand settings to each service: the serving API never receives
# the schema owner's credentials, which bypass row level security. Also checks what compose.obs.yaml exposes.
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
api=$(jq -c '{api: .services.api.mem_limit, gomem: .services.api.environment.GOMEMLIMIT, migrate: .services.migrate.mem_limit}' <<<"$config")
if [ "$api" = '{"api":"134217728","gomem":"100MiB","migrate":"67108864"}' ]; then
	pass "the api runs in 128 MiB with the Go heap capped below it, the migration job in 64 MiB"
else
	fail "the api runs in 128 MiB with the Go heap capped below it, the migration job in 64 MiB" "$api"
fi
web=$(jq -c '.services.web | {env: .environment, ro: .read_only, mem: .mem_limit}' <<<"$web_config")
if [ "$web" = '{"env":{"TERKA_ENV":"staging"},"ro":true,"mem":"33554432"}' ]; then
	pass "the web container gets only its environment name, read-only, in 32 MiB"
else
	fail "the web container gets only its environment name, read-only, in 32 MiB" "$web"
fi

# The observability stack, laid out as Ansible writes /opt/terka/obs.
mkdir -p "$work/obs"
cp "$repo/deploy/compose.obs.yaml" "$work/obs/"
printf 'JOURNAL_GID=999\n' >"$work/obs/.env"
printf 'TERKA_MONITOR_DSN=postgres://monitor@postgres/terka_staging\n' >"$work/obs/alloy.env"
printf 'GF_SECURITY_ADMIN_PASSWORD=dummy\n' >"$work/obs/grafana.env"
obs_compose() { docker compose -f "$work/obs/compose.obs.yaml" "$@"; }
obs=$(obs_compose --profile tempo config --format json)
default=$(obs_compose config --services | sort | tr '\n' ' ')
if [ "$default" = 'alloy grafana loki prometheus ' ]; then
	pass "tempo starts only when its profile is asked for"
else
	fail "tempo starts only when its profile is asked for" "$default"
fi
mem=$(jq -c -S '.services | map_values(.mem_limit)' <<<"$obs")
if [ "$mem" = '{"alloy":"201326592","grafana":"335544320","loki":"201326592","prometheus":"201326592","tempo":"335544320"}' ]; then
	pass "each observability service has its memory limit"
else
	fail "each observability service has its memory limit" "$mem"
fi
ports=$(jq -c '[.services | to_entries[] | select(.value.ports) | {(.key): [.value.ports[] | "\(.host_ip):\(.published):\(.target)"]}] | add' <<<"$obs")
if [ "$ports" = '{"grafana":["127.0.0.1:3000:3000"]}' ]; then
	pass "the only host port is grafana on loopback"
else
	fail "the only host port is grafana on loopback" "$ports"
fi
nets=$(jq -c -S '.services | map_values(.networks | keys)' <<<"$obs")
if [ "$nets" = '{"alloy":["terka","terka-obs"],"grafana":["terka-edge","terka-obs"],"loki":["terka-obs"],"prometheus":["terka-obs"],"tempo":["terka-obs"]}' ]; then
	pass "only alloy reaches the app network and only grafana the edge"
else
	fail "only alloy reaches the app network and only grafana the edge" "$nets"
fi
# The Docker socket, /var/lib/docker and the host root hold every container's secrets.
alloy=$(jq -c --arg w "$work/obs/" '.services.alloy
	| {user, group_add, binds: [.volumes[] | select(.type == "bind") | "\(.source | ltrimstr($w)):\(.read_only // false)"]}' <<<"$obs")
if [ "$alloy" = '{"user":"65534:65534","group_add":["999","473"],"binds":["/var/log/journal:true","/run/log/journal:true","/etc/machine-id:true","alloy:true"]}' ]; then
	pass "alloy runs unprivileged and reads only the journal and its config"
else
	fail "alloy runs unprivileged and reads only the journal and its config" "$alloy"
fi
foreign=$(jq -r '.services[].environment // {} | keys[]' <<<"$obs" | grep -v '^GF_' | grep -vx TERKA_MONITOR_DSN | sort -u | tr '\n' ' ')
if [ -z "$foreign" ] && jq -e '.services.alloy.environment.TERKA_MONITOR_DSN' <<<"$obs" >/dev/null; then
	pass "the observability stack reads its own settings and none of the app's"
else
	fail "the observability stack reads its own settings and none of the app's" "$foreign"
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
