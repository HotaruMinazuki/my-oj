# ── Configuration ─────────────────────────────────────────────────────────────
COMPOSE   := docker compose
PY        := python3
PG_USER   := oj
PG_DB     := oj

# Colour helpers (no-op on terminals that don't support ANSI)
CYAN  := \033[96m
RESET := \033[0m

.DEFAULT_GOAL := help

.PHONY: help up down restart logs build \
        e2e db-init db-reset db-shell \
        lint vet fmt

# ── Help ──────────────────────────────────────────────────────────────────────
help:
	@echo ""
	@echo "  $(CYAN)OJ Platform — available targets$(RESET)"
	@echo ""
	@echo "  Environment"
	@echo "    make up          Start all services in detached mode"
	@echo "    make down        Stop and remove containers (volumes preserved)"
	@echo "    make restart     Restart api-server and judger-node only"
	@echo "    make logs        Tail api-server + judger-node logs"
	@echo "    make build       Rebuild api-server and judger-node images (no cache)"
	@echo ""
	@echo "  Database"
	@echo "    make db-init     Apply migrations/001_init.sql to running postgres"
	@echo "    make db-reset    Drop + recreate the oj database, then re-apply schema"
	@echo "    make db-shell    Open an interactive psql session"
	@echo ""
	@echo "  Testing"
	@echo "    make e2e         Run the full end-to-end simulation script"
	@echo ""
	@echo "  Code quality"
	@echo "    make vet         go vet ./..."
	@echo "    make fmt         gofmt -w ./..."
	@echo ""

# ── Environment ───────────────────────────────────────────────────────────────
up:
	@echo "$(CYAN)→ Starting all services…$(RESET)"
	$(COMPOSE) up -d
	@echo "Services started. Open http://localhost to reach the frontend."
	@echo "MinIO console: http://localhost:9001  (minioadmin / minioadmin)"

down:
	@echo "$(CYAN)→ Stopping services…$(RESET)"
	$(COMPOSE) down

restart:
	@echo "$(CYAN)→ Restarting api-server and judger-node…$(RESET)"
	$(COMPOSE) restart api-server judger-node

logs:
	$(COMPOSE) logs -f api-server judger-node

build:
	@echo "$(CYAN)→ Rebuilding images (no cache)…$(RESET)"
	$(COMPOSE) build --no-cache api-server judger-node

# ── Database ──────────────────────────────────────────────────────────────────
# db-init: idempotent — safe to run against an existing database.
# The same file is auto-applied on first postgres container start because it is
# mounted to /docker-entrypoint-initdb.d inside the postgres container.
db-init:
	@echo "$(CYAN)→ Applying migrations/001_init.sql to postgres…$(RESET)"
	$(COMPOSE) exec -T postgres \
	    psql -U $(PG_USER) -d $(PG_DB) \
	    < migrations/001_init.sql
	@echo "Schema applied."

# db-reset: DESTRUCTIVE — drops and recreates the oj database.
# Useful during local development when the schema changes incompatibly.
db-reset:
	@echo "$(CYAN)→ Dropping and recreating database '$(PG_DB)'…$(RESET)"
	$(COMPOSE) exec -T postgres \
	    psql -U $(PG_USER) -d postgres \
	    -c "DROP DATABASE IF EXISTS $(PG_DB);"
	$(COMPOSE) exec -T postgres \
	    psql -U $(PG_USER) -d postgres \
	    -c "CREATE DATABASE $(PG_DB) OWNER $(PG_USER);"
	$(MAKE) db-init

db-shell:
	$(COMPOSE) exec postgres psql -U $(PG_USER) -d $(PG_DB)

# ── Testing ───────────────────────────────────────────────────────────────────
# Runs the full pipeline:
#   1. Uploads testcase zip via Admin API
#   2. Injects AC / WA / TLE submissions into Redis Streams (bypasses DB)
#   3. Waits for judger results on oj:judge:results
#   4. Reads final ranking snapshot from Redis
#   Open public/debug_board.html in a browser to watch the live scoreboard.
e2e:
	@echo "$(CYAN)→ Running E2E simulation…$(RESET)"
	$(PY) scripts/e2e_simulate.py

# ── Code quality ──────────────────────────────────────────────────────────────
vet:
	go vet ./...

fmt:
	gofmt -w ./...
