# Makefile for loomcli project

.PHONY: all build test test-integration test-all lint lint-frontend test-frontend test-e2e test-e2e-api test-e2e-api-local test-e2e-integration clean install install-bd help frontend frontend-ensure sync-beads update-beads check check-go check-frontend gate gate-e2e gate-e2e-full hooks dev dev-check dev-loom dev-vite check-loc check-loc-stale check-no-raw-exec test-coverage test-frontend-coverage test-race-cover test-integration-race-cover

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

# Run tests with race detector and coverage
test-race-cover:
	@echo "Running tests with race detector and coverage..."
	@TEST_COVER=1 TEST_RACE=1 TEST_TIMEOUT=15m ./scripts/test.sh
	@./scripts/check-coverage.sh

# Run integration tests with race detector and coverage
test-integration-race-cover:
	@echo "Running integration tests with race detector and coverage..."
	@TEST_COVER=1 TEST_RACE=1 TEST_TAGS=integration TEST_TIMEOUT=15m ./scripts/test.sh
	@./scripts/check-coverage.sh

# Run Go linter
lint:
	@echo "Running Go linter..."
	golangci-lint run --timeout=5m --allow-parallel-runners

# Run Go tests with coverage threshold enforcement
test-coverage: test
	@./scripts/check-coverage.sh

# Run frontend tests with coverage threshold enforcement
test-frontend-coverage:
	@echo "Running frontend tests with coverage..."
	@cd $(FRONTEND_DIR) && npm run test:coverage

# Check Go file LOC limits
check-loc:
	@./scripts/check-loc.sh 500

# Check for stale LOC allowlist entries
check-loc-stale:
	@./scripts/check-loc-stale.sh --check-stale 500

# Check for raw exec.Command in unit tests (enforces DI)
check-no-raw-exec:
	@echo "Checking for raw exec.Command in unit tests..."
	@./scripts/check-no-raw-exec.sh

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

# Run auth service unit + security tests
test-auth-service:
	@echo "Running auth service tests..."
	@cd services/auth && node --experimental-test-module-mocks --import tsx --test 'src/**/*.test.ts'

# Run full-stack auth E2E smoke test (requires docker)
test-auth-e2e:
	@echo "Running full-stack auth E2E smoke test..."
	@bash e2e/auth/run_test.sh

# Install loom to GOPATH/bin
install: frontend
	@echo "Installing loom to $$(go env GOPATH)/bin..."
	@bash -c 'build=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
		go install -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=$$build" ./cmd/loom'
	@$(MAKE) install-bd

# Install bd (beads CLI) from vendored source
install-bd:
	@echo "Installing bd (beads CLI) from vendored source..."
	cd third_party/beads && go install ./cmd/bd/

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

# Ensure frontend dist exists for go:embed (skip rebuild if already present)
frontend-ensure:
	@if [ ! -f $(FRONTEND_DIR)/dist/index.html ]; then echo "frontend/dist/index.html missing — building..."; $(MAKE) frontend; fi

# Sync beads library packages (rewrite imports from vendored copy)
sync-beads:
	@echo "Syncing beads library packages..."
	./scripts/sync-beads.sh

# Pull latest beads from upstream and sync
update-beads:
	@echo "Pulling latest beads from $(BEADS_REMOTE) ($(BEADS_BRANCH))..."
	git subtree pull --prefix=$(BEADS_PREFIX) $(BEADS_REMOTE) $(BEADS_BRANCH) --squash
	$(MAKE) sync-beads

# Go-only quality gate (skips frontend rebuild if dist/ exists)
check-go: frontend-ensure
	@echo "=== [1/9] Go: format check ==="
	@bad=$$(gofmt -l . 2>/dev/null | grep -v third_party | grep -v worktrees | grep -v vendor | grep -v node_modules | head -20); \
	if [ -n "$$bad" ]; then echo "gofmt violations:"; echo "$$bad"; exit 1; fi
	@echo "=== [2/9] Go: vet ==="
	@go vet ./...
	@echo "=== [3/9] Go: build ==="
	@go build -buildvcs=false ./...
	@echo "=== [4/9] Go: lint (golangci-lint + depguard) ==="
	@golangci-lint run --timeout=5m --allow-parallel-runners
	@echo "=== [5/9] Go: LOC check ==="
	@./scripts/check-loc.sh 500
	@echo "=== [6/9] Go: exec.Command guard ==="
	@./scripts/check-no-raw-exec.sh
	@echo "=== [7/9] Go: log.Printf guard ==="
	@./scripts/check-no-log-printf.sh
	@echo "=== [8/9] Go: test with race detector ==="
	@go test -race -covermode=atomic -coverprofile=/tmp/loom.coverage.out -timeout 15m ./...
	@echo "=== [9/9] Go: coverage threshold ==="
	@./scripts/check-coverage.sh
	@echo "=== Go quality gates PASSED ==="

# Frontend-only quality gate (builds frontend first)
check-frontend: frontend-ensure
	@echo "=== [1/5] Frontend: format check ==="
	@cd $(FRONTEND_DIR) && npm run format:check
	@echo "=== [2/5] Frontend: typecheck ==="
	@cd $(FRONTEND_DIR) && npm run typecheck
	@echo "=== [3/5] Frontend: eslint ==="
	@cd $(FRONTEND_DIR) && npm run lint
	@echo "=== [4/5] Frontend: architectural checks ==="
	@cd $(FRONTEND_DIR) && npm run check:arch
	@echo "=== [5/5] Frontend: unit tests + coverage (60% threshold) ==="
	@cd $(FRONTEND_DIR) && npm run test:coverage
	@echo "=== Frontend quality gates PASSED ==="

# Unified quality gate — runs Go + frontend checks in parallel
check: frontend
	@echo "=== Running Go and Frontend checks in parallel ==="
	@go_log=$$(mktemp); fe_log=$$(mktemp); \
	$(MAKE) check-go >"$$go_log" 2>&1 & go_pid=$$!; \
	$(MAKE) check-frontend >"$$fe_log" 2>&1 & fe_pid=$$!; \
	go_rc=0; fe_rc=0; \
	wait $$go_pid || go_rc=$$?; \
	wait $$fe_pid || fe_rc=$$?; \
	if [ $$go_rc -ne 0 ] || [ $$fe_rc -ne 0 ]; then \
		if [ $$go_rc -ne 0 ]; then \
			echo ""; echo "━━━ Go output (FAILED) ━━━"; cat "$$go_log"; \
		fi; \
		if [ $$fe_rc -ne 0 ]; then \
			echo ""; echo "━━━ Frontend output (FAILED) ━━━"; cat "$$fe_log"; \
		fi; \
		rm -f "$$go_log" "$$fe_log"; \
		exit 1; \
	fi; \
	echo "=== Go quality gates PASSED ==="; \
	echo "=== Frontend quality gates PASSED ==="; \
	rm -f "$$go_log" "$$fe_log"
	@echo "=== All quality gates PASSED ==="

# Backward-compatible alias for 'make check'
gate: check

# Extended quality gate — gate + self-contained e2e tests
gate-e2e: gate
	@echo "=== E2E Gate ==="
	@$(MAKE) test-e2e-api
	@echo "=== E2E Gate PASSED ==="

# Full quality gate — gate-e2e + Docker container tests
gate-e2e-full: gate-e2e
	@echo "=== Container E2E Gate ==="
	@go test -tags container -v -timeout 15m ./e2e/
	@echo "=== Container E2E Gate PASSED ==="

# Install git hooks (pre-push quality gate + pre-commit checks, applies to all worktrees)
hooks:
	@cp scripts/hooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Pre-push hook installed (applies to all worktrees)"
	@command -v pre-commit >/dev/null 2>&1 || { echo "Error: pre-commit not found. Install: brew install pre-commit"; exit 1; }
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Error: golangci-lint not found. Install: brew install golangci-lint"; exit 1; }
	@pre-commit install
	@echo "Pre-commit hooks installed"

# Ensure hooks are installed (runs once — skips if pre-push hook already exists)
.git/hooks/pre-push: scripts/hooks/pre-push
	@$(MAKE) hooks

# Check dev dependencies
dev-check:
	@command -v air >/dev/null 2>&1 || { echo "Error: air not found. Install: go install github.com/air-verse/air@latest"; exit 1; }
	@command -v node >/dev/null 2>&1 || { echo "Error: node not found. Install Node.js >= 20"; exit 1; }
	@echo "All dev dependencies found."

# Run default dev environment (loom serve --dev + frontend dist watcher)
dev: dev-check .git/hooks/pre-push
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
	@echo "  make test-race-cover   - Run tests with race detector + coverage"
	@echo "  make test-integration-race-cover - Run integration tests with race + coverage"
	@echo "  make test-coverage     - Run Go tests with coverage threshold"
	@echo "  make test-frontend-coverage - Run frontend tests with coverage threshold"
	@echo "  make check-no-raw-exec - Check for raw exec.Command in unit tests"
	@echo "  make check-loc-stale   - Check for stale LOC allowlist entries"
	@echo "  make lint              - Run Go linter (golangci-lint)"
	@echo "  make lint-frontend     - Run frontend typecheck + ESLint"
	@echo "  make test-frontend     - Run frontend unit tests (vitest)"
	@echo "  make test-e2e          - Run Playwright mocked e2e tests (no server)"
	@echo "  make test-e2e-api      - Run Playwright API e2e tests (self-contained)"
	@echo "  make test-e2e-api-local - Run Playwright API e2e tests (needs loom serve)"
	@echo "  make test-e2e-integration - Run Playwright integration e2e tests (needs loom serve)"
	@echo "  make install    - Install loom + bd to GOPATH/bin"
	@echo "  make install-bd - Install bd (beads CLI) from vendored source"
	@echo "  make frontend  - Build frontend (requires Node.js >= 20)"
	@echo "  make sync-beads   - Sync beads packages (rewrite imports)"
	@echo "  make update-beads - Pull latest beads + sync"
	@echo "  make check        - Unified quality gate (all 14 checks)"
	@echo "  make check-go     - Go-only quality gate (skips frontend rebuild if dist/ exists)"
	@echo "  make check-frontend - Frontend-only quality gate"
	@echo "  make gate         - Alias for make check"
	@echo "  make gate-e2e     - Quality gate + Playwright API e2e tests (no Docker)"
	@echo "  make gate-e2e-full - Quality gate + API e2e + Docker container tests"
	@echo "  make hooks        - Install git hooks (pre-push gate)"
	@echo "  make dev          - Start default dev flow (same as make dev-loom)"
	@echo "  make dev-loom     - Start loom serve --dev + frontend dist watcher"
	@echo "  make dev-vite     - Start air + Vite hot-reload workflow"
	@echo "  make dev-check    - Check dev dependencies (air, node)"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make help         - Show this help message"
