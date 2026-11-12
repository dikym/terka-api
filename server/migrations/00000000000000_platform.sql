-- What Supabase provided and the schema in the later files expects: the auth schema with the
-- account and session tables, and the two functions that read the caller from the transaction.
-- The roles anon and authenticated exist before any migration runs; migrations never create roles.
create schema auth;
grant usage on schema auth to anon, authenticated;

create table auth.users (
	id uuid primary key default gen_random_uuid(),
	email text not null,
	google_sub text unique,
	created_at timestamptz not null default now()
);
create unique index users_email_key on auth.users (lower(email));

create table auth.sessions (
	id uuid primary key default gen_random_uuid(),
	user_id uuid not null references auth.users (id) on delete cascade,
	token_hash bytea not null unique,
	created_at timestamptz not null default now(),
	last_used_at timestamptz not null default now(),
	expires_at timestamptz not null
);
create index sessions_user_id_idx on auth.sessions (user_id);

-- The server sets request.jwt.claims for the length of one transaction; outside of one it is empty.
create function auth.jwt() returns jsonb
language sql stable as $$
	select coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb, '{}'::jsonb);
$$;

create function auth.uid() returns uuid
language sql stable as $$
	select nullif(auth.jwt() ->> 'sub', '')::uuid;
$$;
