# Fleet-DB Acceptance Gates

> **Status:** Partially implemented — the gate *contract* below is current and
> still the vocabulary used in issue handoffs; the target commands that shipped
> are named in the "Today's command" column, and the ones that never shipped are
> marked so. *audited 2026-08-03*

**History:** this file was written 2026-05-01 as the gate contract for the
beads→FleetDB migration epic (`loomcli-wpltp`), deleted 2026-05-03 in
`ba162d458` ("Remove legacy beads compatibility artifacts"), and restored here
because two docs still instruct contributors to name a gate from it
([README.md](README.md), [go-backend-tests.md](go-backend-tests.md)). The
migration has landed — FleetDB is the canonical issue store — so the
beads-comparison halves of G1/G2 and the deletion lint G8 are history, kept
below because they record *why* the rules exist.

## Purpose

A gate is a named, runnable claim about one surface. When a change touches a
surface, its handoff should name the gate it extended and include the command
output. The gate names are stable; the commands behind them move.

## Global rules

These are the durable part of this document. They still hold.

- A fleet-db-only gate must fail if a `bd` binary or beads daemon is required
  for the tested workflow. Enforced today by `scripts/check-no-beads-prod.sh`,
  wired in as step 10 of `check-go` (`Makefile:523-524`).
- A gate that means to pin the backend does so with `LOOM_ISSUE_BACKEND`:
  `fleetdb` for embedded local mode, `fleet` for remote mode (`api` is the
  third accepted value). The var is the highest-precedence input to
  `ResolveIssueBackendType` and its default is `fleetdb`
  (`internal/cli/issue_backend_resolve.go:18-20,30-39`). Note it is **not**
  `LOOM_BACKEND`, which selects the AI backend (`internal/cli/fleet_mode.go:18-20`).
- **Silent fallback is a failure even when the visible workflow succeeds.**
  This is the reason G0 exists: a fleet-db test that passes because the
  resolver fell back to something else is a false positive.
- A failing gate must leave enough artifact behind to triage without an
  immediate rerun.
- Naming the gate in the handoff is the point. "Tests pass" is not a gate.

## Gate matrix

| Gate | Owns | Today's command | Status |
|---|---|---|---|
| G0: resolver fail-closed | Backend selection, config validation, no silent fallback | `go test ./internal/cli/... -run 'Test.*(IssueBackend\|Fleet\|Config\|Deps)'` ([go-backend-tests.md](go-backend-tests.md)) | Live. `make test-fleetdb-resolver` was proposed and never added. |
| G1: backend/CLI parity | `IssueBackend` methods and CLI issue/task workflows | `go test ./internal/backend/... ./internal/cli/...` | Live as ordinary package tests. `make test-fleetdb-cli` and the beads comparison oracle `make test-parity-cli` do not exist. |
| G2: browser regression | Kanban/table/detail/graph/settings issue UI on a FleetDB-only stack | `make test-fleetdb-ui` (`Makefile:113`) against the stack in `test/fleetdb/` | Live under a different name than the original `make test-fleetdb-browser`. |
| G3: SSE realtime | Reconnect, replay, filtering, backpressure | `make test-e2e-real-smoke` / `test-e2e-real-regression` (`Makefile:417`, `:427`) — see `tests/e2e/integration/sse-updates.integration.spec.ts` and `sse-multiclient.integration.spec.ts` | Live. `make test-fleetdb-sse` does not exist. |
| G4: workspace lifecycle | Workspace list/default/create/clone/delete, repo groups, roles | `tests/e2e/integration/workspace-lifecycle.integration.spec.ts` + `cross-workspace-move.integration.spec.ts`; manual plans [e2e-cli.md](e2e-cli.md) and [e2e-ui.md](e2e-ui.md) | Partly automated. `make test-fleetdb-workspace` does not exist. |
| G5: local supervisor | Supervisor identity, claims, task lifecycle, sessions, control | `make test-fleetdb-supervisor` (`Makefile:94`) | Live, with a pinned `-run` regex — see [go-backend-tests.md](go-backend-tests.md). |
| G6: embedded local mode | Clean-checkout embedded startup, persistence, crash recovery | `make test-fleetdb-embedded` (`Makefile:90`) → `scripts/test-fleetdb-clean-checkout.sh`; new-user CLI scenarios via `make test-fleetdb-empty-cli` (`Makefile:126`) | Live. |
| G7: remote distributed mode | Multi-process, multi-actor fleet-db mode | `make test-distributed-smoke` (`Makefile:237`) → `test/distributed/` | Live under a different name than the original `make test-fleetdb-distributed`. |
| G8: deletion lint | No active beads dependency in production code | `./scripts/check-no-beads-prod.sh` (`Makefile:523-524`) | Live under a different name than the original `make check-no-beads-runtime`. |

## Gate details

Kept because they enumerate *what has to be true*, which outlives whichever
command runs it.

### G0 — resolver fail-closed

The first safety gate. It prevents false positives where a fleet-db test passes
because the resolver fell back to something else.

- Empty config defaults to fleet-db.
- Explicit `fleetdb` and `fleet` modes construct the intended backend.
- A missing fleet-db URL or a failed construction returns a clear error, not a
  fallback.
- Serve startup does not start or stop a beads daemon.

### G1 — backend/CLI parity

- CRUD lifecycle: create, get, update, close, reopen, delete.
- Queues: list, ready, blocked, search, stats, count, children.
- Metadata: labels, owner, assignee, priority, due, defer/undefer.
- Graph: add/remove dependencies and parent/child relationships.
- Collaboration: comments and events.
- Errors: not found, invalid transitions, dependency cycles, malformed batches.
- Claims: owner/actor semantics, contention, stale claim handling.

### G2 — browser regression

Proves the product UI works when fleet-db is the only issue backend:
Kanban/table screens including create/update/status change; issue detail with
comments, events, labels, dependencies, graph context; agent views and queue;
terminal view; file explorer and git diff; settings without beads terminology.

### G3 — SSE realtime

- Initial connection receives the expected current workspace state.
- Reconnect with mutations during the gap replays missed events exactly once.
- Workspace and repo filters suppress unrelated mutations.
- Slow clients do not block fast clients or grow memory unboundedly.
- More than one `loom serve` process against one fleet-db instance
  (`test/distributed/`).

### G4 — workspace lifecycle

- List/default/use/show/status read from fleet-db-backed state.
- Create-existing-dir and clone lifecycles persist into fleet-db.
- Delete/deregister removes the workspace cleanly.
- Repo groups, agent definitions, roles and daemon profiles load from
  fleet-db-backed state.
- The browser workspace selector and `loom workspace list` agree.

### G5 — local supervisor

- Supervisor registers a durable local identity in fleet-db.
- The ready-task loop claims through fleet-db.
- Task lifecycle transitions persist through fleet-db.
- Session and log metadata survive supervisor restart.
- Control commands work through the fleet-db-backed control channel.
- Long-lived agents run for a bounded duration without leaking claims or
  losing heartbeats.

### G6 — embedded local mode

- A fresh checkout runs local mode with no external fleet-db.
- Binary-discovery failures produce actionable diagnostics
  (`internal/bootstrap/embedded.go:526-533` documents the four-step resolution
  order).
- Process reuse works across repeated starts; stale PID/socket files are
  cleaned up.
- Crash/restart preserves issue, workspace, session and supervisor state.

### G7 — remote distributed mode

- Multiple actors authenticate or identify distinctly.
- Audit metadata records the correct actor per mutation.
- Two supervisors contending for one ready issue produce exactly one winner.
- Stale workers are detected and claims recovered.
- Workspace scoping prevents cross-workspace reads, writes and SSE events.

### G8 — deletion lint (history)

The beads deletion phase is complete; the guard remains as a ratchet. It
rejects new production `bd`/beads references outside an explicit allowlist:
no `exec.Command("bd", …)`, no beads backend import from production code, no
serve path starting a beads daemon, no UI or agent prompt telling a user to
run `bd`.

## Merge policy (history)

The original epic required this pairing. It is recorded because the shape is
still a reasonable default for which gate to run when.

| Change | Required gates |
|---|---|
| Fleet-db backend behavior | G0 + the relevant slice of G1 |
| Issue UI behavior | G0, G1 slice, G2 slice |
| SSE behavior | G0, G3 |
| Workspace lifecycle | G0, G4 |
| Local supervisor behavior | G0, G5 (+ G6 if embedded startup is touched) |
| Remote/distributed behavior | G0, G7 |

## Related

- [README.md](README.md) — testing docs index
- [go-backend-tests.md](go-backend-tests.md) — Go test surfaces and the commands behind G0/G1/G5/G6
- [../testing-terminology.md](../testing-terminology.md) — what "gate" means in each of its four senses
