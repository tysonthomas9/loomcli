# Explicit Task Code Lineage Research

Date: 2026-08-26  
Branch examined: `feat/team-task-worktrees` at `ce2db619b9b6b44c031204f228cb6c372969277c`

## Decision

Separate scheduling from code inheritance. A FleetDB `blocks` edge answers only
"when may this task run?" It must no longer implicitly answer "which commit is
this task based on?"

Add two first-class Loom task fields, persisted in FleetDB's existing issue
metadata:

- `inherits_from`: zero or one task ID. Its published delivery is the only
  commit from which the task worktree starts.
- `integration_inputs`: zero or more task IDs. These are immutable published
  deliveries an integration task must merge into its output. They do not
  select the starting commit.

The FleetDB metadata keys should be `loom.inherits_from` and
`loom.integration_inputs`; the latter is a JSON array of task IDs. The public
Loom API/CLI should expose typed fields and hide that persistence detail.

This does **not** require a FleetDB server, storage, or dependency-schema
change. It does require Loom backend projections, CLI/API contracts, prompt
guidance, supervisor preparation, and Git publication checks to change.

## Current model and why it fails

FleetDB has one directed dependency model. Its canonical built-in kinds are
`blocks`, `parent-child`, `related`, `duplicate-of`, and `superseded-by`; only
`blocks` and `parent-child` affect ready work
([`fleet-db/internal/models/dependency.go:8-57`](../../../../../fleet-db/internal/models/dependency.go),
[`fleet-db/docs/architecture.md:677-687`](../../../../../fleet-db/docs/architecture.md)).
The add-dependency request contains only `depends_on_id` and `type`; it has no
code-lineage semantics
([`fleet-db/pkg/client/types.go:361-365`](../../../../../fleet-db/pkg/client/types.go),
[`fleet-db/docs/rpc-spec.md:302-317`](../../../../../fleet-db/docs/rpc-spec.md)).
FleetDB readiness is `status=open`, non-epic, not deferred, and no open
blockers ([`fleet-db/docs/architecture.md:717-725`](../../../../../fleet-db/docs/architecture.md)).

Loom currently reinterprets every direct `blocks` dependency as a required code
delivery. The supervisor extracts all such IDs, unions them with replayed
historical blockers, and sends the whole set to worktree preparation
([`internal/cli/daemon/supervisor/claim.go:125-187`](../../internal/cli/daemon/supervisor/claim.go),
[`internal/cli/daemon/supervisor/claim.go:233-244`](../../internal/cli/daemon/supervisor/claim.go)).
The Fleet adapter reconstructs that history from `dep.add`/`dep.remove` events
because Fleet removes closed blockers from its ready projection
([`internal/backend/fleet/fleet_dependency_lineage.go:25-77`](../../internal/backend/fleet/fleet_dependency_lineage.go)).

Worktree preparation then resolves every delivery and searches for one
candidate commit containing all the others. If none exists, it returns the
divergent-input error that caused the observed claim/release loop
([`internal/localworkspace/task_worktree.go:310-357`](../../internal/localworkspace/task_worktree.go)).
The supervisor discovers this only after claiming, and releases the claim on
any preparation error
([`internal/cli/daemon/supervisor/supervisor.go:425-470`](../../internal/cli/daemon/supervisor/supervisor.go)).

`CandidateDependencyTaskIDs` does not provide a production escape hatch. It is
declared and implemented only in `internal/localworkspace`, with one direct
unit test; neither the supervisor port nor the daemon adapter forwards it
([`internal/localworkspace/task_worktree.go:27-35`](../../internal/localworkspace/task_worktree.go),
[`internal/cli/daemon/supervisor/claim.go:102-112`](../../internal/cli/daemon/supervisor/claim.go),
[`internal/cli/daemon/daemon.go:136-147`](../../internal/cli/daemon/daemon.go)).

## Can the existing FleetDB contract represent the new fields?

Yes. FleetDB issues already have a non-null free-form `metadata` map
([`fleet-db/internal/models/issue.go:75-86`](../../../../../fleet-db/internal/models/issue.go)).
Create accepts metadata and Fleet provides first-party set/remove endpoints
([`fleet-db/internal/api/request.go:29-52`](../../../../../fleet-db/internal/api/request.go),
[`fleet-db/pkg/client/http.go:718-735`](../../../../../fleet-db/pkg/client/http.go)).
Keys may contain letters, numbers, dots, underscores, and hyphens; values are
bounded to 1024 bytes and an issue to 64 entries
([`fleet-db/internal/models/metadata.go:8-24`](../../../../../fleet-db/internal/models/metadata.go)).
The proposed keys fit those constraints.

Loom currently drops this data: `fleetIssueWire` has no metadata member,
`IssueData`/`IssueDetailData` have no metadata member, and `CreateParams` omits
metadata from `FleetCreateBody`
([`internal/backend/fleet/convert.go:13-44`](../../internal/backend/fleet/convert.go),
[`internal/backend/types.go:24-91`](../../internal/backend/types.go),
[`internal/backend/types.go:333-413`](../../internal/backend/types.go)).
Therefore the necessary contract work is in Loom, not FleetDB.

Do not add new Fleet dependency kinds such as `inherits-from` or
`integration-input`. That would mix code transport back into the scheduling
graph, require FleetDB model/API/projection/client changes, and make readiness
semantics ambiguous.

## Proposed invariant

For every task:

1. `blocks` dependencies are scheduling gates only.
2. `inherits_from` is absent or names exactly one direct `blocks` dependency.
3. An ordinary task has no `integration_inputs`.
4. An integration task has exactly one `inherits_from` base and at least one
   `integration_inputs` entry.
5. Base and integration inputs are distinct, non-empty, unique, not the task
   itself, direct blockers of the task at authoring time, and in the same source
   repository. Fleet omits satisfied blockers from the later ready/detail
   projection, so pre-claim validation rechecks shape, existence and repository
   but does not try to reconstruct the closed scheduling edge.
6. An integration output must contain the exact published commit of its base
   and every integration input as Git ancestors. A cherry-pick is insufficient
   because it does not preserve the input commit's immutable identity.
7. The lineage declaration is immutable after task creation. Changing it
   requires creating a replacement task. This avoids a metadata/dependency
   multi-write race.
8. Invalid lineage shape and references are rejected while authoring the task
   and, defensively, rechecked while scanning ready candidates **before** claim.
   It must never be handled by claim-then-release.

Git documents `merge-base --is-ancestor A B` as the exact reachability test for
whether A is an ancestor of B
([official Git `merge-base` documentation](https://git-scm.com/docs/git-merge-base.html)).
Git also defines `worktree add <path> <commit-ish>` as creating the linked
worktree at that explicit commit, which matches a single-base contract
([official Git `worktree` documentation](https://git-scm.com/docs/git-worktree.html)).

The CLI should make the valid path concise:

```text
# ordinary dependent task
loom data create ... --inherits-from TASK-A

# explicit integration task: starts at A and merges B
loom data create ... --inherits-from TASK-A --integration-input TASK-B

# downstream task inherits only the integration result
loom data create ... --inherits-from TASK-I
```

`--inherits-from` and every `--integration-input` should automatically be
included in the create request's `dependencies` union, so callers cannot forget
the corresponding scheduling gate. Other `--depends-on` values remain
ordering-only. Validation and normalization happen before the issue create.

## Smallest coherent implementation seam

### 1. Canonical Loom task-lineage value object

Add a small package such as `internal/tasklineage` containing:

```go
type Spec struct {
    InheritsFrom      string
    IntegrationInputs []string
}
```

It owns metadata key constants, deterministic JSON encoding/decoding,
deduplication, and validation against task ID, direct blockers, and source
repository. All CLI, HTTP, Fleet adapter, and supervisor paths call this one
implementation.

### 2. Carry metadata through Loom contracts

- Add `Metadata map[string]string` to `backend.IssueData` and
  `backend.CreateParams`.
- Preserve it in Fleet wire conversion and `FleetCreateBody`.
- Extend Loom's canonical OpenAPI create/detail schemas, regenerate Go and
  TypeScript types, and forward it through the web issue handler/service.
- Expose typed `inherits_from` and `integration_inputs` in CLI and web create
  requests; translate them to metadata only at the owning backend/service seam.

The main task authors today are `loom data create`, the lead prompt, and the web
create endpoint/modal. The CLI currently exposes repeatable `--depends-on`
([`internal/cli/data/create.go:13-36`](../../internal/cli/data/create.go),
[`internal/cli/data/create.go:85-107`](../../internal/cli/data/create.go)); the
lead explicitly tells operators to create epics and child tasks there
([`internal/cli/agent/prompts/lead.md:38-44`](../../internal/cli/agent/prompts/lead.md),
[`internal/cli/agent/prompts/lead.md:103-109`](../../internal/cli/agent/prompts/lead.md)).
The web API likewise accepts `dependencies`
([`internal/webui/handlers/issues/issues.go:88-104`](../../internal/webui/handlers/issues/issues.go)).
Team templates provision roles and agents, not epic task graphs; their apply
path creates role and agent records
([`internal/teamtemplate/apply.go:234-248`](../../internal/teamtemplate/apply.go),
[`internal/teamtemplate/apply.go:326-346`](../../internal/teamtemplate/apply.go)).

### 3. Replace dependency guessing at the worktree port

Replace `DependencyTaskIDs` and the unused `CandidateDependencyTaskIDs` with:

```go
BaseTaskID             string
IntegrationInputTaskIDs []string
```

Preparation resolves only `BaseTaskID` as the branch start. For each
integration input it resolves and records the immutable delivery ref, but does
not require that input to be an ancestor before the agent runs. Existing task
ownership, cleanliness, and OS lease behavior remain unchanged
([`internal/localworkspace/task_worktree.go:15-53`](../../internal/localworkspace/task_worktree.go),
[`internal/localworkspace/task_worktree.go:102-151`](../../internal/localworkspace/task_worktree.go)).

The integration task needs discoverable immutable refs. Reuse
`refs/loom/inputs/<workspace>/<task>/<input>` and include the task-ID-to-ref map
in its rendered task context. Do not rely on branch names. At publication,
retain the existing atomic check that input delivery refs have not advanced
([`internal/localworkspace/task_worktree.go:412-440`](../../internal/localworkspace/task_worktree.go))
and add an ancestry check from every recorded integration input SHA to the
output HEAD before updating the delivery ref.

### 4. Remove obsolete durable blocker lineage

`DependencyLineageBackend.DependencyTaskIDs`, its Fleet history replay, tracing
adapter, and lazy adapter become unnecessary for code inheritance and should be
deleted after no other consumer remains
([`internal/backend/issuebackend.go:190-197`](../../internal/backend/issuebackend.go),
[`internal/cli/issue_backend_tracing.go:279-297`](../../internal/cli/issue_backend_tracing.go),
[`internal/cli/deps.go:390-403`](../../internal/cli/deps.go)).
Fleet remains the scheduling authority through its normal ready projection.

## Migration and compatibility

This task-worktree implementation is still confined to the unmerged feature
branch under review, so the clean release migration is to replace its implicit
model before merge. Do not ship an inference fallback from `blocks` to
`inherits_from`; that would preserve the ambiguity and recreate divergent
fan-in behavior.

For any preview workspace already dogfooding this branch:

- stop its daemon;
- discard only that preview's task worktrees and Loom refs;
- add explicit lineage metadata to open tasks or recreate the disposable epic;
- restart after validation reports no invalid task specifications.

If this had already shipped, a separate offline migration would be required:
automatically set `inherits_from` only for tasks with exactly one intended code
parent, require a human choice for multiple blockers, and refuse to migrate
claimed tasks. That migration should finish before enabling the new runtime;
runtime dual-read compatibility is specifically not recommended.

Raw clients can still write Fleet metadata or dependencies without Loom's
validation because FleetDB deliberately does not own code-lineage semantics.
The supervisor's pre-claim defensive validator should report such tasks as
invalid configuration and skip them without claiming. The supported mutation
surface remains Loom's typed CLI/API.

## Required proof

### Unit and contract tests

- Ordinary task with no base starts from the configured default branch.
- Ordinary task with one base starts at that exact published SHA.
- Many scheduling blockers plus one base do not trigger ancestry guessing.
- Integration spec requires one base and one or more distinct merge inputs.
- Invalid/self/cross-repo lineage fails before issue creation and fails the
  defensive ready-candidate check before claim. The supported create path
  guarantees every lineage input is also authored as a blocker.
- CLI normalization adds base and integration inputs to the scheduling-edge
  union exactly once.
- Fleet create/get round-trips both metadata keys; Loom API and generated
  TypeScript types round-trip the typed fields.
- Integration preparation begins at only the base SHA and exposes every input
  ref without modifying the task branch.
- Publication refuses missing integration ancestry, a changed delivery ref,
  dirty bytes, and an output not descended from the prepared base.
- Publication accepts an actual merge containing every input.
- Legacy `CandidateDependencyTaskIDs`, multi-candidate base selection, and
  `DependencyTaskIDs` history replay have no production references.

### Deterministic orchestration proof

Run several supervisor polling intervals for an invalid multi-parent ordinary
task and assert zero claims, zero releases, zero sessions, zero worktrees, and
zero Loom refs. This directly proves the 16-cycle liveness bug is gone rather
than delayed.

### Real local Codex team proof

Use a fresh, uniquely named local stack and browser session. Create an epic of
at least six tasks:

```text
A ----> C -----------\
                      I ----> F
B ----> D -----------/
E (scheduling-only) -/
```

- A and B execute in parallel.
- C inherits only A; D inherits only B.
- I starts from C and explicitly merges D; E is a blocker of I but contributes
  no code.
- F inherits only I.
- Every task goes through the real implementation and QA stages.

Retain Git-ref and ancestry evidence proving C=A lineage, D=B lineage,
I contains C and D but not E as an input, and F contains I. Keep the stack up,
verify the epic in a dedicated agent-browser session, capture screenshot and
console evidence, and independently inspect the final downstream worktree/diff.
Also leave an invalid ordinary task with two attempted bases visible across
multiple scheduler intervals and prove it was never claimed.

## Blockers and non-goals

- There is no FleetDB blocker. Its existing metadata contract is sufficient.
- Loom must stop dropping issue metadata before the model can be implemented
  canonically.
- Integration is explicit agent work; automatic merge/conflict resolution by
  the scheduler is out of scope.
- Cherry-pick-only integration is out of scope because it cannot prove the
  immutable input commit is an ancestor of the delivery.
- Auto-closing the parent epic is unrelated.
