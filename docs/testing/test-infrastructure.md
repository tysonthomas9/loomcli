# Test Infrastructure

> **Status:** Current · *audited 2026-08-03*

CI/CD pipelines, scripts, configuration files, coverage, and tooling. This page
is the owner of the make-target surface and the coverage thresholds; other
testing docs should point here rather than restate them.

---

## Table of Contents

1. [CI/CD Pipelines](#cicd-pipelines)
2. [Test Scripts](#test-scripts)
3. [Makefile Targets](#makefile-targets)
4. [Configuration Files](#configuration-files)
5. [Coverage](#coverage)
6. [Quality Gates](#quality-gates)
7. [Build Tags](#build-tags)
8. [Development Workflow](#development-workflow)

---

## CI/CD Pipelines

### Main CI (`.github/workflows/ci.yml`)

**Trigger**: push to `main` or `v5` (`ci.yml:4-5`), plus pull requests targeting
any branch (`ci.yml:6-9`).

**Go toolchain**: never pinned in the workflow — every job uses
`go-version-file: 'go.mod'` (`ci.yml:27`, `:78`, `:162`). `go.mod:3` currently
declares `go 1.25.6`.

**Jobs** (seven):

| Job | Line | What it does |
|---|---|---|
| `go-quality-gate` | `ci.yml:17` | Installs Go + golangci-lint (`golangci-lint-action@v9`, `install-only: true`), runs a decoupling smoke (`rm -rf frontend/dist && make build`), then `make check-go` |
| `frontend-quality-gate` | `ci.yml:49` | Node 20, `npm ci`, then `make check-frontend` |
| `coverage-go` | `ci.yml:68` | `go test -race -coverprofile -tags=integration -timeout 15m ./...`, then `./scripts/check-coverage.sh coverage.out 60` (`ci.yml:89`), then Codecov upload |
| `coverage-frontend` | `ci.yml:99` | `npm run test:coverage` |
| `frontend-standalone` | `ci.yml:118` | Builds + tests the frontend with no Go toolchain present |
| `test-macos-go` | `ci.yml:152` | Cross-platform Go validation |
| `builtin-bundle-pin` | `ci.yml:175` | Guards the builtin workflow bundle pin |

### Other workflows

| Workflow | Purpose |
|---|---|
| `.github/workflows/nightly.yml` | Full suite with `-tags=integration`, 30m timeout, **70%** coverage threshold (`nightly.yml:34`) |
| `.github/workflows/e2e.yml` | Container E2E |
| `.github/workflows/playwright.yml` | Playwright suites |
| `.github/workflows/release.yml` | Tags `v*`, GoReleaser; uses `go-version-file: 'go.mod'` — no version skew with CI |
| `.github/workflows/desktop-release.yml` | Desktop app release |

---

## Test Scripts

### `scripts/test.sh`

**Purpose**: main Go test runner with skip support, coverage, and flexible
configuration.

**Isolation**: if `LOOM_CONFIG_DIR` is unset, the script creates a `mktemp -d`
and removes it on exit (`scripts/test.sh:11-16`) so state-cache writes never
clobber the developer's real `~/.loom`.

**Environment variables**:

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEST_TIMEOUT` | `3m` | Test timeout |
| `TEST_VERBOSE` | (empty) | Enable verbose output (`-v`) |
| `TEST_RUN` | (empty) | Run specific tests (`-run` pattern) |
| `TEST_TAGS` | (empty) | Build tags (`scripts/test.sh:36`) — e.g. `integration`, `integration,e2e` |
| `TEST_RACE` | (empty) | Add `-race` (`scripts/test.sh:39`) |
| `TEST_COVER` | (empty) | Enable coverage collection |
| `TEST_COVERPROFILE` | `/tmp/loom.coverage.out` | Coverage output file |
| `TEST_COVERPKG` | (empty) | Coverage package filter |

**Skip file**: `.test-skip` is optional and is **not present** in this repo.
When absent, `build_skip_pattern()` returns an empty pattern and no `-skip`
flag is added (`scripts/test.sh:20-23`). If you create one: one regex per line,
`#` for comments.

**Usage**:

```bash
./scripts/test.sh                          # basic
TEST_COVER=1 ./scripts/test.sh             # with coverage
TEST_RUN="TestAutomode" ./scripts/test.sh  # specific tests
TEST_TIMEOUT=10m ./scripts/test.sh         # custom timeout
TEST_TAGS=integration ./scripts/test.sh    # integration build tag
```

### `scripts/dev.sh`

**Purpose**: the single dev entry point. Hot-reload environment with parallel
Go + frontend servers.

1. **Air** (Go hot-reload): watches Go files, rebuilds, runs `loom serve --no-daemon --frontend-url http://localhost:3000`
2. **Vite** (frontend HMR): watches frontend files, serves on port 3000, proxies `/api/*` and `/health` to `:8080`

**Dependency checks**: validates `air`, `node`, `npm` exist with install
instructions. **Cleanup**: kills both processes on exit via trap.

### `scripts/dev_test.sh`

Tests `dev.sh`'s structure without running it: existence, executability,
shebang, strict mode, dependency checks, cleanup trap, frontend directory
references.

### `scripts/start-e2e-server.sh`

Builds and starts `loom serve` plus a Vite preview server for the
self-contained Playwright projects. Invoked by Playwright's `webServer`
(`playwright.config.ts:73`), not directly.

### `scripts/hooks/pre-push`

Git pre-push hook. It first clears git's repository-local environment so nested
git commands in temp repos cannot mutate this checkout, then runs the gate:

```bash
#!/usr/bin/env bash
set -euo pipefail

while IFS= read -r git_env; do
  unset "$git_env"
done < <(git rev-parse --local-env-vars)

echo "[pre-push] Running quality gate..."
make check
```

**Installation**: `make hooks` (`Makefile:593`) copies it into the git hooks
directory (resolves correctly in worktrees) and installs `pre-commit`.

### Gate scripts

Called from `check-go`; each is also runnable standalone.

| Script | Argument | Enforces |
|---|---|---|
| `scripts/check-control-plane-paths.sh` | — | The two legal control-plane paths; memstore is test-only |
| `scripts/check-loc.sh` | `1000 2500` | Per-file line-count ceilings |
| `scripts/check-package-size.sh` | `25` | Max files per package |
| `scripts/check-import-fanout.sh` | `18` | Max imports pulled by one package |
| `scripts/check-no-raw-exec.sh` | — | No raw `exec.Command`; use the wrapper |
| `scripts/check-no-log-printf.sh` | — | No `log.Printf`; use structured logging |
| `scripts/check-no-beads-prod.sh` | — | No new production beads/`bd` references |
| `scripts/check-go-api-staleness.sh` | — | Generated Go API matches `api/openapi.yaml` |
| `scripts/check-coverage.sh` | profile, threshold | Coverage floor |
| `scripts/with-clean-loom-env.sh` | command | Strips inherited `LOOM_*` desktop env before running tests |

---

## Makefile Targets

### Quality gates

| Target | Line | Purpose |
|--------|------|---------|
| `check-go` | `Makefile:502` | 15-step Go gate (see [Quality Gates](#quality-gates)) |
| `check-frontend` | `Makefile:538` | 6-step frontend gate |
| `check` | `Makefile:554` | Runs both in parallel; fails if either fails |
| `gate` | `Makefile:578` | Backward-compatible alias for `check` |
| `gate-e2e` | `Makefile:581` | `gate` + `make test-e2e-real-smoke` |
| `gate-e2e-full` | `Makefile:587` | `gate-e2e` + `go test -tags container ./e2e/` |

### Test targets

| Target | Line | Purpose |
|--------|------|---------|
| `test` | `Makefile:45` | `TEST_COVER=1 ./scripts/test.sh` — unit suite with coverage |
| `test-integration` | `Makefile:50` | `TEST_TAGS=integration` |
| `test-all` | `Makefile:55` | `TEST_TAGS=integration,e2e` |
| `test-frontend` | `Makefile:374` | `npx vitest run` |
| `test-e2e` | `Makefile:381` | Route-mocked Playwright chromium project |
| `test-e2e-api` / `-local` | `Makefile:387` / `:392` | API contract specs, self-contained / against a running `loom serve` |
| `test-e2e-integration` / `-local` / `-full` | `Makefile:437` / `:442` / `:447` | Browser integration specs |
| `test-e2e-real-smoke` / `-local` | `Makefile:417` / `:422` | `integration-smoke` + `api-smoke` projects |
| `test-e2e-real-regression` / `-local` | `Makefile:427` / `:432` | `integration-regression` + `api-regression` projects |
| `test-e2e-github-webhook` | `Makefile:397` | Ephemeral fleet-db + loom serve webhook dispatch |
| `test-e2e-github-webhook-live` | `Makefile:403` | **Live** — requires `LOOM_E2E_GITHUB_REPO`, opens and closes a real PR |
| `test-e2e-stackpublish` | `Makefile:412` | **Live** — requires `LOOM_STACK_E2E` + `LOOM_STACK_E2E_REPO` |
| `test-fleetdb-embedded` | `Makefile:90` | Clean-checkout embedded smoke |
| `test-fleetdb-supervisor` | `Makefile:94` | Supervisor control-plane gate (pinned `-run` regex) |
| `test-fleetdb-ui` | `Makefile:113` | FleetDB-only UI regression suite |
| `test-fleetdb-empty-cli` | `Makefile:126` | New-user CLI scenarios against the empty stack |
| `test-distributed-smoke` | `Makefile:237` | Distributed fleet-db smoke |
| `test-playground` | `Makefile:73` | Daemon failure-mode harness |
| `test-builtin-workflows` | `Makefile:62` | Builtin workflow node tests |

### Local-mode stacks

| Target | Line | Purpose |
|--------|------|---------|
| `local-mode-up` | `Makefile:168` | Deterministic dogfood stack (`localdogfood` backend) |
| `local-mode-codex-up` | `Makefile:174` | Real Codex CLI variant |
| `local-mode-claude-up` | `Makefile:180` | Real Claude CLI variant |
| `local-mode-daytona-up` | `Makefile:190` | Daemon TS leaf routed to a real Daytona sandbox; needs `DAYTONA_API_KEY` |
| `local-mode-down` / `-logs` | `Makefile:196` / `:201` | Teardown / follow logs |
| `local-mode-verify` | `Makefile:206` | `test/local-mode/verify-local-mode.sh` |
| `local-mode-routing-verify` | `Makefile:214` | `test/local-mode/verify-agent-routing.py` |
| `local-mode-webhook-verify` | `Makefile:221` | `test/local-mode/verify-webhook.sh` |
| `local-mode-codex-verify` | `Makefile:224` | Verifier with the Codex task IDs |

See [local-mode-podman-e2e.md](local-mode-podman-e2e.md) for the runbook.

### Build and development targets

| Target | Line | Purpose |
|--------|------|---------|
| `build` | `Makefile:40` | Go binary only |
| `build-frontend` | `Makefile:490` | Frontend dist only (Go-free) |
| `build-all` | `Makefile:495` | Both |
| `frontend` | `Makefile:498` | DEPRECATED alias for `build-frontend` |
| `dev` | `Makefile:615` | `./scripts/dev.sh` — Go API on `:8080` + Vite on `:3000` |
| `dev-loom` / `dev-vite` | `Makefile:620` / `:626` | DEPRECATED aliases for `dev`; print a warning and forward |
| `dev-check` | `Makefile:609` | Validates `air`, `node` |
| `hooks` | `Makefile:593` | Install git hooks |

## Configuration Files

### `.golangci.yml`

`linters.default: none` then an explicit enable list of 14
(`.golangci.yml:8-23`):

`cyclop`, `errcheck`, `funlen`, `gocognit`, `gocritic`, `gosec`, `govet`,
`ineffassign`, `misspell`, `revive`, `staticcheck`, `depguard`, `unconvert`,
`unparam`.

**`depguard` is the one that blocks PRs most often** — it encodes the
architectural layering (handler-layer isolation, sdk leaf, infra isolation,
webui isolation, data isolation). Read its rule groups in `.golangci.yml`
before moving a package.

Notable settings: `cyclop.max-complexity: 20`, `funlen.lines: 50`,
`gocognit.min-complexity: 30`, `gocritic` with experimental/opinionated/
performance tags disabled, and a `gosec` exclusion block for the SSA
taint-analysis rules G702-G706 whose sanitizer recognition is broken in the
current gosec version (the AST-based G204/G304 cover the same ground).

### `.air.toml`

Hot-reload configuration for Go development.

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

**Key exclusions**: test files, frontend (has its own HMR), third_party.

### `.gitignore` (test-related entries)

```
tmp/                                           # Air build artifacts
internal/webui/frontend/test-results/          # Playwright test results
internal/webui/frontend/playwright-report/     # Playwright HTML report
internal/webui/frontend/tests/e2e/.e2e-state.json  # E2E state file
```

### Playwright (`internal/webui/frontend/playwright.config.ts`)

Ports are resolved through `resolvePort()` with env overrides
(`playwright.config.ts:26-28`):

- API server: `E2E_PORT`, default **8090**
- Vite preview: `E2E_FRONTEND_PORT`, default **3100**

In self-contained mode (`RUN_INTEGRATION_TESTS=1` without `LOOM_LOCAL_SERVER`)
`apiBaseURL` and `frontendBaseURL` resolve to those ports; otherwise they fall
back to `LOOM_BASE_URL` / `http://localhost:8080` and `LOOM_FRONTEND_BASE_URL` /
`http://localhost:3000` (`playwright.config.ts:45-54`).

**Projects**:

| Project | Line | Test dir | Selection |
|---|---|---|---|
| `chromium` | `:128` | `tests/e2e` | Route-mocked; ignores `*.integration.spec.ts` unless `RUN_INTEGRATION_TESTS=1` |
| `integration` | `:138` | `tests/e2e/integration` | All `*.integration.spec.ts` |
| `integration-smoke` | `:155` | `tests/e2e/integration` | `grep: /@smoke/` (`:158`) |
| `integration-regression` | `:173` | `tests/e2e/integration` | `grep: /@regression/` (`:176`) |
| `local-integration` | `:191` | `tests/e2e/integration` | Only `terminal-parity.integration.spec.ts`, gated on `RUN_LOCAL_INTEGRATION_TESTS` |
| `api` | `:208` | `tests/e2e/api` | All `*.api.spec.ts` |
| `api-smoke` | `:220` | `tests/e2e/api` | `grep: /@smoke/` (`:224`) |
| `api-regression` | `:233` | `tests/e2e/api` | `grep: /@regression/` (`:237`) |

A spec joins the smoke or regression suite by carrying a `@smoke` or
`@regression` tag in its title — that is the entire promotion mechanism.

**CI settings**: `retries: 3`, `workers: 1`, `reporter: "github"`
(`playwright.config.ts:100-102`). Locally: 0 retries, HTML reporter.

**webServer**: in self-contained mode Playwright runs
`bash ../../../scripts/start-e2e-server.sh` (`playwright.config.ts:73`) and waits
on the Vite preview URL (`playwright.config.ts:70-95`). In local-server mode it starts nothing.

### Vitest (in `vite.config.ts:219-241`)

```typescript
test: {
  globals: true,
  environment: "node",          // NOT jsdom — see below
  exclude: ["tests/e2e/**", "node_modules/**"],
  pool: "forks",
  coverage: { provider: "v8", /* … */ thresholds: { lines: 60, branches: 60,
                                                    functions: 60, statements: 60 } },
}
```

The default environment is **`node`** (`vite.config.ts:221`). Tests that need a
DOM opt in per file with a `/** @vitest-environment jsdom */` docblock; 317 of
the 369 unit test files carry one. A component test failing on
`document is not defined` is missing it. See
[frontend-tests.md](frontend-tests.md).

---

## Coverage

### Go coverage

Three different thresholds exist. They are not in conflict; they apply to
different runs.

| Where | Threshold | Citation |
|---|---|---|
| `make gate` / `make check-go` | **60%** | `Makefile:534` (`COVERAGE_THRESHOLD=60 ./scripts/check-coverage.sh`) |
| CI `coverage-go` job | **60%** | `.github/workflows/ci.yml:89` |
| Nightly | **70%** | `.github/workflows/nightly.yml:34` |
| Script default when neither arg nor env is given | 70 | `scripts/check-coverage.sh:9` |

**Reporting**:
- `go tool cover -func=coverage.out` (terminal summary)
- `go tool cover -html=coverage.out` (browser visualization)
- Codecov upload (CI only)

```bash
make test                                  # via Makefile
TEST_COVER=1 ./scripts/test.sh             # via script
go test -coverprofile=coverage.out ./...   # manual
go tool cover -html=coverage.out
```

### Frontend coverage

`npm run test:coverage` → `vitest run --coverage`. It is step 6 of
`check-frontend` and carries its own 60% threshold (`Makefile:549-550`).

---

## Quality Gates

`make gate` is an alias for `make check` (`Makefile:578`). `check`
(`Makefile:554-575`) forks `check-go` and `check-frontend` in parallel, buffers
each to a temp log, and prints only the failing side's output.

### `check-go` — 15 steps (`Makefile:502-535`)

| # | Step | Command |
|---|---|---|
| 1 | Format | `gofmt -l .` (excluding third_party, worktrees, vendor, node_modules) |
| 2 | Vet | `go vet ./...` |
| 3 | Build | `go build -buildvcs=false ./...` |
| 4 | Lint | `golangci-lint run --timeout=5m` + `scripts/check-control-plane-paths.sh` |
| 5 | LOC | `scripts/check-loc.sh 1000 2500` |
| 6 | Package size | `scripts/check-package-size.sh 25` |
| 7 | Import fanout | `scripts/check-import-fanout.sh 18` |
| 8 | Exec guard | `scripts/check-no-raw-exec.sh` |
| 9 | Log guard | `scripts/check-no-log-printf.sh` |
| 10 | Beads guard | `scripts/check-no-beads-prod.sh` |
| 11 | API staleness | `scripts/check-go-api-staleness.sh` |
| 12 | `docs/api.md` staleness | `scripts/check-api-docs-staleness.sh` |
| 13 | `docs/reference` staleness | `scripts/check-loomdoc-staleness.sh` |
| 14 | Race test | `scripts/with-clean-loom-env.sh go test -p 1 -race -covermode=atomic -timeout 15m ./...` |
| 15 | Coverage | `COVERAGE_THRESHOLD=60 scripts/check-coverage.sh` |

Steps 5-13 are the ones people are surprised by: a push can fail on file
length, package size, import fanout, a raw `exec.Command`, a `log.Printf`, a
new beads reference, a stale generated API, a `docs/api.md` that no longer
matches `api/openapi.yaml`, or a `docs/reference/*.md` that no longer matches
the Go source it is generated from — none of which are test failures.

### `check-frontend` — 6 steps (`Makefile:538-551`)

| # | Step | Command |
|---|---|---|
| 1 | Format | `npm run format:check` |
| 2 | Typecheck | `npm run typecheck` |
| 3 | ESLint | `npm run lint` |
| 4 | Architecture | `npm run check:arch` = `check:loc` + `check:no-raw-fetch` + `check:no-hardcoded-urls` + `check:boundaries` |
| 5 | Generated staleness | `npm run check:generated` (OpenAPI types) |
| 6 | Unit tests + coverage | `npm run test:coverage` (60% threshold) |

### Local gate environment

`make gate` inherits whatever env your shell has. From a Loom desktop-launched
shell that means tests can resolve the real desktop workspace instead of their
fixtures. `AGENTS.md` §Local Gate Environment lists the variables to unset;
step 14 already wraps the race test in `scripts/with-clean-loom-env.sh`.

---

## Build Tags

### `e2e`

**Purpose**: end-to-end tests requiring external dependencies (tmux, a real
daemon, an ephemeral fleet-db).

**Usage**: `go test -tags=e2e ./...`, or `make test-all`.

**Files**: 12, including `internal/cli/automode_e2e_test.go`,
`internal/cli/serve_e2e_test.go`,
`internal/webui/handlers/webhooks/webhooks_e2e_test.go`,
`internal/webui/handlers/workflows/workflow_endpoints_e2e_test.go`,
`internal/stackpublish/stackpublish_e2e_test.go`.

```go
//go:build e2e

func TestE2E_Something(t *testing.T) {
    skipIfNoTmux(t)
    // ...
}
```

### `integration`

**Purpose**: integration-depth Go tests. Set via `TEST_TAGS=integration`
(`Makefile:50`) or `-tags=integration` in CI (`ci.yml:86`).

### `container`

**Purpose**: the `./e2e/` container suite, run by `make gate-e2e-full`
(`Makefile:589`).

### `testing.Short()`

**Usage**: `go test -short ./...`. Used in 7 places under `internal/`.

### Benchmarks

There is **no `bench` build tag**. The repo has exactly two benchmarks, both
untagged and both in `internal/types/id_generator_test.go`:
`BenchmarkGenerateHashID` (`:197`) and `BenchmarkGenerateChildID` (`:206`).

```bash
go test -bench=. ./internal/types
go test -bench=. -benchmem ./internal/types
```

---

## Development Workflow

1. **Start dev servers**: `make dev` (Go API on `:8080` + Vite HMR on `:3000`)
2. **Write tests first**: create `*_test.go` or `__tests__/*.test.ts(x)` files
3. **Run tests in watch mode**:
   - Go: `go test -v ./internal/cli/... -run TestMyFeature`
   - Frontend: `cd internal/webui/frontend && npm run test:watch`
4. **Verify quality gate**: `make gate`
5. **Push**: the pre-push hook runs `make check` automatically

### Test data

**Go fixtures**: `internal/cli/testdata/`

**Frontend fixtures**: `internal/webui/frontend/tests/fixtures/` — `base.ts`
defines the `mockApi` / `mockSSE` / `appPage` fixtures. Helpers live in
`tests/helpers/`, page objects in `tests/pages/`. See
[test-patterns.md](test-patterns.md).

### Debugging failed tests

```bash
# Go: verbose single test
go test -v -run TestSpecificTest ./internal/cli/...

# Go: with race detection
go test -race -v -run TestSpecificTest ./internal/cli/...

# Frontend unit: single file
cd internal/webui/frontend
npx vitest run src/stores/__tests__/issueStore.test.ts

# Frontend E2E: with browser
cd internal/webui/frontend
npx playwright test kanban.spec.ts --headed --debug
```

### Test dependencies

| Dependency | Purpose | Used By |
|------------|---------|---------|
| `miniredis` | In-memory Redis | `internal/kv`, `internal/webui/localredis` |
| `httptest` | HTTP server mocking | `internal/webui/**` tests |
| `os.Pipe()` | Output capture | `internal/debug` tests |
| `t.TempDir()` | Isolated temp directories | Filesystem tests |
| `sync.WaitGroup` | Goroutine coordination | Concurrency tests |
| MockEventSource | Browser EventSource mock | Frontend SSE tests |
| React Testing Library | Component rendering | Frontend component tests |
| Playwright | Browser automation | Frontend E2E tests |

## Related

- [README.md](README.md) — testing docs index
- [../testing-terminology.md](../testing-terminology.md) — what `gate`, `local`, `real`, `live` and `verify` mean here
- [test-patterns.md](test-patterns.md) — how to write a test in this repo
- [frontend-tests.md](frontend-tests.md) — frontend test layout
- [fleetdb-acceptance-gates.md](fleetdb-acceptance-gates.md) — the named acceptance gates
