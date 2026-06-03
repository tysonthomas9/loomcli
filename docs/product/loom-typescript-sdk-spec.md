# Loom TypeScript SDK & Flue-as-Control-Plane-Client — PRD

**Status:** Draft
**Date:** 2026-06-03
**Owner:** Tyson

## Summary

Today loom drives a Flue runner by marshalling a JSON payload into the runner,
then reconstructing results from a stream of `LOOMRUNNER` NDJSON events and a
patch file. The runner is an opaque executor: it cannot read its own task,
update status, post artifacts, or close the task, because the `loom`/`bd` CLIs
and the control-plane API are not reachable from inside the (often remote)
sandbox.

This PRD proposes the inverse: ship an embeddable **`@loom/sdk` (TypeScript)**
generated from loom's existing OpenAPI surface, and let the **Flue runtime call
the loom control plane directly**. loom injects only a tiny, scoped capability
(server URL + per-TaskRun token + task/fencing IDs); the runner pulls task data
and pushes results through typed SDK calls against the entities loom already
models in `internal/domain/control_plane.go` (`AgentSession`, `Artifact`,
`Node`, leases).

The data/control plane moves to the SDK. The code-change bytes (diff/commit)
remain a git/filesystem artifact that the runner *registers* via the SDK.

## Problem

The current "loom marshals a blob → Flue emits NDJSON → loom bridges results"
model has produced concrete, repeatable friction (observed end-to-end this
cycle while wiring Flue + Daytona):

1. **Triple-mirrored contract.** The runner I/O is hand-mirrored in three
   places that drift independently:
   - `runnerInput` (Go struct, `internal/cli/backends/flue_runner.go`)
   - `RunnerInput` (TS interface, `internal/flue/template/.flue/workflows/runner.ts`)
   - `LOOMRUNNER` event shapes (`runnerEvent` Go ↔ `emit()` TS)
   This is the same fragility as `internal/infra/fleetdb/agent.go`'s
   `agentWire` "mirrors fleet-db's models.Agent JSON shape" pattern.

2. **The runner can't see its own task.** loom's task prompt assumes the `loom`
   CLI is present to *discover* the claimed task's design. A remote sandbox has
   no `loom` CLI, so the agent reported *"no implementable task details"* and
   made no changes. We worked around it by fetching the design in Go and
   inlining it into the prompt — a bridge, not a contract.

3. **The runner can't close/advance its task.** The sandbox agent cannot run
   `loom data close` / `loom complete`, so a completed Daytona run never closed
   the task and the daemon re-claimed it in a loop.

4. **Result-bridging is bolt-on and lossy.** Work lands in the sandbox's git
   world and returns only as a patch. Commit/push "back to loom" is a host-side
   bolt-on (`pushWorktreeBack`), and `base_ref` divergence between the local
   worktree and the remote clone caused hard failures.

5. **No server-visible source of truth.** Artifacts (diff, transcript, usage)
   are local-first per-session files. There is no `Artifact` posted against the
   `AgentSession`/TaskRun, which Phase 4 (server scale-out) requires.

Net: the runner is treated as a function loom calls with a blob, instead of an
autonomous executor that talks to the control plane. That mismatch caps us at
single-machine, local-first execution.

## Goals

- A generated, versioned **`@loom/sdk`** TypeScript package, embeddable in Flue
  project templates, that targets the **loom server** API (not fleetdb directly).
- A minimal **`TaskRunClient`** surface a runner needs: read task, update status,
  comment, post artifact, heartbeat the session, complete/fail/block — all
  fencing-gated.
- A **scoped capability bootstrap**: loom injects `{ server_url, token, task_id,
  session_id, fencing_token }` only; the runner self-serves the rest.
- **One typed contract** generated from `api/openapi.yaml`, with the existing
  staleness gate extended to the SDK so Go and TS cannot drift.
- Land cleanly on the **existing `control_plane.go` model** (`AgentSession`,
  `Artifact`, leases) — no new parallel abstraction.
- Backward compatible: the blob/NDJSON runner keeps working until the SDK path
  is proven; both can coexist behind a runner-mode flag.

## Non-Goals

- Exposing fleetdb directly to sandboxes. The SDK targets `loom serve`, which
  remains the trust boundary and mediates fleetdb.
- Moving code-change *bytes* into the API. The diff/commit stays a git artifact;
  the SDK only registers references to it.
- Replacing Flue's harness or the runtime-provider work. This is orthogonal: it
  changes *how the runner talks to loom*, not where it runs.
- A general-purpose public API SDK for third parties. Scope is the runner/agent
  control-plane surface (though the package can grow later).
- Building the Phase-4 scheduler itself. This PRD unblocks it; it does not
  deliver it.

## Users

- **Flue runner templates** (`.flue/workflows/*.ts`, `.flue/agents/*.ts`) — the
  primary consumer; import `@loom/sdk` and drive the task lifecycle.
- **loom maintainers** — author/version the SDK from one spec instead of hand-
  mirroring shapes across Go and TS.
- **Other TS tooling** — the web UI, scripts, MCP servers, future harnesses can
  reuse the same SDK.

## Glossary

- **Control plane** — `loom serve` + fleetdb: owns epics, tasks, agents, roles,
  `AgentSession`s (TaskRuns), `Artifact`s, `Node`s, and leases.
- **TaskRun** — one finite task-execution attempt. Already modeled as
  `domain.AgentSession{Kind=task}` with a `queued→leased→…→completed/failed`
  lifecycle, `Attempt`, `NodeID`, `TaskID`, `ExitCode`.
- **Runner** — the Flue process executing one TaskRun inside a runtime provider
  (local / daytona / e2b / …).
- **Capability** — the scoped, short-lived credential a runner is given for
  exactly one TaskRun.
- **Fencing token** — monotonic token proving the holder still owns the lease;
  the server rejects mutations carrying a stale token.

## What already exists (the ~70%)

This is mostly a packaging + auth job, not a from-scratch build:

- `../fleet-db/api/openapi.yaml` and an API-versioning ADR — fleetdb is already
  API-governed.
- `internal/webui/frontend/src/types/generated/openapi.ts` — loom's web API
  **already generates TS types**, with `scripts/check-openapi-staleness.mjs`
  gating drift.
- `internal/webui/frontend/src/api/workspace/*.ts` + `hooks/api.ts` — a de-facto
  typed TS client over the loom API already exists.
- `internal/domain/control_plane.go` — `AgentSession`, `Artifact`, `Node`,
  `AgentLease`, `AgentOwnershipLease` (fencing) are already modeled, and the
  supervisor already creates `AgentSession{Kind=task}` with status transitions.

What's missing is (a) packaging an embeddable SDK from the spec, (b) the
scoped-token + fencing **auth model** on `loom serve`, and (c) making
`loom serve` the reachable gateway for remote runners.

## Proposed approach

### Inversion

```
Before (blob bridge):
  loom ──(fat JSON payload)──▶ flue run runner ──(LOOMRUNNER NDJSON + patch)──▶ loom

After (control-plane client):
  loom ──(tiny scoped capability)──▶ flue runner
  flue runner ──(typed @loom/sdk calls)──▶ loom serve ──▶ fleetdb
  flue runner ──(branch-push / upload)──▶ git remote / artifact store
                       (refs registered back via @loom/sdk)
```

loom serve is the single reachable endpoint and trust boundary; fleetdb stays
behind it. Local-dev embedded fleetdb (127.0.0.1 + miniredis) is never exposed
to a sandbox.

### `@loom/sdk` surface (minimal `TaskRunClient`)

Generated request/response types come from the OpenAPI spec; this is the
hand-curated client facade a runner uses:

```ts
import { TaskRunClient } from '@loom/sdk';

// Bootstrap is the ONLY thing loom injects (env or argv).
const run = TaskRunClient.fromBootstrap({
  serverUrl:    env.LOOM_SERVER_URL,
  token:        env.LOOM_TASKRUN_TOKEN,   // scoped, short-lived
  taskId:       env.LOOM_TASK_ID,
  sessionId:    env.LOOM_SESSION_ID,       // the AgentSession/TaskRun
  fencingToken: env.LOOM_FENCING_TOKEN,
});

const task = await run.getTask();          // title, description, design, AC, labels, repo
await run.heartbeat();                     // keep the lease alive (also auto-pinged)
await run.appendLog({ stream: 'stdout', text });
await run.recordUsage({ inputTokens, outputTokens, cacheRead, cacheWrite });
await run.postArtifact({ type: 'patch', uri, summary, filesChanged });
await run.comment('progress note for the human/lead');
await run.complete({ exitCode: 0, summary });   // or .fail({ errorClass }) / .block({ reason, dependsOn })
```

Every mutating call carries the fencing token implicitly; the server rejects
stale-token writes (HTTP 409). The client retries idempotently on transient
errors and surfaces `FencedError` when the lease was lost (the runner should
stop, not fight).

### Auth & trust model (the hard part)

1. **Capability minting.** When loom leases a TaskRun to a runner, `loom serve`
   mints a **scoped, short-lived JWT/macaroon** bound to `{ workspace, task_id,
   session_id, fencing_token, scope }`. Scope is least-privilege: read *this*
   task, write *this* session's status/logs/usage/artifacts/comments. It cannot
   read other tasks, claim work, or touch other agents.
2. **TTL + refresh.** Token TTL ≈ lease TTL; the SDK refreshes via heartbeat
   while the lease holds. Lease lost → token not refreshed → calls fail closed.
3. **Fencing enforcement server-side.** Mutations include the fencing token;
   `loom serve` rejects any whose token is older than the current lease holder's
   (prevents a zombie/duplicate runner from corrupting state).
4. **fleetdb stays internal.** Only `loom serve` holds fleetdb credentials.
   Sandboxes never see the `X-Actor` dev-mode header or a broad fleetdb key.
5. **Egress.** Remote sandboxes need outbound HTTPS to the loom server only.
   Document the single endpoint; no inbound exposure of the sandbox required.

### Bootstrap handshake (replaces the fat blob)

loom injects a tiny, typed capability — not the task contents:

| Var | Meaning |
|---|---|
| `LOOM_SERVER_URL` | reachable `loom serve` base URL |
| `LOOM_TASKRUN_TOKEN` | scoped, short-lived capability |
| `LOOM_TASK_ID` | the task to implement |
| `LOOM_SESSION_ID` | the `AgentSession`/TaskRun to report against |
| `LOOM_FENCING_TOKEN` | current lease fencing token |

Everything else (design, description, acceptance criteria, repo info) is pulled
via `run.getTask()`. This deletes the `runnerInput` blob *and* the design-
inlining workaround.

### Result handling: data via SDK, bytes via git

- **Data plane (SDK):** status, logs, usage, comments, artifact *metadata*,
  completion/failure/block.
- **Code bytes:** the runner produces a diff/commit in its runtime. It either
  (a) pushes a branch to the repo remote, or (b) uploads a bundle/patch to an
  artifact store, then calls `postArtifact({ type:'commit'|'patch', uri })`. The
  `AgentSession`/TaskRun now carries server-visible result refs — the Phase-4
  source of truth — instead of relying on host-side patch-back.
- Local-first mode can still patch-back to a worktree as a convenience, but it
  is no longer the contract.

## Conceptual fit

This lands directly on `internal/domain/control_plane.go`:

| SDK concept | Existing domain entity |
|---|---|
| TaskRun the runner reports against | `AgentSession{Kind=task}` (status, attempt, exit code) |
| Lead/orchestration session | `AgentSession{Kind=orchestration}` |
| `postArtifact(...)` | `Artifact{TaskID, SessionID, Type, URI}` |
| heartbeat / fencing | `AgentLease` / `AgentOwnershipLease` |
| where the runner runs | `Node.RuntimeProvider` (extend enum: `daytona`, `podman`) |

No new abstraction is introduced; the SDK is the typed client for entities loom
already has.

## Phasing

- **Phase A — SDK generation.** Generate `@loom/sdk` from `api/openapi.yaml`;
  hand-author the `TaskRunClient` facade; extend `check-openapi-staleness` to
  the SDK; publish internally (npm workspace or vendored into the Flue template).
  *No behavior change.*
- **Phase B — Read path.** Runner calls `getTask()` instead of consuming the
  inlined design; delete the design-inlining workaround. Keep NDJSON results.
- **Phase C — Write path + auth.** Implement scoped-token minting + fencing on
  `loom serve`; runner reports status/logs/usage/artifacts and
  completes/fails/blocks via the SDK. Retire `LOOMRUNNER` for the data plane.
- **Phase D — Artifacts as source of truth.** Branch-push / upload + register
  refs; server-visible artifacts replace host patch-back as the contract
  (patch-back remains an opt-in local convenience). Unblocks Phase-4 scheduling.

Each phase is independently shippable; A+B deliver value with no auth work.

## Risks & open questions

1. **Auth is the critical path.** Scoped per-TaskRun tokens + server-side
   fencing is the hard, security-sensitive work. Decision: JWT vs macaroon;
   where minting lives; revocation on lease loss.
2. **Server reachability for remote runtimes.** Embedded fleetdb is unreachable
   from a sandbox; this forces a deployed/tunneled `loom serve` for remote
   providers. Local + podman can use loopback. Tunnel story for local-dev remote
   sandboxes is an open question.
3. **SDK versioning.** Flue templates pin an SDK version embedded in loom and
   built in sandboxes; API changes must be semver'd and staleness-gated. Risk of
   template/SDK skew (we already hit cross-compiled-fleetdb skew).
4. **State-mutation safety.** Direct writes from runners require the server to
   enforce all invariants (fencing, status transitions) so a buggy/rogue sandbox
   cannot corrupt task state. Trades single-chokepoint simplicity for distributed
   consistency discipline.
5. **Bytes still need a home.** Branch-push needs writable-repo credentials in
   the runtime (or an artifact store). Read-only repos can only upload. This is
   independent of the SDK but must be specified alongside it.
6. **Go↔TS boundary persists.** The SDK makes the boundary typed and minimal,
   but loom is still Go and the runner still TS/Node; this is not in-process.
7. Does `loom serve`'s current OpenAPI cover the full TaskRun write surface
   (artifacts, comment, complete/fail/block), or must endpoints be added first?

## Success metrics / acceptance criteria

- A Flue runner completes a task using **only** the bootstrap capability — no
  task contents passed in, no `loom` CLI in the sandbox, no design-inlining.
- The `runnerInput` blob and the `LOOMRUNNER` data-plane events are removed; one
  generated contract remains, guarded by the staleness check.
- A completed Daytona run **closes its task** via the SDK (no orphan/re-claim
  loop) and posts a server-visible `Artifact` for the result.
- A runner with a **stale fencing token is rejected** (409) and stops; a runner
  with a valid token completes — verified with a duplicate-runner test.
- The SDK targets `loom serve`; fleetdb is never directly reachable from a
  sandbox (verified by network policy / the sandbox holding no fleetdb creds).
- Drift gate: changing `api/openapi.yaml` without regenerating the SDK fails CI.

## Recommended next step

Spec the `@loom/sdk` `TaskRunClient` interface and the `loom serve`
scoped-token + fencing endpoints against the current `api/openapi.yaml` (confirm
the write surface exists), then implement Phase A + B (generation + read path),
which deliver value with zero auth work and immediately delete the design-
inlining workaround.
