.PHONY: help dev dev-up dev-down dev-logs db-up db-down migrate sqlc openapi build admin-cli test lint fmt clean

help:
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ─── Dev ──────────────────────────────────────────────────────
dev: dev-up ## Start the full live-reload dev stack

dev-up: ## Start db + backend + web with Docker Compose
	docker compose -f docker-compose.dev.yml up --build

dev-down: ## Stop the full dev stack
	docker compose -f docker-compose.dev.yml down

dev-logs: ## Tail full dev stack logs
	docker compose -f docker-compose.dev.yml logs -f

db-up: ## Start Postgres (dev)
	docker compose -f docker-compose.dev.yml up -d db

db-down: ## Stop Postgres (dev)
	docker compose -f docker-compose.dev.yml stop db

db-reset: ## Wipe and recreate Postgres volume
	docker compose -f docker-compose.dev.yml down -v
	docker compose -f docker-compose.dev.yml up -d db

# ─── Codegen ──────────────────────────────────────────────────
migrate: ## Apply app + River database migrations (local)
	cd backend && DATABASE_URL=$${DATABASE_URL:-postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable} atlas migrate apply --env local
	cd backend && DATABASE_URL=$${DATABASE_URL:-postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable} go run ./cmd/folio-river-migrate -direction up

river-migrate: ## Apply River queue migrations only (local)
	cd backend && DATABASE_URL=$${DATABASE_URL:-postgres://folio:folio_dev_password@localhost:5432/folio?sslmode=disable} go run ./cmd/folio-river-migrate -direction up

migrate-new: ## Create a new migration: make migrate-new NAME=add_accounts
	cd backend && atlas migrate new $(NAME)

sqlc: ## Generate typed Go queries
	cd backend && sqlc generate

openapi: ## Regenerate Go server + TS client from OpenAPI spec
	cd backend && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest \
		-config ../openapi/oapi-codegen.yaml ../openapi/openapi.yaml
	cd web && pnpm openapi:gen

gen: sqlc openapi ## All codegen

# ─── Build ────────────────────────────────────────────────────
build: ## Build backend binary and web production bundle
	cd backend && go build -o bin/server ./cmd/server
	cd web && pnpm build

admin-cli: ## Build folio-admin CLI binary
	cd backend && go build -o bin/folio-admin ./cmd/folio-admin
	@echo "built: backend/bin/folio-admin"

# ─── Quality ──────────────────────────────────────────────────
test: ## Run all tests
	cd backend && go test ./...
	cd web && pnpm test

lint: ## Run linters
	cd backend && golangci-lint run
	cd web && pnpm lint

fmt: ## Format code
	cd backend && gofmt -w . && goimports -w .
	cd web && pnpm format

# ─── Cleanup ──────────────────────────────────────────────────
clean: ## Remove build artefacts
	rm -rf backend/bin backend/tmp
	rm -rf web/.next web/out
