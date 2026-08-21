# Task lanes: first-class routing of tasks to specialist agents

Status: PROPOSAL rev 2 (codex-vetted: REVISE → all 8 findings folded)
Size: **L** (≈4–5 days incl. tests) — or the narrowed M v1 in "Scope options"
Evidence: 18 benchmark runs; B2f-revised routing POC (all assertions green);
gap G12; harness `harbor/` on branch `swe-marathon-harness`; vet:
`trials/docs/codex-lanes-vet.md` in the evidence repo

## Problem

Loom can route tasks to agents by **lifecycle stage** only (`plan` role ←
`needs_plan`, `task` role ← `has_design`). There is no way to route by
**kind of work** — "this task is for the QA agent," "this one is for the
docs agent." Two consequences, both hit in practice:

1. **The only selector is repo scoping, and it disagrees with itself.**
   The daemon claim path prefilters candidates by `ReadyOpts.SourceRepos`
   (`internal/cli/daemon/supervisor/claim.go:96-110`) and the fleet
   backend strictly rejects empty `source_repo`
   (`internal/backend/fleet/deferred.go:36-46,105-126`), while the router
   is lenient (`internal/cli/task_router.go:111-119`) and `loom daemon
   queue` fetches and scores through yet another path
   (`internal/cli/daemon/daemon_queue.go:85-127`). Result (G12): a task
   created without `--source-repo` is silently unclaimable by every
   repo-scoped agent while the preview shows it as claimable.
2. **Specialist routing today requires abusing repo semantics.** The
   campaign's verification arm routed QA work by stamping tasks
   `--source-repo qa-verify` — which works *only because of the strict
   filter above*, pollutes `source_repo` (no such repo exists), and
   collides with the natural G12 fix. Routing must become first-class
   before or with that fix.

The B2f-revised POC proved the pattern end-to-end when routing holds: a
lead filed `Verify:` tasks into a lane, a persistent QA agent drained
them (claim → execute against the running app → close with evidence
comments), implementation workers never touched them, and QA's corrective
tasks flowed back to the general pool.

## Proposal

### 1. Lanes, as namespaced labels on issues

A **lane** is a label with the reserved prefix `lane:` (e.g. `lane:qa`).
Issue labels are already first-class end-to-end — fields
(`fleet-db/internal/models/issue.go:57-61`), events
(`fleet-db/internal/models/event.go:37-39`), projections/indexes
(`fleet-db/internal/projection/handlers.go:99-103,886-940`), snapshots,
and list filters (`fleet-db/internal/storage/filter.go:28-33`) — so the
*issue side* needs no schema change.

CLI sugar:

```sh
loom data create --type task --lane qa --title "Verify: ..." ...
loom data list --lane qa      # NOTE: `data list` has no label flag today
                              # (internal/cli/data/list.go:47-52); ListOpts
                              # and the API already support labels
                              # (internal/backend/types.go:191-198), so this
                              # is new flag plumbing, not new capability.
```

**One-lane-per-task is enforced authoritatively in fleet-db**, not in the
CLI: labels are mutable through generic paths the CLI never sees (create
`internal/cli/data/create.go:100-131`, update deltas
`internal/cli/data/update.go:145-155`, the fleet-db add-label endpoint
`fleet-db/api/openapi.yaml:1211-1235` →
`fleet-db/internal/service/issue_service.go:1035-1069`, and web UI
mutations). The service rejects any mutation that would leave an issue
with two `lane:*` labels.

### 2. Agentdef lane selectors — full persistence checklist

```sh
loom agentdef add qa --role verify --auto --backend codex --lane qa
```

`lanes: []string` (default empty) must be added across the whole agentdef
surface — this is real API work, not config sugar:

- domain struct `internal/domain/agent.go:42-63` and store
  `internal/store/agent_store.go:12-50`
- fleet-db wire create/update/read structs
  (`internal/infra/fleetdb/agent.go:20-48,77-108,138-182`)
- CLI flags + show/list output (`internal/cli/agentdef/agentdef_cmd.go`)
- project-config conversion `AgentEntry`
  (`internal/cli/config/project.go:96-111`)
- `api/openapi.yaml:2291-2342` + regenerated Go types + frontend types and
  the web UI agent create/edit/display
  (`internal/webui/frontend/src/api/workspace/workspace.ts:42-53`)

### 3. One routing contract (matcher AND candidate fetch)

A shared matcher alone cannot fix G12, because call sites diverge at the
*candidate fetch* before any scoring happens. The contract therefore has
two halves — `MatchTaskToAgent(task, agentdef)` (stage + repo + lane) and
shared candidate-fetch semantics — adopted by **all five** claimant/preview
surfaces:

1. daemon claim (`supervisor/claim.go` + `backend/fleet/deferred.go`)
2. the router (`task_router.go`)
3. `loom daemon queue` preview (`daemon_queue.go`)
4. the web UI daemon queue (`internal/cli/serve/daemonwire/daemon.go:362-409`)
5. automode's duplicate checks (`internal/cli/automode/automode_poller.go:183-201`)

**Wildcard scope**: empty `source_repo` means wildcard **for routing
only**. User-facing list/ready/deferred/blocked repo filters keep their
strict semantics (`backend/fleet/fleet.go:380-412`) — broadening them
would silently add repo-less tasks to repo-filtered queries, and stacked
delivery derives repo/base from `source_repo`
(`internal/cli/agent/prompts/task.md:193-199`).

Lane matching itself: an agent with lanes claims only tasks whose lane is
in its lanes; an agent without lanes claims only tasks without a lane
label. Strict, bidirectional.

### 4. Non-daemon claimants and bypasses — explicit semantics

- **Manual `loom data claim`** (`internal/cli/data/claim.go:14-27`):
  operator override, allowed by design; lanes do not block it. Documented
  as such.
- **Driver / epic-runner `claimReady`**
  (`internal/workflows/builtin/epic-runner.ts:95-103`,
  `internal/driver/task_mutation.go:167-190`): treated as generalist —
  laned tasks are **excluded** from its candidate set via the shared
  fetch semantics.
- **Daemon pinned tasks** (`supervisor/claim.go:141-155`): pinning is an
  operator override like manual claim; lanes do not block it, and the
  queue view marks the mismatch.

### 5. Built-in `verify` role (companion, first consumer)

`--role verify` with stock `fleet_verify.md`: claim a lane task, run the
application from a checkout, exercise it against what the task/spec
states, file corrective tasks (unlaned → normal pool), close the verify
task with a structured `QA-RESULT:` comment.

**Claim filter: `task_filter: any`** — verify tasks carry no design, and a
`--role task` agent would never claim them (`has_design` filter,
`internal/cli/daemon/supervisor/role.go:59-66`,
`task_router.go:231-242`). This is why the earlier rev's `--role task
--lane qa` example was wrong; the POC masked it because its QA
self-claimed from a persistent session rather than through the daemon.

## Migration and compatibility

- Issue side: no event-schema change; snapshots unaffected.
- **Reserved-prefix audit**: before `lane:` becomes reserved, ship
  `loom data list --label-prefix lane:` (or a doctor check) to find
  pre-existing labels that would collide, and document the cleanup
  (`--remove-label`). Snapshots containing multi-lane issues from before
  enforcement are grandfathered read-only; the first mutation must
  resolve to one lane.
- Existing agentdefs (no lanes) behave as today except the routing-scoped
  G12 wildcard fix — which un-strands empty-`source_repo` tasks.
- The marathon harness migrates `--source-repo qa-verify` → `--lane qa`.

## Scope options

- **Full (this document): L, ≈4–5 days.** Routing contract across all
  five surfaces, agentdef persistence through API/webui, service-side
  lane validation, verify role, migration tooling, tests.
- **Narrowed v1: M.** Label sugar + daemon-only routing (claim, queue
  preview, router, automode) with lanes read from agentdef config;
  no web UI display, no service-side single-lane enforcement (documented
  best-effort), driver path excluded from lanes by construction. Honest
  only if labeled experimental.

## Testing

- Matcher/fetch parity: all five surfaces return identical verdicts for a
  (stage × repo × lane) matrix; the G12 case (empty source_repo,
  repo-scoped agent) claims; both lane-partition directions hold.
- **A laned, undesigned verify task is claimed by a `--role verify` agent
  through the daemon** (the case rev 1 got wrong).
- Service-side: single-lane invariant across create, label add/remove,
  patch, and the fleet-db HTTP add-label path.
- Driver: epic-runner never claims a laned task.
- Integration: extend `harbor/test/run-stub-trial.sh` with a laned-task
  exclusion invariant; the B2f-revised POC (assertions A–F) remains the
  live-fire reference.

## Evidence appendix

- G12 discovery + preview mismatch: stub dry-run 2026-07-31; workaround
  (`--source-repo app` on every create) in every campaign prompt.
- Routing POC (2026-08-05): lead filed 3 lane tasks on an integration
  delta; persistent QA claimed, executed against the running app, closed
  each with evidence; daemon (planner+coder) alive throughout and never
  claimed a lane task; QA correctives flowed back to the general pool.
- Codex vet (2026-08-05): REVISE — candidate-fetch divergence, has_design
  trap, CLI-only validation impossible, agentdef API surface, wildcard
  scoping, bypass claimants, list-flag gap, sizing. All folded here.
