# loomcli E2E Test Container

Alpine-based container for running loomcli E2E tests in isolation.
Includes Go binaries, Chromium, Playwright, agent-browser, and stub backends.

**Image size:** ~1.5GB | **Min Podman VM disk:** 14GB

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

## Real Backend CLI Session Smoke

Use this when you need to verify Loom's backend invocation, session metadata,
native transcript mirroring where supported, token capture, and backend-reported
cost with the actual installed CLIs instead of stubs:

```bash
make test-real-clis

# Narrow to one backend and keep the temp session root for inspection
scripts/test-real-clis.sh --backend claude --keep

# Run a subset with a longer per-backend timeout
scripts/test-real-clis.sh --backends "codex opencode" --timeout 5m
```

This is intentionally opt-in because it invokes real `claude`, `codex`, and
`opencode` binaries and can spend real tokens. The test logs the temporary root,
Loom `metadata.json`, `agent_transcript.jsonl` path when captured, token totals,
cost, and model for each backend.

Useful knobs:

| Variable / flag | Purpose |
|---|---|
| `LOOM_REAL_CLI_BACKENDS` / `--backends` | Backends to run, default `claude,codex,opencode` |
| `LOOM_REAL_CLI_PROMPT` / `--prompt` | Prompt sent to each backend |
| `LOOM_REAL_CLI_TIMEOUT` / `--timeout` | Per-backend timeout, default `3m` |
| `LOOM_REAL_CLI_KEEP` / `--keep` | Keep the temporary root for manual inspection |
| `LOOM_REAL_CLI_ROOT` / `--root` | Write smoke artifacts to a specific directory |
| `LOOM_REAL_CLI_SKIP_MISSING` / `--skip-missing` | Skip missing selected CLI binaries |
| `LOOM_REAL_CLI_REQUIRE_COST` / `--require-cost` | Require non-zero cost for every selected backend |

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

Stage 2: node:20-alpine (frontend)
  └── Builds frontend dist/ only (node_modules cache-mounted)

Stage 3: node:20-alpine (runtime)
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
