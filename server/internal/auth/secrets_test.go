package auth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/dikym/terka-api/server/internal/auth"
)

func master(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func keys(t *testing.T, b byte) auth.Keys {
	t.Helper()
	k, err := auth.DeriveKeys(master(b))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDeriveKeysRejectsShortMaster(t *testing.T) {
	if _, err := auth.DeriveKeys(bytes.Repeat([]byte{1}, 31)); err == nil {
		t.Fatal("a 31-byte master key was accepted")
	}
}

func TestDerivedKeysDiffer(t *testing.T) {
	k := keys(t, 7)
	all := [][]byte{k.CodeMAC, k.OAuthCookie, k.BindingMAC}
	for i, a := range all {
		if len(a) != 32 {
			t.Errorf("key %d has %d bytes, want 32", i, len(a))
		}
		for j, b := range all[i+1:] {
			if bytes.Equal(a, b) {
				t.Errorf("keys %d and %d are equal", i, i+1+j)
			}
		}
		if bytes.Equal(a, master(7)) {
			t.Errorf("key %d is the master key itself", i)
		}
	}
}

func TestNewCodeIsSixDigits(t *testing.T) {
	for range 10_000 {
		code, err := auth.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 || nonDigits(code) != "" {
			t.Fatalf("code %q is not six digits", code)
		}
	}
}

func nonDigits(s string) string {
	return string(bytes.Trim([]byte(s), "0123456789"))
}

func TestNewCodeCoversEveryDigitPosition(t *testing.T) {
	seen := [6]map[byte]bool{}
	for i := range seen {
		seen[i] = map[byte]bool{}
	}
	for range 10_000 {
		code, _ := auth.NewCode()
		for i := range 6 {
			seen[i][code[i]] = true
		}
	}
	for i, digits := range seen {
		if !digits['0'] || !digits['9'] {
			t.Errorf("position %d never held 0 or 9 in 10,000 codes", i)
		}
	}
}

func TestCodeMACDependsOnKeyAndEmail(t *testing.T) {
	base := auth.CodeMAC(keys(t, 1), "a@b.co", "123456")
	if bytes.Equal(base, auth.CodeMAC(keys(t, 2), "a@b.co", "123456")) {
		t.Error("the MAC does not depend on the key")
	}
	if bytes.Equal(base, auth.CodeMAC(keys(t, 1), "c@b.co", "123456")) {
		t.Error("the MAC does not depend on the address")
	}
	if bytes.Equal(base, auth.CodeMAC(keys(t, 1), "a@b.co", "123457")) {
		t.Error("the MAC does not depend on the code")
	}
	if bytes.Contains(base, []byte("123456")) {
		t.Error("the MAC holds the code")
	}
}

func TestTokenHashIsNotTheToken(t *testing.T) {
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token %q is not 32 bytes of base64url: %v", token, err)
	}
	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(hash, want[:]) || !bytes.Equal(auth.HashToken(token), want[:]) {
		t.Fatal("the hash is not SHA-256 of the token")
	}
	if bytes.Contains(hash, []byte(token)) {
		t.Fatal("the hash holds the token")
	}
}

func TestTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1_000 {
		token, _, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatalf("token %q came twice", token)
		}
		seen[token] = true
	}
}

var now = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func TestSealRoundTrip(t *testing.T) {
	k := keys(t, 3).OAuthCookie
	sealed, err := auth.Seal(k, []byte("state and nonce"), now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := auth.Open(k, sealed, now.Add(9*time.Minute))
	if err != nil || string(got) != "state and nonce" {
		t.Fatalf("got %q, %v", got, err)
	}
	if bytes.Contains([]byte(sealed), []byte("state")) {
		t.Fatal("the sealed value shows its plaintext")
	}
}

func TestOpenRejectsTamperedByte(t *testing.T) {
	k := keys(t, 3).OAuthCookie
	sealed, _ := auth.Seal(k, []byte("payload"), now, time.Minute)
	raw, _ := base64.RawURLEncoding.DecodeString(sealed)
	for i := range raw {
		tampered := bytes.Clone(raw)
		tampered[i] ^= 0x01
		if _, err := auth.Open(k, base64.RawURLEncoding.EncodeToString(tampered), now); err == nil {
			t.Fatalf("a change at byte %d was accepted", i)
		}
	}
	for _, junk := range []string{"", "x", "!!!", base64.RawURLEncoding.EncodeToString(raw[:10])} {
		if _, err := auth.Open(k, junk, now); err == nil {
			t.Fatalf("%q was accepted", junk)
		}
	}
}

func TestOpenRejectsExpired(t *testing.T) {
	k := keys(t, 3).OAuthCookie
	sealed, _ := auth.Seal(k, []byte("payload"), now, 10*time.Minute)
	if _, err := auth.Open(k, sealed, now.Add(10*time.Minute+time.Second)); err == nil {
		t.Fatal("an expired value was accepted")
	}
}

func TestOpenRejectsOtherKey(t *testing.T) {
	sealed, _ := auth.Seal(keys(t, 3).OAuthCookie, []byte("payload"), now, time.Minute)
	if _, err := auth.Open(keys(t, 4).OAuthCookie, sealed, now); err == nil {
		t.Fatal("a value sealed with another key was accepted")
	}
}

func TestOpenWorksInANewProcessWithTheSameKey(t *testing.T) {
	sealed, _ := auth.Seal(keys(t, 5).OAuthCookie, []byte("payload"), now, time.Minute)
	// A restarted server derives its keys again from the same master key.
	if got, err := auth.Open(keys(t, 5).OAuthCookie, sealed, now); err != nil || string(got) != "payload" {
		t.Fatalf("got %q, %v", got, err)
	}
}
