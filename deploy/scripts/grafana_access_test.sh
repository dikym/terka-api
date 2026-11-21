#!/usr/bin/env bash
# Runs the pinned Grafana image with deploy/obs/grafana/grafana.ini and a test RSA key in place of the
# Cloudflare Access JWKS, and checks that only a correctly signed, unexpired token for the expected
# audience opens a session. Caddy's header gate is the first check; this is the second.
set -uo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

[ -f "$repo/deploy/obs/grafana/grafana.ini" ] || { echo "deploy/obs/grafana/grafana.ini is missing"; exit 1; }
image=$(sed -n 's|^    image: \(grafana/grafana:.*\)$|\1|p' "$repo/deploy/compose.obs.yaml")
[ -n "$image" ] || { echo "grafana image not found in deploy/compose.obs.yaml"; exit 1; }

work=$(mktemp -d "$repo/.grafana-access-test.XXXXXX")
name=terka-grafana-access-test-$RANDOM$RANDOM
cleanup() {
	docker rm -f "$name" >/dev/null 2>&1
	rm -r "$work"
}
trap cleanup EXIT

{
	openssl genrsa -out "$work/key.pem" 2048 &&
		openssl rsa -in "$work/key.pem" -pubout -out "$work/pub.pem" &&
		openssl genrsa -out "$work/other.pem" 2048
} 2>/dev/null || { echo "key generation failed"; exit 1; }
# The container runs as a non-root user.
chmod 644 "$work/pub.pem"

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
# token <private key> <claims json>
token() {
	local head body
	head=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
	body=$(printf '%s' "$2" | b64url)
	printf '%s.%s.%s' "$head" "$body" "$(printf '%s.%s' "$head" "$body" | openssl dgst -sha256 -sign "$1" | b64url)"
}
now=$(date +%s)
# claims <aud json> <exp>
claims() { printf '{"aud":%s,"sub":"u1","email":"owner@example.test","exp":%s}' "$1" "$2"; }

docker run -d --name "$name" --read-only --tmpfs /tmp --tmpfs /var/lib/grafana:uid=472 -p 127.0.0.1:0:3000 \
	-e GF_AUTH_JWT_KEY_FILE=/test/pub.pem \
	-e GF_AUTH_JWT_EXPECT_CLAIMS='{"aud":"test-aud"}' \
	-v "$repo/deploy/obs/grafana/grafana.ini:/etc/grafana/grafana.ini:ro" \
	-v "$work/pub.pem:/test/pub.pem:ro" \
	"$image" >/dev/null || { echo "grafana failed to start"; exit 1; }
port=$(docker port "$name" 3000/tcp | head -n1 | sed 's/.*://')

ready=
for _ in $(seq 1 120); do
	[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/health")" = 200 ] && { ready=1; break; }
	sleep 0.5
done
[ -n "$ready" ] || { echo "grafana did not become ready"; docker logs "$name" 2>&1 | tail -n 20; exit 1; }

# get <path> [jwt]: prints the body, then the status on its own line.
get() { curl -s -w '\n%{http_code}' ${2:+-H "Cf-Access-Jwt-Assertion: $2"} "http://127.0.0.1:$port$1"; }
# expect <name> <status> <jwt>
expect() {
	local out code
	out=$(get /api/user "$3")
	code=${out##*$'\n'}
	if [ "$code" = "$2" ]; then pass "$1"; else fail "$1" "want $2, got $code"; fi
}

valid=$((now + 300))
key=$work/key.pem
good=$(token "$key" "$(claims '"test-aud"' "$valid")")
out=$(get /api/user "$good")
if [ "${out##*$'\n'}" = 200 ] && printf '%s' "$out" | grep -q '"login":"owner@example.test"'; then
	pass "valid token with a string aud opens a session as the token's email"
else
	fail "valid token with a string aud opens a session as the token's email" "got: $out"
fi
expect "valid token with an array aud opens a session" 200 "$(token "$key" "$(claims '["test-aud","other-aud"]' "$valid")")"

out=$(get /api/user/orgs "$good")
if printf '%s' "$out" | grep -q '"role":"Admin"'; then pass "the session is org Admin"; else fail "the session is org Admin" "got: $out"; fi

expect "another aud is refused" 401 "$(token "$key" "$(claims '"other-aud"' "$valid")")"
expect "an array aud without the expected one is refused" 401 "$(token "$key" "$(claims '["other-aud"]' "$valid")")"
expect "a token signed by another key is refused" 401 "$(token "$work/other.pem" "$(claims '"test-aud"' "$valid")")"
expect "an expired token is refused" 401 "$(token "$key" "$(claims '"test-aud"' "$((now - 300))")")"
expect "a token without aud is refused" 401 "$(token "$key" "$(printf '{"sub":"u1","email":"owner@example.test","exp":%s}' "$valid")")"
unsigned="$(printf '{"alg":"none","typ":"JWT"}' | b64url).$(claims '"test-aud"' "$valid" | b64url)."
expect "an unsigned token is refused" 401 "$unsigned"
expect "no header gives no session" 401 ""

[ "$failures" = 0 ] || { echo "$failures failed"; exit 1; }
