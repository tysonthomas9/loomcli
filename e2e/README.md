# loomcli E2E Test Container

> **Status:** Current · *audited 2026-07-24*

Alpine-based container for running loomcli E2E tests in isolation.
Includes Go binaries, Chromium, Playwright, agent-browser, and stub backends.

**Image size:** ~1.5GB | **Min Podman VM disk:** 14GB

This container is self-contained. The **epic-runner lanes** at the bottom of
this file are not — they need sibling `fleet-db` and `flue` checkouts.

## Quick Start

```bash
# Build (first time ~3min, rebuilds ~5s with cache)
podman build -f e2e/Dockerfile -t loomcli-e2e .

# Run all test phases
podman run --rm loomcli-e2e

# Run a specific phase
podman run --rm loomcli-e2e run_test.sh --phase smoke
podman run --rm loomcli-e2e run_test.sh --phase playwright
```

## What's in the Container

| Tool | Purpose |
|---|---|
| `loom` | Static Go binary (CGO_ENABLED=0) |
| `chromium-browser` | Alpine's Chromium package |
| `agent-browser` | Browser automation CLI for AI agents |
| `@playwright/test` | Playwright test runner |
| `curl`, `jq`, `git`, `tmux` | CLI utilities |
| `claude`, `codex`, `opencode` | Stub backend CLIs |

## Test Phases

The `run_test.sh` orchestrator runs four phases:

| Phase | What it tests | Requirements |
|---|---|---|
| `smoke` | Binary existence, stub output, workspace setup, loom commands | Shell only |
| `unit` | `go test ./...` | Go toolchain (skipped in Alpine image) |
| `e2e` | `go test -tags e2e ./internal/cli/` per backend | Go or pre-compiled test binary (skipped if neither available) |
| `playwright` | Mocked chromium tests + self-contained API e2e | Node.js + Chromium |

```bash
# Run specific phase
podman run --rm loomcli-e2e run_test.sh --phase smoke
podman run --rm loomcli-e2e run_test.sh --phase playwright

# E2E for one backend only
podman run --rm loomcli-e2e run_test.sh --phase e2e --backend claude

# Continue after failures
podman run --rm loomcli-e2e run_test.sh --no-fail-fast

# Pass extra flags
podman run --rm loomcli-e2e run_test.sh -- -run TestE2E_TmuxSession
```

## Local Development (run_local.sh)

Wraps build + run with sensible defaults:

```bash
# Build and run
e2e/run_local.sh

# Skip rebuild
e2e/run_local.sh --no-build

# Mount real CLI binaries from host
e2e/run_local.sh --mount-clis

# Set a specific backend
e2e/run_local.sh --backend codex
```

Auto-mounts auth configs (`~/.claude/`, `~/.codex/`, `~/.config/opencode/`) read-only and forwards `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, and `STUB_*` env vars.

## Using agent-browser

agent-browser requires a `navigate` before other commands:

```bash
podman run --rm loomcli-e2e bash -c '
  # Start loom serve
  mkdir -p /tmp/ws && cd /tmp/ws
  git config --global user.email "test@test.com"
  git config --global user.name "Test"
  git init -q && git commit --allow-empty -m seed -q
  loom serve --port 8099 &
  sleep 2

  # Use agent-browser
  agent-browser navigate http://127.0.0.1:8099/health
  agent-browser screenshot /tmp/health.png
  agent-browser snapshot

  kill %1
'
```

## Podman Setup

```bash
# Create VM (14GB minimum for Playwright builds)
podman machine init --disk-size 14 --memory 4096
podman machine start

# Build
podman build -f e2e/Dockerfile -t loomcli-e2e .

# Clean up disk space
podman system prune -a --force
```

## Architecture

The Dockerfile uses a 3-stage build with cache mounts to minimize image size:

```
Stage 1: golang:bookworm (builder)
  └── Compiles loom as a static binary (CGO_ENABLED=0)
  └── Cache mounts: /go/pkg/mod, /root/.cache/go-build

Stage 2: node:22-alpine (frontend)
  └── Builds frontend dist/ only (node_modules cache-mounted)

Stage 3: node:22-alpine (runtime)
  └── Alpine chromium + agent-browser + @playwright/test
  └── Copies static binaries from builder
  └── Copies dist/ from frontend
  └── Source tree for go test (when Go is available)
```

Cache mounts keep Go modules (~500MB) and build cache (~500MB) out of committed layers. Alpine Chromium replaces Playwright's download + X11 deps, saving ~500MB.

## Smoke Tests

Two verification scripts are included:

```bash
podman run --rm loomcli-e2e verify_todo.sh     # binaries, stubs, workspace setup, loom commands
podman run --rm loomcli-e2e verify_list.sh     # loom list behavior
```

Use `-v` for verbose or `-q` for summary only.

## Stub Configuration

Each stub backend supports env vars for testing error paths:

| Variable | Description | Default |
|---|---|---|
| `STUB_CLAUDE_EXIT_CODE` | Exit code for claude stub | `0` |
| `STUB_CLAUDE_RESPONSE` | Custom response text | built-in |
| `STUB_CLAUDE_DELAY` | Sleep seconds before responding | `0` |
| `STUB_CODEX_*` | Same as above for codex | |
| `STUB_OPENCODE_*` | Same as above for opencode | |

```bash
podman run --rm -e STUB_CLAUDE_EXIT_CODE=1 loomcli-e2e run_test.sh --phase smoke
```

## Go Test Harness (CI)

Run container tests from the host via Go's test framework:

```bash
go test -tags container -v -timeout 15m ./e2e/
KEEP_IMAGE=1 go test -tags container -v -timeout 15m ./e2e/  # skip cleanup
```

## Real Backend CLIs

Mount a real CLI binary to replace a stub:

```bash
podman run --rm -v $(which claude):/usr/local/bin/claude \
  -e ANTHROPIC_API_KEY=sk-... loomcli-e2e
```

## Epic-runner lanes (need sibling checkouts)

Two extra podman drivers live here and are **not** covered by the phases above.
Both build a real `fleet-db` binary from the sibling checkout and exit 1 when it
is missing (`run_epic_runner_codex_podman.sh:18-23`). Supplying a prebuilt
`FLEET_DB_BIN` skips the build and the checkout requirement.

| Script | What it runs |
|---|---|
| `run_epic_runner_codex_podman.sh` | Codex-backed epic runner E2E in podman (`e2e/epic_runner_codex.sh` inside the image) |
| `run_epic_runner_real_codex_octocat_podman.sh` | Real Codex against `octocat/Hello-World`; also needs `flue` and a `CODEX_HOME` |

Their checkout defaults are inconsistent — set both env vars explicitly:

- `run_epic_runner_codex_podman.sh:8` → `FLEET_DB_REPO=<repo>/../../fleet-db`
- `run_epic_runner_real_codex_octocat_podman.sh:8,10` → `FLEET_DB_REPO=<repo>/../../fleet-db`
  but `FLUE_REPO=<repo>/../flue` (different depth, same file)

```bash
FLEET_DB_REPO=/path/to/fleet-db FLUE_REPO=/path/to/flue \
  ./e2e/run_epic_runner_codex_podman.sh
```

`e2e/Dockerfile.base` is a slower-moving toolchain base image (Go + Node +
Playwright deps); rebuild it only when toolchain versions change.

## Related

- [`../docs/testing/README.md`](../docs/testing/README.md) — index of every test
  surface and which one to reach for
- [`../docs/testing-terminology.md`](../docs/testing-terminology.md) — mandatory
  before running anything slow or irreversible; defines `real` vs `live` here
- [`../test/local-mode/README.md`](../test/local-mode/README.md) — full-stack
  dogfood stack (this container is not that)
- [`../deploy/podman-stack/README.md`](../deploy/podman-stack/README.md) —
  distributed-topology platform e2e
- [`../AGENTS.md`](../AGENTS.md) — sibling-checkout setup and the terminology
  handshake
