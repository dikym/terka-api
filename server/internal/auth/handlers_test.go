package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/auth/oidctest"
	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/server"
)

// stack is the whole server with the auth routes, over a real database and fake outside services.
type stack struct {
	handler   http.Handler
	mail      *stubMailer
	turnstile *stubTurnstile
	issuer    *oidctest.Issuer
	clock     *clock
	auth      *db.Auth
	app       *db.App
	sessions  *auth.Sessions

	mu      sync.Mutex
	logs    bytes.Buffer
	secrets []string
}

func (s *stack) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logs.Write(p)
}

func newStack(t *testing.T, trustCloudflare bool) *stack {
	t.Helper()
	app, a := pools(t)
	s := &stack{mail: &stubMailer{}, turnstile: &stubTurnstile{}, clock: newClock(), auth: a, app: app}
	s.issuer = oidctest.NewIssuer(t, googleClient)
	s.issuer.Now = s.clock.Now
	log := slog.New(slog.NewJSONHandler(s, nil))
	s.sessions = &auth.Sessions{Auth: a, Now: s.clock.Now}
	google, err := auth.NewGoogle(context.Background(), s.issuer.URL, googleClient, "terka-secret", googleRedirect, testKeys, a, s.sessions, s.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	h := &auth.Handlers{
		Codes: &auth.EmailCodes{
			Auth: a, Sessions: s.sessions, Keys: testKeys, Mailer: s.mail, Turnstile: s.turnstile,
			PublicURL: "https://terka.my.id", Now: s.clock.Now, Log: log,
		},
		Google: google, Sessions: s.sessions, Auth: a, App: app,
		Env: "staging", TrustCloudflare: trustCloudflare, Log: log, Now: s.clock.Now,
	}
	s.handler = server.New(server.Deps{Version: "test", Env: "staging", Ping: up, Logger: log, Routes: h.Mount})
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, secret := range s.secrets {
			if strings.Contains(strings.ToLower(s.logs.String()), strings.ToLower(secret)) {
				t.Errorf("log carries the secret %q:\n%s", secret, s.logs.String())
			}
		}
	})
	return s
}

func up(context.Context) error { return nil }

func (s *stack) remember(secrets ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, secret := range secrets {
		if secret != "" {
			s.secrets = append(s.secrets, secret)
		}
	}
}

type call struct {
	method, target, body string
	cookies              []*http.Cookie
	header               map[string]string
}

func (s *stack) do(c call) *httptest.ResponseRecorder {
	var body *strings.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(c.method, c.target, body)
	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.header {
		r.Header.Set(k, v)
	}
	for _, ck := range c.cookies {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, r)
	for _, ck := range rec.Result().Cookies() {
		s.remember(ck.Value)
	}
	return rec
}

func cookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", name, rec.Header().Values("Set-Cookie"))
	return nil
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("body %s, want code %s", rec.Body.String(), code)
	}
}

func requestBody(email string) string {
	return fmt.Sprintf(`{"email":%q,"turnstile":"turnstile-token"}`, email)
}

func (s *stack) requestCode(t *testing.T, email string, cookies ...*http.Cookie) (*http.Cookie, string) {
	t.Helper()
	s.remember(email)
	rec := s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody(email), cookies: cookies})
	expect(t, rec, http.StatusAccepted, "")
	code := s.mail.lastCode(t)
	s.remember(code)
	return cookie(t, rec, "__Host-terka_email_binding"), code
}

func verifyBody(email, code string) string {
	return fmt.Sprintf(`{"email":%q,"code":%q}`, email, code)
}

func (s *stack) signIn(t *testing.T, email string, cookies ...*http.Cookie) *http.Cookie {
	t.Helper()
	binding, code := s.requestCode(t, email, cookies...)
	rec := s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody(email, code), cookies: append(cookies, binding)})
	expect(t, rec, http.StatusOK, "")
	return cookie(t, rec, "__Host-terka_session")
}

func TestEmailRequestContract(t *testing.T) {
	s := newStack(t, false)
	rec := s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("ani@contoh.id")})
	expect(t, rec, http.StatusAccepted, "")
	if strings.TrimSpace(rec.Body.String()) != "{}" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body %s, cache %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	s.remember("ani@contoh.id", s.mail.lastCode(t))

	rec = s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("ani@contoh.id")})
	expect(t, rec, http.StatusTooManyRequests, "too_soon")
	if rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
	}
	s.clock.Advance(29*time.Second + 500*time.Millisecond)
	rec = s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("ani@contoh.id")})
	if rec.Header().Get("Retry-After") != "31" {
		t.Fatalf("30.5 seconds left: Retry-After = %q, want 31 (rounded up)", rec.Header().Get("Retry-After"))
	}

	expect(t, s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("not an address")}), http.StatusBadRequest, "invalid_email")

	s.turnstile.err = fmt.Errorf("turnstile: challenge failed")
	expect(t, s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("budi@contoh.id")}), http.StatusBadRequest, "captcha_failed")
	s.turnstile.err = nil

	err := s.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `insert into auth.email_codes (email, mailbox, code_hmac, binding_hash, created_at, expires_at)
			select 'x' || i || '@contoh.id', 'x' || i || '@contoh.id', '\x00', '\x00', $1, $1 from generate_series(1, 80) i`, s.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("citra@contoh.id")}), http.StatusTooManyRequests, "rate_limited")
}

func TestEmailVerifyContract(t *testing.T) {
	s := newStack(t, false)
	binding, code := s.requestCode(t, "ani@contoh.id")

	expect(t, s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code)}), http.StatusBadRequest, "other_browser")
	expect(t, s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", wrong(code)), cookies: []*http.Cookie{binding}}),
		http.StatusBadRequest, "invalid")

	rec := s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code), cookies: []*http.Cookie{binding}})
	expect(t, rec, http.StatusOK, "")
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["email"] != "ani@contoh.id" || body["method"] != "email" || uuid.Validate(body["user_id"]) != nil || uuid.Validate(body["session_id"]) != nil || len(body) != 4 {
		t.Fatalf("body = %v", body)
	}
	if cleared := cookie(t, rec, "__Host-terka_email_binding"); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("binding cookie not cleared: %+v", cleared)
	}

	s.clock.Advance(61 * time.Second)
	binding, code = s.requestCode(t, "ani@contoh.id")
	err := s.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			"insert into auth.signin_failures (mailbox, failed_at) select 'ani@contoh.id', $1 from generate_series(1, 20)", s.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code), cookies: []*http.Cookie{binding}})
	expect(t, rec, http.StatusTooManyRequests, "locked")
	if rec.Header().Get("Retry-After") != "86400" {
		t.Fatalf("Retry-After = %q, want 86400", rec.Header().Get("Retry-After"))
	}
}

func TestVerifyIsLimitedPerIP(t *testing.T) {
	s := newStack(t, false)
	for range 30 {
		expect(t, s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", "123456")}), http.StatusBadRequest, "other_browser")
	}
	expect(t, s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", "123456")}), http.StatusTooManyRequests, "rate_limited")
}

func TestSessionContract(t *testing.T) {
	s := newStack(t, false)
	expect(t, s.do(call{method: "GET", target: "/api/auth/session"}), http.StatusUnauthorized, "signed_out")
	session := s.signIn(t, "ani@contoh.id")
	rec := s.do(call{method: "GET", target: "/api/auth/session", cookies: []*http.Cookie{session}})
	expect(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"email":"ani@contoh.id"`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	s := newStack(t, false)
	c := s.signIn(t, "ani@contoh.id")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 400*24*60*60 || len(c.Value) != 43 {
		t.Fatalf("session cookie = %+v", c)
	}
}

func TestBindingCookieAttributes(t *testing.T) {
	s := newStack(t, false)
	c, _ := s.requestCode(t, "ani@contoh.id")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 15*60 {
		t.Fatalf("binding cookie = %+v", c)
	}
}

func TestOAuthCookieAttributes(t *testing.T) {
	s := newStack(t, false)
	rec := s.do(call{method: "GET", target: "/api/auth/google/start"})
	expect(t, rec, http.StatusSeeOther, "")
	if !strings.HasPrefix(rec.Header().Get("Location"), s.issuer.URL+"/auth?") {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
	c := cookie(t, rec, "__Host-terka_oauth")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 10*60 {
		t.Fatalf("oauth cookie = %+v", c)
	}
}

func TestRequestResponsesAreIdenticalForNewKnownAndLockedAddresses(t *testing.T) {
	s := newStack(t, false)
	createUser(t, s.auth, "lama@contoh.id")
	err := s.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			"insert into auth.signin_failures (mailbox, failed_at) select 'kunci@contoh.id', $1 from generate_series(1, 20)", s.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var first *httptest.ResponseRecorder
	for _, email := range []string{"baru@contoh.id", "lama@contoh.id", "kunci@contoh.id"} {
		s.remember(email)
		rec := s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody(email)})
		s.remember(s.mail.lastCode(t))
		rec.Header().Del("Date")
		rec.Header().Del("Set-Cookie")
		if first == nil {
			first = rec
			continue
		}
		if rec.Code != first.Code || rec.Body.String() != first.Body.String() || fmt.Sprint(rec.Header()) != fmt.Sprint(first.Header()) {
			t.Fatalf("%s answered %d %v %s, the first %d %v %s", email, rec.Code, rec.Header(), rec.Body, first.Code, first.Header(), first.Body)
		}
	}
}

func (s *stack) googleSignIn(t *testing.T, claims oidctest.Claims, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	start := s.do(call{method: "GET", target: "/api/auth/google/start"})
	expect(t, start, http.StatusSeeOther, "")
	q := s.issuer.Authorize(t, start.Header().Get("Location"), claims)
	s.remember(q.Get("code"), q.Get("state"))
	return s.do(call{method: "GET", target: "/api/auth/google/callback?" + q.Encode(),
		cookies: append(cookies, cookie(t, start, "__Host-terka_oauth"))})
}

func TestGoogleCallbackAlwaysRedirectsToTheCallbackPage(t *testing.T) {
	s := newStack(t, false)
	s.remember("ani@gmail.com")
	rec := s.googleSignIn(t, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/callback" {
		t.Fatalf("success: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if c := cookie(t, rec, "__Host-terka_session"); c.MaxAge != 400*24*60*60 {
		t.Fatalf("session cookie = %+v", c)
	}
	if c := cookie(t, rec, "__Host-terka_oauth"); c.MaxAge >= 0 {
		t.Fatalf("oauth cookie not cleared: %+v", c)
	}

	start := s.do(call{method: "GET", target: "/api/auth/google/start"})
	state := s.issuer.Authorize(t, start.Header().Get("Location"), nil).Get("state")
	for _, tc := range []struct {
		name, query string
		cookie      bool
		want        string
	}{
		{"cancelled", "error=access_denied&state=" + state, true, "/auth/callback?error=cancelled"},
		{"no cookie", "code=x&state=" + state, false, "/auth/callback?error=other_browser"},
		{"wrong state", "code=x&state=nope", true, "/auth/callback?error=failed"},
		{"redirect smuggling", "code=x&state=nope&next=https://evil.example&redirect_uri=https://evil.example", true, "/auth/callback?error=failed"},
	} {
		c := call{method: "GET", target: "/api/auth/google/callback?" + tc.query}
		if tc.cookie {
			c.cookies = []*http.Cookie{cookie(t, start, "__Host-terka_oauth")}
		}
		rec := s.do(c)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.want {
			t.Errorf("%s: %d %q, want %q", tc.name, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
}

func TestGoogleStartIsLimitedPerIP(t *testing.T) {
	s := newStack(t, false)
	for range 30 {
		expect(t, s.do(call{method: "GET", target: "/api/auth/google/start"}), http.StatusSeeOther, "")
	}
	expect(t, s.do(call{method: "GET", target: "/api/auth/google/start"}), http.StatusTooManyRequests, "rate_limited")
}

func TestCrossSiteVerifyIsRefused(t *testing.T) {
	s := newStack(t, false)
	binding, code := s.requestCode(t, "ani@contoh.id")
	rec := s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code),
		cookies: []*http.Cookie{binding}, header: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site verify: %d", rec.Code)
	}
	rec = s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code),
		cookies: []*http.Cookie{binding}, header: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	expect(t, rec, http.StatusOK, "")
}

func TestLogoutIsPostOnly(t *testing.T) {
	s := newStack(t, false)
	rec := s.do(call{method: "GET", target: "/api/auth/logout"})
	expect(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if rec.Header().Get("Allow") != "POST" {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestSignInRevokesTheSessionAlreadyInThisBrowser(t *testing.T) {
	s := newStack(t, false)
	planted := s.signIn(t, "penyerang@contoh.id")
	s.signIn(t, "ani@contoh.id", planted)
	expect(t, s.do(call{method: "GET", target: "/api/auth/session", cookies: []*http.Cookie{planted}}), http.StatusUnauthorized, "signed_out")

	planted = s.signIn(t, "penyerang2@contoh.id")
	s.remember("ani@gmail.com")
	s.googleSignIn(t, nil, planted)
	expect(t, s.do(call{method: "GET", target: "/api/auth/session", cookies: []*http.Cookie{planted}}), http.StatusUnauthorized, "signed_out")
}

func TestRandomSessionCookieLooksSignedOut(t *testing.T) {
	s := newStack(t, false)
	without := s.do(call{method: "GET", target: "/api/auth/session"})
	random := s.do(call{method: "GET", target: "/api/auth/session",
		cookies: []*http.Cookie{{Name: "__Host-terka_session", Value: "dGhpcyBpcyBub3QgYSByZWFsIHNlc3Npb24gdG9rZW4"}}})
	without.Header().Del("Date")
	random.Header().Del("Date")
	if without.Code != random.Code || without.Body.String() != random.Body.String() || fmt.Sprint(without.Header()) != fmt.Sprint(random.Header()) {
		t.Fatalf("no cookie: %d %v %s\nrandom: %d %v %s", without.Code, without.Header(), without.Body, random.Code, random.Header(), random.Body)
	}
}

func TestLogoutRevokesTheSessionServerSide(t *testing.T) {
	s := newStack(t, false)
	session := s.signIn(t, "ani@contoh.id")
	rec := s.do(call{method: "POST", target: "/api/auth/logout", cookies: []*http.Cookie{session}})
	expect(t, rec, http.StatusNoContent, "")
	if c := cookie(t, rec, "__Host-terka_session"); c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("session cookie not cleared: %+v", c)
	}
	expect(t, s.do(call{method: "GET", target: "/api/auth/session", cookies: []*http.Cookie{session}}), http.StatusUnauthorized, "signed_out")
}

func TestLogoutWithoutSessionIs204(t *testing.T) {
	s := newStack(t, false)
	expect(t, s.do(call{method: "POST", target: "/api/auth/logout"}), http.StatusNoContent, "")
	expect(t, s.do(call{method: "POST", target: "/api/auth/logout",
		cookies: []*http.Cookie{{Name: "__Host-terka_session", Value: "unknown"}}}), http.StatusNoContent, "")
}

func TestOversizedBodyIsRejected(t *testing.T) {
	s := newStack(t, false)
	for _, path := range []string{"/api/auth/email/request", "/api/auth/email/verify"} {
		expect(t, s.do(call{method: "POST", target: path, body: `{"email":"` + strings.Repeat("a", 9000) + `"}`}), http.StatusBadRequest, "bad_request")
	}
}

func TestUnknownJSONFieldIsRejected(t *testing.T) {
	s := newStack(t, false)
	expect(t, s.do(call{method: "POST", target: "/api/auth/email/request",
		body: `{"email":"ani@contoh.id","turnstile":"t","redirect":"https://evil.example"}`}), http.StatusBadRequest, "bad_request")
	if s.mail.count() != 0 {
		t.Fatal("a request with an unknown field sent a code")
	}
}

func TestPerIPLimitUsesTheConnectionAddressUnlessCloudflareIsTrusted(t *testing.T) {
	send := func(s *stack, i int) *httptest.ResponseRecorder {
		email := fmt.Sprintf("orang%d@contoh.id", i)
		s.remember(email)
		rec := s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody(email),
			header: map[string]string{"CF-Connecting-IP": fmt.Sprintf("198.51.100.%d", i)}})
		if rec.Code == http.StatusAccepted {
			s.remember(s.mail.lastCode(t))
		}
		return rec
	}
	untrusted := newStack(t, false)
	for i := range 10 {
		expect(t, send(untrusted, i), http.StatusAccepted, "")
	}
	expect(t, send(untrusted, 10), http.StatusTooManyRequests, "rate_limited")

	trusted := newStack(t, true)
	for i := range 11 {
		expect(t, send(trusted, i), http.StatusAccepted, "")
	}
	var ip string
	query(t, trusted.auth, "select host(request_ip) from auth.email_codes where email = 'orang10@contoh.id'", nil, &ip)
	if ip != "198.51.100.10" {
		t.Fatalf("stored request ip %q, want the Cloudflare client address", ip)
	}
}

func TestSessionRemovedAnswers(t *testing.T) {
	s := newStack(t, false)
	user := createUser(t, s.auth, "ani@contoh.id")
	var removedSession, current auth.Session
	err := s.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		var err error
		if _, removedSession, err = s.sessions.Create(context.Background(), tx, user, "email"); err != nil {
			return err
		}
		_, current, err = s.sessions.Create(context.Background(), tx, user, "email")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	device := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	if err := s.app.AsUser(ctx, db.Claims{UserID: user, SessionID: removedSession.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "insert into public.devices (id, created_at, label, platform) values ($1, now(), 'HP lama', 'android')", device)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.app.AsUser(ctx, db.Claims{UserID: user, SessionID: current.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "select public.remove_device($1)", device)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	removed := func(session string) string {
		rec := s.do(call{method: "GET", target: "/api/auth/session-removed?session=" + url.QueryEscape(session)})
		expect(t, rec, http.StatusOK, "")
		return strings.TrimSpace(rec.Body.String())
	}
	if got := removed(removedSession.ID.String()); got != `{"removed":true}` {
		t.Fatalf("removed device's session: %s", got)
	}
	if got := removed(current.ID.String()); got != `{"removed":false}` {
		t.Fatalf("current session: %s", got)
	}
	if got := removed(uuid.NewString()); got != `{"removed":false}` {
		t.Fatalf("unknown session: %s", got)
	}
	expect(t, s.do(call{method: "GET", target: "/api/auth/session-removed?session=not-a-uuid"}), http.StatusBadRequest, "invalid_session")
	for range 60 - 4 {
		s.do(call{method: "GET", target: "/api/auth/session-removed?session=" + uuid.NewString()})
	}
	expect(t, s.do(call{method: "GET", target: "/api/auth/session-removed?session=" + uuid.NewString()}), http.StatusTooManyRequests, "rate_limited")
}

func TestNoEndpointAcceptsAPassword(t *testing.T) {
	s := newStack(t, false)
	for _, path := range []string{"/api/auth/email/request", "/api/auth/email/verify", "/api/auth/logout"} {
		expect(t, s.do(call{method: "POST", target: path, body: `{"password":"hunter2"}`}), http.StatusBadRequest, "bad_request")
	}
	for _, path := range []string{"/api/auth/session", "/api/auth/google/start", "/api/auth/google/callback", "/api/auth/session-removed"} {
		expect(t, s.do(call{method: "POST", target: path, body: `{"password":"hunter2"}`}), http.StatusMethodNotAllowed, "method_not_allowed")
	}
	for _, path := range []string{"/api/auth/password", "/api/auth/signup", "/api/auth/password/reset"} {
		expect(t, s.do(call{method: "POST", target: path, body: `{"password":"hunter2"}`}), http.StatusNotFound, "not_found")
	}
}

// Every flow, including the failing ones, runs against one log; the cleanup of newStack then
// checks that no address, code, token, cookie value, state or Google code reached it.
func TestNoSecretReachesTheLogs(t *testing.T) {
	s := newStack(t, false)
	session := s.signIn(t, "ani@contoh.id")
	s.do(call{method: "GET", target: "/api/auth/session", cookies: []*http.Cookie{session}})
	s.do(call{method: "POST", target: "/api/auth/logout", cookies: []*http.Cookie{session}})

	s.clock.Advance(61 * time.Second)
	binding, code := s.requestCode(t, "ani@contoh.id")
	s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", wrong(code)), cookies: []*http.Cookie{binding}})
	s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code)})
	s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("ani@contoh.id")})

	s.mail.err = fmt.Errorf("550 <budi@contoh.id> rejected")
	s.remember("budi@contoh.id")
	s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("budi@contoh.id")})
	s.mail.err = nil
	s.turnstile.err = fmt.Errorf("turnstile: challenge failed")
	s.remember("citra@contoh.id")
	s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("citra@contoh.id")})
	s.turnstile.err = nil

	s.remember("ani@gmail.com", "dewi@contoh.id")
	s.googleSignIn(t, nil)
	s.googleSignIn(t, oidctest.Claims{"nonce": "wrong"})
	s.googleSignIn(t, oidctest.Claims{"sub": "g-9", "email": "ani@contoh.id"})
	s.googleSignIn(t, oidctest.Claims{"email": "dewi@contoh.id", "email_verified": false})

	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.Contains(s.logs.String(), "google sign-in failed") || !strings.Contains(s.logs.String(), `"method":"email"`) {
		t.Fatalf("the flows did not log what they should:\n%s", s.logs.String())
	}
}
