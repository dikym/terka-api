#!/usr/bin/env bash
# Creates, or brings up to date, the database and login roles of one environment:
#   terka_<env>         the database
#   terka_<env>_owner   owns the schema; used by `terka migrate`; not a superuser
#   terka_<env>_app     the server's login; noinherit, member of anon and authenticated
# Roles are cluster-wide and environments share the cluster, so each one only reaches its own database.
# Safe to run again: it also resets both passwords to the given values.
#
# Usage: OWNER_PASSWORD=… APP_PASSWORD=… create-env-db.sh <env>
# Talks to the postgres service of COMPOSE_FILE (default: the VPS infra stack).
set -euo pipefail

env_name=${1:-}
if [[ ! $env_name =~ ^[a-z]+$ ]]; then
	echo "usage: OWNER_PASSWORD=… APP_PASSWORD=… $0 <env>   (env: lowercase letters only)" >&2
	exit 64
fi
: "${OWNER_PASSWORD:?OWNER_PASSWORD is required}"
: "${APP_PASSWORD:?APP_PASSWORD is required}"
# Letters and digits only (openssl rand -hex 24), so the values need no quoting in SQL or URLs.
for password in "$OWNER_PASSWORD" "$APP_PASSWORD"; do
	if [[ ! $password =~ ^[A-Za-z0-9]{16,}$ ]]; then
		echo "passwords must be at least 16 letters or digits" >&2
		exit 64
	fi
done
export COMPOSE_FILE=${COMPOSE_FILE:-/opt/terka/infra/compose.infra.yaml}
export COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME:-terka-infra}

db=terka_$env_name
owner=${db}_owner
app=${db}_app

# Passwords travel on stdin, never in a command line that other processes can read.
docker compose exec -T postgres psql -q -v ON_ERROR_STOP=1 -U postgres -d postgres <<SQL
\\set owner_password '$OWNER_PASSWORD'
\\set app_password '$APP_PASSWORD'
do \$\$
begin
	create role anon nologin;
exception when duplicate_object then null;
end \$\$;
do \$\$
begin
	create role authenticated nologin;
exception when duplicate_object then null;
end \$\$;
do \$\$
begin
	if not exists (select from pg_roles where rolname = '$owner') then
		create role $owner login;
	end if;
	if not exists (select from pg_roles where rolname = '$app') then
		create role $app login noinherit;
	end if;
end \$\$;
alter role $owner password :'owner_password';
alter role $app noinherit password :'app_password';
grant anon, authenticated to $app;
select 'create database $db owner $owner' where not exists (select from pg_database where datname = '$db')
\\gexec
revoke connect on database $db from public;
grant connect on database $db to $app;
SQL
echo "database $db is ready (roles $owner, $app)"
