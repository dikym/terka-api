package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dikym/terka-api/server/internal/db"
	"github.com/dikym/terka-api/server/internal/store"
)

const (
	sessionIdle      = 90 * 24 * time.Hour
	sessionAbsolute  = 400 * 24 * time.Hour
	sessionTouchStep = 10 * time.Minute
	// Kept a day past expiry, so a code that just expired is still found and reported as invalid.
	codeKeep = 24 * time.Hour
	// The per-mailbox lock looks back 24 hours.
	failureKeep = 24 * time.Hour
)

type Session struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Email  string
	Method string
}

var ErrSignedOut = errors.New("signed out")

type Sessions struct {
	Auth *db.Auth
	Now  func() time.Time
}

// Create starts a session inside the caller's sign-in transaction, so the session exists only if the
// sign-in that earned it commits.
func (s *Sessions) Create(ctx context.Context, tx pgx.Tx, userID uuid.UUID, method string) (string, Session, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.Now()
	row, err := store.New(tx).CreateSession(ctx, store.CreateSessionParams{
		UserID:            userID,
		TokenHash:         hash,
		Now:               now,
		ExpiresAt:         now.Add(sessionIdle),
		AbsoluteExpiresAt: now.Add(sessionAbsolute),
		Method:            method,
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, Session{ID: row.ID, UserID: row.UserID, Email: row.Email, Method: row.Method}, nil
}

// Lookup finds the session of a cookie. An unknown, expired or malformed token is signed out, with no
// way to tell those apart from outside.
func (s *Sessions) Lookup(ctx context.Context, token string) (Session, error) {
	var sess Session
	err := s.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.SessionByTokenHash(ctx, HashToken(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSignedOut
		}
		if err != nil {
			return err
		}
		now := s.Now()
		if now.After(row.ExpiresAt) || now.After(row.AbsoluteExpiresAt) {
			return ErrSignedOut
		}
		// Writing on every request would turn each read into a write; ten minutes is precise enough.
		if now.Sub(row.LastUsedAt) >= sessionTouchStep {
			if err := q.TouchSession(ctx, store.TouchSessionParams{ID: row.ID, Now: now, ExpiresAt: now.Add(sessionIdle)}); err != nil {
				return err
			}
		}
		sess = Session{ID: row.ID, UserID: row.UserID, Email: row.Email, Method: row.Method}
		return nil
	})
	return sess, err
}

// Revoke ends a session for good. Revoking one that does not exist is not an error.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	return s.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		return store.New(tx).DeleteSessionByTokenHash(ctx, HashToken(token))
	})
}

// Cleanup removes sessions, codes and failures that can no longer matter, and reports how many
// sessions it removed.
func (s *Sessions) Cleanup(ctx context.Context) (int64, error) {
	var removed int64
	err := s.Auth.AsAuth(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.Now()
		var err error
		if removed, err = q.DeleteExpiredSessions(ctx, now); err != nil {
			return err
		}
		if _, err = q.DeleteOldEmailCodes(ctx, now.Add(-codeKeep)); err != nil {
			return err
		}
		_, err = q.DeleteOldSigninFailures(ctx, now.Add(-failureKeep))
		return err
	})
	return removed, err
}
