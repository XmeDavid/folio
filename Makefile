.PHONY: help dev db-up db-down migrate sqlc openapi build test lint fmt clean

help:
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ─── Dev ──────────────────────────────────────────────────────
dev: db-up ## Start db + hint to run backend & web
	@echo ""
	@echo "Run in two separate terminals:"
	@echo "  cd backend && go run ./cmd/server"
	@echo "  cd web && pnpm dev"

db-up: ## Start Postgres (dev)
	docker compose -f docker-compose.dev.yml up -d

db-down: ## Stop Postgres (dev)
	docker compose -f docker-compose.dev.yml down

db-reset: ## Wipe and recreate Postgres volume
	docker compose -f docker-compose.dev.yml down -v
	docker compose -f docker-compose.dev.yml up -d

# ─── Codegen ──────────────────────────────────────────────────
migrate: ## Apply database migrations (local)
	cd backend && atlas migrate apply --env local

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
