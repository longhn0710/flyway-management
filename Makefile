.PHONY: install dev-web dev-api build run check test-e2e test-race security

# Pin the patched toolchain even when the caller sets GOTOOLCHAIN=local.
export GOTOOLCHAIN := go1.26.9

install:
	npm --prefix web ci
	go mod download

dev-web:
	npm --prefix web run dev

# Build assets first with `make build`, then use a second terminal for Vite.
dev-api:
	go run ./cmd/flyway-manager

build:
	npm --prefix web run build
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/flyway-manager ./cmd/flyway-manager

run:
	./bin/flyway-manager

check: build
	go vet ./cmd/... ./internal/... ./web
	go test ./cmd/... ./internal/... ./web
	npm --prefix web run lint
	npm --prefix web test

test-e2e: build
	npm --prefix web run test:e2e

test-race: build
	go test -race ./cmd/... ./internal/... ./web

security:
	npm --prefix web audit --audit-level=moderate
	go run golang.org/x/vuln/cmd/govulncheck@latest ./cmd/... ./internal/... ./web
