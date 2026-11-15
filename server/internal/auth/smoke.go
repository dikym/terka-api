package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/store"
)

// SmokeEmail is the account the deploy smoke check signs in as. The .invalid domain can never
// receive mail, so no sign-in code reaches anyone. Every deploy signs it in afresh, so copies of the
// database and any count of users or sign-ins must leave it out.
const SmokeEmail = "smoke@terka.invalid"

// A session the check fails to revoke still lapses on its own, and cleanup removes it.
const smokeTTL = 5 * time.Minute

// Smoke runs the session flow against a server before it takes traffic: a session for SmokeEmail is
// recognised through its cookie, the same request without it is signed out, and the session is
// revoked whatever happens. base has no trailing slash.
func Smoke(ctx context.Context, s *Sessions, base string, client *http.Client) (err error) {
	token, hash, err := NewToken()
	if err != nil {
		return err
	}
	// Registered before the session exists: a create that commits but reports an error is revoked
	// too, and revoking a token that was never stored is a no-op. A server that cannot revoke fails.
	defer func() {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if rerr := s.Revoke(revokeCtx, token); rerr != nil && err == nil {
			err = fmt.Errorf("revoke the session: %w", rerr)
		}
	}()
	now := s.Now()
	err = s.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		user, err := q.UpsertEmailUser(ctx, store.UpsertEmailUserParams{Email: SmokeEmail, Now: &now})
		if err != nil {
			return err
		}
		_, err = q.CreateSession(ctx, store.CreateSessionParams{
			UserID: user, TokenHash: hash, Now: now,
			ExpiresAt: now.Add(smokeTTL), AbsoluteExpiresAt: now.Add(smokeTTL), Method: "email",
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}

	status, body, err := getSession(ctx, client, base, token)
	if err != nil {
		return fmt.Errorf("signed-in request: %w", err)
	}
	var got sessionJSON
	if status != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Email != SmokeEmail {
		return fmt.Errorf("signed-in request: status %d, body %.80s", status, body)
	}
	if status, _, err = getSession(ctx, client, base, ""); err != nil {
		return fmt.Errorf("signed-out request: %w", err)
	}
	if status != http.StatusUnauthorized {
		return fmt.Errorf("signed-out request: status %d, want 401", status)
	}
	return nil
}

func getSession(ctx context.Context, client *http.Client, base, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/auth/session", nil)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	return res.StatusCode, body, err
}
