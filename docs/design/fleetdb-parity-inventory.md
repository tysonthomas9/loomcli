# Fleet-DB Parity Inventory

**Status:** Implementation inventory
**Date:** 2026-05-01
**Epic:** `loomcli-wpltp` - Fleet-db full parity before beads removal

## Scope

This inventory roots the fleet-db parity epic in the current loomcli feature
set. The goal is not to delete beads first. The goal is to prove and complete
fleet-db parity for every production surface that currently depends on beads,
the bd daemon, or daemon-backed issue state, then remove the legacy fallback
code once the fleet-db path is the only supported path.

The retained target shape is:

- Fleet-db owns issue/task data for embedded local mode and remote distributed
  mode.
- The local agent supervisor remains. It should claim work, persist lifecycle
  state, expose control channels, and report sessions through fleet-db instead
  of through beads or bd daemon issue state.
- Browser, CLI, terminal, file explorer, git diff, agent queue, workspace
  lifecycle, and SSE must have fleet-db-only acceptance coverage before beads
  deletion begins.
- Beads remains only as a migration/parity comparison source until the deletion
  phase.

## Status Legend

| Status | Meaning |
|---|---|
| Green | Fleet-db path exists and has meaningful coverage; deletion still waits for parent gates |
| Partial | Fleet-db path exists, but semantics, coverage, or fallback behavior are incomplete |
| Missing | No usable fleet-db path for the production surface |
| Legacy | Beads-only or bd-daemon-only code that must be deleted or isolated |

## Current Surface Matrix

| Surface | Owning code | Fleet-db status | Current coverage | Deletion or completion blocker |
|---|---|---|---|---|
| Backend contract | `internal/backend/issuebackend.go` | Partial | Unit tests by backend plus parity fixtures under `internal/backend/paritytest` | Complete method-level gates in `loomcli-wpltp.2.*` |
| CRUD lifecycle | `internal/backend/fleet`, `internal/backend/beads`, `internal/webui/service` | Partial | Backend tests and browser parity cover common create/update/close paths | `loomcli-wpltp.2.2`, `loomcli-wpltp.2.7` |
| List, ready, blocked, search, stats, count | `IssueBackend` implementations, `internal/cli/task_router.go` | Partial | CLI parity fixtures exist, but fleet-db-only harness is not the default gate | `loomcli-wpltp.2.1`, `loomcli-wpltp.2.3` |
| Metadata fields: labels, owner, assignee, priority, due, defer | Backend models and conversion code | Partial | Some metadata paths covered by browser parity; unsupported field behavior needs explicit acceptance | `loomcli-wpltp.2.4` |
| Dependencies and graph | Backend dependency methods, graph/web issue views | Partial | Browser parity exercises graph basics | `loomcli-wpltp.2.5`, `loomcli-wpltp.3.2` |
| Comments and events | `ListComments`, `AddComment`, `ListEvents` implementations | Partial | Spot coverage only | `loomcli-wpltp.2.6` |
| Batch operations and error semantics | `IssueBackend.Batch`, CLI/web callers | Partial | Not enough negative-path parity | `loomcli-wpltp.2.7` |
| Claim and lock semantics | `ClaimIssue`, task router, supervisor claim flow | Partial | Local claim path exists; distributed contention is not proven | `loomcli-wpltp.2.8`, `loomcli-wpltp.5.2`, `loomcli-wpltp.8.2` |
| Mutations and realtime cursors | `GetMutations`, `WaitForMutations`, fleet subscriber | Green/Partial | Durable fleet SSE cursor tests and browser parity exist | Complete reconnect/filter/backpressure/scale gates in `loomcli-wpltp.6.*` |
| CLI backend resolver | `internal/cli/issue_backend_resolve.go`, `internal/cli/deps.go` | Legacy | Config tests cover allowed names; default is still beads | `loomcli-wpltp.9.1`, `loomcli-wpltp.9.2` |
| CLI workspace-aware backend | `internal/cli/issue_backend_workspace.go` | Legacy | Limited tests | Falls back to default/beads when fleet-db URL is absent or construction fails; fix in `loomcli-wpltp.9.2` |
| CLI issue/task commands | `internal/cli`, `internal/cli/task_router.go` | Partial | CLI parity harness exists but is not fleet-db-only acceptance | `loomcli-wpltp.2.1` through `loomcli-wpltp.2.8` |
| Agent prompts and task-driven flow | `internal/cli/agent/prompts.go`, `internal/cli/agent/*` | Legacy/Partial | Prompt text still teaches `bd ready` paths; some typed backend usage exists | `loomcli-wpltp.5.2`, `loomcli-wpltp.5.6`, `loomcli-wpltp.10.6` |
| Serve startup backend wiring | `internal/cli/serve/serve.go` | Legacy/Partial | Serve/browser tests exercise happy paths | Still ensures/stops bd daemon and only opens fleet store in selected modes; fix in `loomcli-wpltp.9.3` |
| Web app hook composition | `internal/webui/appinfra`, `internal/webui/hooks` | Partial | Browser parity suite covers issue UI under both modes | Beads pool and daemon subscriber remain active outside fleet mode; delete in `loomcli-wpltp.10.2` after `loomcli-wpltp.6.*` |
| Web issue services | `internal/webui/service` | Partial | Browser parity covers common views | `issue_move.go` still uses daemon RPC paths; complete in `loomcli-wpltp.2.2`, `loomcli-wpltp.4.*` |
| Browser issue screens | WebUI modules and handlers | Partial | Full parity browser suite recently passed side-by-side | Convert to fleet-db-only regression and expand issue coverage in `loomcli-wpltp.3.1`, `loomcli-wpltp.3.2` |
| Terminal view | `internal/webui/handlers`, terminal/tmux managers | Partial | Existing UI/e2e coverage is not fleet-db acceptance-specific | `loomcli-wpltp.3.3`, session persistence in `loomcli-wpltp.5.4` |
| File explorer and git diff | WebUI file/diff modules, agent services | Partial | Existing browser parity covers basic rendering | Add fleet-db acceptance for repo/workspace scoping in `loomcli-wpltp.3.4` |
| Agent views and queue | WebUI daemon handlers, queue callbacks, `FetchReadyIssues` | Partial | Queue can call typed backend, but daemon config/control is still legacy-shaped | `loomcli-wpltp.3.5`, `loomcli-wpltp.5.*` |
| Workspace list/default | `internal/cli/workspace`, `internal/cli/serve/workspacemgr` | Partial | Store-backed v2 commands exist; serve still has yaml/workspacemgr fallback | `loomcli-wpltp.4.1`, `loomcli-wpltp.4.6` |
| Workspace create existing-dir | `workspacemgr.CreateWorkspace`, workspace CLI | Legacy/Partial | Best-effort mirror-to-store only | `loomcli-wpltp.4.2` |
| Workspace clone async lifecycle | `workspacemgr.CloneWorkspaceAsync` and store mirror | Legacy/Partial | Browser workflow exists; persistence split across yaml/store | `loomcli-wpltp.4.3` |
| Workspace delete/deregister | `workspacemgr.DeleteWorkspace`, daemon stop cleanup | Legacy/Partial | Limited tests | Must stop bd-specific cleanup and make fleet-db the source of truth; `loomcli-wpltp.4.4` |
| Repo groups, agent definitions, roles, daemon profiles | Fleet store models and workspace config | Missing/Partial | Data-model work exists but not fully wired into active serve path | `loomcli-37h1h`, `loomcli-wpltp.4.5` |
| Local agent supervisor identity | `internal/cli/daemon/supervisor`, daemon config/state | Missing/Partial | Supervisor tests exist for local mode, not fleet-db identity registration | `loomcli-wpltp.5.1` |
| Supervisor ready-task polling | `task_router.FetchReadyIssues`, daemon supervisor loops | Partial | Uses typed backend but resolver/fallback can still land on beads | `loomcli-wpltp.5.2`, `loomcli-wpltp.9.1` |
| Task lifecycle persistence | Supervisor state, backend updates, session metadata | Partial | Local state tests exist | Persist lifecycle through fleet-db in `loomcli-wpltp.5.3` |
| Session/log metadata | Agent session services and daemon state files | Missing/Partial | Current state is file/local-daemon oriented | `loomcli-wpltp.5.4` |
| Supervisor control channel | Daemon IPC/control socket, web daemon handlers | Partial | Local control works, but not as fleet-db-backed control-plane state | `loomcli-26v50.28`, `loomcli-wpltp.5.5` |
| Long-lived and orchestrator agents | Supervisor runtime and task prompts | Missing/Partial | No durable fleet-db model for cron/on-call/orchestrator lifetimes | `loomcli-wpltp.5.6` |
| Embedded local fleet-db startup | Fleet-db bootstrap/cmdstore paths | Partial | Local smoke has coverage, but process reuse and diagnostics are incomplete | `loomcli-wpltp.7.1`, `loomcli-wpltp.7.2`, `loomcli-wpltp.7.5` |
| Embedded persistence and crash recovery | Fleet store/process ownership | Partial | Not enough crash/restart acceptance | `loomcli-wpltp.7.3`, `loomcli-wpltp.7.4` |
| Remote distributed mode | Fleet HTTP backend, fleet subscriber, remote config | Partial | Browser parity can run remote, but distributed multi-actor gates are missing | `loomcli-wpltp.8.*` |
| Multi-user auth and audit | Fleet/API backend, HTTP auth transport, actor metadata | Missing/Partial | Device/auth path exists, but multi-actor acceptance is not tied to loomcli parity | `loomcli-wpltp.8.1` |
| Distributed heartbeat and stale workers | Supervisor/fleet control-plane state | Missing/Partial | Not proven in fleet-db mode | `loomcli-wpltp.8.3` |
| Remote workspace scoping | Fleet workspace IDs, web hooks, workspace store | Partial | Risk: fleet hook can use configured fleet workspace instead of local workspace UUID | `loomcli-wpltp.8.4` |
| Fleet-db default and fail-closed mode | Resolver, config validation, serve startup | Legacy | Defaults and fallbacks still allow beads | `loomcli-wpltp.9.*` |
| Beads backend implementation | `internal/backend/beads`, `internal/cli/cli_beads_adapter.go` | Legacy | Covered only as current backend/parity baseline | Delete in `loomcli-wpltp.10.1` |
| Daemon issue RPC pools/subscribers | `internal/webui/daemon`, `BeadsPoolHook`, `DaemonSubscriber` | Legacy | Existing browser behavior relies on it outside fleet mode | Delete in `loomcli-wpltp.10.2` after fleet SSE gates |
| Parity beads sidecar and bd seeding | `test/parity`, parity docker compose/scripts | Legacy | Useful until acceptance turns fleet-db-only | Delete in `loomcli-wpltp.10.3` |
| Vendored beads tree and build hooks | `third_party/beads`, release/build scripts | Legacy | Current AGENTS.md still asks humans/agents to use bd | Delete in `loomcli-wpltp.10.4`, docs cleanup in `loomcli-wpltp.10.5` |
| User-facing beads terminology | Docs, frontend fixtures/copy, agent prompts | Legacy | Scattered | Remove in `loomcli-wpltp.3.6`, `loomcli-wpltp.10.5`, `loomcli-wpltp.10.6` |

## Main Design Risks From The Inventory

1. Resolver fallback is the highest-risk hidden behavior. Several paths try
   fleet-db and silently fall back to the default backend, which is currently
   beads. Fleet-db parity cannot be trusted until fleet-db-only mode fails
   closed.
2. Workspace state is split across fleet store, yaml/workspacemgr state, and
   bd initialization side effects. Workspace parity must be completed before
   deleting beads, otherwise browser and supervisor paths will disagree on the
   active workspace.
3. The local supervisor should not be removed with the bd daemon code. The
   supervisor is product functionality; beads daemon issue storage is legacy
   plumbing. Deletion tickets must separate those two concerns.
4. SSE has two implementations today: daemon polling/subscriber and backend
   mutation subscriber. Fleet-db mode needs reconnect, filtering, backpressure,
   and distributed scale tests before the daemon subscriber can be removed.
5. Distributed mode introduces semantics that beads never had to solve:
   multi-actor auth/audit, claim contention across supervisors, stale worker
   recovery, workspace scoping, and event replay across processes.
6. Agent prompts and task-driven commands are part of the product surface. If
   they keep instructing agents to run `bd`, fleet-db may pass backend tests
   while real agent workflows still depend on legacy behavior.

## Immediate Implementation Order

1. Finish `loomcli-wpltp.1.2`: define the fleet-db-only acceptance gates that
   will be required before a ticket can be marked parity-complete.
2. Finish `loomcli-wpltp.1.3`: classify beads code into migration-only,
   parity-only, local-supervisor-retained, and removable categories.
3. Build proof gates first: `loomcli-wpltp.2.1` CLI fleet-db-only harness,
   `loomcli-wpltp.3.1` browser fleet-db-only regression mode, and
   `loomcli-wpltp.7.5` clean-checkout embedded local smoke.
4. Then implement feature buckets in parallel where possible:
   backend/CLI parity (`loomcli-wpltp.2.*`), WebUI parity
   (`loomcli-wpltp.3.*`), workspace lifecycle (`loomcli-wpltp.4.*`), local
   supervisor (`loomcli-wpltp.5.*`), SSE (`loomcli-wpltp.6.*`), embedded local
   mode (`loomcli-wpltp.7.*`), and remote distributed mode (`loomcli-wpltp.8.*`).
5. Only after those gates pass, make fleet-db the default/fail-closed backend
   in `loomcli-wpltp.9.*`.
6. Delete beads and fallback code in `loomcli-wpltp.10.*`.
