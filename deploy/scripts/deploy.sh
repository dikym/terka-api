#!/usr/bin/env bash
# Deploys one image of one service to one environment: pull, migrate (api only), replace the
# container, wait for health, and fall back to the previous version if the new one never turns
# healthy. Each service keeps its own compose project and its own record of the running version.
#
# Usage: deploy.sh <api|web> <staging|production> <40-character commit SHA>
# As a CI key's forced command the server runs "deploy.sh <service>", so the key can only deploy
# that service; the environment and SHA come from SSH_ORIGINAL_COMMAND.
#
# Exit: 0 deployed · 1 pull or migration failed, running version untouched · 2 the new version
# failed to start or turn healthy and the previous one was started again · 3 the same on a first
# deploy, container stopped · 64 bad arguments. The new version serves traffic until it is judged
# unhealthy.
set -euo pipefail

root=${TERKA_ROOT:-/opt/terka}
docker=${DOCKER:-docker}
health_timeout=${TERKA_HEALTH_TIMEOUT:-60}

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

compose() {
	local image=$1
	shift
	TERKA_ENV_NAME=$env_name TERKA_IMAGE=$image \
		"$docker" compose -p "terka-$env_name-$service" -f "$root/compose.$service.yaml" "$@"
}

# Waits for Docker's health check; "unhealthy" ends the wait early, "starting" runs out the clock.
healthy() {
	local container status deadline=$((SECONDS + health_timeout))
	container=$(compose "$1" ps -q "$service")
	while [ $SECONDS -lt $deadline ]; do
		status=$("$docker" inspect -f '{{.State.Health.Status}}' "$container" 2>/dev/null || echo missing)
		case "$status" in
		healthy) return 0 ;;
		unhealthy) return 1 ;;
		esac
		sleep 1
	done
	return 1
}

image=$registry:$tag
echo "deploying $image to $env_name (previous: ${previous:-none})"

"$docker" pull "$image" || { echo "pull failed; nothing changed" >&2; exit 1; }
if [ "$service" = api ]; then
	compose "$image" --profile tools run --rm migrate ||
		{ echo "migration failed; the api was not replaced" >&2; exit 1; }
fi
# A container that fails to start is handled like one that never turns healthy: compose may already
# have removed the old one, so the previous version has to be started again either way.
if compose "$image" up -d --no-deps "$service" && healthy "$image"; then
	echo "$tag" >"$current_file"
	echo "$service in $env_name now runs $tag"
	exit 0
fi

if [ -n "$previous" ]; then
	echo "$tag did not turn healthy; rolling back to $previous" >&2
	compose "$registry:$previous" up -d --no-deps "$service"
	healthy "$registry:$previous" || echo "the previous version is not healthy either" >&2
	exit 2
fi

echo "$tag did not turn healthy and there is no previous version; stopping it" >&2
compose "$image" stop "$service" || true
exit 3
