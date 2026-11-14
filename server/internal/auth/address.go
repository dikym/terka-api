// Package auth signs people in: email codes, Google, and the sessions both produce.
package auth

import (
	"errors"
	"strings"
)

var ErrInvalidEmail = errors.New("invalid email")

// Characters that would let an address change an email header or be read two ways.
const forbidden = `<>()[],;:"\`

// NormalizeEmail returns the address in the one form the server stores and compares. It accepts
// plain ASCII addresses only: anything that could smuggle a header, hide a lookalike character or be
// parsed two ways is refused before it reaches a query or an email.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) == 0 || len(email) > 254 {
		return "", ErrInvalidEmail
	}
	for i := 0; i < len(email); i++ {
		c := email[i]
		if c < 0x21 || c > 0x7e || strings.IndexByte(forbidden, c) >= 0 {
			return "", ErrInvalidEmail
		}
	}
	local, domain, ok := strings.Cut(email, "@")
	// A local part that is only a "+tag" would leave Mailbox an empty name, shared by every such address.
	if !ok || local == "" || local[0] == '+' || strings.Contains(domain, "@") || !strings.Contains(domain, ".") ||
		strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// Mailbox maps the variants that deliver to one inbox onto one key, so per-address limits cannot be
// multiplied with "+tag" or, on Gmail, with dots. The account itself keeps the address as typed.
func Mailbox(email string) string {
	local, domain, _ := strings.Cut(email, "@")
	local, _, _ = strings.Cut(local, "+")
	if domain == "gmail.com" || domain == "googlemail.com" {
		return strings.ReplaceAll(local, ".", "") + "@gmail.com"
	}
	return local + "@" + domain
}
