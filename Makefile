BINARY := bin/api

.DEFAULT_GOAL := help
.PHONY: help tidy build run test vet db-up db-down db-logs clean

help: ## List targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-9s %s\n", $$1, $$2}'

tidy: ## Reconcile go.mod and go.sum
	go mod tidy

build: ## Compile the API to bin/api
	go build -o $(BINARY) ./cmd/api

run: ## Run the API, loading .env if present
	@set -a; if [ -f .env ]; then . ./.env; fi; set +a; go run ./cmd/api

test: ## Run all tests
	go test ./...

vet: ## Report suspicious constructs
	go vet ./...

db-up: ## Start PostgreSQL and block until it is healthy
	docker compose up -d --wait postgres

db-down: ## Stop and remove the container, keeping the data volume
	docker compose down

db-logs: ## Follow PostgreSQL logs
	docker compose logs -f postgres

clean: ## Remove build output
	rm -rf bin
