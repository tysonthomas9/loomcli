# Task lanes: first-class routing of tasks to specialist agents

Status: PROPOSAL (from the SWE-Marathon dogfooding campaign, 2026-08)
Size: M (≈2 days incl. tests)
Evidence: 18 benchmark runs; B2f-revised routing POC (all assertions green);
gaps G12; harness `harbor/` on branch `swe-marathon-harness`

## Problem

Loom can route tasks to agents by **lifecycle stage** only (`plan` role ←
`needs_plan`, `task` role ← `has_design`). There is no way to route by
**kind of work** — "this task is for the QA agent," "this one is for the
docs agent." Two consequences, both hit in practice:

1. **The only selector is repo scoping, and it is broken in a way that
   lies.** The daemon claim path post-filters by literal `source_repo`
   match (`internal/backend/fleet/deferred.go:124`, strict) while the
   router is lenient — empty passes (`internal/cli/task_router.go:112`).
   Result (G12): a task created without `--source-repo` is silently
   unclaimable by every repo-scoped agent, while `loom daemon queue`
   (lenient path) previews it as claimable. Daemons idle on NoWork forever
   next to visibly "ready" work.
2. **Specialist routing today requires abusing repo semantics.** The
   campaign's verification arm needed a QA lane and got it by stamping
   tasks `--source-repo qa-verify` — which routes correctly *only because
   of the strict-match accident above*. It pollutes `source_repo` (there
   is no such repo), and the obvious G12 fix (treat empty as wildcard)
   would not remove the need for a routing mechanism — it would only
   force specialist lanes onto an even worse channel. Routing must become
   first-class **before or with** the G12 fix.

The POC proved the pattern works end-to-end when routing holds: a lead
filed `Verify: ...` tasks into a lane, a persistent QA agent drained them
(claim → execute → close with evidence comments), implementation workers
never touched them, and QA's corrective tasks flowed back to the normal
pool. All of that should be expressible without repurposing a bug.

## Proposal

### 1. Lanes, as namespaced labels (no schema change)

A **lane** is a label with the reserved prefix `lane:` — e.g. `lane:qa`,
`lane:docs`. Labels already exist end-to-end (events, projections,
`--add-label`, snapshots), so v1 requires **no fleet-db change**.

CLI sugar (thin wrappers over labels):

```sh
loom data create --type task --lane qa --title "Verify: ..." ...
loom data list --lane qa
```

A task has at most one `lane:*` label (create/update validate this).

### 2. Agentdef lane selectors

```sh
loom agentdef add qa --role task --auto --backend codex --lane qa
```

`agentdef.lanes: []string` (default empty). Claim matching:

- An agent **with** lanes claims only tasks whose lane label is in its
  lanes.
- An agent **without** lanes claims only tasks **without** a lane label.

The partition is strict in both directions — lane tasks never leak to
generalist workers, unlaned tasks are never grabbed by specialists. (This
mirrors the POC semantics that worked; the accidental version of this
bidirectional exclusion is what `source_repo` abuse provided.)

### 3. One matcher, three call sites (fixes G12 for real)

Extract a single `MatchTaskToAgent(task, agentdef)` covering stage filter
+ repo filter + lane filter, used by:

- the daemon claim path (today `deferred.go`, strict),
- the router (today `task_router.go`, lenient),
- `loom daemon queue` preview (today the lenient path — which is why the
  preview lies).

With lanes carrying specialist routing, `source_repo` matching can adopt
the sane semantics everywhere: **empty means wildcard**. The preview,
router, and claim path can no longer disagree, because they are the same
function.

### 4. Companion (optional, separable): a built-in `verify` role

`--role verify` with a stock `fleet_verify.md` template: claim a lane
task, run the application from a checkout, exercise it against what the
task/spec states, file corrective tasks (unlaned, so they route to
implementation workers), close the verify task with a structured
`QA-RESULT:` comment. This productizes the campaign's proven verification
duty; it is orthogonal to lanes but is the first real consumer.

## Non-goals (v1)

- A first-class `lane` field on issues (cleaner, but an event/projection/
  snapshot change; revisit after label-based lanes prove out).
- Persistent worker sessions for daemon roles (the supervisor keeping one
  controlled session per agentdef and delivering claims into it — the
  campaign's worker-persistence lever). Separate proposal.
- Multi-lane tasks, lane priorities, lane quotas.

## Migration and compatibility

- No event-schema change; snapshots unaffected.
- Existing agentdefs (no lanes) behave as today except the G12 wildcard
  fix — which un-strands empty-`source_repo` tasks, the behavior users
  already believe they have (the preview shows it).
- The marathon harness migrates `--source-repo qa-verify` →
  `--lane qa` (one-line changes in two prompts and one assertion script).

## Testing

- Unit: matcher parity — claim path, router, and preview return identical
  verdicts for a matrix of (stage × repo × lane) tasks and agentdefs;
  the G12 case (empty source_repo, repo-scoped agent) claims successfully;
  the lane-partition invariants both directions.
- Integration: extend `harbor/test/run-stub-trial.sh` invariants with a
  laned task — stub daemon must never claim it; a stub lane agent must.
- The B2f-revised POC script (`scratchpad/b2fpoc/`, assertions A–F) is
  the live-fire reference for the end-to-end behavior.

## Evidence appendix

- G12 discovery + preview mismatch: stub dry-run 2026-07-31; workaround
  (`--source-repo app` stamped on every create) in every campaign prompt.
- Routing POC (2026-08-05): lead filed 3 lane tasks on an integration
  delta; persistent QA claimed, executed against the running app, closed
  each with evidence; daemon (planner+coder) alive throughout and never
  claimed a lane task; QA correctives flowed back to the general pool.
- Scale rationale: lanes are the mechanism that lets specialist roles
  multiply without repo-semantics abuse — QA today; docs, security
  review, release checks tomorrow.
