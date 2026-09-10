# Ticket Recommender — Codebase Research

**Status:** Research (raw findings, no design)
**Date:** 2026-08-13
**Scope:** loomcli worktree `feat/ticket-recommender-v1`; companion repo cited as `fleet-db:<path>`.

## Research Question

We plan a proactive background "ticket recommender": when a user creates a
workspace it analyzes the attached repo, recommends a few tickets (fleet-db
issues), writes an `agents.md` (agent onboarding notes) outside the repo
checkout and a `history.md` (run history); afterwards it re-runs daily/weekly
to propose new tickets. What does the codebase already provide that this
should build on? Every claim below is verified against primary sources — the
repo's own docs and source — and each design-doc claim is labeled
**implemented**, **partial**, or **proposal-only**. Code citations are
`path:line`; doc citations are `path#heading`.

---

## 1. Trigger / schedule mechanism

Doc: `docs/design/2026-06-07-trigger-workflow-proposal.md`. Verdict: the
trigger platform the proposal describes is now **implemented and live**, and
several items the doc lists as "missing" or "deferred" have since shipped.
The doc's Current State section is stale in our favor.

### What exists in code

- **Domain model** — `domain.TriggerBinding` with `SourceKind`, `RouteKey`,
  `EventTypePatterns`, pinned `DriverID`/`DriverVersionID`,
  `ConcurrencyPolicy`, `WebhookSecret`, `SubjectKeyTemplate`, `ActorFilter`,
  retry knobs, and cron `Schedule`/`ScheduleTimezone`
  (`internal/domain/platform.go:214-254`). Concurrency policies include
  `allow|forbid|replace|queue|one_active_per_epic`
  (`internal/domain/platform.go:207-211`). Retry defaults: 5 attempts, 30s
  backoff (`internal/domain/platform.go:173-176`).
- **Cron scheduler (the "schedule runner" the doc deferred)** — fully
  implemented. `trigger.CronScheduler` sweeps enabled `source_kind=cron`
  bindings and fires synthetic `cron.tick` events into the normal
  trigger-route dispatch path (`internal/trigger/cron.go:74-102`,
  `RunOnce` at `internal/trigger/cron.go:117`). Missed-tick policy: at most
  one catch-up tick per binding per sweep, never backfilled
  (`internal/trigger/cron.go:80-86`). Tick idempotency key
  `cron:{bindingID}:{fireUnix}` makes overlapping schedulers safe
  (`internal/trigger/cron.go:256-258`). Grammar is standard 5-field cron
  plus `@descriptors` via `robfig/cron` `ParseStandard`, mirroring fleet-db's
  write-time validation (`internal/trigger/cron.go:55-61`;
  fleet-db:`internal/models/platform.go:472-475`, `:433-436`).
- **Always-on serve loops** — `loom serve` unconditionally starts the cron
  scheduler (`internal/cli/serve/serve.go:231`,
  `internal/cli/serve/serve_loops.go:90-123`; sweep interval
  `LOOM_TRIGGER_CRON_INTERVAL`, default 30s), a delivery retry sweeper
  (`internal/cli/serve/serve_loops.go:125-167`), an await-timeout sweeper
  (`internal/cli/serve/serve_loops.go:169-210`), plus the stale-task sweeper
  and outbox dispatcher. None are gated behind `LOOM_DRIVER_EXECUTOR`.
- **Issue-journal bridge (internal events)** — `loom serve` also polls
  fleet-db's issue mutation journal and re-emits each entry into the trigger
  router as a system-origin internal event, e.g. `internal.issue.created`
  (`internal/cli/serve/serve_loops.go:212-277`,
  `internal/trigger/issue_journal_bridge.go:1-45`). Deterministic loopback
  event ids make replay dedup-absorbed; first observation fast-forwards past
  the historical backlog to avoid a triage storm
  (`internal/trigger/issue_journal_bridge.go:21-27`). The header documents a
  mandatory self-trigger guard: any binding on `internal.issue.created`
  whose driver itself creates issues must carry an actor filter
  (`exclude_actor_kinds`) or it will re-trigger off its own output forever —
  the hop-depth cap does not stop depth-0 loops
  (`internal/trigger/issue_journal_bridge.go:29-45`;
  `domain.TriggerActorFilter` at `internal/domain/platform.go:178-198`).
  This is directly load-bearing for a recommender that creates issues.
- **Webhook ingestion** — generic route
  `POST /api/workspaces/{ws}/webhooks/{name}` with a GitHub adapter
  (signature verification via `X-Hub-Signature-256`, dedup via
  `X-GitHub-Delivery`) (`internal/webui/handlers/webhooks/adapter.go:1-79`,
  `internal/webui/handlers/webhooks/github.go:16-25`,
  `internal/webui/handlers/webhooks/module.go:45-49`).
- **Route patterns** — dot-segmented glob patterns (`*`, `{a,b}`) matched
  segment-by-segment, kept in lockstep with
  fleet-db `internal/routing/pattern.go` (`internal/trigger/pattern.go:1-27`).
- **Dispatch** — everything funnels into
  `store.TriggerRoutes().DispatchTriggerRouteV2`, which persists the
  TriggerEvent, fans out TriggerDeliveries, and enqueues queued DriverRuns
  (`internal/store/platform_store.go:419-420`; cron path at
  `internal/trigger/cron.go:213-232`; memstore dispatch creates the DriverRun
  with concurrency gating at `internal/infra/memstore/platform_trigger.go:543-563`).
  Awaiting workflows can resume on a cron tick via the dispatch-time await
  matcher (`internal/trigger/cron.go:239-251`).
- **CLI** — `loom trigger bindings create|update|list|show`,
  `loom trigger events list|show`, `loom trigger deliveries list|show`
  (`internal/cli/trigger/trigger_cmd.go:25-26,60-83,437-517`). Cron bindings
  take `--schedule` (required when `--source cron`) and
  `--schedule-timezone` (`internal/cli/trigger/trigger_cmd.go:113-116,165-169`).

### Claim-by-claim vs the proposal

| Proposal claim (`docs/design/2026-06-07-trigger-workflow-proposal.md`) | Status |
|---|---|
| `#Current State` "Already exists": TriggerBinding/Event/Delivery, Driver/DriverVersion/DriverRun models + dispatch route | **implemented** (see above) |
| `#Current State` "Missing": GitHub webhook adapter, signature verify, delivery-id dedupe | **implemented since** (`internal/webui/handlers/webhooks/github.go`) |
| `#Current State` "Missing": replay/redelivery tooling | **partial** — events/deliveries are listable via CLI/API; no replay command exists (no `replay` in `internal/cli/trigger/trigger_cmd.go`); idempotent re-dispatch exists internally |
| `#Current State` "Missing": UI/API for managing bindings | **partial** — CLI + store API exist; no web-UI surface (no trigger-binding component under `internal/webui/frontend/src`) |
| `#Proposed Flow` step 8 (executor claims with lease/fencing, runs Flue TS) | **implemented** (`internal/driver/executor.go:108-167`; §5 below) |
| `#Deferred` "Schedule runner implementation" | **implemented since** — `trigger.CronScheduler` (above) |
| `#Deferred` Slack adapter | **proposal-only** — webhook registry contains only `github` (`internal/webui/handlers/webhooks/adapter.go:79`) |
| `#Deferred` WorkerProfile-based routing for trigger-created runs | **proposal-only** (no code path found) |

**Net for the recommender:** a daily/weekly re-run needs zero new scheduling
machinery — create a `source_kind=cron` TriggerBinding whose `RouteKey`
routes to a pinned driver version, and `loom serve` fires it. The
issue-journal bridge additionally offers an event-driven lane (react to
issue changes) with a documented self-trigger guard the recommender must
adopt.

---

## 2. The builtin epic-runner — how a builtin autonomous flow is structured

Docs: `docs/design/epic-runner-lead-control.md`,
`docs/product/lead-agent-epic-runner-spec.md`. Verdict: the *shipped* shape
differs materially from the design doc; the product spec is mostly accurate.

### Shipped architecture

- epic-runner is **not Go**: it is a TypeScript Flue workflow
  (`internal/workflows/builtin/epic-runner.ts`, 684 lines) embedded in the
  binary via `//go:embed` (`internal/workflows/workflows.go:29-30`),
  registered lazily as a **trusted** Driver/DriverVersion, and executed as a
  DriverRun. It is watch-driven and edge-triggered (no polling loop)
  (`internal/workflows/builtin/epic-runner.ts:40-52`, watch loop `:78-205`),
  enforced by `internal/workflows/workflows_test.go:75`
  (`TestBuiltinEpicRunnerWorkflowIsWatchDriven`).
- The Go package `internal/epicrunner` is only shared primitives: lead-role
  predicate `IsLeadRole` (`internal/epicrunner/start.go:71-79`), a
  per-workspace file bind lock under `<LoomDir>/epic-runner-locks/`
  (`internal/epicrunner/start.go:82-133`), error kinds, and the
  lead-assignment context formatter
  (`internal/epicrunner/assignment_context.go:26-82`).
- Server-side Go owns what the workflow must not: lead-notification outbox
  dispatch (`internal/driver/outbox_dispatcher.go:145-148`), TaskRun
  journaling + lead messages (`internal/driver/task_events.go:18-22,145-190`),
  and stale-task sweeping (`internal/driver/stale_task_sweeper.go`).

### What triggers it (all paths end at `driver.CreateDriverRun`, `internal/driver/run.go:38-72`)

1. `loom epic run --parent <epic>` — queues a DriverRun
   (`SourceKind:"cli"`) then executes it inline via `Executor.RunOnce`
   unless `--detach` (`internal/cli/epic/run.go:194-215,297-324`).
2. `POST /api/workspaces/{ws}/workflows/{name}` — returns 202 + the run
   (`internal/webui/handlers/workflows/module.go:35,126-170`).
3. Web-UI "Run epic" button — creates a `lead-<epic-slug>` agent then calls
   the same route
   (`internal/webui/frontend/src/hooks/workspace/startEpicRunnerForIssue.ts`).
4. Generic `loom workflow run` / `loom driver run`
   (`internal/cli/workflow/workflow_cmd.go:333`,
   `internal/cli/driver/driver_cmd.go:180`).
5. **Trigger bindings** — a binding pins a DriverID, so cron/webhook events
   can start it (`internal/infra/memstore/platform_trigger.go:551-563`); no
   shipped binding defaults to it.

### Trust and admission (how a builtin is allowed to run un-sandboxed)

- Trust levels are `trusted|untrusted` on the Driver row
  (`internal/domain/platform.go:44-61,73`) — an admission decision, not
  confinement (`docs/loom-glossary.md#Isolation and Trust`).
- `EnsureBuiltinWorkflow` is the **only** call site that stamps
  `Trust: domain.DriverTrustTrusted`
  (`internal/workflows/workflows.go:166-178`). HTTP-submitted workflows
  default to untrusted, fail-closed (`internal/workflows/workflows.go:329-334`;
  test `internal/workflows/workflows_test.go:250`). A submitted bundle
  cannot self-elevate: `trust_level` is stripped from client manifests
  (`internal/driver/register.go:696`) and re-registration can only demote
  (`internal/driver/register.go:575-588`).
- At run time, `sandbox.RefuseUntrustedPlacement` refuses an untrusted
  driver on a non-isolating launcher before spawning anything
  (`internal/driver/sandbox/policy.go:70-93`, called from
  `internal/driver/executor.go:755`); every run records
  `driver_trust_level` + `sandbox_launcher`
  (`internal/driver/sandbox/policy.go:95-108`).
- **Net:** a recommender shipped as a builtin runs trusted on the local
  process launcher, like epic-runner; shipped as a user upload it would be
  refused outside `LOOM_DRIVER_SANDBOX=container` unless operator-approved
  (`internal/driver/approval.go:81-96`).

### Design-doc claims worth flagging

| Claim (`docs/design/epic-runner-lead-control.md`) | Status |
|---|---|
| `#Model` — dedicated `LeadAssignment`/`EpicRun` stores; `agent.parent` as read-model | **proposal-only / inverted** — no such stores; `agent.parent` IS authoritative, mutated under bind lock (`internal/webui/handlers/driverapi/module.go:405-438`); the durable record is `DriverRun` |
| `#Source-Of-Truth` — one assignment per lead / one run per epic / idempotent resume | **implemented** in TS (`internal/workflows/builtin/epic-runner.ts:499-533,603-615`) |
| `#Source-Of-Truth` — explicit version increment per transition | **not implemented** — "version" is the agent row's `UpdatedAt` timestamp (`internal/epicrunner/assignment_context.go:49-52`) |
| `#Assignment-Delivery` — delivered/acknowledged version fields | **partial** — agent-session metadata keys, not columns (`internal/leadcontrol/codex_metadata.go:24-28`); delivery states are `none|pending|delivered|unsupported` (`internal/leadcontrol/delivery.go:17-24`) |
| `#Backend-Controller-Lease` | **proposal-only** — no controller-lease symbol exists |
| `#Hook-Strategy` blocking semantics (PreToolUse/Stop blocking) | **partial** — four hooks exist but only inject context, never block (`internal/cli/hooks/hooks_assignment_context.go:57-70`) |
| `#Validation-Log` 2026-05-17 (reconcile loop in `internal/epicrunner`, `epics/{id}/run` route, foreground CLI loop) | **superseded** — package doc disclaims it (`internal/epicrunner/start.go:1-4`); the route no longer exists; the loop lives in the TS workflow |

Product-spec claims (`docs/product/lead-agent-epic-runner-spec.md`): lead
role = `lead|orchestrator` (**implemented, triplicated** —
`internal/epicrunner/start.go:71-79`,
`internal/workflows/builtin/epic-runner.ts:617-624`,
`internal/driver/task_events.go:168-176`); bind/resume/reject tri-state
(**implemented**, `internal/workflows/builtin/epic-runner.ts:494-563`);
UI and CLI sharing backend semantics (**implemented**, both create a
DriverRun against the same driver).

---

## 3. "Agent service" — long-lived background agents

Docs: `docs/design/2026-06-07-slack-agent-service-proposal.md`,
`docs/design/2026-06-07-agent-service-driver-version-proposal.md`.

### What exists

- **`domain.AgentService` model + CRUD** — implemented as a desired-state
  record: `ServiceID`, `Kind`, `DesiredState (running|stopped|paused)`,
  `RoleName`, `ProfileName`, `ScheduleID`, `EventSources`, `TriggerRefs`,
  `MaxInstances`, `RestartPolicy`, `Metadata`…
  (`internal/domain/platform.go:147-168`). Kinds include `triage`,
  `scheduled`, `cron`, `always_on`, `on_call`, `maintenance`
  (`internal/domain/platform.go:123-136`). Stores: memstore
  (`internal/infra/memstore/agent_service.go:33`), fleet-db client
  (`internal/infra/fleetdb/client.go:201`). CLI:
  `loom … service add|list|show|set|unset|remove`
  (`internal/cli/serve/worker/service_cmd.go:45-115`). A TriggerBinding can
  reference one via `TargetAgentServiceID`
  (`internal/domain/platform.go:230`).
- **No controller.** Nothing watches `desired_state=running` and starts
  anything: `AgentServiceDesiredRunning` is referenced only by the domain
  type, memstore validation, and the CLI parser
  (`internal/domain/platform.go:142`,
  `internal/infra/memstore/agent_service.go:286`,
  `internal/cli/serve/worker/service_cmd.go:461`). The record is inert
  configuration today.

### Claim-by-claim

| Claim | Status |
|---|---|
| driver-version proposal `#Proposed Shape` — `driver_id`/`driver_version_id` on AgentService | **proposal-only** — fields absent from `domain.AgentService` (`internal/domain/platform.go:147-168`) |
| driver-version proposal `#Runtime Model` — controller with fenced lease starts pinned DriverVersion | **proposal-only** (no controller; above) |
| slack proposal `#Current State` "AgentService model/storage/API as desired-state concept exists" | **implemented** (above) |
| slack proposal — Slack runtime adapter, Socket Mode service, event dedupe | **proposal-only** as a *service*; but Slack **side effects** shipped via a different mechanism: the connector system — `slack.chat.post` / `slack.conversations.read` with idempotency and grants (`internal/connector/providers/slack.go:19-40`, registry with GitHub/Slack/Datadog at `internal/connector/registry_default.go:25-31`), exposed to workflows as the `connector-dispatch` driver op (`internal/webui/handlers/driverapi/module.go:156`) |
| slack proposal — ActionLedger for side effects | **partial** — `ActionLedgerID` exists on driver steps (`internal/domain/platform.go:469`, `internal/infra/fleetdb/platform.go:511`); the Slack-specific ledger action types from the doc do not |

**Net:** "long-lived always-on agent service" is not an available vehicle.
The proven vehicle for background autonomy is *triggered short-lived
DriverRuns* (§1) — which also matches the recommender's daily/weekly shape
better than an always-on process. The slack proposal itself points HTTP
event sources at TriggerBinding/Event/Delivery
(`docs/design/2026-06-07-slack-agent-service-proposal.md#Implementation Plan`
step 5).

---

## 4. Product docs and epics — existing proactive-feature plans and overlaps

- **`docs/product/README.md`** — the product-doc index; nothing about repo
  analysis, ticket suggestion, or backlog grooming.
- **`docs/product/web-onboarding-spec.md`** — the closest existing
  "workspace onboarding" plan, but it is a *guided manual checklist*, not
  automated analysis: six fixed steps ending in "Create first issue" and
  "Run first agent" (`docs/product/web-onboarding-spec.md#Onboarding Flow`).
  Status: **partial** — the frontend flow exists
  (`internal/webui/frontend/src/components/OnboardingFlow/OnboardingFlow.tsx`,
  wired in `internal/webui/frontend/src/App.tsx:68-70`) and a combined
  create-issue-and-start-agent endpoint exists
  (`POST /api/workspaces/{ws}/onboarding/first-task`,
  `internal/webui/handlers/onboarding/module.go:23`,
  `internal/webui/handlers/onboarding/first_task.go:52-80`); the spec's
  server-derived `GET /api/onboarding/status` contract
  (`docs/product/web-onboarding-spec.md#Server Contract`) is **not found in
  Go** — no `ComputeOnboardingStatus` symbol exists. Overlap: step 5's
  "Create first issue" is exactly the moment a recommender could supply
  candidates; the spec explicitly wants first-run onboarding not to strand
  users in an empty shell.
- **`docs/product/orchestrator-worker-model.md#Service agents`** — plans
  "service agents" (on-call, bug-triage, Datadog-monitor) that "watch
  signals, dedupe events, **create or update issues**, and launch
  workflows", with guardrails: event dedupe/replay cursors, cooldowns, one
  active lease per scope, human approval for destructive actions
  (`docs/product/orchestrator-worker-model.md#Service agents`, and
  `#Service-agent placement` — persist cursors in shared storage, not
  node-local files). **Proposal-only as such**, but it is the closest
  articulated product intent to a ticket recommender, and its guardrail
  list maps 1:1 onto machinery that now exists (§1: idempotency keys,
  journal cursor, actor filters, concurrency policies).
- **`docs/product/daemon-agent-runtime-architecture.md`** — daemon/runner
  ownership-lease architecture. Largely **partial/proposal** (its
  `#Existing Codebase Fit` table self-reports gaps; `AgentOwnershipLease`
  is MVP scope, `#MVP Implementation Scope`). Relevant shipped piece: the
  supervisor-owned `hooks.on_complete` pipeline (comment/add_label after a
  successful run) is implemented per
  `#Completing A Run (on_complete Hooks)` — deliberately limited to two
  action types because agent definitions are remotely writable
  control-plane data. Precedent for keeping a recommender's side effects
  declarative and server-executed.
- **`docs/design/fleetdb-agent-platform-v2-proposal.md`** — contains the
  intellectual ancestor of the recommender: an issue-triage workflow
  example that prompts an agent "Triage this issue and recommend next
  action" with a structured result schema
  (`docs/design/fleetdb-agent-platform-v2-proposal.md#DriverVersion`,
  the `.loom/workflows/triage.ts` example), plus `github-issue-triage`
  bindings. **Proposal-only** at that fidelity (the shipped SDK differs,
  §5), but the platform legs it assumed (drivers, runs, triggers) shipped.
- **`docs/epics/cortex-ui-v6-workspace-redesign.md`** — UI-only epic
  (workspace-centric sidebar, workspace-creation modal, backlog counts in a
  Work Queue section). No recommender overlap beyond being the surface
  where workspace creation happens.
- **`docs/product/pr-review-spec.md`** — on-demand PR review agent, not
  proactive; shows the pattern of a persisted special-purpose agent with
  its own queue surface.
- Small adjacent feature: issue close accepts `suggest_next` — a
  suggest-the-next-ready-task flag on close
  (`internal/webui/handlers/issues/issues.go:29`,
  `internal/webui/handlers/issues/write_ops.go:103`). Nothing generates
  new tickets anywhere.

**Net:** no existing doc plans repo analysis or ticket generation; the
recommender fills a hole that `orchestrator-worker-model.md#Service agents`
and the platform-v2 triage example gesture at. No conflicting in-flight
implementation was found.

---

## 5. Authoring a new builtin workflow (the likely vehicle)

Docs: `docs/design/native-flue-driver-integration.md`,
`docs/design/workflow-driver-authoring-guide.md`. Verdict: the platform
contract both docs describe is **implemented**, but the authoring guide's
code examples have drifted from the real contract in three ways that would
break a copy-paste implementation (flagged below). Neither doc documents the
builtin-embedding path itself; the nearest rule is `AGENTS.md:37-42`
("Generated Workflow Bundles").

### Source layout and registry

- Exactly **two builtins**, in one map
  (`internal/workflows/workflows.go:79-82`): `epic-runner` (entry
  `internal/workflows/builtin/epic-runner.ts` + three sibling task-runner
  sources bundled into the same driver,
  `internal/workflows/workflows.go:95-101`) and `github-review-agent`
  (`internal/workflows/builtin/github-review-agent.ts`).
- Adding one is deliberately two Go changes: a
  `//go:embed builtin/<name>.ts` var (`internal/workflows/workflows.go:29-45`)
  and a `builtinWorkflows` map entry via `builtinSpec`
  (`internal/workflows/workflows.go:84-93`). Sibling leaf runners are extra
  entries in the spec's `Files`. Note
  `internal/workflows/github_review_agent_test.go:17`
  (`TestBuiltinWorkflowRegistryListsBothBuiltins`) hard-codes the builtin
  count and will fail on a third builtin (intentional tripwire).
- Only `.ts` **source** is embedded; bundles are never committed
  (`AGENTS.md:37-42`). Flue toolchain is pinned by commit:
  `internal/workflows/FLUE_COMMIT` enforced by
  `internal/workflows/flue_pin_test.go:27-35`.

### Required workflow shape (where the authoring guide is wrong)

- Modules must default-export `defineWorkflow({...})` with a
  credential-free stub agent (`model: false`); a bare
  `export async function run` no longer normalizes at the pinned flue
  commit. The invocation payload arrives via env
  (`LOOM_FLUE_INVOKE_PAYLOAD`, fallback `LOOM_TASK_RUN_REQUEST_JSON`), not
  flue's input channel (`internal/workflows/builtin/epic-runner.ts:1-29`).
  The guide's bare-`run` example
  (`docs/design/workflow-driver-authoring-guide.md#minimal-workflow-shape`)
  is **stale**.
- SDK import is `@loom/sdk/driver`; the guide's `@loom/sdk/flue` does not
  exist (`sdk/package.json:6-25` exports only `.`, `./runner`, `./driver`,
  `./runtime-adapters`; `internal/driver/register.go:24`). **Doc drift.**
- `providerProfile`/`supportedProviders`/`sandboxPlacement` from the
  guide's epic-runner example are **actively forbidden** in builtin sources
  — a test greps for them (`internal/workflows/workflows_test.go:209-230`).
  The real contract is a named `runner` string resolved against the
  version manifest (`internal/workflows/builtin/epic-runner.ts:83,276-284`;
  `internal/driver/register.go:223-226`).

### Build, registration, execution (all implemented)

- Build: `BuildAndRegister` writes a temp project (symlinking `@loom/sdk`,
  `@flue/runtime`, hono; optional `@daytona/sdk`) and shells
  `flue build --target node`
  (`internal/workflows/workflows.go:337,614-672,732-752`). Dev
  regeneration: `scripts/rebuild-builtin-bundle.sh` (refuses on flue-pin
  drift). Two digests: canonical `SourceDigest`
  (`internal/workflows/digest.go:32-49`) and a bundle-tree digest that
  names the version id (`internal/driver/register.go:438-443,796`).
- Registration is **lazy self-heal**, not a startup seed: nothing registers
  builtins at `loom serve` boot. `EnsureBuiltinWorkflow`
  (`internal/workflows/workflows.go:134-190`) runs on first invocation from
  exactly three call sites (`internal/webui/handlers/workflows/module.go:172-189`,
  `internal/cli/epic/run.go:288-295`) and stamps `Trust: trusted`,
  `CreatedBy:"system"`, `DeriveRunners:true`. Reuse deliberately does not
  require digest equality (drift is logged, not rebuilt — a toolchain-less
  serve must not fail closed; `internal/workflows/workflows.go:194-217`).
  This matters for the recommender's "runs when a workspace is created"
  moment: today something must *invoke* a builtin before it exists as a
  driver.
- Execution: serve's executor loop (2s tick) claims queued runs with
  lease/fencing, mints a run-scoped HS256 token, verifies the bundle
  digest, and launches `dist/server.mjs` over a ready/invoke IPC handshake
  (`internal/cli/serve/serve.go:299,324-343`;
  `internal/driver/executor.go:108-184,565-631,743`;
  `internal/driver/sandbox/launcher.go:255-320`). `LOOM_DRIVER_EXECUTOR=0`
  leaves runs queued (`internal/cli/serve/serve.go:42,448-455`). Lifecycle
  states are `queued → running → completed|failed|needs_review|cancelled`
  plus the guide-omitted `suspended_awaiting_event`
  (`internal/driver/executor.go:891-922`).

### How a workflow gets LLM/agent capability

A workflow never calls an LLM directly. The chain is:
workflow → `loom.taskRuns.request()` (op `exec-task`, enqueue-only)
(`sdk/driver.js:279-309`;
`internal/webui/handlers/driverapi/module.go:150`) → queued TaskRun →
serve TaskWorker → host bridge (`internal/driver/task_bridge.go:202,304`) →
node launcher forks the bundle's task-runner entrypoint → the leaf
`execFile`s a backend CLI. The stock leaf
`internal/workflows/builtin/local-task-runner.ts` supports
`claude|codex|opencode|gemini|cursor` (`:52-58`), builds backend args
mirroring `internal/cli/backends/backend_*.go` (`:449-493`), and completes
only on exit 0. A second pattern —
`internal/workflows/builtin/github-review-task-runner.ts:25-33` — runs
`codex exec` with `--output-schema`/`--output-last-message` for
**JSON-schema-constrained structured output**, which is the closest shipped
precedent for "analyze a repo and emit N structured ticket proposals".
TaskRuns normally require an existing `taskId` (a fleet-db issue,
`sdk/driver.js:279`) — a chicken-and-egg constraint for a recommender whose
job is to *produce* issues (see Open Questions).

### Data access from workflow code

The workflow-side SDK (`@loom/sdk/driver`, `sdk/driver.js:63-115`) routes
every call through `POST /api/workspaces/{ws}/driver/{op}` authenticated by
the run token; namespaces cover epics (get/snapshot/SSE watch), tasks
(claimReady/complete/release), taskRuns, agents, events (await/list),
child workflows, and connectors (GitHub/Slack/Datadog). Fleet-db issue
access is server-side only: each op builds a workspace+actor-scoped
fleet-db backend with actor `driver-run:{runId}`
(`internal/webui/handlers/driverapi/module.go:170-185,340`), so the
credential never enters the sandbox. The task-runner-side SDK
(`@loom/sdk/runner`) can fetch its issue via `getTask()`
(`sdk/runner.js:113-127`). **There is no driver op for creating issues** —
the op registry (`internal/webui/handlers/driverapi/module.go:141-158`)
has no `create-issue`; issue creation today happens via the web/CLI issue
services or agents running `loom data` in a checkout.

---

## 6. Workspace-adjacent file conventions (for `agents.md` / `history.md`)

### The directory model

- Per-user root `LoomDir()` = `~/.loom` (`LOOM_CONFIG_DIR` override)
  (`internal/bootstrap/paths.go:39-51`); default workspace checkout root
  `<LoomDir>/workspaces/<name>` (`internal/bootstrap/paths.go:68-74`).
- **`cli.GetWorkspaceRuntimeDir()` is the anchor for workspace-local,
  non-repo files** (`internal/cli/worktree_resolve.go:575-601`):
  `LOOM_WORKSPACE_RUNTIME_DIR` env → active workspace's configured path →
  `"."`. Caveat: the `"."` fallback means in single-project (non-workspace)
  mode "workspace runtime dir" *is the repo checkout*, which is why
  `.gitignore:33-52` carries `/sessions/`, `usage.jsonl`, `notify.token`,
  `.loom/`. Any new workspace-root file inherits this hazard.
- Layout under a workspace: repo checkouts as direct children
  (`internal/localworkspace/localworkspace.go:98-120`), agent worktrees at
  `worktrees/<repo>/<agent>` (`:40-42`), task/PR worktrees, driver bundles,
  logs, sockets under `.loom/` (`internal/rpc/socket_path.go:31-58`,
  `internal/driver/register.go:443-444`,
  `internal/webui/app/server_workspace.go:108,195`). `loom init` scaffolds
  only `worktrees/` — **no workspace-root markdown/config file is ever
  generated** (`internal/cli/workspace/init_helpers.go:63-88`).

### AGENTS.md / agents.md / CLAUDE.md today

- **Loom neither reads nor writes any agent-onboarding file.** The only
  code references: (a) `AGENTS.md` is in `ProtectedRuntimePaths` so
  recovery `git clean` never deletes it
  (`internal/cli/daemon_runtime.go:127-134`, consumed as `--exclude` args
  at `internal/cli/agent/recover_helpers.go:248-267`) — Loom protects a
  repo-root AGENTS.md it expects *backends* to use but never creates;
  (b) UI transcript filters strip Codex's synthetic
  "# AGENTS.md instructions" turn
  (`internal/webui/frontend/src/components/IssueDetailPanel/sessions/SessionDetailView.tsx:115-123`,
  `internal/webui/handlers/prreview/stream.go:288-300`); (c) prompt prose
  in `internal/cli/agent/prompts/pr-review-checkout.md:39-43`. AGENTS.md
  discovery is the harness backend's cwd-rooted behavior, not Loom's — a
  workspace-root `agents.md` would **not** be auto-discovered by
  claude/codex and needs explicit injection.
- fleet-db has a **manual** convention only:
  fleet-db:`docs/agents/worktree-agents-template.md:1-4` ("Copy this file
  to `<worktree>/AGENTS.md`"); no code in either repo copies it.

### History / notes / memory precedents

- **Nothing in the Go tree generates markdown at all** — file-write sites
  are JSON/JSONL/text tokens/patches. The de facto run history is
  `<ws>/sessions/index.jsonl` (append-only, flock-guarded)
  (`internal/sessions/finalize.go:104-118`), beside per-session dirs
  `<ws>/sessions/<id>/` with `prompt.txt`, `metadata.json`, `diff.patch`,
  `agent_transcript.jsonl`, `events.jsonl`
  (`internal/sessions/store.go:43-52,82-85,253-261`;
  `internal/sessions/eventstore/eventstore.go:36-48`), plus
  `<ws>/usage.jsonl` (`internal/usage/store.go:21-27`). Retention exists
  (`internal/sessions/purge.go:15-40`). A `history.md` would be a
  human-readable sibling/derivative of `index.jsonl`.
- Cross-run memory today is only the per-worktree
  `.agent.checkpoint.json` → "PREVIOUS ATTEMPT CONTEXT" prompt block
  (`internal/cli/config/checkpoint.go:11-51`,
  `internal/cli/agent/prompts.go:472-494`) — inside the worktree, single
  attempt lookback.
- `docs/product/session-artifact-contract.md`: metadata/transcript/diff/
  usage/error-class **implemented** (sessions package above);
  `#Cleanup Metadata` and `#Test Artifact` **partial**; it says nothing
  about onboarding notes or human-readable history — the recommender's
  files are greenfield relative to it.
- Adjacent proposal-only file conventions found while sweeping (do not
  build on them): `~/.loom/session-scrollback/*.log` writer has no non-test
  callers (`internal/webui/sessionhistory/store.go:80,123` vs
  `docs/arch/terminal-system.md:400`); `.loom/terminal-worktrees` exists
  only in a hardening test
  (`internal/webui/svcimpl/file_walk_hardening_test.go:25` vs
  `docs/design/workspace-file-browser-security.md#Filesystem boundary`);
  `~/.loom/stack-publish-log.jsonl`
  (`docs/design/2026-06-18-stack-aware-pr-publisher.md:349`) unimplemented.

### Established mechanisms the new files can ride

- Workspace-root non-repo storage is a shipped pattern (`sessions/`,
  `usage.jsonl`, `notify.token` at `internal/webui/app/notify_token.go:24-25`)
  — all via `GetWorkspaceRuntimeDir()`.
- Workspace-root files are already browsable/editable in the web UI via
  the file browser's `scope=workspace`
  (`internal/webui/svcimpl/file_service.go:151-152,178-187`).
- Workspace-relative *reads* of hand-authored markdown are established:
  role `prompt_file` resolves cwd → workspace root
  (`internal/cli/agent/prompts.go:448-470`;
  `internal/cli/role/role_cmd.go:128` "relative to workspace") — the
  natural injection path for `agents.md` content into agent prompts.
- Atomic-write and flock utilities exist (`internal/atomicfile`,
  `internal/sessions/store.go:253-261` tmp+rename pattern).
- Protection precedent: new non-repo files written where the `"."`
  fallback can land them must be added to `.gitignore` and
  `ProtectedRuntimePaths` (`internal/cli/daemon_runtime.go:130-134`) or
  agent recovery's `git clean` can destroy them.

---

## Open questions for design

1. **Trigger vs first-run.** Cron bindings cover daily/weekly re-runs, but
   nothing fires on *workspace creation* — the issue-journal bridge covers
   issue mutations only, and builtins register lazily on first invocation
   (`internal/workflows/workflows.go:134-190`). Should workspace-create
   emit an internal trigger event, should the onboarding/first-run web flow
   invoke the workflow directly, or should workspace creation seed both the
   cron TriggerBinding and an immediate run?
2. **Issue creation from a workflow.** No `create-issue` driver op exists
   (`internal/webui/handlers/driverapi/module.go:141-158`), and
   `loom.taskRuns.request` requires an existing `taskId`
   (`sdk/driver.js:279`). Does the recommender get a new driver op (with
   actor `driver-run:{runId}` provenance), reuse the web issue service, or
   run its LLM leaf in a checkout where `loom data` works?
3. **Self-trigger guard.** If the recommender ever binds to
   `internal.issue.*` events, it must carry `exclude_actor_kinds` per the
   mandatory guidance at `internal/trigger/issue_journal_bridge.go:29-45`.
   Even cron-only, should its created issues carry a distinguishing actor
   or label so other triage bindings can exclude them?
4. **LLM leaf shape.** The repo-analysis step needs an agent run without a
   pre-existing task. Follow the `github-review-task-runner.ts` pattern
   (schema-constrained `codex exec` against a checkout,
   `internal/workflows/builtin/github-review-task-runner.ts:25-33`), or
   generalize `local-task-runner.ts` to accept a taskless prompt?
5. **Trust/isolation posture.** As a builtin it runs trusted and
   un-sandboxed (`internal/workflows/workflows.go:177`,
   `internal/driver/sandbox/policy.go:70-93`). Is read-only repo analysis
   acceptable under that posture, or should the recommender voluntarily run
   its leaf under `LOOM_DRIVER_SANDBOX=container` / Daytona?
6. **Where exactly do `agents.md`/`history.md` live**, given the
   `GetWorkspaceRuntimeDir()` `"."` fallback lands files inside the repo in
   single-project mode (`internal/cli/worktree_resolve.go:575-601`)?
   Workspace root (browsable via `scope=workspace`) vs a `.loom/`
   subdirectory (already protected+ignored) trade visibility against
   collision safety; either way both names likely need `.gitignore` +
   `ProtectedRuntimePaths` entries.
7. **`history.md` vs `sessions/index.jsonl`.** Derive the markdown from the
   existing append-only history (`internal/sessions/finalize.go:104-118`)
   plus DriverRun records, or append independently and accept two diverging
   histories? DriverRuns already persist status/summary/logs_ref per run
   (`internal/driver/executor.go:924-960`).
8. **`agents.md` injection.** Backends auto-load repo-root `AGENTS.md` from
   cwd, one level below a workspace-root `agents.md`. Inject via the
   workspace-relative `prompt_file` resolver
   (`internal/cli/agent/prompts.go:448-470`), a template variable like the
   existing `{{.WorkspaceBlock}}` set
   (`docs/loom-glossary.md#Custom Prompt Template Variables`), or copy into
   worktrees per fleet-db's manual template convention?
9. **Recommendation dedupe/cadence state.** Where does "already proposed
   this" live — issue labels, a workspace-root state file (cf. the
   issue-bridge cursor pattern,
   `internal/cli/serve/serve_loops.go:319-336`), or fleet-db records —
   given `orchestrator-worker-model.md#Service-agent placement` requires
   shared storage, not node-local files, for service-agent cursors?
10. **AgentService record.** Should the recommender register a
    `domain.AgentService` row (`kind=scheduled`) purely as declarative
    intent/observability even though no controller executes them, or avoid
    the inert concept entirely?
11. **Builtin count tripwire.** A third builtin fails
    `internal/workflows/github_review_agent_test.go:17` by design — the
    test must be extended deliberately, and the flue pin
    (`internal/workflows/FLUE_COMMIT`) constrains the workflow shape
    (`model: false`, `defineWorkflow` default export).
