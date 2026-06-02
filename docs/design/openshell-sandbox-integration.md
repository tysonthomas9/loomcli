# OpenShell Sandbox Integration

Run loom AI agents (Claude, Codex, OpenCode) inside isolated OpenShell containers
instead of directly on the host.

**Status:** Implemented on `falcon` branch
**Backend:** [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) (K3s-in-Docker, OPA/Rego policy, credential proxy)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  Host Machine                                                   │
│                                                                 │
│  ┌──────────────┐     ┌──────────────────────────────────────┐  │
│  │ loom daemon   │────▶│ ExecutionStrategy interface          │  │
│  │ (supervisor)  │     │                                      │  │
│  │               │     │  ┌─────────────┐ ┌────────────────┐ │  │
│  │ superviseAgent│     │  │DirectStrategy│ │SandboxStrategy │ │  │
│  │ waitForAgent  │     │  │ (default)    │ │ (OpenShell)    │ │  │
│  │ stopAgent     │     │  └─────────────┘ └───────┬────────┘ │  │
│  └──────────────┘     └───────────────────────────┼──────────┘  │
│                                                    │             │
│         git push ◀──────────────────────┐          │             │
│         git fetch ─────────────────┐    │          │             │
│                                    │    │          ▼             │
│  ┌─────────────────────────────────┼────┼──────────────────┐    │
│  │  OpenShell Sandbox (Docker)     │    │                  │    │
│  │                                 │    │                  │    │
│  │  /sandbox/bin/loom  (uploaded)  │    │                  │    │
│  │  /sandbox/repo/     (cloned) ◀──┘    │                  │    │
│  │                                      │                  │    │
│  │  agent works on code ────────────────┘                  │    │
│  │  bd sync + git push      (results pushed back)         │    │
│  │                                                         │    │
│  │  Network: OPA policy (ports 443, 80, 22)               │    │
│  │  Credentials: proxy-injected (--provider)               │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

## Core Design: Git-Native Workflow

The integration uses git as the transport layer, eliminating file sync complexity:

```
 Host                          Sandbox
  │                              │
  │  1. git push (branch)        │
  │─────────────────────────────▶│
  │                              │  2. git clone --single-branch
  │                              │  3. loom task <worktree> --auto
  │                              │  4. agent does work...
  │                              │  5. bd sync
  │                              │  6. git add -A && git commit
  │  7. git fetch origin branch  │  7. git push origin branch
  │◀─────────────────────────────│
  │  8. git merge --ff-only      │
  │  9. sandbox delete           │
  │                              │
```

Beads state (`.beads/issues.jsonl`) travels through git — no separate sync needed.

## ExecutionStrategy Interface

`internal/cli/execution_strategy.go`

```go
type ExecutionStrategy interface {
    Spawn(ap *AgentProcess, loomArgs []string, env []string, logFile *os.File) (*exec.Cmd, error)
    Kill(ap *AgentProcess)
    Cleanup(ap *AgentProcess) error
    Name() string
}
```

Two implementations:

| Method      | DirectStrategy (default)         | SandboxStrategy (OpenShell)                  |
|-------------|----------------------------------|----------------------------------------------|
| `Spawn`     | `exec.Command("loom", ...)`      | `exec.Command("openshell", "sandbox", ...)` |
| `Kill`      | SIGTERM → SIGKILL to pgroup      | SIGTERM to pgroup + `sandbox delete`         |
| `Cleanup`   | no-op                            | `git fetch` + `git merge --ff-only` + delete |

**Key constraint:** `Spawn` returns `*exec.Cmd` — the caller assigns `ap.cmd`/`ap.pid` while holding `ap.mu`. Strategy implementations must NOT acquire `ap.mu`.

## File Layout

```
internal/cli/
├── execution_strategy.go      # ExecutionStrategy interface + DirectStrategy
├── sandbox_strategy.go        # SandboxStrategy (Spawn/Kill/Cleanup)
├── sandbox_config.go          # overlaySandboxConfig, mergeSandboxConfig, resolveStrategy
├── sandbox_policy.go          # Default "open" policy YAML constant
├── sandbox_oneshot.go         # --sandbox flag for loom task / loom plan
├── sandbox_strategy_test.go   # Unit tests (args, config, quoting, validation)
├── sandbox_lifecycle_test.go  # Mock-based lifecycle tests (spawn, kill, cleanup)
├── sandbox_mock_test.go       # Mock openshell shell script helpers
└── sandbox_integration_test.go # Live Docker integration tests (build tag: integration)
```

Modified files:
- `daemon.go` — `AgentProcess` gains `strategy ExecutionStrategy` + `sandboxName string`
- `daemon_spawn.go` — `spawnAgent` delegates to `strategy.Spawn()`, `waitForAgent` calls `strategy.Cleanup()`
- `daemon_health.go` — `stopAgent` delegates to `strategy.Kill()`
- `daemon_hotreload.go` — `addAgent` uses `resolveStrategy()`
- `daemon_config.go` — `SandboxConfig` struct, `Execution`/`Sandbox` fields on `AgentEntry`
- `config_validate.go` — Validates execution field and sandbox provider requirements
- `task.go`, `plan.go` — `--sandbox` flag for one-shot mode

## Configuration

### loom.yaml

```yaml
daemon:
  sandbox:                    # daemon-level defaults
    providers: [claude, github]
    network: open             # "open" or path to custom policy YAML
    from: ubuntu:22.04        # container image (--from)
    backend: claude           # override backend inside sandbox

agents:
  - worktree: falcon
    role: task
    execution: sandbox        # "direct" (default) | "sandbox"
    sandbox:                  # per-agent override (merged with daemon defaults)
      providers: [claude, github, npm]
      backend: codex
```

Config resolution: `mergeSandboxConfig(daemon.Sandbox, agent.Sandbox)` — agent fields win when set.

### Strategy Resolution

```
resolveStrategy(agent, daemonSandbox, projectDir)
  ├── agent.Execution == "sandbox" → SandboxStrategy{merged config, projectDir, repoURL}
  └── otherwise                    → DirectStrategy{}
```

## Sandbox Bootstrap Script

Generated by `buildSandboxCommand()` / `buildOneshotCommand()`:

```sh
set -e
chmod +x /sandbox/bin/loom
export GIT_SSL_NO_VERIFY=1
git clone --branch <branch> --single-branch <repoURL> /sandbox/repo
cd /sandbox/repo
git config user.name "loom-sandbox"
git config user.email "loom-sandbox@local"
/sandbox/bin/loom task worktrees/<branch> --auto --daemon-mode [--backend <backend>]
bd sync
git add -A
git diff --cached --quiet || git commit -m "sandbox agent work [<branch>]"
git push origin <branch>
```

**GIT_SSL_NO_VERIFY:** Required because the OpenShell proxy intercepts HTTPS but its CA cert is not in the container trust store.

## Two Execution Modes

### 1. Daemon Mode (supervised)

The daemon spawns sandboxed agents via `SandboxStrategy`. The supervisor loop is unchanged — it calls `Spawn`, monitors the process, calls `Cleanup` on exit:

```
loom daemon start  →  superviseAgent loop
                          │
                          ├── pushWorktreeBranch()  (hard error on failure)
                          ├── strategy.Spawn()       → openshell sandbox create ...
                          ├── waitForAgent()          → cmd.Wait()
                          ├── strategy.Cleanup()      → git fetch + merge + delete
                          └── restart loop
```

Pre-spawn push is a hard error — if the branch can't be pushed, the sandbox can't clone it.

### 2. One-Shot Mode (interactive)

```
loom task <worktree> --sandbox
loom plan <worktree> --sandbox
```

Runs a single agent invocation in a sandbox with inherited stdio (interactive TTY). Lifecycle:

1. Push current branch to origin
2. Create sandbox (with TTY — no `--no-tty`)
3. Wait for completion
4. Fetch + fast-forward merge changes back
5. Delete sandbox (deferred cleanup on all exit paths)

`--sandbox` and `--auto` are mutually exclusive.

## OpenShell CLI Surface

Flags used by this integration:

| Flag | Usage |
|------|-------|
| `--name` | Unique sandbox name: `loom-<worktree>-<unix_ms_hex>` |
| `--upload <src>:<dir>` | Single value. Destination is a directory, file keeps its name |
| `--from <image>` | Container base image |
| `--provider <name>` | Repeatable. Credential proxy injection (e.g., `claude`, `github`) |
| `--policy <file>` | OPA/Rego policy YAML. Skipped for "open" network (avoids provisioning issues) |
| `--no-tty` | Disable PTY allocation (daemon mode only) |
| `-- <cmd>` | Trailing command to execute inside sandbox |

**Discovered limitations:**
- `--upload` accepts only one value (not repeatable)
- `--env`, `--network`, `--image` flags don't exist; use `--from` for images
- `--policy` can cause sandbox provisioning hangs in some OpenShell versions — skip for default "open" policy

## Network Policy

Default "open" policy (`sandbox_policy.go`):

```yaml
version: 1
filesystem_policy:
  include_workdir: true
  read_only: [/usr, /lib, /etc, /proc, /dev/urandom, /opt, /var/log]
  read_write: [/sandbox, /tmp, /dev/null, /home]
network_policies:
  allow_all:
    endpoints:
      - { host: "**", ports: [443, 80, 22] }
    binaries:
      - { path: "/**" }
```

Custom policies can be specified via `network: ./path/to/policy.yaml` in config.

## Test Strategy

Three tiers, all run under `make gate`:

### Unit Tests (`sandbox_strategy_test.go`)
- Argument building, shell quoting, config merging, validation
- No external dependencies

### Mock Lifecycle Tests (`sandbox_lifecycle_test.go`)
- Shell script mock of `openshell` binary (`sandbox_mock_test.go`)
- NUL-delimited log format for CI-safe arg verification
- Tests: spawn, kill, cleanup, failure modes, race conditions

### Integration Tests (`sandbox_integration_test.go`)
- Build tag: `//go:build integration`
- Require Docker + OpenShell gateway running
- Tests: upload+run, git clone, upload path format, sandbox cleanup
- Run with: `go test -run TestSandboxIntegration -tags integration -timeout 300s`

## Concurrency and Locking

- `AgentProcess.mu` protects `cmd`, `pid`, `sandboxName`
- `Spawn()` called outside `ap.mu` — returns `*exec.Cmd`, caller assigns fields under lock
- `Cleanup()` locks `ap.mu` to read+clear `sandboxName`, then releases before I/O
- `configSnapshot()` called before `ap.mu.Lock()` to avoid lock-order issues with config mutex

## Config Validation

`config_validate.go` checks:
- `execution` field must be `""`, `"direct"`, or `"sandbox"`
- Sandbox agents require `providers` (error, not warning)
- Warns if `openshell` binary not on PATH
- Warns if no git remote URL can be resolved
