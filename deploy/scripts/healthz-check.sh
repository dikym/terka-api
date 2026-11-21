#!/usr/bin/env bash
# Usage: healthz-check.sh <url> <previous-conclusion>
# Prints "down" or "up" only when the state changed since the previous run, so a scheduled
# workflow can alert on transitions instead of every run. Exits 1 when the endpoint is unhealthy.
# previous-conclusion is success, failure or empty (no earlier run, treated like success).
set -uo pipefail

url=${1:?usage: healthz-check.sh <url> <previous-conclusion>}
previous=${2:-}

status() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$url" || true; }

# One retry absorbs a deploy restart or a network blip.
if [ "$(status)" != 200 ]; then
	sleep "${HEALTHZ_RETRY_DELAY:-20}"
	if [ "$(status)" != 200 ]; then
		[ "$previous" = failure ] || echo down
		exit 1
	fi
fi
[ "$previous" = failure ] && echo up
exit 0
