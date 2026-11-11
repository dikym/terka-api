// Package config reads the server's settings from the environment.
package config

import "fmt"

type Config struct {
	Env                string
	Addr               string
	DatabaseURL        string
	MigrateDatabaseURL string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Env:                getenv("TERKA_ENV"),
		Addr:               getenv("TERKA_ADDR"),
		DatabaseURL:        getenv("TERKA_DATABASE_URL"),
		MigrateDatabaseURL: getenv("TERKA_MIGRATE_DATABASE_URL"),
	}
	switch cfg.Env {
	case "local", "staging", "production":
	default:
		return Config{}, fmt.Errorf("TERKA_ENV must be local, staging or production, got %q", cfg.Env)
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	return cfg, nil
}
