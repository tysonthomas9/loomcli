# OpenShell Sandbox Integration — port onto v5

Restores Tyson's **OpenShell sandbox integration** (PR #20, originally on `falcon`)
onto v5. Branch: `feat/sandbox-openshell-v5` (off `v5`).

## Status

| Slice | State |
|-------|-------|
| **One-shot `loom task/plan <wt> --sandbox`** | ✅ **Implemented** — builds, `go vet` clean, unit-tested, `golangci-lint` 0 issues |
| Daemon-mode `execution: sandbox` (supervised agents) | ⛔ **Deferred** — needs FleetDB/domain config plumbing (see below) |

## What's implemented (one-shot)

`loom task <worktree> --sandbox` / `loom plan <worktree> --sandbox` runs a single
agent invocation inside an OpenShell container instead of on the host:

1. Push the worktree branch to origin (`--force-with-lease`)
2. `openshell sandbox create` — uploads the `loom` binary, clones the branch, runs
   `loom <task|plan> worktrees/<name>` inside the container, then commits and pushes code back
   (no `bd sync` — v5 task state is FleetDB-backed, not in the repo)
3. Host fetches + `git merge --ff-only` the pushed results back
4. Deletes the sandbox (deferred cleanup on all exit paths)

`--sandbox` is mutually exclusive with `--auto`. Network defaults to the sandbox's
built-in "open" policy (common ports); providers default to `claude,github`.

**FleetDB connectivity:** because v5 task state is in FleetDB (not git), the bootstrap exports
`LOOM_SERVER_URL` + `LOOM_WORKSPACE` so the in-container agent uses the loom-serve HTTP API to
claim/update work. The host resolves the URL from `LOOM_SANDBOX_SERVER_URL` (explicit) or a
localhost→`host.docker.internal` rewrite of `LOOM_SERVER_URL`; it **fails fast** if no reachable
server/workspace is found. Remaining environment caveats (host-gateway address, OPA port for a
non-443/80/22 serve, headless auth) are tracked in `docs/design/sandbox-daemon-port.md` §D — not
yet verified against a live gateway.

Files:
```
internal/cli/agent/sandbox_oneshot.go        # all one-shot logic (self-contained)
internal/cli/agent/sandbox_oneshot_test.go   # pure-function unit tests
internal/cli/agent/task.go, plan.go          # --sandbox flag + dispatch (3-line edits each)
docs/design/openshell-sandbox-integration.md # original design doc (full, incl. daemon mode)
```

## Provenance / recovery

The original code was **force-pushed off `falcon`** on 2026-03-27 and exists in no
current branch. Recovered from the dangling commit and pinned:

- Rescue tag: **`rescue-sandbox-openshell-pr20`** → `96dacab127624d660d0e01d614ef8b36e4b8755d`
- The **daemon-mode** files (ExecutionStrategy/DirectStrategy/SandboxStrategy +
  lifecycle/mock tests) were intentionally **removed from this branch** (they were
  written against the v2-era monolithic daemon and don't compile on v5). Retrieve
  them from the rescue tag when doing the daemon port:
  ```
  git checkout rescue-sandbox-openshell-pr20 -- internal/cli/execution_strategy.go internal/cli/sandbox_strategy.go
  ```

## Deferred: daemon-mode sandbox (the larger piece)

v5 replaced `loom.yaml` with a **FleetDB/domain-backed config store**: `config.AgentEntry`
is built from `domain.Agent` via `agentEntryFromDomain`, and `LoadProjectFile`/`ProjectFile`
no longer exist. So letting *supervised* agents declare `execution: sandbox` requires
plumbing new fields through the domain model + store (+ likely the separate `fleet-db`
server repo), not just a struct field. Seam map for that work:

- [ ] **AgentProcess fields** — add `Strategy`/`SandboxName` → `internal/cli/daemon/supervisor/types.go`
- [ ] **Spawn** — branch to a sandbox command in `internal/cli/daemon/supervisor/spawn.go`
      `buildCommand`/`buildAgentExecCmd` (v5 splits build from start; adapt the strategy
      to a `BuildSpawnCommand(ap) (*exec.Cmd, error)` shape rather than build-and-start)
- [ ] **Cleanup** — call after `waitForAgent` in `spawn.go` (fetch + ff-merge + delete)
- [ ] **Kill** — append sandbox delete in `internal/cli/daemon/supervisor/health.go` `StopAgent`
- [ ] **resolveStrategy** at agent creation → `supervisor/supervisor.go` (`ap := &AgentProcess{…}`, ~L114)
- [ ] **Config** — add `Execution`/`Sandbox` to `config.AgentEntry`/`DaemonSettings`
      (`internal/cli/config/project.go`) **and** to `domain.Agent`/`domain.DaemonProfile`
      + `agentEntryFromDomain`/`daemonSettingsFromDomain` + the FleetDB store (+ fleet-db server)
- [ ] **Validation** — `execution` ∈ {"","direct","sandbox"}; sandbox requires providers

## Build / test

- `go build ./...` · `go vet ./internal/cli/agent/...` · `golangci-lint run ./internal/cli/agent/...`
- `LOOM_CONFIG_DIR=$(mktemp -d) go test ./internal/cli/agent/ -run TestSandbox -run 'Oneshot|ShellQuote|DefaultSandbox'`
- Live use needs `openshell` on PATH + a running OpenShell gateway (Docker).
