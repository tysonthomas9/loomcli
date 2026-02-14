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

- **[Go Backend Tests](go-backend-tests.md)** - All Go test files organized by package, every test function, and what it validates
- **[Frontend Tests](frontend-tests.md)** - Vitest unit tests, Playwright E2E tests, component tests, API tests
- **[Test Infrastructure](test-infrastructure.md)** - CI/CD, scripts, Makefile targets, configuration files, coverage
- **[Test Patterns & Conventions](test-patterns.md)** - Common patterns, mocking strategies, helpers, and best practices

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
make gate
```

This runs: `go build` + `go vet` + `go test -race -timeout 15m ./...`

## Test Coverage

- **CI threshold**: 25% minimum (hard fail), 40% warning
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
│   ├── third_party/beads/     # 100+ test files - Beads daemon, storage, doctor
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
