#!/usr/bin/env bash
# Runs the edge routes (deploy/Caddyfile.routes) in the edge image built from deploy/caddy in front of two stand-in
# containers named like an environment's api and web, and checks which one answers each path.
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

docker build -q -t terka-edge:test "$repo/deploy/caddy" >/dev/null || { echo "edge image build failed"; exit 1; }
docker network create "$network" >/dev/null
for service in api web; do
	containers+=("$(docker run -d --network "$network" --network-alias "terka-staging-$service" \
		"$stand_in_image" caddy respond --listen :8080 --body "$service")")
done

cat >"$work/Caddyfile" <<'CADDY'
{
	auto_https off
}
import /etc/caddy/Caddyfile.routes
:8443 {
	import routes terka-staging
}
CADDY
containers+=("$(docker run -d --network "$network" -p 127.0.0.1:18095:8443 \
	-v "$work/Caddyfile:/etc/caddy/Caddyfile:ro" \
	-v "$repo/deploy/Caddyfile.routes:/etc/caddy/Caddyfile.routes:ro" \
	terka-edge:test)")

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

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
