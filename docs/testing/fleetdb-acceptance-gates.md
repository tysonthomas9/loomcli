# Fleet-DB Acceptance Gates

**Status:** Gate contract for `loomcli-wpltp`
**Date:** 2026-05-01
**Source inventory:** `docs/design/fleetdb-parity-inventory.md`

## Purpose

These gates define what "fleet-db parity complete" means before beads
fallbacks can be removed. Implementation tickets should add or extend the
target commands below, then point their acceptance criteria at the relevant
gate.

The gates are fleet-db-only unless explicitly marked "comparison". A comparison
gate may use beads as an oracle while parity is still being built, but it does
not count as a deletion gate until the same behavior is proven with beads
disabled or absent.

## Global Rules

- Fleet-db-only gates must run with `LOOM_ISSUE_BACKEND=fleetdb` for embedded
  local mode or `LOOM_ISSUE_BACKEND=fleet` for remote mode.
- Fleet-db-only gates must fail if a `bd` binary or beads daemon is required
  for the tested workflow.
- Silent fallback from fleet-db to beads is a failure even when the visible
  user workflow succeeds.
- Each gate must write an artifact under `test-results/` or `test/parity/ui/`
  when it fails, with enough context to triage without rerunning immediately.
- A ticket that claims parity for a surface must name the gate it extended and
  include the command output in the handoff.

## Gate Matrix

| Gate | Owns | Target command | Current/provisional command | Fixtures/data | Pass/fail semantics | Runtime budget | Primary tickets |
|---|---|---|---|---|---|---|---|
| G0: resolver fail-closed | Backend selection, config validation, no beads fallback | `make test-fleetdb-resolver` | `go test ./internal/cli/... -run 'Test.*(IssueBackend|Fleet|Config|Deps)'` | Temp project dirs, env/config matrix, missing fleet-db URL cases | Pass only if fleet-db modes never construct beads fallback and errors are explicit | <2 min | `loomcli-wpltp.9.1`, `loomcli-wpltp.9.2`, `loomcli-wpltp.9.4` |
| G1: backend/CLI fleet-db parity | `IssueBackend` methods and CLI issue/task workflows | `make test-fleetdb-cli` | `make test-parity-cli` for comparison, then fleet-db-only variant from `loomcli-wpltp.2.1` | `internal/backend/paritytest` fixtures plus CLI lifecycle fixtures | Pass only if CRUD, list/ready/blocked/search/stats/count, metadata, dependencies, comments/events, batch, errors, and claims match accepted fleet-db contract | <10 min local, <15 min CI | `loomcli-wpltp.2.*` |
| G2: browser fleet-db regression | Kanban/table/detail/graph/settings issue UI | `make test-fleetdb-browser` | `make test-parity-ui` for side-by-side comparison | `test/parity/seed.sh`, `test/parity/ui` specs, generated screenshots/traces | Pass only if fleet UI routes writes to fleet-db and all issue screens work without beads service | <20 min local, <30 min CI/nightly | `loomcli-wpltp.3.1`, `loomcli-wpltp.3.2`, `loomcli-wpltp.3.6` |
| G3: SSE realtime | Reconnect, replay, filtering, backpressure, scale | `make test-fleetdb-sse` | Existing SSE browser specs plus `docs/design/sse-reconnect-parity-spec.md` implementation | Multi-tab browser fixtures, mutation gap/canary issues, slow-client fixtures | Pass only if clients reconnect, replay missed mutations once, filter by workspace/repo, and tolerate slow clients | <10 min local; scale smoke may be nightly | `loomcli-wpltp.6.*` |
| G4: workspace lifecycle | Workspace list/default/create/clone/delete/repo groups/roles | `make test-fleetdb-workspace` | `docs/testing/e2e-cli.md` and `docs/testing/e2e-ui.md` manual plans until automated | Clean temp repos, existing-dir workspace, clone fixture repo, multi-workspace isolation data | Pass only if fleet-db is the source of truth and yaml/workspacemgr fallback is not needed | <15 min local | `loomcli-wpltp.4.*`, `loomcli-37h1h` |
| G5: local supervisor | Local supervisor identity, claims, task lifecycle, sessions, control | `make test-fleetdb-supervisor` | Targeted `go test ./internal/cli/daemon/... ./internal/cli/...` while coverage is built | Temp workspace with ready issues, mock/embedded fleet-db, agent session fixtures | Pass only if local agents claim and update tasks through fleet-db, sessions/log metadata persist, and control commands survive restart | <15 min local, <20 min CI | `loomcli-wpltp.5.*`, `loomcli-26v50.28` |
| G6: embedded local mode | Clean checkout local fleet-db startup, persistence, crash recovery | `make test-fleetdb-embedded` | `docs/testing/e2e-preflight.md` plus clean-checkout smoke until automated | Fresh temp checkout, embedded fleet-db data dir, PID/socket reuse fixtures | Pass only if a user can run local mode with no external fleet-db and no beads init; restart preserves data | <10 min local | `loomcli-wpltp.7.*`, `loomcli-26v50.29`, `loomcli-js8ni` |
| G7: remote distributed mode | Multi-user/multi-supervisor fleet-db mode | `make test-fleetdb-distributed` | Compose smoke from `loomcli-wpltp.8.5` once added | Fleet-db service, multiple loom processes, multiple actors, contention/stale-worker scenarios | Pass only if auth/audit, claim contention, heartbeats, stale workers, and workspace isolation hold across processes | <20 min local; larger scale nightly | `loomcli-wpltp.8.*` |
| G8: deletion lint | No active beads dependency after deletion phase | `make check-no-beads-runtime` | `rg`-based audit until lint script lands | Repo source, generated frontend bundle excluded unless explicitly checked | Pass only if runtime code has no `bd` subprocess, beads backend fallback, beads daemon pool, or user-facing beads terminology | <1 min | `loomcli-wpltp.9.5`, `loomcli-wpltp.10.*` |

## Gate Details

### G0: Resolver Fail-Closed

This is the first safety gate. It prevents false positives where a fleet-db test
passes because the resolver fell back to beads.

Required cases:

- Empty config defaults to fleet-db only after `loomcli-wpltp.9.1`.
- Explicit `fleetdb` and `fleet` modes construct the intended backend.
- Missing fleet-db URL or failed fleet-db construction returns a clear error.
- `WorkspaceAwareIssueBackend` does not fall back to `DefaultIssueBackend` when
  fleet-db setup fails.
- Serve startup does not start or stop `bd daemon` in fleet-db modes.

### G1: Backend/CLI Fleet-DB Parity

This gate owns typed backend behavior and command workflows. It should start as
a comparison gate using the existing parity harness, then graduate to a
fleet-db-only regression gate.

Required operation groups:

- CRUD lifecycle: create, get, update, close, reopen, delete.
- Queues: list, ready, blocked, search, stats, count, children.
- Metadata: labels, owner, assignee, priority, due, defer/undefer.
- Graph: add/remove dependencies and parent/child relationships.
- Collaboration: comments and events.
- Error behavior: not found, invalid transitions, bad dependency cycles,
  malformed batch operations.
- Claims: owner/actor semantics, contention, stale claim handling.

### G2: Browser Fleet-DB Regression

This gate proves the product UI works when fleet-db is the only issue backend.
The current side-by-side suite remains useful for comparison, but the deletion
gate must run without the beads service.

Required views:

- Kanban/table issue screens, including create/update/status changes.
- Issue detail: comments, events, labels, dependencies, graph context.
- Agent views and queue.
- Terminal view.
- File explorer and git diff.
- Settings/backend indicators without user-facing beads terminology.

### G3: SSE Realtime

This gate owns event delivery. The existing durable cursor work is not enough
by itself; deletion requires replay and stress behavior to be explicit.

Required cases:

- Initial connection receives the expected current workspace state.
- Browser reconnect with mutations during the gap replays missed events once.
- Workspace and repo filters suppress unrelated mutations.
- Slow clients do not block fast clients or unboundedly grow memory.
- Distributed smoke covers more than one loom process connected to one
  fleet-db instance.

### G4: Workspace Lifecycle

Workspace behavior must be store-backed before beads deletion because workspace
state controls every other surface.

Required cases:

- List/default/use/show/status read from fleet-db-backed state.
- Create existing-dir and clone async lifecycle persist into fleet-db.
- Delete/deregister cleanup removes the workspace without bd daemon cleanup.
- Repo groups, agent definitions, roles, and daemon profiles are loaded from
  fleet-db-backed state.
- Browser workspace selector and CLI `loom workspace list` agree.

### G5: Local Supervisor

The local supervisor is retained product functionality. This gate ensures it is
not accidentally deleted with beads daemon issue plumbing.

Required cases:

- Supervisor registers a durable local identity in fleet-db.
- Ready-task loop claims through fleet-db and never shells out to `bd ready`.
- Task lifecycle transitions persist through fleet-db.
- Session and log metadata survive supervisor restart.
- Control commands work through the fleet-db-backed control channel.
- Long-lived cron/on-call/orchestrator agents can run for a bounded test
  duration without leaking task claims or losing heartbeats.

### G6: Embedded Local Mode

This is the single-user local setup gate.

Required cases:

- Fresh checkout can run `loom serve` or the equivalent local command with no
  external fleet-db and no `.beads` initialization.
- Embedded fleet-db binary discovery failures produce actionable diagnostics.
- Process reuse works across repeated starts.
- PID/socket ownership cleanup handles stale files.
- Crash/restart preserves issue, workspace, session, and supervisor state.

### G7: Remote Distributed Mode

This is the multi-user distributed setup gate.

Required cases:

- Multiple actors authenticate or identify distinctly.
- Audit metadata records the correct actor for each mutation.
- Two supervisors contending for one ready issue produce one winner.
- Stale workers are detected and claims are recovered according to the fleet-db
  contract.
- Workspace scoping prevents cross-workspace reads, writes, and SSE events.

### G8: Deletion Lint

This gate starts as an audit helper and becomes mandatory once the default is
fleet-db.

Required checks:

- No active runtime call to `exec.Command("bd", ...)` or equivalent wrapper.
- No `internal/backend/beads` import from production code.
- No web hook or subscriber requires the beads daemon pool.
- No serve path starts/stops `bd daemon`.
- No active UI or agent prompt instructs users or agents to run `bd`.
- Migration/parity-only references are either deleted in `loomcli-wpltp.10.*`
  or isolated behind explicit test/build tags.

## Merge Policy For The Epic

| Phase | Required gates |
|---|---|
| Add or change fleet-db backend behavior | G0 plus the relevant slice of G1 |
| Change issue UI behavior | G0, G1 slice, G2 slice |
| Change SSE behavior | G0, G3 |
| Change workspace lifecycle | G0, G4 |
| Change local supervisor behavior | G0, G5, and G6 if embedded startup is touched |
| Change remote/distributed behavior | G0, G7 |
| Make fleet-db default | G0 through G7 green in local/CI scope |
| Delete beads/fallback code | G0 through G8 green |
