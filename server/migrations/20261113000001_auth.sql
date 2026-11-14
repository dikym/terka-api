-- Sign-in for the Go server: email codes, failure counts and richer sessions, all in schema auth.
-- Only the terka_auth role touches these tables. The server reaches it through a login of its own,
-- so SQL running as a user (anon, authenticated) cannot switch to it. Nothing here changes the
-- public schema shared with the old Supabase app.
alter table auth.users
	add column email_verified_at timestamptz,
	add column last_sign_in_at timestamptz;

alter table auth.sessions
	add column absolute_expires_at timestamptz not null,
	add column method text not null check (method in ('google', 'email'));

-- One row per code sent. Only an HMAC of the code is kept, and only a hash of the browser binding.
create table auth.email_codes (
	id uuid primary key default gen_random_uuid(),
	email text not null,
	mailbox text not null,
	code_hmac bytea not null,
	binding_hash bytea not null,
	created_at timestamptz not null default now(),
	expires_at timestamptz not null,
	used_at timestamptz,
	failed_attempts smallint not null default 0 check (failed_attempts >= 0),
	request_ip inet
);
create index email_codes_mailbox_created_idx on auth.email_codes (mailbox, created_at);
create index email_codes_binding_idx on auth.email_codes (binding_hash);

-- One row per wrong guess against a live code, for the per-mailbox lock.
create table auth.signin_failures (
	id bigint generated always as identity primary key,
	mailbox text not null,
	failed_at timestamptz not null default now()
);
create index signin_failures_mailbox_idx on auth.signin_failures (mailbox, failed_at);

grant usage on schema auth to terka_auth;
grant select, insert, update, delete on auth.users, auth.sessions, auth.email_codes, auth.signin_failures
	to terka_auth;
