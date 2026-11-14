package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	turnstileEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileTimeout  = 5 * time.Second
	turnstileAction   = "email_request"
	// Cloudflare documents tokens as at most 2048 characters.
	turnstileMaxToken = 2048
)

// Cloudflare's published test secrets always pass, always fail, or report a spent token.
var turnstileTestSecret = regexp.MustCompile(`^[123]x0{31}AA$`)

// IsTurnstileTestSecret reports whether secret is one of Cloudflare's public test secrets, which
// anyone can use and so must never guard a real deployment.
func IsTurnstileTestSecret(secret string) bool { return turnstileTestSecret.MatchString(secret) }

// Turnstile checks the proof of a human that comes with an email code request.
type Turnstile interface {
	// Verify returns nil only when Cloudflare confirms the token was solved on one of our hostnames
	// for the email request. Any failure, including Cloudflare being unreachable, refuses.
	Verify(ctx context.Context, token, ip string) error
}

type turnstile struct {
	secret    string
	hostnames []string
	endpoint  string
	client    *http.Client
	testing   bool
}

func NewTurnstile(secret string, hostnames []string, endpoint string, client *http.Client) Turnstile {
	if endpoint == "" {
		endpoint = turnstileEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: turnstileTimeout}
	}
	return &turnstile{secret: secret, hostnames: hostnames, endpoint: endpoint, client: client, testing: IsTurnstileTestSecret(secret)}
}

type siteverify struct {
	Success  bool   `json:"success"`
	Hostname string `json:"hostname"`
	Action   string `json:"action"`
	Metadata struct {
		Testing bool `json:"result_with_testing_key"`
	} `json:"metadata"`
}

func (t *turnstile) Verify(ctx context.Context, token, ip string) error {
	if token == "" || len(token) > turnstileMaxToken {
		return errors.New("turnstile: missing or oversized token")
	}
	ctx, cancel := context.WithTimeout(ctx, turnstileTimeout)
	defer cancel()
	form := url.Values{"secret": {t.secret}, "response": {token}, "remoteip": {ip}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: status %d", res.StatusCode)
	}
	var v siteverify
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&v); err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	if !v.Success {
		return errors.New("turnstile: challenge failed")
	}
	// Test secrets answer for example.com without an action; they are refused outside local.
	if t.testing {
		if !v.Metadata.Testing {
			return errors.New("turnstile: test secret got a real result")
		}
		return nil
	}
	if v.Metadata.Testing {
		return errors.New("turnstile: testing result under a live secret")
	}
	if !slices.Contains(t.hostnames, v.Hostname) {
		return fmt.Errorf("turnstile: solved on %q", v.Hostname)
	}
	if v.Action != turnstileAction {
		return fmt.Errorf("turnstile: action %q", v.Action)
	}
	return nil
}
