# Makefile for loomcli project

.PHONY: all build test test-integration test-all lint lint-frontend test-frontend test-e2e test-e2e-api test-e2e-api-local test-e2e-integration clean install help frontend sync-beads update-beads gate gate-e2e hooks dev dev-check dev-loom dev-vite

# Default target
all: build

# Build the loom binary
build: frontend
	@echo "Building loom..."
	go build -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=$$(git rev-parse --short HEAD)" -o loom ./cmd/loom

# Run all tests with coverage
test:
	@echo "Running tests..."
	@TEST_COVER=1 ./scripts/test.sh

# Run integration tests (includes unit tests + integration tests)
test-integration:
	@echo "Running integration tests..."
	@TEST_TAGS=integration TEST_COVER=1 ./scripts/test.sh

# Run all tests (unit + integration + e2e)
test-all:
	@echo "Running all tests..."
	@TEST_TAGS=integration,e2e TEST_COVER=1 ./scripts/test.sh

# Run Go linter
lint:
	@echo "Running Go linter..."
	golangci-lint run --timeout=5m

# Run frontend linter + typecheck
lint-frontend:
	@echo "Running frontend typecheck..."
	@cd $(FRONTEND_DIR) && npm run typecheck
	@echo "Running frontend ESLint..."
	@cd $(FRONTEND_DIR) && npm run lint

# Run frontend unit tests (vitest)
test-frontend:
	@echo "Running frontend unit tests..."
	@cd $(FRONTEND_DIR) && npx vitest run

# Run Playwright e2e tests — mocked chromium tests (no server needed)
test-e2e:
	@echo "Running Playwright e2e tests (mocked)..."
	@cd $(FRONTEND_DIR) && npx playwright install --with-deps chromium 2>/dev/null || true
	@cd $(FRONTEND_DIR) && npx playwright test --project=chromium

# Run Playwright API e2e tests (self-contained: builds loom, starts server, runs tests)
test-e2e-api:
	@echo "Running Playwright API e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=api

# Run Playwright API e2e tests against already-running loom serve
test-e2e-api-local:
	@echo "Running Playwright API e2e tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=api

# Run Playwright integration e2e tests against local loom serve
test-e2e-integration:
	@echo "Running Playwright integration e2e tests..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration

# Install loom to GOPATH/bin
install: frontend
	@echo "Installing loom to $$(go env GOPATH)/bin..."
	@bash -c 'build=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
		go install -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=$$build" ./cmd/loom'

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f loom
	rm -f /tmp/loom.coverage.out

# Frontend directory
FRONTEND_DIR := internal/webui/frontend

# Beads subtree config
BEADS_REMOTE := https://github.com/tysonthomas9/beads
BEADS_BRANCH := feature/web-ui
BEADS_PREFIX := third_party/beads

# Build frontend (requires Node.js >= 20)
# Required: dist/ must exist for go:embed at compile time
frontend:
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build

# Sync beads library packages (rewrite imports from vendored copy)
sync-beads:
	@echo "Syncing beads library packages..."
	./scripts/sync-beads.sh

# Pull latest beads from upstream and sync
update-beads:
	@echo "Pulling latest beads from $(BEADS_REMOTE) ($(BEADS_BRANCH))..."
	git subtree pull --prefix=$(BEADS_PREFIX) $(BEADS_REMOTE) $(BEADS_BRANCH) --squash
	$(MAKE) sync-beads

# Quality gate — full lint + build + vet + test (used by pre-push hook)
gate: frontend
	@echo "=== Quality Gate ==="
	@$(MAKE) lint
	@$(MAKE) lint-frontend
	@go build ./...
	@go vet ./...
	@go test -race -timeout 15m ./...
	@$(MAKE) test-frontend
	@echo "=== Quality Gate PASSED ==="

# Extended quality gate — gate + self-contained e2e tests
gate-e2e: gate
	@echo "=== E2E Gate ==="
	@$(MAKE) test-e2e-api
	@echo "=== E2E Gate PASSED ==="

# Install git hooks (pre-push quality gate + pre-commit checks, applies to all worktrees)
hooks:
	@cp scripts/hooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Pre-push hook installed (applies to all worktrees)"
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		echo "Pre-commit hooks installed"; \
	else \
		echo "Tip: install pre-commit for additional checks: pip install pre-commit"; \
	fi

# Check dev dependencies
dev-check:
	@command -v air >/dev/null 2>&1 || { echo "Error: air not found. Install: go install github.com/air-verse/air@latest"; exit 1; }
	@command -v node >/dev/null 2>&1 || { echo "Error: node not found. Install Node.js >= 20"; exit 1; }
	@echo "All dev dependencies found."

# Run default dev environment (loom serve --dev + frontend dist watcher)
dev: dev-check
	@./scripts/run-web-ui-with-loom.sh

# Run loom serve --dev with auto frontend dist rebuild + Go hot-restart
dev-loom: dev-check
	@./scripts/run-web-ui-with-loom.sh

# Run Vite HMR workflow (frontend at :3000)
dev-vite: dev-check
	@./scripts/dev.sh

# Show help
help:
	@echo "Loomcli Makefile targets:"
	@echo "  make build   - Build the loom binary (builds frontend first)"
	@echo "  make test              - Run unit tests with coverage"
	@echo "  make test-integration  - Run unit + integration tests"
	@echo "  make test-all          - Run all tests (unit + integration + e2e)"
	@echo "  make lint              - Run Go linter (golangci-lint)"
	@echo "  make lint-frontend     - Run frontend typecheck + ESLint"
	@echo "  make test-frontend     - Run frontend unit tests (vitest)"
	@echo "  make test-e2e          - Run Playwright mocked e2e tests (no server)"
	@echo "  make test-e2e-api      - Run Playwright API e2e tests (self-contained)"
	@echo "  make test-e2e-api-local - Run Playwright API e2e tests (needs loom serve)"
	@echo "  make test-e2e-integration - Run Playwright integration e2e tests (needs loom serve)"
	@echo "  make install - Install loom to GOPATH/bin"
	@echo "  make frontend  - Build frontend (requires Node.js >= 20)"
	@echo "  make sync-beads   - Sync beads packages (rewrite imports)"
	@echo "  make update-beads - Pull latest beads + sync"
	@echo "  make gate         - Quality gate (lint + build + vet + test)"
	@echo "  make gate-e2e     - Quality gate + self-contained e2e tests"
	@echo "  make hooks        - Install git hooks (pre-push gate)"
	@echo "  make dev          - Start default dev flow (same as make dev-loom)"
	@echo "  make dev-loom     - Start loom serve --dev + frontend dist watcher"
	@echo "  make dev-vite     - Start air + Vite hot-reload workflow"
	@echo "  make dev-check    - Check dev dependencies (air, node)"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make help         - Show this help message"
