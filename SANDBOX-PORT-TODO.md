# OpenShell Sandbox Integration — port onto v5

This branch (`feat/sandbox-openshell-v5`, off `v5` @ `898af31b`) restores Tyson's
**OpenShell sandbox integration** (PR #20, originally on `falcon`) so it can be
re-applied to the current v5 daemon.

> ⚠️ **This tree does not compile yet.** Only the *self-contained* sandbox files
> have been dropped in (at their original `internal/cli/` paths). The 8
> integration-seam edits still need to be re-applied by hand, because v5
> refactored the monolithic `internal/cli/daemon_*.go` into the
> `internal/cli/daemon/` + `internal/cli/daemon/supervisor/` packages.

## Provenance / recovery

The original code was **force-pushed off `falcon`** on 2026-03-27 and exists in no
current branch. It was recovered from the dangling commit and pinned:

- Rescue tag (server): **`rescue-sandbox-openshell-pr20`** → `96dacab127624d660d0e01d614ef8b36e4b8755d`
- Sandbox lineage parent: `df91e01d2` · original base: v2 `8d781c9ef` (2026-03-21)
- Full original diff: 20 files, +2688/−111. See `docs/design/openshell-sandbox-integration.md`.

(The rescue tag can be deleted once this work is merged or abandoned.)

## Why recreate (not fast-forward / merge)

`git compare v5...96dacab` = **diverged**: v5 ahead 1011, sandbox ahead 15,
merge-base v2 `8d781c9e`. The sandbox commit is **not** an ancestor of v5, so a
fast-forward is impossible; merging would drag ~2.5-month-stale v2 code backward.

## Files dropped in (self-contained, ~2,470 LOC)

```
internal/cli/execution_strategy.go        ExecutionStrategy iface + DirectStrategy
internal/cli/sandbox_strategy.go          SandboxStrategy (Spawn/Kill/Cleanup → openshell)
internal/cli/sandbox_config.go            overlay/mergeSandboxConfig, resolveStrategy
internal/cli/sandbox_policy.go            default "open" OPA/Rego network policy
internal/cli/sandbox_oneshot.go           --sandbox one-shot for task/plan
internal/cli/sandbox_{strategy,lifecycle,mock,integration}_test.go
docs/design/openshell-sandbox-integration.md
```
These will likely **move into `internal/cli/daemon/supervisor/`** (where `AgentProcess`
now lives) and have their package decl + `AgentProcess` field references updated.

## Seam port checklist — old edit → v5 location

- [ ] **AgentProcess fields** — add `Strategy ExecutionStrategy` + `SandboxName string`
      → `internal/cli/daemon/supervisor/types.go` (struct `AgentProcess`, already has `Cmd`,`Pid`)
- [ ] **Spawn delegation** — route through `strategy.Spawn()`
      → `internal/cli/daemon/supervisor/spawn.go` : `buildCommand` / `buildAgentExecCmd`
      (today: `exec.Command("loom", …)`) and `spawnAgent`
- [ ] **Cleanup on exit** — `strategy.Cleanup()` (git fetch + merge --ff-only + sandbox delete)
      → `internal/cli/daemon/supervisor/spawn.go` : `waitForAgent` (and/or `recoverAgent`)
- [ ] **Kill delegation** — `strategy.Kill()` (SIGTERM pgroup + `sandbox delete`)
      → `internal/cli/daemon/supervisor/health.go` : `(*Supervisor).StopAgent(ap, sigtermTimeout)`
- [ ] **resolveStrategy at agent creation** — pick Direct vs Sandbox when building the AgentProcess
      → `internal/cli/daemon/supervisor/supervisor.go` (`ap := &AgentProcess{ … }`, ~line 114)
      and the reconcile path `internal/cli/daemon/daemon_reconciler.go` (`reloadAndReconcile`)
- [ ] **Config structs** — add `SandboxConfig` + `Execution`/`Sandbox` fields
      → `internal/cli/config/project.go` : `DaemonSettings` (~L19), `AgentEntry` (~L93)
- [ ] **Config validation** — `execution` ∈ {"","direct","sandbox"}; sandbox requires providers;
      warn if `openshell` not on PATH / no git remote → locate validation in `internal/cli/config/`
- [ ] **`--sandbox` one-shot flag** — register on the task/plan commands
      → `internal/cli/agent/task.go` and `internal/cli/agent/plan.go` (moved from `internal/cli/`);
      enforce mutual-exclusion with `--auto`

## Build / test

- After the seam port: `make check` (golangci funlen ~50, prettier, arch). See repo CLAUDE.md.
- Integration tests are build-tagged: `go test ./internal/cli/... -run TestSandbox -tags integration -timeout 300s`
  (require Docker + an OpenShell gateway).
- Validate the original mock-lifecycle tests still model v5's spawn/kill seam after the move.
