# Loom TypeScript SDK & Flue-as-Control-Plane-Client — PRD

**Status:** In progress — Phases A + B done; Phase C implemented and the SDK read+write data plane **validated live E2E** through a real Daytona sandbox (auth-hardening: token minting + fencing mount pending; Phase D pending)
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
| **1. Hosted `loom serve`** (cloud VM / managed) — *primary* | `loom serve` (+ fleetdb/redis) runs on an always-on, reachable host; the sandbox calls it over public HTTPS, egress-allowlisted to the endpoint | Team / Phase-4 scale-out | Durable, always-on, server-visible artifacts as source of truth. Real ops: provisioning, TLS, scaling, secrets. Internet-exposed → scoped-token auth is non-negotiable |

> **Provider networking reality + deferred private reachability (researched
> 2026-06).** The managed sandbox providers offer **no native private-network /
> VPC-join** — only *egress firewalling*: **Daytona** = `networkAllowList` (IPv4
> **CIDR only**; no hostnames/domains/IPv6) + `networkBlockAll`; **e2b** =
> `allowInternetAccess` + `network.allowOut/denyOut` (IP/CIDR/**domain**). On
> Daytona/e2b the runner therefore reaches `loom serve` over the **public
> internet**, and the hosted endpoint must sit at a **stable IP/CIDR** for
> Daytona's CIDR-only egress allowlist (e2b can allowlist by domain).
>
> **Private networking is dropped for now.** Revisit it when runtimes run on
> **our own infra** instead of a managed sandbox provider — a **private
> Kubernetes cluster** or a **cloud VPC (AWS / GCP / Azure)**. There the runner
> pod/VM already lives inside the network and reaches a private `loom serve`
> directly (VPC-native), with no public exposure and no app-level overlay
> required. (An in-sandbox `tsnet` mesh could approximate this on managed
> providers, but it is not pursued now.)

When the runner calls loom (Option 1), it reaches **only `loom serve`**; fleetdb
stays behind it. A small hosted deployment can keep `loom serve`'s embedded
fleetdb + miniredis; only at scale do you split out a standalone fleetdb + real
Redis.

### Does the implementation differ by topology?

**No — the runner, `@loom/sdk`, and control-plane API code are identical. That is
a design goal: deployment topology is a config axis, not a code fork.** The runner
only knows `LOOM_SERVER_URL` + a scoped token; whether that resolves to a public
host today, or a private in-VPC host when private networking is revisited, is
irrelevant to it. If per-deployment runner code starts appearing, that's a design
smell.

What varies is config / infra — not logic: the `LOOM_SERVER_URL` value (public vs
private endpoint), TLS/cert trust (public CA vs an internal CA via
`NODE_EXTRA_CA_CERTS`), and whether `loom serve` uses embedded fleetdb (small) or
external fleetdb + real Redis at scale via `LOOM_FLEET_DB_URL` (the existing
ModeCloud path — config, not new code). The deferred private-endpoint path adds
**no runner code** — it just points `LOOM_SERVER_URL` at an in-network address.

## Phasing

- **Phase A — SDK generation. ✅ DONE** (`sdk/typescript`, commits `06926d15` +
  `688cfeda`). `@loom/sdk` generated from `api/openapi.yaml` via
  `openapi-typescript` + an `openapi-fetch` client; the `TaskRunClient` facade
  is hand-authored; a `check:generated` drift gate is wired into `make` as
  `check-sdk` (third parallel lane in `make check`); unit tests cover bootstrap
  + client. The read/control methods (`getTask`, `comment`, `updateStatus`,
  `complete`, `block`, `fail`) are wired against existing loom-serve endpoints;
  `postArtifact`/`recordUsage`/`appendLog`/`heartbeat` are stubbed
  (`NotImplementedError`) pending Phase C. *No behavior change to loom yet.*
- **Phase B — Read path. ✅ DONE.** `@loom/sdk` is vendored into the Flue
  template as a self-contained ESM bundle
  (`internal/flue/template/.flue/vendor/loom-sdk/`, openapi-fetch inlined so it
  builds offline in a sandbox), kept in sync with the SDK source by a
  `check-vendored-sdk` gate in `make check-sdk`. When loom has a reachable serve
  + bootstrap (`LOOM_SERVER_URL` + workspace + task id, all carried via the
  existing `LOOM_` env allowlist), `deriveDaytonaInput` sets `fetch_task` and
  sends **only the sandbox preamble** (no task contents); the runner — which
  runs on the host, so it reaches `loom serve` with nothing extra to host —
  calls `getTask()` and composes the prompt from server-fetched task content.
  It **fails fast** if the bootstrap is set but the server is unreachable (no
  silent empty run). NDJSON results are kept. **Backward compatibility:** when no
  bootstrap is present (the host-orchestrated dev default, Option 0), the Go-side
  design-inlining (`buildSandboxPrompt`) is **retained as the fallback** rather
  than deleted — deleting it unconditionally would break the zero-hosting path
  the Goals require. Unconditional removal of the inlining lands once a served
  endpoint is the default (Phase C). *Token/fencing auth is still Phase C; the
  read path uses dev-mode `X-Actor`.*
- **Phase C — Write path + auth.** *In progress.* The scoped per-TaskRun
  **capability token + fencing primitives have landed**
  (`internal/webui/fleet/taskrun_token.go`): `TaskRunClaims` bound to
  `{workspace, task_id, session_id, fencing_token, scopes}`,
  `GenerateTaskRunToken`/`ValidateTaskRunToken` reusing the existing fleet
  HMAC-JWT pattern (so no new key management — resolves the "JWT vs macaroon"
  open question by precedent), least-privilege default scopes, binding checks
  (`AuthorizesSession`/`AuthorizesTask` → 403 on mismatch) and `FencedOut`
  (stale fencing → 409), fully unit-tested. The **auth/fencing middleware**
  (`taskrun_auth.go`, 401/403/409, reads skip fencing) is also landed + tested.
  The **session write endpoints are implemented**: `api/openapi.yaml` gained POST
  `/sessions/{sessionId}/{artifacts,usage,logs,heartbeat}` (all three type sets
  regenerated, gates green); `internal/webui/handlers/sessionwrite/` persists via
  the existing `ArtifactStore`/`AgentSessionStore` and is mounted on the
  workspace router (store-backed; unit- + route-mount-tested against the
  in-memory store); the **SDK write methods are un-stubbed** to typed calls. Auth
  is currently dev-mode `X-Actor` (per the chosen "finish the old PRD" path).
  The runner also **reports usage + the patch artifact via the SDK**
  (best-effort, keeps `LOOMRUNNER`). The **token-minting primitive is wired to
  the shared signing key** (`SigningKeyManager.MintTaskRunToken` /
  `ValidateTaskRunTokenFromStore`): the fleet signing key lives in Redis (SET-NX
  — first process creates, others reuse), so a token minted by one process
  validates in another (proven by a cross-process miniredis test). This
  dissolves the earlier "key distribution" blocker.
  **Remaining (invocation wiring + stack validation):** decide WHERE mint is
  called from — the daemon/supervisor (needs Redis access) vs a `loom serve`
  mint endpoint — then inject `LOOM_TASKRUN_TOKEN` + `LOOM_FENCING_TOKEN` at
  lease time and mount the fencing middleware on the write routes (deferred until
  minting+injection lands, so the verified dev-mode write path isn't broken);
  retire `LOOMRUNNER` for the data plane. The mint-location choice is a
  daemon/serve topology decision that overlaps the v2 proposal's "facade vs
  direct FleetDB" question. The end-to-end Phase-C validations (duplicate-runner
  fencing → 409, lease-loss fail-closed, no-orphan-reclaim) need the distributed
  stack and are a gated runbook per the Definition of Done.
- **Phase D — Artifacts as source of truth.** *Pending.* Branch-push / upload +
  register refs; server-visible artifacts replace host patch-back as the
  contract (patch-back remains an opt-in local convenience). Unblocks Phase-4
  scheduling.

Each phase is independently shippable; A+B deliver value with no auth work.
**Phases A + B are implemented; C–D remain and span loom serve (Go) + fleetdb +
the Flue template, with C introducing the security-critical auth work.**

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

### Phase B — Read path ✅
**Exit criteria**
- A runner fetches title/description/design/AC via `getTask()` using only the
  bootstrap capability. ✅
- The Go-side design-inlining is **gated off whenever the bootstrap is present**
  and retained only as the no-server fallback. *(Reconciled from the original
  "removed" wording: the Goals mandate the blob/inlining path keep working until
  the SDK path is proven and a server is always reachable. Unconditional removal
  is deferred to Phase C, when a served endpoint becomes the default.)* ✅

**Validation steps**
1. *(Gated manual runbook — CI cannot reach Daytona.)* Run a daytona-task with
   `LOOM_SERVER_URL`/`LOOM_WORKSPACE`/`LOOM_ASSIGNED_TASK_ID` set and `loom serve`
   reachable → loom sends `fetch_task` + preamble only (no task contents), the
   runner fetches the design via the SDK, and the agent makes the change
   (`files_changed ≥ 1`). Mechanically verified by
   `TestDeriveDaytonaInput_ReadPath`: with the bootstrap present the payload
   carries the preamble only (no inlined task body) and `fetch_task=true`. ✅
2. The runner imports the vendored `@loom/sdk`, and the **real `flue build`
   bundles it** — verified that `dist/server.mjs` contains `TaskRunClient` + the
   read-path code; `make check-sdk`'s `check-vendored` gate keeps the bundle in
   sync with the SDK source. ✅
3. A run with `fetch_task` set but the server unreachable **fails fast** with a
   clear error (`runner: could not fetch task … via @loom/sdk: …`) — no silent
   empty run. ✅

### Phase C — Write path + auth
**Exit criteria**
- Runner reports status/logs/usage/artifacts and completes/fails/blocks via the
  SDK; `LOOMRUNNER` data-plane events are removed.
- `loom serve` mints scoped per-TaskRun tokens and enforces fencing.

**Validation steps**

*Live E2E run 2026-06-03 (dev container: real `loom serve` + fleetdb + a real
Daytona sandbox; workspace `HELLO`, task `HELLO-1`):*
- ✅ **Read + write data plane via the SDK, end to end.** The daemon spawned the
  flue agent; the runner fetched `HELLO-1`'s design via `getTask()` from the live
  serve (`task_fetched` event), the agent created `SDK_E2E.md` in the Daytona
  sandbox, and the runner's `postArtifact()` **auto-registered the result
  artifact on the supervisor-created `AgentSession`** (`art_4fa4f6b…` →
  `20260603-210125-sdkbot…`, type `patch`) — the artifact arrived **via the SDK,
  not `LOOMRUNNER`**. `recordUsage`/`heartbeat`/`postArtifact` also verified
  directly against real fleetdb (201/200 + persistence). Sandbox created →
  deleted; no regression in the Daytona lifecycle.
- *Fixed (was a follow-up):* `recordUsage` originally wrote usage into the
  session metadata map, which loom's finalizer overwrote wholesale. Usage is now
  stored as a typed `usage` `Artifact` — durable and immune to the finalizer's
  metadata rewrite.

*Still pending (need token minting wired and/or a writable repo — gated runbook):*
1. A completed Daytona run closes its task via the SDK → the daemon does **not**
   re-claim it (no orphan loop), verified over ≥ 2 supervise cycles. *(The runner
   does not yet call `complete()`; loom's finalizer closes the task in the
   host-orchestrated model.)*
2. Duplicate-runner test: two runners on one TaskRun → the stale-fencing-token
   writer is rejected (HTTP 409) and stops; the current holder completes. *(Needs
   supervisor token minting + the fencing middleware mounted.)*
3. Scope test: the TaskRun token cannot read a different task (403) or claim new
   work (403). *(Primitive proven by unit tests; needs minting wired to exercise live.)*
4. The sandbox holds no fleetdb credentials and can reach only `loom serve`
   (network policy; no `X-Actor` fleetdb key present). *(In the host-orchestrated
   run the runner is on the host; this becomes testable when the runner moves
   into the sandbox.)*
5. Lease-loss test: after the lease expires, SDK calls fail closed (no refresh).
   *(Needs minting + lease-TTL wiring.)*

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
   from a sandbox; a runner that calls loom needs a served `loom serve` endpoint
   (host-orchestrated runs need nothing — see **Deployment & reachability**).
   Research finding: **no managed sandbox provider offers native private
   networking** (Daytona/e2b only do egress firewalling), so v1 uses a **hosted
   `loom serve`** over public HTTPS, and on Daytona the **CIDR-only egress**
   constraint forces the endpoint to a stable IP/CIDR. **Private networking is
   deferred** — revisit when runtimes run on our own private Kubernetes cluster
   or a cloud VPC (AWS/GCP/Azure), where the runner is already in-network.
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

Phases A + B are implemented. Next is **Phase C (write path + auth)** — the
security-critical work: mint scoped, short-lived per-TaskRun tokens on
`loom serve` bound to `{workspace, task_id, session_id, fencing_token, scope}`,
enforce fencing server-side (stale-token writes → HTTP 409), add the write
endpoints (artifact/usage/log/heartbeat), and un-stub the corresponding SDK
methods. Confirm the current `api/openapi.yaml` covers the write surface (open
question #7) before wiring; add endpoints first if not. Completing C unlocks
retiring the `LOOMRUNNER` data plane and, once a served endpoint is the default,
the unconditional removal of the Go-side design-inlining deferred from Phase B.
