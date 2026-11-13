#!/usr/bin/env bash
# Checks a running local stack (`make stack`): one origin on localhost:8800, the API behind /api and
# the health paths, the web build everywhere else.
set -uo pipefail

base=http://localhost:${TERKA_STACK_PORT:-8800}
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}
# get <path>: sets $status, $type and $body.
get() {
	local out
	out=$(curl -s -w '\n%{http_code} %{content_type}' "$base$1")
	body=$(sed '$d' <<<"$out")
	read -r status type <<<"$(tail -1 <<<"$out")"
}
check() {
	local name=$1
	shift
	if "$@"; then pass "$name"; else fail "$name" "status $status, type $type, body ${body:0:80}"; fi
}

get /healthz
check "the API answers its health check" test "$status" = 200 -a "${body:0:15}" = '{"status":"ok",'
get /readyz
check "the API reaches its database" test "$status" = 200
get /
check "the web build serves the home page" test "$status" = 200 -a "$type" = "text/html; charset=utf-8"
html_has_name() { [[ $body == *Terka* ]]; }
check "the home page is Terka's" html_has_name
get /api/nope
check "unknown API paths are JSON 404s" test "$status" = 404 -a "$type" = application/json
get /nope
check "unknown pages get the web 404 page" test "$status" = 404 -a "$type" = "text/html; charset=utf-8"

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
