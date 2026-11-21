#!/usr/bin/env bash
# Runs the edge routes (deploy/Caddyfile.routes) in the Caddy base image built from deploy/caddy, in
# front of two stand-in containers named like an environment's api and web, and checks which one
# answers each path.
set -uo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
stand_in_image=caddy:2.11.9-alpine
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

# A missing file would be mounted by Docker as an empty directory in the repo.
[ -f "$repo/deploy/Caddyfile.routes" ] || { echo "deploy/Caddyfile.routes is missing"; exit 1; }

suffix=$RANDOM$RANDOM
network=terka-edge-test-$suffix
work=$(mktemp -d "$repo/.edge-test.XXXXXX")
containers=()
cleanup() {
	[ ${#containers[@]} -gt 0 ] && docker rm -f "${containers[@]}" >/dev/null 2>&1
	docker network rm "$network" >/dev/null 2>&1
	rm -r "$work"
}
trap cleanup EXIT

edge_image=ghcr.io/dikym/terka-caddy:$(cat "$repo/deploy/caddy/VERSION")
docker build -q -t "$edge_image" "$repo/deploy/caddy" >/dev/null || { echo "caddy base image build failed"; exit 1; }
docker network create "$network" >/dev/null
# stand_in <service>: starts a container answering with the service's name under its edge alias.
stand_in() {
	docker run -d --network "$network" --network-alias "terka-staging-$1" \
		"$stand_in_image" caddy respond --listen :8080 --body "$1"
}
api_id=$(stand_in api)
containers+=("$api_id" "$(stand_in web)")
# Logs every request it receives, so a test can tell a request was never proxied.
grafana_id=$(docker run -d --network "$network" --network-alias grafana "$stand_in_image" \
	caddy respond --listen :3000 --access-log --body grafana)
containers+=("$grafana_id")

cat >"$work/Caddyfile" <<'CADDY'
import /etc/caddy/Caddyfile.routes
{
	auto_https off
	import redacted_logs
}
http://grafana.terka.my.id:8443 {
	import grafana
}
http://broken.terka.my.id:8443 {
	reverse_proxy terka-missing-api:8080
}
:8443 {
	import routes terka-staging
}
CADDY
edge_id=$(docker run -d --network "$network" -p 127.0.0.1:18095:8443 \
	-v "$work/Caddyfile:/etc/caddy/Caddyfile:ro" \
	-v "$repo/deploy/Caddyfile.routes:/etc/caddy/Caddyfile.routes:ro" \
	"$edge_image")
containers+=("$edge_id")

for _ in $(seq 1 50); do
	curl -s -o /dev/null http://127.0.0.1:18095/ && break
	sleep 0.2
done

for pair in /api=api /api/x=api /api/sync/pull/review_logs=api /healthz=api /readyz=api \
	/apix=web /=web /sesi=web /healthzz=web /_app/version.json=web; do
	path=${pair%%=*}
	want=${pair#*=}
	got=$(curl -s "http://127.0.0.1:18095$path")
	if [ "$got" = "$want" ]; then pass "$path goes to $want"; else fail "$path goes to $want" "got: ${got:-nothing}"; fi
done

grafana_get() { curl -s -o /dev/null -w '%{http_code}' -H 'Host: grafana.terka.my.id' "$@" http://127.0.0.1:18095/; }
requests() { docker logs "$grafana_id" 2>&1 | grep -c '"method"'; }
name="grafana without an access token is refused"
code=$(grafana_get)
if [ "$code" = 403 ] && [ "$(requests)" = 0 ]; then pass "$name"; else fail "$name" "got: $code, requests: $(requests)"; fi
name="grafana with an empty access token is refused"
code=$(grafana_get -H 'Cf-Access-Jwt-Assertion;')
if [ "$code" = 403 ] && [ "$(requests)" = 0 ]; then pass "$name"; else fail "$name" "got: $code, requests: $(requests)"; fi
name="grafana with an access token is proxied"
got=$(curl -s -H 'Host: grafana.terka.my.id' -H 'Cf-Access-Jwt-Assertion: x' http://127.0.0.1:18095/)
if [ "$got" = grafana ] && [ "$(requests)" = 1 ]; then pass "$name"; else fail "$name" "got: ${got:-nothing}"; fi

for file in Caddyfile Caddyfile.local; do
	name="$file keeps request details out of its logs"
	if grep -q '^	import redacted_logs$' "$repo/deploy/$file"; then pass "$name"; else fail "$name"; fi
done
# Caddy's error log records the request, and its lines reach Loki.
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: broken.terka.my.id' \
	-H 'Cf-Connecting-Ip: 203.0.113.7' -H 'X-Forwarded-For: 203.0.113.8' \
	-H 'Cf-Access-Jwt-Assertion: jwt-secret-value' -H 'Cf-Access-Authenticated-User-Email: someone@example.com' \
	'http://127.0.0.1:18095/api/auth/google/callback?code=code-secret-value&state=state-secret-value')
docker logs "$edge_id" >"$work/edge.log" 2>&1
name="a failed request is still logged"
if [ "$code" = 502 ] && grep -q '"logger":"http.log.error".*"uri":"/api/auth/google/callback"' "$work/edge.log"; then
	pass "$name"
else
	fail "$name" "got: $code"
fi
for secret in 203.0.113 jwt-secret-value someone@example.com code-secret-value state-secret-value \
	'"remote_ip"' '"client_ip"' '"headers"'; do
	name="the edge log leaves out $secret"
	if grep -qF "$secret" "$work/edge.log"; then fail "$name"; else pass "$name"; fi
done

# A deploy replaces the api container: for a moment no container answers to the alias at all.
docker rm -f "$api_id" >/dev/null
curl -s --max-time 15 http://127.0.0.1:18095/api >"$work/late" &
waiter=$!
sleep 1
api_id=$(stand_in api)
containers+=("$api_id")
wait "$waiter"
got=$(cat "$work/late")
name="a request waits for an api that is being replaced"
if [ "$got" = api ]; then pass "$name"; else fail "$name" "got: ${got:-nothing}"; fi

# The old container closes its listener on SIGTERM but keeps its address in the alias for five more
# seconds, as the API does while it drains, and the new one already answers to the same alias.
docker rm -f "$api_id" >/dev/null
old_id=$(docker run -d --network "$network" --network-alias terka-staging-api "$stand_in_image" \
	sh -c 'caddy respond --listen :8080 --body api & trap "kill $!; sleep 5" TERM; wait')
containers+=("$old_id" "$(stand_in api)")
for _ in $(seq 1 50); do
	[ "$(curl -s http://127.0.0.1:18095/api)" = api ] && break
	sleep 0.2
done
(
	for _ in $(seq 1 30); do
		curl -s --max-time 15 -o /dev/null -w '%{http_code} ' http://127.0.0.1:18095/api
		sleep 0.1
	done
) >"$work/draining" &
waiter=$!
sleep 0.5
docker stop "$old_id" >/dev/null &
stopper=$!
wait "$waiter"
wait "$stopper"
codes=$(cat "$work/draining")
name="a request skips an api that has stopped listening"
if [ "$codes" = "$(printf '200 %.0s' $(seq 1 30))" ]; then pass "$name"; else fail "$name" "got: $codes"; fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
