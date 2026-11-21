package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/store"
)

const (
	codeTTL        = 15 * time.Minute
	requestGap     = time.Minute
	mailboxPerHour = 5
	servicePerHour = 80
	servicePerDay  = 90
	// Twenty failures in this window lock the mailbox; the count is in the TwentiethFailure query.
	lockWindow = 24 * time.Hour
)

var (
	ErrCaptcha      = errors.New("captcha failed")
	ErrTooSoon      = errors.New("too soon")
	ErrServiceLimit = errors.New("service email limit reached")
	ErrInvalid      = errors.New("invalid code")
	ErrLocked       = errors.New("mailbox locked")
	ErrOtherBrowser = errors.New("code requested in another browser")
)

func sixDigits(code string) bool {
	return len(code) == 6 && strings.Trim(code, "0123456789") == ""
}

// RetryAfter is carried by ErrTooSoon and ErrLocked: how long until trying again can work.
type RetryAfter interface{ RetryAfter() time.Duration }

type retryError struct {
	err   error
	after time.Duration
}

func (e *retryError) Error() string              { return e.err.Error() }
func (e *retryError) Unwrap() error              { return e.err }
func (e *retryError) RetryAfter() time.Duration  { return e.after }
func retry(err error, after time.Duration) error { return &retryError{err: err, after: after} }

// EmailCodes signs people in with a six-digit code sent to their address. A code only works in the
// browser that asked for it, which holds the matching binding cookie.
type EmailCodes struct {
	Auth      *db.Auth
	Sessions  *Sessions
	Keys      Keys
	Mailer    Mailer
	Turnstile Turnstile
	PublicURL string
	Now       func() time.Time
	Log       *slog.Logger
	Metrics   *Metrics
}

// RequestResult holds the new binding cookie value for the requesting browser.
type RequestResult struct{ Binding string }

func (e *EmailCodes) bindingHash(binding string) []byte {
	mac := hmac.New(sha256.New, e.Keys.BindingMAC)
	mac.Write([]byte(binding))
	return mac.Sum(nil)
}

// Request sends a fresh code. Its answer never depends on whether the address has an account or is
// locked, so it cannot be used to learn either.
func (e *EmailCodes) Request(ctx context.Context, email, turnstile, ip, oldBinding string) (RequestResult, error) {
	addr, err := NormalizeEmail(email)
	if err != nil {
		return RequestResult{}, err
	}
	if err := e.Turnstile.Verify(ctx, turnstile, ip); err != nil {
		e.Log.InfoContext(ctx, "email code refused", "reason", "captcha", "error", err)
		return RequestResult{}, ErrCaptcha
	}
	mailbox := Mailbox(addr)
	code, err := NewCode()
	if err != nil {
		return RequestResult{}, err
	}
	binding, _, err := NewToken()
	if err != nil {
		return RequestResult{}, err
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		ip = ""
	}

	err = e.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := e.Now()
		if err := q.LockMailbox(ctx, mailbox); err != nil {
			return err
		}
		mine, err := q.MailboxRequests(ctx, store.MailboxRequestsParams{Mailbox: mailbox, Since: now.Add(-time.Hour)})
		if err != nil {
			return err
		}
		if mine.Requests > 0 && now.Sub(mine.Newest) < requestGap {
			return retry(ErrTooSoon, mine.Newest.Add(requestGap).Sub(now))
		}
		if mine.Requests >= mailboxPerHour {
			return retry(ErrTooSoon, mine.Oldest.Add(time.Hour).Sub(now))
		}
		if err := q.LockEmailService(ctx); err != nil {
			return err
		}
		utc := now.UTC()
		service, err := q.ServiceRequests(ctx, store.ServiceRequestsParams{
			HourAgo:  now.Add(-time.Hour),
			DayStart: time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		if service.LastHour >= servicePerHour || service.Today >= servicePerDay {
			return ErrServiceLimit
		}
		if oldBinding != "" {
			if err := q.BurnBindingCodes(ctx, store.BurnBindingCodesParams{BindingHash: e.bindingHash(oldBinding), Now: &now}); err != nil {
				return err
			}
		}
		return q.InsertEmailCode(ctx, store.InsertEmailCodeParams{
			Email:       addr,
			Mailbox:     mailbox,
			CodeHmac:    CodeMAC(e.Keys, addr, code),
			BindingHash: e.bindingHash(binding),
			Now:         now,
			ExpiresAt:   now.Add(codeTTL),
			RequestIp:   ip,
		})
	})
	if err != nil {
		if errors.Is(err, ErrTooSoon) || errors.Is(err, ErrServiceLimit) {
			e.Log.InfoContext(ctx, "email code refused", "reason", err.Error())
		}
		return RequestResult{}, err
	}

	// The code is already stored, so a failed send answers like a sent one; the person asks again.
	mail, err := CodeMail(e.PublicURL, addr, code)
	if err == nil {
		// The code is stored and counted already; a caller who leaves must not cancel its email.
		err = e.Mailer.Send(context.WithoutCancel(ctx), mail)
	}
	e.Metrics.EmailSent(ctx, err == nil)
	if err != nil {
		// Mail servers echo the recipient in their errors.
		e.Log.ErrorContext(ctx, "email code not sent", "error", strings.ReplaceAll(strings.ToLower(err.Error()), addr, "<recipient>"))
	}
	return RequestResult{Binding: binding}, nil
}

// Verify trades a code for a session. Failures are counted only against a live code, so guessing at
// an address that asked for nothing cannot lock it.
func (e *EmailCodes) Verify(ctx context.Context, email, code, binding string) (string, Session, error) {
	if binding == "" {
		return "", Session{}, ErrOtherBrowser
	}
	addr, err := NormalizeEmail(email)
	if err != nil {
		return "", Session{}, ErrInvalid
	}
	// A typo or a stray space is not a guess at the code, so it costs none of the tries.
	code = strings.TrimSpace(code)
	if !sixDigits(code) {
		return "", Session{}, ErrInvalid
	}
	mailbox := Mailbox(addr)

	var token string
	var sess Session
	// A wrong guess must be recorded, so it commits; the outcome travels outside the transaction.
	var outcome error
	var locked bool
	err = e.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := e.Now()
		if err := q.LockMailbox(ctx, mailbox); err != nil {
			return err
		}
		live, err := q.LiveEmailCode(ctx, store.LiveEmailCodeParams{Email: addr, BindingHash: e.bindingHash(binding), Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalid
			return nil
		}
		if err != nil {
			return err
		}
		// Checked only for the browser that holds a live code, so nobody else learns the mailbox is locked.
		twentieth, err := q.TwentiethFailure(ctx, store.TwentiethFailureParams{Mailbox: mailbox, Since: now.Add(-lockWindow)})
		if err == nil {
			outcome = retry(ErrLocked, twentieth.Add(lockWindow).Sub(now))
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !hmac.Equal(live.CodeHmac, CodeMAC(e.Keys, addr, code)) {
			if err := q.FailEmailCode(ctx, live.ID); err != nil {
				return err
			}
			if err := q.RecordSigninFailure(ctx, store.RecordSigninFailureParams{Mailbox: mailbox, Now: now}); err != nil {
				return err
			}
			_, err := q.TwentiethFailure(ctx, store.TwentiethFailureParams{Mailbox: mailbox, Since: now.Add(-lockWindow)})
			if err == nil {
				locked = true
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			outcome = ErrInvalid
			return nil
		}
		if err := q.UseEmailCode(ctx, store.UseEmailCodeParams{ID: live.ID, Now: &now}); err != nil {
			return err
		}
		userID, err := q.UpsertEmailUser(ctx, store.UpsertEmailUserParams{Email: addr, Now: &now})
		if err != nil {
			return err
		}
		token, sess, err = e.Sessions.Create(ctx, tx, userID, "email")
		return err
	})
	if err != nil {
		return "", Session{}, fmt.Errorf("verify email code: %w", err)
	}
	if locked {
		e.Metrics.MailboxLocked(ctx)
	}
	if outcome != nil {
		e.Log.InfoContext(ctx, "email sign-in refused", "reason", outcome.Error())
		return "", Session{}, outcome
	}
	e.Log.InfoContext(ctx, "signed in", "method", "email", "user_id", sess.UserID)
	return token, sess, nil
}
