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
	mkdir -p "$work/root/staging" "$work/c" "$work/net" "$work/stopped"
	cat >"$work/docker" <<'FAKE'
#!/usr/bin/env bash
# State: $FAKE_DIR/c/<compose project>/<container id> holds the container's image, and
# $FAKE_DIR/net/<container id> the edge alias and network it joined, and $FAKE_DIR/stopped/<id>
# marks a container that exists but does not run.
echo "$*" >>"$FAKE_DIR/calls"
project=""
prev=""
for arg in "$@"; do
	[ "$prev" = -p ] && project=$arg
	prev=$arg
done
state=$FAKE_DIR/c
case "$1" in
pull) exit "${FAKE_PULL:-0}" ;;
inspect)
	id=${!#}
	file=$(find "$state" -type f -name "$id" | head -1)
	[ -n "$file" ] || { echo "Error: No such object: $id" >&2; exit 1; }
	if [ "$(cat "$file")" = "$FAKE_NEW_IMAGE" ]; then echo "${FAKE_NEW_HEALTH:-healthy}"; else echo healthy; fi
	;;
exec) exit "${FAKE_SMOKE:-0}" ;;
network)
	[ "${FAKE_CONNECT_FAILS:-0}" = 1 ] && { echo "Error: network $5 not found" >&2; exit 1; }
	echo "$4 $5" >"$FAKE_DIR/net/$6"
	;;
stop)
	shift
	for id in "$@"; do touch "$FAKE_DIR/stopped/$id"; done
	;;
rm)
	shift
	for id in "$@"; do find "$state" -type f -name "$id" -delete; rm -f "$FAKE_DIR/net/$id" "$FAKE_DIR/stopped/$id"; done
	;;
compose)
	mkdir -p "$state/$project"
	case " $* " in
	*" run "*) exit "${FAKE_MIGRATE:-0}" ;;
	*" ps "*)
		[ "${FAKE_PS_FAILS:-0}" = 1 ] && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }
		if [ "${FAKE_PS_FAILS_AFTER_UP:-0}" = 1 ] && grep -q "^up " "$FAKE_DIR/calls"; then
			echo "Cannot connect to the Docker daemon" >&2
			exit 1
		fi
		ls "$state/$project"
		;;
	*" rm "*)
		# Without --stop, compose removes only the containers that are not running.
		for file in "$state/$project"/*; do
			[ -e "$file" ] || continue
			id=$(basename "$file")
			if [ -e "$FAKE_DIR/stopped/$id" ]; then rm -f "$file" "$FAKE_DIR/stopped/$id" "$FAKE_DIR/net/$id"; fi
		done
		;;
	*" up "*)
		# Like compose, up starts every stopped container of the service again.
		for file in "$state/$project"/*; do
			[ -e "$file" ] && rm -f "$FAKE_DIR/stopped/$(basename "$file")"
		done
		want=$(grep -oE 'scale [a-z]+=[0-9]+' <<<"$*" | cut -d= -f2)
		while [ "$(find "$state/$project" -type f | wc -l | tr -d ' ')" -lt "${want:-1}" ]; do
			n=$(($(cat "$FAKE_DIR/next" 2>/dev/null || echo 0) + 1))
			echo "$n" >"$FAKE_DIR/next"
			echo "$TERKA_IMAGE" >"$state/$project/c$n"
		done
		echo "up $TERKA_IMAGE $*" >>"$FAKE_DIR/calls"
		# Compose can create the container and still fail to start it.
		if [ "$TERKA_IMAGE" = "$FAKE_NEW_IMAGE" ] && [ "${FAKE_UP_FAILS:-0}" = 1 ]; then exit 1; fi
		;;
	esac
	;;
esac
FAKE
	chmod +x "$work/docker"
}

# seed <project> <image>: a container already running before the deploy, with the next free id.
seed() {
	local n
	n=$(($(cat "$work/next" 2>/dev/null || echo 0) + 1))
	echo "$n" >"$work/next"
	mkdir -p "$work/c/$1"
	echo "$2" >"$work/c/$1/c$n"
}
# alive <project>: the images of the containers left in a project, sorted, space separated.
alive() { cat "$work/c/$1"/* 2>/dev/null | sort | tr '\n' ' ' | sed 's/ $//'; }
# id_of <project> <image>: the id of the container running that image.
id_of() { grep -lx "$2" "$work/c/$1"/* 2>/dev/null | head -1 | xargs -n1 basename 2>/dev/null; }
net_of() { cat "$work/net/$1" 2>/dev/null; }
joined_edge() { calls | grep -q "^network connect"; }

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

# with_old <service>: a world where the service already runs $OLD in staging.
with_old() {
	new_world
	echo "$OLD" >"$work/root/staging/$1.current"
	seed "terka-staging-$1" "ghcr.io/dikym/terka-$1:$OLD"
}

with_old api
deploy api staging "$NEW"
api_deployed() { [ "$code" = 0 ] && [ "$(current api)" = "$NEW" ] && [ "$(alive terka-staging-api)" = "$API:$NEW" ]; }
check "a healthy api deploy records the new version" api_deployed
api_joined() { [ "$(net_of "$(id_of terka-staging-api "$API:$NEW")")" = "terka-staging-api terka-edge" ]; }
check "the new api joins the edge network under its environment name" api_joined
joined_first() { [ "$(calls | grep -E '^(network connect|stop c1)' | head -1 | cut -d' ' -f1)" = network ]; }
check "the new api joins the edge before the old one stops" joined_first
beside() { calls | grep "^up " | grep -q -- "--no-recreate .*--scale api=2"; }
check "the new api starts beside the old one" beside
migrate_first() { [ "$(calls | grep -nE " run |^up " | head -1 | grep -c " run ")" = 1 ]; }
check "migrations run before the new api starts" migrate_first
own_project() { calls | grep "^up " | grep -q -- "-p terka-staging-api -f $work/root/compose.api.yaml"; }
check "the api runs in its own compose project" own_project

with_old api
FAKE_PULL=1 deploy api staging "$NEW"
pull_stops() {
	[ "$code" = 1 ] && ! calls | grep -q " run " && [ "$(current api)" = "$OLD" ] &&
		[ "$(alive terka-staging-api)" = "$API:$OLD" ]
}
check "a failed pull stops before migrating" pull_stops

with_old api
FAKE_MIGRATE=1 deploy api staging "$NEW"
migrate_stops() {
	[ "$code" = 1 ] && ! calls | grep -q "^up " && [ "$(current api)" = "$OLD" ] &&
		[ "$(alive terka-staging-api)" = "$API:$OLD" ]
}
check "a failed migration leaves the running api alone" migrate_stops

# old_keeps_serving: the deploy gave up, the new container is gone and the old one never stopped.
old_keeps_serving() {
	[ "$code" = 2 ] && [ "$(alive terka-staging-api)" = "$API:$OLD" ] && ! calls | grep -q "^stop c1" &&
		[ "$(current api)" = "$OLD" ] && grep -q "keeps serving" "$work/out"
}

with_old api
FAKE_NEW_HEALTH=unhealthy deploy api staging "$NEW"
unhealthy_discarded() { old_keeps_serving && ! joined_edge; }
check "an unhealthy api is discarded and the old one keeps serving" unhealthy_discarded

with_old api
FAKE_UP_FAILS=1 deploy api staging "$NEW"
check "an api that fails to start is discarded and the old one keeps serving" unhealthy_discarded

with_old api
FAKE_CONNECT_FAILS=1 deploy api staging "$NEW"
check "an api that cannot join the edge is discarded and the old one keeps serving" old_keeps_serving

nothing_left() { [ "$code" = 3 ] && [ -z "$(alive terka-staging-api)" ] && [ ! -e "$work/root/staging/api.current" ]; }

new_world
FAKE_NEW_HEALTH=starting deploy api staging "$NEW"
check "an api that never turns healthy on a first deploy leaves nothing running" nothing_left

new_world
FAKE_UP_FAILS=1 deploy api staging "$NEW"
check "a first api that fails to start leaves nothing running" nothing_left

new_world
deploy api staging "$NEW"
first_one() {
	[ "$code" = 0 ] && calls | grep "^up " | grep -q -- "--scale api=1" &&
		[ "$(alive terka-staging-api)" = "$API:$NEW" ] && api_joined
}
check "a first healthy api deploy starts one container" first_one

with_old api
seed terka-staging-api "$API:$OLD"
deploy api staging "$NEW"
leftovers_gone() {
	[ "$code" = 0 ] && calls | grep "^up " | grep -q -- "--scale api=3" &&
		[ "$(alive terka-staging-api)" = "$API:$NEW" ]
}
check "every leftover api container stops once the new one is healthy" leftovers_gone

with_old api
mkdir "$work/root/staging/api.lock"
deploy api staging "$NEW"
locked_out() {
	[ "$code" = 1 ] && ! calls | grep -q "^pull" && [ "$(alive terka-staging-api)" = "$API:$OLD" ] &&
		grep -q "another deploy" "$work/out" && [ -d "$work/root/staging/api.lock" ]
}
check "a second deploy of the same service is refused while one runs" locked_out

with_old api
mkdir "$work/root/staging/web.lock"
deploy api staging "$NEW"
released() { [ "$code" = 0 ] && [ ! -e "$work/root/staging/api.lock" ]; }
check "a deploy of one service ignores the other's lock and releases its own" released

with_old api
FAKE_NEW_HEALTH=unhealthy deploy api staging "$NEW"
released_on_failure() { [ "$code" = 2 ] && [ ! -e "$work/root/staging/api.lock" ]; }
check "a failed deploy releases its lock" released_on_failure

with_old api
deploy api staging "$NEW"
smoked_first() { [ "$(calls | grep -E '^(exec|network connect)' | head -1)" = "exec c2 /terka smoke" ]; }
check "the new api passes the smoke test before it joins the edge" smoked_first

with_old api
FAKE_SMOKE=1 deploy api staging "$NEW"
smoke_failed() { old_keeps_serving && ! joined_edge && grep -q "failed the smoke test" "$work/out"; }
check "an api that fails the smoke test is discarded and the old one keeps serving" smoke_failed

new_world
FAKE_SMOKE=1 deploy api staging "$NEW"
first_smoke_failed() { nothing_left && ! joined_edge && grep -q "failed the smoke test" "$work/out"; }
check "a first api that fails the smoke test leaves nothing running" first_smoke_failed

with_old web
FAKE_NEW_IMAGE="$WEB:$NEW" deploy web staging "$NEW"
no_web_smoke() { [ "$code" = 0 ] && ! calls | grep -q "^exec"; }
check "a web deploy runs no smoke test" no_web_smoke

OLDER=3333333333333333333333333333333333333333
with_old api
seed terka-staging-api "$API:$OLDER"
touch "$work/stopped/c2"
FAKE_NEW_HEALTH=unhealthy deploy api staging "$NEW"
leftover_stays_down() {
	[ "$code" = 2 ] && [ "$(alive terka-staging-api)" = "$API:$OLD" ] &&
		[ "$(calls | grep -E ' rm -f| up ' | head -1 | grep -c ' rm -f')" = 1 ]
}
check "a stopped leftover is removed before the new api starts, so it never serves again" leftover_stays_down

with_old api
FAKE_PS_FAILS=1 deploy api staging "$NEW"
unlisted() { [ "$code" = 1 ] && ! calls | grep -q "^up " && [ "$(alive terka-staging-api)" = "$API:$OLD" ]; }
check "a deploy that cannot list the running containers changes nothing" unlisted

with_old api
FAKE_PS_FAILS_AFTER_UP=1 deploy api staging "$NEW"
lost_track() {
	[ "$code" = 2 ] && ! joined_edge && ! calls | grep -q "^stop c1" &&
		[ -e "$work/c/terka-staging-api/c1" ] && [ "$(current api)" = "$OLD" ]
}
check "a deploy that loses track of the new api never stops the old one" lost_track

with_old web
echo "$OLD" >"$work/root/staging/api.current"
seed terka-staging-api "$API:$OLD"
FAKE_NEW_IMAGE="$WEB:$NEW" deploy web staging "$NEW"
web_deployed() {
	[ "$code" = 0 ] && [ "$(current web)" = "$NEW" ] && ! calls | grep -q " run " &&
		[ "$(current api)" = "$OLD" ] && [ "$(alive terka-staging-api)" = "$API:$OLD" ] &&
		[ "$(alive terka-staging-web)" = "$WEB:$NEW" ] &&
		[ "$(net_of "$(id_of terka-staging-web "$WEB:$NEW")")" = "terka-staging-web terka-edge" ] &&
		calls | grep "^up " | grep -q -- "-p terka-staging-web -f $work/root/compose.web.yaml"
}
check "a web deploy records its version, skips migrations and leaves the api alone" web_deployed

with_old web
echo "$OLD" >"$work/root/staging/api.current"
seed terka-staging-api "$API:$OLD"
FAKE_NEW_IMAGE="$WEB:$NEW" FAKE_NEW_HEALTH=unhealthy deploy web staging "$NEW"
web_discarded() {
	[ "$code" = 2 ] && [ "$(alive terka-staging-web)" = "$WEB:$OLD" ] && [ "$(current web)" = "$OLD" ] &&
		[ "$(alive terka-staging-api)" = "$API:$OLD" ] && [ "$(current api)" = "$OLD" ]
}
check "an unhealthy web build is discarded without touching the api" web_discarded

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
