-- Queries of the sign-in flow. They run inside db.Auth.AsAuth, as the terka_auth role.

-- name: CreateSession :one
with created as (
	insert into auth.sessions (user_id, token_hash, created_at, last_used_at, expires_at, absolute_expires_at, method)
	values (@user_id, @token_hash, @now, @now, @expires_at, @absolute_expires_at, @method)
	returning id, user_id, method
)
select created.id, created.user_id, created.method, users.email
from created join auth.users on users.id = created.user_id;

-- name: SessionByTokenHash :one
select sessions.id, sessions.user_id, sessions.method, sessions.last_used_at, sessions.expires_at,
	sessions.absolute_expires_at, users.email
from auth.sessions join auth.users on users.id = sessions.user_id
where sessions.token_hash = @token_hash;

-- name: TouchSession :exec
update auth.sessions set last_used_at = @now, expires_at = @expires_at where id = @id;

-- name: DeleteSessionByTokenHash :exec
delete from auth.sessions where token_hash = @token_hash;

-- name: DeleteExpiredSessions :execrows
delete from auth.sessions where expires_at < @now or absolute_expires_at < @now;

-- name: DeleteOldEmailCodes :execrows
delete from auth.email_codes where expires_at < @before;

-- name: DeleteOldSigninFailures :execrows
delete from auth.signin_failures where failed_at < @before;

-- Every email code request and verify for one mailbox runs under this lock, so the limits below
-- are counted and acted on without a race.
-- name: LockMailbox :exec
select pg_advisory_xact_lock(hashtextextended(@mailbox::text, 0));

-- Taken after the mailbox lock, never before, so the two cannot deadlock.
-- name: LockEmailService :exec
select pg_advisory_xact_lock(7462725431);

-- name: MailboxRequests :one
select count(*) as requests,
	coalesce(min(created_at), @since::timestamptz)::timestamptz as oldest,
	coalesce(max(created_at), @since::timestamptz)::timestamptz as newest
from auth.email_codes
where mailbox = @mailbox and created_at > @since;

-- name: ServiceRequests :one
select count(*) filter (where created_at > @hour_ago) as last_hour,
	count(*) filter (where created_at >= @day_start) as today
from auth.email_codes
where created_at > @hour_ago or created_at >= @day_start;

-- name: BurnBindingCodes :exec
update auth.email_codes set used_at = @now where binding_hash = @binding_hash and used_at is null;

-- name: InsertEmailCode :exec
insert into auth.email_codes (email, mailbox, code_hmac, binding_hash, created_at, expires_at, request_ip)
values (@email, @mailbox, @code_hmac, @binding_hash, @now, @expires_at, nullif(@request_ip::text, '')::inet);

-- The twentieth most recent failure in the window; while it exists the mailbox is locked.
-- name: TwentiethFailure :one
select failed_at from auth.signin_failures
where mailbox = @mailbox and failed_at > @since
order by failed_at desc offset 19 limit 1;

-- name: LiveEmailCode :one
select id, code_hmac from auth.email_codes
where email = @email and binding_hash = @binding_hash and used_at is null and expires_at > @now
	and failed_attempts < 5
order by created_at desc limit 1
for update;

-- name: FailEmailCode :exec
update auth.email_codes set failed_attempts = failed_attempts + 1 where id = @id;

-- name: RecordSigninFailure :exec
insert into auth.signin_failures (mailbox, failed_at) values (@mailbox, @now);

-- name: UseEmailCode :exec
update auth.email_codes set used_at = @now where id = @id;

-- name: UpsertEmailUser :one
insert into auth.users (email, email_verified_at, last_sign_in_at) values (@email, @now, @now)
on conflict ((lower(email))) do update
	set email_verified_at = coalesce(users.email_verified_at, excluded.email_verified_at),
		last_sign_in_at = excluded.last_sign_in_at
returning id;

-- name: UserByGoogleSub :one
select id from auth.users where google_sub = @google_sub;

-- name: UserByEmail :one
select id, google_sub, email_verified_at from auth.users where lower(email) = lower(@email);

-- A changed Google address is taken over only when no other account owns it.
-- name: SignInGoogleUser :exec
update auth.users
set email = case
		when exists (select 1 from auth.users other where lower(other.email) = lower(@email) and other.id <> @id)
		then email else @email end,
	email_verified_at = coalesce(email_verified_at, @now),
	last_sign_in_at = @now
where id = @id;

-- name: LinkGoogleUser :exec
update auth.users set google_sub = @google_sub, last_sign_in_at = @now where id = @id;

-- name: CreateGoogleUser :one
insert into auth.users (email, google_sub, email_verified_at, last_sign_in_at)
values (@email, @google_sub, @now, @now)
returning id;
