.DEFAULT_GOAL := help
VERSION ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
# Run through go so the linter is built with this repo's Go version and pinned here.
GOLANGCI ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
SQLC ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
DEV_COMPOSE := docker compose -f deploy/compose.dev.yaml
LOCAL_COMPOSE := docker compose -f deploy/compose.local.yaml
# Local only; the terka_dev database never leaves this machine.
DEV_OWNER_PASSWORD := localdevonlyownerpw
DEV_APP_PASSWORD := localdevonlyapppw1
DEV_AUTH_PASSWORD := localdevonlyauthpw1
DEV_DB := localhost:5433/terka_dev
export TERKA_TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/postgres

.PHONY: help db dev caddy stack stack-down build image test pgtap lint sqlc

help:
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

db: ## start the local Postgres used by tests (port 5433)
	$(DEV_COMPOSE) up -d --wait postgres

dev: db ## run the api on :8080 with live reload, against the terka_dev database
	COMPOSE_FILE=deploy/compose.dev.yaml COMPOSE_PROJECT_NAME=terka-dev \
		OWNER_PASSWORD=$(DEV_OWNER_PASSWORD) APP_PASSWORD=$(DEV_APP_PASSWORD) AUTH_PASSWORD=$(DEV_AUTH_PASSWORD) \
		bash deploy/scripts/create-env-db.sh dev
	cd server && TERKA_MIGRATE_DATABASE_URL=postgres://terka_dev_owner:$(DEV_OWNER_PASSWORD)@$(DEV_DB) \
		go run ./cmd/terka migrate
	cd server && TERKA_ENV=local TERKA_ADDR=:8080 \
		TERKA_DATABASE_URL=postgres://terka_dev_app:$(DEV_APP_PASSWORD)@$(DEV_DB) \
		TERKA_AUTH_DATABASE_URL=postgres://terka_dev_auth:$(DEV_AUTH_PASSWORD)@$(DEV_DB) go tool air

caddy: ## build the shared Caddy base image under its published tag
	docker build -t ghcr.io/dikym/terka-caddy:$$(cat deploy/caddy/VERSION) deploy/caddy

stack: caddy ## build and run api, web, postgres and caddy together on http://localhost:8800
	$(LOCAL_COMPOSE) up -d --build --wait caddy

stack-down: ## stop the local stack (add ARGS=-v to also drop its database)
	$(LOCAL_COMPOSE) down $(ARGS)

build: ## build the server binary into server/bin/terka
	cd server && CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/terka ./cmd/terka

image: ## build the container image as terka-api:$(VERSION)
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t terka-api:$(VERSION) .

test: ## run the server tests (needs `make db`)
	cd server && go test ./...

pgtap: db ## run the database tests against a fresh schema
	bash deploy/scripts/pgtap.sh

lint: ## format check, vet and lint the server
	cd server && test -z "$$(gofmt -l .)" && go vet ./... && $(GOLANGCI) run

sqlc: ## regenerate server/internal/store from server/queries
	cd server && $(SQLC) generate
