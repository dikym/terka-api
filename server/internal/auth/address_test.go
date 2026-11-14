package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dikym/terka-api/server/internal/auth"
)

func TestNormalizeEmailAcceptsAndCanonicalizes(t *testing.T) {
	got, err := auth.NormalizeEmail(" Alice@Example.COM ")
	if err != nil || got != "alice@example.com" {
		t.Fatalf("got %q, %v; want alice@example.com", got, err)
	}
}

func TestNormalizeEmailRejects(t *testing.T) {
	for _, raw := range []string{
		"",
		"a",
		"a@b",
		"@b.co",
		"a@",
		"a@@b.co",
		"a@b@c.co",
		"+terka@contoh.id",
		"+@contoh.id",
		"a b@c.co",
		"a@b.co\r\nBcc: x@y.co",
		"a\n@b.co",
		"\x00a@b.co",
		"a\x7f@b.co",
		"ünï@b.co",
		"a@bü.co",
		"a<b>@c.co",
		"\"a\"@c.co",
		"a,b@c.co",
		"a;b@c.co",
		"a\\b@c.co",
		"a@.co",
		"a@b.",
		strings.Repeat("a", 245) + "@example.co",
	} {
		if got, err := auth.NormalizeEmail(raw); !errors.Is(err, auth.ErrInvalidEmail) {
			t.Errorf("%q: got %q, %v; want ErrInvalidEmail", raw, got, err)
		}
	}
}

func TestNormalizeEmailAcceptsTheLongestAddress(t *testing.T) {
	raw := strings.Repeat("a", 242) + "@example.co" // 254 characters
	if _, err := auth.NormalizeEmail(raw); err != nil {
		t.Fatalf("a 254-character address was refused: %v", err)
	}
}

func FuzzNormalizeEmail(f *testing.F) {
	for _, seed := range []string{"a@b.co", " A@B.CO ", "a@b.co\r\nx", "ü@b.co", "a@@b.co", "\"x\"@y.z"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := auth.NormalizeEmail(raw)
		if err != nil {
			return
		}
		if strings.Count(got, "@") != 1 || len(got) > 254 {
			t.Fatalf("%q normalized to %q", raw, got)
		}
		for _, r := range got {
			if r < 0x21 || r > 0x7e || strings.ContainsRune(`<>()[],;:"\`, r) {
				t.Fatalf("%q normalized to %q, which holds %q", raw, got, r)
			}
		}
		if got != strings.ToLower(got) {
			t.Fatalf("%q normalized to %q, which is not lower case", raw, got)
		}
	})
}

func TestMailboxGroupsVariants(t *testing.T) {
	for email, want := range map[string]string{
		"a.li.ce+x@gmail.com":  "alice@gmail.com",
		"alice@googlemail.com": "alice@gmail.com",
		"alice+y@gmail.com":    "alice@gmail.com",
		"alice@gmail.com":      "alice@gmail.com",
		"a.lice+z@example.com": "a.lice@example.com",
		"a.lice@example.com":   "a.lice@example.com",
	} {
		if got := auth.Mailbox(email); got != want {
			t.Errorf("Mailbox(%q) = %q, want %q", email, got, want)
		}
	}
}
