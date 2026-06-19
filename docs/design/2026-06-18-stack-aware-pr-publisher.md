# Stack-Aware PR Publisher — Design & Implementation Plan

Status: Proposed · Branch: `feat/stack-aware-worktree-lifecycle` · Date: 2026-06-18

## Scope

This document specifies the **native stacked-PR publisher** for Loom: the path that
takes a group of related tasks and publishes them as a stack of GitHub pull
requests where each PR's base is its predecessor's branch.

It is the publishing half of `STACKED-WORKTREE-PROPOSAL.md`. It is deliberately
**decoupled** from the *execution-correctness* half (making a dependent task's
worktree branch off its predecessor's output instead of the shared checkout's
`HEAD`). That layer — resolver base-selection and removing the resolver/runner
double-wrap in `internal/driver/task_worktree_resolver.go` and
`internal/workflows/builtin/local-task-runner.ts` — is tracked separately and
is a *complementary* concern: publishing manages PRs across branches; execution
correctness makes those branches contain the right code. This publisher consumes
per-task branches and does not depend on how their contents were produced.

Storage decision (carried from planning): lineage is a **loomcli-side projection
first**, behind a `Store` interface so it can move to fleet-db later without
touching callers.

## Why native (evidence)

We prototyped against two mature references on throwaway GitHub repos before
committing to a design. Both informed it; neither is usable as-is.

### spr (`ejoffe/spr`) — commit-id model

- Identity = `commit-id:` trailer in commit messages; branch = `spr/<base>/<id>`;
  PR base = previous unit's branch. We bridged Loom→spr with
  `commit-id = sha1(taskID)[:8]` and published a real 3-PR stack.
- ✅ Initial publish, ✅ idempotent re-run (`(skipped, no changes)`, zero churn),
  ✅ drop a unit (closes the PR).
- ❌ **Reorder ghost-merges the displaced PR**: swapping two adjacent units makes
  one branch contain the other as an ancestor, so GitHub auto-marks the displaced
  PR `MERGED`; spr recovers by opening a replacement PR. Reproduced even with full
  isolation and SHA-stable unchanged commits — it is *structural*, not churn.
- ❌ **PR discovery is account-wide** (`Viewer.PullRequests` in
  `github/githubclient/client.go`, matched by commit-id with no repo filter) and
  **global state lives in `$HOME`** (`~/.spr.yml`, `~/.spr.state`). Driving spr for
  multiple repos/stacks requires namespaced commit-ids + a unique `branchPrefix` +
  an isolated `$HOME` per invocation. Workable, but fragile.

### git-town — explicit-lineage model

- Identity = branch **name**; lineage = explicit parent pointers in repo
  `.git/config` (`git-town-branch.<child>.parent`); sync rebases each branch onto
  its parent.
- ⚠️ **`propose` is browser-based** — it pushes the branch and prints a compare
  URL; it does **not** create the PR via API. Unsuitable for headless PR creation.
- ✅✅ **Drop via `git town delete` is the cleanest of any test**: it
  `Updating target branch of proposal #N` (reparents the child PR to the
  grandparent via API) **then** deletes the branch / closes the dropped PR —
  correct order of operations.
- ⚠️ **Reorder via `git town set-parent`** *avoids* spr's ghost-merge (it reparents
  the PR via API **before** the conflicting push) but a full swap's second leg hit
  GitHub `422 "no new commits between base and head"`, leaving the stack partially
  reparented.

### Conclusion

| Operation | spr | git-town | Native target |
|---|---|---|---|
| Create PRs | API ✅ | browser ❌ | repo-scoped API ✅ |
| Idempotent update | ✅ | ✅ | ✅ |
| Drop a unit | ✅ | ✅✅ | ✅ |
| Reorder / swap | ghost-merge → self-heals | API reparent, but 422 on swap | **two-phase reorder** ✅ |
| PR discovery | account-wide | n/a (browser create) | **repo-scoped** |
| Lineage source | commit trailers | `.git/config` | **control plane, by task ID** |

Create / update / drop are solved by both models. **Reorder/swap is the one
genuinely hard operation, and neither tool gets it fully right** — they fail in
*different* ways, which proves the failure is intrinsic to naive ordering. The
native publisher's central contribution is a correct **two-phase reorder**.

## Design principles

1. **Explicit lineage keyed by task ID**, stored in the control plane — not
   `.git/config` (git-town's weakness: local-only, dies on re-clone) and not
   commit trailers (spr's weakness: requires rewriting history).
2. **Deterministic branch-per-task**: `loom/stack/<stack-id>/<task-id>`. Task ID
   is the stable unit identity; re-running a task updates the *same* branch.
3. **Base = predecessor branch** (root unit's base = the stack root branch).
4. **Repo-scoped GitHub API** for *all* PR operations (create / update / reparent /
   close) — fixes the weak spot in both references.
5. **Two-phase reorder** — reparent affected PRs to a safe base before moving
   branches, then set final bases. Avoids both the ghost-merge and the 422.
6. **Idempotent and fail-closed** — skip PR edits when title/body/base already
   correct (spr's check); never silently retarget; error when a predecessor branch
   is missing.

## Architecture

Four new loomcli packages plus a CLI group. Layering is strict: domain (no I/O) →
store → publisher → CLI.

### 1. `internal/stacklineage` — domain (pure, no I/O)

```go
type StackID string            // "epic:<id>" | "manual:<name>" | "auto:<repo>/<topic>"
type CommitMode string          // "loom_commit" (default) | "agent_commit" | "squash_on_publish"

type Stack struct {
    ID           StackID
    WorkspaceKey string
    RepoName     string
    RootBase     string         // branch or SHA the first unit builds on (e.g. "main")
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Node struct {              // one task's slot in the stack
    StackID      StackID
    TaskID       string         // stable unit identity
    BaseTaskID   string         // explicit predecessor; "" = root unit
    OutputBranch string         // "loom/stack/<stack>/<task>"; set once published
    State        string         // pending | published | closed
    UpdatedAt    time.Time
}

// OutputBranchName is deterministic and sanitized to [A-Za-z0-9._/-].
func OutputBranchName(s StackID, taskID string) string  // loom/stack/<s>/<taskID>

// Ordered returns nodes in lineage order; errors on cycle / missing predecessor.
func Ordered(stack Stack, nodes []Node) ([]Node, error)
```

Base selection is explicit: a node's base is `BaseTaskID`'s output branch, or
`RootBase` for a root unit. Fail closed when a referenced predecessor has no
published branch.

Lineage is a **forest of linear chains**: a stack (epic) may hold multiple
independent chains rooted at the same base (parallel sub-stacks), but every unit
has at most one successor, so each chain stays linear and every PR's base is
unambiguous. `Ordered` allows multiple roots and rejects only mid-chain branching
(a unit with two successors) and cycles. `loom stack add --root` starts a new
parallel chain; `--after` chains onto a predecessor. (True trees — a unit with
multiple successors — remain out of scope.)

### 2. `internal/stackstore` — persistence (loomcli-side, swappable)

```go
type Store interface {
    GetStack(ctx, ws string, id StackID) (*Stack, error)
    ListStacks(ctx, ws string) ([]*Stack, error)
    PutStack(ctx, *Stack) error
    ListNodes(ctx, ws string, id StackID) ([]*Node, error)
    UpsertNode(ctx, *Node) error
    DeleteNode(ctx, ws string, id StackID, taskID string) error
}
```

- `LocalStore` backs it with `~/.loom/stacks.json`, using the existing
  `configlock.WithLock` + atomic-write discipline (sibling to `state.json`; keeps
  `WorkspaceLocalState` lean and the boundary clean).
- A future `FleetDBStore` implements the same interface against a fleet-db entity;
  no caller changes. (This is why lineage is *not* folded into `state.json`.)

### 3. `internal/stackpublish` — the GitHub publisher (repo-scoped)

```go
type Forge interface {                       // repo-scoped; no account-wide queries
    ListOpenPRs(ctx, owner, repo string) ([]PR, error)            // GET /repos/{o}/{r}/pulls
    CreatePR(ctx, owner, repo string, p PRSpec) (PR, error)
    UpdatePR(ctx, owner, repo string, num int, p PRSpec) error    // title/body/base
    ClosePR(ctx, owner, repo string, num int, comment string) error
    PushBranches(ctx, repoPath string, refs []RefSpec) error      // --force-with-lease, atomic
}

type Reconciler struct { Store stacklineage.Store; Forge Forge }
func (r *Reconciler) Publish(ctx, ws string, id stacklineage.StackID, opts PublishOpts) (Report, error)
```

- GitHub client mirrors the token + credential-helper pattern already proven in
  `local-task-runner.ts deliverPullRequest` (token never in argv; repo-scoped REST).
- **Idempotent**: skip `UpdatePR` when title, body, and base are all unchanged.

### Branch materialization (input contract)

A unit's branch `loom/stack/<stack>/<task>` must exist at the task's output commit.
Two sources, in priority order:

1. **From the execution layer** — the output-branch finalization (separate work)
   already produced the branch during the task run. The publisher just pushes/PRs.
2. **From a patch (bootstrap/standalone)** — assemble in a Loom-owned **scratch
   clone** (never the canonical checkout): for each unit in lineage order,
   `git apply` the task's captured patch onto the predecessor branch and commit
   (`loom_commit` → one `git commit-tree` from the tree + predecessor parent).

The reconciler depends only on "branches exist at the right commits," so it is
testable independently of which source produced them.

### 4. Reconciler lifecycle — the two-phase reorder

Given desired stack state and the repo's current open PRs (fetched **repo-scoped**,
matched by our deterministic branch names):

```
Phase 0  Fetch current PRs for this stack's branches (repo-scoped).
Phase 1  REPARENT-TO-SAFE: for every PR whose base will change, set base = RootBase
         (e.g. main) via API. Breaks any head⊆base relation BEFORE branches move,
         so no ghost-merge; and main has fewer commits than any child, so no 422.
Phase 2  MOVE BRANCHES: force-with-lease push each unit branch to its new commit
         (atomic multi-ref push where the forge allows).
Phase 3  SET FINAL BASES: in stack order, set each PR base = predecessor branch.
         Both branches are now final, so "new commits between" holds → no 422.
         Skip when already correct (idempotent).
Phase 4  CREATE new units' PRs; CLOSE PRs for units no longer in the stack
         (with a comment).
```

Phase 1 is the step **neither spr nor git-town performs**, and it is exactly what
prevents both observed failures: the ghost-merge (spr) because no branch is pushed
while its PR's base could contain it, and the 422 (git-town) because bases are
never set between two branches with no commit delta.

### 5. `internal/cli/stack` — CLI

Registered via the blank-import + `init()`/`cli.RegisterCommand` convention
(`cmd/loom/main.go`). All commands read/write the `Store`; reads support `--json`
(agents consume it). Human + agent surface:

```
loom stack init <stack-id> --repo <name> --base <branch>
loom stack add  <task-id>  --stack <id> [--after <task-id>]
loom stack move <task-id>  --stack <id> --after <task-id>
loom stack set-base <task-id> --base-task <task-id>
loom stack show|status|validate <stack-id> [--json]
loom stack publish <stack-id> [--pr]          # runs the Reconciler
```

`validate` runs cycle detection + same-repo + predecessor-exists checks; `status`
shows each unit's branch, base, PR number, and state.

## Implementation plan (build order)

| # | Action | Path | Note |
|---|--------|------|------|
| 1 | create | `internal/stacklineage/{types,branch,order}.go` | pure domain + deterministic naming + ordering/cycle detection |
| 2 | create | `internal/stacklineage/*_test.go` | table-driven: naming, ordering, cycle, fail-closed base selection |
| 3 | create | `internal/stackstore/local_store.go` | `~/.loom/stacks.json` behind `Store`, `configlock` + atomic write |
| 4 | create | `internal/stackstore/local_store_test.go` | CRUD + concurrent-write lock (temp `LOOM_CONFIG_DIR`) |
| 5 | create | `internal/stackpublish/forge_github.go` | repo-scoped REST client (token via env cred helper, no argv) |
| 6 | create | `internal/stackpublish/reconciler.go` | the 4-phase lifecycle + idempotent skip |
| 7 | create | `internal/stackpublish/materialize.go` | output-branch (primary) + patch-assembly (bootstrap) in scratch clone |
| 8 | create | `internal/stackpublish/reconciler_test.go` | plan computation: new/removed/reorder/unchanged; phase ordering |
| 9 | create | `internal/cli/stack/*.go` | cobra group: init/add/move/set-base/show/status/validate/publish |
| 10 | edit | `cmd/loom/main.go` | blank-import `internal/cli/stack` |

Ships behind the existing feature branch; no fleet-db changes this iteration.

## Test plan

- **Unit** (no network): branch naming determinism + sanitization; lineage
  ordering + cycle detection; fail-closed base selection (no fallback to default
  branch when a stacked predecessor is missing); reconciler plan computation for
  each delta kind; phase ordering invariant (no branch pushed while its PR base
  could contain it).
- **Integration** (throwaway GitHub repo — the POC harness is the fixture):
  initial publish → idempotent re-run (assert zero PR churn) → **drop** (assert PR
  closed, descendant reparented) → **reorder/swap** (assert **no `MERGED` ghost
  PR**, **no 422**, correct final bases, stable PR numbers). These three assertions
  are exactly the spr/git-town failures this design must beat.
- **Isolation**: integration runs use a dedicated repo + repo-scoped queries, so
  no account-wide bleed (the spr failure mode is structurally impossible here).

## Decisions (2026-06-18, from 4-agent edge-case analysis)

1. **Reconcile posture: fully control-plane authoritative.** `publish` silently
   corrects all drift to match desired lineage (idempotent, headless-first). A PR
   merged on GitHub is detected in Phase 0, marked `merged` (terminal — never
   reopened/retargeted), and its descendants **auto-slide** to `RootBase` on the
   same run. Consequence: a human's manual PR-base edit is overwritten on the next
   publish; this is accepted. (No `loom stack ship` gate — auto-slide is built into
   the reconciler.)
2. **Branch GC: keep branches; explicit `loom stack gc`.** Phase 4 closes PRs for
   removed units but never deletes their branches. Non-destructive; preserves
   cherry-pick and future PR-reopen. A separate `loom stack gc <stack>` does cleanup.
3. **Branch naming: readable name, hash suffix only on collision.**
   `OutputBranchName` sanitizes to a valid ref (`git check-ref-format`) and uses the
   plain `loom/stack/<stack>/<task>` form; only when two task IDs collide within a
   stack does it append `-<sha256(rawTaskID)[:6]>`. Collision check runs at
   `loom stack add` time.
4. **PRs: always ready-for-review, no auto-draft.** No draft toggling anywhere.
   Consequence: the Phase 1→2 reorder window (a PR briefly reparented to `RootBase`)
   is *not* protected by drafting. Accepted, with these built-in mitigations (no
   further decision): Phases 1 and 2 run back-to-back with no human step between;
   only PRs whose base actually changes are reparented; the atomic multi-ref push
   keeps the window to seconds.

## Node state machine (adopted)

```
pending     in stacks.json; no branch/PR yet
published   branch pushed + PR open; BaseTaskID/OutputBranch set
conflicted  PR open but GitHub reports a merge conflict (downstream blocked)
empty       desired, but base..HEAD is empty → no PR (tree-SHA == predecessor)
merged      PR merged on GitHub (TERMINAL — never reopened; descendants slide to RootBase)
closed      PR closed by Loom (unit dropped); branch kept (decision 2)

pending→published|empty · published→{conflicted,merged,closed} · conflicted→{published,closed}
closed→published (re-add → new PR by default) · merged is terminal
```

## Edge-case handling (safe defaults — no decision needed)

Reconciler is re-runnable with **no progress cursor**: every run re-derives the
delta from forge truth (Phase 0), and all four phases are individually idempotent.

| Case | Handling |
|---|---|
| Externally closed (not merged) PR | create a replacement PR; record new number |
| PR in merge queue | **proactive GraphQL pre-flight** (`Forge.QueuedPRNumbers` via `mergeQueueEntry`): if any PR a reorder would reparent is queued, abort BEFORE any mutation with an actionable error (its base is immutable). Dry-run skips the check. |
| Pre-existing PR for a branch | adopt by `headRefName` in Phase 0; never duplicate |
| `GET /pulls` pagination | filter `head=owner:loom/stack/<id>/` + follow `Link` |
| Empty diff / no-op task | tree-SHA == predecessor → skip PR, mark `empty` |
| Patch conflict (assembly) | fail closed per-unit, mark `conflicted`; siblings unaffected |
| Predecessor branch missing | Phase 0.5 `git ls-remote` check; fail closed before any mutation |
| `force-with-lease` rejection | remote ahead of desired → skip; diverged → abort |
| Cross-repo `set-base` | reject at write time (validate task→repo via fleet-db) |
| Cycle / self-parent | `Ordered()` in `UpsertNode` → structured JSON error at write |
| Concurrent publish | per-stack lock (`~/.loom/stack-publish-<id>.lock` via `configlock`) |
| Submodules / Git LFS | unsupported → fail closed (LFS push error surfaces clearly); revisit |
| Secret leakage | port `scrubToken()` to Go; scrub at Forge boundary + Report/log |
| `loom_commit` materialization | `git commit-tree` (tree + predecessor parent) — bypasses apply/CRLF/binary |

## Second-tier defaults (override anytime)

- **Auth:** HTTPS + token from `GITHUB_TOKEN`/`GH_TOKEN` (then `gh auth token`), via
  the env-backed credential helper (token never in argv), `HOME`/`GIT_CONFIG_NOSYSTEM`
  isolated. SSH and a fleet-db-secret source are later additions.
- **RootBase as SHA:** rejected at `loom stack init` (must be a branch name).
- **Rate limits:** retry 5xx with exponential backoff; fail-fast on 429 with a clear
  re-run message (respect `Retry-After` later).
- **Re-add a dropped task:** new PR by default; branch kept (decision 2) enables a
  future `--reopen`.
- **Multi-machine:** `stacks.json` is machine-local; documented limitation until the
  `FleetDBStore` swap. `stacks.json` carries `version: 1` for migration.
- **`--dry-run`:** Phase 0 + plan only, JSON output; the plan doubles as audit.
- **Observability:** `Node` gains `PRNumber`/`PRURL`/`OutputSHA`/`LastPublishedAt`;
  append-only `~/.loom/stack-publish-log.jsonl` for history.
- **PR status display (implemented):** `Forge.PRStatuses` (GraphQL checks/review/
  mergeable) feeds `Reconciler.StackStatus` → `loom stack status` shows live health
  + a derived `next-to-merge` marker (`--json` for the UI; local-only without a
  token/repo-path). Read-only; does not change publish behavior.
- **PR-body stack listing (implemented):** publish phase 5 writes a delimited
  `loom-stack` section into each live PR's description (current unit marked),
  idempotently (only PATCH when the rendered section changes).

## Verification scenarios (extend the integration suite)

Beyond create → idempotent re-run → drop → swap (no ghost-merge, no 422): **drop the
root** (descendants → RootBase), **external merge** (unit→`merged`, descendants
auto-slide), **empty-diff task** (no PR, no 422), **insert in the middle**,
**mid-flight failure → re-run heals**, **patch conflict** (per-unit fail, siblings
unaffected), **name collision** (suffix applied).

## Open items / deferred

- `squash_on_publish` collapses to `agent_commit` until a squash step lands.
- fleet-db `FleetDBStore` swap-in (interface is ready) is a later iteration.
- SSH / fleet-db-secret auth sources (HTTPS+token ships first).
- Execution-correctness layer (resolver base-selection + de-double-wrap) is
  tracked separately; when it lands, the publisher's branch materialization uses
  source (1) and the patch-assembly path becomes bootstrap-only.
