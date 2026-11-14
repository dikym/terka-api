package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/auth/oidctest"
	"github.com/dikym/terka-api/server/internal/db"
)

const (
	googleClient   = "terka-client"
	googleRedirect = "https://terka.my.id/api/auth/google/callback"
)

type googleEnv struct {
	google *auth.Google
	issuer *oidctest.Issuer
	clock  *clock
	auth   *db.Auth
}

func newGoogleEnv(t *testing.T) *googleEnv {
	t.Helper()
	_, a := pools(t)
	e := &googleEnv{issuer: oidctest.NewIssuer(t, googleClient), clock: newClock(), auth: a}
	e.issuer.Now = e.clock.Now
	e.google = e.newGoogle(t)
	return e
}

func (e *googleEnv) newGoogle(t *testing.T) *auth.Google {
	t.Helper()
	return auth.NewGoogle(context.Background(), e.issuer.Provider(), googleClient, "terka-secret", googleRedirect,
		testKeys, e.auth, &auth.Sessions{Auth: e.auth, Now: e.clock.Now}, e.clock.Now)
}

func (e *googleEnv) signIn(t *testing.T, claims oidctest.Claims) (auth.Session, error) {
	t.Helper()
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	_, s, err := e.google.Callback(context.Background(), e.issuer.Authorize(t, redirect, claims), cookie)
	return s, err
}

func (e *googleEnv) sessions(t *testing.T) int {
	t.Helper()
	var n int
	query(t, e.auth, "select count(*) from auth.sessions", nil, &n)
	return n
}

func TestGoogleSignInCreatesAUser(t *testing.T) {
	e := newGoogleEnv(t)
	s, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Email != "ani@gmail.com" || s.Method != "google" {
		t.Fatalf("session = %+v", s)
	}
	var sub string
	query(t, e.auth, "select google_sub from auth.users where id = $1 and email_verified_at is not null and last_sign_in_at is not null",
		[]any{s.UserID}, &sub)
	if sub != "g-1" {
		t.Fatalf("google_sub = %q", sub)
	}
}

func TestGoogleFindsTheUserBySub(t *testing.T) {
	e := newGoogleEnv(t)
	first, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani.baru@gmail.com"})
	if err != nil || second.UserID != first.UserID || second.Email != "ani.baru@gmail.com" {
		t.Fatalf("second sign-in %+v, %v; want user %s with the new address", second, err, first.UserID)
	}
}

func TestGoogleKeepsTheAddressWhenTheNewOneBelongsToAnotherAccount(t *testing.T) {
	e := newGoogleEnv(t)
	other := createUser(t, e.auth, "budi@gmail.com")
	first, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@gmail.com"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "budi@gmail.com"})
	if err != nil || second.UserID != first.UserID || second.UserID == other || second.Email != "ani@gmail.com" {
		t.Fatalf("got %+v, %v; want user %s still on its old address", second, err, first.UserID)
	}
}

func TestGoogleLinksToAVerifiedEmailUser(t *testing.T) {
	e := newGoogleEnv(t)
	id := createUser(t, e.auth, "ani@contoh.id")
	s, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@contoh.id", "hd": "contoh.id"})
	if err != nil || s.UserID != id {
		t.Fatalf("got %+v, %v; want user %s", s, err, id)
	}
	var sub string
	query(t, e.auth, "select google_sub from auth.users where id = $1", []any{id}, &sub)
	if sub != "g-1" {
		t.Fatalf("google_sub = %q, want the link recorded", sub)
	}
}

func TestGoogleMatchesAddressCaseInsensitively(t *testing.T) {
	e := newGoogleEnv(t)
	id := createUser(t, e.auth, "ani@contoh.id")
	s, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "Ani@Contoh.ID", "hd": "Contoh.ID"})
	if err != nil || s.UserID != id {
		t.Fatalf("got %+v, %v; want user %s", s, err, id)
	}
}

func TestGoogleRefusesAnEmailOwnedByAnotherSub(t *testing.T) {
	e := newGoogleEnv(t)
	if _, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@gmail.com"}); err != nil {
		t.Fatal(err)
	}
	_, err := e.signIn(t, oidctest.Claims{"sub": "g-2", "email": "ani@gmail.com"})
	assertIs(t, "address of another Google account", err, auth.ErrGoogleFailed)
	if n := e.sessions(t); n != 1 {
		t.Fatalf("%d sessions, want only the first", n)
	}
}

func TestGoogleDoesNotLinkToAnUnverifiedUser(t *testing.T) {
	e := newGoogleEnv(t)
	var id uuid.UUID
	err := e.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "insert into auth.users (email) values ('ani@contoh.id') returning id").Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@contoh.id"})
	assertIs(t, "unverified user", err, auth.ErrGoogleFailed)
	var linked bool
	query(t, e.auth, "select google_sub is not null from auth.users where id = $1", []any{id}, &linked)
	if linked || e.sessions(t) != 0 {
		t.Fatal("linked to or signed into an unverified user")
	}
}

func TestCallbackWithoutCookieIsOtherBrowser(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, _, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.google.Callback(context.Background(), e.issuer.Authorize(t, redirect, nil), "")
	assertIs(t, "no cookie", err, auth.ErrOtherBrowser)
}

func TestCallbackWithWrongStateFails(t *testing.T) {
	e := newGoogleEnv(t)
	for _, state := range []string{"another-state", ""} {
		redirect, cookie, err := e.google.Start()
		if err != nil {
			t.Fatal(err)
		}
		q := e.issuer.Authorize(t, redirect, nil)
		q.Set("state", state)
		_, _, err = e.google.Callback(context.Background(), q, cookie)
		assertIs(t, "state "+state, err, auth.ErrGoogleFailed)
	}
	if e.sessions(t) != 0 {
		t.Fatal("a session was made")
	}
}

func TestCallbackWithStateOfAnotherStartFails(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, _, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	_, otherCookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.google.Callback(context.Background(), e.issuer.Authorize(t, redirect, nil), otherCookie)
	assertIs(t, "cookie of another start", err, auth.ErrGoogleFailed)
}

func TestCallbackWithExpiredCookieFails(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	q := e.issuer.Authorize(t, redirect, nil)
	e.clock.Advance(10*time.Minute + time.Second)
	_, _, err = e.google.Callback(context.Background(), q, cookie)
	assertIs(t, "expired cookie", err, auth.ErrGoogleFailed)
}

func TestTamperedCookieFails(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	q := e.issuer.Authorize(t, redirect, nil)
	for _, i := range []int{0, len(cookie) / 2, len(cookie) - 2} {
		flipped := []byte(cookie)
		if flipped[i] == 'A' {
			flipped[i] = 'B'
		} else {
			flipped[i] = 'A'
		}
		_, _, err = e.google.Callback(context.Background(), q, string(flipped))
		assertIs(t, "tampered cookie", err, auth.ErrGoogleFailed)
	}
}

func TestCallbackCannotBeReplayed(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	q := e.issuer.Authorize(t, redirect, nil)
	if _, _, err := e.google.Callback(context.Background(), q, cookie); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.google.Callback(context.Background(), q, cookie)
	assertIs(t, "replayed callback", err, auth.ErrGoogleFailed)
	if e.sessions(t) != 1 {
		t.Fatal("a replay made a second session")
	}
}

func TestCallbackCookieWorksAfterRestart(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	restarted := e.newGoogle(t)
	if _, _, err := restarted.Callback(context.Background(), e.issuer.Authorize(t, redirect, nil), cookie); err != nil {
		t.Fatalf("after restart: %v", err)
	}
}

func TestAccessDeniedIsCancelled(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	q := url.Values{"error": {"access_denied"}, "state": {u.Query().Get("state")}}
	_, _, err = e.google.Callback(context.Background(), q, cookie)
	assertIs(t, "access denied", err, auth.ErrCancelled)
	q = url.Values{"error": {"server_error"}, "state": {u.Query().Get("state")}}
	_, _, err = e.google.Callback(context.Background(), q, cookie)
	assertIs(t, "other provider error", err, auth.ErrGoogleFailed)
}

func TestStartUsesPKCEWithS256(t *testing.T) {
	e := newGoogleEnv(t)
	redirect, cookie, err := e.google.Start()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil || !strings.HasPrefix(redirect, e.issuer.URL+"/auth?") {
		t.Fatalf("redirect %s, %v", redirect, err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 {
		t.Errorf("PKCE: %q %q", q.Get("code_challenge_method"), q.Get("code_challenge"))
	}
	if len(q.Get("state")) < 43 || len(q.Get("nonce")) < 43 || q.Get("state") == q.Get("nonce") {
		t.Errorf("state %q, nonce %q", q.Get("state"), q.Get("nonce"))
	}
	if q.Get("scope") != "openid email profile" || q.Get("redirect_uri") != googleRedirect || q.Get("client_id") != googleClient {
		t.Errorf("query %v", q)
	}
	if q.Has("code_verifier") || strings.Contains(cookie, q.Get("state")) {
		t.Error("the verifier or state travels in the clear")
	}
	_, second, err := e.google.Start()
	if err != nil || second == cookie {
		t.Fatal("two starts share a cookie")
	}
}

func TestCallbackRejects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims oidctest.Claims
		other  bool
	}{
		{"signed with another key", nil, true},
		{"another issuer", oidctest.Claims{"iss": "https://accounts.example.com"}, false},
		{"another audience", oidctest.Claims{"aud": "someone-else"}, false},
		{"expired", oidctest.Claims{"exp": time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC).Unix()}, false},
		{"another nonce", oidctest.Claims{"nonce": "replayed-nonce"}, false},
		{"no nonce", oidctest.Claims{"nonce": nil}, false},
		{"unverified email", oidctest.Claims{"email_verified": false}, false},
		{"verified as a string", oidctest.Claims{"email_verified": "true"}, false},
		{"no email", oidctest.Claims{"email": nil}, false},
		{"malformed email", oidctest.Claims{"email": "ani@gmail.com\r\nBcc: x@y.co"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGoogleEnv(t)
			if tc.other {
				e.issuer.SignWithAnotherKey()
			}
			_, err := e.signIn(t, tc.claims)
			if !errors.Is(err, auth.ErrGoogleFailed) {
				t.Fatalf("err = %v, want ErrGoogleFailed", err)
			}
			if e.sessions(t) != 0 {
				t.Fatal("a session was made")
			}
		})
	}
}

// Google vouches for an address only on its own domains or on a Workspace domain it hosts; elsewhere
// email_verified means the address was once proven, not that this person controls it now.
func TestGoogleLinksByAddressOnlyWhereGoogleIsTheAuthority(t *testing.T) {
	e := newGoogleEnv(t)
	createUser(t, e.auth, "ani@contoh.id")
	for _, claims := range []oidctest.Claims{
		{"sub": "g-1", "email": "ani@contoh.id"},
		{"sub": "g-1", "email": "ani@contoh.id", "hd": "lain.id"},
	} {
		_, err := e.signIn(t, claims)
		assertIs(t, fmt.Sprintf("link by %v", claims), err, auth.ErrGoogleFailed)
	}
	if e.sessions(t) != 0 {
		t.Fatal("linked without Google being the authority for the address")
	}
	gmail := createUser(t, e.auth, "budi@gmail.com")
	if s, err := e.signIn(t, oidctest.Claims{"sub": "g-2", "email": "budi@gmail.com"}); err != nil || s.UserID != gmail {
		t.Fatalf("gmail link: %+v, %v", s, err)
	}
}

func TestGoogleTakesANewAddressOnlyWhereGoogleIsTheAuthority(t *testing.T) {
	e := newGoogleEnv(t)
	if _, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@gmail.com"}); err != nil {
		t.Fatal(err)
	}
	s, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@contoh.id"})
	if err != nil || s.Email != "ani@gmail.com" {
		t.Fatalf("got %+v, %v; want the account kept on its Gmail address", s, err)
	}
}

func TestGoogleCreatesAnAccountForAnAddressNobodyHas(t *testing.T) {
	e := newGoogleEnv(t)
	if s, err := e.signIn(t, oidctest.Claims{"sub": "g-1", "email": "ani@contoh.id"}); err != nil || s.Email != "ani@contoh.id" {
		t.Fatalf("got %+v, %v", s, err)
	}
}
