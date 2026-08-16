# Scout — Proactive Workspace Analysis Proposal

Status: Approved (HITL review, Tyson, 2026-08-14; slice 1 of the delivery order green-lit as the follow-on effort) · Date: 2026-08-14 · Branches: loomcli `feat/ticket-recommender-v1` (off origin/v5), fleet-db `feat/ticket-recommender-v1`

## Summary

When a user creates a workspace today, Loom hands them an empty shell: no
issues, no onboarding notes, nothing an agent can pick up. The web-onboarding
spec explicitly wants first-run onboarding not to strand users there
(`docs/product/web-onboarding-spec.md#Onboarding Flow`), and
`docs/product/orchestrator-worker-model.md#Service agents` has long planned
agents that "watch signals … create or update issues" — but nothing in the
tree generates issues anywhere, and no doc plans repo analysis
(`docs/design/2026-08-13-ticket-recommender-research.md`, §4).

**Scout** fills that hole. It is a builtin workflow that analyzes a
workspace's attached repos on workspace creation and weekly thereafter,
creates up to five quarantined **recommended issues** per run, and maintains
two files at the workspace root: `agents.md` (workspace-level agent
onboarding notes) and `history.md` (the scout's own run journal and dedupe
memory). Recommendations are real fleet-db issues from the moment they are
created; they are born in `review` status with a `recommended` label, so they
surface in the human review queue and stay quarantined from every automatic
claim path until a human **accepts** them via the review bar's Approve.

This proposal is the destination of the Scout wayfinder map
(`.scratch/scout/issues/` tickets 01–06, all resolved; charting decisions
settled with Tyson 2026-08-13/14). The riskiest piece — the taskless LLM
analysis leaf — was prototyped against the real loomcli + fleet-db pair and
judged worth shipping: 4 of 5 recommendations were verifiably real,
doc/log-grounded work, with perfect schema adherence and zero fabricated
file anchors (`.scratch/scout/prototype/`, $1.99 / 174.7 s for the full
two-call run). **Implementation is a separate, later effort**; this document
ends at the spec.

## Vocabulary

- **Scout** — the builtin workflow component (name verified unused in both
  loomcli and fleet-db). The scout metaphor is canonical; the informal name
  "ticket recommender" survives only in the research doc's filename.
- **Recommended issue** — a real fleet-db issue created by a scout run,
  carrying the `recommended` label (canonical spelling: lowercase — fleet-db
  compares labels case-sensitively, `fleet-db/internal/models/role.go:203-204`)
  plus the existing `repo:<name>` routing label.
- **Accepting** — approving a recommendation on the standard review bar
  (recommendations are created in `review` status, so they carry the same
  Approve/Reject affordance as plan and code reviews). Approve releases the
  quarantine in one atomic update — status to `open` plus removal of the
  `recommended` label — so the issue can never sit open-but-quarantined.
  CLI equivalent: `loom data update --status open --remove-label recommended`.
  (Terminology note: earlier drafts called this "blessing"; renamed
  2026-08-15.)
- **Dismissing** — rejecting a recommendation on the review bar. The reject
  comment is recorded as `DISMISSED: <reason>` and the issue closes while it
  keeps the `recommended` label; the closed issue stays visible to the
  scout's dedupe pass, so a dismissed recommendation is never re-proposed.
  Unlike plan/code rejection, no `needs-revision` label and no reopen. CLI
  equivalent: `loom data update --status closed`.
- The quarantine is **layered**: `review` status is the primary UX surface
  (review queue, ready set excludes non-open statuses) and the fleet-db
  ready-filter on the `recommended` label is the enforcement backstop — a
  stray status flip alone cannot release a recommendation.
- **Journal** — `history.md`, the scout's append-only run log and dedupe
  memory at the workspace root. It is *not* a general workspace activity log.
- Loom's canonical noun is **issue**, not ticket; **lead** is taken
  (`docs/loom-glossary.md`).

## Architecture

The scout rides the shipped trigger/driver platform end to end; it needs zero
new scheduling machinery (research §1). One run is one DriverRun of a new
third builtin workflow, dispatched either by the workspace-creation hook
(first run) or by a per-workspace cron TriggerBinding (weekly re-runs). The
run's leaf analyzes the repos, calls a new `create-issue` driver op for each
novel recommendation, and rewrites the two workspace-root files.

### Builtin registration

Scout ships as the third builtin workflow: a `//go:embed` var plus a
`builtinWorkflows` map entry (`internal/workflows/workflows.go:29-45,79-93`),
registered lazily by `EnsureBuiltinWorkflow` and stamped
`Trust: trusted, CreatedBy: "system"` like epic-runner and
github-review-agent (`internal/workflows/workflows.go:134-190`).

Two deliberate speed bumps are part of the change, not obstacles to route
around:

- The third-builtin tripwire, `TestBuiltinWorkflowRegistryListsBothBuiltins`
  (`internal/workflows/github_review_agent_test.go:17-50`), hard-codes the
  builtin count, the per-builtin entrypoints, and exact file counts. It gets
  its deliberate extension in the same change that adds the builtin.
- The flue pin (`internal/workflows/FLUE_COMMIT`, enforced by
  `internal/workflows/flue_pin_test.go:28-35`) constrains the workflow shape:
  `defineWorkflow` default export with a credential-free `model: false` stub
  agent, payload via `LOOM_FLUE_INVOKE_PAYLOAD` — the shape all four current
  builtin runners use. The authoring guide's bare-`run` example is stale on
  this point (research §5).

**Registration timing is ensure-at-seed**: the workspace-creation hook calls
`EnsureBuiltinWorkflow(scout)` *before* creating the trigger binding, because
binding creation pins a resolvable driver with an active version
(`internal/cli/trigger/trigger_cmd.go:310-320`) and cron dispatch creates the
DriverRun straight from `binding.DriverID/DriverVersionID`
(`internal/infra/memstore/platform_trigger.go:551-562`). The first workspace
create on a host pays a one-time flue bundle build; the builtin-reuse
decision caches after that. On a toolchain-less serve the ensure degrades to
a create-warning, never a create-failure. Alternatives that lost: eager
registration at serve boot still needs the lazy path for workspaces created
after boot (it is an addition, not a replacement — deferred); defer-to-first-
dispatch requires new placeholder machinery in the cron sweep, the largest
change for no MVP benefit.

### First run: the workspacemgr post-Ready hook

The scout hook attaches at the tail of **workspacemgr's create functions**
(`createStoreBackedEmptyWorkspace` /
`createStoreBackedCloneWorkspace`, `internal/cli/serve/workspacemgr/workspace_store.go:49-150,371-468`),
after `WorkspaceStateReady`, outside the rollback envelope. This is the one
seam all three creation paths funnel through: webui sync, webui async (the
job goroutine runs the same `createFn`,
`internal/webui/svcimpl/workspace_job_store.go:55-59`), and CLI
`loom workspace create` (`internal/cli/workspace/workspace_cmd.go:125`).

The alternative — serve's `wrapWorkspaceCreateFn` decorator
(`internal/webui/app/server_workspace.go:26-29`) — lost because the CLI
bypasses the wrapper entirely, and CLI parity was a charter requirement.
Because the inner create runs the whole `Cloning → Initializing → Ready`
machine synchronously and returns only after Ready, "after the create fn
succeeds" *is* "workspace Ready, repos on disk" on every path.

The hook does three things, in order, and degrades every failure to a
create-warning (a workspace must never fail to create because the scout
could not start):

1. `EnsureBuiltinWorkflow(scout)` (ensure-at-seed, above).
2. Create the cron TriggerBinding (parameters below).
3. Dispatch one immediate DriverRun — the first-run analysis.

**Zero-repo workspaces: always seed, guard at run.** The binding is seeded
wherever a workspace is created; a scout run that finds no repos no-ops and
journals "nothing to analyze"; the next weekly tick picks repos up whenever
they are attached. No repo-attach hooks (there are two attach seams plus a
store-direct CLI path, and hooking them all buys nothing the run-time guard
doesn't; ticket 06). Metadata-only rows from `loom workspace add`
(`internal/cli/workspace/workspacev2_cmd.go:98-123`) follow the same rule
when a dir/runtime exists; otherwise the run-time guard covers them.

### Cadence: the cron binding

One per-workspace `source_kind=cron` TriggerBinding, seeded at creation with
every knob stamped explicitly (all seedable today,
`internal/store/platform_store.go:186-215`):

| Knob | Value | Why |
|---|---|---|
| RouteKey | `cron.scout.weekly` | Matches the `cron.*` convention in tests (`internal/trigger/cron_test.go:155`); `internal.*` is reserved for loopback events (`internal/trigger/internal_source.go:65-70`). Default binding id derives to `binding-cron-scout-weekly`. |
| Schedule | `@weekly` | User-tunable afterwards via `loom trigger bindings update --schedule`. |
| ScheduleTimezone | empty (= UTC) | Deterministic; matches the store default (`internal/trigger/cron.go:63-72`). No timezone plumbing in MVP. |
| ConcurrencyPolicy | `forbid` | An overlapping weekly tick is a fault, not a queueing problem: the tick is rejected with an auditable `concurrency_forbid` delivery and no run is created (`internal/infra/memstore/platform_trigger.go:627-629`). Cron sets SubjectRef = BindingID, so all of one binding's ticks share one subject and `forbid` applies cleanly. Explicitly setting a policy matters: an empty policy defaults to `one_active_per_epic`, which does not gate scout runs at all. `replace`/`queue` lost: a wedged scout should surface as a skipped week, not silently stack or supersede. |
| ActorFilter | `exclude_actor_kinds=["driver-run","task-run"]` | Stamped from day one. Inert while the binding is cron-only, but the issue-journal bridge re-emits every `issue.create` as `internal.issue.created` with the actor verbatim and mandates exactly this guard for any binding whose driver creates issues (`internal/trigger/issue_journal_bridge.go:29-45`) — scout's own issues loop back as `driver-run:*`. Correct-by-construction if event patterns are ever added. |
| Enabled | `true` | The kill switch flips this (see the CLI namespace). |

The cron scheduler that fires this is already always-on in `loom serve`
(`internal/cli/serve/serve_loops.go:90-123`; sweep default 30s), with
missed-tick catch-up capped at one tick and tick idempotency
`cron:{bindingID}:{fireUnix}` (`internal/trigger/cron.go:80-86,256-258`).

### The analysis leaf

The prototype question — is a schema-constrained one-shot backend exec
(the `github-review-task-runner.ts` pattern,
`internal/workflows/builtin/github-review-task-runner.ts:25-33`) enough, or
does the leaf need the heavier `local-task-runner.ts` generalization? — was
answered with a real run and a HITL review (ticket 03):

- **The MVP leaf is agentic with tools**, in the `local-task-runner.ts`
  lineage (Tyson's call, diverging from the prototype's one-shot
  recommendation), so the scout can read files itself for richer grounding
  on large repos. The prototype exposed the ceiling that motivates this:
  under a 40k-char prompt budget, two repos already forced file-tree
  truncation, and the budget shrinks linearly with repo count — anchor
  grounding degrades first as workspaces grow.
- **The proven one-shot is the degraded-mode floor.** The real run showed
  perfect schema adherence (5 items, all required fields, priorities in
  range, both labels on every item, all anchor paths verified on disk, no
  fabricated paths), zero tool use, `num_turns` 1–2, at 2-repo scale. The
  spec records it as fallback evidence: if the agentic leaf is unavailable
  or misbehaves, the one-shot shape is a shippable fallback.
- **Backend: workspace default**, like other builtins; no scout-specific
  model policy in MVP (cost tuning is future work). Nothing in the
  prototype's mechanics required the large model.
- **Call topology**: one run analyzes *all* attached repos together —
  ranking and cross-repo dedupe are exactly the scout's job, and a per-repo
  call cannot prioritize across repos. Multi-repo workspaces get one
  `agents.md` with per-repo sections; issues carry `repo:<name>`.
- **Cap**: hard cap of **5** recommended issues per run, counting created
  issues only (skipped duplicates don't count).

The leaf runs as the workflow's task-runner-side process under the normal
driver execution path (executor claim/lease/fencing, run-scoped token,
bundle digest verification — `internal/driver/executor.go:108-184`). It is
read-only over the repos by prompt discipline (see Trust posture) and its
only write paths are the `create-issue` op and the two workspace-root files.

## The `create-issue` driver op

No driver op for creating issues exists today — the op registry
(`internal/webui/handlers/driverapi/module.go:141-158`) has none, and
`loom.taskRuns.request` requires an existing `taskId` (`sdk/driver.js:279`),
a chicken-and-egg constraint for a workflow whose job is to produce issues.
Scout gets a new `create-issue` op on the driverapi allowlist. The module
already holds the per-(workspace, actor) issue-backend factory
(`module.go:170-185`), so this is a new handler, not new infrastructure.
The full contract below was pinned in ticket 02; citations are to the
current tree.

**Actor identity.** The op follows the established sequence: `verifyParent`
(fenced heartbeat via `VerifyRunningDriverRun`,
`internal/driver/run.go:150-176`), then a backend built with actor
`driverpkg.DriverRunActor(parent.RunID)` = `driver-run:{runId}`
(`internal/driver/run.go:180-182`). The run token's JWT Subject is already
that actor (`internal/driver/run.go:264`), so provenance comes free — and
unlike `claim-ready`, **the op accepts no client actor override**, making
`created_by` provenance unforgeable. The actor reaches fleet-db as the
`X-Actor` header and drives owner defaulting, the idempotency digest scope,
and the soft-dup fingerprint.

**Idempotency (three layers).** (1) Caller-layer default key:
`CreateParams.FleetCreateIdempotencyKey(now)` = sha256(UTC date + `\x00` +
exact wire body) (`internal/backend/types.go:416-428`), mirroring the
`cli/data` convention (`internal/cli/data/create.go:139-146`); an explicit
`idempotencyKey` wins. (2) fleet-db hard idempotency: actor-scoped digest,
two-phase reserve→mint→fill, 24h replay window, 409 `conflict` on same key +
different body (`fleet-db/internal/api/idempotency.go:82-124`). (3) fleet-db
soft-duplicate guard: sha256(actor + title + type) within 60s while the
prior issue is open returns the existing issue
(`fleet-db/internal/storage/idempotency.go:155-176`). Both fleet-db layers
are actor-scoped and the actor is stable within one run but different across
runs — so retries within a run dedupe exactly, while a weekly re-run (new
runId, new date bucket) always mints. Cross-run same-week dedupe is
deliberately *not* this layer's job; that is the journal's (below). `force`
is not exposed in v1: soft-dup returning the existing issue is the desired
scout behavior. Dedup is transparent to the workflow — the backend contract
returns bare issue data without the replay headers
(`internal/backend/fleet/idempotency.go:52-62`).

**Request** (`POST /api/workspaces/{ws}/driver/create-issue`, run-token
Bearer auth; camelCase; only `title` required):

```json
{
  "title": "string (required, <=500 chars)",
  "description": "string (optional; acceptance criteria folded in as a '## Acceptance Criteria' section — fleet-db has no AC write path)",
  "issueType": "task|bug|feature|epic|chore (optional, server defaults task)",
  "priority": "int 0-4 (optional; omitted/0 lets fleet-db default P2 — FleetCreateBody drops 0)",
  "labels": ["string (optional; scout stamps 'recommended' here)"],
  "repo": "string (optional; → CreateParams.SourceRepo → fleet-db 'repo')",
  "parent": "string (optional; create-time only — PATCH cannot set parent later)",
  "design": "string (optional; → fleet-db 'design')",
  "status": "open|deferred|review (optional; the scout passes review to park recommendations in the human review queue; any other value is a 400)",
  "idempotencyKey": "string (optional, <=128 printable ASCII; default: sha256(utcDate + '\\x00' + wire body))"
}
```

Not accepted: `actor` (always `driver-run:{runId}`), `assignee`/`owner`
(recommended issues are unassigned; owner defaults to the actor
server-side), `metadata`, `acceptanceCriteria`, `estimatedMinutes`,
`dependencies`, `createdBy`, `force`. Acceptance criteria are unpersistable
on this path — the field exists on fleet-db's Issue model
(`fleet-db/internal/models/issue.go:60`) but no API endpoint reads or writes
it — so they fold into `description`, not `design` (a separate reviewable
artifact with its own format field). Metadata threading was judged
non-trivial (new `CreateParams` field + a change to the frozen
idempotency-hash body projection shared byte-for-byte by two
depguard-separated consumers, `internal/backend/types.go:386-391`, plus the
deployed-fleet-db strict-decode compat risk) and stays future work; the
quarantine marker rides in `labels`, fully supported at create.

Handler mapping: `backend.CreateParams{Title, Description, IssueType,
Priority, Labels, SourceRepo: repo, Parent, Design, Status, IdempotencyKey}`
→ `m.issueBackends(ws, driverpkg.DriverRunActor(parent.RunID)).Create(ctx, params)`.

**Response 200** (camelCase wire struct built from the returned
`backend.IssueData`, mirroring `ClaimedTask` conventions — new ops define a
wire struct per the module's v2 camelCase rule, not raw snake_case
`IssueData`):

```json
{
  "id": "ISSUE-123",
  "title": "…",
  "status": "open",
  "priority": 2,
  "issueType": "task",
  "labels": ["recommended"],
  "sourceRepo": "loomcli",
  "parent": "",
  "createdBy": "driver-run:run-1",
  "createdAt": "2026-08-13T00:00:00Z"
}
```

**Errors** (frozen envelope `{code, message, retryable, details?}`,
`module.go:777-801`). The handler must translate backend kinds to domain
sentinels (`backend.IsKind` → KindConflict→ErrConflict,
KindValidation→ErrInvalid, KindNotFound→ErrNotFound) or fleet-db failures
surface as 500 `internal`; precedent at
`internal/driver/task_mutation.go:209`.

| HTTP | code | when |
|---|---|---|
| 400 | `invalid` | missing/oversize title, bad JSON, bad status/type/priority, malformed idempotency key |
| 401 | `unauthenticated` / `token_expired` / `identity_mismatch` | auth layer, before the handler |
| 403 | `not_owner` | verifyParent: foreign/superseded lease credentials |
| 404 | `not_found` | unknown parent issue |
| 409 | `conflict` | idempotency key reused with different body; in-flight same-key create; non-retryable |
| 409 | `invalid_transition` | verifyParent: driver run not running |
| 499/504 | `canceled` / `timeout` | context; retryable=true |
| 500 | `internal` | anything untranslated |

**Conventions.** Registration in the `m.ops` map updates, in the same
change: the frozen server op table
(`internal/webui/handlers/driverapi/contract_test.go:17-48`), the SDK
manifest `sdk/api-surface.v1.json` (`ops` + `client.namespaces`),
`sdk/contract.test.mjs`, and `sdk/driver.d.ts`. Handler shape mirrors the
newest exemplar (`emit_event.go` + `emit_event_test.go`): own file, own test
file, `decodeParams[T]`, validation errors wrapping `domain.ErrInvalid`.

**SDK surface** — a new `issues` namespace:

```js
const issue = await loom.issues.create({
  title: "…", description: "…", issueType: "task",
  labels: ["recommended"], repo: "loomcli", priority: 2,
  // idempotencyKey optional — defaults per run+day+body
});
```

## Quarantine and acceptance

The quarantine audit (ticket 01) enumerated every path an agent can acquire
a task and found the decisive fact: **every automatic acquisition path
sources its candidate set exclusively from fleet-db `GET /issues/ready`** —
supervisor claim (`internal/cli/daemon/supervisor/claim.go:134`), router
checks (`internal/cli/task_router.go:312`), automode
(`internal/cli/automode/automode_poller.go:80`), the `claim-ready` driver op
(`internal/driver/task_mutation.go:187`), epic-runner (via claim-ready), and
the LLM's `loom data ready`. No path excludes labels today; the ready query
has inclusion filters only.

**Decision: one chokepoint, in fleet-db.** `matchesReadyFilter`
(`fleet-db/internal/storage/ready.go:594`) default-excludes issues carrying
the `recommended` label, with an explicit **`include_recommended` opt-in**
query param parsed in `parseReadyFilter` (`fleet-db/internal/api/ready.go:124`)
and threaded through `service.ReadyFilter`
(`fleet-db/internal/service/ready.go:33,101`). Enforcement sits at **query
time**, not eligibility/index time: label edits take effect instantly, and
accepting (removing the label) de-quarantines with no ready-queue membership
rebuild. Client plumbing for the opt-in rides on `backend.ReadyOpts`
(`internal/backend/types.go:252`) → `readyOptsToQuery`
(`internal/backend/fleet/params.go:222`), needed only by callers that must
*see* recommended issues.

The loomcli-filter alternative lost decisively: it needs at least four
coordinated edits (`MatchTask`, `IsWorkableTask`, `ClaimReadyTask`, and the
prompt jq in `task.md`/`planning.md`), `MatchTask` does not call
`IsWorkableTask` so a single-predicate patch silently leaks
(`internal/cli/task_router.go:76-88`), and non-loomcli fleet-db clients
would remain unprotected. Baking the exclusion into epic-runner's existing
`excludeLabels` defaults would duplicate policy per path. fleet-db's dormant
Role `ExcludeLabels` field is stored-only, evaluated nowhere — do not build
quarantine on it (per-role opt-in is not global quarantine).

**Manual paths need zero changes, by design.** `loom data claim`
(`internal/cli/data/claim.go:14-29`) and the web UI claim
(`internal/webui/service/issue_impl.go:351`) go by explicit ID and never
consult the ready view — a human explicitly claiming a recommended issue
keeps working. That asymmetry *is* the feature.

The audit's recorded warnings carry into the spec verbatim:

1. **Claim-by-ID is unguarded — known soft spot.** fleet-db `ClaimIssue`
   (`fleet-db/internal/service/issue_service.go:495`), `loom data claim`,
   and the daemon IPC claim will claim a recommended issue given its ID, and
   an LLM can obtain IDs outside the ready view (`loom data list`, prompted
   at `internal/cli/agent/prompts/task.md:24`). Quarantine for the LLM
   self-claim path is only as strong as "the ready list doesn't show it".
   Hard enforcement needs an actor-aware label check in fleet-db
   `ClaimIssue` that distinguishes human from agent actors — deliberately
   out of the minimal set; recorded, not solved.
2. **Directed dispatch over-blocks.** `claimRequestedTask` validates the
   requested ID against the ready response
   (`internal/cli/daemon/supervisor/claim.go:147-165`), so "run this
   recommended task with agent X" fails with "not ready" under default
   exclusion. Whether that one `readyIssues` call passes `include_recommended`
   is left to implementation (below).
3. **Case convention pinned**: `recommended` is lowercase; fleet-db compares
   labels case-sensitively while `ClaimReadyTask`'s client-side excludes are
   case-insensitive (`internal/driver/task_mutation.go:220-231`) — the
   server-side match must be exact.
4. **Web UI ready views must opt in.** The Kanban/ready views consume the
   same endpoint (`internal/webui/handlers/issues/ready.go`); without
   `include_recommended` the board hides exactly the issues humans are
   allowed to claim. The existing issue lists are the MVP's review surface,
   so they must pass the opt-in.
5. The stale SYNC comment at `internal/cli/taskfilter.go:23` cites files
   that no longer exist in either repo — they are not additional enforcement
   sites. loomcli's client-side ready re-filter
   (`internal/backend/fleet/deferred.go:36`) is inclusion-only and harmless
   alongside the server change.

Because the chokepoint lands in fleet-db, a small **fleet-db companion
change** ships with this feature — mind the usual companion-branch merge
ordering (fleet-db first; see Known implementation notes).

## `agents.md` contract

`agents.md` is the workspace-level agent onboarding file at the workspace
root. Loom neither reads nor writes any onboarding file today (research §6);
backends' AGENTS.md discovery is cwd-rooted in repo checkouts, one level
below — a workspace-root file is invisible to them without explicit
injection. That is deliberate, and the lowercase name is load-bearing:
**the file stays `agents.md`** so ancestor-climbing backends (Claude Code
climbs; codex stops at the git root) don't auto-load it and double-inject
alongside the template splice. The file is never copied into repo checkouts.

**Sections** (prototype structure adopted, plus one addition from grilling):

- Workspace Overview
- **How the repos relate** — the cross-repo section: companion-branch
  ordering, shared contracts, cross-repo test entry points
- Per repo: Build/Test, Architecture Sketch, Conventions, Gotchas

The scout *promises* Build/Test currency; everything else is best-effort.
The prompt carries a complement-don't-duplicate rule: workspace-level facts
(cross-repo, environment, workflow) only; anything belonging to a single
repo's own AGENTS.md is out of bounds.

**Regeneration is staged.** Weekly re-runs never overwrite the live file:
they write `agents.md.pending`; `loom scout diff` shows the change and
`loom scout approve` promotes it. Human edits are preserved via scout-owned
fence markers — the scout regenerates only inside its own fences. The first
generation at workspace creation auto-applies (nothing to clobber, no human
present). Injected content is always the approved file, never the pending
one.

**Injection.** A new prompt template variable — working name
`{{.WorkspaceNotes}}` — joins the existing variable set
(`docs/loom-glossary.md#Custom Prompt Template Variables`,
`internal/cli/agent/prompts.go`): available to custom prompts on reference,
and spliced into the built-in plan/task worker prompts by default, the same
way `{{.WorkspaceBlock}}` content is spliced today. The MVP floor remains
the passive file at the workspace root (browsable/editable via the web
file browser's `scope=workspace`,
`internal/webui/svcimpl/file_service.go:151-152`); injection is the designed
path above it.

## `history.md` contract

`history.md` is the scout's own journal: **pure markdown**, one section per
run — timestamp, repos + commit SHAs analyzed, issues created (IDs +
titles), candidates skipped as duplicates, warnings/errors. A run with zero
novel candidates still journals; a zero-repo run journals "nothing to
analyze". The agentic leaf reads its own markdown as memory; there is no
parser and no machine sidecar (future work). It is explicitly *not* a
workspace work-history view over sessions or DriverRun records — that idea
is out of scope entirely.

**Dedupe is LLM-only** (Tyson's call, diverging from the recommended
deterministic title backstop). The leaf receives the journal plus open *and*
recently-closed `recommended` issues in context and is instructed not to
re-propose covered work; closed/wontfix stays suppressed indefinitely.
There is no code-side guard before create. **Accepted risk, recorded**: a
bad run can double-create — the create-issue op's idempotency only catches
same-day identical bodies. If duplicates show up in practice, the
deterministic exact-title guard before create is the known cheap fix.

**fleet-db wins on conflict or loss.** The durable record is the set of
`recommended`-labeled issues (open + closed) in fleet-db; the journal is
memory, not truth. If the journal is lost (workspace dir recreated) or
disagrees, the scout re-derives suppression from that query and starts a
fresh journal whose first section notes the rebuild.

## The `loom scout` CLI namespace

A small scout-branded namespace is the human control surface; each verb
wraps existing primitives rather than new machinery:

| Command | Does |
|---|---|
| `loom scout disable` / `enable` | Flips `Enabled` on the workspace's `binding-cron-scout-weekly` binding. Disabled bindings are dead at sweep time (`internal/trigger/cron.go:127-132`) and skipped at route dispatch — reversible silence. Requires adding the client-side Enabled-patch plumbing that `loom trigger bindings update` lacks today (the store/client layers already support it, `internal/store/platform_store.go:254`); the generic `--enabled` flag on `bindings update` ships alongside, since it is the same one-field patch exposed generically (delegated to this spec in ticket 06). |
| `loom scout status` | Shows the binding (enabled, schedule, next/last tick), last run outcome, and pending-file state. |
| `loom scout diff` | Shows `agents.md.pending` vs `agents.md`. |
| `loom scout approve` | Promotes the pending file to `agents.md`. |

Delete-as-kill-switch was rejected: `TriggerBindingStore` has no client-side
Delete, and disable achieves the same silence reversibly.

## File placement and guards

Both files live at the **workspace root**, anchored by
`cli.GetWorkspaceRuntimeDir()` (`internal/cli/worktree_resolve.go:575-601`)
— the shipped pattern for workspace-local non-repo files (`sessions/`,
`usage.jsonl`, `notify.token`).

That resolver's `"."` fallback is a real hazard: in single-project
(non-workspace) mode the "workspace runtime dir" *is* the repo checkout.
**Guard: the scout refuses to write when the runtime dir resolves to the
`"."` fallback** — it must never write `agents.md`/`history.md` inside a
repo checkout. Belt-and-braces for the paths where the fallback can land
files anyway: both filenames (and `agents.md.pending`) are added to
`.gitignore` and to `ProtectedRuntimePaths`
(`internal/cli/daemon_runtime.go:127-134`) so agent recovery's `git clean`
can never destroy them — the precedent that already protects a repo-root
`AGENTS.md`.

Writes use the existing atomic tmp+rename pattern (`internal/atomicfile`;
`internal/sessions/store.go:253-261`).

## Trust posture

Scout ships as a **trusted builtin**, un-sandboxed on the local process
launcher, exactly like epic-runner and github-review-agent:
`EnsureBuiltinWorkflow` is the only call site that stamps
`Trust: trusted` (`internal/workflows/workflows.go:166-178`), HTTP-submitted
workflows default to untrusted fail-closed, and
`sandbox.RefuseUntrustedPlacement` enforces the admission decision at run
time (`internal/driver/sandbox/policy.go:70-93`). Per the glossary, trust is
admission, not confinement — the scout is **read-only over the repos by
prompt discipline**, not by an OS boundary. Its sanctioned write paths are
the `create-issue` op (server-side, actor-stamped, so the fleet-db
credential never enters the leaf) and the two workspace-root files.
Sandboxing the leaf (container/Daytona posture) is explicitly out of scope
for the MVP: revisiting isolation is a platform-wide effort, not scout's.

## MVP boundary

### Future work (fog — charted, deliberately unbuilt)

- **Backlog-aware re-runs** — scout reads the whole open backlog and
  recommends *which existing* issues to work on / re-prioritizes; needs the
  repo-analysis half proven in production first.
- **Metadata provenance** — threading fleet-db's issue `metadata` K/V
  through `backend.CreateParams` / the create-issue op so provenance is
  machine-queryable beyond the label; judged non-trivial in ticket 02
  (frozen idempotency-hash projection + deployed-server compat risk).
- **Triage ergonomics** — bulk accept, notification when new
  recommendations land, surfacing counts in existing UI lists; wait until
  the label flow is felt in practice.
- **Cost/model tuning** for the leaf (cheaper pinned model, thinner backend
  entrypoint); nothing in the prototype required the large model.
- **Machine-readable journal sidecar**, if LLM-only journal parsing proves
  insufficient.
- **Hard claim-by-ID enforcement** — the actor-aware label check in fleet-db
  `ClaimIssue` (quarantine soft spot #1).

### Out of scope

- **Implementing the MVP** — this destination ends at spec + prototype; the
  build is a fresh effort with its own map or plan.
- **New UI surfaces for recommendations** — MVP deliberately rides existing
  issue lists/labels; an aether review panel would be its own effort.
- **Sandboxing the scout leaf** (container/Daytona posture) — MVP ships
  trusted like other builtins.
- **Workspace work-history views** (markdown over sessions/DriverRun
  records) — explicitly not what `history.md` is; a separate feature if
  ever.

## Suggested delivery order (vertical slices)

A suggested test-first ordering for the build effort — explicitly *not* a
committed plan (the map ends at spec + prototype; the build charts its own).
Each slice is independently verifiable before the next starts:

1. **Manually-triggered end-to-end vertical.** The builtin scout workflow +
   the agentic leaf + the `create-issue` driver op, fired via the existing
   workflow-run CLI path (`loom workflow run` /
   `POST /api/workspaces/{ws}/workflows/scout`) — no hook, no cron. Produces
   real quarantined issues, `agents.md`, and `history.md` in a test
   workspace. Demoable and testable first; proves the whole data path.
2. **Quarantine enforcement.** The fleet-db ready-filter default-exclusion +
   `include_recommended` opt-in. Quarantine becomes enforced rather than
   nominal; mind the companion merge ordering (fleet-db first).
3. **Wiring.** The workspacemgr post-Ready hook + cron binding seeding with
   ensure-at-seed registration. Scout now starts itself.
4. **Human controls.** The `loom scout` CLI namespace + staged
   `agents.md.pending` / `diff` / `approve` regeneration.
5. **Injection.** The `{{.WorkspaceNotes}}` template variable + the built-in
   plan/task prompt splice.

## Known implementation notes

Carried forward from the prototype and the charting so the build effort does
not rediscover them:

- **Fix the inverted priority semantics.** The prototype prompt said
  0=lowest; Loom is 0=P0 critical … 4=backlog
  (`fleet-db/internal/models/priority.go:5-20`). The real leaf's prompt and
  schema must use Loom semantics.
- **Restructure prompts for cache reuse.** The prototype's two calls shared
  ~17.6k chars of workspace context but got zero cache reads — the
  structured-output tooling changes the request prefix and the shared
  context sat after differing task text. Order shared context first, task
  text last.
- **Context budget pressure** shrinks linearly with repo count (the file
  tree truncates first, so anchor grounding degrades first). The agentic
  leaf largely obsoletes the pressure, but the deterministic context
  gatherer remains the seed.
- **fleet-db companion-change merge ordering.** The quarantine chokepoint is
  a fleet-db change; the loomcli side (opt-in plumbing, op, hook, CLI) must
  not merge ahead of it. Follow the usual companion-branch ordering
  (fleet-db `feat/ticket-recommender-v1` first).
- The tripwire test extension and the SDK/manifest/contract-table updates
  are same-change requirements, not follow-ups (see Builtin registration
  and the op contract).

## Test strategy

Vocabulary per `docs/testing-terminology.md` (axes: depth / realness /
provisioning / polarity; evidence classes deterministic / real / live).
The scout's split is sharp: everything except the LLM leaf is testable
deterministically; the leaf itself is inherently *live* evidence and stays
out of gates.

**Unit (deterministic, no provisioning, both polarities):**

- fleet-db: `matchesReadyFilter` default-excludes `recommended`; opt-in
  includes it; case-sensitive exact match; acceptance (label removal) makes
  the issue ready with no re-index. Negative: a recommended issue never
  appears in the default ready view.
- loomcli op handler: `create-issue` via the existing driverapi harness
  (`module_test.go`'s `newTestHarness` + a `fakeIssueBackend` recording
  `CreateParams` including the defaulted idempotency key); happy path,
  every error-envelope row, actor is the run actor and cannot be overridden.
- workspacemgr hook: seeds the binding with the exact parameter table,
  dispatches the immediate run, degrades every failure to a create-warning
  (negative: workspace creation never fails because of the scout), skips
  analysis but still seeds for zero-repo.
- Cron/binding: `forbid` rejects an overlapping tick with a
  `concurrency_forbid` delivery; disabled binding is dead at sweep and
  dispatch (negative).
- `loom scout` namespace: enable/disable patches `Enabled`; `diff`/`approve`
  stage-file mechanics; fence-preserving regeneration keeps human edits
  outside scout fences byte-identical.
- Placement guard: writer refuses when `GetWorkspaceRuntimeDir()` resolves
  to the `"."` fallback (negative); `.gitignore` + `ProtectedRuntimePaths`
  entries exist (meta-test alongside the existing protected-paths tests).
- Tripwire: the extended builtin-registry test lists all three builtins with
  the scout's entrypoint and file counts.

**Integration (real local backend — embedded/podman fleet-db, provisioned
via the existing `test-fleetdb-*` lanes):**

- End-to-end op: a driver run creates an issue through `create-issue`
  against real fleet-db; `created_by` is `driver-run:{runId}`; same-run
  retry replays (hard idempotency), soft-dup returns the existing issue;
  next-day/next-run key mints fresh.
- Ready-view round trip: created recommended issue invisible to
  `loom data ready` and supervisor claim; visible with the opt-in; claimable
  by explicit ID (the human path, positive); accepted issue claimed by the
  normal auto path.
- Hook-to-run: workspace create on a real store seeds binding + run;
  `EnsureBuiltinWorkflow` at seed (needs the flue toolchain — provisioning
  axis; the toolchain-less path asserts warn-and-continue).

**E2E / live (real backend CLI, costs money, non-deterministic — never in
`make gate`):**

- A scripted leaf run against a fixture workspace, the prototype's
  post-hoc checks promoted to assertions: schema validity, cap ≤5, both
  labels present, priorities in Loom semantics, anchor paths exist on disk.
  Run manually or in an opt-in lane, per the terminology handshake:
  deterministic evidence covers orchestration only; a blocked live path is
  reported blocked/unverified, never fabricated.

## Left to implementation

Genuinely unspecified; listed here rather than decided:

- Whether directed dispatch (`claimRequestedTask`) passes
  `include_recommended` so a lead can deliberately hand a recommended issue
  to an agent, or over-blocking is accepted for MVP (quarantine warning #2).
- The exact fence-marker syntax for scout-owned regions in `agents.md`
  (ticket 04 fixed the mechanism, not the spelling).
- The exact section headings / markdown shape of a `history.md` run entry
  (contract fixes the required fields only).
- How the leaf receives its dedupe context (journal text + open/closed
  recommended issues): payload assembly server-side vs a read op — either
  satisfies the contract.
- Where the leaf's `rationale` and `anchors` fields land on the created
  issue (fold into `description` vs `design`).
- `loom scout status` exact output fields/format.
- A size limit for `{{.WorkspaceNotes}}` injection, if any.
- Behavior when `agents.md.pending` already exists from an earlier
  unapproved run (overwrite vs preserve).
- The run-time budget/timeout for a scout run (the create hook's work shares
  the async job's 5-minute budget only on the dispatch step; the run itself
  is executor-owned).

## Provenance

- Map and resolved tickets: `.scratch/scout/map.md`,
  `.scratch/scout/issues/01…06`.
- Research (all file:line claims verified there):
  `docs/design/2026-08-13-ticket-recommender-research.md`.
- Prototype and real-run evidence: `.scratch/scout/prototype/scout-leaf.mjs`,
  `.scratch/scout/prototype/out/{recommendations.json,agents.md,run-meta.json}`.
