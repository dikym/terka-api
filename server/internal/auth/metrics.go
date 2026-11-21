package auth

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics counts sign-ins and emails. Attributes are fixed words only, never an email address,
// user, client IP or code. Every method does nothing on a nil *Metrics.
type Metrics struct {
	signins, emailSent, emailRefused, mailboxLocked metric.Int64Counter
}

func NewMetrics(mp metric.MeterProvider) (*Metrics, error) {
	meter := mp.Meter("github.com/dikym/terka-api/server/internal/auth")
	m := &Metrics{}
	var err error
	for _, c := range []struct {
		dst  *metric.Int64Counter
		name string
	}{
		{&m.signins, "terka.auth.signins"},
		{&m.emailSent, "terka.auth.email.sent"},
		{&m.emailRefused, "terka.auth.email.refused"},
		{&m.mailboxLocked, "terka.auth.mailbox.locked"},
	} {
		if *c.dst, err = meter.Int64Counter(c.name); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// SignIn records one sign-in attempt; reason is kept only for refused ones.
func (m *Metrics) SignIn(ctx context.Context, method, outcome, reason string) {
	if m == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("method", method), attribute.String("outcome", outcome)}
	if outcome == "refused" {
		attrs = append(attrs, attribute.String("reason", reason))
	}
	m.signins.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func (m *Metrics) EmailSent(ctx context.Context, ok bool) {
	if m == nil {
		return
	}
	outcome := "failed"
	if ok {
		outcome = "ok"
	}
	m.emailSent.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (m *Metrics) EmailRefused(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	m.emailRefused.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

func (m *Metrics) MailboxLocked(ctx context.Context) {
	if m == nil {
		return
	}
	m.mailboxLocked.Add(ctx, 1)
}
