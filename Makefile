.DEFAULT_GOAL := help
VERSION ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
# Run through go so the linter is built with this repo's Go version and pinned here.
GOLANGCI ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
DIST := server/internal/web/dist
DEV_COMPOSE := docker compose -f deploy/compose.dev.yaml
export TERKA_TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/postgres

.PHONY: help db web build image test pgtap lint

help:
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

db: ## start the local Postgres used by tests (port 5433)
	$(DEV_COMPOSE) up -d --wait postgres

web: ## build the client and copy it to where the server embeds it
	cd web && bun install --frozen-lockfile && bun run check && bun run build
	find $(DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R web/build/. $(DIST)/

build: ## build the server binary into server/bin/terka (run `make web` first to embed the client)
	cd server && CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/terka ./cmd/terka

image: ## build the container image as terka-go:$(VERSION)
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t terka-go:$(VERSION) .

test: ## run the server tests (needs `make db`)
	cd server && go test ./...

pgtap: db ## run the database tests against a fresh schema
	bash deploy/scripts/pgtap.sh

lint: ## format check, vet and lint the server
	cd server && test -z "$$(gofmt -l .)" && go vet ./... && $(GOLANGCI) run
