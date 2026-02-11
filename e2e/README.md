# loomcli E2E Test Container

Multi-stage Docker image for running loomcli E2E tests in isolation.

## Build

```bash
docker build -f e2e/Dockerfile -t loomcli-e2e .
```

## Run

```bash
# All E2E tests
docker run loomcli-e2e

# Specific test
docker run loomcli-e2e go test -tags e2e -v -run TestE2E_TmuxSessionLifecycle ./internal/cli/
```

## Local Development (run_local.sh)

The `run_local.sh` script wraps the build and run workflow with sensible defaults:

```bash
# Build and run all E2E tests
e2e/run_local.sh

# Skip rebuild, run specific test
e2e/run_local.sh --no-build -- go test -tags e2e -v -run TestE2E_Foo ./internal/cli/

# Mount real CLI binaries from host
e2e/run_local.sh --mount-clis

# Set a specific backend
e2e/run_local.sh --backend codex

# See all options
e2e/run_local.sh --help
```

The script auto-detects and mounts auth config directories (`~/.claude/`, `~/.codex/`, `~/.config/opencode/`) read-only, and forwards `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, and any `STUB_*` environment variables from the host.

## Smoke Test

Run the built-in smoke test to verify the container is correctly configured:

```bash
docker run loomcli-e2e verify_todo.sh
```

This verifies: binary existence, stub output, bd task CRUD, loom commands, lock files, and signal files.

Use `-v` for detailed output or `-q` for summary only:

```bash
docker run loomcli-e2e verify_todo.sh -v
docker run loomcli-e2e verify_todo.sh -q
```

## Test Orchestrator

The container includes `run_test.sh` which runs the full test suite:

```bash
# Full suite (smoke + unit + e2e across all backends)
docker run loomcli-e2e

# E2E tests only
docker run loomcli-e2e run_test.sh --phase e2e

# Single backend
docker run loomcli-e2e run_test.sh --backend claude

# Pass extra go test flags
docker run loomcli-e2e run_test.sh -- -run TestE2E_TmuxSession
```

## Real Backend CLIs

Mount a real CLI binary to replace a stub:

```bash
docker run -v $(which claude):/usr/local/bin/claude loomcli-e2e
```

Pass API keys via environment:

```bash
docker run -e ANTHROPIC_API_KEY=sk-... loomcli-e2e
```

## Stub Configuration

Each stub backend supports environment variables for testing error paths and custom responses:

| Variable | Description | Default |
|---|---|---|
| `STUB_CLAUDE_EXIT_CODE` | Exit code for claude stub | `0` |
| `STUB_CLAUDE_RESPONSE` | Custom response text | built-in |
| `STUB_CLAUDE_DELAY` | Seconds to sleep before responding | `0` |
| `STUB_CODEX_EXIT_CODE` | Exit code for codex stub | `0` |
| `STUB_CODEX_RESPONSE` | Custom response text | built-in |
| `STUB_CODEX_DELAY` | Seconds to sleep before responding | `0` |
| `STUB_OPENCODE_EXIT_CODE` | Exit code for opencode stub | `0` |
| `STUB_OPENCODE_RESPONSE` | Custom response text | built-in |
| `STUB_OPENCODE_DELAY` | Seconds to sleep before responding | `0` |

Example:

```bash
docker run -e STUB_CLAUDE_EXIT_CODE=1 loomcli-e2e go test -tags e2e -v -run TestE2E_BackendError ./internal/cli/
```
