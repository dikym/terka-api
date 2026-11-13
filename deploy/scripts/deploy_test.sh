#!/usr/bin/env bash
# Tests deploy.sh against a fake docker that records every call and plays one scenario.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
NEW=1111111111111111111111111111111111111111
OLD=2222222222222222222222222222222222222222
API=ghcr.io/dikym/terka-api
WEB=ghcr.io/dikym/terka-web
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
	*" up "*)
		# Compose may already have removed the old container when starting the new one fails.
		if [ "$TERKA_IMAGE" = "$FAKE_NEW_IMAGE" ] && [ "${FAKE_UP_FAILS:-0}" = 1 ]; then
			rm -f "$FAKE_DIR/running"
			exit 1
		fi
		echo "$TERKA_IMAGE" >"$FAKE_DIR/running" && echo "up $TERKA_IMAGE $*" >>"$FAKE_DIR/calls"
		;;
	*" ps "*) echo container-id ;;
	*" stop "*) rm -f "$FAKE_DIR/running" ;;
	esac
	;;
esac
FAKE
	chmod +x "$work/docker"
}

# deploy [args...]: runs deploy.sh in the current world and sets $code. FAKE_NEW_IMAGE names the
# image whose health the scenario controls; it defaults to the new API image.
deploy() {
	DOCKER="$work/docker" TERKA_ROOT="$work/root" TERKA_HEALTH_TIMEOUT=2 \
		FAKE_DIR="$work" FAKE_NEW_IMAGE="${FAKE_NEW_IMAGE:-$API:$NEW}" \
		bash "$here/deploy.sh" "$@" >"$work/out" 2>&1
	code=$?
}

calls() { cat "$work/calls" 2>/dev/null; }
current() { cat "$work/root/staging/$1.current" 2>/dev/null || echo "<none>"; }
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1 (exit $code; api $(current api); web $(current web))"
	sed 's/^/     | /' "$work/out"
	failures=$((failures + 1))
}
check() {
	local name=$1
	shift
	if "$@"; then pass "$name"; else fail "$name"; fi
}
refused() { [ "$code" = 64 ] && [ -z "$(calls)" ]; }

new_world
deploy api prod "$NEW"
check "an unknown environment is refused before docker runs" refused

new_world
deploy api staging abc
check "a tag that is not a full commit SHA is refused" refused

new_world
deploy api staging
check "a missing tag is refused" refused

new_world
deploy db staging "$NEW"
check "an unknown service is refused" refused

new_world
echo "$OLD" >"$work/root/staging/api.current"
deploy api staging "$NEW"
api_deployed() { [ "$code" = 0 ] && [ "$(current api)" = "$NEW" ]; }
check "a healthy api deploy records the new version" api_deployed
migrate_first() { [ "$(calls | grep -nE " run |^up " | head -1 | grep -c " run ")" = 1 ]; }
check "migrations run before the api is replaced" migrate_first
own_project() { calls | grep "^up " | grep -q -- "-p terka-staging-api -f $work/root/compose.api.yaml"; }
check "the api runs in its own compose project" own_project

new_world
echo "$OLD" >"$work/root/staging/api.current"
FAKE_PULL=1 deploy api staging "$NEW"
pull_stops() { [ "$code" = 1 ] && ! calls | grep -q " run " && [ "$(current api)" = "$OLD" ]; }
check "a failed pull stops before migrating" pull_stops

new_world
echo "$OLD" >"$work/root/staging/api.current"
FAKE_MIGRATE=1 deploy api staging "$NEW"
migrate_stops() { [ "$code" = 1 ] && ! calls | grep -q "^up " && [ "$(current api)" = "$OLD" ]; }
check "a failed migration leaves the running api alone" migrate_stops

new_world
echo "$OLD" >"$work/root/staging/api.current"
FAKE_NEW_HEALTH=unhealthy deploy api staging "$NEW"
rolled_back() { [ "$code" = 2 ] && calls | grep -q "^up $API:$OLD " && [ "$(current api)" = "$OLD" ]; }
check "an unhealthy api is rolled back to the previous one" rolled_back

new_world
FAKE_NEW_HEALTH=starting deploy api staging "$NEW"
first_stopped() {
	[ "$code" = 3 ] && [ "$(calls | grep -c "^up ")" = 1 ] && calls | grep -q " stop " &&
		[ ! -e "$work/root/staging/api.current" ]
}
check "an api that never turns healthy times out and stops on a first deploy" first_stopped

new_world
echo "$OLD" >"$work/root/staging/api.current"
FAKE_UP_FAILS=1 deploy api staging "$NEW"
check "an api that fails to start is rolled back to the previous one" rolled_back

new_world
FAKE_UP_FAILS=1 deploy api staging "$NEW"
first_failed() { [ "$code" = 3 ] && [ ! -e "$work/root/staging/api.current" ]; }
check "a first api that fails to start reports a failed first deploy" first_failed

new_world
echo "$OLD" >"$work/root/staging/web.current"
FAKE_NEW_IMAGE="$WEB:$NEW" deploy web staging "$NEW"
web_deployed() {
	[ "$code" = 0 ] && [ "$(current web)" = "$NEW" ] && ! calls | grep -q " run " &&
		[ ! -e "$work/root/staging/api.current" ] &&
		calls | grep "^up " | grep -q -- "-p terka-staging-web -f $work/root/compose.web.yaml"
}
check "a web deploy records its version, skips migrations and leaves the api alone" web_deployed

new_world
echo "$OLD" >"$work/root/staging/web.current"
echo "$OLD" >"$work/root/staging/api.current"
FAKE_NEW_IMAGE="$WEB:$NEW" FAKE_NEW_HEALTH=unhealthy deploy web staging "$NEW"
web_rolled_back() {
	[ "$code" = 2 ] && calls | grep -q "^up $WEB:$OLD " && [ "$(current web)" = "$OLD" ] &&
		[ "$(current api)" = "$OLD" ]
}
check "an unhealthy web build is rolled back without touching the api" web_rolled_back

new_world
FAKE_NEW_IMAGE="$WEB:$NEW" SSH_ORIGINAL_COMMAND="staging $NEW" deploy web
forced_web() { [ "$code" = 0 ] && [ "$(current web)" = "$NEW" ]; }
check "the forced command fixes the service and SSH supplies the rest" forced_web

new_world
SSH_ORIGINAL_COMMAND="api staging $NEW" deploy web
check "a web key cannot deploy the api" refused

new_world
SSH_ORIGINAL_COMMAND="api staging $NEW" deploy
check "a forced command without a service is refused" refused

new_world
SSH_ORIGINAL_COMMAND="staging $NEW; rm -rf /" deploy api
check "a forced SSH command with anything extra is refused" refused

new_world
SSH_ORIGINAL_COMMAND="staging \$(id)" deploy api
check "a forced SSH command is never evaluated by a shell" refused

new_world
SSH_ORIGINAL_COMMAND="production $OLD" deploy api staging "$NEW"
full_args_win() { [ "$code" = 0 ] && [ "$(current api)" = "$NEW" ]; }
check "full arguments ignore the SSH command" full_args_win

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	exit 1
fi
echo "all passed"
