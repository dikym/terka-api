#!/usr/bin/env bash
# Creates, or brings up to date, terka_monitor: the login Alloy uses to read Postgres statistics.
# It is a member of pg_monitor and gets no grants on any table, so it never reads user data.
# Safe to run again: it also resets the password to the given value.
#
# Usage: MONITOR_PASSWORD=… create-monitor-role.sh
# Talks to the postgres service of COMPOSE_FILE (default: the VPS infra stack).
set -euo pipefail

: "${MONITOR_PASSWORD:?MONITOR_PASSWORD is required}"
# Letters and digits only (openssl rand -hex 24), so the value needs no quoting in SQL or URLs.
if [[ ! $MONITOR_PASSWORD =~ ^[A-Za-z0-9]{16,}$ ]]; then
	echo "MONITOR_PASSWORD must be at least 16 letters or digits" >&2
	exit 64
fi
export COMPOSE_FILE=${COMPOSE_FILE:-/opt/terka/infra/compose.infra.yaml}
export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-terka-infra}

# The password travels on stdin, never in a command line that other processes can read.
docker compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U postgres -d postgres <<SQL
\\set monitor_password '$MONITOR_PASSWORD'
do \$\$
begin
	create role terka_monitor login;
exception when duplicate_object then null;
end \$\$;
alter role terka_monitor login password :'monitor_password';
grant pg_monitor to terka_monitor;
SQL
echo "role terka_monitor is ready"
