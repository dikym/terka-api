package config_test

import (
	"strings"
	"testing"

	"github.com/dikym/terka-api/server/internal/config"
)

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
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
	cfg, err := config.Load(env(map[string]string{
		"TERKA_ENV":          "staging",
		"TERKA_ADDR":         "127.0.0.1:9000",
		"TERKA_DATABASE_URL": "postgres://app@db/terka",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Env:         "staging",
		Addr:        "127.0.0.1:9000",
		DatabaseURL: "postgres://app@db/terka",
	}
	if cfg != want {
		t.Fatalf("cfg = %+v, want %+v", cfg, want)
	}
}
