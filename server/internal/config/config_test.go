package config_test

import (
	"bytes"
	"encoding/base64"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/dikym/terka-api/server/internal/config"
)

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

var authKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

func staging() map[string]string {
	return map[string]string{
		"TERKA_ENV":                  "staging",
		"TERKA_ADDR":                 "127.0.0.1:9000",
		"TERKA_DATABASE_URL":         "postgres://app@db/terka",
		"TERKA_AUTH_DATABASE_URL":    "postgres://auth@db/terka",
		"TERKA_PUBLIC_URL":           "https://staging.terka.my.id",
		"TERKA_AUTH_KEY":             authKey,
		"TERKA_GOOGLE_CLIENT_ID":     "client-id",
		"TERKA_GOOGLE_CLIENT_SECRET": "client-secret",
		"TERKA_TURNSTILE_SECRET":     "turnstile-secret",
		"TERKA_TURNSTILE_HOSTNAMES":  "staging.terka.my.id, terka.my.id",
		"TERKA_SMTP_URL":             "smtp+starttls://user:pass@smtp.resend.com:587",
		"TERKA_MAIL_FROM":            "Terka <no-reply@terka.my.id>",
		"TERKA_TRUST_CLOUDFLARE":     "true",
	}
}

func TestLoadRejectsUnknownEnv(t *testing.T) {
	for _, value := range []string{"", "prod"} {
		_, err := config.Load(env(map[string]string{"TERKA_ENV": value}))
		if err == nil || !strings.Contains(err.Error(), "TERKA_ENV") {
			t.Fatalf("TERKA_ENV=%q: err = %v, want one naming TERKA_ENV", value, err)
		}
	}
}

func TestLoadDefaultsAddr(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"TERKA_ENV": "local"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	cfg, err := config.Load(env(staging()))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Env:                "staging",
		Addr:               "127.0.0.1:9000",
		DatabaseURL:        "postgres://app@db/terka",
		AuthDatabaseURL:    "postgres://auth@db/terka",
		PublicURL:          "https://staging.terka.my.id",
		AuthKey:            bytes.Repeat([]byte{7}, 32),
		GoogleClientID:     "client-id",
		GoogleClientSecret: "client-secret",
		TurnstileSecret:    "turnstile-secret",
		TurnstileHostnames: []string{"staging.terka.my.id", "terka.my.id"},
		SMTPURL:            "smtp+starttls://user:pass@smtp.resend.com:587",
		MailFrom:           "Terka <no-reply@terka.my.id>",
		TrustCloudflare:    true,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("cfg = %+v\nwant %+v", cfg, want)
	}
}

func TestStagingRefusesMissingAuthSettings(t *testing.T) {
	for _, name := range []string{
		"TERKA_DATABASE_URL", "TERKA_AUTH_DATABASE_URL", "TERKA_PUBLIC_URL", "TERKA_AUTH_KEY",
		"TERKA_GOOGLE_CLIENT_ID", "TERKA_GOOGLE_CLIENT_SECRET", "TERKA_TURNSTILE_SECRET",
		"TERKA_TURNSTILE_HOSTNAMES", "TERKA_SMTP_URL", "TERKA_MAIL_FROM", "TERKA_TRUST_CLOUDFLARE",
	} {
		for _, envName := range []string{"staging", "production"} {
			vars := maps.Clone(staging())
			vars["TERKA_ENV"] = envName
			vars[name] = ""
			if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s without %s: err = %v, want one naming it", envName, name, err)
			}
		}
	}
}

func TestStagingRefusesShortAuthKey(t *testing.T) {
	vars := staging()
	vars["TERKA_AUTH_KEY"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 31))
	if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_AUTH_KEY") {
		t.Fatalf("31-byte key: err = %v, want one naming TERKA_AUTH_KEY", err)
	}
	vars["TERKA_AUTH_KEY"] = "not base64!"
	if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_AUTH_KEY") {
		t.Fatalf("garbage key: err = %v, want one naming TERKA_AUTH_KEY", err)
	}
}

func TestStagingRefusesPlainSMTP(t *testing.T) {
	vars := staging()
	vars["TERKA_SMTP_URL"] = "smtp://user:pass@smtp.resend.com:587"
	if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_SMTP_URL") {
		t.Fatalf("plain SMTP in staging: err = %v, want one naming TERKA_SMTP_URL", err)
	}
}

func TestRefusesAnUnclearCloudflareSetting(t *testing.T) {
	vars := staging()
	vars["TERKA_TRUST_CLOUDFLARE"] = "yes"
	if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_TRUST_CLOUDFLARE") {
		t.Fatalf("err = %v, want one naming TERKA_TRUST_CLOUDFLARE", err)
	}
}

func TestLocalHasSafeTestDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"TERKA_ENV": "local"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TurnstileSecret != "1x0000000000000000000000000000000AA" || !reflect.DeepEqual(cfg.TurnstileHostnames, []string{"localhost"}) {
		t.Errorf("turnstile = %q %v, want Cloudflare's always-pass test secret for localhost", cfg.TurnstileSecret, cfg.TurnstileHostnames)
	}
	if cfg.SMTPURL != "smtp://localhost:1025" || cfg.PublicURL != "http://localhost:5173" {
		t.Errorf("smtp = %q, public = %q", cfg.SMTPURL, cfg.PublicURL)
	}
	if len(cfg.AuthKey) != 32 || cfg.TrustCloudflare || cfg.GoogleClientID != "" {
		t.Errorf("auth key %d bytes, trust %v, google %q", len(cfg.AuthKey), cfg.TrustCloudflare, cfg.GoogleClientID)
	}
}

func TestBadSMTPURLErrorHidesTheCredentials(t *testing.T) {
	vars := staging()
	vars["TERKA_SMTP_URL"] = "smtp+starttls://user:hunter2secret@smtp.resend.com:bad"
	_, err := config.Load(env(vars))
	if err == nil || !strings.Contains(err.Error(), "TERKA_SMTP_URL") || strings.Contains(err.Error(), "hunter2secret") {
		t.Fatalf("err = %v, want one naming TERKA_SMTP_URL without the password", err)
	}
}

func TestStagingRefusesTurnstileTestSecrets(t *testing.T) {
	for _, secret := range []string{"1x0000000000000000000000000000000AA", "2x0000000000000000000000000000000AA", "3x0000000000000000000000000000000AA"} {
		vars := staging()
		vars["TERKA_TURNSTILE_SECRET"] = secret
		if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_TURNSTILE_SECRET") {
			t.Errorf("%s: err = %v, want one naming TERKA_TURNSTILE_SECRET", secret, err)
		}
	}
}

func TestStagingRefusesTheLocalZeroAuthKey(t *testing.T) {
	vars := staging()
	vars["TERKA_AUTH_KEY"] = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := config.Load(env(vars)); err == nil || !strings.Contains(err.Error(), "TERKA_AUTH_KEY") {
		t.Fatalf("all-zero key: err = %v, want one naming TERKA_AUTH_KEY", err)
	}
}
