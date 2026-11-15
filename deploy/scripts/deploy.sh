#!/usr/bin/env bash
# Deploys one image of one service to one environment: pull, migrate (api only), start the new
# container beside the running one, wait for health, admit it to the edge network, then stop the
# old one. A new container that never turns healthy is removed before it serves a single request.
# Each service keeps its own compose project and its own record of the running version.
#
# Usage: deploy.sh <api|web> <staging|production> <40-character commit SHA>
# As a CI key's forced command the server runs "deploy.sh <service>", so the key can only deploy
# that service; the environment and SHA come from SSH_ORIGINAL_COMMAND.
#
# Exit: 0 deployed · 1 another deploy of the service is running, or pull, migration or listing
# the containers failed; running version untouched · 2 the new version failed to start, turn
# healthy, pass the smoke test (api only) or join the edge and was removed; the old one kept serving
# throughout · 3 the same on a first deploy, nothing left running · 64 bad arguments.
set -euo pipefail

root=${TERKA_ROOT:-/opt/terka}
docker=${DOCKER:-docker}
health_timeout=${TERKA_HEALTH_TIMEOUT:-60}
# Caddy lives only on this network; Ansible creates it.
edge_network=terka-edge

usage() {
	echo "usage: deploy.sh <api|web> <staging|production> <40-character commit SHA>" >&2
	exit 64
}

if [ $# -eq 1 ]; then
	# Split on spaces only; the text is never handed to a shell.
	read -r -a words <<<"${SSH_ORIGINAL_COMMAND:-}"
	set -- "$1" "${words[@]}"
fi
[ $# -eq 3 ] || usage
service=$1
env_name=$2
tag=$3
case "$service" in api | web) ;; *) usage ;; esac
case "$env_name" in staging | production) ;; *) usage ;; esac
[[ $tag =~ ^[0-9a-f]{40}$ ]] || usage

registry=ghcr.io/dikym/terka-$service
current_file=$root/$env_name/$service.current
previous=$(cat "$current_file" 2>/dev/null || true)
alias=terka-$env_name-$service
image=$registry:$tag

compose() {
	TERKA_ENV_NAME=$env_name TERKA_IMAGE=$image \
		"$docker" compose -p "terka-$env_name-$service" -f "$root/compose.$service.yaml" "$@"
}

containers() { compose ps -a -q "$service"; }

# Waits for Docker's health check; "unhealthy" ends the wait early, "starting" runs out the clock.
healthy() {
	local status deadline=$((SECONDS + health_timeout))
	while [ $SECONDS -lt $deadline ]; do
		status=$("$docker" inspect -f '{{.State.Health.Status}}' "$1" 2>/dev/null || echo missing)
		case "$status" in
		healthy) return 0 ;;
		unhealthy) return 1 ;;
		esac
		sleep 1
	done
	return 1
}

# Stop honours the service's stop_grace_period, so requests in flight finish first.
discard() {
	"$docker" stop "$@" >/dev/null
	"$docker" rm "$@" >/dev/null
}

# Two deploys of one service would each take the other's new container for their own. mkdir is
# atomic and, unlike flock, also exists on macOS where the tests run. A deploy killed with SIGKILL
# leaves the lock behind; the runbook says how to clear it.
lock=$root/$env_name/$service.lock
mkdir "$lock" 2>/dev/null ||
	{ echo "another deploy of $service in $env_name is running (lock: $lock); nothing changed" >&2; exit 1; }
trap 'rmdir "$lock"' EXIT

echo "deploying $image to $env_name (previous: ${previous:-none})"

"$docker" pull "$image" || { echo "pull failed; nothing changed" >&2; exit 1; }
if [ "$service" = api ]; then
	compose --profile tools run --rm migrate ||
		{ echo "migration failed; the api was not replaced" >&2; exit 1; }
fi

# up starts every stopped container of the service, and a leftover from an interrupted deploy
# still holds its edge alias: it would serve an older version without a health check.
compose rm -f "$service" >/dev/null || { echo "cannot remove stopped containers; nothing changed" >&2; exit 1; }
# A failed listing read as "no containers" would make the running one look new and discard it.
listed=$(containers) || { echo "cannot list containers; nothing changed" >&2; exit 1; }
old=()
read -r -d '' -a old <<<"$listed" || true
reason=""
compose up -d --no-deps --no-recreate --scale "$service=$((${#old[@]} + 1))" "$service" ||
	reason="failed to start"
new=()
if listed=$(containers); then
	read -r -d '' -a new < <(comm -13 <(printf '%s\n' ${old[@]+"${old[@]}"} | sort) <(sort <<<"$listed")) || true
else
	reason="could not be found after starting; check for a container outside $edge_network"
fi

if [ -z "$reason" ] && [ ${#new[@]} -ne 1 ]; then
	reason="failed to start"
fi
if [ -z "$reason" ] && ! healthy "${new[0]}"; then
	reason="did not turn healthy"
fi
if [ -z "$reason" ] && [ "$service" = api ] && ! "$docker" exec "${new[0]}" /terka smoke; then
	reason="failed the smoke test"
fi
if [ -z "$reason" ] && ! "$docker" network connect --alias "$alias" "$edge_network" "${new[0]}"; then
	reason="could not join $edge_network"
fi

if [ -z "$reason" ]; then
	if [ ${#old[@]} -gt 0 ]; then
		discard "${old[@]}" || echo "could not remove an old container; it still serves beside $tag" >&2
	fi
	echo "$tag" >"$current_file"
	echo "$service in $env_name now runs $tag"
	exit 0
fi

echo "$tag $reason" >&2
if [ ${#new[@]} -gt 0 ]; then
	discard "${new[@]}" || echo "could not remove ${new[*]}" >&2
fi
if [ ${#old[@]} -gt 0 ]; then
	echo "$tag was not admitted; ${previous:-the old container} keeps serving" >&2
	exit 2
fi
echo "$tag was not admitted and there is no previous version; removed it" >&2
exit 3
