// Package config reads the server's settings from the environment.
package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dikym/terka-api/server/internal/auth"
)

// Config is what serve needs. The schema owner's URL is read only by the migrate command.
type Config struct {
	Env                string
	Addr               string
	DatabaseURL        string
	AuthDatabaseURL    string
	PublicURL          string
	AuthKey            []byte
	GoogleClientID     string
	GoogleClientSecret string
	TurnstileSecret    string
	TurnstileHostnames []string
	SMTPURL            string
	MailFrom           string
	TrustCloudflare    bool
}

// Local development runs without real accounts: Cloudflare's always-pass Turnstile test secret,
// Mailpit for email, a fixed auth key, and Google off unless its client is set.
var localDefaults = map[string]string{
	"TERKA_PUBLIC_URL":          "http://localhost:5173",
	"TERKA_AUTH_KEY":            base64.StdEncoding.EncodeToString(make([]byte, 32)),
	"TERKA_TURNSTILE_SECRET":    "1x0000000000000000000000000000000AA",
	"TERKA_TURNSTILE_HOSTNAMES": "localhost",
	"TERKA_SMTP_URL":            "smtp://localhost:1025",
	"TERKA_MAIL_FROM":           "Terka <no-reply@localhost>",
}

// Outside local, a missing value is a deployment mistake, and the server refuses to start rather
// than run sign-in half configured.
var required = []string{
	"TERKA_DATABASE_URL", "TERKA_AUTH_DATABASE_URL", "TERKA_PUBLIC_URL", "TERKA_AUTH_KEY",
	"TERKA_GOOGLE_CLIENT_ID", "TERKA_GOOGLE_CLIENT_SECRET", "TERKA_TURNSTILE_SECRET",
	"TERKA_TURNSTILE_HOSTNAMES", "TERKA_SMTP_URL", "TERKA_MAIL_FROM", "TERKA_TRUST_CLOUDFLARE",
}

func Load(getenv func(string) string) (Config, error) {
	envName := getenv("TERKA_ENV")
	switch envName {
	case "local", "staging", "production":
	default:
		return Config{}, fmt.Errorf("TERKA_ENV must be local, staging or production, got %q", envName)
	}
	get := getenv
	if envName == "local" {
		get = func(key string) string {
			if v := getenv(key); v != "" {
				return v
			}
			return localDefaults[key]
		}
	} else {
		var missing []string
		for _, key := range required {
			if strings.TrimSpace(getenv(key)) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("%s must be set outside the local environment", strings.Join(missing, ", "))
		}
	}

	cfg := Config{
		Env:                envName,
		Addr:               get("TERKA_ADDR"),
		DatabaseURL:        get("TERKA_DATABASE_URL"),
		AuthDatabaseURL:    get("TERKA_AUTH_DATABASE_URL"),
		PublicURL:          strings.TrimRight(get("TERKA_PUBLIC_URL"), "/"),
		GoogleClientID:     get("TERKA_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: get("TERKA_GOOGLE_CLIENT_SECRET"),
		TurnstileSecret:    get("TERKA_TURNSTILE_SECRET"),
		SMTPURL:            get("TERKA_SMTP_URL"),
		MailFrom:           get("TERKA_MAIL_FROM"),
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	for _, host := range strings.Split(get("TERKA_TURNSTILE_HOSTNAMES"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			cfg.TurnstileHostnames = append(cfg.TurnstileHostnames, host)
		}
	}

	key, err := base64.StdEncoding.DecodeString(get("TERKA_AUTH_KEY"))
	if err != nil || len(key) < 32 {
		return Config{}, errors.New("TERKA_AUTH_KEY must be at least 32 random bytes in standard base64 (openssl rand -base64 32)")
	}
	if envName != "local" && bytes.Equal(key, make([]byte, len(key))) {
		return Config{}, errors.New("TERKA_AUTH_KEY is the all-zero local key; generate one with openssl rand -base64 32")
	}
	cfg.AuthKey = key

	switch trust := get("TERKA_TRUST_CLOUDFLARE"); trust {
	case "", "false":
	case "true":
		cfg.TrustCloudflare = true
	default:
		return Config{}, fmt.Errorf("TERKA_TRUST_CLOUDFLARE must be true or false, got %q", trust)
	}

	smtp, err := url.Parse(cfg.SMTPURL)
	// The parse error quotes the whole URL, password included, and this message ends up in logs.
	if err != nil {
		return Config{}, errors.New("TERKA_SMTP_URL is not a valid URL")
	}
	if envName != "local" && auth.IsTurnstileTestSecret(cfg.TurnstileSecret) {
		return Config{}, errors.New("TERKA_TURNSTILE_SECRET is a Cloudflare test secret, which passes anyone")
	}
	if envName != "local" && smtp.Scheme != "smtp+starttls" {
		return Config{}, errors.New("TERKA_SMTP_URL must use smtp+starttls:// outside the local environment")
	}
	return cfg, nil
}
