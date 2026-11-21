#!/usr/bin/env bash
# Runs healthz-check.sh against a local server and a closed port for every previous conclusion.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

free_port() { python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }

work=$(mktemp -d)
server_pid=
cleanup() {
	[ -n "$server_pid" ] && { kill "$server_pid"; wait "$server_pid"; } 2>/dev/null
	rm -r "$work"
}
trap cleanup EXIT

closed=$(free_port)
port=$(free_port)
echo ok >"$work/healthz"
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$work" >/dev/null 2>&1 &
server_pid=$!
for _ in $(seq 50); do
	curl -s -o /dev/null "http://127.0.0.1:$port/healthz" && break
	sleep 0.1
done

export HEALTHZ_RETRY_DELAY=0
# check <name> <url> <previous> <expected output> <expected exit>
check() {
	local out code
	out=$(bash "$here/healthz-check.sh" "$2" "$3") && code=0 || code=$?
	if [ "$out" = "$4" ] && [ "$code" = "$5" ]; then pass "$1"; else fail "$1" "output '$out', exit $code"; fi
}

healthy=http://127.0.0.1:$port/healthz
check "healthy after success is silent" "$healthy" success "" 0
check "healthy after failure reports up" "$healthy" failure up 0
check "healthy on the first run is silent" "$healthy" "" "" 0
check "closed port after success reports down" "http://127.0.0.1:$closed/healthz" success down 1
check "closed port after failure is silent" "http://127.0.0.1:$closed/healthz" failure "" 1
check "closed port on the first run reports down" "http://127.0.0.1:$closed/healthz" "" down 1
check "non-200 answer counts as unhealthy" "http://127.0.0.1:$port/missing" success down 1

[ "$failures" -eq 0 ] || { echo "$failures failed"; exit 1; }
