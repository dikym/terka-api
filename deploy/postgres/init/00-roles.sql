-- Cluster-wide group roles that the schema grants to. Each environment's app login is a member of
-- anon and authenticated; its separate auth login is a member of terka_auth only. Migrations never
-- create roles, so these exist before the first one runs.
create role anon nologin;
create role authenticated nologin;
create role terka_auth nologin;
