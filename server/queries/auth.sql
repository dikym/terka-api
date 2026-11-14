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
