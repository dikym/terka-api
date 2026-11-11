.DEFAULT_GOAL := help
VERSION ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
# Run through go so the linter is built with this repo's Go version and pinned here.
GOLANGCI ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
DIST := server/internal/web/dist

.PHONY: help web build test lint

help:
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

web: ## build the client and copy it to where the server embeds it
	cd web && bun install --frozen-lockfile && bun run check && bun run build
	find $(DIST) -mindepth 1 ! -name .gitkeep -delete
	cp -R web/build/. $(DIST)/

build: ## build the server binary into server/bin/terka (run `make web` first to embed the client)
	cd server && CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/terka ./cmd/terka

test: ## run the server tests
	cd server && go test ./...

lint: ## format check, vet and lint the server
	cd server && test -z "$$(gofmt -l .)" && go vet ./... && $(GOLANGCI) run
