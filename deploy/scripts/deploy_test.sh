#!/usr/bin/env bash
# Tests deploy.sh against a fake docker that records every call and plays one scenario.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
NEW=1111111111111111111111111111111111111111
OLD=2222222222222222222222222222222222222222
IMAGE=ghcr.io/dikym/terka-go
failures=0

new_world() {
	work=$(mktemp -d)
	mkdir -p "$work/root/staging"
	cat >"$work/docker" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >>"$FAKE_DIR/calls"
case "$1" in
pull) exit "${FAKE_PULL:-0}" ;;
inspect)
	if [ "$(cat "$FAKE_DIR/running" 2>/dev/null)" = "$FAKE_NEW_IMAGE" ]; then
		echo "${FAKE_NEW_HEALTH:-healthy}"
	else
		echo healthy
	fi
	;;
compose)
	case " $* " in
	*" run "*) exit "${FAKE_MIGRATE:-0}" ;;
	*" up "*) echo "$TERKA_IMAGE" >"$FAKE_DIR/running" && echo "up $TERKA_IMAGE" >>"$FAKE_DIR/calls" ;;
	*" ps "*) echo container-id ;;
	*" stop "*) rm -f "$FAKE_DIR/running" ;;
	esac
	;;
esac
FAKE
	chmod +x "$work/docker"
}

# deploy [args...]: runs deploy.sh in the current world and sets $code.
deploy() {
	DOCKER="$work/docker" TERKA_ROOT="$work/root" TERKA_HEALTH_TIMEOUT=2 \
		FAKE_DIR="$work" FAKE_NEW_IMAGE="$IMAGE:$NEW" \
		bash "$here/deploy.sh" "$@" >"$work/out" 2>&1
	code=$?
}

calls() { cat "$work/calls" 2>/dev/null; }
current() { cat "$work/root/staging/current" 2>/dev/null || echo "<none>"; }

pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1 (exit $code; current $(current))"
	sed 's/^/     | /' "$work/out"
	failures=$((failures + 1))
}

new_world
deploy prod "$NEW"
if [ $code = 64 ] && [ -z "$(calls)" ]; then pass "an unknown environment is refused before docker runs"; else fail "an unknown environment is refused before docker runs"; fi

new_world
deploy staging abc
if [ $code = 64 ] && [ -z "$(calls)" ]; then pass "a tag that is not a full commit SHA is refused"; else fail "a tag that is not a full commit SHA is refused"; fi

new_world
deploy staging
if [ $code = 64 ] && [ -z "$(calls)" ]; then pass "a missing tag is refused"; else fail "a missing tag is refused"; fi

new_world
echo "$OLD" >"$work/root/staging/current"
deploy staging "$NEW"
if [ $code = 0 ] && [ "$(current)" = "$NEW" ]; then pass "a healthy deploy records the new version"; else fail "a healthy deploy records the new version"; fi
if [ "$(calls | grep -nE " run |^up " | head -1 | grep -c " run ")" = 1 ]; then pass "migrations run before the app is replaced"; else fail "migrations run before the app is replaced"; fi

new_world
echo "$OLD" >"$work/root/staging/current"
FAKE_PULL=1 deploy staging "$NEW"
if [ $code = 1 ] && ! calls | grep -q " run " && [ "$(current)" = "$OLD" ]; then pass "a failed pull stops before migrating"; else fail "a failed pull stops before migrating"; fi

new_world
echo "$OLD" >"$work/root/staging/current"
FAKE_MIGRATE=1 deploy staging "$NEW"
if [ $code = 1 ] && ! calls | grep -q "^up " && [ "$(current)" = "$OLD" ]; then pass "a failed migration leaves the running app alone"; else fail "a failed migration leaves the running app alone"; fi

new_world
echo "$OLD" >"$work/root/staging/current"
FAKE_NEW_HEALTH=unhealthy deploy staging "$NEW"
if [ $code = 2 ] && calls | grep -q "^up $IMAGE:$OLD$" && [ "$(current)" = "$OLD" ]; then pass "an unhealthy version is rolled back to the previous one"; else fail "an unhealthy version is rolled back to the previous one"; fi

new_world
FAKE_NEW_HEALTH=starting deploy staging "$NEW"
if [ $code = 3 ] && [ "$(calls | grep -c "^up ")" = 1 ] && calls | grep -q " stop " && [ ! -e "$work/root/staging/current" ]; then pass "a version that never turns healthy times out and stops on a first deploy"; else fail "a version that never turns healthy times out and stops on a first deploy"; fi

new_world
SSH_ORIGINAL_COMMAND="staging $NEW" deploy
if [ $code = 0 ] && [ "$(current)" = "$NEW" ]; then pass "arguments can arrive through the forced SSH command"; else fail "arguments can arrive through the forced SSH command"; fi

new_world
SSH_ORIGINAL_COMMAND="staging $NEW; rm -rf /" deploy
if [ $code = 64 ] && [ -z "$(calls)" ]; then pass "a forced SSH command with anything extra is refused"; else fail "a forced SSH command with anything extra is refused"; fi

new_world
SSH_ORIGINAL_COMMAND="staging \$(id)" deploy
if [ $code = 64 ] && [ -z "$(calls)" ]; then pass "a forced SSH command is never evaluated by a shell"; else fail "a forced SSH command is never evaluated by a shell"; fi

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
