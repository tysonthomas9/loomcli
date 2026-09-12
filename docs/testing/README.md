# Loom CLI Test Documentation

Comprehensive documentation of all tests in the loomcli project.

## Quick Reference

| Layer | Framework | Files | Run Command |
|-------|-----------|-------|-------------|
| Go Unit/Integration | `go test` | 95+ `*_test.go` (internal/) | `make test` or `./scripts/test.sh` |
| Frontend Unit | Vitest + React Testing Library | 100+ `.test.ts(x)` | `cd internal/webui/frontend && npm run test:unit` |
| Frontend E2E | Playwright | 65+ `.spec.ts` | `cd internal/webui/frontend && npm run test:e2e` |
| Frontend Integration | Playwright + Podman | 12+ `.spec.ts` | `RUN_INTEGRATION_TESTS=1 npm run test:e2e:integration` |
| Benchmarks | `go test -bench` | 15 files | `go test -bench=. -tags=bench ./...` |

## Documents

- **[Go Backend Tests](go-backend-tests.md)** - Current Go test surfaces and FleetDB-focused backend gates
- **[Frontend Tests](frontend-tests.md)** - Vitest unit tests, Playwright E2E tests, component tests, API tests
- **[Dogfood to Playwright Coverage](dogfood-playwright-coverage.md)** - Maps `dogfood-output/` findings to automated smoke/regression coverage and pending gaps
- **[Test Infrastructure](test-infrastructure.md)** - CI/CD, scripts, Makefile targets, configuration files, coverage
- **[Test Patterns & Conventions](test-patterns.md)** - Common patterns, mocking strategies, helpers, and best practices

### Manual E2E plans (loomcli ↔ fleet-db migration)

Reproducible end-to-end plans for the fleet-db-backed architecture. Designed to be runnable by an agent or by hand.

- **[E2E preflight](e2e-preflight.md)** - shared setup (binaries, podman Redis, fleet-db subprocess, env vars, runner conventions, cleanup)
- **[E2E CLI + curl](e2e-cli.md)** - CLI noun-verb commands, failure modes, embedded mode, direct fleet-db API, multi-workspace isolation
- **[E2E Web UI](e2e-ui.md)** - multi-workspace lifecycle via agent-browser (gated on Phase 4 of the migration)
- **[Local Mode Podman E2E](local-mode-podman-e2e.md)** - one-command Podman stack for FleetDB-backed local planner/coder dogfood runs, including the Codex CLI variant
- **[Known issues](known-issues.md)** - documented expected-failures + bug references + test-methodology pitfalls. Read before claiming a clean run
- **[Fleet-DB acceptance gates](fleetdb-acceptance-gates.md)** - named gates for backend/CLI, browser, SSE, workspace, supervisor, embedded local, remote distributed, and deletion lint checks

## Running Tests

### The quality gate is wall-capped

`make check` (the pre-push gate, `scripts/hooks/pre-push`) and `make check-go` run
under a wall-clock cap, so one gate invocation can never outlast the turn budget
or session it runs inside. Without it the run is unbounded: `-timeout 15m` is
go's *per-package* timeout, and a serial `-p 1` run over ~159 packages has no
total. An overrun that outlives its caller becomes an orphaned `go test -race`
tree competing for the machine with whatever starts next.

The cap resolves in `scripts/gate-timeout-seconds.sh`:

1. `LOOM_GATE_TIMEOUT_SECONDS`, if set to a non-negative integer, wins verbatim.
   **`0` disables the cap** — the escape hatch for a deliberate long local run.
2. else, if the loom supervisor exported `LOOM_RUN_TURN_TIMEOUT_SECONDS`,
   `min(1800, budget - 300)`, floored at 300.
3. else 1800 (30 minutes) — the default on developer machines and in CI, roughly
   2.7x a measured green CI `make check-go`.

When the cap fires, `scripts/with-timeout.sh` SIGTERMs the whole process group
(then SIGKILLs it after a grace period, `LOOM_GATE_TIMEOUT_KILL_GRACE_SECONDS`,
default 10s), prints a `[gate] TIMEOUT:` banner naming the cap, and exits **124**
— the coreutils `timeout` convention.

**Exit 124 means the clock ran out, not that a test failed.** Re-run with a
larger `LOOM_GATE_TIMEOUT_SECONDS`, or `LOOM_GATE_TIMEOUT_SECONDS=0` for no cap,
before reading it as a regression. A `[gate] ... already bounded by an outer
wall-time cap` note is normal: it means an inner wrapper deliberately did not arm
a second deadline.

`LOOM_GATE_TIMEOUT_ACTIVE` is **internal** and must never be set by hand —
setting it silently unwraps the entire gate. Use `LOOM_GATE_TIMEOUT_SECONDS=0`.

Tests for the wrapper: `./scripts/with-timeout_test.sh` (run manually, like the
other `scripts/*_test.sh`).

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

# Benchmarks
go test -bench=. -tags=bench ./...
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

# E2E tests
npm run test:e2e

# E2E with browser visible
npm run test:e2e:headed

# E2E with debug UI
npm run test:e2e:ui

# Integration tests (requires Podman Compose stack)
RUN_INTEGRATION_TESTS=1 npm run test:e2e:integration

# API tests against real backend
RUN_INTEGRATION_TESTS=1 npm run test:e2e:api

# Visual regression tests
npm run test:visual

# Update visual snapshots
npm run test:visual:update
```

### Quality Gate (Pre-push)

```bash
# Standard gate (Go + frontend checks)
make gate

# Gate + Playwright API e2e tests (no Docker required)
make gate-e2e

# Gate + API e2e + Docker container tests (requires Docker)
make gate-e2e-full
```

## Test Coverage

- **CI threshold**: 70% minimum (enforced via `scripts/check-coverage.sh`)
- **Coverage tool**: Codecov (uploaded from Ubuntu CI runs)
- **Local coverage**: `TEST_COVER=1 ./scripts/test.sh`

## Architecture Overview

```
Tests
├── Go Backend (go test)
│   ├── internal/cli/          # 40+ test files - CLI commands, daemon, backends
│   ├── internal/webui/        # 21 test files - HTTP handlers, SSE, auth, terminal, routes
│   ├── internal/webui/fleet/  # 5 test files - Fleet metrics, auth, signing
│   ├── internal/webui/daemon/ # 5 test files - Connection pool, discovery, circuit breaker
│   ├── internal/types/        # 8 test files - Validation, ID generation, federation
│   ├── internal/kv/           # 4 test files - Redis scripts, stale detection, reconciler
│   ├── internal/rpc/          # 6 test files - Protocol, client, auth, mutations, metrics
│   ├── internal/circuitbreaker/ # 1 test file - State machine, concurrency
│   ├── internal/lockfile/     # 1 test file - File locking, PID management
│   ├── internal/debug/        # 1 test file - Debug/verbose/quiet modes
│   └── makefile_test.go       # Meta-test for build system
│
└── Frontend (Vitest + Playwright)
    ├── src/api/*.test.ts           # API client, SSE client
    ├── src/hooks/*.test.ts         # 25+ custom hook tests
    ├── src/components/**/*.test.tsx # 60+ component tests
    ├── tests/e2e/*.spec.ts         # 55+ E2E browser tests
    ├── tests/e2e/integration/      # 2 real backend integration specs
    └── tests/e2e/api/              # 12 API contract test specs
```
