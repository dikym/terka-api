begin;
select plan(17);

-- Users of the app, signed in or not, never touch the auth tables; only the auth role does.
select tests.as_user(tests.create_user('grants@test.terka'));
select throws_ok('select 1 from auth.users', '42501', null, 'a user cannot read auth.users');
select throws_ok('select 1 from auth.sessions', '42501', null, 'a user cannot read auth.sessions');
select throws_ok('select 1 from auth.email_codes', '42501', null, 'a user cannot read auth.email_codes');
select throws_ok('select 1 from auth.signin_failures', '42501', null, 'a user cannot read auth.signin_failures');
select throws_ok(
	$$ insert into auth.sessions (user_id, token_hash, expires_at, absolute_expires_at, method)
	   values (auth.uid(), '\x00', now(), now(), 'email') $$,
	'42501', null, 'a user cannot write a session'
);
select tests.as_owner();

select tests.as_anon();
select throws_ok('select 1 from auth.users', '42501', null, 'anon cannot read auth.users');
select throws_ok('select 1 from auth.sessions', '42501', null, 'anon cannot read auth.sessions');
select throws_ok('select 1 from auth.email_codes', '42501', null, 'anon cannot read auth.email_codes');
select throws_ok('select 1 from auth.signin_failures', '42501', null, 'anon cannot read auth.signin_failures');
select tests.as_owner();

set local role terka_auth;
select lives_ok('select 1 from auth.users', 'the auth role reads auth.users');
select lives_ok('select 1 from auth.sessions', 'the auth role reads auth.sessions');
select lives_ok('select 1 from auth.signin_failures', 'the auth role reads auth.signin_failures');
select lives_ok(
	$$ insert into auth.email_codes (email, mailbox, code_hmac, binding_hash, expires_at)
	   values ('a@test.terka', 'a@test.terka', '\x01', '\x02', now() + interval '15 minutes') $$,
	'the auth role writes auth.email_codes'
);
select throws_ok('select 1 from public.devices', '42501', null, 'the auth role cannot read devices');
select throws_ok('select 1 from public.review_logs', '42501', null, 'the auth role cannot read learning data');
select throws_ok(
	$$ insert into auth.sessions (user_id, token_hash, expires_at, absolute_expires_at, method)
	   values (gen_random_uuid(), '\x00', now(), now(), 'password') $$,
	'23514', null, 'a session records only the google or email method'
);
reset role;
select is(
	(select count(*)::integer from information_schema.columns where table_schema = 'auth' and column_name like '%password%'),
	0, 'no auth table has a password column'
);

select * from finish();
rollback;
