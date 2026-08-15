# Scout-as-Role — Design Validation Research

**Status:** Research (validation of design section B2, no new design)
**Date:** 2026-08-15
**Scope:** Validates `docs/design/2026-08-14-scout-architecture-refactor.md` §B2 ("the
discriminator is the role") against loomcli worktree `feat/ticket-recommender-v1` and its
sibling fleet-db checkout (cited as `fleet-db:<path>`). Every claim cites its source;
verdicts per question are at the end of each section, the overall verdict at the bottom.

---

## 1. Role model fit — RoleKind values and validation

**Values.** loomcli defines exactly two kinds: `interactive` and `worker`
(`internal/domain/role.go:10-15`). fleet-db's closed vocabulary is `""`,
`"interactive"`, `"worker"` (`fleet-db:internal/models/role.go:24-28`).

**Where validated.**

- **fleet-db, strictly, on both write paths**: create via `Role.Validate` —
  `"role kind must be one of interactive, worker"`
  (`fleet-db:internal/models/role.go:299-301`) — and PATCH via an explicit re-check
  because the update path writes hash fields directly
  (`fleet-db:internal/storage/role.go:170-172`). Like `AgentService.Kind`, fleet-db
  rejects unknown enum values.
- **loomcli, at two entry points only**: `loom role add/set` —
  `"kind must be interactive or worker"` (`internal/cli/role/role_cmd.go:434-441`) —
  and webui agent creation (`internal/webui/svcimpl/agent_service.go:626-631`).
  The domain type itself never validates; `ResolveRoleKind` passes any non-empty
  string through after lowercasing (`internal/domain/role.go:254-264`).

**Behavior of an unknown kind in each consumer.** Every kind consumer in loomcli
compares against `RoleKindInteractive` only (roles module
`internal/webui/handlers/roles/module.go:261-304`; terminal sessions
`internal/webui/handlers/terminal/agent_session.go:202,253,263,392`; supervisor
`internal/cli/daemon/supervisor/role.go:54-57`; project config
`internal/cli/config/project.go:150`; svcimpl `agent_service.go:394,500`). A
hypothetical `"scripted"` kind would therefore behave exactly like `worker`
everywhere in loomcli — but **would never get that far**, because fleet-db rejects
it at create and update.

**Which kind for scout.** `worker` fits: it is non-interactive, and the roles module's
worker path (PromptFile-backed, see §4) is the editable one. A new enum value buys
nothing — B2's discrimination is by role *name* through the compiled catalog, not by
kind — and would require a lockstep fleet-db companion change plus the two loomcli
closed-vocab validators.

**Verdict: WORKS AS DESIGNED** with `kind: worker`. Introducing a new kind value
would BLOCK until a fleet-db companion ships; the design doesn't need one.

---

## 2. AgentService.role_name reality

**fleet-db persists and returns it.** `AgentService.RoleName`
(`fleet-db:internal/models/platform.go:296`), marshaled/unmarshaled in redis storage
(`fleet-db:internal/storage/platform.go:2990,3022`), filterable on list
(`fleet-db:internal/storage/platform.go:3832`, `platform_types.go:82`), updatable via
`*string` patch (`platform_types.go:92,683-684`), and present in the openapi
create/update schemas (`fleet-db:api/openapi.yaml:7920-7997`).

**Exactly-one behavior reference — the load-bearing constraint.** fleet-db validation:

> `agent service behavior must use exactly one of role_name or driver ref`
> (`fleet-db:internal/models/platform.go:343-352`)

loomcli's memstore mirrors it verbatim
(`internal/infra/memstore/agent_service.go:292-299`). An AgentService **cannot**
carry both `role_name` and `driver_id`/`driver_version_id`. So "scout resolves its
machinery from role_name" means the scout service record must *switch* from driver-ref
to role_name — a single update patch that sets `RoleName` and clears both driver
fields (both stores re-validate the merged record:
`fleet-db:internal/storage/platform.go:297-311`,
`internal/infra/memstore/agent_service.go:288-299`).

**Role existence is validated server-side.** On agent-service create AND update,
fleet-db resolves the role and fails with not-found if absent
(`fleet-db:internal/storage/platform.go:3860-3865`, called at `platform.go:249,307`);
memstore does the same (`internal/infra/memstore/agent_service.go:255-256`).
**Seeding order is therefore forced: role record first, service patch second.**

**loomcli carries it end-to-end.** `domain.AgentService.RoleName`
(`internal/domain/platform.go:153`); `store.AgentServiceCreate/Update` → memstore
(`internal/infra/memstore/agent_service.go:73,367-368`); fleetdb adapter sends
`role_name` on create/update and filters on list
(`internal/infra/fleetdb/agent_service.go:24,44,84-85,114,133`); webui DTO exposes it
as `behavior.roleName` (`internal/webui/handlers/agentservices/module.go:74,179`).

**scout_provision.go today does the opposite.** It provisions with
`DriverID`/`DriverVersionID` (`internal/workflows/scout_provision.go:65-69`) and
**actively repairs `RoleName` back to empty** if anyone sets it
(`scout_provision.go:98-101`). Two knock-ons of flipping to role_name:

1. The workflows-POST special case reads `svc.DriverID` to create the run
   (`internal/webui/handlers/workflows/module.go:155-160`); with a role_name-only
   service that is `""` and run creation breaks. The handler must resolve the driver
   from the workflow name (as `resolveWorkflowDriverID` already does for other
   builtins, `module.go:203-220`) — which B1's catalog provides.
2. The webui derives `kind: "scripted"` from the driver ref and `"prompt"` from
   role_name (`internal/webui/handlers/agentservices/module.go:221-229`); scout would
   silently reclassify as "prompt". B2 already plans to retire this derivation — it
   must land in the same change, not after.

**Verdict: NEEDS CHANGE** (not a blocker). role_name is real and validated, but the
design must state: (a) role seeded before the service patch; (b) the service patch
sets role and clears the driver ref atomically (exactly-one rule); (c) the
workflows-POST driver resolution and `deriveAgentServiceKind` are updated in the
same commit as the provisioning flip.

---

## 3. Side effects of a scout role record existing

Every consumer of the roles list (`Roles().List` call sites, non-test):

| Surface | Source | Effect of a scout role row |
|---|---|---|
| **AgentsPage roster** | agent services + live agents only — `useAgentServices` (`internal/webui/frontend/src/views/AgentsPage.tsx:128-148`), rows built purely from `AgentServiceDTO` (`components/WorkspaceTree/agentSectionAutomationRows.ts:15-25`) | **None.** Roles are not a roster input. |
| **Daemon config** | store roles become a `name → RoleConfig` lookup map (`internal/cli/serve/daemonwire/daemon.go:264-281`; `internal/cli/config/project.go:243-252`) | An extra map entry. Agents spawn from `Agent` records, not per role — nothing spawns. |
| **workspacemgr seeding** | seeds only `plan`/`task`/`lead` (`internal/cli/serve/workspacemgr/workspace_store.go:479-506`) | Unaffected; scout seeding is additive. |
| **`loom role list` / `workspace status`** | `internal/cli/role/role_cmd.go:183-201`; `internal/cli/workspace/workspacev2_cmd.go:222-229` | Scout appears — expected, arguably desirable. |
| **`loom workspace doctor`** | flags agents referencing unknown roles (`internal/cli/workspace/ops_cmd.go:341,539-545`) | A scout role *removes* a potential warning. |
| **roles webui GET matrix** | `internal/webui/handlers/roles/module.go:116-140` | Scout appears in the Roles list — this is the invited surface (RolePromptCard). |
| **monitor data source** | roles fetched to label assignments (`internal/cli/serve/metricscmd/monitor_store_data_source.go:196,227`) | Label lookup only. |

Two second-order interactions, neither a silent surprise:

- **Agent-creation collision**: creating an *interactive* webui agent auto-creates a
  role named after it (`internal/webui/svcimpl/agent_service.go:451-489`). Once a
  worker-kind scout role exists, an agent named "scout" fails loudly:
  `"role %q already exists and is not interactive"` (`agent_service.go:497-505`).
- **Worker-agent opt-in**: a user *can* deliberately create an `Agent` with
  `role_name: scout` (worker roles must pre-exist, `agent_service.go:459-461`), and
  the daemon would then run a task-loop agent with scout's prompt file
  (`internal/cli/daemon/supervisor/role.go:30-51`). User-initiated, not uninvited,
  but worth a line in the design's consequences.

**Verdict: WORKS AS DESIGNED.** No surface picks up a scout role uninvited; the only
place it appears automatically is the Roles list, which is the point.

---

## 4. The editability matrix

**"Managed"/"builtin" are hardcoded name lists, and scout is on neither.**
Builtin = `plan`, `task` (`internal/webui/handlers/roles/module.go:318-325`) → PATCH
returns **405** `builtin_role` (`module.go:178-182`). Managed = `pr-reviewer` only
(`module.go:37,327-329`) → **409** `managed_role` (`module.go:183-186`). A seeded
scout role reaches the editable path.

**But the projection branch decides editability, and worker-inline is a trap.**
`projectRole` (`module.go:256-305`):

- interactive + inline `Prompt` → `sourceKind: inline`, editable (`module.go:284-286`)
- any kind + `PromptFile` readable in-workspace → `sourceKind: file`, **editable**
  (`module.go:291-295`)
- **worker + inline-only prompt → `sourceKind: file` with error "Prompt file is not
  configured", `editable: false`** (`module.go:304`)

The PATCH path confirms the worker convention: editing a worker role always publishes
a content-addressed PromptFile and clears the inline prompt (`module.go:209-222`,
via `roleprompts.Publish` → `<workspaceDir>/.loom/prompts/<role>.<sha6>.md`,
`internal/roleprompts/roleprompts.go:33-46`).

**Which seeding shape is right: PromptFile.** A create-only seed with inline
`Prompt` would render scout **uneditable** in RolePromptCard (the worker-inline
branch above) — defeating B2's goal — unless `projectRole` is also changed. Seeding
via `roleprompts.Publish` matches the module's own write path, is idempotent for
identical content (`roleprompts.go:35,66-85`), and sidesteps fleet-db's 100KB inline
cap (`MaxRolePromptBytes`, `fleet-db:internal/models/role.go:57,393-398`). Cost: the
seeder needs the workspace dir on disk — available in serve via
`storeadapter.ResolveOrHealWorkspacePath`, exactly as the module resolves it
(`module.go:61-63`). RolePromptCard already renders for any service with
`behavior.roleName` and retires the scripted-prompt-note automatically
(`internal/webui/frontend/src/components/AgentServiceDetail/AgentServiceDetail.tsx:358-373`).

**Verdict: NEEDS CHANGE** (one word of the design): the seed must be
worker-kind **with a published PromptFile**, not an inline prompt — or `projectRole`
must learn worker-inline editing. PromptFile is the smaller, convention-following
change. Everything else (405/409 buckets, `sourceKind: file`) works as designed.

---

## 5. Prompt delivery path

**Where the preamble lives today.** Entirely hardcoded in the leaf:
`analysisPrompt` (`internal/workflows/builtin/scout-task-runner.ts:268-344`)
assembles, in order: fixed intro ("You are the Scout…", lines 274-283) →
machine-gathered repo seeds (284-286, built at 349-364) → prior agents.md + journal
(287-296) → the `--- TASK ---` rules block (297-342). The role-editable "preamble"
maps to the intro + TASK sections; the seeds/journal stay leaf-assembled.

**How input reaches the leaf today** (the opaque-payload contract):

1. `POST /api/workspaces/{ws}/workflows/{name}` (`internal/webui/handlers/workflows/module.go:42,145-201`)
   stores the raw JSON body as the DriverRun payload (4 MiB handler cap, `module.go:25`).
2. Executor delivers it via `LOOM_FLUE_INVOKE_PAYLOAD` (`internal/driver/executor.go:822`;
   read at `internal/workflows/builtin/scout.ts:16-23`).
3. `scout.ts` builds the leaf task input (`scout.ts:66-70,81-86,112-119`) and calls
   `loom.taskRuns.request`, whose Go server side is `RequestTaskRun`
   (`internal/driver/task_request.go:223`, parent run verified at `task_request.go:475`).
4. The bridge hands the request to the leaf as `LOOM_TASK_RUN_REQUEST_JSON`
   (`internal/driver/task_bridge.go:684`, `internal/driver/bundled_runner.go:117`);
   the leaf reads `request.input` (`scout-task-runner.ts:96-100`).

**Who should read the role record.** The POST handler alone is the wrong injection
point: cron-fired runs never pass through it — `CronScheduler.dispatchTick` fabricates
a `{"tick": …}` payload and routes it directly (`internal/trigger/cron.go:227-246`).
The workflow sandbox has no roles read op in the SDK. The one choke point both paths
share is the Go side of `taskRuns.request` (`RequestTaskRun`), which already loads the
parent DriverRun → its `AgentServiceID` → service → `role_name` → role prompt
(PromptFile read via `roleprompts.ReadValidated`, `internal/roleprompts/roleprompts.go:91-127`).
The leaf change is then: prefer `input.analysisPreamble` (name illustrative) over the
embedded strings, keeping the embedded template as fallback exactly as B2 says.

**Size limits.** fleet-db API request bodies cap at **1 MiB**
(`fleet-db:internal/api/json.go:7`); `DriverRun.Payload` and `TaskRun.Input` have no
model-level byte caps (only JSON validity, `fleet-db:internal/models/platform.go:1016-1018`;
`TaskRun.Validate` checks no input size). Role inline prompts cap at 100KB
(`fleet-db:internal/models/role.go:57`). One operational caveat: the whole task
request rides in a **single env var** (`task_bridge.go:684`); Linux caps one env
string at 128 KiB (`MAX_ARG_STRLEN`), so a ~100KB prompt plus the rest of the request
JSON is uncomfortably close. Worth a stated prompt budget (e.g. ≤64KB) or moving
delivery off env if prompts grow.

**Verdict: NEEDS CHANGE** (delivery point, not concept): task-payload delivery works,
but the design must name `RequestTaskRun` (or equivalent shared choke point) as the
injection site — POST-handler-only injection silently misses every cron run — and
should state a prompt size budget for the env-var hop.

---

## 6. Collisions

**"scout" / "epic-runner" as role names: unclaimed.** The only non-test `RoleCreate`
call sites are `loom role add` (`internal/cli/role/role_cmd.go:154`), workspace
seeding of `plan`/`task`/`lead` (`internal/cli/serve/workspacemgr/workspace_store.go:479-506`),
interactive-agent auto-roles named after the agent
(`internal/webui/svcimpl/agent_service.go:470`), and `pr-reviewer`
(`internal/webui/handlers/prreview/reviewer.go:33,449`). No fixture, seed, or roster
default creates a role named "scout" or "epic-runner".

**Adjacent namespaces that do use "scout"** (no conflict, different keyspaces): the
AgentService ID (`internal/workflows/scout_provision.go:15`) and the journal
handler's `serviceID != "scout"` special case
(`internal/webui/handlers/agentservices/module.go:392`).

**Uniqueness enforcement.** Per-workspace: fleet-db's create script returns
`ErrAlreadyExists` on a name hit within the workspace's role set
(`fleet-db:internal/storage/role.go:83-92`); memstore likewise
(`internal/infra/memstore/role.go:35-37`). The name pattern admits both names
(`fleet-db:internal/models/role.go:13`).

**Workflow↔role name identity.** Workflow names are the exact strings `"scout"` and
`"epic-runner"` (`internal/workflows/workflows.go:23-28`), so a catalog keyed by role
name can serve the POST `/workflows/{name}` reverse lookup with no mapping table.

**The real collision risk is temporal, not present:** a user can create a role named
"scout" *before* the seed ships (CLI `role add`, or an interactive agent named
"scout" auto-creating an interactive role, `svcimpl/agent_service.go:451-489`).
A strictly create-only seed would then adopt a foreign role — possibly
`kind: interactive` with an arbitrary prompt — and bind catalog machinery to it. The
seed needs a kind check/repair on adopt (pr-reviewer's reconcile already models this:
kind repair + inline-prompt clearing while preserving what should be preserved,
`internal/webui/handlers/prreview/reviewer.go:468-495`; B3's `Diff` callback is the
natural home). "Create-only" must mean *prompt*-create-only, not shape-blind.

**Verdict: WORKS AS DESIGNED**, with one required nuance: define adoption semantics
(kind repair, prompt preserved) for a pre-existing role bearing a catalog name.

---

## 7. Role deletion

Delete exists at every layer:

- loomcli store interface: `RoleStore.Delete` (`internal/store/role_store.go:74`);
  fleetdb adapter `DELETE /api/v1/{ws}/roles/{name}` (`internal/infra/fleetdb/role.go:249-251`);
  memstore (`internal/infra/memstore/role.go:176-187`); CLI `loom role remove`
  (`internal/cli/role/role_cmd.go:262`).
- fleet-db API: `DELETE /api/v1/{workspace}/roles/{name}` gated by `PermRoleDelete`
  (`fleet-db:internal/api/roles.go:109,281-288`).

**The referential rule already exists on both sides — it is not new work:**

- fleet-db `DeleteRole` refuses (`ErrInvalidTransition`) when any *non-deleted* agent
  service references the role (`fleet-db:internal/storage/role.go:367-372`;
  soft-deleted services excluded by `agentServiceMatches`,
  `fleet-db:internal/storage/platform.go:3822-3825`).
- memstore mirrors: `services.hasRole` over `DeletedAt == nil` services
  (`internal/infra/memstore/role.go:176-179`, `internal/infra/memstore/agent_service.go:232-241`).

Note the guard is generic — it protects *any* referenced role, not only scripted
ones. The design's "a scripted role cannot be deleted while instances reference it"
is satisfied for free; deleting a scripted role with **zero** instances remains
possible (roles module exposes no DELETE anyway, `internal/webui/handlers/roles/module.go:67-74`
— only CLI/API can), after which the reconciler would re-seed it.

**Verdict: WORKS AS DESIGNED — already implemented.** The design doc can cite it as
existing behavior rather than planned work.

---

## 8. Verdict summary

| # | Question | Verdict |
|---|---|---|
| 1 | Role model fit | **WORKS AS DESIGNED** — use `kind: worker`; no new enum (a new value would need lockstep fleet-db + 2 loomcli validators for zero benefit). |
| 2 | role_name reality | **NEEDS CHANGE** — exactly-one behavior-ref rule forces the service to *swap* driver-ref→role_name in one patch; role must exist first (server-validated); workflows-POST driver resolution + `deriveAgentServiceKind` must change in the same commit. |
| 3 | Side effects of the role record | **WORKS AS DESIGNED** — no uninvited surfaces; roster/daemon/seeding untouched. |
| 4 | Editability matrix | **NEEDS CHANGE** — seed must publish a **PromptFile** (`roleprompts.Publish`), not an inline prompt, or worker-inline roles render uneditable ("Prompt file is not configured"). |
| 5 | Prompt delivery | **NEEDS CHANGE** — inject at the shared `RequestTaskRun` choke point, not the POST handler (cron runs bypass it); state a prompt size budget (env-var hop, Linux 128KiB/string). |
| 6 | Collisions | **WORKS AS DESIGNED** — names unclaimed, uniqueness enforced, workflow/role strings identical; but define adoption semantics for a pre-existing user role with a catalog name (kind repair, prompt preserved). |
| 7 | Role deletion | **WORKS AS DESIGNED** — delete + referential guard already exist end-to-end (fleet-db `storage/role.go:367-372`, memstore `role.go:176-179`). |

**Overall: the design is sound — nothing blocks it.** Minimal corrections the
refactor plan needs:

1. **Provisioning order + atomic swap** (Q2): seed role → patch service setting
   `role_name` and clearing `driver_id`/`driver_version_id` in one update; update the
   workflows-POST special case to resolve the driver via the catalog's workflow name,
   and retire `deriveAgentServiceKind` in the same change.
2. **PromptFile seed** (Q4): the create-only seed writes the preamble through
   `roleprompts.Publish` and sets `prompt_file`, matching the roles module's worker
   PATCH convention; requires the workspace dir at seed time (serve has it).
3. **Injection site** (Q5): role-prompt → task-input injection lives in
   `driver.RequestTaskRun` (covers manual *and* cron dispatch); leaf prefers the
   payload preamble with the embedded string as fallback; cap the prompt budget well
   under 128KiB.
4. **Adoption semantics** (Q6): on seeing an existing role named "scout"/"epic-runner",
   repair kind to `worker` (and clear a conflicting inline prompt into a file) but
   never overwrite prompt content — "create-only" applies to the prompt, not the shape.
5. **Documentation nit** (Q7): the referential-deletion rule is existing fleet-db +
   memstore behavior; cite it instead of scheduling it.
