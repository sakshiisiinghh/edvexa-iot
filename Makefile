BINARY := bin/api

# Serialises concurrent `make migrate` runs. Nothing else in this project takes
# an advisory lock; if anything ever does, the keys must not collide.
MIGRATION_LOCK := 4242424242

TEST_DB := edvexa_test

ENVLOAD := set -a; if [ -f .env ]; then . ./.env; fi; set +a
# PSQL takes the database as its next argument, so the same plumbing serves the
# development and test databases.
PSQL = docker compose exec -T postgres psql -v ON_ERROR_STOP=1 \
         -U $${POSTGRES_USER:-edvexa} -d
DEV_DB = $${POSTGRES_DB:-edvexa}
BOOTSTRAP_MIGRATIONS := SET client_min_messages = warning; \
    CREATE TABLE IF NOT EXISTS schema_migrations ( \
    version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())
# DO takes a plain string literal, which avoids dollar-quoting inside a
# Makefile recipe, and returns no rows unlike SELECT pg_advisory_xact_lock().
MIGRATION_LOCK_SQL := DO 'BEGIN PERFORM pg_advisory_xact_lock($(MIGRATION_LOCK)); END';

# apply_migrations <database>
define apply_migrations
$(PSQL) $(1) -q -c "$(BOOTSTRAP_MIGRATIONS)"; \
for f in migrations/*.sql; do \
  v=$$(basename $$f .sql); \
  if [ -n "$$($(PSQL) $(1) -tAc "SELECT 1 FROM schema_migrations WHERE version='$$v'")" ]; then \
    echo "skip   $$v"; continue; \
  fi; \
  echo "apply  $$v"; \
  { echo "$(MIGRATION_LOCK_SQL)"; \
    cat $$f; \
    echo "INSERT INTO schema_migrations (version) VALUES ('$$v') ON CONFLICT DO NOTHING;"; \
  } | $(PSQL) $(1) -q --single-transaction -f - || exit 1; \
done
endef

.DEFAULT_GOAL := help
.PHONY: help tidy build run test test-integration vet migrate migrate-status db-up db-down db-logs clean

help: ## List targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-14s %s\n", $$1, $$2}'

tidy: ## Reconcile go.mod and go.sum
	go mod tidy

build: ## Compile the API to bin/api
	go build -o $(BINARY) ./cmd/api

run: ## Run the API, loading .env if present
	@$(ENVLOAD); go run ./cmd/api

test: ## Run all tests
	go test ./...

vet: ## Report suspicious constructs
	go vet ./...

migrate: ## Apply pending migrations
	@$(ENVLOAD); $(call apply_migrations,$(DEV_DB))

migrate-status: ## Show applied and pending migrations
	@$(ENVLOAD); \
	$(PSQL) $(DEV_DB) -q -c "$(BOOTSTRAP_MIGRATIONS)"; \
	$(PSQL) $(DEV_DB) -c "SELECT version, applied_at FROM schema_migrations ORDER BY version"; \
	for f in migrations/*.sql; do \
	  v=$$(basename $$f .sql); \
	  if [ -z "$$($(PSQL) $(DEV_DB) -tAc "SELECT 1 FROM schema_migrations WHERE version='$$v'")" ]; then \
	    echo "pending $$v"; \
	  fi; \
	done

test-integration: ## Run integration tests against a real PostgreSQL
	@$(ENVLOAD); \
	echo "preparing $(TEST_DB)"; \
	$(PSQL) postgres -q -c "CREATE DATABASE $(TEST_DB)" >/dev/null 2>&1 || true; \
	$(call apply_migrations,$(TEST_DB)); \
	go test -tags=integration -race -count=1 -p 1 ./...

db-up: ## Start PostgreSQL and block until it is healthy
	docker compose up -d --wait postgres

db-down: ## Stop and remove the container, keeping the data volume
	docker compose down

db-logs: ## Follow PostgreSQL logs
	docker compose logs -f postgres

clean: ## Remove build output
	rm -rf bin
