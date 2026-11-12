#!/usr/bin/env bash
# Deploys one image to one environment: pull, migrate, replace the app, wait for health,
# and fall back to the previous version if the new one never turns healthy.
#
# Usage: deploy.sh <staging|production> <40-character commit SHA>
# When run as the CI key's forced command, the two words come from SSH_ORIGINAL_COMMAND.
#
# Exit: 0 deployed · 1 pull or migration failed, app untouched · 2 rolled back to the previous
# version · 3 first deploy failed, app stopped · 64 bad arguments.
set -euo pipefail

root=${TERKA_ROOT:-/opt/terka}
docker=${DOCKER:-docker}
health_timeout=${TERKA_HEALTH_TIMEOUT:-60}
registry=ghcr.io/dikym/terka-go

usage() {
	echo "usage: deploy.sh <staging|production> <40-character commit SHA>" >&2
	exit 64
}

if [ $# -eq 0 ] && [ -n "${SSH_ORIGINAL_COMMAND:-}" ]; then
	# Split on spaces only; the text is never handed to a shell.
	read -r -a words <<<"$SSH_ORIGINAL_COMMAND"
	set -- "${words[@]}"
fi
[ $# -eq 2 ] || usage
env_name=$1
tag=$2
case "$env_name" in staging | production) ;; *) usage ;; esac
[[ $tag =~ ^[0-9a-f]{40}$ ]] || usage

current_file=$root/$env_name/current
previous=$(cat "$current_file" 2>/dev/null || true)

app() {
	local image=$1
	shift
	TERKA_ENV_NAME=$env_name TERKA_IMAGE=$image \
		"$docker" compose -p "terka-$env_name" -f "$root/compose.app.yaml" "$@"
}

# Waits for Docker's health check; "unhealthy" ends the wait early, "starting" runs out the clock.
healthy() {
	local container status deadline=$((SECONDS + health_timeout))
	container=$(app "$1" ps -q app)
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
app "$image" --profile tools run --rm migrate || { echo "migration failed; the app was not replaced" >&2; exit 1; }
app "$image" up -d --no-deps app

if healthy "$image"; then
	echo "$tag" >"$current_file"
	echo "$env_name now runs $tag"
	exit 0
fi

if [ -n "$previous" ]; then
	echo "$tag did not turn healthy; rolling back to $previous" >&2
	app "$registry:$previous" up -d --no-deps app
	healthy "$registry:$previous" || echo "the previous version is not healthy either" >&2
	exit 2
fi

echo "$tag did not turn healthy and there is no previous version; stopping it" >&2
app "$image" stop app
exit 3
