# Test Infrastructure

CI/CD pipelines, scripts, configuration files, coverage, and tooling.

---

## Table of Contents

1. [CI/CD Pipelines](#cicd-pipelines)
2. [Test Scripts](#test-scripts)
3. [Makefile Targets](#makefile-targets)
4. [Configuration Files](#configuration-files)
5. [Coverage](#coverage)
6. [Quality Gates](#quality-gates)
7. [Build Tags](#build-tags)
8. [Benchmarks](#benchmarks)
9. [Development Workflow](#development-workflow)

---

## CI/CD Pipelines

### Main CI (`.github/workflows/ci.yml`)

**Trigger**: Push to any branch, pull requests

**Jobs**:

#### Build & Test

| Platform | Race Detection | Coverage | Purpose |
|----------|---------------|----------|---------|
| Ubuntu (latest) | Yes (`-race`) | Yes (`-coverprofile`) | Primary test + coverage |
| macOS (latest) | Yes (`-race`) | No | Cross-platform validation |

- **Go version**: 1.24
- **Fail-fast**: Disabled (all platforms run even if one fails)
- **Coverage threshold**: 25% minimum (hard fail), 40% warning

#### Lint

- **Tool**: `golangci-lint-action@v9` (latest version)
- **Timeout**: 5 minutes

#### Coverage Upload

- **Service**: Codecov
- **Condition**: Ubuntu runs only

### Release (`.github/workflows/release.yml`)

- **Trigger**: Tags matching `v*`
- **Tool**: GoReleaser
- **Platforms**: Linux, macOS, Windows (amd64 + arm64)
- **Note**: Uses Go 1.21 (differs from CI's 1.24)

---

## Test Scripts

### `scripts/test.sh`

**Purpose**: Main test runner with skip support, coverage, and flexible configuration.

**Environment Variables**:

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEST_TIMEOUT` | `3m` | Test timeout |
| `TEST_VERBOSE` | (empty) | Enable verbose output (`-v`) |
| `TEST_RUN` | (empty) | Run specific tests (`-run` pattern) |
| `TEST_COVER` | (empty) | Enable coverage collection |
| `TEST_COVERPROFILE` | `/tmp/loom.coverage.out` | Coverage output file |
| `TEST_COVERPKG` | (empty) | Coverage package filter |

**Skip File**: `.test-skip`
- One regex pattern per line
- Lines starting with `#` are comments
- Currently empty (no tests skipped)

**Usage**:

```bash
# Basic
./scripts/test.sh

# With coverage
TEST_COVER=1 ./scripts/test.sh

# Specific tests
TEST_RUN="TestAutomode" ./scripts/test.sh

# Custom timeout
TEST_TIMEOUT=10m ./scripts/test.sh

# Verbose
TEST_VERBOSE=1 ./scripts/test.sh
```

### `scripts/dev.sh`

**Purpose**: The single dev entry point post-Phase 5. Hot-reload development
environment with parallel Go + frontend servers.

**Components**:
1. **Air** (Go hot-reload): Watches Go files, rebuilds, runs `loom serve --no-daemon --frontend-url http://localhost:3000`
2. **Vite** (Frontend HMR): Watches frontend files, serves on port 3000, proxies `/api/*` and `/health` to `:8080`

**Dependency checks**: Validates `air`, `node`, `npm` exist with install instructions.

**Cleanup**: Kills both processes on exit via trap.

### `scripts/dev_test.sh`

**Purpose**: Tests the `dev.sh` script structure without actually running it.

**Validates**: Existence, executability, shebang, strict mode, dependency checks, cleanup trap, frontend directory references.

### `scripts/hooks/pre-push`

**Purpose**: Git pre-push hook that runs the quality gate.

```bash
#!/usr/bin/env bash
set -euo pipefail
echo "[pre-push] Running quality gate..."
make gate
```

**Installation**: `make hooks` copies to the git hooks directory (works in worktrees)

---

## Makefile Targets

### Test Targets

| Target | Command | Purpose |
|--------|---------|---------|
| `test` | `TEST_COVER=1 ./scripts/test.sh` | Full test suite with coverage |
| `gate` | `go build && go vet && go test -race -timeout 15m` | Quality gate (pre-push) |
| `frontend` | `cd internal/webui/frontend && npm install && npm run build` | Build frontend |

### Development Targets

| Target | Command | Purpose |
|--------|---------|---------|
| `dev` | `./scripts/dev.sh` | Go API server on `:8080` + Vite dev server on `:3000` (the canonical dev path post-Phase 5) |
| `dev-loom` | `./scripts/dev.sh` | DEPRECATED alias for `make dev`; prints a warning and forwards |
| `dev-vite` | `./scripts/dev.sh` | DEPRECATED alias for `make dev`; prints a warning and forwards |
| `dev-check` | Validates air, node, npm | Check dev dependencies |
| `hooks` | Copy pre-push hook | Install git hooks |

## Configuration Files

### `.golangci.yml`

**Enabled Linters**:

| Linter | Purpose |
|--------|---------|
| `errcheck` | Unchecked error return values |
| `gosec` | Security vulnerability detection |
| `misspell` | Spelling errors in code/comments |
| `unconvert` | Unnecessary type conversions |
| `unparam` | Unused function parameters |

**Exclusions** (with rationale):

| Rule | Files | Rationale |
|------|-------|-----------|
| G104 (unhandled error) | Cleanup calls | `Close()`, `Remove()` errors rarely actionable |
| G301 (dir permissions) | General | 0755 is standard for directories |
| G302 (file permissions) | `internal/cli/lock.go` | Lock file needs specific permissions |
| G304 (file inclusion) | `automode.go`, `lock.go` | Variable paths are validated |
| G204 (subprocess) | Multiple | tmux and agent backends require subprocess exec |

### `.air.toml`

**Purpose**: Hot-reload configuration for Go development.

```toml
tmp_dir = "tmp"

[build]
  bin = "./tmp/loom"
  cmd = "go build -o ./tmp/loom ./cmd/loom"
  args_bin = ["serve", "--no-daemon", "--frontend-url", "http://localhost:3000"]
  exclude_dir = ["tmp", "internal/webui/frontend", "node_modules",
                 "vendor", "testdata", ".git", "third_party"]
  exclude_regex = ["_test\\.go$"]
```

**Key exclusions**: Test files, frontend (has its own HMR), third_party.

### `.test-skip`

**Purpose**: Patterns for tests to skip during `./scripts/test.sh` runs.

**Format**: One regex per line, `#` for comments. Currently empty.

### `.gitignore` (test-related entries)

```
tmp/                                           # Air build artifacts
internal/webui/frontend/test-results/          # Playwright test results
internal/webui/frontend/playwright-report/     # Playwright HTML report
internal/webui/frontend/tests/e2e/.e2e-state.json  # E2E state file
```

### Playwright Configuration (`playwright.config.ts`)

**Projects**:

| Project | Environment | URL | Condition |
|---------|------------|-----|-----------|
| `chromium` | Mocked E2E | `http://localhost:3000` | Always |
| `integration` | Real backend | `http://localhost:8080` | `RUN_INTEGRATION_TESTS=1` |
| `api` | API-only | `http://localhost:9000` | `RUN_INTEGRATION_TESTS=1` |

**CI settings**: Single worker, 2 retries, GitHub reporter.

### Vitest Configuration (in `vite.config.ts`)

```typescript
test: {
  globals: true,
  environment: 'jsdom',
  // setup files, coverage config, etc.
}
```

---

## Coverage

### Go Coverage

**CI threshold**: 25% hard fail, 40% warning.

**Collection**: `go test -coverprofile=coverage.out ./...`

**Reporting**:
- `go tool cover -func=coverage.out` (terminal summary)
- `go tool cover -html=coverage.out` (browser visualization)
- Codecov upload (CI only, Ubuntu)

**Local commands**:

```bash
# Via Makefile
make test

# Via script
TEST_COVER=1 ./scripts/test.sh

# Manual with HTML report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Frontend Coverage

Configured via Vitest. Run with:

```bash
cd internal/webui/frontend
npx vitest run --coverage
```

---

## Quality Gates

### Pre-push Hook

Runs `make gate` which executes:

1. `go build ./...` - Compilation check
2. `go vet ./...` - Static analysis
3. `go test -race -timeout 15m ./...` - Full test suite with race detection

**Effect**: Prevents pushing code that fails build, vet, or tests.

### CI Pipeline

1. Build verification
2. Test with race detection (Ubuntu + macOS)
3. Coverage threshold check (Ubuntu)
4. Lint check (golangci-lint)
5. Coverage upload (Codecov)

---

## Build Tags

### `e2e`

**Purpose**: End-to-end tests requiring external dependencies (tmux, real daemon).

**Usage**: `go test -tags=e2e ./...`

**Files**: `internal/cli/automode_e2e_test.go` and others.

**Guard pattern**:
```go
//go:build e2e
// +build e2e

func TestE2E_Something(t *testing.T) {
    skipIfNoTmux(t)
    // ...
}
```

### `bench`

**Purpose**: Benchmark tests (excluded from normal test runs).

**Usage**: `go test -bench=. -tags=bench ./...`

**Files**: Benchmark files live with the packages they exercise.

**Guard pattern**:
```go
//go:build bench

func BenchmarkGetReadyWork_Large(b *testing.B) {
    // ...
}
```

### `testing.Short()`

**Purpose**: Skip slow tests for fast iteration.

**Usage**: `go test -short ./...`

**Approximately 860+ uses** across the codebase.

---

## Benchmarks

### Location

- `internal/types/` - ID generation benchmarks
- `internal/rpc/` - RPC performance benchmarks

### Running

```bash
# All benchmarks
go test -bench=. -tags=bench ./...

# With memory allocation reporting
go test -bench=. -benchmem -tags=bench ./...
```

### Patterns

```go
func BenchmarkOperation(b *testing.B) {
    // Setup (not measured)
    db := setupBenchDB(b)

    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        // Measured operation
        db.GetReadyWork(ctx, filter)
    }
}
```

---

## Development Workflow

### Recommended Workflow

1. **Start dev servers**: `make dev` (Go API on :8080 + Vite HMR on :3000)
2. **Write tests first**: Create `*_test.go` or `*.test.ts(x)` files
3. **Run tests in watch mode**:
   - Go: `go test -v ./internal/cli/... -run TestMyFeature`
   - Frontend: `cd internal/webui/frontend && npm run test:watch`
4. **Verify quality gate**: `make gate`
5. **Push**: Pre-push hook runs gate automatically

### Test Data

**Go fixtures**: `internal/cli/testdata/`
- `merge_conflict_files.txt` - Mock merge conflict file list

**Frontend fixtures**: `tests/e2e/fixtures/`
- `mockApi` - API response factory
- `mockSSE` - SSE event simulator

### Debugging Failed Tests

```bash
# Go: verbose single test
go test -v -run TestSpecificTest ./internal/cli/...

# Go: with race detection
go test -race -v -run TestSpecificTest ./internal/cli/...

# Frontend unit: single file
cd internal/webui/frontend
npx vitest run src/hooks/useIssues.test.ts

# Frontend E2E: with browser
cd internal/webui/frontend
npx playwright test kanban.spec.ts --headed --debug
```

### Test Dependencies

| Dependency | Purpose | Used By |
|------------|---------|---------|
| `miniredis` | In-memory Redis mock | `internal/kv` tests |
| `httptest` | HTTP server mocking | `internal/webui` tests |
| `os.Pipe()` | Output capture | `internal/debug` tests |
| `exec.Command` | Subprocess testing | `internal/cli` tests |
| `t.TempDir()` | Isolated temp directories | Filesystem tests |
| `sync.WaitGroup` | Goroutine coordination | Concurrency tests |
| MockEventSource | Browser EventSource mock | Frontend SSE tests |
| React Testing Library | Component rendering | Frontend component tests |
| Playwright | Browser automation | Frontend E2E tests |
