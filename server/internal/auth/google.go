package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/store"
)

const (
	oauthTTL   = 10 * time.Minute
	googleWait = 10 * time.Second
)

// GoogleProvider holds Google's published endpoints, so starting the server needs no request to
// Google; its signing keys are fetched on the first sign-in and refreshed as they rotate.
var GoogleProvider = oidc.ProviderConfig{
	IssuerURL:  "https://accounts.google.com",
	AuthURL:    "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:   "https://oauth2.googleapis.com/token",
	JWKSURL:    "https://www.googleapis.com/oauth2/v3/certs",
	Algorithms: []string{oidc.RS256},
}

var (
	ErrCancelled    = errors.New("sign-in cancelled at google")
	ErrGoogleFailed = errors.New("google sign-in failed")
)

// Google signs people in with their Google account over OpenID Connect with PKCE. What the callback
// must match travels in a sealed cookie, so any server instance, including one restarted since the
// start, can finish the sign-in.
type Google struct {
	Auth     *db.Auth
	Sessions *Sessions
	Keys     Keys
	Config   *oauth2.Config
	Verifier *oidc.IDTokenVerifier
	Now      func() time.Time
	client   *http.Client
}

// NewGoogle signs in against provider, which is GoogleProvider outside tests.
func NewGoogle(ctx context.Context, provider oidc.ProviderConfig, clientID, clientSecret, redirectURL string,
	keys Keys, a *db.Auth, sessions *Sessions, now func() time.Time) *Google {
	client := &http.Client{Timeout: googleWait}
	// The key set keeps this context to fetch signing keys later, so it must outlive the caller's.
	p := provider.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), client))
	return &Google{
		Auth:     a,
		Sessions: sessions,
		Keys:     keys,
		Config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     p.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		Verifier: p.Verifier(&oidc.Config{ClientID: clientID, Now: now}),
		Now:      now,
		client:   client,
	}
}

type oauthState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
}

// Start returns where to send the browser and the sealed cookie it must bring back.
func (g *Google) Start() (string, string, error) {
	state, _, err := NewToken()
	if err != nil {
		return "", "", err
	}
	nonce, _, err := NewToken()
	if err != nil {
		return "", "", err
	}
	verifier := oauth2.GenerateVerifier()
	plain, err := json.Marshal(oauthState{State: state, Nonce: nonce, Verifier: verifier})
	if err != nil {
		return "", "", err
	}
	cookie, err := Seal(g.Keys.OAuthCookie, plain, g.Now(), oauthTTL)
	if err != nil {
		return "", "", err
	}
	return g.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), cookie, nil
}

func failed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGoogleFailed, fmt.Sprintf(format, args...))
}

// Callback finishes a sign-in. Besides ErrCancelled and ErrOtherBrowser, every failure is
// ErrGoogleFailed with the cause attached for the log.
func (g *Google) Callback(ctx context.Context, query url.Values, cookie string) (string, Session, error) {
	if e := query.Get("error"); e == "access_denied" {
		return "", Session{}, ErrCancelled
	} else if e != "" {
		return "", Session{}, failed("provider error %q", e)
	}
	if cookie == "" {
		return "", Session{}, ErrOtherBrowser
	}
	plain, err := Open(g.Keys.OAuthCookie, cookie, g.Now())
	if err != nil {
		return "", Session{}, failed("state cookie: %v", err)
	}
	var st oauthState
	if err := json.Unmarshal(plain, &st); err != nil {
		return "", Session{}, failed("state cookie: %v", err)
	}
	if subtle.ConstantTimeCompare([]byte(st.State), []byte(query.Get("state"))) != 1 {
		return "", Session{}, failed("state does not match")
	}

	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	tok, err := g.Config.Exchange(ctx, query.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return "", Session{}, failed("code exchange: %v", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", Session{}, failed("no id token")
	}
	idToken, err := g.Verifier.Verify(oidc.ClientContext(ctx, g.client), raw)
	if err != nil {
		return "", Session{}, failed("id token: %v", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(st.Nonce)) != 1 {
		return "", Session{}, failed("nonce does not match")
	}
	var claims struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
		Domain   string `json:"hd"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", Session{}, failed("claims: %v", err)
	}
	if !claims.Verified || idToken.Subject == "" {
		return "", Session{}, failed("address not verified at google")
	}
	addr, err := NormalizeEmail(claims.Email)
	if err != nil {
		return "", Session{}, failed("address not usable")
	}

	var token string
	var sess Session
	err = g.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := g.Now()
		if err := q.LockMailbox(ctx, Mailbox(addr)); err != nil {
			return err
		}
		userID, err := g.findOrCreate(ctx, q, idToken.Subject, addr, googleVouches(addr, claims.Domain), now)
		if err != nil {
			return err
		}
		token, sess, err = g.Sessions.Create(ctx, tx, userID, "google")
		return err
	})
	if err != nil {
		if errors.Is(err, ErrGoogleFailed) {
			return "", Session{}, err
		}
		return "", Session{}, fmt.Errorf("%w: %w", ErrGoogleFailed, err)
	}
	return token, sess, nil
}

// googleVouches reports whether Google is the authority for addr: its own mail domains, or the
// Workspace domain named in the hd claim. Anywhere else email_verified only says the address was
// proven once, not that this person still controls it.
func googleVouches(addr, hostedDomain string) bool {
	domain := addr[strings.LastIndex(addr, "@")+1:]
	return domain == "gmail.com" || domain == "googlemail.com" || (hostedDomain != "" && strings.EqualFold(hostedDomain, domain))
}

// findOrCreate looks the person up by Google account first, then by address. An address is linked
// only when Google vouches for it, to an account that proved it owns it and that no other Google
// account claimed.
func (g *Google) findOrCreate(ctx context.Context, q *store.Queries, sub, addr string, vouched bool, now time.Time) (uuid.UUID, error) {
	id, err := q.UserByGoogleSub(ctx, &sub)
	if err == nil {
		return id, q.SignInGoogleUser(ctx, store.SignInGoogleUserParams{ID: id, Email: addr, TakeAddress: vouched, Now: &now})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, err
	}
	user, err := q.UserByEmail(ctx, addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return q.CreateGoogleUser(ctx, store.CreateGoogleUserParams{Email: addr, GoogleSub: &sub, Now: &now})
	}
	if err != nil {
		return uuid.UUID{}, err
	}
	if !vouched {
		return uuid.UUID{}, failed("google does not vouch for the address of an existing account")
	}
	if user.GoogleSub != nil {
		return uuid.UUID{}, failed("address belongs to another google account")
	}
	if user.EmailVerifiedAt == nil {
		return uuid.UUID{}, failed("address belongs to an unverified account")
	}
	return user.ID, q.LinkGoogleUser(ctx, store.LinkGoogleUserParams{ID: user.ID, GoogleSub: &sub, Now: &now})
}
