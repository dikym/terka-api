package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/auth"
)

const turnstileSecret = "live-secret"

// fakeTurnstile answers siteverify with reply and records the last form it received.
func fakeTurnstile(t *testing.T, status int, reply string) (string, *url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("siteverify form: %v", err)
		}
		got = r.PostForm
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &got
}

func verdict(success bool, hostname, action string) string {
	b, _ := json.Marshal(map[string]any{"success": success, "hostname": hostname, "action": action})
	return string(b)
}

func TestTurnstileAcceptsAValidToken(t *testing.T) {
	endpoint, form := fakeTurnstile(t, 200, verdict(true, "terka.my.id", "email_request"))
	ts := auth.NewTurnstile(turnstileSecret, []string{"staging.terka.my.id", "terka.my.id"}, endpoint, nil)
	if err := ts.Verify(context.Background(), "token-1", "203.0.113.7"); err != nil {
		t.Fatalf("valid token refused: %v", err)
	}
	if form.Get("secret") != turnstileSecret || form.Get("response") != "token-1" {
		t.Fatalf("form = %v", *form)
	}
}

func TestTurnstileRejects(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
		_, _ = w.Write([]byte(verdict(true, "terka.my.id", "email_request")))
	}))
	t.Cleanup(slow.Close)

	for _, tc := range []struct {
		name   string
		status int
		reply  string
	}{
		{"failed challenge", 200, verdict(false, "terka.my.id", "email_request")},
		{"other site", 200, verdict(true, "evil.example", "email_request")},
		{"other action", 200, verdict(true, "terka.my.id", "login")},
		{"no action", 200, verdict(true, "terka.my.id", "")},
		{"server error", 500, verdict(true, "terka.my.id", "email_request")},
		{"not json", 200, "<html>oops</html>"},
		{"testing key result under a live secret", 200,
			`{"success":true,"hostname":"terka.my.id","action":"email_request","metadata":{"result_with_testing_key":true}}`},
	} {
		endpoint, _ := fakeTurnstile(t, tc.status, tc.reply)
		ts := auth.NewTurnstile(turnstileSecret, []string{"terka.my.id"}, endpoint, nil)
		if err := ts.Verify(context.Background(), "token-1", "203.0.113.7"); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}

	ts := auth.NewTurnstile(turnstileSecret, []string{"terka.my.id"}, slow.URL, &http.Client{Timeout: 100 * time.Millisecond})
	if err := ts.Verify(context.Background(), "token-1", "203.0.113.7"); err == nil {
		t.Error("timeout: accepted")
	}
	endpoint, form := fakeTurnstile(t, 200, verdict(true, "terka.my.id", "email_request"))
	for _, token := range []string{"", string(make([]byte, 2049))} {
		ts := auth.NewTurnstile(turnstileSecret, []string{"terka.my.id"}, endpoint, nil)
		if err := ts.Verify(context.Background(), token, "203.0.113.7"); err == nil || *form != nil {
			t.Errorf("token of %d bytes: err = %v, sent = %v; want refused without asking Cloudflare", len(token), err, *form)
		}
	}
}

func TestTurnstileSendsTheRemoteIP(t *testing.T) {
	endpoint, form := fakeTurnstile(t, 200, verdict(true, "terka.my.id", "email_request"))
	ts := auth.NewTurnstile(turnstileSecret, []string{"terka.my.id"}, endpoint, nil)
	if err := ts.Verify(context.Background(), "token-1", "2001:db8::1"); err != nil {
		t.Fatal(err)
	}
	if form.Get("remoteip") != "2001:db8::1" {
		t.Fatalf("remoteip = %q", form.Get("remoteip"))
	}
}

// Cloudflare's test secrets answer for example.com with no action, so locally only the testing
// marker is checked.
func TestTurnstileTestSecretAcceptsTestingResults(t *testing.T) {
	const testSecret = "1x0000000000000000000000000000000AA"
	endpoint, _ := fakeTurnstile(t, 200, `{"success":true,"hostname":"example.com","metadata":{"result_with_testing_key":true}}`)
	if err := auth.NewTurnstile(testSecret, []string{"localhost"}, endpoint, nil).Verify(context.Background(), "XXXX.DUMMY.TOKEN.XXXX", "127.0.0.1"); err != nil {
		t.Fatalf("test secret: %v", err)
	}
	endpoint, _ = fakeTurnstile(t, 200, `{"success":false,"hostname":"example.com","metadata":{"result_with_testing_key":true}}`)
	if err := auth.NewTurnstile(testSecret, []string{"localhost"}, endpoint, nil).Verify(context.Background(), "XXXX.DUMMY.TOKEN.XXXX", "127.0.0.1"); err == nil {
		t.Fatal("test secret: failed challenge accepted")
	}
}
