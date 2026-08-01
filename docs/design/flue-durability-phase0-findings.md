# Flue durability, Phase 0: what was built, why it was removed, what to keep

Status: closed 2026-08-01. Supersedes the FLUE-DURABILITY Phase-0 spike
(`41edcaf7`), whose code is removed by the commit that adds this note.

## What existed

`internal/workflows/builtin/flue-durability-store.mjs` — a flue
`PersistenceAdapter` wrapping flue's SQL agent-execution store
(`@flue/runtime/node` `sqlite()`) at a host path keyed by the **task-run id**.
The id is stable across reclaim (`internal/driver/task_scheduling.go` keeps it;
only the lease token rotates), so a relaunched runner would re-open the same
store and flue's reconciler would resume an interrupted submission mid-turn
rather than restarting it. It shipped with a contract spec riding flue's own
`defineStoreContractTests` and a runner script.

The adapter was correct. It was never reachable.

## Why it could not be wired (both blockers verified)

**1. No bundle path accepts it.** It targets flue's `db.ts` convention — a
source root's `db.ts` default-exports the adapter the runtime loads at boot.
loom's bundle layout has no such slot:

- `readWorkflowSourceFiles` (`internal/workflows/source_layout.go`) walks
  **only** `<root>/workflows/` and returns an error for any file whose
  extension is not `.ts`. A root-level `db.ts` is never collected; an `.mjs`
  anywhere under `workflows/` is a hard failure.
- The builtin path never reads the source tree at all: `//go:embed` covers the
  six `.ts` workflows, and `writeWorkflowBuildProject`
  (`internal/workflows/workflows.go`) writes only that embedded file map plus a
  generated `package.json` and symlinked deps.

**2. It would persist nothing.** Its stated scope was the on-host
`local-task-runner`, which makes **zero** flue agent calls: its bound agent is
`model: false` (a credential-free stub) and it `execFile`s a backend CLI
(`claude`/`codex`/…) directly. There are no flue agent submissions to
reconcile. The only real flue agent in the tree lives inside
`daytona-task-runner.ts`, which builds its own harness at runtime and
explicitly passes `defaultStore: new InMemorySessionStore()` — a different
runner, and one whose durability question is cross-node, not on-host.

## The finding worth keeping

Verified against flue's store architecture (2026-06), and the reason a future
cross-node store should not be hand-rolled:

> flue's stores are **SQL-driver parameterized**.
> `createSqlAgentExecutionStoreFromSql(sql)` builds the full
> `SessionStore` + `AgentSubmissionStore` — including the
> sessionKey/canonicalization derivations in `session-identity.ts` — from a
> low-level `SqlStorage`. A backend supplies **that driver**, not the ~25 store
> methods.

So cross-node durability is best implemented as a `SqlStorage` over fleet-db's
Postgres, reusing all of flue's store logic, rather than reimplementing the
store over a KV (the shape first sketched in the spike).

## What reviving this would require

All three, in order — any one alone is inert:

1. A real flue agent in the leaf that needs resuming. Today's local leaf
   deliberately shells out to backend CLIs; the daemon's own conversational
   resume is being solved separately via harness-wrapper `pkg/chat`
   (`Reopen` + continuation branches), which is the nearer-term answer for
   agent-run durability.
2. A bundle slot for a root-level `db.ts` (source layout + the builtin embed
   list), or an equivalent runtime hook to install an adapter.
3. For cross-node: the `SqlStorage`-over-Postgres backing above.

Related: the workflow runtime's own durability gaps (no step journal; a crash
outside an await boundary is swept to `failed` with no requeue) are a separate
and larger question — an agent-submission store does not address them.
