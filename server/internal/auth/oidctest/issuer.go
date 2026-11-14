// Package oidctest is a fake OpenID Connect provider for tests: discovery, keys, and a token
// endpoint that checks PKCE and issues ID tokens with whatever claims a test asks for.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// Claims override the defaults of one ID token; a nil value removes the claim.
type Claims map[string]any

type grant struct {
	nonce, challenge, redirect string
	claims                     Claims
}

type Issuer struct {
	URL      string
	ClientID string
	// Now is the issuer's clock, for iat and exp.
	Now func() time.Time

	mu       sync.Mutex
	grants   map[string]grant
	otherKey bool
}

var keys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return signing, other
})

func NewIssuer(t *testing.T, clientID string) *Issuer {
	t.Helper()
	i := &Issuer{ClientID: clientID, Now: time.Now, grants: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                i.URL,
			"authorization_endpoint":                i.URL + "/auth",
			"token_endpoint":                        i.URL + "/token",
			"jwks_uri":                              i.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		signing, _ := keys()
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &signing.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", i.token)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	i.URL = srv.URL
	return i
}

// SignWithAnotherKey makes every later ID token carry a signature the published keys do not match.
func (i *Issuer) SignWithAnotherKey() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.otherKey = true
}

// Authorize plays the person approving the sign-in at the provider: it reads the authorization URL
// the app redirected to and returns the query the provider sends back to the callback. The ID token
// later issued for this code carries claims over the defaults.
func (i *Issuer) Authorize(t *testing.T, authURL string, claims Claims) url.Values {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("client_id") != i.ClientID || q.Get("response_type") != "code" {
		t.Fatalf("authorization request %v", q)
	}
	code := rand.Text()
	i.mu.Lock()
	i.grants[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: claims}
	i.mu.Unlock()
	return url.Values{"code": {code}, "state": {q.Get("state")}}
}

func (i *Issuer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clientID, _, ok := r.BasicAuth()
	if !ok {
		clientID = r.PostForm.Get("client_id")
	}
	i.mu.Lock()
	g, found := i.grants[r.PostForm.Get("code")]
	// Codes are single use, as at a real provider.
	delete(i.grants, r.PostForm.Get("code"))
	otherKey := i.otherKey
	i.mu.Unlock()

	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || clientID != i.ClientID || r.PostForm.Get("grant_type") != "authorization_code" ||
		r.PostForm.Get("redirect_uri") != g.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}

	now := i.Now()
	claims := Claims{
		"iss": i.URL, "aud": i.ClientID, "sub": "google-sub-1", "email": "ani@gmail.com", "email_verified": true,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": g.nonce,
	}
	for k, v := range g.claims {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	signing, other := keys()
	key := signing
	if otherKey {
		key = other
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payload, _ := json.Marshal(claims)
	signed, err := signer.Sign(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idToken, err := signed.CompactSerialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
