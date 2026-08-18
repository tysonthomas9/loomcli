# Makefile for loomcli project

.PHONY: all build build-frontend build-all test test-builtin-workflows test-integration test-all test-playground test-fleetdb-embedded test-fleetdb-supervisor test-fleetdb-ui test-fleetdb-empty-cli fleetdb-empty-up fleetdb-empty-down fleetdb-regression-up fleetdb-regression-down test-env-up test-env-down test-env-status ensure-frontend-dist ensure-frontend-deps local-mode-frontend-dist local-mode-up local-mode-codex-up local-mode-claude-up local-mode-daytona-up local-mode-down local-mode-logs local-mode-verify local-mode-codex-verify test-local-mode-harness test-distributed-smoke lint lint-frontend test-frontend e2e test-e2e test-e2e-api test-e2e-api-local test-e2e-real-smoke test-e2e-real-smoke-local test-e2e-real-regression test-e2e-real-regression-local test-e2e-integration test-e2e-integration-local test-e2e-integration-full clean install help frontend check check-go check-frontend gate gate-e2e gate-e2e-full hooks ensure-hooks dev dev-check dev-loom dev-vite check-loc check-loc-stale check-control-plane-paths check-no-raw-exec check-no-beads-prod test-coverage test-forkwatch test-frontend-coverage test-race-cover test-integration-race-cover gen-go-api check-go-api-staleness local-mode-webhook-verify local-mode-skills-verify local-mode-skill-pointer-verify test-e2e-github-webhook test-e2e-github-webhook-live

# Default target
all: build

LOCAL_MODE_COMPOSE_PROJECT ?= loomcli-local-mode
LOCAL_MODE_COMPOSE ?=
LOCAL_MODE_COMPOSE_FILES ?=
LOCAL_MODE_COMPOSE_UP_FLAGS ?= --build
LOCAL_MODE_FLEETDB_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-fleet-db:latest
LOCAL_MODE_LOOM_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom:latest
LOCAL_MODE_LOOM_CODEX_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom-codex:latest
LOCAL_MODE_LOOM_CLAUDE_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom-claude:latest
LOCAL_MODE_COMPOSE_EXTRA := $(foreach file,$(LOCAL_MODE_COMPOSE_FILES),-f $(file))
LOCAL_MODE_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_CODEX_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.codex.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_CLAUDE_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.claude.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_DAYTONA_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.daytona.yml $(LOCAL_MODE_COMPOSE_EXTRA)
export LOCAL_MODE_FLEETDB_IMAGE
export LOCAL_MODE_LOOM_IMAGE
export LOCAL_MODE_LOOM_CODEX_IMAGE
export LOCAL_MODE_LOOM_CLAUDE_IMAGE
LOCAL_MODE_COMPOSE_SELECT = \
	if [ "$(strip $(LOCAL_MODE_COMPOSE))" != "" ]; then \
	  compose="$(LOCAL_MODE_COMPOSE)"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	elif command -v podman-compose >/dev/null 2>&1; then \
	  compose="podman-compose"; \
	elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	else \
	  echo "podman compose or docker compose is required" >&2; \
	  exit 127; \
	fi

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

# Run the builtin workflow node unit tests (internal/workflows/builtin/*.test.mjs).
# Deliberately NOT folded into check-go (that gate is Go-only + decoupling-smoke
# tested); run it standalone here and in the builtin-bundle-pin CI job. Needs ../flue.
test-builtin-workflows:
	@echo "Running builtin workflow node tests..."
	@./scripts/test-builtin-workflows.sh

# Daemon-lifecycle failure-mode harness (crash/hang/slow backends + a
# happy-path scaffold). Requires `loom serve` running on
# http://localhost:8080 and a clean ~/.loom (or no orphan PLAYGROUND
# state). The shell + Go tests drive setup.sh → daemon → assertions →
# teardown.sh. The Playwright stage re-creates the workspace and asserts
# the kanban renders the seed tasks. For full-stack dogfooding use
# `make local-mode-up` instead — see test/playground/README.md.
test-playground:
	@echo "=== Playground: Go scenarios (happy path + failure modes) ==="
	@LOOM_BASE_URL="$${LOOM_BASE_URL:-http://localhost:8080}" LOOM_PLAYGROUND_REQUIRE_SERVE=1 go test -tags=playground -count=1 -timeout=5m ./test/playground/...
	@echo "=== Playground: Playwright UI test ==="
	@set -e; \
		root="$$(pwd)"; \
		base_url="$${LOOM_BASE_URL:-http://localhost:8080}"; \
		frontend_url="$${LOOM_FRONTEND_BASE_URL:-$$base_url}"; \
		bash "$$root/test/playground/teardown.sh" >/dev/null 2>&1 || true; \
		trap 'bash "$$root/test/playground/teardown.sh" >/dev/null 2>&1 || true' EXIT; \
		bash "$$root/test/playground/setup.sh" >/dev/null; \
		cd "$$root/internal/webui/frontend"; \
		RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 LOOM_BASE_URL="$$base_url" LOOM_FRONTEND_BASE_URL="$$frontend_url" \
			npx playwright test --project=integration playground.integration.spec.ts

# Run clean-checkout embedded local mode smoke. Requires a loom binary and
# fleet-db binary; override with LOOM_BIN=/path/to/loom FLEET_DB_BIN=/path/to/fleet-db.
test-fleetdb-embedded: build
	@echo "Running fleet-db-only clean checkout embedded smoke..."
	LOOM_BIN="$(PWD)/loom" scripts/test-fleetdb-clean-checkout.sh

test-fleetdb-supervisor:
	@echo "Running fleet-db supervisor control-plane gate..."
	go test -count=1 ./internal/cli ./internal/cli/data ./internal/cli/agentdef ./internal/cli/daemon ./internal/cli/daemon/supervisor \
	  -run 'Test(AgentIPCClient|IPCServer_|Data(Ready|ShowClaimClose)_NoServer|ClaimTask_|TaskIDForLifecycle_|Supervisor(Register|Heartbeats|Mirrors)ControlPlane|BuildCommand_SessionEnvVars)'

# Run the UI browser suite in fleet-db-only regression mode.
# Assumes docker-compose.regression.yml is already up AND seeded — the suite's
# preflight will abort with an actionable error if anything is missing
# (never silently "skip because no stack" — see ui-test-plan.md §0).
#
# Usage:
#   docker compose -f test/fleetdb/docker-compose.regression.yml up -d
#   docker compose -f test/fleetdb/docker-compose.regression.yml run --rm fleetdb-regression-seed-fleet
#   make test-fleetdb-ui
#
# Environment overrides accepted (see playwright.config.ts):
#   LOOM_FLEET_URL   default http://localhost:8082
#   FLEET_DB_URL     default http://localhost:8080
#   FLEETDB_WORKSPACE default FLEETDB
test-fleetdb-ui:
	@echo "Running fleet-db-only UI regression suite (Playwright)..."
	@cd test/fleetdb/ui && \
	  if [ ! -d node_modules ]; then \
	    echo "[test-fleetdb-ui] installing npm deps..."; \
	    npm install --no-audit --no-fund --silent || exit 1; \
	    npx playwright install --with-deps chromium || exit 1; \
	  fi && \
	  FLEETDB_MODE=fleet-only npx playwright test

# CLI integration scenarios against the empty fleet-db stack.
# Brings up the stack (or reuses a running one), runs workspace-resolution
# + doctor probe scenarios via podman exec, tears it down on exit.
test-fleetdb-empty-cli:
	@./scripts/test-fleetdb-empty-cli.sh

# Start an empty fleet-db-only UI stack for manual new-user testing. This stack
# has no seeded workspaces or issues; create a workspace from the UI.
fleetdb-empty-up: build-frontend
	@echo "Starting empty fleet-db UI stack on http://localhost:$${LOOM_UI_PORT:-8091}..."
	@set -e; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	elif command -v podman-compose >/dev/null 2>&1; then \
	  compose="podman-compose"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	else \
	  echo "docker compose or podman compose is required" >&2; \
	  exit 127; \
	fi; \
	$$compose -f test/fleetdb/docker-compose.empty.yml up --build

fleetdb-empty-down:
	@set -e; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	elif command -v podman-compose >/dev/null 2>&1; then \
	  compose="podman-compose"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	else \
	  echo "docker compose or podman compose is required" >&2; \
	  exit 127; \
	fi; \
	$$compose -f test/fleetdb/docker-compose.empty.yml down -v --remove-orphans

# Guard for every UI-bearing compose stack. The Web UI bundle is gitignored
# build output, and compose bind-mounts $(FRONTEND_DIR)/dist into the Caddy
# sidecar. Docker silently substitutes an empty directory when the bind-mount
# source is missing, so a stack started from a clean checkout comes up "healthy"
# and serves 404 with nothing in any log. Build it once on the host instead.
ensure-frontend-dist:
	@if [ ! -f "$(FRONTEND_DIR)/dist/index.html" ]; then \
	  echo "Web UI dist is missing; building it once on the host..."; \
	  $(MAKE) build-frontend; \
	fi

# Guard for every target that shells into the frontend toolchain. npm scripts
# resolve binaries out of node_modules/.bin, which is gitignored and therefore
# absent in a fresh clone and in any new git worktree (worktrees do not inherit
# the parent's ignored files). Without this the gate dies with a bare
#   sh: prettier: command not found
#   make[1]: *** [check-frontend] Error 127
# and because check-frontend runs inside the pre-push hook, that surfaces as
# "git push is broken" with nothing pointing at missing dependencies. The
# playwright targets fail worse: npx reaches for the registry and runs an
# unpinned browser driver instead of failing at all.
#
# Marker is node_modules/.package-lock.json, the file npm writes when it
# finishes reifying the tree — same idiom as scripts/dev.sh:61-68. A bare
# presence check on a binary passes for an interrupted install and for a tree
# that predates a package.json/package-lock.json change (e.g. after `git pull`),
# which then fails at import time instead of here.
ensure-frontend-deps:
	@marker="$(FRONTEND_DIR)/node_modules/.package-lock.json"; \
	if [ ! -f "$$marker" ] \
	   || [ "$(FRONTEND_DIR)/package-lock.json" -nt "$$marker" ] \
	   || [ "$(FRONTEND_DIR)/package.json" -nt "$$marker" ]; then \
	  echo "Frontend dependencies are missing or stale; installing them once on the host..."; \
	  cd $(FRONTEND_DIR) && npm install; \
	fi

# Disposable fleet-db backend for tests that need a real control plane. Kept
# separate from the live stack on :3011, which holds real workspace data: a test
# that writes there is mutating production. See scripts/test-env.sh.
test-env-up:
	@./scripts/test-env.sh up

test-env-down:
	@./scripts/test-env.sh down

test-env-status:
	@./scripts/test-env.sh status

# Start the fleet-db regression stack: redis, fleet-db, loom serve on the
# fleet-db backend, the Web UI sidecar, and a one-shot fixture seeder.
#
# Engine support: this stack needs docker compose >= 2.22, which is where
# `additional_contexts: fdb-source: "service:fleet-db"` (the build-time edge the
# seeder image depends on) landed. The podman-compose / `podman compose`
# fallbacks below are kept for parity with fleetdb-empty-up, but they are not
# known to implement `service:` build contexts: on those engines the seeder
# build degrades back to the original "pull access denied" failure. Use docker
# compose for the regression stack.
fleetdb-regression-up: ensure-frontend-dist
	@echo "Starting fleet-db regression stack (API http://localhost:8082, UI http://localhost:8083)..."
	@set -e; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	elif command -v podman-compose >/dev/null 2>&1; then \
	  compose="podman-compose"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	else \
	  echo "docker compose or podman compose is required" >&2; \
	  exit 127; \
	fi; \
	$$compose -f test/fleetdb/docker-compose.regression.yml up --build -d

fleetdb-regression-down:
	@set -e; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	elif command -v podman-compose >/dev/null 2>&1; then \
	  compose="podman-compose"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	else \
	  echo "docker compose or podman compose is required" >&2; \
	  exit 127; \
	fi; \
	$$compose -f test/fleetdb/docker-compose.regression.yml down -v --remove-orphans

# Start the local-mode dogfood stack: fleet-db, loom serve, workspace daemon
# manager, a deterministic planner/coder backend, and the Web UI.
local-mode-frontend-dist: ensure-frontend-dist

local-mode-up: local-mode-frontend-dist
	@echo "Starting local-mode dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-codex-up: local-mode-frontend-dist
	@echo "Starting local-mode Codex dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_CODEX_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-claude-up: local-mode-frontend-dist
	@echo "Starting local-mode Claude dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_CLAUDE_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

# Daemon TS leaf routed to Daytona: a claimed task runs inside a real Daytona
# sandbox. Requires DAYTONA_API_KEY on the host; DAYTONA_REPO_URL must be a
# network-reachable git URL. e.g.:
#   DAYTONA_API_KEY=... LOOM_DAEMON_LEAF=ts make local-mode-daytona-up
local-mode-daytona-up: local-mode-frontend-dist
	@echo "Starting local-mode Daytona dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_DAYTONA_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-down:
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) down -v --remove-orphans

local-mode-logs:
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) logs -f loom-local ui-local

local-mode-verify:
	@test/local-mode/verify-local-mode.sh

# Verify role-based task routing for UI-registered plan/task agents against a
# running stack: seeds a no-design task (must go to the plan agent) and a
# designed task (must go to the task agent), exercises the UI POST /agents
# endpoint, and asserts the claims. Pairs with `LOOM_DAEMON_LEAF=ts make
# local-mode-codex-up` to prove UI agent creation maps to the TS execution path.
local-mode-routing-verify:
	@python3 test/local-mode/verify-agent-routing.py

# Real-stack E2E for the trigger-driven GitHub webhook path: signs a
# pull_request.opened delivery, asserts the durable TriggerEvent/Delivery/
# DriverRun records, and checks redelivery is idempotent. Requires a running
# `make local-mode-up` stack plus curl, openssl, and python3.
local-mode-webhook-verify:
	@test/local-mode/verify-webhook.sh

# Deterministic end-to-end verification of the skills vertical (fleet-db CRUD
# -> CLI -> materializer -> INDEX.md/catalog projection -> baked backend
# hook/pointer config) against a running local-mode stack. No model calls.
local-mode-skills-verify:
	@test/local-mode/verify-skills.sh

# Live-model smoke: a long-lived codex session must learn about skills
# added/removed after its session-start snapshot, via the managed
# UserPromptSubmit hook (files) and the loom-skill-catalog/INDEX.md pointer
# (awareness). Burns real model tokens; needs make local-mode-codex-up.
# On-demand only — keep out of CI.
local-mode-skill-pointer-verify:
	@test/local-mode/verify-skill-pointer.sh

local-mode-codex-verify:
	@LOOM_LOCAL_MODE_PLAN_TASK_ID="$${LOOM_LOCAL_MODE_PLAN_TASK_ID:-LOCALMODE-2}" \
	  LOOM_LOCAL_MODE_CODE_TASK_ID="$${LOOM_LOCAL_MODE_CODE_TASK_ID:-LOCALMODE-3}" \
	  test/local-mode/verify-local-mode.sh

test-local-mode-harness: local-mode-verify

# Run the fleet-db distributed smoke stack: shared fleet-db/Redis, two loom
# serve processes, two local supervisor heartbeat loops, and a one-shot smoke
# runner that reports auth, claim contention, SSE reconnect catch-up, and WebUI
# health. Builds local static loom/fleet-db binaries, then mounts them into
# small runtime containers. Override FLEET_DB_REPO if the sibling repo is not at
# ../../fleet-db. Falls back to Podman Compose when Docker is unavailable.
test-distributed-smoke:
	@echo "Running fleet-db distributed smoke..."
	@mkdir -p "$(DISTRIBUTED_SMOKE_BIN)"
	@echo "[distributed-smoke] building loom binary for $(DISTRIBUTED_SMOKE_GOOS)/$(DISTRIBUTED_SMOKE_GOARCH)..."
	@CGO_ENABLED=0 GOOS="$(DISTRIBUTED_SMOKE_GOOS)" GOARCH="$(DISTRIBUTED_SMOKE_GOARCH)" go build -o "$(DISTRIBUTED_SMOKE_BIN)/loom" ./cmd/loom
	@echo "[distributed-smoke] building fleet-db binary from $(FLEET_DB_REPO) for $(DISTRIBUTED_SMOKE_GOOS)/$(DISTRIBUTED_SMOKE_GOARCH)..."
	@cd "$(FLEET_DB_REPO)" && CGO_ENABLED=0 GOOS="$(DISTRIBUTED_SMOKE_GOOS)" GOARCH="$(DISTRIBUTED_SMOKE_GOARCH)" go build -o "$(DISTRIBUTED_SMOKE_BIN)/fleet-db" ./cmd/fleet-db
	@set +e; \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then \
	  compose="docker compose"; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then \
	  compose="podman compose"; \
	else \
	  echo "docker compose or podman compose is required" >&2; \
	  exit 127; \
	fi; \
	compose="$$compose -f test/distributed/docker-compose.smoke.yml"; \
	trap "$$compose down -v --remove-orphans" EXIT; \
	$$compose build; \
	status=$$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	$$compose up --abort-on-container-exit --exit-code-from distributed-smoke distributed-smoke; \
	status=$$?; \
	exit $$status

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
	@./scripts/check-control-plane-paths.sh

# Run Go tests with coverage threshold enforcement
test-coverage: test
	@./scripts/check-coverage.sh

# Run frontend tests with coverage threshold enforcement
test-frontend-coverage: ensure-frontend-deps
	@echo "Running frontend tests with coverage..."
	@cd $(FRONTEND_DIR) && npm run test:coverage

# Check Go file LOC limits
check-loc:
	@./scripts/check-loc.sh 1000 2500

check-control-plane-paths:
	@./scripts/check-control-plane-paths.sh

# Check for stale LOC allowlist entries
check-loc-stale:
	@./scripts/check-loc-stale.sh --check-stale 1000

# Run tests under a fork-bomb watchdog: kills the run and fails fast if test
# binaries start multiplying (a test spawning os.Executable() re-runs the whole
# suite recursively) instead of taking down the machine. Also fails on test
# processes leaked past the end of the run.
#   make test-forkwatch                                   # whole repo
#   make test-forkwatch PKG=./internal/cli/daemon/...     # one subtree
test-forkwatch:
	@./scripts/test-forkwatch.sh $(or $(PKG),./...)

# Check for raw exec.Command in unit tests (enforces DI)
check-no-raw-exec:
	@echo "Checking for raw exec.Command in unit tests..."
	@./scripts/check-no-raw-exec.sh

check-no-beads-prod:
	@echo "Checking for new production beads/bd references..."
	@./scripts/check-no-beads-prod.sh

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
lint-frontend: ensure-frontend-deps
	@echo "Running frontend typecheck..."
	@cd $(FRONTEND_DIR) && npm run typecheck
	@echo "Running frontend ESLint..."
	@cd $(FRONTEND_DIR) && npm run lint

# Run frontend unit tests (vitest)
test-frontend: ensure-frontend-deps
	@echo "Running frontend unit tests..."
	@cd $(FRONTEND_DIR) && npx vitest run

# Run Playwright e2e tests — mocked chromium tests (no server needed)
e2e: test-e2e

test-e2e: ensure-frontend-deps
	@echo "Running Playwright e2e tests (mocked)..."
	@cd $(FRONTEND_DIR) && npx playwright install --with-deps chromium 2>/dev/null || true
	@cd $(FRONTEND_DIR) && npx playwright test --project=chromium --workers=1

# Run Playwright API e2e tests (self-contained: builds loom, starts server, runs tests)
test-e2e-api: ensure-frontend-deps
	@echo "Running Playwright API e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=api

# Run Playwright API e2e tests against already-running loom serve
test-e2e-api-local: ensure-frontend-deps
	@echo "Running Playwright API e2e tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=api

# Run the real ephemeral fleet-db + loom serve GitHub webhook dispatch e2e.
test-e2e-github-webhook:
	@echo "Running GitHub webhook e2e (ephemeral fleet-db + loom serve)..."
	@GOCACHE=$${GOCACHE:-/tmp/go-build-cache} go test -count=1 -tags e2e -run TestE2E_GitHubWebhookDispatchesDriverRunWithEphemeralStack ./internal/webui/handlers/webhooks

# Run the live GitHub webhook e2e against a real private/public repo. Creates
# and closes a temporary PR. Usage: LOOM_E2E_GITHUB_REPO=owner/repo make test-e2e-github-webhook-live
test-e2e-github-webhook-live:
	@: "$${LOOM_E2E_GITHUB_REPO:?set LOOM_E2E_GITHUB_REPO=owner/repo}"
	@echo "Running live GitHub webhook e2e against $$LOOM_E2E_GITHUB_REPO..."
	@GOCACHE=$${GOCACHE:-/tmp/go-build-cache} go test -count=1 -tags e2e -run TestE2E_GitHubWebhookRunsDriverAgainstLiveGitHubPR ./internal/webui/handlers/webhooks -timeout 5m

# Compile + run the real GitHub stacked-PR publisher e2e (initial publish / re-run /
# drop-a-unit / reorder). The test is //go:build e2e tagged, so this target is also the
# compile guard against bit-rot. Skips unless gated:
#   LOOM_STACK_E2E=1 LOOM_STACK_E2E_REPO=owner/name (+ gh auth) make test-e2e-stackpublish
test-e2e-stackpublish:
	@echo "Running stacked-PR publisher e2e (skips unless LOOM_STACK_E2E is set)..."
	@GOCACHE=$${GOCACHE:-/tmp/go-build-cache} go test -count=1 -tags e2e -run TestE2EStackPublisher ./internal/stackpublish -timeout 10m

# Run the real Playwright smoke suite: browser + API contracts against FleetDB-backed loom serve.
test-e2e-real-smoke: ensure-frontend-deps
	@echo "Running real Playwright smoke tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration-smoke --project=api-smoke

# Run the real Playwright smoke suite against an already-running loom serve/UI.
test-e2e-real-smoke-local: ensure-frontend-deps
	@echo "Running real Playwright smoke tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration-smoke --project=api-smoke

# Run the real Playwright regression suite: slower browser + API contracts.
test-e2e-real-regression: ensure-frontend-deps
	@echo "Running real Playwright regression tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration-regression --project=api-regression

# Run the real Playwright regression suite against an already-running loom serve/UI.
test-e2e-real-regression-local: ensure-frontend-deps
	@echo "Running real Playwright regression tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration-regression --project=api-regression

# Run Playwright integration e2e tests (self-contained, starts loom serve automatically)
test-e2e-integration: ensure-frontend-deps
	@echo "Running Playwright integration e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration

# Run Playwright integration e2e tests against local loom serve
test-e2e-integration-local: ensure-frontend-deps
	@echo "Running Playwright integration e2e tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration

# Run ALL Playwright integration e2e tests including cross-workspace and terminal-fleetdb-regression
test-e2e-integration-full: ensure-frontend-deps
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

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f loom
	rm -f /tmp/loom.coverage.out

# Frontend directory
FRONTEND_DIR := internal/webui/frontend
LOCAL_FLEET_DB_REPO := $(firstword $(wildcard $(CURDIR)/../fleet-db $(CURDIR)/../../fleet-db))
LOCAL_FLEET_DB_BIN := $(firstword $(wildcard $(CURDIR)/../fleet-db/fleet-db $(CURDIR)/../../fleet-db/fleet-db))
FLEET_DB_REPO ?= $(if $(LOCAL_FLEET_DB_REPO),$(LOCAL_FLEET_DB_REPO),../../fleet-db)
ifneq ($(LOCAL_FLEET_DB_BIN),)
FLEET_DB_BIN ?= $(LOCAL_FLEET_DB_BIN)
export FLEET_DB_BIN
endif
DISTRIBUTED_SMOKE_BIN := $(CURDIR)/tmp/distributed-smoke/bin
DISTRIBUTED_SMOKE_GOOS ?= linux
DISTRIBUTED_SMOKE_GOARCH ?= $(shell go env GOARCH)

# Git hooks directory (resolves correctly in both regular repos and worktrees)
GIT_HOOKS_DIR := $(shell git rev-parse --git-path hooks)

# Build the frontend dist (requires Node.js >= 20). Go-free.
build-frontend:
	@echo "Building frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build

# Build both Go binary and frontend dist
build-all: build build-frontend

# Deprecated alias — use 'make build-frontend'
frontend: build-frontend
	@echo "Note: 'make frontend' is deprecated. Use 'make build-frontend'."

# Go-only quality gate (no Node, no frontend dist)
check-go:
	@echo "=== [1/13] Go: format check ==="
	@bad=$$(gofmt -l . 2>/dev/null | grep -v third_party | grep -v worktrees | grep -v vendor | grep -v node_modules | head -20); \
	if [ -n "$$bad" ]; then echo "gofmt violations:"; echo "$$bad"; exit 1; fi
	@echo "=== [2/13] Go: vet ==="
	@go vet ./...
	@echo "=== [3/13] Go: build ==="
	@go build -buildvcs=false ./...
	@echo "=== [4/13] Go: lint (golangci-lint + depguard + control-plane path guard) ==="
	@golangci-lint run --timeout=5m --allow-parallel-runners
	@./scripts/check-control-plane-paths.sh
	@echo "=== [5/13] Go: LOC check ==="
	@./scripts/check-loc.sh 1000 2500
	@echo "=== [6/13] Go: package size check ==="
	@./scripts/check-package-size.sh 25
	@echo "=== [7/13] Go: import fanout check ==="
	@./scripts/check-import-fanout.sh 18
	@echo "=== [8/13] Go: exec.Command guard ==="
	@./scripts/check-no-raw-exec.sh
	@echo "=== [9/13] Go: log.Printf guard ==="
	@./scripts/check-no-log-printf.sh
	@echo "=== [10/13] Go: no new production beads/bd references ==="
	@./scripts/check-no-beads-prod.sh
	@echo "=== [11/13] Go: generated API staleness ==="
	@./scripts/check-go-api-staleness.sh
	@echo "=== [12/13] Go: test with race detector ==="
#   Steps 12 and 13 share one shell so they can share a PER-RUN profile path.
#   A fixed /tmp path is unsafe two ways: two concurrent gates interleave writes
#   into one file, and a gate killed mid-`go test` leaves a truncated file behind
#   for the next run to read. Either produces a malformed record and the opaque
#   failure `cover: line "..." doesn't match expected format`, which looks like a
#   coverage regression but is a corrupt profile. The trap removes it on every
#   exit path, so nothing stale survives to poison a later run.
	@set -e; \
	 profile="$$(mktemp "$${TMPDIR:-/tmp}/loom.coverage.XXXXXX")"; \
	 trap 'rm -f "$$profile"' EXIT; \
	 ./scripts/with-clean-loom-env.sh go test -p 1 -race -covermode=atomic -coverprofile="$$profile" -timeout 15m ./...; \
	 echo "=== [13/13] Go: coverage threshold ==="; \
	 COVERAGE_THRESHOLD=60 ./scripts/check-coverage.sh "$$profile"
	@echo "=== Go quality gates PASSED ==="

# Frontend-only quality gate (no Go toolchain, no dist prerequisite)
check-frontend: ensure-frontend-deps
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
	@$(MAKE) test-e2e-real-smoke
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

# Ensure hooks are installed (skips only when the installed pre-push already
# matches scripts/hooks/pre-push byte for byte)
# Reinstall when the hook is missing *or* has drifted from scripts/hooks/pre-push.
# scripts/hooks/pre-push is the source of truth and the installed hook is a
# managed artifact: `cmp` sees any difference, so a hand-edited local hook is
# overwritten on the next `make dev`. That is deliberate — local copies drifting
# is the whole failure mode below — and `make hooks` announces the reinstall.
# An existence-only check lets a stale hook survive forever, which is not
# hypothetical: a pre-push installed before scripts/hooks/pre-push started
# clearing `git rev-parse --local-env-vars` leaves GIT_DIR exported into
# `make check`. Every test that drives a throwaway repo under t.TempDir() then
# resolves git against this checkout instead — `git commit` fires this repo's
# pre-commit hook ("No .pre-commit-config.yaml file was found") and branch
# lookups return the branch being pushed. Ten packages fail that way and the
# gate can never pass, so nothing can be pushed at all.
ensure-hooks:
	@cmp -s scripts/hooks/pre-push '$(GIT_HOOKS_DIR)/pre-push' || $(MAKE) hooks

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
	@echo "  make test-forkwatch    - Run tests under a fork-bomb/process-leak watchdog (PKG=./path/...)"
	@echo "  make check-no-raw-exec - Check for raw exec.Command in unit tests"
	@echo "  make check-control-plane-paths - Check local/cloud fleet-db runtime path invariants"
	@echo "  make check-loc-stale   - Check for stale LOC allowlist entries"
	@echo "  make lint              - Run Go linter (golangci-lint)"
	@echo "  make lint-frontend     - Run frontend typecheck + ESLint"
	@echo "  make test-frontend     - Run frontend unit tests (vitest)"
	@echo "  make test-e2e          - Run Playwright mocked e2e tests (no server)"
	@echo "  make test-fleetdb-ui   - Run fleet-db-only UI regression suite"
	@echo "  make test-env-up        - Start the disposable fleet-db test backend (workspace LOOMTEST, :53351)"
	@echo "  make test-env-down      - Stop it and drop its volumes"
	@echo "                            Point a shell at it: eval \"\$$(scripts/test-env.sh env)\""
	@echo "                            Use this, not the live stack on :3011, for anything that writes"
	@echo "  make local-mode-up      - Run local-mode Podman/Docker stack"
	@echo "  make local-mode-codex-up - Run local-mode stack with Codex agents"
	@echo "  make local-mode-verify  - Verify deterministic local-mode stack"
	@echo "  make local-mode-codex-verify - Verify Codex local-mode stack"
	@echo "  make local-mode-skills-verify - Verify the skills vertical e2e (no model)"
	@echo "  make local-mode-skill-pointer-verify - Live-model smoke of the skill catalog pointer"
	@echo "  make local-mode-logs    - Tail selected local-mode stack logs"
	@echo "  make local-mode-down    - Stop selected local-mode stack and volumes"
	@echo "    LOCAL_MODE_COMPOSE='docker compose' LOCAL_MODE_COMPOSE_PROJECT=name LOCAL_MODE_UI_PORT=8383 LOCAL_MODE_API_PORT=8382 LOCAL_MODE_FLEETDB_PORT=8380"
	@echo "    LOCAL_MODE_COMPOSE_FILES=/path/override.yml LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' make local-mode-up"
	@echo "    LOCAL_MODE_FLEETDB_IMAGE=tag LOCAL_MODE_LOOM_IMAGE=tag LOCAL_MODE_LOOM_CODEX_IMAGE=tag"
	@echo "  make test-fleetdb-embedded - Run clean-checkout embedded fleet-db smoke"
	@echo "  make test-distributed-smoke - Run fleet-db distributed compose smoke"
	@echo "  make test-e2e-api      - Run Playwright API e2e tests (self-contained)"
	@echo "  make test-e2e-api-local - Run Playwright API e2e tests (needs loom serve)"
	@echo "  make test-e2e-real-smoke - Run real Playwright smoke tests (browser + API)"
	@echo "  make test-e2e-real-smoke-local - Run real Playwright smoke tests (needs loom serve/UI)"
	@echo "  make test-e2e-real-regression - Run real Playwright regression tests"
	@echo "  make test-e2e-real-regression-local - Run real Playwright regression tests (needs loom serve/UI)"
	@echo "  make test-e2e-integration - Run Playwright integration e2e tests (self-contained)"
	@echo "  make test-e2e-integration-local - Run Playwright integration e2e tests (needs loom serve)"
	@echo "  make test-e2e-integration-full - Run ALL integration e2e tests (cross-workspace + terminal regression)"
	@echo "  make install    - Install loom to GOPATH/bin"
	@echo "  make frontend         - DEPRECATED alias for make build-frontend"
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
