-- The local stack's database, with the same role layout as an environment on the VPS
-- (see deploy/scripts/create-env-db.sh). Runs once, when the local cluster is first created.
create role terka_local_owner login password 'localonlyownerpw1';
create role terka_local_app login noinherit password 'localonlyapppw123';
grant anon, authenticated to terka_local_app;
create database terka_local owner terka_local_owner;
revoke connect on database terka_local from public;
grant connect on database terka_local to terka_local_app;
create role terka_local_auth login noinherit password 'localonlyauthpw123';
grant terka_auth to terka_local_auth;
grant connect on database terka_local to terka_local_auth;
