# Repo affinity

An agent may be bound to a subset of a workspace's repos with `repos:` (explicit
names) or `repo_groups:` (group bindings declared on each `RepoConfig`). An agent
that declares neither is fleet-wide and unaffected by everything below.

## The binding is a hard filter

A bound agent is **never offered another repo's work**. `cli.MatchTask` rejects a
foreign-repo issue outright (`Score 0`, reason `repo mismatch`) rather than
scoring it low, and `SelectBestTask` therefore cannot return one — not even a P0
foreign issue against a P3 issue in the bound repo.

Affinity used to be a scoring term: a mismatch produced `Score 5`, and since
`SelectBestTask` admits everything above zero, a bound agent could still be
handed another repo's task whenever nothing else was available. That made the
router unable to express a repo partition at all, which is the property any
integrator scale-out depends on.

Two further rules follow from making it a filter:

- **An issue with no `source_repo` is rejected too** (reason `repo unset`). This
  matches the fetch layer (`internal/backend/fleet.issueDataMatches`, which
  excludes an empty source repo whenever `SourceRepos` is set), so a preview and
  a real claim agree. Two layers disagreeing here is what produced the
  "the queue says claimable, the claim says nothing is ready" class of confusion.
- **The gate runs before the skill fallback.** `MatchTask` evaluates repo
  affinity before the `Score 10` "no skill match" early return, so a wrong-repo
  issue cannot slip through by failing the skill match.

## `cross_repo: true` is the escape hatch

`cross_repo` already means "ignore repo affinity" in agent-repo selection
(`localworkspace.SelectAgentRepos`) and worktree naming
(`Supervisor.NewAgent`); routing reuses the same flag rather than inventing a
second one. With `cross_repo: true` an agent keeps the pre-filter semantics
exactly: `+30` on a match, `Score 5` on a mismatch, neutral on an unset repo.

## An unresolved binding fails closed

`config.ResolveAgentRepos` errors when an agent declares affinity that resolves
to zero repos — a mistyped repo name, a group nothing belongs to, a repo with no
`source_repo_id`. That error used to be logged as a warning, after which the
claim proceeded with no `SourceRepos` at all — which drops *both* the fetch
filter and (with an empty constraint list) the router gate. A typo silently
promoted a bound agent to the whole fleet: the exact outcome the binding exists
to prevent.

Now:

- **Supervisor claim** (`buildClaimOpts`): the claim is refused with a preflight
  `repo binding unresolved: …`, in the same shape as "no claimable tasks". No
  ready query is issued.
- **`loom queue <agent>`**: exits non-zero. A queue computed against no binding
  is a queue for a different agent than the one asked about.
- **Agent spawn** (`spawn.go`): already fatal, unchanged.

An agent that declares no affinity resolves to `(nil, nil)` and is never
affected by any of this.

## The previews agree with the claim

Three call sites fetch the ready set *unfiltered* and rely on the router alone
for repo authority: `loom queue`, the webui agent-queue panel, and the agent-side
router check inside a spawned agent. All three now resolve the binding the same
way the supervisor does:

- `loom queue` and the webui panel share
  `config.ResolveAgentReposFromActiveWorkspace`, replacing two drifting copies of
  the same resolution.
- A spawned agent receives `LOOM_AGENT_CROSS_REPO` alongside `LOOM_SOURCE_REPOS`,
  so its own verdict matches the supervisor's. A missing or unparseable value
  reads as `false` — the strict side; widening must never be the consequence of a
  bad env var.

## What this does *not* do

It enforces whatever partition the bindings express; it does not choose one. If
every agent is bound to every repo, the filter rejects exactly the empty set and
two "partitioned" agents are not partitioned at all. Likewise, a repo in no
agent's binding is claimable by nobody — after this change that shows up in
`loom queue`'s rejection histogram under `repo mismatch` instead of being
silently absent. Deciding the bindings is an operator call.
