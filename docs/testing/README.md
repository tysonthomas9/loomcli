# Loom CLI Test Documentation

> **Status:** Current · *audited 2026-08-03*

Index for the testing docs. Start with
[../testing-terminology.md](../testing-terminology.md) if you are about to run
something slow or irreversible — `AGENTS.md` requires a terminology handshake
first, and this repo overloads `local`, `live`, `real`, `verify` and `gate`.

## Quick Reference

| Layer | Framework | Where | Run Command |
|-------|-----------|-------|-------------|
| Go unit/integration | `go test` | `*_test.go` under `internal/` | `make test` or `./scripts/test.sh` |
| Go e2e (`e2e` build tag) | `go test -tags=e2e` | 12 `*_e2e_test.go` files | `make test-all` |
| Frontend unit | Vitest + React Testing Library | `src/**/*.test.ts(x)`, mostly under `__tests__/` | `cd internal/webui/frontend && npm run test:unit` |
| Frontend E2E (mocked) | Playwright | `tests/e2e/*.spec.ts` | `make test-e2e` |
| Frontend API contracts | Playwright | `tests/e2e/api/*.api.spec.ts` | `make test-e2e-api` |
| Frontend integration (real backend) | Playwright | `tests/e2e/integration/*.integration.spec.ts` | `make test-e2e-integration` |

File counts are deliberately omitted — they rot on every commit. Count them
yourself:

```bash
find internal -name '*_test.go' | wc -l
find internal/webui/frontend/src -name '*.test.ts*' | wc -l
ls internal/webui/frontend/tests/e2e/*.spec.ts | wc -l
```

## Documents

- **[Testing terminology](../testing-terminology.md)** - the four axes (depth / realness / provisioning / polarity), the trap words, the handshake protocol, and the harness directory map
- **[Go Backend Tests](go-backend-tests.md)** - current Go test surfaces and FleetDB-focused backend gates
- **[Frontend Tests](frontend-tests.md)** - Vitest and Playwright layout, entry points, fixtures, page objects
- **[Test Infrastructure](test-infrastructure.md)** - CI/CD, scripts, Makefile targets, configuration files, coverage thresholds
- **[Test Patterns & Conventions](test-patterns.md)** - common patterns, mocking strategies, helpers, anti-patterns
- **[Fleet-DB acceptance gates](fleetdb-acceptance-gates.md)** - the named gates G0-G8 and which command runs each today
- **[Real-stack Playwright coverage](dogfood-playwright-coverage.md)** - what the real-stack smoke/regression suites cover and how a spec gets promoted into them

### Manual E2E plans

Reproducible end-to-end plans for the fleet-db-backed architecture. Designed to be runnable by an agent or by hand.

- **[E2E preflight](e2e-preflight.md)** - shared setup (binaries, podman Redis, fleet-db subprocess, env vars, runner conventions, cleanup)
- **[E2E CLI + curl](e2e-cli.md)** - CLI noun-verb commands, failure modes, embedded mode, direct fleet-db API, multi-workspace isolation
- **[E2E Web UI](e2e-ui.md)** - multi-workspace lifecycle via agent-browser
- **[Local Mode Podman E2E](local-mode-podman-e2e.md)** - one-command Podman stack for FleetDB-backed local planner/coder dogfood runs, including the Codex CLI variant
- **[Known issues](known-issues.md)** - closed expected-failures with their regression guards, plus test-methodology pitfalls

### Status snapshots

- **[Coverage gaps](coverage-gaps.md)** - point-in-time gap analysis from 2026-02-11; historical, most paths predate the `internal/webui` split

### Harness directories (each has its own README)

- `test/local-mode/README.md` - full-stack dogfood compose stacks
- `test/playground/README.md` - daemon-lifecycle failure-mode harness
- `test/fleetdb/README.md` - empty new-user FleetDB UI stack
- `test/distributed/README.md` - distributed fleet-db smoke
- `e2e/README.md` - Alpine E2E container (loom + Chromium + Playwright + agent-browser + stub CLIs)
- `.agent-skills/loom-pr-test/SKILL.md` - real Loom PR runtime testing runbook

## Running Tests

### Go Tests

```bash
# Full suite with coverage
make test

# With race detection (used in CI)
go test -race ./...

# Quick (skip slow tests)
go test -short ./...

# Specific package
go test -v ./internal/cli/...

# Specific test
TEST_RUN="TestAutomode" ./scripts/test.sh

# E2E tests (requires tmux)
go test -tags=e2e ./...

# The only two benchmarks in the repo (untagged)
go test -bench=. ./internal/types
```

### Frontend Tests

```bash
cd internal/webui/frontend

# All tests (unit + E2E)
npm test

# Unit tests only
npm run test:unit

# Unit tests in watch mode
npm run test:watch

# Unit tests with coverage (what check-frontend runs)
npm run test:coverage

# E2E tests
npm run test:e2e

# E2E with browser visible
npm run test:e2e:headed

# E2E with debug UI
npm run test:e2e:ui

# Visual regression tests
npm run test:visual

# Update visual snapshots
npm run test:visual:update
```

Real-backend runs are driven from the repo root. The first two are exact
aliases for the npm scripts above (`Makefile:387`, `:437`); the smoke and
regression suites have **no** npm equivalent and exist only as make targets.
Playwright itself starts the server in every one of them, via its `webServer`
config (`playwright.config.ts:70-95`) — see
[test-infrastructure.md](test-infrastructure.md):

```bash
make test-e2e-api            # = npm run test:e2e:api
make test-e2e-integration    # = npm run test:e2e:integration
make test-e2e-real-smoke     # tagged @smoke browser + API (no npm script)
make test-e2e-real-regression
```

### Quality Gate (Pre-push)

```bash
# Standard gate (Go + frontend checks in parallel)
make gate

# Gate + real Playwright smoke suite
make gate-e2e

# Gate + container E2E tests
make gate-e2e-full
```

`gate` is an alias for `check` (`Makefile:578`). What it actually runs — 15 Go
steps and 6 frontend steps, including lint, LOC, package-size, import-fanout
and OpenAPI/docs-staleness guards — is documented in
[test-infrastructure.md](test-infrastructure.md).

## Test Coverage

Thresholds are owned by [test-infrastructure.md](test-infrastructure.md).
Short version: gate and CI enforce 60%, nightly enforces 70%.

- **Coverage tool**: Codecov (uploaded from CI)
- **Local coverage**: `TEST_COVER=1 ./scripts/test.sh`

## Architecture Overview

```
Tests
├── Go backend (go test)
│   ├── internal/cli/**             # CLI commands, agent, automode, daemon, workspace, monitor, serve
│   ├── internal/backend/**         # IssueBackend contract, fleet client
│   ├── internal/infra/fleetdb/     # FleetDB infrastructure
│   ├── internal/webui/             # 2 files at package root (auth_proxy, health_doctor)
│   ├── internal/webui/handlers/**  # HTTP handlers (issues, workflows, webhooks, …)
│   ├── internal/webui/service/     # service layer
│   ├── internal/webui/app/         # server + routes
│   ├── internal/webui/server/realtime/  # SSE
│   ├── internal/webui/log/         # log streaming
│   ├── internal/webui/localredis/  # embedded miniredis manager + snapshot
│   ├── internal/types/             # validation, ID generation (+ the 2 benchmarks)
│   ├── internal/kv/                # Redis scripts, stale detection, reconciler
│   ├── internal/rpc/               # protocol, client, auth, mutations, metrics
│   ├── internal/driver/            # workflow sandbox / trust placement
│   └── makefile_test.go            # meta-test for the build system
│
└── Frontend (Vitest + Playwright)
    ├── src/api/{common,issues,workspace,agents,terminal,workflows}/__tests__/
    ├── src/hooks/{common,issues,ui,workspace,agents,terminal}/__tests__/
    ├── src/components/<Component>/__tests__/
    ├── src/stores/__tests__/            # Zustand issue store (replaced useIssues)
    ├── src/utils/__tests__/
    ├── tests/e2e/*.spec.ts              # mocked browser specs
    ├── tests/e2e/api/*.api.spec.ts      # API contract specs
    └── tests/e2e/integration/*.integration.spec.ts   # real-backend specs
```

## Related

- [../README.md](../README.md) — index of the whole `docs/` tree
- [../testing-terminology.md](../testing-terminology.md)
- [../loom-glossary.md](../loom-glossary.md)
- `AGENTS.md` — agent runbooks and the terminology handshake
