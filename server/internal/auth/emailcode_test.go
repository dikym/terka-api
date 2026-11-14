package auth_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/db"
)

type stubMailer struct {
	mu   sync.Mutex
	sent []auth.Mail
	err  error
}

func (m *stubMailer) Send(_ context.Context, mail auth.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, mail)
	return nil
}

func (m *stubMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

var codeInMail = regexp.MustCompile(`Kode masuk: (\d{6})`)

func (m *stubMailer) lastCode(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no email sent")
	}
	found := codeInMail.FindStringSubmatch(m.sent[len(m.sent)-1].Text)
	if found == nil {
		t.Fatalf("no code in %q", m.sent[len(m.sent)-1].Text)
	}
	return found[1]
}

type stubTurnstile struct {
	calls atomic.Int32
	err   error
}

func (s *stubTurnstile) Verify(context.Context, string, string) error {
	s.calls.Add(1)
	return s.err
}

type codesEnv struct {
	codes     *auth.EmailCodes
	mail      *stubMailer
	turnstile *stubTurnstile
	clock     *clock
	auth      *db.Auth
	keys      auth.Keys
	mu        sync.Mutex
	secrets   []string
}

var testKeys = func() auth.Keys {
	k, err := auth.DeriveKeys(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		panic(err)
	}
	return k
}()

// newCodesEnv runs the email code service on a fresh database. When the test ends, the log must
// hold none of the addresses, codes, bindings or tokens the test used.
func newCodesEnv(t *testing.T) *codesEnv {
	t.Helper()
	_, a := pools(t)
	e := &codesEnv{mail: &stubMailer{}, turnstile: &stubTurnstile{}, clock: newClock(), auth: a, keys: testKeys}
	var logs bytes.Buffer
	e.codes = e.service(testKeys, slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, s := range e.secrets {
			if strings.Contains(strings.ToLower(logs.String()), strings.ToLower(s)) {
				t.Errorf("log carries the secret %q:\n%s", s, logs.String())
			}
		}
	})
	return e
}

func (e *codesEnv) service(keys auth.Keys, log *slog.Logger) *auth.EmailCodes {
	return &auth.EmailCodes{
		Auth:      e.auth,
		Sessions:  &auth.Sessions{Auth: e.auth, Now: e.clock.Now},
		Keys:      keys,
		Mailer:    e.mail,
		Turnstile: e.turnstile,
		PublicURL: "https://terka.my.id",
		Now:       e.clock.Now,
		Log:       log,
	}
}

func (e *codesEnv) remember(secrets ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range secrets {
		if s != "" {
			e.secrets = append(e.secrets, s)
		}
	}
}

func (e *codesEnv) request(t *testing.T, email, oldBinding string) (string, string) {
	t.Helper()
	res, err := e.codes.Request(context.Background(), email, "turnstile-token", "203.0.113.7", oldBinding)
	if err != nil {
		t.Fatalf("request %s: %v", email, err)
	}
	code := e.mail.lastCode(t)
	e.remember(email, res.Binding, code)
	return res.Binding, code
}

func (e *codesEnv) verify(email, code, binding string) (auth.Session, error) {
	token, s, err := e.codes.Verify(context.Background(), email, code, binding)
	if token != "" {
		e.remember(token)
	}
	return s, err
}

func (e *codesEnv) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	query(t, e.auth, sql, args, &n)
	return n
}

func wrong(code string) string {
	return code[:5] + string('0'+(code[5]-'0'+1)%10)
}

func assertIs(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func retryAfter(t *testing.T, err error) time.Duration {
	t.Helper()
	var ra auth.RetryAfter
	if !errors.As(err, &ra) {
		t.Fatalf("%v carries no retry time", err)
	}
	return ra.RetryAfter()
}

func TestCodeSignsInAndCreatesTheUser(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	s, err := e.verify("ani@contoh.id", code, binding)
	if err != nil {
		t.Fatal(err)
	}
	if s.Email != "ani@contoh.id" || s.Method != "email" {
		t.Fatalf("session = %+v", s)
	}
	if n := e.count(t, "select count(*) from auth.users where id = $1 and email_verified_at is not null and last_sign_in_at is not null", s.UserID); n != 1 {
		t.Fatal("the new user is not marked verified and signed in")
	}
	if n := e.count(t, "select count(*) from auth.sessions where id = $1 and method = 'email'", s.ID); n != 1 {
		t.Fatal("no email session row")
	}
}

func TestCodeSignsInAnExistingUser(t *testing.T) {
	e := newCodesEnv(t)
	id := createUser(t, e.auth, "ani@contoh.id")
	binding, code := e.request(t, "ani@contoh.id", "")
	s, err := e.verify("ani@contoh.id", code, binding)
	if err != nil || s.UserID != id {
		t.Fatalf("session %+v, %v; want user %s", s, err, id)
	}
}

func TestAddressCaseDoesNotSplitAccounts(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "Alice@Example.com", "")
	first, err := e.verify("alice@example.com", code, binding)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(61 * time.Second)
	binding, code = e.request(t, " alice@example.com ", binding)
	second, err := e.verify("ALICE@example.com", code, binding)
	if err != nil || second.UserID != first.UserID {
		t.Fatalf("second sign-in: %+v, %v; want user %s", second, err, first.UserID)
	}
	if n := e.count(t, "select count(*) from auth.users"); n != 1 {
		t.Fatalf("%d users, want 1", n)
	}
}

func TestWrongCodeIsInvalid(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	_, err := e.verify("ani@contoh.id", wrong(code), binding)
	assertIs(t, "wrong code", err, auth.ErrInvalid)
	for _, garbage := range []string{"", "12345", "1234567", "abcdef", strings.Repeat("9", 4000)} {
		_, err := e.verify("ani@contoh.id", garbage, binding)
		assertIs(t, fmt.Sprintf("code %.10q", garbage), err, auth.ErrInvalid)
	}
}

func TestExpiredCodeIsInvalid(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	e.clock.Advance(15*time.Minute + time.Second)
	_, err := e.verify("ani@contoh.id", code, binding)
	assertIs(t, "expired code", err, auth.ErrInvalid)
}

func TestFiveWrongGuessesBurnTheCode(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	for range 5 {
		_, err := e.verify("ani@contoh.id", wrong(code), binding)
		assertIs(t, "wrong guess", err, auth.ErrInvalid)
	}
	_, err := e.verify("ani@contoh.id", code, binding)
	assertIs(t, "right code after five wrong", err, auth.ErrInvalid)
}

func (e *codesEnv) fail(t *testing.T, email string, codes int) {
	t.Helper()
	for range codes {
		binding, code := e.request(t, email, "")
		for range 5 {
			_, err := e.verify(email, wrong(code), binding)
			assertIs(t, "wrong guess", err, auth.ErrInvalid)
		}
		e.clock.Advance(61 * time.Second)
	}
}

func TestTwentyFailuresLockTheMailboxFor24Hours(t *testing.T) {
	e := newCodesEnv(t)
	e.fail(t, "ani@contoh.id", 4)
	binding, code := e.request(t, "ani@contoh.id", "")
	_, err := e.verify("ani@contoh.id", code, binding)
	assertIs(t, "21st attempt", err, auth.ErrLocked)
	if wait := retryAfter(t, err); wait <= 0 || wait > 24*time.Hour {
		t.Fatalf("retry after %v, want within 24 hours", wait)
	}
	e.clock.Advance(24 * time.Hour)
	binding, code = e.request(t, "ani@contoh.id", binding)
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatalf("after 24 hours: %v", err)
	}
}

func TestLockCoversMailboxVariants(t *testing.T) {
	e := newCodesEnv(t)
	e.fail(t, "b.udi@gmail.com", 4)
	binding, code := e.request(t, "budi+terka@googlemail.com", "")
	_, err := e.verify("budi+terka@googlemail.com", code, binding)
	assertIs(t, "variant of a locked mailbox", err, auth.ErrLocked)
}

func TestFailuresWithoutALiveCodeAreNotCounted(t *testing.T) {
	e := newCodesEnv(t)
	for i := range 100 {
		_, err := e.verify("ani@contoh.id", fmt.Sprintf("%06d", i), "a-binding-of-no-request")
		assertIs(t, "verify without a code", err, auth.ErrInvalid)
	}
	if n := e.count(t, "select count(*) from auth.signin_failures"); n != 0 {
		t.Fatalf("%d failures recorded, want none", n)
	}
	binding, code := e.request(t, "ani@contoh.id", "")
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatalf("the mailbox was locked by guesses against nothing: %v", err)
	}
}

func TestCodeWorksOnce(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatal(err)
	}
	_, err := e.verify("ani@contoh.id", code, binding)
	assertIs(t, "second use", err, auth.ErrInvalid)
}

func TestConcurrentVerifiesCreateOneSession(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	var wg sync.WaitGroup
	var ok, invalid atomic.Int32
	for range 20 {
		wg.Go(func() {
			_, err := e.verify("ani@contoh.id", code, binding)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, auth.ErrInvalid):
				invalid.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 || invalid.Load() != 19 {
		t.Fatalf("%d succeeded, %d invalid; want 1 and 19", ok.Load(), invalid.Load())
	}
	if n := e.count(t, "select count(*) from auth.sessions"); n != 1 {
		t.Fatalf("%d sessions, want 1", n)
	}
}

func TestNewRequestFromTheSameBrowserBurnsItsOldCode(t *testing.T) {
	e := newCodesEnv(t)
	oldBinding, oldCode := e.request(t, "ani@contoh.id", "")
	e.clock.Advance(61 * time.Second)
	binding, code := e.request(t, "ani@contoh.id", oldBinding)
	_, err := e.verify("ani@contoh.id", oldCode, oldBinding)
	assertIs(t, "old code", err, auth.ErrInvalid)
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatalf("new code: %v", err)
	}
}

func TestRequestFromAnotherBrowserKeepsTheFirstCode(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	e.clock.Advance(61 * time.Second)
	e.request(t, "ani@contoh.id", "")
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatalf("a request from another browser burned the first code: %v", err)
	}
}

func TestVerifyWithoutBindingIsOtherBrowser(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	_, err := e.verify("ani@contoh.id", code, "")
	assertIs(t, "no binding", err, auth.ErrOtherBrowser)
	if n := e.count(t, "select (select count(*) from auth.sessions) + (select count(*) from auth.signin_failures)"); n != 0 {
		t.Fatalf("%d sessions or failures, want none", n)
	}
	if _, err := e.verify("ani@contoh.id", code, binding); err != nil {
		t.Fatalf("the code was spent by the other browser: %v", err)
	}
}

func TestVerifyWithAnotherRequestsBindingIsInvalid(t *testing.T) {
	e := newCodesEnv(t)
	_, code := e.request(t, "ani@contoh.id", "")
	attacker, _ := e.request(t, "budi@contoh.id", "")
	_, err := e.verify("ani@contoh.id", code, attacker)
	assertIs(t, "another request's binding", err, auth.ErrInvalid)
}

func TestNoColumnHoldsTheRawCode(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	var row string
	var codeMAC, bindingHash []byte
	query(t, e.auth, "select to_jsonb(c)::text, code_hmac, binding_hash from auth.email_codes c", nil, &row, &codeMAC, &bindingHash)
	if strings.Contains(row, code) || strings.Contains(row, binding) {
		t.Fatalf("row carries the code or binding: %s", row)
	}
	for _, raw := range [][]byte{codeMAC, bindingHash} {
		if bytes.Contains(raw, []byte(code)) || bytes.Contains(raw, []byte(binding)) {
			t.Fatal("a bytea column carries the code or binding")
		}
	}
}

func TestCodeFromAnotherKeyIsRejected(t *testing.T) {
	e := newCodesEnv(t)
	binding, code := e.request(t, "ani@contoh.id", "")
	other := e.keys
	other.CodeMAC = bytes.Repeat([]byte{9}, 32)
	_, _, err := e.service(other, slog.New(slog.DiscardHandler)).Verify(context.Background(), "ani@contoh.id", code, binding)
	assertIs(t, "code checked under another key", err, auth.ErrInvalid)
}

func TestRequestAnswersTheSameForNewKnownAndLockedAddresses(t *testing.T) {
	e := newCodesEnv(t)
	createUser(t, e.auth, "lama@contoh.id")
	err := e.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			"insert into auth.signin_failures (mailbox, failed_at) select 'kunci@contoh.id', $1 from generate_series(1, 20)", e.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, email := range []string{"baru@contoh.id", "lama@contoh.id", "kunci@contoh.id"} {
		res, err := e.codes.Request(context.Background(), email, "turnstile-token", "203.0.113.7", "")
		if err != nil || len(res.Binding) != 43 {
			t.Fatalf("%s: binding %d chars, %v", email, len(res.Binding), err)
		}
		e.remember(email, res.Binding)
		if e.mail.count() != i+1 {
			t.Fatalf("%s: no email sent", email)
		}
	}
}

func TestMailFailureLooksLikeSuccess(t *testing.T) {
	e := newCodesEnv(t)
	e.mail.err = errors.New("550 5.1.1 <ani@contoh.id>: recipient rejected")
	res, err := e.codes.Request(context.Background(), "ani@contoh.id", "turnstile-token", "203.0.113.7", "")
	if err != nil || res.Binding == "" {
		t.Fatalf("mail failure surfaced: %+v, %v", res, err)
	}
	e.remember("ani@contoh.id", res.Binding)
}

func TestCaptchaFailureSendsNothing(t *testing.T) {
	e := newCodesEnv(t)
	e.turnstile.err = errors.New("turnstile: challenge failed")
	_, err := e.codes.Request(context.Background(), "ani@contoh.id", "bad-token", "203.0.113.7", "")
	assertIs(t, "failed captcha", err, auth.ErrCaptcha)
	if e.mail.count() != 0 || e.count(t, "select count(*) from auth.email_codes") != 0 {
		t.Fatal("a code was made or sent")
	}
}

func TestSecondRequestWithin60SecondsIsTooSoon(t *testing.T) {
	e := newCodesEnv(t)
	e.request(t, "ani@contoh.id", "")
	e.clock.Advance(30 * time.Second)
	_, err := e.codes.Request(context.Background(), "ani@contoh.id", "turnstile-token", "203.0.113.7", "")
	assertIs(t, "second request", err, auth.ErrTooSoon)
	if wait := retryAfter(t, err); wait != 30*time.Second {
		t.Fatalf("retry after %v, want 30s", wait)
	}
	e.clock.Advance(30 * time.Second)
	e.request(t, "ani@contoh.id", "")
}

func TestSixthRequestInAnHourIsTooSoon(t *testing.T) {
	e := newCodesEnv(t)
	for range 5 {
		e.request(t, "ani@contoh.id", "")
		e.clock.Advance(61 * time.Second)
	}
	_, err := e.codes.Request(context.Background(), "ani@contoh.id", "turnstile-token", "203.0.113.7", "")
	assertIs(t, "sixth request", err, auth.ErrTooSoon)
	wait := retryAfter(t, err)
	if want := time.Hour - 5*61*time.Second; wait != want {
		t.Fatalf("retry after %v, want %v", wait, want)
	}
	e.clock.Advance(wait)
	e.request(t, "ani@contoh.id", "")
}

func TestMailboxVariantsShareTheRequestQuota(t *testing.T) {
	e := newCodesEnv(t)
	e.request(t, "c.itra@gmail.com", "")
	e.clock.Advance(10 * time.Second)
	_, err := e.codes.Request(context.Background(), "citra+x@googlemail.com", "turnstile-token", "203.0.113.7", "")
	assertIs(t, "variant within 60 seconds", err, auth.ErrTooSoon)
}

func (e *codesEnv) burst(t *testing.T, prefix string, n int) {
	t.Helper()
	for i := range n {
		e.request(t, fmt.Sprintf("%s%d@contoh.id", prefix, i), "")
	}
}

func TestEightyFirstEmailInAnHourIsRefused(t *testing.T) {
	e := newCodesEnv(t)
	e.burst(t, "a", 80)
	_, err := e.codes.Request(context.Background(), "ke81@contoh.id", "turnstile-token", "203.0.113.7", "")
	assertIs(t, "81st email", err, auth.ErrServiceLimit)
	if e.mail.count() != 80 {
		t.Fatalf("%d emails, want 80", e.mail.count())
	}
}

func TestNinetyFirstEmailInAUTCDayIsRefused(t *testing.T) {
	e := newCodesEnv(t)
	e.clock.Set(time.Date(2026, 11, 14, 0, 30, 0, 0, time.UTC))
	e.burst(t, "a", 80)
	e.clock.Advance(61 * time.Minute)
	e.burst(t, "b", 10)
	_, err := e.codes.Request(context.Background(), "ke91@contoh.id", "turnstile-token", "203.0.113.7", "")
	assertIs(t, "91st email of the day", err, auth.ErrServiceLimit)
}

func TestServiceDayResetsAtUTCMidnight(t *testing.T) {
	e := newCodesEnv(t)
	e.clock.Set(time.Date(2026, 11, 14, 22, 0, 0, 0, time.UTC))
	e.burst(t, "a", 80)
	e.clock.Set(time.Date(2026, 11, 14, 23, 59, 0, 0, time.UTC))
	e.burst(t, "b", 10)
	e.clock.Set(time.Date(2026, 11, 15, 0, 1, 0, 0, time.UTC))
	e.request(t, "ke91@contoh.id", "")
}

func TestConcurrentRequestsForOneMailboxLetOneThrough(t *testing.T) {
	e := newCodesEnv(t)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			res, err := e.codes.Request(context.Background(), "ani@contoh.id", "turnstile-token", "203.0.113.7", "")
			e.remember(res.Binding)
			errs <- err
		}()
	}
	first, second := <-errs, <-errs
	if (first == nil) == (second == nil) || !errors.Is(first, auth.ErrTooSoon) && !errors.Is(second, auth.ErrTooSoon) {
		t.Fatalf("errors %v and %v, want one nil and one too soon", first, second)
	}
	e.remember("ani@contoh.id", e.mail.lastCode(t))
}

func TestInvalidAddressIsRejectedBeforeAnythingElse(t *testing.T) {
	e := newCodesEnv(t)
	for _, email := range []string{"a@b.co\r\nBcc: x@y.co", "", "not-an-address", "ünï@b.co"} {
		_, err := e.codes.Request(context.Background(), email, "turnstile-token", "203.0.113.7", "")
		assertIs(t, fmt.Sprintf("%q", email), err, auth.ErrInvalidEmail)
	}
	if e.turnstile.calls.Load() != 0 || e.count(t, "select count(*) from auth.email_codes") != 0 {
		t.Fatal("an invalid address reached Turnstile or the database")
	}
}

func TestLockIsReportedOnlyToTheRequestingBrowser(t *testing.T) {
	e := newCodesEnv(t)
	e.fail(t, "ani@contoh.id", 4)
	for _, binding := range []string{"made-up-binding", strings.Repeat("A", 43)} {
		_, err := e.verify("ani@contoh.id", "123456", binding)
		assertIs(t, "locked mailbox, binding of no request", err, auth.ErrInvalid)
	}
}
