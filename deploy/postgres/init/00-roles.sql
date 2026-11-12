-- Cluster-wide group roles that the schema grants to. Every environment's application login role
-- is a member of both. Migrations never create roles, so these exist before the first one runs.
create role anon nologin;
create role authenticated nologin;
