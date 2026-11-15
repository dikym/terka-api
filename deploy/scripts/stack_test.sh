#!/usr/bin/env bash
# Checks a running local stack (`make stack`): one origin on localhost:8800, the API behind /api and
# the health paths, the web build everywhere else, and a full email sign-in read back from Mailpit.
# TERKA_STACK_URL points it at another origin, such as staging after a deploy. TERKA_STACK_SIGNIN=0
# skips the sign-in, which needs Mailpit and the always-pass Turnstile secret; what is left only
# sends GET requests, so it is safe against a live environment.
set -uo pipefail

base=${TERKA_STACK_URL:-http://localhost:${TERKA_STACK_PORT:-8800}}
mailpit=http://localhost:${TERKA_STACK_MAIL_PORT:-8826}
# Seconds per request: a stalled origin must end in FAIL lines, not in the CI job's own timeout.
timeout=${TERKA_STACK_TIMEOUT:-15}
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}
# get <path> / post <path> <json>: set $status, $type and $body, keeping cookies in $jar.
get() {
	local out
	out=$(curl -s -m "$timeout" -b "$jar" -c "$jar" -w '\n%{http_code} %{content_type}' "$base$1")
	body=$(sed '$d' <<<"$out")
	read -r status type <<<"$(tail -1 <<<"$out")"
}
post() {
	local out
	out=$(curl -s -m "$timeout" -b "$jar" -c "$jar" -H 'Content-Type: application/json' -d "$2" -w '\n%{http_code} %{content_type}' "$base$1")
	body=$(sed '$d' <<<"$out")
	read -r status type <<<"$(tail -1 <<<"$out")"
}
# mailed_code <address>: prints the code in the one message Mailpit holds for the address.
mailed_code() {
	local id
	for _ in $(seq 50); do
		id=$(curl -s -m "$timeout" "$mailpit/api/v1/search?query=to:%22$1%22" | grep -oE '"ID":"[^"]+"' | head -1 | cut -d'"' -f4)
		if [ -n "$id" ]; then
			curl -s -m "$timeout" "$mailpit/api/v1/message/$id" | grep -oE 'Kode masuk: [0-9]{6}' | head -1 | grep -oE '[0-9]{6}'
			return
		fi
		sleep 0.1
	done
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
# Fresh URLs each run: behind Cloudflare a fixed path can be answered from the edge cache, which
# would vouch for an older origin than the one just deployed.
fresh=$(date +%s)$$
get "/?smoke=$fresh"
check "the web build serves the home page" test "$status" = 200 -a "$type" = "text/html; charset=utf-8"
html_has_name() { [[ $body == *Terka* ]]; }
check "the home page is Terka's" html_has_name
get "/api/nope-$fresh"
check "unknown API paths are JSON 404s" test "$status" = 404 -a "$type" = application/json
get "/nope-$fresh"
check "unknown pages get the web 404 page" test "$status" = 404 -a "$type" = "text/html; charset=utf-8"

get /api/auth/session
check "without a session the API says signed out" test "$status" = 401 -a "$type" = application/json
signed_out() { [[ $body == *'"code":"signed_out"'* ]]; }
check "the signed-out answer carries its code" signed_out

if [ "${TERKA_STACK_SIGNIN:-1}" = 1 ]; then
	# Cloudflare's always-pass test secret is the local default; any token passes it.
	email="stack-$(date +%s)-$$@contoh.id"
	post /api/auth/email/request "{\"email\":\"$email\",\"turnstile\":\"XXXX.DUMMY.TOKEN.XXXX\"}"
	check "a code can be requested" test "$status" = 202
	code=$(mailed_code "$email")
	check "the code arrives in Mailpit" test -n "$code"
	post /api/auth/email/verify "{\"email\":\"$email\",\"code\":\"$code\"}"
	check "the code signs in" test "$status" = 200
	get /api/auth/session
	check "the session cookie works through Caddy" test "$status" = 200
	session_is_mine() { [[ $body == *"\"email\":\"$email\""* ]]; }
	check "the session belongs to the address that signed in" session_is_mine
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
