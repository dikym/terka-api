#!/usr/bin/env bash
# Runs the repo's Alertmanager configuration against a stand-in for Discord and checks the messages
# it sends: a plain heading, the value, when it started, a dashboard link, and how long it lasted.
set -uo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
alertmanager=$(sed -n 's|^ *image: *\(prom/alertmanager[^ ]*\).*|\1|p' "$repo/deploy/compose.obs.yaml")
catcher_image=python:3.13-alpine
failures=0
pass() { echo "ok   $1"; }
fail() {
	echo "FAIL $1${2:+ ($2)}"
	failures=$((failures + 1))
}

suffix=$RANDOM$RANDOM
network=terka-alert-test-$suffix
catcher=terka-alert-catcher-$suffix
manager=terka-alert-manager-$suffix
work=$(mktemp -d "$repo/.alert-test.XXXXXX")
cleanup() {
	docker rm -f "$catcher" "$manager" >/dev/null 2>&1
	docker network rm "$network" >/dev/null 2>&1
	rm -r "$work"
}
trap cleanup EXIT

# The repo's file, with its secrets pointed at the stand-in and the route told not to wait.
mkdir -p "$work/secrets"
echo "http://$catcher:8080/hook" >"$work/secrets/discord-staging"
echo "http://$catcher:8080/hook" >"$work/secrets/discord-production"
awk '{ sub("/etc/alertmanager/secrets", "/etc/alertmanager/test-secrets"); print }
	/^route:/ { print "  group_wait: 1s"; print "  group_interval: 1s" }' \
	"$repo/deploy/obs/alertmanager/alertmanager.yml" >"$work/alertmanager.yml"
chmod -R a+rX "$work"

docker network create "$network" >/dev/null
docker run -d --name "$catcher" --network "$network" "$catcher_image" python3 -c '
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        with open("/tmp/out", "ab") as f:
            f.write(body + b"\n")
        self.send_response(204)
        self.end_headers()
http.server.HTTPServer(("", 8080), H).serve_forever()' >/dev/null
docker run -d --name "$manager" --network "$network" \
	-v "$work/alertmanager.yml:/etc/alertmanager/alertmanager.yml:ro" \
	-v "$work/secrets:/etc/alertmanager/test-secrets:ro" \
	"$alertmanager" --config.file=/etc/alertmanager/alertmanager.yml --cluster.listen-address= >/dev/null

for _ in $(seq 30); do
	docker exec "$manager" amtool --alertmanager.url=http://localhost:9093 alert >/dev/null 2>&1 && break
	sleep 1
done

# GNU date reads -d @epoch, BSD date reads -r epoch.
stamp() { date -d "@$1" "$2" 2>/dev/null || date -r "$1" "$2"; }
at() { TZ=UTC stamp "$(($(date +%s) + $1))" +%Y-%m-%dT%H:%M:%SZ; }
send() { docker exec "$manager" amtool --alertmanager.url=http://localhost:9093 alert add "$@" >/dev/null; }
# A message body spans several lines, so count messages, not lines.
received() { docker exec "$catcher" sh -c 'grep -c "\"embeds\"" /tmp/out' 2>/dev/null || echo 0; }
wait_for() {
	for _ in $(seq 30); do
		[ "$(received)" -ge "$1" ] && return
		sleep 1
	done
}

# Alertmanager only reports a resolution for an alert it has reported firing, so each one that should
# resolve fires first and is sent again with an end time. Every time is fixed before any waiting, so a
# slow runner cannot stretch the durations being checked.
memory_start=$(at -13200)
hours_start=$(at -7200)
blip_start=$(at -20)
mixed_start=$(at -600)
now=$(at 0)
api_epoch=$(($(date +%s) - 60))
api_wib=$(TZ=Asia/Jakarta stamp "$api_epoch" +%H:%M)
send ApiDown env=staging severity=critical --annotation=title="API is not reporting" \
	--annotation=summary="No metrics from the API for 5 minutes." --start="$(TZ=UTC stamp "$api_epoch" +%Y-%m-%dT%H:%M:%SZ)"
send DiskFull env=host severity=warning --annotation=title="Disk almost full" \
	--annotation=summary="15% disk space free." --start="$(at -60)"
send ProdDown env=production severity=critical --annotation=title="Production check" \
	--annotation=summary="Production is failing." --start="$(at -60)"
send Envless severity=warning --annotation=title="Envless check" \
	--annotation=summary="A rule without an environment." --start="$(at -60)"
send Severity env=staging severity=warning kind=a --annotation=title="Severity mix" \
	--annotation=summary="The warning half." --start="$(at -60)"
send Severity env=staging severity=critical kind=b --annotation=title="Severity mix" \
	--annotation=summary="The critical half." --start="$(at -60)"
memory() { send MemoryLow env=staging severity=warning --annotation=title="Memory low" \
	--annotation=summary="100MiB memory available." --start="$memory_start" "$@"; }
hours() { send Hours env=staging severity=warning --annotation=title="Two hours" \
	--annotation=summary="Lasting a round two hours." --start="$hours_start" "$@"; }
blip() { send Blip env=staging severity=warning --annotation=title="Short blip" \
	--annotation=summary="Something happened briefly." --start="$blip_start" "$@"; }
# The resolved half sorts first (kind=a), so the heading must still come from the firing half.
mixed() { send Mixed env=staging severity=critical kind="$1" --annotation=title="$2" \
	--annotation=summary="$3" --start="$mixed_start" "${@:4}"; }
memory
hours
blip
mixed a "Mixed group, old half" "Over already."
mixed b "Mixed group" "Still failing."
wait_for 10
memory --end="$now"
hours --end="$now"
blip --end="$now"
mixed a "Mixed group, old half" "Over already." --end="$now"
# Wait for the four resolutions themselves rather than a message count, which extra flushes can reach early.
for _ in $(seq 30); do
	[ "$(docker exec "$catcher" sh -c 'grep -c Lasted /tmp/out' 2>/dev/null || echo 0)" -ge 4 ] && break
	sleep 1
done

docker exec "$catcher" cat /tmp/out >"$work/out" 2>/dev/null
# The latest message for a heading is the one that matters: a resolution follows its firing.
message() { jq -c --arg t "$1" '.embeds[0] | select(.title | contains($t))' "$work/out" | tail -1; }
title() { message "$1" | jq -r '.title // empty'; }
body() { message "$1" | jq -r '.description // empty'; }

check() {
	local name=$1 detail=$2
	shift 2
	if "$@"; then pass "$name"; else fail "$name" "$detail"; fi
}
has() { grep -q"${3:-}" -- "$2" <<<"$1"; }
lacks() { ! grep -q -- "$2" <<<"$1"; }

t=$(title "API is not reporting")
check "a firing critical alert has a plain heading" "$t" [ "$t" = "🔴 Staging · API is not reporting" ]
b=$(body "API is not reporting")
check "a firing alert gives its value" "$b" has "$b" "No metrics from the API for 5 minutes."
check "a firing alert says when it started, in WIB" "$b" has "$b" "Since $api_wib WIB" F
check "a firing alert links the dashboard for its environment" "$b" has "$b" "https://grafana.terka.my.id/d/terka/terka?var-env=staging" F

t=$(title "Disk almost full")
check "a host alert is labelled VPS" "$t" [ "$t" = "🟡 VPS · Disk almost full" ]
b=$(body "Disk almost full")
check "a host alert links the dashboard" "$b" has "$b" "https://grafana.terka.my.id/d/terka/terka" F
check "a host alert links no environment" "$b" lacks "$b" "var-env"

t=$(title "Memory low")
check "a resolved alert says so in its heading" "$t" [ "$t" = "🟢 Resolved · Staging · Memory low" ]
b=$(body "Memory low")
check "a resolved alert says how long it lasted" "$b" has "$b" "^Lasted 3h40m\.$" E
check "a resolved alert leaves out the firing text" "$b" lacks "$b" "memory available"

b=$(body "Short blip")
check "a blip under a minute reads as under a minute" "$b" has "$b" "^Lasted under a minute\.$" E

b=$(body "Two hours")
check "a duration of whole hours reads as hours" "$b" has "$b" "^Lasted 2h\\.$" E

t=$(title "Production check")
check "a production alert is labelled Production" "$t" [ "$t" = "🔴 Production · Production check" ]

t=$(title "Envless check")
check "an alert without an environment has a heading without one" "$t" [ "$t" = "🟡 Envless check" ]
b=$(body "Envless check")
check "an alert without an environment links no environment" "$b" lacks "$b" "var-env"
check "an alert without an environment still links the dashboard" "$b" has "$b" "https://grafana.terka.my.id/d/terka/terka" F

t=$(title "Severity mix")
check "a group with a critical alert is marked critical" "$t" [ "$t" = "🔴 Staging · Severity mix" ]

t=$(title "Mixed group")
check "a partly resolved group is headed by its firing alert" "$t" [ "$t" = "🔴 Staging · Mixed group" ]
b=$(body "Mixed group")
check "a mixed group shows the firing alert" "$b" has "$b" "Still failing."
check "a mixed group shows the resolved alert" "$b" has "$b" "Mixed group, old half: Lasted 10m." F

if [ "$failures" -gt 0 ]; then
	echo "$failures failed"
	echo "headings received:"
	jq -r '.embeds[0].title' "$work/out" 2>/dev/null
	docker logs --tail 20 "$manager" 2>&1
	exit 1
fi
echo "all passed"
