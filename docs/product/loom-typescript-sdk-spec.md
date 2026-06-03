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

## Deployment & reachability

**This requirement only exists once the runner calls loom directly (the SDK /
control-plane-client model).** In the **host-orchestrated model loom runs today**
(Phase 2), the flue runner process stays on the host and only the agent's *tools*
execute in the sandbox via the connector — loom is never called from the sandbox,
so **nothing needs to be hosted** (the sandbox needs only outbound to its git
remote). Adopt one of the served topologies below only when you move the runner
itself into the sandbox.

Once the runner does call loom, it reaches **`loom serve`** (never fleetdb
directly). The embedded dev fleetdb (miniredis on `127.0.0.1`) is fundamentally
**not network-reachable**, so any remote runner needs a *served, addressable,
authenticated* loom endpoint:

| Option | What it is | Best for | Trade-offs |
|---|---|---|---|
| **0. Host-orchestrated (today)** | Flue runner runs on the host; the sandbox is just a remote shell/FS reached via the Daytona API; loom is never called from the sandbox | Dev / single-user; Phase 1–2 | **Nothing to host.** But the runner can't self-serve task data/artifacts — you keep the blob bridge and its limits (no in-sandbox task read/close, patch-back fragility) |
| **1. Tunnel local `loom serve`** | Cloudflare Tunnel / ngrok / Tailscale Funnel exposes the laptop's `loom serve` at a public HTTPS URL | Dev / single-user trying the SDK model | No VM; loom stays local. Laptop must stay online; tunnel adds its own auth + latency; if `loom serve` dies mid-run the control plane drops |
| **2. Daytona ↔ private network** | The sandbox joins your network (Tailscale daemon in the sandbox image, or Daytona VPC peering) and reaches a private `loom serve` | Teams wanting no public exposure | Strongest isolation. Requires network-join provisioning in the sandbox image/runtime; depends on Daytona's networking support |
| **3. Hosted `loom serve` (cloud VM / managed)** | `loom serve` (+ fleetdb/redis) runs on an always-on reachable host | Team / Phase-4 scale-out | Durable, always-on, server-visible artifacts as source of truth. Real ops: provisioning, TLS, scaling, secrets |

In all three the runner reaches **only `loom serve`**; fleetdb stays behind it.
A small hosted deployment can keep `loom serve`'s embedded fleetdb + miniredis;
only at scale do you split out a standalone fleetdb + real Redis.

### Does the implementation differ across the three?

**No — the runner, `@loom/sdk`, and control-plane API code are identical across
all three. That is a design goal: deployment topology is a config axis, not a
code fork.** The runner only knows `LOOM_SERVER_URL` + a scoped token; whether
that URL resolves to a tunnel, a tailnet host, or a public VM is irrelevant to
it. If per-deployment runner code starts appearing, that's a design smell.

What varies is config / infra / provisioning — not logic:

| Concern | 1. Tunnel | 2. Private net | 3. Hosted VM |
|---|---|---|---|
| Runner / SDK / control-plane API code | same | same | same |
| `LOOM_SERVER_URL` value | tunnel URL | private host | public/VPC host |
| TLS / cert trust | tunnel-provided HTTPS | may need a custom CA in the sandbox (`NODE_EXTRA_CA_CERTS`) | standard public TLS |
| Sandbox network setup | none (public egress) | **network-join in the image** (Tailscale, etc.) | none (public egress) |
| fleetdb / redis | embedded in `loom serve` | embedded or external | embedded (small) → external + real Redis (scale) |
| Auth posture | scoped token (mandatory) | scoped token + reduced exposure | scoped token (mandatory; internet-exposed) |
| Availability assumption | laptop-bound; exercises lease-loss/reconnect | host-bound | always-on |

The only genuine deltas beyond config are: **(a)** Option 2 needs network-join
provisioning in the *sandbox image* (the runtime-provider layer, not the SDK);
and **(b)** at scale you flip `loom serve` from embedded fleetdb to external via
`LOOM_FLEET_DB_URL` (the existing ModeCloud path — config, not new code).
Notably, the laptop-bound tunnel option simply *exercises* the
reconnect / lease-loss / fencing paths more often — the same code that must
exist anyway — so building those robustly is the right move under any topology.

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

## Validation & exit criteria

A phase is "done" only when every validation step below passes. The PRD is done
when all four phases pass and the Definition of Done holds.

### Phase A — SDK generation
**Exit criteria**
- `@loom/sdk` builds and type-checks; request/response types are generated from
  `api/openapi.yaml` (no hand-written shapes).
- The drift gate fails CI when the spec changes without regenerating the SDK.

**Validation steps**
1. On a clean checkout, run the generator → `git diff` is empty (SDK in sync).
2. Edit a field in `api/openapi.yaml` without regenerating →
   `check-openapi-staleness` (extended to the SDK) fails CI.
3. The Flue template build imports `@loom/sdk` and compiles.

### Phase B — Read path
**Exit criteria**
- A runner fetches title/description/design/AC via `getTask()` using only the
  bootstrap capability.
- The Go-side design-inlining workaround is removed.

**Validation steps**
1. Run a daytona-task whose injected payload carries **no task contents** (only
   the bootstrap) → the agent receives the design via the SDK and makes the
   change (`files_changed ≥ 1`).
2. `grep` confirms `buildSandboxPrompt` design-inlining and the task-content
   fields of `runnerInput` are deleted.
3. A run with the server unreachable fails fast with a clear error (no silent
   empty run).

### Phase C — Write path + auth
**Exit criteria**
- Runner reports status/logs/usage/artifacts and completes/fails/blocks via the
  SDK; `LOOMRUNNER` data-plane events are removed.
- `loom serve` mints scoped per-TaskRun tokens and enforces fencing.

**Validation steps**
1. A completed Daytona run closes its task via the SDK → the daemon does **not**
   re-claim it (no orphan loop), verified over ≥ 2 supervise cycles.
2. Duplicate-runner test: two runners on one TaskRun → the stale-fencing-token
   writer is rejected (HTTP 409) and stops; the current holder completes.
3. Scope test: the TaskRun token cannot read a different task (403) or claim new
   work (403).
4. The sandbox holds no fleetdb credentials and can reach only `loom serve`
   (network policy; no `X-Actor` fleetdb key present).
5. Lease-loss test: after the lease expires, SDK calls fail closed (no refresh).

### Phase D — Artifacts as source of truth
**Exit criteria**
- The runner branch-pushes or uploads the result and registers an `Artifact`
  ref on the TaskRun; host patch-back is opt-in only.

**Validation steps**
1. After a run, the `AgentSession`/TaskRun carries an `Artifact` with a
   resolvable URI (commit SHA / branch ref / patch object).
2. A fresh client with **no local worktree** retrieves the result from the
   server alone (server-visible source of truth).
3. Phase-4 smoke: a server-scheduled TaskRun (no developer worktree) produces
   server-visible artifacts end to end.

## Definition of Done

The PRD is complete — not merely "the SDK was written" — when all hold:

- A Flue runner in a remote Daytona sandbox completes a real task end to end
  using **only** the bootstrap capability: no task contents in the payload, no
  `loom`/`bd` CLI in the sandbox, no design-inlining, and host patch-back is not
  the contract.
- The task is **auto-closed** by the run; no orphan/re-claim loop.
- The result is a **server-visible `Artifact`** on the TaskRun, retrievable
  without a local worktree.
- **Fencing is enforced:** a stale-token writer is rejected; the lease holder
  wins (duplicate-runner test green).
- **One generated contract** remains — no `runnerInput`/`RunnerInput`/
  `LOOMRUNNER` triple-mirror — and the drift gate is green in CI.
- fleetdb is **never directly reachable** from a sandbox.
- The gated integration test above runs green in CI, or a documented manual
  runbook exists where CI cannot reach Daytona.

## Risks & open questions

1. **Auth is the critical path.** Scoped per-TaskRun tokens + server-side
   fencing is the hard, security-sensitive work. Decision: JWT vs macaroon;
   where minting lives; revocation on lease loss.
2. **Server reachability for remote runtimes.** Embedded fleetdb is unreachable
   from a sandbox; remote runtimes need a served `loom serve` endpoint (local +
   podman can use loopback). See **Deployment & reachability** for the three
   topologies and why the implementation is identical across them. Remaining
   open question: the default dev path (tunnel vs lightweight hosted), and
   whether to bundle a Tailscale-based network-join in the sandbox image for
   Option 2.
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

## Recommended next step

Spec the `@loom/sdk` `TaskRunClient` interface and the `loom serve`
scoped-token + fencing endpoints against the current `api/openapi.yaml` (confirm
the write surface exists), then implement Phase A + B (generation + read path),
which deliver value with zero auth work and immediately delete the design-
inlining workaround.
