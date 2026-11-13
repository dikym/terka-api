#!/usr/bin/env bash
# The Caddy base image's version lives in deploy/caddy/VERSION (<caddy release>-p<patch level>).
# Checks that the Dockerfile builds that release on that release's image, and that the VPS edge
# runs that exact tag. terka-web/Dockerfile names the same tag and is bumped by hand with it.
set -uo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

version=$(cat "$repo/deploy/caddy/VERSION" 2>/dev/null)
if [[ $version =~ ^([0-9]+\.[0-9]+\.[0-9]+)-p[0-9]+$ ]]; then
	pass "VERSION reads <release>-p<n> ($version)"
else
	fail "VERSION reads <release>-p<n>" "got: ${version:-nothing}"
	echo "$failures failed"
	exit 1
fi
release=${BASH_REMATCH[1]}
dockerfile=$repo/deploy/caddy/Dockerfile

if grep -qE "xcaddy build v$release( |$)" "$dockerfile"; then
	pass "the Dockerfile builds Caddy $release"
else
	fail "the Dockerfile builds Caddy $release" "$(grep -o 'xcaddy build v[^ ]*' "$dockerfile")"
fi
if grep -qx "FROM caddy:$release-alpine" "$dockerfile"; then
	pass "the Dockerfile starts from the caddy:$release-alpine image"
else
	fail "the Dockerfile starts from the caddy:$release-alpine image" "$(grep -E '^FROM caddy' "$dockerfile")"
fi
if grep -q "image: ghcr.io/dikym/terka-caddy:$version$" "$repo/deploy/compose.infra.yaml"; then
	pass "the VPS edge runs terka-caddy:$version"
else
	fail "the VPS edge runs terka-caddy:$version" "$(grep -n 'image:' "$repo/deploy/compose.infra.yaml" | tr '\n' ' ')"
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
