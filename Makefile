# Makefile for loomcli project

.PHONY: all build build-frontend build-all test test-integration test-all lint lint-frontend test-frontend test-e2e test-e2e-api test-e2e-api-local test-e2e-integration test-e2e-integration-local test-e2e-integration-full clean install install-bd help frontend sync-beads update-beads check check-go check-frontend gate gate-e2e gate-e2e-full hooks ensure-hooks dev dev-check dev-loom dev-vite check-loc check-loc-stale check-no-raw-exec test-coverage test-frontend-coverage test-race-cover test-integration-race-cover gen-go-api check-go-api-staleness

# Default target
all: build

# Build the loom binary (Go-only; no frontend dependency)
build:
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
	@./scripts/check-loc.sh 1000 2500

# Check for stale LOC allowlist entries
check-loc-stale:
	@./scripts/check-loc-stale.sh --check-stale 1000

# Check for raw exec.Command in unit tests (enforces DI)
check-no-raw-exec:
	@echo "Checking for raw exec.Command in unit tests..."
	@./scripts/check-no-raw-exec.sh

# Generate Go types from api/openapi.yaml using oapi-codegen (via a 3.1->3.0
# preprocessor since oapi-codegen v2.6 does not yet fully support 3.1).
OAPI_CODEGEN_VERSION := v2.6.0
gen-go-api:
	@echo "Generating Go types from api/openapi.yaml..."
	@mkdir -p tmp
	@go run ./scripts/openapi-to-30 api/openapi.yaml > tmp/openapi-3.0.yaml
	@go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config api/oapi-codegen.yaml tmp/openapi-3.0.yaml
	@rm -f tmp/openapi-3.0.yaml
	@echo "Generated: internal/backend/api/gen/types.gen.go"

# Check that committed types.gen.go is in sync with api/openapi.yaml
check-go-api-staleness:
	@./scripts/check-go-api-staleness.sh

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

# Run Playwright integration e2e tests (self-contained, starts loom serve automatically)
test-e2e-integration:
	@echo "Running Playwright integration e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration

# Run Playwright integration e2e tests against local loom serve
test-e2e-integration-local:
	@echo "Running Playwright integration e2e tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration

# Run ALL Playwright integration e2e tests including cross-workspace and terminal-parity
test-e2e-integration-full:
	@echo "Running full Playwright integration e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 RUN_LOCAL_INTEGRATION_TESTS=1 npx playwright test --project=integration --project=local-integration

# Run auth service unit + security tests
test-auth-service:
	@echo "Running auth service tests..."
	@cd services/auth && node --experimental-test-module-mocks --import tsx --test 'src/**/*.test.ts'

# Run full-stack auth E2E smoke test (requires docker)
test-auth-e2e:
	@echo "Running full-stack auth E2E smoke test..."
	@bash e2e/auth/run_test.sh

# Install loom to GOPATH/bin (Go-only; no frontend dependency)
install:
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

# Git hooks directory (resolves correctly in both regular repos and worktrees)
GIT_HOOKS_DIR := $(shell git rev-parse --git-path hooks)

# Beads subtree config
BEADS_REMOTE := https://github.com/tysonthomas9/beads
BEADS_BRANCH := feature/web-ui
BEADS_PREFIX := third_party/beads

# Build the frontend dist (requires Node.js >= 20). Go-free.
build-frontend:
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build

# Build both Go binary and frontend dist
build-all: build build-frontend

# Deprecated alias — use 'make build-frontend'
frontend: build-frontend
	@echo "Note: 'make frontend' is deprecated. Use 'make build-frontend'."

# Sync beads library packages (rewrite imports from vendored copy)
sync-beads:
	@echo "Syncing beads library packages..."
	./scripts/sync-beads.sh

# Pull latest beads from upstream and sync
update-beads:
	@echo "Pulling latest beads from $(BEADS_REMOTE) ($(BEADS_BRANCH))..."
	git subtree pull --prefix=$(BEADS_PREFIX) $(BEADS_REMOTE) $(BEADS_BRANCH) --squash
	$(MAKE) sync-beads

# Go-only quality gate (no Node, no frontend dist)
check-go:
	@echo "=== [1/12] Go: format check ==="
	@bad=$$(gofmt -l . 2>/dev/null | grep -v third_party | grep -v worktrees | grep -v vendor | grep -v node_modules | head -20); \
	if [ -n "$$bad" ]; then echo "gofmt violations:"; echo "$$bad"; exit 1; fi
	@echo "=== [2/12] Go: vet ==="
	@go vet ./...
	@echo "=== [3/12] Go: build ==="
	@go build -buildvcs=false ./...
	@echo "=== [4/12] Go: lint (golangci-lint + depguard) ==="
	@golangci-lint run --timeout=5m --allow-parallel-runners
	@echo "=== [5/12] Go: LOC check ==="
	@./scripts/check-loc.sh 1000 2500
	@echo "=== [6/12] Go: package size check ==="
	@./scripts/check-package-size.sh 25
	@echo "=== [7/12] Go: import fanout check ==="
	@./scripts/check-import-fanout.sh 12
	@echo "=== [8/12] Go: exec.Command guard ==="
	@./scripts/check-no-raw-exec.sh
	@echo "=== [9/12] Go: log.Printf guard ==="
	@./scripts/check-no-log-printf.sh
	@echo "=== [10/12] Go: generated API staleness ==="
	@./scripts/check-go-api-staleness.sh
	@echo "=== [11/12] Go: test with race detector ==="
	@go test -race -covermode=atomic -coverprofile=/tmp/loom.coverage.out -timeout 15m ./...
	@echo "=== [12/12] Go: coverage threshold ==="
	@COVERAGE_THRESHOLD=60 ./scripts/check-coverage.sh
	@echo "=== Go quality gates PASSED ==="

# Frontend-only quality gate (no Go toolchain, no dist prerequisite)
check-frontend:
	@echo "=== [1/6] Frontend: format check ==="
	@cd $(FRONTEND_DIR) && npm run format:check
	@echo "=== [2/6] Frontend: typecheck ==="
	@cd $(FRONTEND_DIR) && npm run typecheck
	@echo "=== [3/6] Frontend: eslint ==="
	@cd $(FRONTEND_DIR) && npm run lint
	@echo "=== [4/6] Frontend: architectural checks ==="
	@cd $(FRONTEND_DIR) && npm run check:arch
	@echo "=== [5/6] Frontend: generated code staleness ==="
	@cd $(FRONTEND_DIR) && npm run check:generated
	@echo "=== [6/6] Frontend: unit tests + coverage (60% threshold) ==="
	@cd $(FRONTEND_DIR) && npm run test:coverage
	@echo "=== Frontend quality gates PASSED ==="

# Unified quality gate — runs Go + frontend checks in parallel
check:
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
	@test -n '$(GIT_HOOKS_DIR)' || { echo "Error: not inside a git repository"; exit 1; }
	@mkdir -p '$(GIT_HOOKS_DIR)'
	@cp scripts/hooks/pre-push '$(GIT_HOOKS_DIR)/pre-push'
	@chmod +x '$(GIT_HOOKS_DIR)/pre-push'
	@echo "Pre-push hook installed (applies to all worktrees)"
	@command -v pre-commit >/dev/null 2>&1 || { echo "Error: pre-commit not found. Install: brew install pre-commit"; exit 1; }
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Error: golangci-lint not found. Install: brew install golangci-lint"; exit 1; }
	@pre-commit install
	@echo "Pre-commit hooks installed"

# Ensure hooks are installed (runs once — skips if pre-push hook already exists)
ensure-hooks:
	@test -f '$(GIT_HOOKS_DIR)/pre-push' || $(MAKE) hooks

# Check dev dependencies
dev-check:
	@command -v air >/dev/null 2>&1 || { echo "Error: air not found. Install: go install github.com/air-verse/air@latest"; exit 1; }
	@command -v node >/dev/null 2>&1 || { echo "Error: node not found. Install Node.js >= 20"; exit 1; }
	@echo "All dev dependencies found."

# Run dev environment: Go API server on :8080 + Vite dev server on :3000
dev: dev-check ensure-hooks
	@./scripts/dev.sh

# Deprecated alias — 'make dev-loom' is removed in a follow-up task.
# Post-Phase-5 there is no Loom-served UI; use `make dev` (Vite on :3000).
dev-loom: dev-check
	@echo "Note: 'make dev-loom' is deprecated — use 'make dev'."
	@./scripts/dev.sh

# Deprecated alias — 'make dev-vite' is removed in a follow-up task.
# Post-Phase-5 this is the only dev path; use `make dev`.
dev-vite: dev-check
	@echo "Note: 'make dev-vite' is deprecated — use 'make dev'."
	@./scripts/dev.sh

# Show help
help:
	@echo "Loomcli Makefile targets:"
	@echo "  make build            - Build the loom Go binary (no frontend)"
	@echo "  make build-frontend   - Build the frontend dist (no Go)"
	@echo "  make build-all        - Build both Go binary and frontend dist"
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
	@echo "  make test-e2e-integration - Run Playwright integration e2e tests (self-contained)"
	@echo "  make test-e2e-integration-local - Run Playwright integration e2e tests (needs loom serve)"
	@echo "  make test-e2e-integration-full - Run ALL integration e2e tests (cross-workspace + terminal-parity)"
	@echo "  make install    - Install loom + bd to GOPATH/bin"
	@echo "  make install-bd - Install bd (beads CLI) from vendored source"
	@echo "  make frontend         - DEPRECATED alias for make build-frontend"
	@echo "  make sync-beads   - Sync beads packages (rewrite imports)"
	@echo "  make update-beads - Pull latest beads + sync"
	@echo "  make check        - Unified quality gate (all 14 checks)"
	@echo "  make check-go     - Go-only quality gate (no Node, no frontend dist)"
	@echo "  make check-frontend - Frontend-only quality gate"
	@echo "  make gate         - Alias for make check"
	@echo "  make gate-e2e     - Quality gate + Playwright API e2e tests (no Docker)"
	@echo "  make gate-e2e-full - Quality gate + API e2e + Docker container tests"
	@echo "  make hooks        - Install git hooks (pre-push gate)"
	@echo "  make dev          - Start dev environment (Go API :8080 + Vite :3000)"
	@echo "  make dev-loom     - DEPRECATED alias for make dev"
	@echo "  make dev-vite     - DEPRECATED alias for make dev"
	@echo "  make dev-check    - Check dev dependencies (air, node)"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make help         - Show this help message"
