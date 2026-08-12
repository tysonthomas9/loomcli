# Makefile for loomcli project

.PHONY: all build build-frontend build-all test test-builtin-workflows test-characterization test-phase6-parity check-supervisor-disabled test-supervisor-disabled test-integration test-all test-playground test-fleetdb-embedded test-fleetdb-ui test-fleetdb-empty-cli fleetdb-empty-up fleetdb-empty-down local-mode-frontend-dist local-mode-info local-mode-up local-mode-codex-up local-mode-codex-workflows-up local-mode-workflow-build-check local-mode-daytona-build-check local-mode-claude-up local-mode-daytona-up local-mode-down local-mode-logs local-mode-verify local-mode-codex-verify test-local-mode-harness test-distributed-smoke lint lint-frontend test-frontend e2e test-e2e-api test-e2e-api-local test-e2e-real-smoke test-e2e-real-smoke-local test-e2e-real-regression test-e2e-real-regression-local test-e2e-integration test-e2e-integration-local test-e2e-integration-full test-e2e-daytona-broker clean install help frontend check check-go check-frontend check-fleetdb-binary gate gate-e2e gate-e2e-full hooks ensure-hooks dev dev-check dev-loom dev-vite check-loc check-loc-stale check-control-plane-paths check-architecture check-architecture-memory check-no-raw-exec check-no-beads-prod test-coverage test-forkwatch test-frontend-coverage test-race-cover test-integration-race-cover gen-go-api check-go-api-staleness local-mode-webhook-verify test-e2e-github-webhook test-e2e-github-webhook-live

# Default target
all: build

_LOCAL_MODE_SOURCE_ROOT_ORIGIN := $(origin LOCAL_MODE_SOURCE_ROOT)
LOCAL_MODE_SOURCE_ROOT ?= $(shell pwd -P)
# Hash either the physical working directory or an environment override
# without interpolating the path into shell syntax. GNU Make 3.81 does not put
# command-line/file assignments in the environment used by $(shell ...), so
# those less common override forms must provide the identity as a pair.
ifeq ($(_LOCAL_MODE_SOURCE_ROOT_ORIGIN),undefined)
LOCAL_MODE_CHECKOUT_ID ?= $(shell pwd -P | git hash-object --stdin | cut -c1-12)
else ifneq ($(filter environment environment override,$(_LOCAL_MODE_SOURCE_ROOT_ORIGIN)),)
LOCAL_MODE_CHECKOUT_ID ?= $(shell printf '%s\n' "$$LOCAL_MODE_SOURCE_ROOT" | git hash-object --stdin | cut -c1-12)
else
ifeq ($(filter command line override file,$(origin LOCAL_MODE_CHECKOUT_ID)),)
$(error LOCAL_MODE_SOURCE_ROOT passed after make requires LOCAL_MODE_CHECKOUT_ID; prefer setting LOCAL_MODE_SOURCE_ROOT in the environment before make)
endif
endif
LOCAL_MODE_COMPOSE_PROJECT ?= loomcli-local-mode-$(LOCAL_MODE_CHECKOUT_ID)
LOCAL_MODE_RUN_ID ?= $(shell sh -c 'printf "%s-%s\n" "$$(date -u +%Y%m%dT%H%M%SZ)" "$$$$"')
LOCAL_MODE_SOURCE_ROOT := $(LOCAL_MODE_SOURCE_ROOT)
LOCAL_MODE_CHECKOUT_ID := $(LOCAL_MODE_CHECKOUT_ID)
LOCAL_MODE_COMPOSE_PROJECT := $(LOCAL_MODE_COMPOSE_PROJECT)
LOCAL_MODE_RUN_ID := $(LOCAL_MODE_RUN_ID)
LOCAL_MODE_COMPOSE ?=
LOCAL_MODE_COMPOSE_FILES ?=
LOCAL_MODE_COMPOSE_UP_FLAGS ?= --build
# This checked-in credential is intentionally deterministic so the disposable
# local-mode Compose profile and its host-side verification scripts agree. It
# is an admin credential for this test stack only; never reuse it in a shared
# or production FleetDB deployment.
LOCAL_MODE_FLEETDB_API_KEY ?= loom-local-mode-test-only-admin-key-v1
LOCAL_MODE_FLEETDB_ADMIN_ACTOR ?= local-mode-harness@fixture.local
# The supervisor-disabled proof owns an isolated host-port tuple so its clean
# environment cannot collide with the normal 828x dogfood stack.
SUPERVISOR_DISABLED_FLEETDB_PORT ?= 8380
SUPERVISOR_DISABLED_API_PORT ?= 8382
SUPERVISOR_DISABLED_UI_PORT ?= 8383
# Override this when validating a paired FleetDB feature worktree. The default
# preserves the long-standing sibling-repository layout.
LOCAL_MODE_FLEETDB_SOURCE_ROOT ?= $(abspath test/local-mode/../../../fleet-db)
FLUE_SRC ?= $(abspath test/local-mode/../../../../dynamic-workflows/flue)
# The local-mode image is Debian/glibc and normally uses the host CPU
# architecture. Override this only when Compose is targeting another CPU.
LOCAL_MODE_CONTAINER_ARCH ?= $(shell uname -m)
LOCAL_MODE_FLEETDB_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-fleet-db:latest
LOCAL_MODE_LOOM_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom:latest
LOCAL_MODE_LOOM_CODEX_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom-codex:latest
LOCAL_MODE_LOOM_CLAUDE_IMAGE ?= $(LOCAL_MODE_COMPOSE_PROJECT)-loom-claude:latest
LOCAL_MODE_COMPOSE_EXTRA := $(foreach file,$(LOCAL_MODE_COMPOSE_FILES),-f $(file))
LOCAL_MODE_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_CODEX_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.codex.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_CODEX_WORKFLOW_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.codex.yml -f test/local-mode/docker-compose.workflow-build.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_CLAUDE_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.claude.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_DAYTONA_COMPOSE_ARGS = -p $(LOCAL_MODE_COMPOSE_PROJECT) -f test/local-mode/docker-compose.yml -f test/local-mode/docker-compose.workflow-build.yml -f test/local-mode/docker-compose.daytona.yml $(LOCAL_MODE_COMPOSE_EXTRA)
LOCAL_MODE_WORKFLOW_BUILD_CHECK = $(if $(filter %docker-compose.workflow-build.yml,$(LOCAL_MODE_COMPOSE_FILES)),local-mode-workflow-build-check)
export LOCAL_MODE_FLEETDB_IMAGE
export LOCAL_MODE_LOOM_IMAGE
export LOCAL_MODE_LOOM_CODEX_IMAGE
export LOCAL_MODE_LOOM_CLAUDE_IMAGE
export LOCAL_MODE_SOURCE_ROOT
export LOCAL_MODE_CHECKOUT_ID
export LOCAL_MODE_COMPOSE_PROJECT
export LOCAL_MODE_RUN_ID
export LOCAL_MODE_EXPECTED_BACKEND
export LOCAL_MODE_FLEETDB_API_KEY
export LOCAL_MODE_FLEETDB_ADMIN_ACTOR
export LOCAL_MODE_FLEETDB_SOURCE_ROOT
export FLUE_SRC
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

# Run the builtin workflow node unit tests (internal/infra/workflowdistribution/builtin/*.test.mjs).
# Deliberately NOT folded into check-go (that gate is Go-only + decoupling-smoke
# tested); run it standalone here and in the builtin-bundle-pin CI job. Needs ../flue.
test-builtin-workflows:
	@echo "Running builtin workflow node tests..."
	@./scripts/test-builtin-workflows.sh

# Phase 1 migration characterization gate. The runner validates the manifest,
# checks that every regex still selects the exact pinned tests, then runs them.
test-characterization:
	@go run ./test/modular-monolith/characterization

# Phase 6 exact named-test matrix. The shared runner rejects missing, renamed,
# extra, or disabled rows before executing any test.
test-phase6-parity:
	@go run ./test/modular-monolith/characterization --manifest test/modular-monolith/phase6-parity-matrix.yaml

# Phase 6 supervisor-disabled execution contract. Validation is provisioning-free;
# the full target runs the deterministic Compose row plus exact parity matrix and
# always tears the stack down.
check-supervisor-disabled:
	@go run ./scripts/supervisordisabled --manifest test/modular-monolith/supervisor-disabled-matrix.yaml --validate

test-supervisor-disabled:
	@go run ./scripts/supervisordisabled \
		--manifest test/modular-monolith/supervisor-disabled-matrix.yaml \
		--fleetdb-source-root "$(LOCAL_MODE_FLEETDB_SOURCE_ROOT)" \
		--fleetdb-port "$(SUPERVISOR_DISABLED_FLEETDB_PORT)" \
		--api-port "$(SUPERVISOR_DISABLED_API_PORT)" \
		--ui-port "$(SUPERVISOR_DISABLED_UI_PORT)"

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

# Start the local-mode dogfood stack: fleet-db, loom serve, a deterministic
# planner/coder backend, and the Web UI. Trigger-driven prompt agents are the
# only execution plane.
local-mode-frontend-dist:
	@if [ ! -f "$(FRONTEND_DIR)/dist/index.html" ]; then \
	  echo "Web UI dist is missing; building it once on the host..."; \
	  $(MAKE) build-frontend; \
	fi

local-mode-info:
	@echo "source_root=$(LOCAL_MODE_SOURCE_ROOT)"
	@echo "checkout_id=$(LOCAL_MODE_CHECKOUT_ID)"
	@echo "compose_project=$(LOCAL_MODE_COMPOSE_PROJECT)"

local-mode-up: local-mode-frontend-dist $(LOCAL_MODE_WORKFLOW_BUILD_CHECK)
	@echo "Starting local-mode dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-codex-up: local-mode-frontend-dist
	@echo "Starting local-mode Codex dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_CODEX_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

# Full unified-agent authoring profile: Codex plus the pinned local Flue
# toolchain needed to materialize embedded prompt/scripted workflow drivers.
local-mode-workflow-build-check:
	@set -e; \
	missing=""; \
	for rel in packages/cli/bin/flue.mjs packages/cli/dist/flue.js packages/runtime/package.json packages/runtime/dist/node/index.mjs packages/runtime/node_modules/@hono/node-server packages/runtime/node_modules/hono; do \
	  if [ ! -e "$(FLUE_SRC)/$$rel" ]; then missing="$$missing $$rel"; fi; \
	done; \
	if [ "$$missing" != "" ]; then \
	  echo "Flue workflow build toolchain is incomplete at $(FLUE_SRC)." >&2; \
	  echo "Missing:$$missing" >&2; \
	  echo "Built-in workflow sources require the Flue CLI/runtime." >&2; \
	  echo "From the pinned Flue checkout, run:" >&2; \
	  echo "  pnpm install --frozen-lockfile --force --filter @flue/cli... --filter @flue/runtime..." >&2; \
	  echo "  pnpm build" >&2; \
	  echo "Or set FLUE_SRC=/path/to/flue with an already prepared checkout." >&2; \
	  exit 1; \
	fi; \
	container_arch="$(LOCAL_MODE_CONTAINER_ARCH)"; \
	case "$$container_arch" in \
	  arm64|aarch64) pnpm_cpu="arm64" ;; \
	  amd64|x86_64) pnpm_cpu="x64" ;; \
	  *) \
	    echo "Unsupported local-mode container architecture '$$container_arch'." >&2; \
	    echo "Set LOCAL_MODE_CONTAINER_ARCH to arm64/aarch64 or amd64/x86_64 to match the Compose target." >&2; \
	    exit 1 ;; \
	esac; \
	binding="linux-$$pnpm_cpu-gnu"; \
	rolldown_count=0; \
	missing_binding=""; \
	for rolldown_pkg in "$(FLUE_SRC)"/node_modules/.pnpm/rolldown@*/node_modules/rolldown; do \
	  if [ ! -d "$$rolldown_pkg" ]; then continue; fi; \
	  rolldown_count=$$((rolldown_count + 1)); \
	  dep_root="$${rolldown_pkg%/rolldown}"; \
	  candidate="$$dep_root/@rolldown/binding-$$binding/rolldown-binding.$$binding.node"; \
	  if [ ! -f "$$candidate" ]; then missing_binding="$$candidate"; break; fi; \
	done; \
	if [ "$$rolldown_count" -eq 0 ] || [ "$$missing_binding" != "" ]; then \
	  echo "Flue cannot load Rolldown inside the Linux/$$pnpm_cpu/glibc local-mode container." >&2; \
	  echo "Missing @rolldown/binding-$$binding (rolldown-binding.$$binding.node) under $(FLUE_SRC)/node_modules/.pnpm." >&2; \
	  echo "pnpm likely installed optional native dependencies only for the host platform." >&2; \
	  echo "From the pinned Flue checkout, install both current-host and Linux-container dependencies:" >&2; \
	  echo "  export XDG_CONFIG_HOME=\"\$${TMPDIR:-/tmp}/loom-flue-pnpm-$$binding\"" >&2; \
	  echo "  pnpm config set --global supportedArchitectures '{\"os\":[\"current\",\"linux\"],\"cpu\":[\"current\",\"$$pnpm_cpu\"],\"libc\":[\"current\",\"glibc\"]}'" >&2; \
	  echo "  pnpm install --frozen-lockfile --force --filter @flue/cli... --filter @flue/runtime... --filter hello-world..." >&2; \
	  echo "Then rerun the selected local-mode target." >&2; \
	  exit 1; \
	fi; \
	expected="$$(tr -d '[:space:]' < internal/infra/workflowdistribution/FLUE_COMMIT)"; \
	actual="$$(git -C "$(FLUE_SRC)" rev-parse HEAD 2>/dev/null || true)"; \
	if [ "$$actual" != "$$expected" ]; then \
	  echo "Flue checkout $$actual does not match Loom's pinned commit $$expected." >&2; \
	  echo "Check out the pin at $(FLUE_SRC), or set FLUE_SRC to a matching checkout." >&2; \
	  exit 1; \
	fi

local-mode-codex-workflows-up: local-mode-frontend-dist local-mode-workflow-build-check
	@echo "Starting local-mode Codex + workflow-authoring stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_CODEX_WORKFLOW_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-claude-up: local-mode-frontend-dist
	@echo "Starting local-mode Claude dogfood stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_CLAUDE_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-daytona-build-check: local-mode-workflow-build-check
	@if [ ! -f "$(FLUE_SRC)/node_modules/.pnpm/node_modules/@daytona/sdk/package.json" ]; then \
	  echo "Daytona SDK is missing from the pinned Flue checkout at $(FLUE_SRC)." >&2; \
	  echo "Run pnpm install in that checkout before starting the Daytona profile." >&2; \
	  exit 1; \
	fi

# Starts the real host-broker Daytona profile. Provider credentials are never
# accepted through Compose env; save Daytona (and GitHub for PR delivery) in
# Settings after startup so loom serve seals them in its local credential vault.
local-mode-daytona-up: local-mode-frontend-dist local-mode-daytona-build-check
	@echo "Starting local-mode Daytona host-broker stack ($(LOCAL_MODE_COMPOSE_PROJECT)) on http://localhost:$${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban..."
	@echo "After startup, save provider credentials in Settings; they are resolved only by loom serve."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_DAYTONA_COMPOSE_ARGS) up $(LOCAL_MODE_COMPOSE_UP_FLAGS)

local-mode-down:
	@echo "Removing local-mode project $(LOCAL_MODE_COMPOSE_PROJECT), including its named volumes..."
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) down -v --remove-orphans

local-mode-logs:
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) logs -f loom-local ui-local

local-mode-verify:
	@set -e; \
	$(LOCAL_MODE_COMPOSE_SELECT); \
	manifest="$$( $$compose $(LOCAL_MODE_COMPOSE_ARGS) exec -T loom-local sh -c 'manifest="$${LOCAL_MODE_RUN_MANIFEST:-/tmp/loom-local-mode-run.json}"; attempt=0; while [ ! -s "$$manifest" ]; do attempt=$$((attempt + 1)); if [ "$$attempt" -ge 120 ]; then echo "timed out waiting for local-mode run manifest: $$manifest" >&2; exit 1; fi; sleep 1; done; cat "$$manifest"' )"; \
	LOCAL_MODE_RUN_MANIFEST_JSON="$$manifest" test/local-mode/verify-local-mode.sh; \
	$$compose $(LOCAL_MODE_COMPOSE_ARGS) exec -T loom-local verify-supervisor-disabled

# Verify role-based task routing for UI-registered plan/task agents against a
# running stack: seeds a no-design task (must go to the plan agent) and a
# designed task (must go to the task agent), exercises the UI POST /agents
# endpoint, and asserts the claims through the default Execution-owned worker.
local-mode-routing-verify:
	@python3 test/local-mode/verify-agent-routing.py

# Real-stack E2E for the trigger-driven GitHub webhook path: signs a
# pull_request.opened delivery, asserts the durable TriggerEvent/Delivery/
# DriverRun records, and checks redelivery is idempotent. Requires a running
# `make local-mode-up` stack plus curl, openssl, and python3.
local-mode-webhook-verify:
	@test/local-mode/verify-webhook.sh

local-mode-codex-verify:
	@$(MAKE) --no-print-directory LOCAL_MODE_EXPECTED_BACKEND=codex local-mode-verify

test-local-mode-harness: local-mode-verify

# Run the fleet-db distributed smoke stack: shared fleet-db/Redis, two loom
# serve processes, two runtime worker-heartbeat loops, and a one-shot smoke
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
test-frontend-coverage:
	@echo "Running frontend tests with coverage..."
	@cd $(FRONTEND_DIR) && npm run test:coverage

# Check Go file LOC limits
check-loc:
	@./scripts/check-loc.sh 1000 2500

check-control-plane-paths:
	@./scripts/check-control-plane-paths.sh

check-architecture:
	@case "$$(go env GOOS)" in \
		darwin|linux) go run ./scripts/rsswatch $(ARCHCHECK_RSS_LIMIT_MIB) $(ARCHCHECK_RSS_TIMEOUT_SECONDS) go run ./scripts/archcheck check ;; \
		*) go run ./scripts/archcheck check ;; \
	esac

ARCHCHECK_RSS_LIMIT_MIB ?= 2048
ARCHCHECK_RSS_TIMEOUT_SECONDS ?= 1200

check-architecture-memory:
	@$(MAKE) check-architecture

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
	@echo "Generated: internal/platform/loomapi/gen/types.gen.go"

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
e2e: test-e2e

test-e2e:
	@echo "Running Playwright e2e tests (mocked)..."
	@cd $(FRONTEND_DIR) && npx playwright install --with-deps chromium 2>/dev/null || true
	@cd $(FRONTEND_DIR) && npx playwright test --project=chromium --workers=1

# Run Playwright API e2e tests (self-contained: builds loom, starts server, runs tests)
test-e2e-api:
	@echo "Running Playwright API e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=api

# Run Playwright API e2e tests against already-running loom serve
test-e2e-api-local:
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

# Provisions a real Daytona sandbox and may use a paid Codex backend. The test
# harness seals DAYTONA_API_KEY into a temporary Loom vault before exercising
# the host broker; the raw credential is not forwarded to the workflow process.
test-e2e-daytona-broker: local-mode-daytona-build-check
	@: "$${DAYTONA_API_KEY:?set DAYTONA_API_KEY for the opt-in live test}"
	@echo "Running paid external-service Daytona host-broker e2e..."
	@LOOM_DAYTONA_BROKER_E2E=1 \
	  LOOM_FLUE_RUNTIME_ROOT="$(FLUE_SRC)/packages/runtime" \
	  LOOM_SDK_ROOT="$(CURDIR)/sdk" \
	  DAYTONA_SDK_ROOT="$(FLUE_SRC)/node_modules/.pnpm/node_modules/@daytona/sdk" \
	  LOOM_REAL_FLUE_CMD_JSON='["node","$(FLUE_SRC)/packages/cli/bin/flue.mjs"]' \
	  GOCACHE=$${GOCACHE:-/tmp/go-build-cache} \
	  go test -count=1 -tags e2e -run TestE2EDaytonaProviderBroker ./internal/cli/serve/serveadapter -timeout 15m

# Compile + run the real GitHub stacked-PR publisher e2e (initial publish / re-run /
# drop-a-unit / reorder). The test is //go:build e2e tagged, so this target is also the
# compile guard against bit-rot. Skips unless gated:
#   LOOM_STACK_E2E=1 LOOM_STACK_E2E_REPO=owner/name (+ gh auth) make test-e2e-stackpublish
test-e2e-stackpublish:
	@echo "Running stacked-PR publisher e2e (skips unless LOOM_STACK_E2E is set)..."
	@GOCACHE=$${GOCACHE:-/tmp/go-build-cache} go test -count=1 -tags e2e -run TestE2EStackPublisher ./internal/stackpublish -timeout 10m

# Run the real Playwright smoke suite: browser + API contracts against FleetDB-backed loom serve.
test-e2e-real-smoke:
	@echo "Running real Playwright smoke tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration-smoke --project=api-smoke

# Run the real Playwright smoke suite against an already-running loom serve/UI.
test-e2e-real-smoke-local:
	@echo "Running real Playwright smoke tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration-smoke --project=api-smoke

# Run the real Playwright regression suite: slower browser + API contracts.
test-e2e-real-regression:
	@echo "Running real Playwright regression tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration-regression --project=api-regression

# Run the real Playwright regression suite against an already-running loom serve/UI.
test-e2e-real-regression-local:
	@echo "Running real Playwright regression tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration-regression --project=api-regression

# Run Playwright integration e2e tests (self-contained, starts loom serve automatically)
test-e2e-integration:
	@echo "Running Playwright integration e2e tests (self-contained)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration

# Run Playwright integration e2e tests against local loom serve
test-e2e-integration-local:
	@echo "Running Playwright integration e2e tests (local server)..."
	@cd $(FRONTEND_DIR) && RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 npx playwright test --project=integration

# Run ALL Playwright integration e2e tests including cross-workspace and terminal-fleetdb-regression
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

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f loom
	rm -f /tmp/loom.coverage.out

# Frontend directory
FRONTEND_DIR := internal/webui/frontend
LOCAL_FLEET_DB_REPO := $(firstword $(wildcard $(CURDIR)/../fleet-db $(CURDIR)/../../fleet-db))
FLEET_DB_REPO ?= $(if $(LOCAL_FLEET_DB_REPO),$(LOCAL_FLEET_DB_REPO),../../fleet-db)
LOCAL_FLEET_DB_BIN := $(firstword $(wildcard $(FLEET_DB_REPO)/bin/fleet-db $(FLEET_DB_REPO)/fleet-db))
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

# Go-only quality gate (no Node, no frontend dist). Two package workers overlap
# the longest independent race suites; callers can set this to 1 if needed.
GATE_GO_TEST_PARALLELISM ?= 2

check-fleetdb-binary:
	@fleet_bin="$${FLEET_DB_BIN:-}"; \
	if [ -z "$$fleet_bin" ]; then fleet_bin=$$(command -v fleet-db 2>/dev/null || true); fi; \
	if [ -n "$$fleet_bin" ]; then \
		help_output=$$("$$fleet_bin" --help 2>&1 || true); \
		if ! printf '%s\n' "$$help_output" | grep -q -- 'Usage of fleet-db' || \
		   ! printf '%s\n' "$$help_output" | grep -Eq '^[[:space:]]+--?auth-bootstrap-admin-actor([[:space:]]|$$)'; then \
			echo "Error: fleet-db binary $$fleet_bin is incompatible with this Loom checkout (missing auth-bootstrap-admin-actor)." >&2; \
			echo "Build the paired fleet-db checkout and set FLEET_DB_BIN=/path/to/fleet-db." >&2; \
			exit 1; \
		fi; \
	fi

CHECK_GO_SKIP_ARCHITECTURE ?= 0

check-go:
	@echo "=== [1/16] Go: FleetDB compatibility + format check ==="
	@$(MAKE) check-fleetdb-binary
	@bad=$$(gofmt -l . 2>/dev/null | grep -v third_party | grep -v worktrees | grep -v vendor | grep -v node_modules | head -20); \
	if [ -n "$$bad" ]; then echo "gofmt violations:"; echo "$$bad"; exit 1; fi
	@echo "=== [2/16] Go: vet ==="
	@go vet ./...
	@echo "=== [3/16] Go: build ==="
	@go build -buildvcs=false ./...
	@echo "=== [4/16] Go: lint (golangci-lint + depguard + control-plane path guard) ==="
	@golangci-lint run --timeout=5m --allow-parallel-runners
	@./scripts/check-control-plane-paths.sh
	@echo "=== [5/16] Go: LOC check ==="
	@./scripts/check-loc.sh 1000 2500
	@echo "=== [6/16] Go: package size check ==="
	@./scripts/check-package-size.sh 25
	@echo "=== [7/16] Go: import fanout check ==="
	@./scripts/check-import-fanout.sh 18
	@echo "=== [8/16] Go: modular-monolith architecture guard ==="
	@if [ "$(CHECK_GO_SKIP_ARCHITECTURE)" = "1" ]; then \
		echo "Architecture guard is owned by the separate measured CI job."; \
	else \
		$(MAKE) check-architecture; \
	fi
	@echo "=== [9/16] Go: modular-monolith characterization gate ==="
	@$(MAKE) test-characterization
	@echo "=== [10/16] Go: supervisor-disabled contract validation ==="
	@$(MAKE) check-supervisor-disabled
	@echo "=== [11/16] Go: exec.Command guard ==="
	@./scripts/check-no-raw-exec.sh
	@echo "=== [12/16] Go: log.Printf guard ==="
	@./scripts/check-no-log-printf.sh
	@echo "=== [13/16] Go: no new production beads/bd references ==="
	@./scripts/check-no-beads-prod.sh
	@echo "=== [14/16] Go: generated API staleness ==="
	@./scripts/check-go-api-staleness.sh
# The production architecture pass above, or CI's separate measured job, runs
# the full repository scan and enforces the exact checked-in snapshot. Keep
# focused archtest coverage in the race pass, but do not repeat the two
# repository-scale scans already enforced by that measured transaction.
	@echo "=== [15/16] Go: test with race detector ==="
	@set -e; coverage_profile=; \
	cleanup_coverage() { \
		rc=$$?; \
		trap - EXIT HUP INT TERM; \
		if [ -n "$$coverage_profile" ]; then rm -f "$$coverage_profile"; fi; \
		exit "$$rc"; \
	}; \
	trap cleanup_coverage EXIT; \
	trap 'exit 129' HUP; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	coverage_profile=$$(mktemp "$${TMPDIR:-/tmp}/loom-coverage.XXXXXX"); \
	./scripts/with-clean-loom-env.sh go test -p $(GATE_GO_TEST_PARALLELISM) -race -covermode=atomic \
		-coverprofile="$$coverage_profile" \
		-skip '^(TestCheckedInManifestsAndRepository|TestPhase5AgentsOwnershipBlockerRatchet)$$' \
		-timeout 15m ./...; \
	echo "=== [16/16] Go: coverage threshold ==="; \
	COVERAGE_PROFILE="$$coverage_profile" COVERAGE_THRESHOLD=60 ./scripts/check-coverage.sh
	@echo "=== Go quality gates PASSED ==="

# Frontend-only quality gate (no Go toolchain, no dist prerequisite)
VITEST_MAX_WORKERS ?= auto

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
	@cd $(FRONTEND_DIR) && workers='$(VITEST_MAX_WORKERS)'; \
	if [ "$$workers" = auto ]; then \
		workers=$$(node -e 'const os = require("node:os"); const n = typeof os.availableParallelism === "function" ? os.availableParallelism() : os.cpus().length; process.stdout.write(String(Math.max(1, Math.min(4, n - 1))))'); \
	fi; \
	VITEST_MAX_WORKERS="$$workers" npm run test:coverage
	@echo "=== Frontend quality gates PASSED ==="

# Unified quality gate — runs Go + frontend checks in parallel
check:
	@echo "=== Running Go and Frontend checks in parallel ==="
	@set -e; go_log=; fe_log=; go_pid=; fe_pid=; \
	cleanup() { \
		rc=$$?; \
		trap - EXIT HUP INT TERM; \
		if [ -n "$$go_pid" ]; then kill "$$go_pid" 2>/dev/null || true; wait "$$go_pid" 2>/dev/null || true; fi; \
		if [ -n "$$fe_pid" ]; then kill "$$fe_pid" 2>/dev/null || true; wait "$$fe_pid" 2>/dev/null || true; fi; \
		if [ -n "$$go_log" ]; then rm -f "$$go_log"; fi; \
		if [ -n "$$fe_log" ]; then rm -f "$$fe_log"; fi; \
		exit "$$rc"; \
	}; \
	trap cleanup EXIT; \
	trap 'exit 129' HUP; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	go_log=$$(mktemp); fe_log=$$(mktemp); \
	$(MAKE) check-go >"$$go_log" 2>&1 & go_pid=$$!; \
	$(MAKE) check-frontend >"$$fe_log" 2>&1 & fe_pid=$$!; \
	go_rc=0; fe_rc=0; \
	wait "$$go_pid" || go_rc=$$?; go_pid=; \
	wait "$$fe_pid" || fe_rc=$$?; fe_pid=; \
	if [ $$go_rc -ne 0 ] || [ $$fe_rc -ne 0 ]; then \
		if [ $$go_rc -ne 0 ]; then \
			echo ""; echo "━━━ Go output (FAILED) ━━━"; cat "$$go_log"; \
		fi; \
		if [ $$fe_rc -ne 0 ]; then \
			echo ""; echo "━━━ Frontend output (FAILED) ━━━"; cat "$$fe_log"; \
		fi; \
		exit 1; \
	fi; \
	echo "=== Go quality gates PASSED ==="; \
	echo "=== Frontend quality gates PASSED ==="
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
	@echo "  make test-characterization - Run the Phase 1 modular-monolith characterization matrix"
	@echo "  make check-supervisor-disabled - Validate the Phase 4 supervisor-disabled matrix without provisioning"
	@echo "  make test-supervisor-disabled - Run the deterministic supervisor-disabled execution proof"
	@echo "  make test-frontend-coverage - Run frontend tests with coverage threshold"
	@echo "  make test-forkwatch    - Run tests under a fork-bomb/process-leak watchdog (PKG=./path/...)"
	@echo "  make check-no-raw-exec - Check for raw exec.Command in unit tests"
	@echo "  make check-control-plane-paths - Check local/cloud fleet-db runtime path invariants"
	@echo "  make check-architecture - Check modular-monolith manifests and coupling ratchets"
	@echo "  make check-architecture-memory - Enforce the archtest process-tree RSS ceiling"
	@echo "  make check-loc-stale   - Check for stale LOC allowlist entries"
	@echo "  make lint              - Run Go linter (golangci-lint)"
	@echo "  make lint-frontend     - Run frontend typecheck + ESLint"
	@echo "  make test-frontend     - Run frontend unit tests (vitest)"
	@echo "  make test-e2e          - Run Playwright mocked e2e tests (no server)"
	@echo "  make test-fleetdb-ui   - Run fleet-db-only UI regression suite"
	@echo "  make local-mode-up      - Run local-mode Podman/Docker stack"
	@echo "  make local-mode-codex-up - Run local-mode stack with Codex agents"
	@echo "  make local-mode-codex-workflows-up - Run Codex stack with prompt/scripted workflow authoring"
	@echo "  make local-mode-verify  - Verify deterministic local-mode stack"
	@echo "  make local-mode-codex-verify - Verify Codex local-mode stack"
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
