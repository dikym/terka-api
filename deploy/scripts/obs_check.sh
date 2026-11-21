#!/usr/bin/env bash
# Validates the observability configuration in the same images compose.obs.yaml pins, so a mistake
# fails here and not after it reaches the VPS.
set -euo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
obs="$repo/deploy/obs"
image() { sed -n "s|^ *image: *\(.*$1[^ ]*\).*|\1|p" "$repo/deploy/compose.obs.yaml"; }
prometheus=$(image prom/prometheus)
alertmanager=$(image prom/alertmanager)
alloy=$(image grafana/alloy)
loki=$(image grafana/loki)

docker run --rm --entrypoint promtool -v "$obs/prometheus:/etc/prometheus:ro" "$prometheus" \
	check config /etc/prometheus/prometheus.yml
docker run --rm --entrypoint promtool -v "$obs/prometheus:/etc/prometheus:ro" "$prometheus" \
	test rules /etc/prometheus/rules/terka_test.yml

# The webhook files only exist on the VPS; amtool needs them readable, not real.
docker run --rm --user root --entrypoint sh -v "$obs/alertmanager:/etc/alertmanager:ro" "$alertmanager" -c '
	set -e
	mkdir -p /tmp/secrets
	echo https://discord.invalid/api/webhooks/0/dummy >/tmp/secrets/discord-staging
	echo https://discord.invalid/api/webhooks/0/dummy >/tmp/secrets/discord-production
	sed "s|/etc/alertmanager/secrets|/tmp/secrets|" /etc/alertmanager/alertmanager.yml >/tmp/alertmanager.yml
	amtool check-config /tmp/alertmanager.yml'

for file in "$obs"/alloy/*.alloy; do
	docker run --rm -v "$obs/alloy:/etc/alloy:ro" "$alloy" fmt --test "/etc/alloy/$(basename "$file")"
done

docker run --rm -v "$obs/loki:/etc/loki:ro" "$loki" -config.file=/etc/loki/loki.yaml -verify-config
