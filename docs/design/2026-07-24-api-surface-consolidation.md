# API surface consolidation — should we collapse the 167 routes?

> **Status:** Proposal · *2026-07-24* — **revised 2026-08-03** after an
> adversarial review and an independent fact-check. The revision **cut the
> draft's one affirmative consolidation proposal** (a `git/{op}` dispatcher) and
> **inverted one of its two headline "ground facts."** §9 records exactly what
> changed and why, so the earlier version is not quotable as current.

## 1. Bottom line

**No. Route count is not the problem, and consolidating endpoints would make
this API measurably worse.** The honest answer to "can't we consolidate these
endpoints?" is that the question contains a false premise: nothing in this repo
records a cost being paid for having 167 routes, and the three things that
*would* make the surface nicer to use — dead-route removal, correct OpenAPI
schemas, one error envelope — are all achievable **without changing a single
route**.

What the evidence supports:

| Action | Routes changed | Breaking? | Confidence |
|---|---|---|---|
| **Tier 0** — delete unreachable Go (dead `agentcontrol` package, shadowed registrations, duplicate handler) | **0** | No | Medium — gated on a runtime route dump |
| **Tier 1** — delete dead/tombstoned routes | **−5 free, up to −16 with owner decisions** | Yes, but nothing consumes them | High for the free 5 |
| **Tier 2** — fix the agent git cluster's **OpenAPI schemas** | **0** | No | High |
| **Tier 3** — unify the two incompatible error envelopes | **0** | No | High |
| ~~Collapse the git verbs into `POST git/{op}`~~ | ~~−5~~ | ~~Yes~~ | **Cut — see §3.5 and §9** |

Realistic net effect: **167 → ~162 without any judgement call, → ~151 if every
owner decision goes the delete way.** Consolidation proper contributes **zero**.
If only one thing gets done, do Tier 2 — it is non-breaking, it is the change
the git cluster actually needs, and the draft's dispatcher proposal would have
made it permanently impossible.

### Why "no" and not "a little"

Four measurements, each independently reproducible:

1. **There is no recorded pain.** `git log --grep=route -i` surfaces nothing
   about route consolidation, and a grep for "endpoint sprawl" / "route count" /
   "surface area" across `docs/` returns exactly one file: this proposal.
2. **The contract is at zero drift right now.** `docs/api.md`, "Appendix: Spec
   Coverage vs Registered Routes" (`:6638-6645` at time of writing) reports 168
   spec operations, 167 registered routes, 167 matched, 0 registered-but-
   undocumented, 0 documented-but-unregistered, 1 served by a subtree mount.
   The gate demonstrably works at this scale. **That state is uncommitted
   working-tree work, days old, and every route change spends it** (§5).
3. **Registration is not the mass.** All `module.go` files under
   `internal/webui` total 2,967 lines against 16,944 lines of non-test Go under
   `internal/webui/handlers` alone. The granular git module registers 13 routes
   in a 48-line file (`internal/webui/handlers/git/module.go:29-48`); the 16-op
   dispatcher needs 831 lines
   (`internal/webui/handlers/driverapi/module.go`).
4. **The one place the draft wanted to consolidate has a schema bug, not a
   route-count bug.** Five verified spec defects in six git operations (§3.3) —
   all fixable at constant route count, none fixable behind a dispatcher.

> **Citation convention for `docs/api.md`.** It is generated and regenerates on
> every API change, so its line numbers churn — they shifted by 10 lines *during
> the writing of this revision*. The file says so itself: "Provenance is
> file-level on purpose. This document is staleness-gated, and citing `file:line`
> would make every unrelated edit above a registration fail the gate without any
> API change." **Cite it by section heading; the line numbers below are a
> convenience snapshot and should be re-derived, not trusted.** Every citation
> into Go, TypeScript, Rust and `api/openapi.yaml` in this document is line-exact
> and was verified individually.

### Two ground facts, corrected

Both are corrections *to the draft*, and both moved the recommendation.

**The 167 figure is already a distinct method+path count — not a count of
registration statements.** The draft claimed the opposite and used it to imply
part of the owner's instinct was a counting artifact. It is not.
`scripts/openapi-to-md/routes.go:57` builds `seen := make(map[string]bool)`,
keys it `found.Method + " " + found.Path` at `:72`, and skips repeats at
`:73-76`. Deleting every duplicate registration in the tree changes the route
count by **zero** — which is exactly what Tier 0 reports, and the draft
contradicted itself between its §1 and its §3. **No part of the 167 is
inflated.**

**The "~428 frontend `/api/…` call sites" figure is real, and the draft's
dismissal of it was wrong in a way that mattered.** The draft said 428 was an
import-specifier artifact. It was not:
`grep -rno '"/api/' internal/webui/frontend/src` returns exactly **428**. It is
a quoted-URL-literal count that includes tests and generated types. The honest
decomposition:

| Bucket | Count | Hand-edited on a route change? |
|---|---|---|
| `internal/webui/frontend/src/types/generated/openapi.ts` | 145 | No — regenerated |
| Frontend `__tests__/` + `test-utils/` | 213 | **Yes** |
| Production source | 70 | **Yes** |

Widening to all quote styles, production URL literals are **82 across 23 files,
21 of them under `src/api/`** — the two exceptions being a comment
(`internal/webui/frontend/src/components/TerminalView/instances/TerminalInstance.tsx:53`)
and a test helper
(`internal/webui/frontend/src/test-utils/chrome-visual-helpers.ts`). But **82
structurally undercounts**, because it only finds strings containing `/api/`,
and `wsUrl()` (`internal/webui/frontend/src/api/common/client.ts:74-76`)
prepends `/api/workspaces/${ws}` to a *suffix* literal — **31 further non-test
call sites**, covering `/monitor/status`, `/agents/{name}/start`,
`/agents/{name}/logs`, `/runs/{id}/stream`, `/git/push-all`, `/terminal/ws`,
`/files/*`, `/issues`, `/events`, `/pull-requests`, `/workflows/{name}`, plus
everything behind `agentGitUrl`.

Add the Go side — **1,607 `/api/` occurrences across 149 Go `_test.go` files** —
and a hand-written route list at `test/fleetdb/ui/_support/coverage.ts` (20
entries, 169 lines), and **the hand-edited migration surface is roughly 1,900
sites, not 82.** The draft's "the frontend is not the blast radius people think
it is" headline was directionally backwards and has been removed. Risk is a
real objection, and it lives in the tests.

---

## 2. What the 167 routes actually are

### Where the mass is

| Cluster | Distinct routes | Character |
|---|---|---|
| agents (`/agents/{name}/…`) | ~25 | Mixed: 6 RPC verbs, 4 lifecycle verbs, 5 cacheable reads, 4 transport-specific terminal routes |
| issues (`/issues/…` + `/ready`, `/blocked`) | 25 | Mostly genuine REST; 4 action verbs with real non-PATCH semantics |
| driver + task-run dispatchers | **8 registered** (2 of them `{op}` dispatchers) | **26 operations behind them** |
| workers + fleet | 9 | Machine-to-machine, separately deployed |
| monitor + daemon diagnostics | 11 | Several are projections of one aggregate |
| everything else | remainder | files, workflows, prreview, terminal, webhooks, approvals, auth |

The dispatcher row is not a typo. Eight routes are registered across the two
dispatcher modules — `internal/webui/handlers/driverapi/module.go:191,192,194,195,197,198`
and `internal/webui/handlers/taskrunapi/module.go:145,148` — because the `{op}`
pattern could not carry all the traffic. §6 returns to this.

### Genuine REST vs RPC in disguise

- **Genuine resources** (majority): issues CRUD, workspaces, files, agents
  create/read/update/delete, workflows, pull requests. Real identifiers, real
  representations, methods that mean what they say.
  `GET /api/workspaces/{ws}/issues/{id}` already embeds comments, dependencies
  and dependents unconditionally
  (`internal/webui/service/issue_backend_helpers.go:204-209`).
- **RPC verbs wearing REST clothing** (~12 routes): the six agent git mutations
  (`internal/webui/handlers/git/module.go:32-36,38`), the four agent lifecycle
  verbs (`internal/webui/handlers/agents/module.go:36-39`), and arguably the
  fleet verbs. `push` is not a noun.
- **Already-consolidated RPC** (26 ops): `POST /driver/{op}` and
  `POST /task-run/{op}`.

The critical framing point: **the route count already understates the operation
count.** 16 driver ops (`internal/webui/handlers/driverapi/module.go:142-157`)
plus 10 task-run ops (`internal/webui/handlers/taskrunapi/module.go:105`) sit
behind 2 dispatch routes. The true operation surface is ~191, not 167.
Consolidation shrinks the metric, not the surface — it moves complexity from the
mux (where a script counts and diffs it, and where a CI gate does exactly that)
into a map (where nothing does).

### How much is provably dead

Verified by repo-wide grep across the frontend, SDK, Go CLI, `internal/backend`,
`desktop/`, `scripts/`, `test/` and e2e specs, **with the concatenating-client
exclusion of §5 applied**:

- **3 tombstones** — routes that can only ever return an error.
- **10 zero-consumer routes** (only their own registration line plus tests).
- **9 duplicate or shadowed registration statements** that are not routes at all.

**Caveat that must ride with this list:** deadness here is *static*. An
independent fact-check re-swept every entry against dynamically-constructed
paths in `.ts/.tsx/.js/.mjs/.go/.rs/.sh/.yaml/.json/.py` and found **no route
wrongly labelled dead** — but it *did* reclassify two of them (§3.2, group E),
and it found a consumer class no grep in either pass would have caught: a
hand-concatenated raw-socket request in Rust
(`desktop/src-tauri/src/lib.rs:321`). **Re-verify per route immediately before
deleting; do not act on the list as a batch.**

---

## 3. Tiered recommendations

### 3.1 Tier 0 — code deletions that change zero routes

These remove source without touching the API surface, `api/openapi.yaml`, or
either generated client. Nothing regenerates.

**An entire serve branch is unreachable.** `internal/cli/serve/serve.go:227-231`
calls `log.Fatalf` if the store cannot be opened, and
`internal/cli/serve/serve.go:609-610` then assigns `cfg.Store = storeHandle.Store`.
`webuiapp.StartServer` has exactly one non-test caller
(`internal/cli/serve/serve.go:249`). Therefore `storeBacked` at
`internal/webui/app/server_modules.go:104` is always true, and the `else` branch
at `internal/webui/app/server_modules.go:147-154` never executes. That branch is
the *only* thing that constructs:

- the whole `internal/webui/handlers/agentcontrol` package — 5 registrations at
  `internal/webui/handlers/agentcontrol/module.go:29-33`, all duplicating live
  paths already served by `internal/webui/handlers/agents/module.go:30,36-39`;
- `internal/webui/handlers/git/pull_requests_module.go:27` (`GET /pull-requests`),
  already served by `internal/webui/handlers/prreview/module.go:102`.

**A ninth registration belongs here, and it is coupled to Tier 1.** The
daemon-backed agent-queue handler is gated by
`internal/webui/app/server_modules.go:31`
(`app.config.AgentQueueFn != nil && !storeBacked && !app.config.FleetClient`),
fed into the ops module at `:42`, and registered on the **inner** `wsMux` at
`internal/webui/handlermux/handlermux.go:99-100`. By the identical `!storeBacked`
argument it is dead, and with it `internal/webui/handlers_daemon.go:106`
(`HandleAgentQueue`), `daemonwire.BuildAgentQueueFn`
(`internal/cli/serve/daemonwire/daemon.go:361`) and `ServerConfig.AgentQueueFn`
(`internal/webui/server_config.go:104`).

> **Ordering constraint.** The live 501 at
> `internal/webui/handlers/agents/module.go:34` sits on the **outer** mux and
> shadows `handlermux.go:100`. Deleting the outer 501 (Tier 1, Group A) **without** also
> deleting `handlermux.go:99-100` would unshadow the inner handler in any world
> where `storeBacked` is not in fact always true — the opposite of the intended
> 404. **Delete both in the same commit, or neither.** (The mutual exclusivity
> is also what stops the two registrations from panicking Go's `ServeMux`.)

**Two shadowed registrations.**
`internal/webui/handlermux/handlermux.go:98` registers
`GET /api/workspaces/{ws}/config/backend` on the inner `wsMux`, but
`internal/webui/app/routes.go:155` registers the identical exact pattern on the
outer mux, and Go 1.22 `ServeMux` gives the exact pattern precedence over the
subtree mount at `internal/webui/app/routes.go:187`. The inner line is
unreachable. Likewise `internal/webui/handlers/misc/worker_api.go:460` re-mounts
`POST /api/internal/workers/register` on a path already covered by the subtree
mount on the preceding line (`:459`).

**One dead duplicate handler.**
`internal/webui/handlers/issues/diff_stat.go:19` (`handleGetIssueDiffStat`) is
byte-for-byte identical in body to the registered
`internal/webui/handlers/git/agent_diff_stat.go:40` (`HandleGetIssueDiffStat`,
wired at `internal/webui/handlers/git/module.go:41`) and is referenced only by
its own test file. Its test suite is the more thorough of the two — port those
six cases onto the live handler rather than dropping them.

*Confidence, stated once and not contradicted later:* **medium.** The deadness
of the `agentcontrol` and `agentQueueH` branches is inferred from one
`log.Fatalf`. If that becomes a soft failure, or a second `StartServer` caller
appears, both branches come back. **Verify with a runtime route dump from a live
`loom serve` before deleting.** Also note the package doc at
`internal/webui/handlers/agentcontrol/module.go:23-27` claims its agent list is
what `loom data agents list` consumes — stale, since the module never registers.
Do not use module doc comments as a consumer map.

**Tier 0 total: 9 registration statements and ~1 package deleted, 0 routes
changed, 0 client churn, no regeneration.**

---

### 3.2 Tier 1 — route deletions, sorted by what they actually need

The draft presented these as one 18-route block with a confidence label per
sub-group. The fact-check moved two routes between groups and the labels are
tightened accordingly. Groups are ordered by decreasing confidence.

#### Group A — free (5 routes). Delete without discussion.

| Route | Why |
|---|---|
| `PUT /api/workspaces/default` | `internal/webui/service/workspace_impl.go:448-451` returns `ErrUnavailable` unconditionally with both parameters discarded; registered `internal/webui/app/routes.go:144` |
| `DELETE /api/workspaces/default` | `internal/webui/service/workspace_impl.go:454-457`, same; registered `internal/webui/app/routes.go:145` |
| `GET /api/workspaces/{ws}/agents/{name}/queue` | `internal/webui/handlers/agents/handlers.go:169-171` is a hardcoded 501; registered `internal/webui/handlers/agents/module.go:34`. **Inherits Tier 0's medium confidence and its ordering constraint** — the real handler's gate is `internal/webui/app/server_modules.go:31`, not the `else` branch |
| `GET /api/monitor/stats` | `HandleStatsWithDataSource` (`internal/cli/serve/metricscmd/handlers.go:258-270`) runs the *full* `dataSource.Resolve(r)` sweep and then projects two fields — strictly redundant against `/api/monitor/status`, with no cost advantage |
| `GET /api/monitor/sync` | `HandleSync` (`:279-288`) — same shape: full `collectDataFn()` then project |

`StatsResponse` and `SyncResponse` (`internal/cli/serve/metricscmd/handlers.go:81-90`)
are field-for-field subsets of `StatusResponse` (`:93-108`), which
`HandleStatusWithSources` populates at `:155-157`.

The only real cost in this group: the 501 on `/queue` is self-documenting — it
says "exists, not in this mode" where a 404 says "you typed it wrong". That
diagnosis currently lives in the spec
(`internal/webui/frontend/src/types/generated/openapi.ts:1224-1228`) and is lost.

#### Group B — zero-consumer, but ask the owner first (6 routes).

`GET /api/monitor/stale-detector`, `GET /api/daemon/config`,
`GET /api/workspaces/{ws}/trigger-events`,
`GET /api/workspaces/{ws}/trigger-events/{eventId}`,
`GET /api/workspaces/{ws}/trigger-deliveries`,
`GET /api/workspaces/{ws}/trigger-deliveries/{deliveryId}`.

**Do not soft-pedal the cost.** These are audit/diagnostic reads whose intended
consumer is *a human with curl during an incident*; "nothing calls it" is the
expected state for such an endpoint, not evidence it is dead. The trigger-audit
four (`internal/webui/handlers/webhooks/module.go:46-49`) proxy an API fleet-db
already exposes directly — all live trigger traffic in the tree targets
fleet-db's own `/api/v1/…` surface
(`internal/infra/fleetdb/platform.go:870,889,914,935`), a different service —
and the `loom trigger events list|show` CLI reads the store directly
(`internal/cli/trigger/trigger_cmd.go:463,486`), not over HTTP. So they are
genuinely unconsumed; the question is whether the owner wants them anyway.

#### Group C — zero-consumer, and we recommend **keeping** them (2 routes).

`GET /api/workspaces/{ws}/runs/{runId}/events` and
`GET /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/stream`.

These are the reverse of dead — they are the *better* design that never got
adopted. `usePRReviewConversation` polls on a 1.5s timer
(`internal/webui/frontend/src/hooks/workspace/usePRReviewConversation.ts:11`)
while the SSE endpoint sits unused at
`internal/webui/handlers/prreview/stream.go:57`. Deleting it is choosing the
worse architecture because it is the one currently wired. Note also that the
frontend *does* use the sibling SSE route for workflow runs, built as a template
literal through `wsUrl`
(`internal/webui/frontend/src/hooks/workflows/useWorkflowRunStreams.ts:69-77`) —
exactly the trap a literal grep falls into, and the reason the per-route
re-verification caveat above is not boilerplate.

#### Group D — one pure alias, and the direction is an owner call (1 route).

`GET /api/workspaces/{ws}/monitor/status` is not a distinct endpoint.
`internal/webui/app/routes.go:158-170` is a closure that copies the path's
workspace into the `?workspace=` query string and calls the same injected
handler, which reads only that query param
(`internal/cli/serve/metricscmd/handlers.go:127`). No sibling monitor endpoint
has such an alias. The frontend calls both spellings —
`internal/webui/frontend/src/api/agents/agents.ts:94` (path-scoped) and `:102`
(flat).

*Counter-argument that must be weighed:* the path-scoped form is the **better**
REST shape and the only one wrapped in `workspaceMW`
(`internal/webui/app/routes.go:159`), which validates and resolves the workspace
before the handler sees it. Collapsing to the query form loses a validation
layer. The defensible alternative is the opposite merge — delete the flat form
and move `/monitor/agents` and `/monitor/tasks` onto path scoping too — costing
3 frontend functions instead of 1 and leaving the surface more consistent.
**Do not decide this on route count.**

#### Group E — harness-gated (4 routes). Cannot ship with Tier 1's other groups.

`GET /api/workspaces/{ws}/issues/{id}/comments` and
`GET /api/workspaces/{ws}/issues/{id}/dependencies` — **the draft had these in
the low-risk bucket and that was wrong.** Neither generated client calls them:
`internal/webui/frontend/src/api/issues/issues.ts` has `api.POST` to both
(`:493`, `:552`) and no `api.GET` to either; `internal/backend/api/backend.go`
POSTs comments (`:533`) and dependencies (`:492`) and DELETEs a dependency
(`:497`) with no GET for either sub-resource. **But both are GET-exercised by
the fleet-db parity Playwright suite** —
`test/fleetdb/ui/07-comments.spec.ts:43` and
`test/fleetdb/ui/08-dependencies.spec.ts:45` call `apiResponseDiff`, which
issues a plain `fetchJson` GET at `test/fleetdb/ui/_support/diff.ts:189-193`;
both are in the hand-maintained route list at
`test/fleetdb/ui/_support/coverage.ts:30-31` and asserted at
`test/fleetdb/ui/15-issue-route-coverage.spec.ts:180-181`. **The Playwright
suite is not one of the four regenerated artifacts, so the staleness gates will
not catch this breakage.**

`GET /api/monitor/workspaces` — consumed only by
`test/fleetdb/ui/_support/backends.ts:626-644`, and there only as a *fallback*
discovery path when `/api/workspaces` is unavailable under a fleet backend.
Deleting it removes harness resilience and may make that suite flaky against
fleet-mode stacks.

`GET /api/workspaces/{ws}/issues/search` — no shipped-code consumer in any
language. **Not a pure alias, and the doc should not pretend it is.** The two
routes reach different engines: `/issues/search` always goes through
`backend.IssueBackend.SearchIssues`
(`internal/webui/service/issue_impl.go:552-576`), while `GET /issues` prefers the
daemon pool's composite `ListKanban` RPC whenever a pool exists
(`internal/webui/service/issue_list.go:15-70`). In a daemon deployment, merging
would silently answer via `rpc.ListArgs.Query` — a filter, not relevance-ranked
search — changing ordering and recall. Limits differ too (search: default 100,
cap 500; list: `handler.MaxListLimit`). The repo's own Go client implements
`SearchIssues` as `GET /issues?q=` (`internal/backend/api/backend.go:314`), not
via this route. **Ask whether this is unfinished rather than dead**; server-side
issue search is a capability the UI may well want, and re-adding costs more than
keeping.

**Also weigh the resulting shape.** Deleting the two GET sub-resources while
keeping their POST siblings (`internal/webui/handlers/issues/module.go:53-54`,
`:60-62`) yields a resource you can write but not read — arguably a **worse**
API than the two routes it saves (1.2% of the surface). And you lose the ability
to refresh just the comment list after posting: callers must re-fetch the whole
detail document, which carries description/design/notes and can be multi-KB (the
list endpoint sets `Lightweight=true` precisely to avoid that,
`internal/webui/handlers/issues/issues.go:147-149`).

**Tier 1 totals: 5 free · 6 owner-confirm · 2 recommend-keep · 1 direction call ·
4 harness-gated. Maximum 16 deletions (9.6%), realistic free win 5 (3.0%).** No
new pattern is introduced anywhere.

---

### 3.3 Tier 2 — fix the agent git cluster's OpenAPI schemas (0 routes changed)

**This replaces the draft's `git/{op}` dispatcher and is the single highest-value
item in the document.** It is non-breaking, changes no route, needs no frontend
edit to ship, and is a prerequisite for the git module ever adopting the
generated client.

The draft justified collapsing the git verbs partly on "the typed-client cost is
already paid — there is no generated typing here to lose." **That is false.**
The generated types are real:

- `internal/webui/frontend/src/types/generated/openapi.ts:7392` — `gitPush` has
  a typed request body `{ target?: string }` and a `MessageResponse` 200.
- `:7424` — `gitPull` has `{ source?: string }`.
- `:7531` — `updateGitTarget` has a **required** request body.

What is true is narrower and much more damaging to the dispatcher idea: **the
git cluster's schemas are wrong.** Five verified defects across six operations:

| # | Defect | Evidence |
|---|---|---|
| 1 | `updateGitTarget` declares request property `target`; the handler requires `branch` and 400s without it | spec `api/openapi.yaml:3014` vs `internal/webui/handlers/git/git.go:288-290`, 400 at `:313-316`; frontend correctly sends `branch` (`internal/webui/frontend/src/api/workspace/git.ts:186-188`) |
| 2 | `gitPush`/`gitPull` 200 documented as `MessageResponse` — `{success: true (const), message: string}` — but the handler writes the full result object | schema `api/openapi.yaml:6422-6430`; handler `internal/webui/handlers/git/git.go:74` |
| 3 | **409 + `conflicted_files` is undocumented** | `internal/webui/handlers/git/git.go:69-71` (push), `:127-130` (pull); read by the UI at `internal/webui/frontend/src/hooks/workspace/useGitActions.ts:98-107` |
| 4 | **423 + `lock_info` is undocumented** — there is not one `"423"` response anywhere in `api/openapi.yaml` | `internal/webui/handlers/git/git.go:244-258`; read by the UI at `useGitActions.ts:109-117` |
| 5 | `gitCreatePR` returns **201** on one branch and 200 on the other; the spec documents 200 only, with a bare `type: object` | `internal/webui/handlers/git/git.go:198,200` vs `api/openapi.yaml:2969-2981` |

That drift survived precisely because the caller bypasses the typed client:
`internal/webui/frontend/src/api/workspace/git.ts:3` says so outright ("Uses raw
fetch because most spec responses are untyped `Record<string, never>`") and
imports hand-rolled `get`/`post`/`patch` from `@/api/common` at `:6`.

**The loop closes the right way.** Fix the schemas → `git.ts` can drop its
hand-rolled fetch and adopt the generated client → the typed/untyped split (§5)
narrows by one module → the 409/423/201 contracts become machine-readable
instead of tribal knowledge. Zero routes move. Nothing regenerates in the Go
client's *shape*, only its accuracy.

**Collapsing to `POST git/{op}` would make this permanently impossible.** The
house precedent proves what the generator emits for a dispatcher:
`internal/webui/frontend/src/types/generated/openapi.ts:9622-9625` types
`dispatchDriverOp`'s request body and 200 response as `{[key: string]: unknown}`,
and `internal/backend/api/gen/types.gen.go:3976` renders it
`map[string]interface{}`, because `api/openapi.yaml` declares them
`type: object, additionalProperties: true`. You cannot express five per-op
contracts through that hole.

*Deliverable:* one PR against `api/openapi.yaml` correcting the five defects,
plus regeneration of the two typed clients and `docs/api.md`. Optionally a
second PR migrating `git.ts` onto the generated client. **Neither changes a
route.**

---

### 3.4 Tier 3 — unify the error envelopes (0 routes changed)

Also promoted from the draft's closing paragraph, where it was buried.

The issues cluster emits **two mutually incompatible error envelopes, and the
same route can produce either**: `writeIssuesError` emits `{success, error, code}`
(`internal/webui/handlers/issues/issues.go:114-116`) while
`handler.HandleServiceError` emits `{error, kind}` with no `success` field
(`internal/webui/server/handler/errors.go:64-67`). `HandleListIssues` produces
both — `writeIssuesError` at `:144`, `:153`, `:175` and `HandleServiceError` at
`:163`. On the success side there are at least **13** distinct `Success bool`
envelope structs across `handlers/issues/` plus `graph.go`.

**This is what "consolidate the endpoints" is actually asking for.** A client
author's pain is "every endpoint answers differently," not "there are 167 of
them." Unifying envelopes is non-breaking if done additively, needs no route
change, needs no regeneration of route lists, and does not reduce the route
count by one. The structured-error upside usually claimed *for* a dispatcher is
already 90% present: `handler.HandleServiceError`
(`internal/webui/server/handler/errors.go:48-72`) gives every issue route a
status plus a machine-readable `kind`. Adding `Retryable`/`Details` to
`ServiceError` gets the rest with zero route churn.

---

### 3.5 Cut from the draft: the `POST git/{op}` dispatcher

The draft's one affirmative consolidation. **It does not survive review.** The
proposal was `POST /api/workspaces/{ws}/agents/{name}/git/{op}` with
`op ∈ {push, pull, sync, pr, reset, set-target}`, replacing six registrations at
`internal/webui/handlers/git/module.go:32-36,38` and leaving `GET .../git/status`
(`:37`) and `GET .../git/diff-stat` (`:42`) alone — so **8 git routes → 3, not
8 → 1**, a net of −5.

Five reasons it was cut, in descending weight:

1. **It forecloses Tier 2.** Six typed operations with five fixable schema
   defects become one operation with an unfixable `{[key: string]: unknown}`
   body. You trade the cheap correct fix for an expensive incorrect one.
2. **Its stated premise was false.** "There is no generated typing here to lose"
   is contradicted by `openapi.ts:7392,7424,7531`.
3. **Its own mitigation was self-contradictory.** The draft claimed the op enum
   *is* preserved in the spec so "op-name typos are still caught at compile time
   in TS" — true only through the typed client — while specifying the frontend
   change as "the one-function edit at
   `internal/webui/frontend/src/api/workspace/git.ts:74-83`". That function is
   `agentGitUrl(workspaceId: string, agentName: string, action: string)`.
   **`action: string` gets no typo checking at all.** Either the edit is one
   function (no type safety) or you get type safety (and the edit is a rewrite of
   `git.ts` plus its exact-URL assertions in `__tests__/git.test.ts`). Not both.
4. **The trade is bad, quantified.** −5 routes (3.0% of the surface) in exchange
   for: +6 ungated map keys, taking the ungated share of the ~191-operation
   surface from 26/191 (13.6%) to 32/191 (16.8%) — **a 24% relative increase in
   ungated operations to reduce route count by 3.0%**; −112 lines of generated
   reference documentation (156 lines today across the six `git/*` sections of
   `docs/api.md`, `:2520-2675`, versus ~44 for a dispatcher section); **−3 of the
   document's 20 structured "Body fields" tables** (`gitPush`, `gitPull`,
   `updateGitTarget`; `:2540`, `:2572`, `:2664`) — a 15% loss of all structured
   request-body documentation in the API reference; and one more route rendered
   with a **wrong** auth label (see 5).
5. **The generated auth documentation goes wrong.** The
   `POST /api/workspaces/{ws}/driver/{op}` section of `docs/api.md` (`:4219`)
   renders it "**Auth:** **None** (public route)" (`:4226`) — for the most
   privileged surface in the system — because the auth is enforced in-handler and
   is therefore invisible to the spec, and the renderer reads only
   `op.Security` (`scripts/openapi-to-md/render.go:225`). The granular
   `gitPush` section correctly renders "**Auth:** `BearerAuth`" (`:2525`).
   **Seventeen** routes already carry the misleading label; every consolidation
   adds more.

**Everything the draft got right about this cluster is preserved above.** Its
strongest finding — that a careless port would break the 409/423 contracts —
became Tier 2's defects #3 and #4, plus a third status the draft missed
(`gitCreatePR`'s 201, `internal/webui/handlers/git/git.go:198`). Its
observation that both consumers already dispatch by op string
(`internal/webui/frontend/src/api/workspace/git.ts:74-83`,
`internal/cli/data/agentctl.go:89`) remains true and remains an argument the
owner may weigh — see §4.

---

### 3.6 Also considered, also not recommended

#### Agent lifecycle verbs (4 → 1)

`POST /agents/{name}/{start,stop,restart,yield}`
(`internal/webui/handlers/agents/module.go:36-39`). **Looks like the best
candidate; is among the worst value.**

*For:* the four handlers are already a table-driven dispatcher one layer below
the mux — `HandleStart`/`Stop`/`Restart`/`Yield`
(`internal/webui/handlers/agents/handlers.go:129-167`) are four eight-line
`lifecyclePatch` literals (`:173-185`) over one shared `handleLifecycle`
(`:187`). The Go CLI parameterises the verb already
(`internal/cli/data/agentctl.go:89`, actions at `:39,48,57,66`) and would need
**zero changes**. The frontend calls exactly one of the four
(`internal/webui/frontend/src/api/agents/agents.ts:67`).

*Against:* because the dispatch table already exists, consolidation buys 3 route
strings and 4 spec entries and **nothing structural**. The four ops have
genuinely different status codes, and the two implementations already disagree
across serve modes (`internal/webui/handlers/agents/handlers.go:149-157` returns
202 for restart vs `internal/webui/handlers/agentcontrol/handlers.go:78-92`
returning 200) — while the spec documents only the **dead** module's behaviour
(`api/openapi.yaml:2835+`, tagged `agent-control`, 200 only). A dispatcher makes
that divergence harder to see. And the URL stops being self-describing in access
logs and browser network panes — a real cost on a human-operated control surface.

**If ever done, do NOT use `/agents/{name}/{op}`** — it would shadow
`/agents/{name}/git/…` and `/agents/{name}/terminal/…` in the ServeMux. Use
`/agents/{name}/lifecycle/{op}`. *Recommendation: skip.*

#### Path-parameter normalization `{issueId}` → `{id}` (0 routes, breaking)

The same domain concept has two parameter names: `{id}` in
`internal/webui/handlers/issues/module.go:42-62` versus `{issueId}` in
`internal/webui/handlers/issues/session_module.go:51-52` and
`tab_module.go:32-34`. This leaks into both generated clients —
`api/openapi.yaml:6375-6376` defines `components/parameters/IssueId` as
`name: id`, referenced by exactly 14 operations, alongside 5 inline
`name: issueId` declarations. Fixing it reduces client friction and reduces
**zero** routes, but it *is* a breaking path change. **If the owner ever does one
breaking pass, this rides along. Never as its own release.**

#### Registration ownership (0 routes)

`/blocked` and `/issues/graph` are registered by the git package
(`internal/webui/handlermux/handlermux.go:92,94` → `internal/webui/handlers/git/graph.go`),
and `GET /issues/{id}/git/diff-stat` by the git module
(`internal/webui/handlers/git/module.go:41`). Moving issue-shaped routes into
`IssueModule` is a navigability win with zero API change — *but* the git/diff
module is constructed conditionally
(`internal/webui/app/server_modules.go:113`) while the issue modules are not, so
relocating registration would make the route appear in deployments where it
previously did not. Low value, non-zero risk. Optional.

---

### 3.7 Explicitly NOT recommended

This section matters as much as the ones above — it is what stops a good Tier 1
from turning into a bad six-week refactor. Every claim here was independently
re-verified and none was weakened.

#### Terminal routes — hard no, with a security-shaped failure mode

`terminal/{info,token,ws,session}`
(`internal/webui/handlers/terminal/module.go:79-88`) is four routes with **three
incompatible transport contracts**: `info` and `session` are plain JSON, `token`
is JSON with `Cache-Control: no-store`
(`internal/webui/handlers/terminal/agent.go:130`), and `ws` is a **WebSocket
protocol upgrade and relay** (`:145-174`) that cannot ride a JSON op route at
all.

Worse, `terminal/ws` is **exempt from the JWT middleware by a path-suffix
match**: `internal/webui/server/middleware/auth_routes.go:54` marks it public via
`HasPrefix("/api/agents/") && HasSuffix("/terminal/ws")` after workspace-prefix
stripping, because the handshake authenticates with a one-time token in-handler.
Any reshape either (a) makes `/terminal/ws` stop matching, so the upgrade starts
demanding a JWT the browser cannot attach to a WebSocket handshake and every
terminal breaks, or (b) makes a consolidated path accidentally end in the exempt
suffix, **silently exposing sibling operations without auth**. There is a second
path-string dependency too: the frontend client special-cases 503 responses for
paths containing `/terminal/` to suppress the workspace-unavailable banner
(`internal/webui/frontend/src/api/common/client.ts:240,431`).

And `terminal/token` and `logs` are the **only two** routes in the whole agents
cluster with a real generated-client consumer
(`internal/webui/frontend/src/api/terminal/logs.ts:107,149`). There is no upside
to price against this.

#### Agent diff reads — no

`diff/commits`, `diff/files`, `diff/file`
(`internal/webui/handlers/git/module.go:45-47`) are three GETs returning three
genuinely distinct representations behind one already-shared envelope
(`internal/webui/handlers/git/diff.go:11-24`, handlers at `:27,55,74`).
Collapsing three cacheable, safely-retryable reads into a POST dispatcher trades
HTTP caching and self-documenting query strings for two saved lines. The
sub-proposal to merge `diff/file` into `diff/files?path=` is worse: the response
type becomes polymorphic on the presence of a query parameter, and two cache
keys collapse into one. **The envelope consolidation these routes would benefit
from has already happened, at the body level, without touching the URL space.**

#### Issue action verbs (close / reopen / claim / move) — no

Each maps to a distinct `backend.IssueBackend` operation with semantics PATCH
cannot express: `claim` does a blocker precheck then an atomic claim then forces
status (`internal/webui/service/issue_impl.go:351-395`, `ensureClaimable` at
`:399`); `close` handles `{Reason, Session, SuggestNext, Force}`
(`internal/webui/service/issue_impl.go:297`, fields at `:307-310`) with
already-closed idempotency via `backend.IsAlreadyClosedConflict` (`:317`);
`move` validates the target workspace and returns warnings
(`internal/webui/handlers/issues/move.go:34` declares the field, `:117`
populates it). **Collapsing into PATCH is a correctness regression.** Collapsing
into `POST /issues/{id}/{op}` removes 3 of 25 routes and costs: per-op
request/response schemas that `oapi-codegen` can only express as a loose
`oneOf` — making both generated clients *weaker*, the opposite of what
consolidation should buy; per-op status codes such as claim's
409-on-already-claimed; and per-op observability. See Tier 3 for the upside
without the cost.

#### `/ready`, `/blocked`, `/issues/graph` → `GET /issues?view=` — no

They return structurally *different* documents, not three filters of one:
`ready` returns `[]*ReadyIssueWithParent` with parent/repo for epic swimlanes
(`internal/webui/handlers/issues/ready.go:31-43`); `blocked`
(`HandleBlockedWithBackend`, `internal/webui/handlers/git/graph.go:140`) writes a
`{success, data}` pass-through over `[]backend.IssueData` at `:199-202`; `graph`
returns a `GraphResponse` with its own validation (`:368-378`). Three genuinely
distinct response types, each with its own query vocabulary, so the merged
parameter list becomes a grab-bag where most params are invalid for most views —
the exact failure mode that makes a consolidated endpoint harder to use than what
it replaced. *(The draft claimed `blocked` returns "issues enriched with blocker
details"; it does not — but the conclusion is unchanged, the three types differ.)*
*Renaming* them under `/issues/` is defensible as a naming fix at constant route
count, but only bundled into a single breaking pass.

#### Fleet and worker routes — hard no

`POST /api/workspaces/{ws}/fleet/{register,claim,heartbeat,done/{id}}`
(`internal/webui/fleet/module.go:48,60,64,68`) and the five
`/api/internal/workers/*` routes
(`internal/webui/handlers/misc/worker_api.go:451-455`) are the **only** routes
consumed by separately-deployed processes:
`internal/cli/serve/worker/worker_cmd.go:255,285`,
`internal/cli/serve/worker/log_forwarder.go:103`,
`internal/cli/lock_bridge.go:88`,
`internal/cli/automode/event_emitter.go:62` — all building URLs from a
configurable control-plane URL. With no versioning anywhere (§5), consolidating
here breaks any worker binary not redeployed in lockstep.

Structurally the fleet four are the best-shaped dispatcher candidate outside the
agents cluster — one noun, four POST verbs, all JSON. They are also the worst on
risk, and for a reason beyond deployment: **they have different auth.**
`register` is deliberately unauthenticated for self-registration
(`internal/webui/fleet/module.go:46-51`) while claim/done/heartbeat are
JWT-wrapped (`:54-69`). A single `{op}` route forces one auth decision at the mux
and pushes the register exemption into handler-level branching — a security
regression in the exact place you least want one. The blast radius is also
unmeasurable from inside this repo, which is disqualifying on its own.

#### File routes — hard no

`internal/webui/handlers/misc/files.go` implements strong ETag + `If-Match`
optimistic concurrency for the file editor: helpers at `:44-57` and `:59`, ETags
emitted at `:121`, `:146`, `:362`, `:443`, and the load-bearing `If-Match` reads
at **`:347` and `:375`**. This is the **only** genuine HTTP-semantics dependence
in the entire `internal/webui` surface — a repo-wide scan for
`Cache-Control`/`ETag` outside tests finds nothing else but `no-cache`/`no-store`
on SSE and auth endpoints. Collapsing these into a POST dispatcher would destroy
a working concurrency-control mechanism.

---

## 4. Arguments against this recommendation

The owner should see the strongest case *for* consolidating, not just the case
against. Four arguments survive; each is answered.

**1. "The git cluster really is RPC, and both clients already treat it that
way."** True, and verified. The frontend builds every git URL from a variable —
`agentGitUrl(workspaceId, agentName, action)` at
`internal/webui/frontend/src/api/workspace/git.ts:74-83`, called with `"status"`
(`:90`), `"push"` (`:100`), `"pull"` (`:115`), `"sync"` (`:129`), `"pr"`
(`:144`), `"reset"` (`:161`), `"target"` (`:186`). The Go CLI does the same for
lifecycle verbs (`internal/cli/data/agentctl.go:89`). A `{op}` server would just
be catching up to what the clients already believe.
*Answer:* the clients believing it is an argument about the clients, not the
contract. The contract is what the generated types and the drift gate protect,
and both get worse. Tier 2 gives the git cluster the thing it is actually
missing — accurate types — at zero route cost. If the owner still wants op-string
dispatch, do Tier 2 **first**, live with correct schemas for a release, and see
whether the itch remains.

**2. "The house precedent works and is documented as Current."** True.
`docs/design/driver-op-http-api.md` is marked Current and audited, and a dozen
design docs reference it as the canonical control surface. Nothing in the repo
records a complaint about it.
*Answer:* the case against extending it is about **fit**, not quality — see §6's
"the house precedent's own limits." It was built for machine clients with
hand-written type definitions; the git cluster has a browser client with
generated ones.

**3. "167 is genuinely a lot to hold in your head."** Also true, and the
strongest *felt* argument.
*Answer:* the felt problem is navigability, and the fix is not fewer routes but
fewer *shapes*. Tier 3 attacks 13 success envelopes and 2 error envelopes; Tier 2
attacks 5 lying schemas. Both make the surface easier to hold in your head at a
constant 167. A dispatcher makes it *harder* — the endpoint table in
`docs/api.md` (`:410`) renders 16 driver operations as **one** row, with the
per-op contract surviving only as an English sentence ending in an instruction to
go read Go source (`:4223`).

**4. "You are recommending three changes and calling it 'no'."** Fair
characterisation.
*Answer:* the question was specifically about consolidating *endpoints*. Tier 0,
2 and 3 change zero endpoints; Tier 1 deletes endpoints rather than merging them.
None of it is consolidation. Saying "no to consolidation, yes to these four
cheaper things" is the accurate answer, not a hedge.

---

## 5. Blast radius and migration

### Consumers, counted properly

| Consumer | Reality |
|---|---|
| **React frontend** | **82 production URL literals in 23 files, 21 under `src/api/`** — plus **31 non-test `wsUrl()` call sites** that build paths from a suffix and contain no `/api/` string at all. Zero raw `fetch()` outside `src/api/`; all 19 apparent hits are local `refetch()` |
| **Typed-client coverage** | Split roughly in half: **53 non-test `api.GET/POST/…` calls** through the generated client (`createClient<paths>` at `internal/webui/frontend/src/api/common/client.ts:272`) alongside the 31 `wsUrl()` sites through hand-rolled helpers. The split is *not* arbitrary — the untyped modules are exactly the ones whose routes have weak or wrong OpenAPI schemas (`internal/webui/frontend/src/api/workspace/git.ts:3`). **This split should drive candidate selection more than route count does — and it selects Tier 2, not a dispatcher** |
| **Go issue-tracker client** | `internal/backend/api/backend.go` — a first-class typed consumer of the issues cluster with 33 methods, invisible to any frontend grep because it builds every path by concatenation (`workspaceBasePath()` at `:96`, used at `:110`) |
| **Go CLI (`loom data`)** | `internal/cli/data/agentctl.go:89` concatenates the verb into the path, keeping `restart`/`yield`/`stop` alive with **zero literal grep hits** |
| **`@loom/sdk`** | Frozen contract on paper — `sdk/api-surface.v1.json` (**19** op paths, 25 error codes) plus dual contract tests — but **not published to npm** (`sdk/CHANGELOG.md:6-9`, `sdk/README.md:236`). Today it is a CI gate you control, not an external compatibility event. That window closes on first publish |
| **Tauri desktop app** | Wraps the same SPA, **and issues exactly one HTTP call of its own**: a raw-socket `GET /api/health` liveness probe (`desktop/src-tauri/src/lib.rs:310-330`, request string at `:321`, asserted at `:584`). Not a consumer of any route on the deletion list — but a non-TypeScript, non-Go caller that neither a frontend grep nor the Go client checklist surfaces |
| **External fleet workers** | Real, out-of-tree, ungreppable. See §3.7 |

### The frontend containment is lint-enforced

`internal/webui/frontend/eslint.config.js:182-185` sets
`"boundaries/dependencies": ["error", { default: "disallow", … }]` and the
`components` rule (`:247`) allows components/contexts/hooks/utils/styles/types
and **not** `api`. Components reach the API only through the
`internal/webui/frontend/src/hooks/api.ts` re-export barrel. **The containment
will not erode between now and whenever anything lands** — you are not racing a
decaying invariant.

### METHOD WARNING for any dead-route sweep

**Four** clients build loom paths without a greppable literal, so literal grep
will condemn live routes:

1. `internal/cli/data/agentctl.go:89` (Go concatenation) — keeps `restart`,
   `yield`, `stop` alive.
2. `internal/backend/api/backend.go:96,110` (Go concatenation) — keeps
   `/issues/{id}/claim`, `/issues/{id}/close`, `/issues/{id}/dependencies`,
   `/ready`, `/blocked`, `/issues/{id}/events` alive.
3. `sdk/runner.js:127` and `sdk/driver.js:604` (JS template concatenation).
4. `desktop/src-tauri/src/lib.rs:321` (**Rust, hand-built request string**) —
   keeps `GET /api/health` alive, and would be invisible to a `.ts`/`.go` sweep.

Frontend template literals through `wsUrl` are a fifth trap in the same family —
see `internal/webui/frontend/src/hooks/workflows/useWorkflowRunStreams.ts:69-77`.
**Any deletion pass must enumerate all of these by hand.** The Tier 1 list was
built with that exclusion applied and re-verified once; **re-verify per route
before acting.**

### The real blast radius is tests

- **1,607 `/api/` occurrences across 149 Go `_test.go` files** (~89 containing
  `git/`).
- **213 quoted `/api/` literals across frontend test files**, asserting exact
  URL strings.
- Playwright specs under `test/fleetdb/ui/`, plus
  `test/fleetdb/ui/_support/coverage.ts` — a **hand-written** 20-entry route
  list, 169 lines, **not covered by any staleness gate**.

Tests outnumber production call sites roughly 20:1. **This is where the
days-vs-weeks answer lives, and it is the main reason risk is a live objection
rather than a retired one.**

### There is no version boundary. None.

Verified: zero `/api/v1` registrations in non-test Go under `internal/webui`,
zero `X-API-Version`/`Accept-Version` handling anywhere in the repo, and no
`/api/v1` paths in `api/openapi.yaml`. The `/api/v1/` paths in this tree belong
to **fleet-db, a different service** that `internal/infra/fleetdb` is a *client*
of (`internal/infra/fleetdb/control_plane.go:42,50,60,70`). Do not cite them as
evidence a version boundary exists.

**Plainly: there is nowhere to migrate behind.** Every route change is
immediately breaking. The options are (a) a hard cutover coordinated with a
frontend release, or (b) dual-registering old routes as aliases for a
deprecation window. The repo has done (a) before and has the harness —
`internal/webui/app/routes_test.go:1846-1866` asserts the removed flat
`/api/agents/{name}/…` routes now return 404, and the `scopedRoutes` table at
`:1894-1913` asserts the workspace-scoped equivalents are handled. **That test
is the migration template.**

### The regeneration gate, and what it costs to spend

Every route change forces a four-way regeneration: `api/openapi.yaml`,
`internal/backend/api/gen/types.gen.go` (`make gen-go-api`),
`internal/webui/frontend/src/types/generated/openapi.ts`, and `docs/api.md`
(`make gen-api-docs`), with `check-go-api-staleness` and
`check-api-docs-staleness` wired into `make check-go` (`Makefile:526,528`).

**Current state is zero drift, and it is brand new.** 168 spec operations, 167
registered, 167 matched, 0 undocumented, 0 documented-but-unregistered
(`docs/api.md`, "Appendix: Spec Coverage vs Registered Routes"). **That state is
not yet in git history** — the last commit touching `docs/api.md` is
`1a36fbadf` (2026-05-28); the 167/167 appendix lives in uncommitted work in the
tree right now, and the file was regenerated *during the writing of this
revision*. Land it before spending it.

This is a **safety property, not an obstacle** — the gate makes it impossible to
land a partial consolidation. It is also the thing an op dispatcher turns off
(§6), which is why the timing argument matters: **you would be spending a
mechanism with a measured 0% drift rate to expand one with a measured 57% drift
rate.**

---

## 6. What consolidation actually costs

Six concrete degradations, each evidenced from the existing precedent rather
than from theory. This section is unchanged in substance from the draft — both
reviews agreed it was the strongest part — and two items are now
better-quantified.

**1. Typed clients degrade to `unknown`, and the generator cannot help.**
`internal/backend/api/gen/types.gen.go:3976` renders the driver dispatcher body
as literally `map[string]interface{}`, and
`internal/webui/frontend/src/types/generated/openapi.ts:9622-9625` types both the
request body and the 200 response as `{[key: string]: unknown}`, because
`api/openapi.yaml` declares them `type: object, additionalProperties: true`. The
op *name* survives as a string union (`openapi.ts:9601-9617`) — but only for
callers who go through the typed client, which the git module does not.

**2. Per-route observability collapses, and the repo went out of its way to
prevent exactly this.** Prometheus route labels
(`internal/webui/prom_metrics.go:20-26,123-137`) and OTel span names
(`internal/webui/otel_tracing.go:34-43`) both derive from `r.Pattern`.
`internal/webui/app/routes.go:176-186` exists *specifically* to pre-resolve the
nested-mux pattern so metrics show granular routes instead of a lumped prefix
bucket; the comment says so at `:176`. Today `exec-task` (seconds to minutes) and
`list-agents` (milliseconds) share one latency histogram and one error-rate
series. **Recoverable** — a dispatcher can call `webui.SetPromRoutePattern`
(`internal/webui/prom_metrics.go:169`) — but neither existing dispatcher does
(the only caller repo-wide is `internal/webui/app/routes.go:184`), so "the
precedent already handles this" is false.

**3. The doc-drift gate goes blind, and 57% of the mirrors have already
drifted.** `scripts/check-api-docs-staleness.sh` regenerates from a **syntactic
scan of route registration strings** (`docs/api.md`, appendix preamble,
`:6628-6632`). Granular routes
get a free, automatic drift gate. Ops get a hand-maintained enum in **seven**
places, and `emit-event` is present in three and absent from four:

| Mirror | `emit-event`? |
|---|---|
| `internal/webui/handlers/driverapi/module.go:157` (ops map) | present |
| `internal/webui/handlers/driverapi/contract_test.go:27` (`frozenDriverOps`) | present |
| `api/openapi.yaml` op enum | present |
| `sdk/api-surface.v1.json` — 19 op keys | **absent** |
| `sdk/driver.js` | **absent** |
| `sdk/driver.d.ts` | **absent** |
| `docs/design/workflow-driver-authoring-guide.md:258-266` op table | **absent** |

`grep -rn "emit-event" sdk/` returns nothing at all. And the gate that should
catch this is a tautology: `TestContractDriverOpNamesFrozen`
(`internal/webui/handlers/driverapi/contract_test.go:38-46`) compares
`h.module.ops` against `frozenDriverOps`, a literal declared **immediately above
it in the same package** (`:19-36`). It cannot see `sdk/api-surface.v1.json`, cannot
see `sdk/driver.js`, and passes green today with four mirrors drifted.

Note also that `sdk/api-surface.v1.json` stores a `fields` array per op. **That
file exists *because* the dispatcher cannot express per-op schemas in OpenAPI.**
It is a hand-maintained, ungated re-implementation of the "Body fields" table the
generator produces for free on granular routes — and it has already drifted from
its own changelog (`sdk/CHANGELOG.md:12-13` says 20; the manifest has 19).

**4. HTTP semantics.** Narrower than the textbook argument, because this repo
barely uses caching — the *only* genuine HTTP-semantics dependence outside SSE
and auth headers is the file editor's ETag/`If-Match` concurrency control
(`internal/webui/handlers/misc/files.go:44-59,347,375`). But method and status
semantics *are* load-bearing on frontend-facing routes in a way they are not on
driver routes: the driver dispatcher returns a flat 200 for every op
(`internal/webui/handlers/driverapi/module.go:262`), whereas the lifecycle verbs
encode async-ness in 202, `gitCreatePR` encodes creation in 201
(`internal/webui/handlers/git/git.go:198`), and git encodes conflict and lock
state in 409/423 *with domain payloads*. The frozen `opError` envelope
`{code, message, retryable, details?}`
(`internal/webui/handlers/driverapi/module.go:775-783`, whose comment reads
"FROZEN as the SDK v1 contract") has no room for any of that. Any
frontend-facing dispatcher must preserve per-op statuses — reintroducing the
per-op branching the consolidation was supposed to remove, one layer down.

**5. Documentation gets worse per operation, and the generator already shows
it.** Sixteen driver operations render as **one** endpoint-table row
(`docs/api.md:410`) and one section (`:4219-4262`) whose request body schema is
bare `object`, whose 200 schema is bare `object`, whose auth is rendered as
"**None** (public route)" (`:4226`) despite being the most privileged surface in
the system, and whose per-op contract survives only as an English paragraph
ending in an instruction to go read Go source: "The op set is defined as the keys
of the `ops` map in `driverapi.NewModule`" (`:4223`).

**6. The pattern does not reduce code — it relocates it.** The granular git
module registers 13 routes in a 48-line file
(`internal/webui/handlers/git/module.go:29-48`). The 16-op dispatcher needs 831
lines in `internal/webui/handlers/driverapi/module.go` alone — the
second-largest non-test file in `internal/webui` — plus a contract test to keep
the table honest. Route registration is ~1 line per endpoint; the handlers, DTOs,
tests and spec entries are the mass, and consolidation touches none of them.
**"167 routes is too much code" does not survive contact with the file sizes.**

### The house precedent's own limits

Two things to know before extrapolating from `driverapi`:

**It leaks.** `internal/webui/handlers/driverapi/module.go:191-198` registers the
`{op}` route *and then five more explicit routes*, with in-code comments saying
why: "two-segment paths the `{op}` pattern cannot match" (`:193`) and "same
two-segment-path situation" (`:196`), plus a GET SSE stream a POST-only
dispatcher cannot carry. `internal/webui/handlers/taskrunapi/module.go:148` has
the same escape hatch for a raw-body upload. The outcome is a **hybrid** — a
dispatcher plus leftovers — which is more shapes to learn, not fewer.

**It has never been tested by the client consolidation would affect.**
`grep -rn -- "/driver/" internal/webui/frontend/src` returns no matches outside
generated types. Every measured benefit was realised against machine clients —
`sdk/driver.js`, `sdk/runner.js` — with **hand-written** `.d.ts` files and no
reliance on generated types or per-route metrics. And the clearest evidence that
a flat `{op}` wire is not ergonomically good: **both SDKs immediately rebuild a
namespaced method-per-op facade on top of it by hand** (`sdk/driver.js:80-114`,
plus 481 lines of hand-written `sdk/driver.d.ts`), plus a hand-maintained
op-translation table at
`docs/design/workflow-driver-authoring-guide.md:258-266`. The wire being flat did
not make the developer experience flat — it made someone hand-write the structure
back, twice.

**Fair to the pro side:** nothing in the repo records a complaint about the
pattern, and `docs/design/driver-op-http-api.md` is Current and audited. **The
case against extending it is about *fit* — browser client, generated types,
per-route metrics — not about quality.**

---

## 7. Recommended sequencing

**Step 0 — measure the reachable surface, before touching anything.** Get a
**runtime route dump from a live `loom serve`**. The 167 figure is a static upper
bound: `scripts/openapi-to-md/routes.go:46-49` warns it reports
conditionally-registered routes as always present, and essentially the whole
surface is nil-gated (`internal/webui/handlers/agents/module.go:26`,
`prreview/module.go:99`, `webhooks/module.go:42`, `terminal/module.go:77-96` with
six independent gates). The target is the *reachable* set, which is smaller. This
also settles Tier 0's dead-branch question empirically rather than by reading
`server_modules.go`, which a static read cannot fully close — `bootstrap.OpenStore`
returning a non-nil handle with a nil `.Store` cannot be statically ruled out.

**Step 1 — Tier 2 (schema fixes). Ship this first; it is not gated on anything.**
Correct the five defects in §3.3 against `api/openapi.yaml`, regenerate the two
typed clients and `docs/api.md`. **Zero routes change; the appendix should still
read 167/167 with 0 drift.** Optionally follow with `git.ts` adopting the
generated client. *Gate: a passing e2e run covering conflict (409), lock (423)
and PR-created (201) paths through `useGitActions` — the three behaviours the
spec currently does not describe.*

**Step 2 — Tier 0.** Delete the dead `agentcontrol` package, the gh-backed
pull-request module, the two shadowed registrations, the dead `agentQueueH`
chain, and the duplicate `diff_stat` handler (porting its better test cases
first). **Zero routes change, so nothing regenerates.** *Gate: Step 0 confirms
the branches are unreachable at runtime.*

**Step 3 — Tier 1 Group A.** Delete the 3 tombstones and the 2 redundant monitor
projections. **−5 routes.** Regenerate all four artifacts. *Gate: the `/queue`
deletion must land in the same commit as `handlermux.go:99-100` (§3.1); re-run
the four-client checklist in §5 per route, not per batch.*

**Step 4 — decide Groups B, D and E with the owner, not in code.** Whether he
curls `/api/monitor/*` or the trigger-audit routes; the `monitor/status` alias
direction (collapse to query, or promote all three to path scoping); whether
`issues/search` is dead or unfinished; whether the two GET sub-resources are
worth the Playwright migration and the write-without-read shape. **−0 to −11
routes** depending on answers. *Gate for Group E: `test/fleetdb/ui/` and
`test/fleetdb/ui/_support/coverage.ts` updated in the same PR — no staleness gate
covers them.*

**Step 5 — Tier 3 (error envelopes).** Non-breaking, no route change, no
regeneration of route lists. This is the change that most improves what the
question was really about.

**Step 6 — stop and re-ask.** Re-count, live with it for a release, and see
whether the surface still feels too large. If it does, the next lever is still
**not** more dispatchers.

### If the owner overrides and wants one breaking consolidation pass

Then bundle everything breaking into a single cutover: Steps 3–4, the
`{issueId}` → `{id}` normalization (§3.6), the `/ready`, `/blocked`,
`/issues/graph` renames under `/issues/`, and — if he still wants it after
Tier 2 ships — the git `{op}` dispatcher **with raw status codes and per-op
response bodies preserved** (not the frozen `opError` envelope) and a
`SetPromRoutePattern(pattern+op)` call so metrics stay granular. Since there is
no version boundary, one coordinated cutover costs no more than the first one
does and the marginal breaking changes ride free. **This is the only circumstance
under which the naming fixes are worth doing, and the only circumstance under
which the dispatcher should be reconsidered.**

### If versioning ever gets introduced

`/api/internal/workers/*` and `/api/workspaces/{ws}/fleet/*` need it first —
they are the only routes where a version-skewed deployment breaks in production.
Everything else ships in one artifact with its client.

---

## 8. Open questions requiring a human decision

1. **`/api/monitor/stale-detector` and `/api/daemon/config`** — do you curl these
   during incidents? If yes they stay, and "zero consumers" was never the right
   test for them.
2. **The four trigger-audit routes** — delete, or keep as a local mirror of
   fleet-db's `/api/v1/…`?
3. **`monitor/status` alias direction** — collapse to the flat query form (1
   frontend function, loses `workspaceMW` validation) or promote all three
   monitor reads to path scoping (3 functions, more consistent)?
4. **`issues/search`** — dead, or unfinished? It reaches a different engine than
   `GET /issues` and is not a merge candidate either way.
5. **The two GET issue sub-resources** — worth a Playwright migration and a
   write-without-read shape to save 2 routes (1.2%)?
6. **The two unadopted SSE endpoints** — keep as the better-but-unwired design
   (recommended), or delete as unused?
7. **The git `{op}` dispatcher** — cut here, but it is your call. Recommendation
   is to revisit only after Tier 2 ships and only inside a single breaking pass.

---

## 9. What changed in this revision, and why

Recorded so the earlier draft is not quotable as current.

**Cut outright**

- **The `POST git/{op}` dispatcher (old §3, Tier 2).** Its stated premise — "the
  typed-client cost is already paid, there is no generated typing here to lose" —
  is falsified by `internal/webui/frontend/src/types/generated/openapi.ts:7392,7424,7531`.
  Its mitigation was self-contradictory (`action: string` gets no typo checking).
  Its trade is −5 routes for +6 ungated ops, −3 of 20 structured body-field
  tables, and one more wrongly-labelled auth row. And it would foreclose the
  cheaper fix. Replaced by Tier 2 (schema corrections).
- **"Consolidate a little, delete a lot" as the headline.** By the draft's own
  arithmetic, consolidation was 5/167 = 3.0% of the surface while the draft's own
  cost section (retained here as §6) showed it net-negative on five of six axes.
  An owner reading only the draft's bottom line got a green light for a change
  its own body showed to be a loss. The headline is now the direct answer: no.
- **"The frontend blast radius turned out to be far smaller than assumed" and
  "the strongest argument against a large consolidation is not risk."**
  Directionally backwards: the hand-edited migration surface is ~1,900 sites.

**Corrected (factual errors confirmed)**

- **"167 counts registration statements, not distinct paths."** Inverted. The
  scanner dedupes (`scripts/openapi-to-md/routes.go:57,72-76`); 167 **is** the
  distinct method+path count, and deleting duplicate registrations changes it by
  zero — as the draft's own Tier 0 said.
- **"The ~428 figure is an import-specifier artifact."** It is not;
  `grep -rno '"/api/'` returns exactly 428. Decomposed honestly in §1.
- **"The Tauri desktop app issues no HTTP calls of its own."** It issues one:
  `desktop/src-tauri/src/lib.rs:321`.
- **The two redundant issue-sub-resource GETs** were labelled "low risk"; they
  are GET-exercised by the Playwright parity suite
  (`test/fleetdb/ui/07-comments.spec.ts:43`,
  `test/fleetdb/ui/08-dependencies.spec.ts:45`) and moved to the harness-gated
  group. Step 2's old "the staleness gates will enforce completeness" was wrong
  for them.
- **The `/queue` tombstone cited the wrong gate.** The real gate is
  `internal/webui/app/server_modules.go:31` → `handlermux.go:99-100`, not the
  `else` branch, and Tier 0 undercounted its free win by one registration. An
  ordering constraint has been added.
- **Tier 0's "strictly free and should not be debated"** contradicted its own
  "verify with a runtime route dump" caveat twelve lines later. Confidence is now
  stated once: medium, gated on Step 0. The draft's Tier 1a "zero risk" is
  likewise downgraded for `/queue`, which inherits the same inference.

**Checked and NOT changed: the dead-route verdicts**

An independent sweep re-checked every route on the deletion list against
dynamically-constructed paths — TypeScript template literals, `wsUrl`/
`agentGitUrl` builders, Go string concatenation, JS template concatenation, and
Rust request strings — across `.ts/.tsx/.js/.mjs/.go/.rs/.sh/.yaml/.json/.py`
and `Makefile` under `internal/webui/frontend`, `test/`, `sdk/`, `desktop/`,
`scripts/`, `deploy/`, `internal/cli` and `internal/backend`. **No route is
wrongly labelled dead.** Two were reclassified by *consumer class* (the issue
sub-resource GETs, above), not by deadness.

That is a verification result, not a licence. It was produced at one commit, by
grep, against a codebase with at least five non-greppable URL-construction paths
(§5). **Re-verify each route immediately before deleting it, and delete in small
commits rather than one batch**, so a miss shows up as one broken thing rather
than nine.
- **Citations fixed:** `git/module.go:32-38` → `:32-36,38` (7 registrations, not
  the 6 mutations); every `docs/api.md` line reference was stale by 10–18 lines
  and has been re-derived (metrics table `:6620-6627` → `:6638-6645`; appendix
  preamble `:6609-6614` → `:6628-6632`; driver section `:4209-4247` → `:4219-4262`;
  op-set sentence `:4213` → `:4223`; git sections `:2510-2665` → `:2520-2675`) —
  and a citation-convention note now warns that this generated file churns;
  "fourteen routes carry the misleading auth label" → **seventeen**;
  `sdk/api-surface.v1.json` 20 op paths → **19**; "driver +
  task-run dispatchers | 2 registered" → 8 registered; `ensureClaimable`
  `:401-414` → `:399`; `move.go:37-63` → `:34` and `:117`;
  `graph.go:184-200` "enriched with blocker details" → `:140` handler, `:199-202`
  pass-through; `backend.go:97` → `:96`; `agentcontrol/module.go:24-27` →
  `:23-27`; `routes_test.go:1893-1911` → `:1894-1913`; `sdk/driver.js:79-113` →
  `:80-114`; `useGitActions.ts:97-104` → `:98-107` and `:110-118` → `:109-117`;
  `routes.go:180-186` → `:176-186`; monitor+daemon cluster "~10" → 11;
  "20+ methods" on the Go client → 33; "62 `api.*` calls" → 53 non-test;
  "28 `wsUrl()` sites" → 31; `writeAgentGitError` "used by every git handler" →
  every *agent-scoped* git handler (`HandleGitPushAll` uses
  `handler.RespondError` at `internal/webui/handlers/git/git.go:88`).
- **A third per-op status was missing** from the careless-port warning:
  `gitCreatePR` returns 201 (`internal/webui/handlers/git/git.go:198`), not just
  409 and 423.

**Kept from the draft, unchanged**

§6 in full (both reviews called it the strongest section), the "Explicitly NOT
recommended" section in full — the terminal suffix-exemption argument
(`internal/webui/server/middleware/auth_routes.go:54`), the fleet split-auth
argument (`internal/webui/fleet/module.go:46-51` vs `:54-69`), and the
ETag/`If-Match` argument all re-verified — and the house-precedent critique
("it leaks", "both SDKs rebuild a facade by hand").

**Promoted**

Error-envelope unification moved from a closing afterthought to Tier 3. Schema
correction moved from a bullet inside the dispatcher rationale to Tier 2 and to
the head of the sequencing.

**One fact-check finding rejected.** A reviewer flagged
`internal/webui/handlers/taskrunapi/module.go:148` as the closing brace with
`:147` the raw-body escape hatch. `:145` is the `{op}` route, `:146-147` are the
comment, and **`:148` is the `PUT …/artifacts/{artifactId}/content`
registration**. The draft's citation was right and is retained.
