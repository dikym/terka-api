package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/dikym/terka-api/server/internal/auth"
	"github.com/dikym/terka-api/server/internal/auth/oidctest"
)

func newMetricsProvider() (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	reader := sdkmetric.NewManualReader()
	return reader, sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
}

// counter sums the data points of one counter whose attributes match exactly.
func counter(t *testing.T, reader *sdkmetric.ManualReader, name string, attrs map[string]string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				got := map[string]string{}
				for _, kv := range dp.Attributes.ToSlice() {
					got[string(kv.Key)] = kv.Value.String()
				}
				if fmt.Sprint(got) == fmt.Sprint(attrs) {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func wantCount(t *testing.T, reader *sdkmetric.ManualReader, name string, attrs map[string]string, want int64) {
	t.Helper()
	if got := counter(t, reader, name, attrs); got != want {
		t.Errorf("%s %v = %d, want %d", name, attrs, got, want)
	}
}

func TestNilMetricsRecordNothingAndDoNotPanic(t *testing.T) {
	var m *auth.Metrics
	ctx := context.Background()
	m.SignIn(ctx, "email", "refused", "locked")
	m.EmailSent(ctx, true)
	m.EmailRefused(ctx, "captcha")
	m.MailboxLocked(ctx)
}

func TestEmailSignInsAreCountedByOutcomeAndReason(t *testing.T) {
	reader, mp := newMetricsProvider()
	s := newStackWithMetrics(t, false, mp)
	post := func(target, body string, cookies ...*http.Cookie) {
		s.do(call{method: "POST", target: target, body: body, cookies: cookies})
	}

	binding, code := s.requestCode(t, "ani@contoh.id")
	post("/api/auth/email/verify", verifyBody("ani@contoh.id", code))
	post("/api/auth/email/verify", verifyBody("ani@contoh.id", wrong(code)), binding)
	post("/api/auth/email/verify", verifyBody("ani@contoh.id", code), binding)

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
	post("/api/auth/email/verify", verifyBody("ani@contoh.id", code), binding)

	signins := func(attrs map[string]string, want int64) {
		t.Helper()
		wantCount(t, reader, "terka.auth.signins", attrs, want)
	}
	signins(map[string]string{"method": "email", "outcome": "ok"}, 1)
	signins(map[string]string{"method": "email", "outcome": "refused", "reason": "other_browser"}, 1)
	signins(map[string]string{"method": "email", "outcome": "refused", "reason": "invalid_code"}, 1)
	signins(map[string]string{"method": "email", "outcome": "refused", "reason": "locked"}, 1)

	for range 30 {
		post("/api/auth/email/verify", verifyBody("ani@contoh.id", "123456"))
	}
	signins(map[string]string{"method": "email", "outcome": "refused", "reason": "rate_limited"}, 4)
}

func TestEmailFailuresAreCountedAsFailed(t *testing.T) {
	reader, mp := newMetricsProvider()
	s := newStackWithMetrics(t, false, mp)
	s.mail.err = fmt.Errorf("550 rejected")
	s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody("ani@contoh.id")})
	wantCount(t, reader, "terka.auth.email.sent", map[string]string{"outcome": "failed"}, 1)
	wantCount(t, reader, "terka.auth.email.sent", map[string]string{"outcome": "ok"}, 0)
}

func TestEmailRequestsAreCountedAsSentOrRefused(t *testing.T) {
	reader, mp := newMetricsProvider()
	s := newStackWithMetrics(t, false, mp)
	request := func(email string) {
		s.do(call{method: "POST", target: "/api/auth/email/request", body: requestBody(email)})
	}

	request("ani@contoh.id")
	request("ani@contoh.id")
	s.turnstile.err = fmt.Errorf("turnstile: challenge failed")
	request("budi@contoh.id")
	s.turnstile.err = nil
	err := s.auth.AsAuth(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `insert into auth.email_codes (email, mailbox, code_hmac, binding_hash, created_at, expires_at)
			select 'x' || i || '@contoh.id', 'x' || i || '@contoh.id', '\x00', '\x00', $1, $1 from generate_series(1, 80) i`, s.clock.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	request("citra@contoh.id")

	wantCount(t, reader, "terka.auth.email.sent", map[string]string{"outcome": "ok"}, 1)
	wantCount(t, reader, "terka.auth.email.refused", map[string]string{"reason": "too_soon"}, 1)
	wantCount(t, reader, "terka.auth.email.refused", map[string]string{"reason": "captcha"}, 1)
	wantCount(t, reader, "terka.auth.email.refused", map[string]string{"reason": "service_limit"}, 1)

	for range 10 {
		request("dewi@contoh.id")
	}
	wantCount(t, reader, "terka.auth.email.refused", map[string]string{"reason": "rate_limited"}, 4)
}

func TestGoogleSignInsAreCountedByOutcomeAndReason(t *testing.T) {
	reader, mp := newMetricsProvider()
	s := newStackWithMetrics(t, false, mp)

	s.googleSignIn(t, nil)
	s.googleSignIn(t, oidctest.Claims{"email": "dewi@contoh.id", "email_verified": false})
	s.googleSignIn(t, oidctest.Claims{"nonce": "wrong"})
	start := s.do(call{method: "GET", target: "/api/auth/google/start"})
	state := s.issuer.Authorize(t, start.Header().Get("Location"), nil).Get("state")
	s.do(call{method: "GET", target: "/api/auth/google/callback?error=access_denied&state=" + state,
		cookies: []*http.Cookie{cookie(t, start, "__Host-terka_oauth")}})
	s.do(call{method: "GET", target: "/api/auth/google/callback?code=x&state=" + state})

	signins := func(attrs map[string]string, want int64) {
		t.Helper()
		wantCount(t, reader, "terka.auth.signins", attrs, want)
	}
	signins(map[string]string{"method": "google", "outcome": "ok"}, 1)
	signins(map[string]string{"method": "google", "outcome": "refused", "reason": "unverified"}, 1)
	signins(map[string]string{"method": "google", "outcome": "failed"}, 1)
	signins(map[string]string{"method": "google", "outcome": "refused", "reason": "cancelled"}, 1)
	signins(map[string]string{"method": "google", "outcome": "refused", "reason": "other_browser"}, 1)

	for range 30 {
		s.do(call{method: "GET", target: "/api/auth/google/start"})
	}
	signins(map[string]string{"method": "google", "outcome": "refused", "reason": "rate_limited"}, 4)
}

func TestUnverifiedGoogleAddressStillLandsOnTheFailedPage(t *testing.T) {
	s := newStack(t, false)
	rec := s.googleSignIn(t, oidctest.Claims{"email": "dewi@contoh.id", "email_verified": false})
	if got := rec.Header().Get("Location"); got != "/auth/callback?error=failed" {
		t.Fatalf("Location = %q", got)
	}
}

func TestMailboxLockIsCountedOnceAtTheTwentiethWrongCode(t *testing.T) {
	reader, mp := newMetricsProvider()
	e := newCodesEnv(t)
	metrics, err := auth.NewMetrics(mp)
	if err != nil {
		t.Fatal(err)
	}
	e.codes.Metrics = metrics

	e.fail(t, "ani@contoh.id", 3)
	wantCount(t, reader, "terka.auth.mailbox.locked", map[string]string{}, 0)

	binding, code := e.request(t, "ani@contoh.id", "")
	for range 4 {
		_, err := e.verify("ani@contoh.id", wrong(code), binding)
		assertIs(t, "wrong guess", err, auth.ErrInvalid)
	}
	wantCount(t, reader, "terka.auth.mailbox.locked", map[string]string{}, 0)
	_, err = e.verify("ani@contoh.id", wrong(code), binding)
	assertIs(t, "twentieth wrong guess", err, auth.ErrInvalid)
	wantCount(t, reader, "terka.auth.mailbox.locked", map[string]string{}, 1)

	e.clock.Advance(61 * time.Second)
	binding, code = e.request(t, "ani@contoh.id", "")
	_, err = e.verify("ani@contoh.id", code, binding)
	assertIs(t, "21st attempt", err, auth.ErrLocked)
	wantCount(t, reader, "terka.auth.mailbox.locked", map[string]string{}, 1)
}

func TestServerErrorOnEmailVerifyIsCountedAsFailedWithoutReason(t *testing.T) {
	reader, mp := newMetricsProvider()
	s := newStackWithMetrics(t, false, mp)
	binding, code := s.requestCode(t, "ani@contoh.id")
	s.auth.Close()
	rec := s.do(call{method: "POST", target: "/api/auth/email/verify", body: verifyBody("ani@contoh.id", code), cookies: []*http.Cookie{binding}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	wantCount(t, reader, "terka.auth.signins", map[string]string{"method": "email", "outcome": "failed"}, 1)
}

func TestSmokeCheckIsNotCountedAsASignIn(t *testing.T) {
	reader, mp := newMetricsProvider()
	st := newStackWithMetrics(t, false, mp)
	srv := httptest.NewServer(st.handler)
	defer srv.Close()
	if err := auth.Smoke(context.Background(), st.sessions, srv.URL, srv.Client()); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "terka.auth.signins" {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && len(sum.DataPoints) > 0 {
				t.Fatalf("smoke check recorded sign-ins: %+v", sum.DataPoints)
			}
		}
	}
}
