#!/usr/bin/env bash
# The Caddy base image's version lives in deploy/caddy/VERSION (<caddy release>-p<patch level>).
# Checks that the Dockerfile builds that release on that release's image, and that the VPS edge,
# the local stack and the CI stand-ins run that exact tag. terka-web/Dockerfile names the same tag
# and is bumped by hand with it.
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
if grep -q "image: ghcr.io/dikym/terka-caddy:$version$" "$repo/deploy/compose.local.yaml"; then
	pass "the local stack's edge runs terka-caddy:$version"
else
	fail "the local stack's edge runs terka-caddy:$version" "$(grep -n 'terka-caddy' "$repo/deploy/compose.local.yaml" | tr '\n' ' ')"
fi
stand_ins=$(grep -E '^\s*image:' "$repo/deploy/compose.stand-in.yaml")
if [ -n "$stand_ins" ] && ! grep -qv "image: ghcr.io/dikym/terka-caddy:$version$" <<<"$stand_ins"; then
	pass "every stand-in runs terka-caddy:$version"
else
	fail "every stand-in runs terka-caddy:$version" "$(tr '\n' ' ' <<<"$stand_ins")"
fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
