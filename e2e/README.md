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
