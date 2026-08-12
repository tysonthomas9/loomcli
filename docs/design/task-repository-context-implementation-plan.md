# Task repository context implementation plan

**Decision:** [ADR 0001: Model task repository context separately from access and hand off pushed commits](../adr/0001-task-repository-context-and-change-handoff.md)

**Current-state research:** [ADR 0001 versus the existing implementation](adr-0001-existing-implementation-contradictions-research.md)

## Task 1: Prove a two-repository implementation-to-review flow

### Outcome

Deliver one complete working path in which:

1. a Task has `repo-a` as its Primary Repository and `repo-b` as a Selected
   Repository;
2. FleetDB admits a TaskRun with the exact repository set `{repo-a, repo-b}`;
3. the node provisions one composite TaskRun Root containing a worktree for
   each repository;
4. a real Codex backend starts in that root, changes both repositories, and
   authors commits on stable Task Branches;
5. Loom pushes and verifies both commits without exposing Git credentials to
   Codex;
6. FleetDB records immutable Task Change Set version 1 with the confirmed base
   and head of each repository; and
7. a different backend session reviews that exact change-set version from a
   separate TaskRun Root.

This is the first product proof. Adding repository fields without completing
this flow does not complete Task 1.

### Owning seams

Task 1 introduces three deep modules.

#### FleetDB TaskRun admission

FleetDB owns the logical contract and invariants for:

- Primary and Selected Repository names on the Task;
- the exact admitted TaskRun repository snapshot;
- implementation versus review execution class;
- the Task writer lease;
- stable per-repository Task Branch records;
- publication progress; and
- immutable Task Change Set versions.

Repository names are exact workspace-scoped `Repo.Name` values. Repository
groups are not part of Task 1. Repository membership is preferred working
context, not authorization.

#### TaskRun Root Manager

The node-local Root Manager owns the physical realization of an admitted
repository set behind this interface:

```go
Prepare(ctx context.Context, spec RootSpec) (RootManifest, error)
Release(ctx context.Context, lease RootLease, policy RetentionPolicy) error
Reconcile(ctx context.Context) error
```

Task 1 implements the local Git adapter. The module owns staging, containment,
one worktree per repository, atomic manifest publication, rollback, fenced
cleanup, stale Git registration pruning, and inventory reporting.

The physical shape is:

```text
<workspace>/.loom/task-roots/<task-run-id>/
  manifest.json
  repo-a/
  repo-b/
```

The composite directory is the backend's starting directory. It is not itself
a Git worktree.

#### Task completion and publication

One completion module owns Git validation and publication behind this
interface:

```go
Finalize(ctx context.Context, claim CompletionClaim) (CompletionOutcome, error)
```

Its outcomes are:

- `no_change`;
- `continuation_required`;
- `ready_to_review`; or
- `failed`.

The module verifies every repository, invokes Backend Continuation for dirty
state, pushes backend-authored commits through Loom-held credentials, confirms
the exact remote heads, records partial publication durably, and creates the
Task Change Set only after all changed repositories are confirmed.

### Canonical contract

Add the minimum durable model required for the proof.

#### Task

```text
primary_repository: optional Repo.Name
selected_repositories: sorted unique Repo.Name[]
```

#### TaskRun execution context

```text
execution_class: implementation | review | correction
repository_set: exact Repo.Name[]
root_generation: integer
root_state: pending | provisioning | ready | retained | released | failed
root_node_id: node identity
root_fencing_token: integer
backend_kind: backend identity
backend_session_ref: native continuation identity
```

#### Task Branch

```text
task_id
repo_name
branch_name
admitted_base_sha
confirmed_remote_head_sha
expected_remote_head_sha
```

#### Task Change Set entry

```text
task_id
version
repo_name
base_sha
head_sha
branch_name
remote_name
publication_status
artifact_refs
```

FleetDB OpenAPI is the canonical schema. FleetDB server types, Loom Go types,
and runner TypeScript types are generated from it and updated together. The new
contract rejects unknown repository names and malformed or missing required
fields at admission. Do not add normalization or `source_repo` compatibility
logic to this path.

### Implementation order

#### 1. Contract and storage

- Add the canonical schemas, migrations, indexes, validation, and generated
  consumers.
- Migrate an existing Task's singular `repo` to `primary_repository`.
- Add a writer lease that permits only one implementation or correction
  TaskRun per Task while allowing concurrent review runs.
- Add repository-keyed publication state and versioned Task Change Sets.

Gate: FleetDB can admit a two-repository TaskRun and reject unknown,
cross-workspace, duplicate, or malformed repository identities.

#### 2. Composite local root

- Replace the singular resolver for this path with the Root Manager.
- Provision both worktrees in canonical repository-name order.
- Publish `manifest.json` only after every worktree succeeds.
- Roll back every child if any provision step fails.
- Start the runner in the composite root and pass the versioned manifest.

Gate: cardinality one and cardinality two use the same module and runner
envelope; neither can fall back to a workspace repository.

#### 3. Codex execution and continuation

- Convert the local Codex runner to consume the manifest.
- Instruct Codex to author commits in every changed repository.
- Persist the native Codex session reference on TaskRun.
- When completion finds dirty state, continue the same session in the same root
  and report the validation failure.
- Keep backend-continuation count separate from infrastructure and publication
  retry counts.

Gate: a dirty completion claim cannot become a patch-back success or a
Loom-authored commit.

#### 4. Push proxy and Task Change Set

- Assign stable Task Branches before execution.
- Push fast-forward-only with credentials held outside the backend process.
- Verify each exact remote SHA after push.
- Preserve confirmed repository results after partial publication failure and
  retry only incomplete repositories.
- Create Task Change Set version 1 only after every changed repository is
  remotely confirmed.

Gate: Loom never runs `git commit` and never normally force-pushes a Task
Branch.

#### 5. Independent review

- Admit a review TaskRun naming Task Change Set version 1.
- Provision a separate composite root pinned to its exact repository heads.
- Start a different backend session in that root.
- Store repository-keyed findings and derive one aggregate review result.
- Reject a review result if its observed head differs from the change set.

Gate: review never reads the implementation root and passes only when every
required repository result passes.

#### 6. Lifecycle completion

- Retain failed roots for diagnostics.
- Release successful roots only after remote heads and required artifacts are
  durable.
- Remove each child through its owning Git repository before deleting the
  composite directory.
- Reconcile FleetDB state, manifests, and `git worktree list --porcelain` after
  restart or lease loss.
- Derive root and worktree totals from manifests.

Gate: repeated or stale cleanup requests are safe, and a stale fence cannot
remove a newer root.

### Required verification

#### Deterministic integration proof

Use two temporary repositories with bare remotes and a controlled backend
adapter. Verify:

- exact repository admission;
- atomic two-worktree provisioning;
- rollback after the second repository fails;
- no first-repository or opaque-input fallback;
- writer exclusion for concurrent implementation runs;
- dirty-state continuation;
- backend-authored commit preservation;
- fast-forward push and exact remote-head verification;
- repair of partial multi-repository publication;
- immutable change-set creation;
- review pinned to both exact heads; and
- fenced, idempotent cleanup and reconciliation.

#### Real backend proof

Run the same flow with the real local Codex backend. Evidence must include:

- the implementation TaskRun transcript and native session reference;
- the composite-root manifest;
- backend-authored commit identities in both repositories;
- confirmed remote branch heads;
- Task Change Set version 1;
- a distinct review TaskRun/session and root; and
- its aggregate repository-keyed verdict.

Real Codex execution may consume paid service capacity and requires the normal
configured Codex authentication. This must be disclosed before running the
proof.

### Explicit exclusions

Task 1 does not include:

- repository-group editing or UI;
- runtime Repository Acquisition;
- Daytona or other remote Root Manager adapters;
- Claude, Gemini, Cursor, or OpenCode conversion;
- pull-request creation or stack reconciliation;
- per-repository access modes in Repository Set;
- broad WorkerProfile repository-affinity redesign; or
- automatic migration of ambiguous queued TaskRuns.

The canonical schemas and manifest must leave room for later adapters, but Task
1 must not wait for them.

### Forbidden fallbacks

The Task 1 path must not:

- infer repository identity from TaskRun opaque input;
- match repositories by URL basename or `SourceRepoID`;
- select the first sorted Workspace repository;
- expose a singular `LOOM_WORKTREE_PATH` as the canonical runner contract;
- accept dirty state as successful patch delivery;
- let Loom or a runner author the backend's commit;
- force-push normal Task Branch history;
- review the mutable implementation directory; or
- use Repository Set membership as an authorization decision.

### Definition of done

Task 1 is complete only when the deterministic integration proof and real Codex
proof both pass end to end, all shared types come from the canonical generated
contract, and the converted path contains none of the forbidden fallbacks.

The next task should add runtime Repository Acquisition to the same TaskRun and
Root Manager seams; it should not introduce a second provisioning or runner
contract.

