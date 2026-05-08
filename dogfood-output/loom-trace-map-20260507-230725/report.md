# Loom OTel Trace Map — Scenario → Trace Correlation

**Date**: 2026-05-07
**Stack under test**: loom-serve (`:18888`) → fleet-db (`:18080`, Redis-backed) → Jaeger (`:16686`)
**Environment**: Local dev, single tenant, 3 workspaces seeded (HAPPY, DEMO, E2E)
**Scope**: 12 user-facing scenarios driven via `agent-browser`, ~4.2s window per scenario for span flush
**Goal**: Map every UI scenario to its Jaeger trace footprint so we can find the chattiest paths and target call-pattern optimizations.

---

## TL;DR

> **Every scenario produces ~530 fleet-db calls/second regardless of what the user does.** That's not user-driven — it's React Query polling fanning out. Top three duplicates per window: `/admin/workspaces/HAPPY` (~325×), `/HAPPY/daemon` (~213×), `/HAPPY/agents` (~213×). The polling cadence dwarfs every actual user action by 30-100×, so optimizing the click handlers themselves is wasted effort until we tame the background poll.

| Concern | Magnitude | Fix priority |
|---|---|---|
| Background polling fan-out | ~530 calls/s, ~325× duplicates per 4s window | **P0** |
| Cross-workspace prefetch in `/admin/workspaces/:key` | Polled even when not viewing that workspace | **P0** |
| `git.branch` in hot path | 714 spans / 4s on the kanban | **P1** |
| Redis `HGETALL` fan-out | 3230 calls / largest trace | **P1** |
| Backend invocation spans (`service.IssueBackend.List`) | 267 / window — backend is being asked the same question | **P2** |

---

## Scenarios

Wall-clock windows captured in `scenarios.tsv`. All Jaeger queries used `service=loom-serve` and the `[start_us, end_us]` window.

| ID | Scenario | Window | Traces | Spans | fleet-db calls | calls/s | Top duplicate |
|----|----------|--------|--------|-------|----------------|---------|----------------|
| A | Initial SPA load | 4.21s | 60 | 15 722 | 2 263 | 538 | 326× `/admin/workspaces/HAPPY` |
| L | Reload kanban | 4.20s | 31 | 15 635 | 2 263 | 538 | 330× `/admin/workspaces/HAPPY` |
| B | Switch to List view | 4.26s | 7 | 15 365 | 2 218 | 528 | 324× `/admin/workspaces/HAPPY` |
| C | Open Monitor | 4.21s | 10 | 15 375 | 2 219 | 528 | 324× `/admin/workspaces/HAPPY` |
| D | Open Settings | 4.20s | 12 | 15 397 | 2 222 | 529 | 325× `/admin/workspaces/HAPPY` |
| E | Open issue detail | 4.21s | 8 | 15 392 | 2 224 | 529 | 324× `/admin/workspaces/HAPPY` |
| F | Search / filter | 4.19s | 5 | 15 371 | 2 221 | 528 | 324× `/admin/workspaces/HAPPY` |
| G | Workspace switch HAPPY → DEMO | 4.21s | 63 | 15 754 | **2 272** | 540 | 320× `/admin/workspaces/HAPPY` |
| H | Create issue (submit) | 4.20s | 11 | 15 431 | 2 230 | 530 | 324× `/admin/workspaces/HAPPY` |
| I | Add repo | 4.20s | 6 | 15 387 | 2 223 | 529 | 325× `/admin/workspaces/HAPPY` |
| J | Create agent (submit) | 4.21s | 18 | 15 604 | 2 252 | 536 | 327× `/admin/workspaces/HAPPY` |
| K | Create workspace TRACEMAP | 4.18s | 16 | 15 444 | 2 231 | 531 | 325× `/admin/workspaces/HAPPY` |

**Key observation**: the scenario column is *barely correlated* with fleet-call volume. Switching to the List view, opening Settings, or just sitting on the kanban all produce ~2 220 fleet-db calls in 4 seconds. The user action is a rounding error against the polling baseline.

---

## What's actually being called

Aggregating across all 12 scenarios:

```
~325× /api/v1/admin/workspaces/HAPPY        (workspace metadata refetch — the worst offender)
~215× /api/v1/HAPPY/daemon                  (daemon status poll)
~213× /api/v1/HAPPY/agents                  (agent list poll)
~210× /api/v1/HAPPY/roles                   (roles list poll, never changes)
~200× /api/v1/HAPPY/issues/ready
~200× /api/v1/HAPPY/issues/blocked
~200× /api/v1/HAPPY/issues/count?group_by=status
~200× /api/v1/HAPPY/issues?limit=10000&status=in_progress
~200× /api/v1/HAPPY/issues?limit=10000&status=review
~200× /api/v1/HAPPY/issues?limit=50&status=closed
```

Then a smaller cross-workspace bleed:
```
~9× /api/v1/admin/workspaces/DEMO
~9× /api/v1/admin/workspaces/E2E
~9× /api/v1/DEMO/daemon, /E2E/daemon
~3× per-status issue queries on DEMO/E2E
```

That last block is the *workspace switcher dropdown*: the SPA prefetches every workspace's daemon/issue summary on every render of the dropdown, even when it's not open.

---

## Span hierarchy (sample: largest trace from Scenario A)

Trace `3c1b598f0f1e51005a82ed926e3a331a` — **18 906 spans in 4.2 seconds**:

| Span | Count | What it is |
|------|------:|------------|
| `redis.HGETALL` | 3 230 | issue/workspace hash reads — every read fans out per-key |
| `redis.EVALSHA` | 2 732 | scripted multi-step reads (issue list per status) |
| `HTTP GET` (outbound, loom-serve → fleet-db) | 2 732 | 1:1 with EVALSHA — every issue list query is one HTTP hop |
| `redis.PIPELINE` | 1 949 | bundled reads from fleet-db side |
| `redis.SMEMBERS` | 738 | set reads (workspace memberships, role assignments) |
| `service.IssueService.List` | 726 | server handler invocations — `~172/s` |
| `git.branch` | 714 | **per-render `git branch` shell-out** in repo display |
| `service.Store.Workspaces.Get` | 534 | workspace metadata DAL — should be cached |
| `service.IssueBackend.List` | 267 | backend (Redis) invocation — ~60/s |
| `service.RoleService.List` | 244 | static-ish data |

Roots: 0 — this trace inherits from a parent that started before the window (`web.spa.session` or similar).

Raw JSON saved to `traces/scenario-A-60948dde.json`, `traces/scenario-G-54d3a95c.json`, `traces/scenario-L-a5fa41c9.json`.

---

## Top optimization candidates

Ranked by total waste (duplicate calls × cost-per-call estimate × frequency):

### 1. React Query polling cadence is too aggressive — fix first
**Symptom**: every kanban-relevant query refetches ~75×/s (300+ in 4s). Independent of user input.

**Hypothesis**: `staleTime: 0` or `refetchInterval: 50ms` on the issue list queries; the workspace dropdown component triggers `useQuery` for *every* workspace on mount even when collapsed.

**Where to look**:
- `internal/webui/frontend/src/api/issues/issues.ts` — refetch policy on `useIssuesList`, `useIssuesReady`, `useIssuesBlocked`
- `internal/webui/frontend/src/api/workspace/workspace.ts` — `useWorkspaceQuery` and the dropdown's prefetch loop
- `internal/webui/frontend/src/api/workspace/workspaceConfig.ts` — config fetch policy

**Likely fix**: set `staleTime` to 5–30s on data that doesn't change between user actions; use the realtime SSE channel (already wired via `internal/webui/server/realtime/handler.go`) to invalidate queries on actual mutations rather than polling. **Estimated reduction: 90 %+** of fleet-db calls during idle.

### 2. `/admin/workspaces/:key` is over-fetched (325× / 4s)
**Symptom**: This single endpoint is the top duplicate in every scenario. It returns workspace metadata that almost never changes during a session.

**Where to look**:
- `internal/webui/frontend/src/api/workspace/workspaceConfig.ts` — see whether it lacks `staleTime`
- The header / sidebar component re-mounting on every route change and re-firing this query

**Likely fix**: `staleTime: Infinity` plus SSE invalidation on workspace config change. **Estimated reduction**: ~325 calls per 4s window.

### 3. Cross-workspace prefetch in dropdown (9× per non-active workspace per 4s)
**Symptom**: opening the workspace dropdown for HAPPY still fires queries against DEMO and E2E.

**Where to look**:
- The workspace switcher component; likely a `useQueries(allWorkspaces.map(w => issuesQuery(w)))` pattern.

**Likely fix**: only fetch the *active* workspace's data; lazily fetch peers when the dropdown is actually opened.

### 4. `git.branch` in render hot path (714 spans / 4s in a single trace)
**Symptom**: every issue/workspace card render shells out to `git branch`. The decorator (`internal/cli/git_runner_tracing.go`) is doing its job — the spans are real shell calls.

**Where to look**:
- `internal/cli/repo/repo_cmd.go` and any `git.branch` callers in the issue card / repo list code path
- Likely a `getCurrentBranch()` called inside a render loop without memoization

**Likely fix**: cache branch per-repo with a short TTL (1s) or only re-read on filesystem change. **Estimated reduction**: 95 % of git.branch calls.

### 5. `redis.HGETALL` 3 230× per trace — N+1 over issues
**Symptom**: the issue list query loads issues one HGETALL at a time. With 100 issues × 30 polls = 3 000 calls.

**Where to look**:
- `fleet-db/internal/storage/postgres/issues.go` (or `internal/storage/redis/issues.go` — verify which backend is wired)
- The issue list path: probably loops `for _, id := range ids { hgetall(id) }`; should `MGET` or use a Lua script that returns the whole list.

**Likely fix**: batch HGETALL via `MGET` or a single `EVALSHA` returning the whole result set. **Estimated reduction**: ~10× per list query.

### 6. `service.IssueBackend.List` invoked 267× / 4s
**Symptom**: even after collapsing duplicate UI calls, the server still asks the backend for the same answer multiple times per second.

**Where to look**:
- `internal/cli/issue_backend_tracing.go` (the decorator already caches `backendAttr`; the cache miss isn't the issue)
- `internal/cli/serve/...` issue handler — likely no per-request memoization, so `/issues/ready`, `/issues?status=review` etc. each call the backend independently even when the answer is identical for the same workspace within one render.

**Likely fix**: server-side request-level cache keyed by `(workspace, query_hash)` with ~500ms TTL. **Estimated reduction**: 50–80 %.

---

## Methodology notes

- Wall-clock timestamps recorded via `python3 -c 'import time; print(int(time.time()*1e6))'` to avoid the macOS `date +%6N` issue.
- 4-second post-action sleep per scenario to let async spans flush from the batched serve provider.
- Jaeger queried with `service=loom-serve` because every fleet-db client call is a child span of a loom-serve request — querying `service=fleet-db` would miss the inbound HTTP server spans on the loom side.
- Scenario `G` was driven via direct URL navigation (`/ws/DEMO/kanban`) because the workspace dropdown DEMO option wasn't visible in the snapshot at action time. It still exercises the workspace-switch backend path.

---

## Files

```
dogfood-output/loom-trace-map-20260507-230725/
├── report.md                  (this file)
├── scenarios.tsv              (raw timestamp windows)
├── trace-map.json             (per-scenario endpoint counts)
└── traces/
    ├── scenario-A-60948dde.json   (Initial SPA load — biggest)
    ├── scenario-G-54d3a95c.json   (Workspace switch — most duplicates)
    └── scenario-L-a5fa41c9.json   (Reload kanban — control)
```
