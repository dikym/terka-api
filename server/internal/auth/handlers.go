package auth

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/httpx"
)

const (
	sessionCookie = "__Host-terka_session"
	bindingCookie = "__Host-terka_email_binding"
	oauthCookie   = "__Host-terka_oauth"
	bindingTTL    = 15 * time.Minute
	callbackPage  = "/auth/callback"
)

type Handlers struct {
	Codes    *EmailCodes
	Google   *Google // nil when no Google client is configured; its routes are then not mounted
	Sessions *Sessions
	Auth     *db.Auth
	App      *db.App

	Env             string
	TrustCloudflare bool
	Log             *slog.Logger
	Now             func() time.Time
	Metrics         *Metrics

	requestLimit, verifyLimit, googleLimit, removedLimit *Limiter
}

func clientErr(status int, code, message string) error {
	return &httpx.Error{Status: status, Code: code, Message: message}
}

var (
	errRateLimited = clientErr(http.StatusTooManyRequests, "rate_limited", "too many requests, try again later")
	errSignedOut   = clientErr(http.StatusUnauthorized, "signed_out", "not signed in")
)

func (h *Handlers) Mount(mux *http.ServeMux) {
	h.requestLimit, h.verifyLimit = NewLimiter(10), NewLimiter(30)
	h.googleLimit, h.removedLimit = NewLimiter(30), NewLimiter(60)

	route := func(method, path string, handler http.Handler) {
		mux.Handle(method+" "+path, handler)
		// Without this, another method would fall through to the catch-all 404.
		mux.Handle(path, httpx.Handle(func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Allow", method)
			return clientErr(http.StatusMethodNotAllowed, "method_not_allowed", "use "+method)
		}, h.Log))
	}
	handle := func(method, path string, fn httpx.Handler) { route(method, path, httpx.Handle(fn, h.Log)) }

	handle("POST", "/api/auth/email/request", h.emailRequest)
	handle("POST", "/api/auth/email/verify", h.emailVerify)
	if h.Google != nil {
		handle("GET", "/api/auth/google/start", h.googleStart)
		route("GET", "/api/auth/google/callback", httpx.Recover(http.HandlerFunc(h.googleCallback), h.Log))
	}
	handle("GET", "/api/auth/session", h.RequireSession(func(w http.ResponseWriter, _ *http.Request, s Session) error {
		return httpx.JSON(w, http.StatusOK, sessionBody(s))
	}))
	handle("POST", "/api/auth/logout", h.logout)
	handle("GET", "/api/auth/session-removed", h.sessionRemoved)
}

func (h *Handlers) RequireSession(next func(w http.ResponseWriter, r *http.Request, s Session) error) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		token := cookieValue(r, sessionCookie)
		if token == "" {
			return errSignedOut
		}
		s, err := h.Sessions.Lookup(r.Context(), token)
		if errors.Is(err, ErrSignedOut) {
			return errSignedOut
		}
		if err != nil {
			return err
		}
		return next(w, r, s)
	}
}

type sessionJSON struct {
	UserID    uuid.UUID `json:"user_id"`
	SessionID uuid.UUID `json:"session_id"`
	Email     string    `json:"email"`
	Method    string    `json:"method"`
}

func sessionBody(s Session) sessionJSON {
	return sessionJSON{UserID: s.UserID, SessionID: s.ID, Email: s.Email, Method: s.Method}
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: int(maxAge / time.Second),
		HttpOnly: true, Secure: true, SameSite: sameSite,
	})
}

func clearCookie(w http.ResponseWriter, name string, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: sameSite})
}

func retryAfter(w http.ResponseWriter, err error) {
	var ra RetryAfter
	if errors.As(err, &ra) {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(ra.RetryAfter().Seconds())))))
	}
}

// signedIn sets the new session cookie and revokes the session this browser held before, so a
// cookie planted before sign-in is worth nothing after it.
func (h *Handlers) signedIn(w http.ResponseWriter, r *http.Request, token string) {
	if old := cookieValue(r, sessionCookie); old != "" {
		if err := h.Sessions.Revoke(r.Context(), old); err != nil {
			h.Log.WarnContext(r.Context(), "previous session not revoked", "error", err)
		}
	}
	setCookie(w, sessionCookie, token, sessionAbsolute, http.SameSiteLaxMode)
}

func (h *Handlers) emailRequest(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email     string `json:"email"`
		Turnstile string `json:"turnstile"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if _, err := NormalizeEmail(in.Email); err != nil {
		return clientErr(http.StatusBadRequest, "invalid_email", "the email address is not valid")
	}
	ip := httpx.ClientIP(r, h.TrustCloudflare)
	if !h.requestLimit.Allow(IPKey(ip), h.Now()) {
		h.Metrics.EmailRefused(r.Context(), "rate_limited")
		return errRateLimited
	}
	res, err := h.Codes.Request(r.Context(), in.Email, in.Turnstile, ip, cookieValue(r, bindingCookie))
	switch {
	case errors.Is(err, ErrInvalidEmail):
		return clientErr(http.StatusBadRequest, "invalid_email", "the email address is not valid")
	case errors.Is(err, ErrCaptcha):
		h.Metrics.EmailRefused(r.Context(), "captcha")
		return clientErr(http.StatusBadRequest, "captcha_failed", "the human check failed")
	case errors.Is(err, ErrTooSoon):
		h.Metrics.EmailRefused(r.Context(), "too_soon")
		retryAfter(w, err)
		return clientErr(http.StatusTooManyRequests, "too_soon", "a code was sent recently")
	case errors.Is(err, ErrServiceLimit):
		h.Metrics.EmailRefused(r.Context(), "service_limit")
		return errRateLimited
	case err != nil:
		return err
	}
	setCookie(w, bindingCookie, res.Binding, bindingTTL, http.SameSiteStrictMode)
	return httpx.JSON(w, http.StatusAccepted, struct{}{})
}

func (h *Handlers) emailVerify(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if !h.verifyLimit.Allow(IPKey(httpx.ClientIP(r, h.TrustCloudflare)), h.Now()) {
		h.Metrics.SignIn(ctx, "email", "refused", "rate_limited")
		return errRateLimited
	}
	token, s, err := h.Codes.Verify(ctx, in.Email, in.Code, cookieValue(r, bindingCookie))
	switch {
	case errors.Is(err, ErrOtherBrowser):
		h.Metrics.SignIn(ctx, "email", "refused", "other_browser")
		return clientErr(http.StatusBadRequest, "other_browser", "open the code in the browser that asked for it")
	case errors.Is(err, ErrInvalid):
		h.Metrics.SignIn(ctx, "email", "refused", "invalid_code")
		return clientErr(http.StatusBadRequest, "invalid", "the code is wrong or no longer valid")
	case errors.Is(err, ErrLocked):
		h.Metrics.SignIn(ctx, "email", "refused", "locked")
		retryAfter(w, err)
		return clientErr(http.StatusTooManyRequests, "locked", "too many wrong codes, try again later")
	case err != nil:
		h.Metrics.SignIn(ctx, "email", "refused", "failed")
		return err
	}
	h.Metrics.SignIn(ctx, "email", "ok", "")
	h.signedIn(w, r, token)
	clearCookie(w, bindingCookie, http.SameSiteStrictMode)
	return httpx.JSON(w, http.StatusOK, sessionBody(s))
}

func (h *Handlers) googleStart(w http.ResponseWriter, r *http.Request) error {
	if !h.googleLimit.Allow(IPKey(httpx.ClientIP(r, h.TrustCloudflare)), h.Now()) {
		h.Metrics.SignIn(r.Context(), "google", "refused", "rate_limited")
		return errRateLimited
	}
	redirect, cookie, err := h.Google.Start()
	if err != nil {
		return err
	}
	setCookie(w, oauthCookie, cookie, oauthTTL, http.SameSiteLaxMode)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, redirect, http.StatusSeeOther)
	return nil
}

// googleCallback always lands on the client's callback page, never on an address taken from the
// request.
func (h *Handlers) googleCallback(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, oauthCookie, http.SameSiteLaxMode)
	w.Header().Set("Cache-Control", "no-store")
	token, _, err := h.Google.Callback(r.Context(), r.URL.Query(), cookieValue(r, oauthCookie))
	target := callbackPage
	switch {
	case errors.Is(err, ErrCancelled):
		h.Metrics.SignIn(r.Context(), "google", "refused", "cancelled")
		target += "?error=cancelled"
	case errors.Is(err, ErrOtherBrowser):
		h.Metrics.SignIn(r.Context(), "google", "refused", "other_browser")
		target += "?error=other_browser"
	case err != nil:
		reason := "failed"
		if errors.Is(err, ErrUnverified) {
			reason = "unverified"
		}
		h.Metrics.SignIn(r.Context(), "google", "refused", reason)
		h.Log.WarnContext(r.Context(), "google sign-in failed", "error", err)
		target += "?error=failed"
	default:
		h.Metrics.SignIn(r.Context(), "google", "ok", "")
		h.signedIn(w, r, token)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) error {
	// Logout takes no input; refusing a body keeps every auth endpoint from accepting stray fields.
	if n, _ := io.ReadFull(io.LimitReader(r.Body, 1), make([]byte, 1)); n > 0 {
		return clientErr(http.StatusBadRequest, "bad_request", "logout takes no body")
	}
	// Cleared first: even when the server cannot end the session, this browser stops carrying it,
	// and the 500 tells the client the session may still be alive elsewhere.
	clearCookie(w, sessionCookie, http.SameSiteLaxMode)
	if token := cookieValue(r, sessionCookie); token != "" {
		if err := h.Sessions.Revoke(r.Context(), token); err != nil {
			return err
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handlers) sessionRemoved(w http.ResponseWriter, r *http.Request) error {
	if !h.removedLimit.Allow(IPKey(httpx.ClientIP(r, h.TrustCloudflare)), h.Now()) {
		return errRateLimited
	}
	id, err := uuid.Parse(r.URL.Query().Get("session"))
	if err != nil {
		return clientErr(http.StatusBadRequest, "invalid_session", "session must be a uuid")
	}
	var removed bool
	err = h.App.AsAnon(r.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), "select public.session_removed($1)", id).Scan(&removed)
	})
	if err != nil {
		return err
	}
	return httpx.JSON(w, http.StatusOK, map[string]bool{"removed": removed})
}
