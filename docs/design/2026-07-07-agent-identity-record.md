# Durable Agent Identity: the Agent Record for the Unified Platform

**Status:** Proposed (design for task #27; consumed by task #26 / WS4a)
**Date:** 2026-07-07
**Provenance:** Fable planner agent, designing to the "Decision — durable agent identity (Tyson, 2026-07-07)" addendum of `2026-07-01-unified-agent-ux-proposal.md`. Every structural claim verified in-tree at loomcli `00a91efc` / fleet-db `34c9cf3`.
**Related:** `docs/design/2026-07-01-unified-agent-ux-proposal.md` (Decision addendum, Open Question 2), `docs/design/2026-06-07-agent-service-driver-version-proposal.md`, `docs/design/2026-07-07-e2e-feature-parity-plan.md` (WS4a), `docs/design/create-agent-redesign.md` (superseded create-flow context for #26).

Paths abbreviated `loomcli/`, `fleet-db/`.

## Summary of the recommendation

**Reuse fleet-db's existing `AgentService` as the durable Agent identity record — trimmed at the loomcli API, not forked.** Do not invent a new lean record (it would be the *third* agent-identity table in fleet-db), and do not put TS agents into the Go `agentdef` table (the daemon supervisor spawns processes off those rows). The entire persistence chain for `AgentService` already exists end-to-end: fleet-db model + validation (`fleet-db/internal/models/platform.go:290-350`), redis and postgres storage (`fleet-db/internal/storage/platform.go:238-330`, `fleet-db/internal/storage/postgres/platform.go:306-420`), HTTP CRUD routes (`fleet-db/internal/api/platform.go:70-74`), openapi coverage (`fleet-db/api/openapi.yaml:2434-2490`), a loomcli client (`loomcli/internal/infra/fleetdb/agent_service.go`), the store interface (`loomcli/internal/store/platform_store.go:131-186`), and a CLI (`loomcli/internal/cli/serve/worker/service_cmd.go:166`).

Critically, **the binding→agent attachment field already exists and is enforced**: `TriggerBinding.TargetAgentServiceID` (`loomcli/internal/domain/platform.go:230`) round-trips through the loomcli client (create `fleetdb/platform.go:158`, filter `:212-213`, patch `:865`), is a list filter on both fleet-db backends (`postgres/platform.go:493`), and fleet-db *refuses to delete an agent service that still has bindings attached* (`postgres/platform.go:402-413`, redis `storage/platform.go:313-318`). The record, the join, and the referential guard are all shipped. What this design adds is the loomcli API surface, lifecycle semantics, the 1:1 binding migration, and one small, justified fleet-db field batch.

Delivery is two waves: **Wave A is loomcli-only** and gives #26 everything it needs now (prompt agents — which all carry a `role_name` — get identity records; the record is created transactionally with its binding). **Wave B is one contained fleet-db batch** (driver-ref fields, `deleted_at`, `created_by`, run-level agent stamping) that brings scripted agents onto the record and makes run attribution direct instead of joined.

---

## 1. Record shape

### 1.1 Why `AgentService`, not a new record and not `agentdef`

Three candidates were evaluated against real code:

| Candidate | Verdict | Reason |
|---|---|---|
| Go `agentdef` (`fleet-db/internal/models/agent.go:54-130`, loomcli `internal/domain/agent.go:42`) | **No — do not put TS agents here** | The daemon reconciler treats these rows as spawn intent: `loom agentdef add` even provisions worktrees at create (`loomcli/internal/cli/agentdef/agentdef_cmd.go:134-141`, `ensureAgentDefinitionLocalWorktrees` `:215-256`), and fleet-db overlays process liveness from the session+lease join (`models/agent.go:113-129`). A TS agent row here risks the supervisor trying to spawn it. It is however the **lifecycle model** we copy: the row exists independently of any process/task/claim, with create/list/show/remove/start/stop verbs (`agentdef_cmd.go:59-99`). |
| New lean `Agent` record | **No** | fleet-db would then hold *three* agent identity tables (`agents`, `agent_services`, plus the new one). All the new-record work (storage ×2 backends, routes, permissions, openapi, client, contract-guard vendoring) is L-sized cross-repo work to reach a shape `AgentService` already has. The decision addendum names `AgentService` as the shape explicitly. |
| **`AgentService`, trimmed** | **Yes** | Everything in the Summary above, plus: `Artifact` and `Lease` already recognize `agent_service` as an owner/resource type (`fleet-db/internal/models/control_plane.go:301,428`), so Phase-5 controller plumbing has anchor points waiting. |

### 1.2 The fields (what the loomcli Agent API exposes)

The stored model is `domain.AgentService` (`loomcli/internal/domain/platform.go:147-168`) unchanged in Wave A. The loomcli `agents` surface exposes a **trimmed DTO** — the identity subset — and treats everything else as dormant:

| DTO field | Backing field | Notes |
|---|---|---|
| `id` | `ServiceID` | Immutable identity. New agents mint `agt-<slug>-<rand>`; migrated agents keep their old `binding_id` as id (§6.2). |
| `name` | `Name` | Display name, freely renameable (fixes F6a-class "name ignored" bugs at the root: identity is `id`, not name). |
| `kind` | derived | `"prompt"` when `role_name` is set, `"scripted"` when the behavior is a driver ref (Wave B). Not stored separately — see §1.4 on the stored `Kind`. |
| `behavior` | `RoleName` (Wave A) · `DriverID`+`DriverVersionID` (Wave B) | **Exactly one behavior ref.** Prompt agent → `{"role_name": "docs-assistant"}`; scripted agent → `{"driver_id": "bug-fix-agent", "driver_version_id": "drvver_…"}`. Config-by-reference settled: the record points at the Role or DriverVersion, never embeds prompt/source. |
| `enabled` | `DesiredState` | `enabled ⇔ desired_state == "running"`; disable maps to `"paused"` (§3.2). |
| `budget_policy` | `BudgetPolicy` | Attribution home for budget (field exists today). |
| `workspace_key`, `created_at`, `updated_at` | same | — |
| `created_by` | new (Wave B) | Stamped from fleet-db's `X-Actor`; `WorkflowSchedule.CreatedBy` is the in-repo precedent (`domain/platform.go:100`). |
| `bindings` | derived | Attached trigger bindings via `TriggerBindingFilter.TargetAgentServiceID` (`store/platform_store.go:264-271`). Config, not identity. |
| `last_run_status`, `consecutive_failures`, `next_fire_at` | derived | Reuse the existing binding decorators (`triggerbindings/module.go:106-119`, `142-176`), aggregated worst-of across attached bindings. |

### 1.3 Deferred to Phase 5 (present on the model, dormant at the API)

`MaxInstances`, `RestartPolicy`, `PlacementPolicy`, `LeaseID`, `StateRef`, `ScheduleID`, `EventSources`, `ProfileName`, `Permissions`. These are the desired-state **controller's** vocabulary (per `2026-06-07-agent-service-driver-version-proposal.md`, which owns that phase). The loomcli DTO does not expose them; fleet-db keeps storing them untouched. Explicitly: `desired_state` is used *only* as the enabled/paused bit until the controller exists — no loomcli code may reconcile it into processes before Phase 5.

`TriggerRefs` (`domain/platform.go:157`) is **deliberately never written**. It is the forward form of the same join `TargetAgentServiceID` expresses backward, and fleet-db validates their consistency (`storage/platform.go:3839-3855`) — two writable representations of one edge is a drift generator. Authority: `binding.target_agent_service_id`, the field fleet-db's delete guard and list filters already use.

### 1.4 The stored `Kind` field

`AgentServiceKind` (`domain/platform.go:123-137`) already includes `event` and `cron`. Set it descriptively at create (`event` for `internal.task.ready` agents — the Decision-4 default — `cron` for scheduled ones) and PATCH it on trigger swap (the update path supports it, `fleet-db/internal/api/platform.go:487`). It is **informational**: the authoritative trigger topology is the attached bindings. Do not build behavior on `Kind`; the capability-based-tabs principle (proposal §UX) applies to the API too. `lead`/`always_on` kinds stay reserved for Phase 5 interactive agents.

### 1.5 Where it is stored

**fleet-db, via the existing `agent-services` resource** — because there is nowhere else durable: loomcli always runs against fleet-db, embedded in local mode (`loomcli/internal/bootstrap/openstore.go:30-37,128`); `memstore` is test-only. A "loomcli-side interim store" would in practice mean wedging identity into a free-form field of some other record — exactly the `source_config_ref` pattern the proposal's Miss-2/config-by-reference addenda spent a week unwinding. Rejected.

The standing loomcli-only constraint is honored by **splitting the waves**: Wave A uses only fields and routes fleet-db already serves (zero fleet-db changes); Wave B is the one justified fleet-db batch (§6.3), and fleet-db strict-decodes unknown fields (`fleet-db/internal/api/request.go:96-129`), so there is no wedge alternative for those fields even if we wanted one.

---

## 2. Attribution model

### 2.1 The three layers

```
Agent record (identity)          agent_services row, id = agent_id
  └─ attached config             trigger_bindings rows, target_agent_service_id = agent_id
       └─ dispatch provenance    driver_runs rows, trigger_binding_id = binding_id
                                 (Wave B: + agent_service_id = agent_id, stamped at dispatch)
```

- **Runs.** fleet-db already stamps `trigger_binding_id` on every trigger-dispatched run and loomcli decodes it (`domain/platform.go:405-410`); the binding-scoped run-now stamps it on manual runs too (`triggerbindings/module.go:360-373`). *That field stays* — it is dispatch-level provenance (which trigger leg admitted this run). Agent attribution is the join `run.trigger_binding_id → binding.target_agent_service_id` in Wave A, and a direct `run.agent_service_id` stamp in Wave B (fleet-db copies `binding.TargetAgentServiceID` onto the run in `dispatchTriggerRouteLeg`, exactly parallel to how it stamps the binding id today, plus a list filter — the same shape as the `trigger_binding_id` filter that landed in fleet-db `b8ecd01`).
- **Grants.** Connector grants stay keyed to bindings (`domain/connector.go:157`, provisioned per binding by the create flow, revoked on binding delete per Decision 6, `triggerbindings/module.go:492-546`). The **agent-level grants view is derived**: union of grants across attached bindings. No schema change; the agent is the unit you *reason* about, the binding is the unit the credential is *scoped* to — which is what makes "delete binding = detach config, revoke its grants" compositional.
- **Budget.** `BudgetPolicy` lives on the record now (field exists); spend attribution is "runs of this agent", i.e. the same run query. Enforcement is out of scope here (budget-policy territory flagged in the adversarial vet).

### 2.2 Why the join is not enough long-term (the Wave-B stamp is required)

Binding churn breaks the join: if a config swap ever *replaces* a binding (delete + recreate) rather than PATCHing it, runs recorded under the deleted binding no longer join to the agent. The Wave-A rule is therefore **PATCH bindings in place** (`TriggerBindingUpdate` supports even `SourceKind`, `store/platform_store.go:273-276`), and the Wave-B `agent_service_id` stamp makes attribution immune to binding replacement — which is the entire point of the identity decision. Until Wave B, a forced binding replacement loses that binding's runs from the *agent's* history view (they remain in the driver-level history); this is priced and temporary.

### 2.3 The 1:1 binding→agent migration and pre-migration runs

Every existing TS agent is one binding today (wave 1 of the parity plan). Migration (§6.2) creates one agent record per agent-shaped binding with **`agent_id := binding_id`** and back-stamps `binding.target_agent_service_id = agent_id`. Consequences, all free:

- **Pre-migration runs attribute correctly with zero data migration**: they carry `trigger_binding_id`; the binding survives and now points at the agent; the join covers them.
- **Existing deep links keep working**: the detail route is `/ws/{ws}/agents/{name}` where the URL segment is the binding id today (`AgentsPage.tsx:159-175`); after migration the same segment resolves as the agent id.
- Runs belonging to bindings deleted *before* migration are orphans (attributable to no agent). Accepted — they were already orphans under the binding-proxy model.

### 2.4 The id-reuse ambiguity this kills (and the rule that keeps it dead)

Wave 1's live run surfaced it: `promptAgentBindingId` derives a **deterministic** id from the role slug (`CreateAgentModal/agentTemplates.ts:364-374`), so delete + recreate silently *adopts* the old binding's run history and failure health (both keyed to binding id, `triggerbindings/module.go:142-176`). Under the record: **new creates mint fresh unique ids — both the agent id and its binding ids** (binding id = `<agent_id>-<n>` or random suffix). Deterministic ids survive only as grandfathered migration seeds. History adoption then requires deliberately reusing a *record*, which is exactly what identity means.

---

## 3. Lifecycle semantics

### 3.1 State machine

```
            create                       delete (lifecycle event)
  (none) ──────────► enabled ◄──────► disabled ──────► archived
                     (desired_state    (desired_state   (deleted_at set [Wave B] /
                      = running,        = paused,        metadata archive marker [Wave A];
                      bindings          bindings         bindings hard-deleted,
                      enabled=true)     enabled=false)   grants revoked, runs retained)
```

- **Create** — always via the transactional create flow (§5): identity row first, config attached second. An agent with zero bindings is legal ("unconfigured": visible, disabled-looking, deletable) — it is the compensation-window state, not an error state.
- **Enable / disable** — action sub-resources mirroring the binding pattern (`triggerbindings/module.go:52-53`). Semantics: set `desired_state` (`running`/`paused`) on the record **and fan out** `enabled=true/false` to every attached binding, because fleet-db's dispatch route resolution filters on *binding* enabled only (proposal's fan-out reference: `fleet-db/internal/api/platform.go:1397,1525-1546`) and does not consult the agent record. To keep one authority, the existing per-binding enable route **rejects attached bindings** with 409 "managed by agent {id}" (loomcli-side check on `binding.TargetAgentServiceID` in `setEnabled`) — a disabled agent can no longer be resurrected one binding at a time.
- **Delete = the lifecycle event.** Ordered: (1) list attached bindings; (2) per binding, delete it and revoke its grants — reusing the existing delete-then-revoke handler logic verbatim (`triggerbindings/module.go:492-546`); (3) **archive** the record — set `deleted_at` (Wave B) / `metadata["archived_at"]` (Wave A interim; `Metadata` is patchable end-to-end, `store/platform_store.go:176`, `fleet-db/internal/api/platform.go:501`), and `desired_state=stopped`. List views exclude archived by default (`?include=archived` opts in). Run history is **retained and still attributed** (the record survives; Wave-B-stamped runs don't even need it to survive). fleet-db's hard `DELETE /agent-services/{id}` remains ops-only purge tooling; its bindings-attached guard (`postgres/platform.go:402-413`) makes the ordering above mandatory anyway.
- **Config swap under stable identity** — all PATCHes, identity untouched:
  - *Trigger change* (cron↔event, cadence): PATCH the attached binding (`TriggerBindingUpdate` carries `SourceKind`, schedule fields); update record `Kind` descriptively. Prefer PATCH over replace (§2.2).
  - *Driver-version upgrade* (scripted): PATCH `binding.driver_version_id` — the binding pins the version a fire dispatches (`triggerbindings/module.go:368-372`); Wave B also updates the record's `driver_version_id` behavior ref (validated to belong to `driver_id`, per the companion proposal's open notes).
  - *Role reassignment* (prompt): PATCH `record.role_name` — fleet-db re-validates the role exists (`storage/platform.go:3830-3833`, re-run on update at `:307`). Prompt *content* edits don't touch the agent at all (Role is the shared behavior object, Decision 2).

### 3.2 Why disable = `paused`, not `stopped`

`AgentServiceDesiredState` is `running|stopped|paused` (`domain/platform.go:139-145`). `paused` reads as "configured but temporarily off" — the operator's disable. `stopped` is reserved: pre-Phase-5 it marks archived records; at Phase 5 the controller gives `running/stopped` its real reconciliation meaning for long-running agents without colliding with the enable/disable bit. This mapping is the one Phase-5-facing choice in this doc and it is deliberately conservative.

---

## 4. API contract

All new routes are loomcli webui routes (no fleet-db route additions in Wave A). The path `/api/workspaces/{ws}/agents` is today the Go agentdef CRUD (`loomcli/internal/webui/handlers/agents/module.go:24-33`); it **becomes the unified surface** rather than gaining a sibling.

### 4.1 Routes

```
GET    /api/workspaces/{ws}/agents                 unified list (kind-discriminated)
POST   /api/workspaces/{ws}/agents                 kind-routed create (§5)
GET    /api/workspaces/{ws}/agents/{idOrName}      detail (record embeds attached bindings)
PATCH  /api/workspaces/{ws}/agents/{idOrName}      rename / behavior-ref swap / budget  (supervised: existing semantics)
DELETE /api/workspaces/{ws}/agents/{idOrName}      lifecycle delete (§3.1)              (supervised: existing semantics)
POST   /api/workspaces/{ws}/agents/{id}/enable     ┐ record-kind only; supervised keep
POST   /api/workspaces/{ws}/agents/{id}/disable    ┘ start/stop/restart/yield (module.go:30-33)
GET    /api/workspaces/{ws}/agents/{id}/runs       agent-scoped run history (§4.3)
```

`{idOrName}` resolution order (server and client identical): **agentdef name first, agent-record id second** — the exact precedence `AgentsPage.tsx:159-175` already implements for bindings, with the binding branch demoted to third as a legacy fallback during migration. Collisions are prevented structurally: new record ids carry the `agt-` prefix (agentdef names are operator-chosen handles like "falcon"); migration checks seed ids against existing agentdef names and prefixes on conflict.

### 4.2 The `kind` discriminator (closes Open Question 2)

List entries:

```json
{ "kind": "supervised", "id": "falcon", "name": "falcon", "role_name": "task", "state": "active", ... }   // agentdef, unchanged fields
{ "kind": "prompt",    "id": "agt-docs-x7", "name": "Docs assistant", "enabled": true,
  "behavior": { "role_name": "docs-assistant" }, "bindings": [...], "last_run_status": "completed", ... }
{ "kind": "scripted",  "id": "agt-bugfix-k2", "behavior": { "driver_id": "bug-fix-agent", "driver_version_id": "..." }, ... }
{ "kind": "binding",   "id": "review-loop", ... }   // legacy: unattached binding, disappears as migration completes
```

**What consumers must tolerate** (the contract, stated once so daemon/CLI/UI teams can code to it):

1. **Unknown `kind` values must render/route generically, never crash or filter-to-empty.** New kinds *will* appear (Phase 5 adds `lead`-like service agents).
2. **Key on `id`, never on `name`** — names are mutable display data on record-kind entries.
3. **Absent fields are capability signals, not errors** — no `state`/`live_status` on record kinds (no process exists); no `bindings` on `supervised`. This is the API-level form of "capability-based tabs, not kind-based views".
4. Existing consumers are safe by construction: the FE reads `/api/monitor/agents` for Go agents (`api/agents/agents.ts:33-39`), and `loom data agents list` reads the *daemon's* `agentcontrol` route (`webui/handlers/agentcontrol/handlers.go:113-115`) — a different server that is untouched. The webui GET simply gains entries and a `kind` field on old ones.

### 4.3 The runs sub-resource, and how WS4a builds it without throwaway

`GET /agents/{id}/runs` is the durable home of the surface the interim decision roots on bindings. Implementation: resolve attached bindings (`TriggerBindingFilter.TargetAgentServiceID`), list runs per binding via the already-pushed-down `DriverRunFilter.BindingID` (`infra/fleetdb/platform.go:349`, used by binding health at `triggerbindings/module.go:162-165`), merge newest-first with the shared sorter (`store.SortDriverRunsNewestFirst`). Wave B collapses this to one filtered query (`agent_service_id`).

**Instruction to WS4a (build NOW, keep later):** implement the binding-scoped run listing as a **shared helper in `runhistory`** (it already owns `ParseRunLimit`/`SortAndTrim`, see `workflows/module.go:139-161`) taking a `DriverRunFilter`, and mount it twice: under the existing `?binding=` param on `GET /workflows/{name}/runs` *and* ready for `GET /agents/{id}/runs`. The response envelope for binding/agent-scoped views must **not** be driver-rooted — return `{ runs: [...] }` with per-run provenance, not the current `{driver_id, active_version_id, runs}` shape, which is a driver-history envelope. That is the concrete meaning of "the runs surface must not stay rooted on the driver."

### 4.4 Sidebar and AgentsPage resolver

- **Sidebar** (`WorkspaceTree/AgentSection.tsx`): the Autonomous group currently lists all bindings via `useAutomations` (`:62-63`). It becomes: agent records (all kinds except `supervised`, which stays in its current lanes) **plus** only *unattached* bindings; any binding with `target_agent_service_id` set is hidden as covered by its agent row. One dedupe rule, applied in one place.
- **Detail** (`AgentsPage.tsx:588`): the `selectedBinding` branch generalizes to `selectedAgentRecord` — `WorkflowAgentDetail` receives the record plus its attached bindings and renders trigger/config from them. The Go-agent branch is untouched. WS7's capability-shell convergence later dissolves the branch entirely; nothing here blocks or duplicates that.

---

## 5. Create flow integration (task #26 behavior cards)

Today the modal performs client-side sequencing — `ensureRole` then `createBinding` (`CreateAgentModal.tsx:368-400`), or `createBinding` then connector grants for the review loop (`:321-345`) — with the partial-failure hazards living in the browser. #26 replaces this with **one server-side transactional create**:

```json
POST /api/workspaces/{ws}/agents
{
  "kind": "prompt",
  "name": "Docs assistant",
  "backend": "codex",
  "behavior": {
    "role_name": "docs-assistant",
    "role_create": { "prompt": "...", "prompt_filename": "docs-assistant.md",
                     "description": "...", "task_filter": "..." }        // optional; ensure-if-absent
  },
  "trigger": { "source_kind": "internal", "event_type_patterns": ["internal.task.ready"] },
                                          // or {"source_kind":"cron","schedule":"*/10 * * * *","schedule_timezone":"UTC"}
  "grants": [ { "connector_id": "github", "action": "pulls.comment", "resource_pattern": "repo:o/r" } ],  // scripted templates
  "enabled": true
}
→ 201 { agent record, "bindings": [created binding] }
```

Server-side order and failure semantics (fleet-db offers no cross-call transaction, so this is explicit orchestration + compensation):

1. **Ensure Role** (prompt kinds, when `role_create` present) — already idempotent (`roles` module create/write, precedent `roles/module.go:375-397`). Failure → error out; nothing created.
2. **Create the agent record** — identity first. `ServiceID` minted `agt-…`; duplicate → fleet-db `ErrAlreadyExists` → 409.
3. **Create the binding** with `target_agent_service_id` set at create (the client already sends the field, `infra/fleetdb/platform.go:158`), derived route (the cron/internal `WithDerivedRoute` model), `run_input {roleName, backend}` for prompt agents (unchanged config-by-reference transport). **Failure → compensate: delete the agent record** (fleet-db's guard permits it — zero bindings attached) **and return the binding error.** No orphan identity on the success-visible path. The reverse hazard the task brief names — "binding created but agent row fails" — is **eliminated by ordering**: the agent row always exists before its binding.
4. **Provision grants** (when requested): ensure connector, add grants keyed to the new binding id. Failure → revoke any grants added, delete the binding, delete the agent record, return the error — the "fail fast rather than half-provision" stance the modal already documents (`CreateAgentModal.tsx:300`), now enforced where it belongs.

Residual window: a crash between steps 2 and 3 leaves an "unconfigured" agent — visible in the list, harmless, deletable; no sweeper (it would be more machinery than the failure mode deserves). The modal's own submit becomes one `POST` + navigate to `/ws/{ws}/agents/{agent.id}`; the `onWorkflowActivated` plumbing collapses.

Supervised cards (`lead`/`plan`/`task`/custom-role) keep their existing `POST /agents` agentdef payload verbatim — a body without `kind` (or `kind:"supervised"`) routes to the existing handler unchanged, so `createWorkspaceAgent` (`api/workspace/workspace.ts:310-319`) and its callers need no migration deadline.

---

## 6. Migration + sequencing

### 6.1 Wave A — loomcli-only, build now (pre-Phase-5, alongside #26/WS4a)

| Step | Size | Content |
|---|---|---|
| A1 | **S** | `agt-` id minting + DTO/derivation helpers (`kind`, `enabled`, behavior ref) in a new `webui/handlers/agentrecords` (or grown `agents`) module. No store work — `AgentServiceStore` is complete. |
| A2 | **M** | Unified `/agents` surface: kind-discriminated GET; kind-routed POST with the §5 transaction; PATCH/DELETE/enable/disable for record kinds incl. binding fan-out + the attached-binding 409 guard in `triggerbindings.setEnabled`; `GET /agents/{id}/runs` over the shared `runhistory` helper. |
| A3 | **S** | WS4a alignment: the shared binding-filter run helper + non-driver-rooted envelope (§4.3). Do this *inside* WS4a's task so it is written once. |
| A4 | **S** | Migration ensure-loop at serve start (idempotent, precedent: WS1a's role backfill): for each binding whose driver is an agent-shaped builtin and `target_agent_service_id == ""` — create `AgentService{ServiceID: binding_id, Name: binding.Name or binding_id, Kind: by source_kind, RoleName: from run_input.roleName (parse `source_config_ref`), DesiredState: running iff enabled}`, then PATCH the binding's `target_agent_service_id`. Prompt-agent bindings only (they carry `roleName`); scripted bindings stay `kind:"binding"` until Wave B. Collision-check seed ids against agentdef names. |
| A5 | **S/M** | Frontend: resolver third-branch demotion, sidebar dedupe, `WorkflowAgentDetail` fed by record+bindings; #26 modal switches to the transactional POST. |

**What #26 must build now so nothing is thrown away:** submit to the §5 endpoint (not client-side sequencing); navigate by returned `agent.id`; render gallery cards off the DTO `kind`. **What WS4a must build now:** A3's shared helper and envelope. Both are listed in their own tasks' terms above so the instruction is unambiguous.

### 6.2 Wave B — the justified fleet-db batch (one PR, spec-first)

| Change | Size | Justification |
|---|---|---|
| `AgentService.driver_id` + `driver_version_id`; validation becomes "exactly one of role_name / driver ref" (relaxing `validateAgentServiceReferences`'s unconditional `GetRole`, `storage/platform.go:3830-3833`); validate version∈driver | S/M | Verbatim the companion proposal's "Open Implementation Notes" (`2026-06-07-agent-service-driver-version-proposal.md`); unblocks scripted-agent records. |
| `AgentService.deleted_at` (+ default-exclude filter) | S | Archive-not-erase delete (§3.1); replaces the Wave-A metadata marker. |
| `AgentService.created_by` (actor-stamped) | S | Owner field; precedent `WorkflowSchedule.CreatedBy`. |
| `DriverRun.agent_service_id`: stamped in `dispatchTriggerRouteLeg` from `binding.TargetAgentServiceID`; list filter; loomcli decode + `DriverRunFilter.AgentServiceID` | M | Direct attribution robust to binding churn (§2.2); mirrors the shipped `trigger_binding_id` work (fleet-db `b8ecd01`/`34c9cf3`). |

All fields land in `api/openapi.yaml` **in the same PR** and the loomcli snapshot is re-vendored (`infra/fleetdb/contract_guard_test.go:47-48`) — the Miss-2 discipline, now mechanically enforced. After Wave B: migrate scripted bindings to records (A4's loop extended), and #26's scripted cards switch from binding-proxy to record create.

### 6.3 Phase 5 (unchanged ownership — explicitly NOT this design)

The desired-state controller (restart policy, `MaxInstances`, placement, PTY attach, lease-fenced supervision), migrating Go `agentdef` rows onto the record and retiring the `agents` table with the supervisor, real task-type plane arbitration, and interactive/lead agents as `always_on` services. This doc only guarantees Phase 5 inherits a populated identity table instead of having to invent one.

---

## 7. Risks / blast radius

- **Go-plane coexistence — map at read time, do not dual-write.** Go agents do *not* get an `AgentService` record now: two records per agent is the two-identity-systems hazard instantiated, and nothing consumes the duplicate before the Phase-5 controller. Each agent has exactly one record (agentdef *or* agent-service); the unified GET merges at read time with `kind`. The daemon reconciler reads only `Agents()` — a disjoint fleet-db resource — so record-kind rows are structurally invisible to the supervisor (no spawn risk). Residual: two lifecycle vocabularies (`start/stop` vs `enable/disable`) coexist in the UI until Phase 5 — mitigated by the capability rules in §4.2 (render the verbs the kind carries).
- **Two identity systems during migration (record vs binding-proxy).** The window is one serve boot (A4 runs at start, idempotent) plus the scripted-agent gap until Wave B. Hazards and their single-point mitigations: double sidebar rows → the one dedupe rule (hide attached bindings); split control paths → the 409 guard on attached-binding enable; URL ambiguity → fixed resolver precedence + `agt-` prefix. Scripted agents remaining `kind:"binding"` in the interim is honest UI, not a bug.
- **fleet-db contract seam.** Wave A adds zero fleet-db routes/fields — by design, the whole wave rides shipped surface. Wave B is spec-first with the contract guard as tripwire; strict decode (`request.go:127-129`) means any attempt to skip the batch fails loudly rather than silently.
- **Enable/disable fan-out races.** Fan-out to N bindings is not atomic; a crash mid-disable leaves mixed binding states under a `paused` record. The record is authoritative intent: the same serve-start ensure-loop (A4) reconciles attached bindings' `enabled` to the record's state. Bounded, self-healing.
- **Archive marker in metadata (Wave A only).** `metadata["archived_at"]` is a temporary wedge by this doc's own standards — priced, single-reader (the list filter), removed by Wave B's `deleted_at`. Named here so it cannot quietly become load-bearing.
- **Delete-guard coupling.** Any future code path that hard-deletes agent services must detach bindings first or hit `ErrInvalidTransition` (`postgres/platform.go:402-413`) — a feature (referential integrity), but one that must be documented on the ops purge path so it isn't "fixed" by removing the guard.

---

### Critical Files for Implementation

- /Users/tyson/codebase/code-agents/unified-agents/loomcli/internal/store/platform_store.go (AgentServiceStore/TriggerBinding filter+update seams, lines 131-186, 264-276)
- /Users/tyson/codebase/code-agents/unified-agents/loomcli/internal/webui/handlers/triggerbindings/module.go (delete/revoke, enable guard, run-now, health decorators to reuse)
- /Users/tyson/codebase/code-agents/unified-agents/loomcli/internal/webui/handlers/agents/module.go (the `/api/workspaces/{ws}/agents` surface that becomes unified)
- /Users/tyson/codebase/code-agents/unified-agents/loomcli/internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx (#26 create-flow replacement, lines 300-445)
- /Users/tyson/codebase/code-agents/unified-agents/fleet-db/internal/models/platform.go (AgentService model + Validate, lines 290-350; Wave-B field batch lands here + api/openapi.yaml)
