# Custom Agent Authoring — Implementation Plan

> Status: proposed (plan for review, pre-code)
> Follows: `docs/design/create-agent-redesign.md` (the three-lane agent model + roles module)

## Goal

Let users **author, edit, and template custom agents** from the UI, in both a
**YAML** and a **TypeScript** format, and let a custom agent (e.g. `bug-triage`)
**run on the TypeScript execution leaf** (Phase U) rather than only the Go path.

Decided scope (user, this session):

- **Phased.**
  - **Phase 1 (now):** YAML + **TS-as-config** → the existing Role model →
    `local-task-runner`. Data-driven add/edit/template UI. `bug-triage` runs
    **read-only on the TS leaf**.
  - **Phase 2 (later):** **TS-as-behavior** — a user-supplied custom runner
    module (`ts_entrypoint`), bundled and sandboxed. Outlined here, not built.

## Mental model

Two objects, deliberately kept separate:

| Object | Is | Storage | CRUD today |
|--------|----|---------|-----------|
| **Role** | the *behavior template* — prompt, task_filter, backend, model, effort, read_only, allowed/denied tools, skills | `store.Roles()` | Create / Get / List / **Update** / **Delete** all exist at the store layer (`role_store.go:51`) |
| **Agent** | a *named instance* referencing a role + repos/auto/parent/backend | `store.Agents()` | Full CRUD (`loom agentdef`, webui, `store.Agents().Update`) |

A **"template of agents" = the catalog of Roles** (built-in + custom). "Add from
template" = pick a Role → create an Agent. "Edit an agent" = edit the **Role**
(prompt/filters/tools/runtime). **YAML and TS are two serializations of the Role
model.**

## Current state (grounded)

- **RoleStore is already full-CRUD** — `Create/Get/List/Update/Delete`
  (`internal/store/role_store.go:51-57`), `RoleUpdate` is a partial-update
  payload (`:31`). Implemented in `internal/infra/memstore/role.go` and
  `internal/infra/fleetdb/role.go`. **The HTTP roles module only exposes
  `GET`+`POST`** (`internal/webui/handlers/roles/module.go:41-42`) — no PUT/DELETE.
- **The Role stores `PromptFile` (a path), not the prompt body**
  (`role_store.go:15`). The webui create writes the body to
  `<ws>/.loom/prompts/<file>` and stores the path
  (`roles/module.go:115-122`). → a portable export must **inline** the prompt body.
- **Templates are hardcoded in TS** —
  `internal/webui/frontend/src/components/CreateAgentModal/agentTemplates.ts`
  (`AGENT_TEMPLATES` = planner/task/bug-triage/lead). Not data-driven, no edit.
- **TS execution leaf (Phase U) is wired for `plan`/`task` only** —
  `runPlanDaemon` (`plan.go:168`) and `runTaskDaemon` (`task.go:161`) wrap
  `tsruntime.Invoker(deps.Agent).InvokeNonInteractive(...)`. The **custom-role**
  daemon path `runAgentDaemon` (`agent_cmd.go:131`) calls `cli.InvokeAgent(...)`
  directly — **no `tsruntime` hook**. This is the bug-triage gap.
- **Read-only is *soft* (prompt preamble), not a sandbox** —
  `spawn.go:131` sets `LOOM_READ_ONLY` from `RoleConfig.ReadOnly`;
  `prompts.go:407-410` `ReadOnlyPreamble()` injects a read-only instruction when
  `LOOM_READ_ONLY=="1"`. `RoleConstraints.ReadOnly`/`AllowedTools` are tagged
  *"informational, carried through for downstream use"* (`task_router.go:25-26`).
  There is no per-backend tool-gating wiring beyond the `SetAllowedTools`
  capability stub (`backend_capabilities.go:29`).
- **The TS runner runs at max permission** — `local-task-runner.backendArgs`
  (`:449`) uses `--dangerously-bypass-approvals-and-sandbox` (codex),
  `--dangerously-skip-permissions` (claude), `--approval-mode=yolo` (gemini),
  `--force` (cursor), and **ignores** `LOOM_READ_ONLY`/`*_TOOLS`.

**Consequence for safety:** read-only *parity* with the Go path is cheap (the
preamble already rides in `LOOM_TASK_RUN_PROMPT`). *Hard* read-only (a real tool
gate) would be a genuine improvement over today's Go behavior — tracked as an
optional Phase-1 stretch, not a blocker.

---

## Phase 1

### Workstream A — Role model + CRUD API

**A1. Add two fields to the Role model** (default-safe, back-compat):
- `runtime` — `"go" | "ts"`, default `"go"` (which leaf executes the agent).
- `ts_entrypoint` — `string`, **Phase 2**; validated empty/ignored in Phase 1.

Thread through every layer (this is the field-plumbing the earlier pressure-test
enumerated):
- `internal/domain` Role type
- `internal/store/role_store.go` — `RoleCreate` (`:11`) + `RoleUpdate` (`:31`)
- `internal/infra/memstore/role.go` — Create / Update / clone
- `internal/infra/fleetdb/role.go` — wire struct / `toDomain` / Create / Update
- `roleConfigFromDomain` (`internal/cli/config/project.go:294` **and** the
  supervisor copy) + `MergeRoleConfig`
- `RoleConfig` / `RoleConstraints` (`task_router.go`) so the supervisor can read
  `runtime` at spawn time

**A2. Expose CRUD in the HTTP roles module**
(`internal/webui/handlers/roles/module.go`):
- `PUT /api/workspaces/{ws}/roles/{name}` → `store.Roles().Update` (partial). On
  prompt change, re-write the prompt file (reuse `writeRolePrompt`, `:142`).
- `DELETE /api/workspaces/{ws}/roles/{name}` → `store.Roles().Delete`.
- Keep the idempotent `POST` create. Error mapping already handled by
  `handler.WriteDomainError` (404/400/409/500).
- Guard built-in roles (`plan`/`task`) from destructive edits/delete.

**A3. API client** (`frontend/src/api/workspace/workspace.ts`): add
`updateWorkspaceRole`, `deleteWorkspaceRole`, `listWorkspaceRoles` (the GET list
already exists server-side) + types.

### Workstream B — YAML + TS-config serialization

**B1. One canonical Role schema**, two encoders. The portable definition inlines
the prompt body (not the file path) plus: `name`, `description`, `prompt`,
`taskFilter`, `backend`, `model`, `effort`, `readOnly`, `allowedTools`,
`deniedTools`, `skills`, `maxPriority`, `maxConcurrency`, `maxBudgetUsd`,
`runtime`.

**B2. YAML** (server-side, Go `yaml`): import = parse → `RoleCreate`/`RoleUpdate`
→ existing CRUD; export = Role (+ prompt body read from `PromptFile`) → YAML.
Endpoints:
- `GET .../roles/{name}/export?format=yaml` → text
- `POST .../roles/import` (body: yaml text) → Create-or-Update

**B3. TS-as-config (Phase 1 = config only, no user-code execution).** A typed
helper `defineAgentTemplate({...})` exported from `@loom/sdk`. The `.ts` form is
a **pure object literal**:
- **Export** = codegen a `.ts` from the Role JSON.
- **Import** = **static parse** of the exported object literal (or accept JSON
  the editor produced) → same Role model. *No `eval`, no bundling, no running
  user TS in Phase 1* — that is explicitly Phase 2 (`ts_entrypoint`).

This makes "YAML format as well as TypeScript format" two views of one Role,
which is the symmetric, safe Phase-1 shape.

### Workstream C — bug-triage on the TS leaf

**C1. Unify the daemon leaf.** Factor the three near-duplicate daemon-mode
handlers — `runPlanDaemon` (`plan.go:130`), `runTaskDaemon` (`task.go`),
`runAgentDaemon` (`agent_cmd.go:131`) — into **one** handler that always:
`tsruntime.Invoker(deps.Agent).InvokeNonInteractive(worktree, prompt, name,
shutdown, collector)` + `applyTaskRunnerResult` + `applyLeafPatchBack`. This
brings custom roles onto the same path as plan/task instead of adding a 3rd
special case (altitude). Default `runtime:"go"` → behavior unchanged.

**C2. Per-role runtime selector.** In `spawn.go` (`appendRoleEnv`/
`appendRoutingEnv`), translate `RoleConfig.Runtime=="ts"` →
`LOOM_DAEMON_LEAF=ts` for that spawned agent (and `LOOM_DAEMON_LEAF_RUNNER` from
`ts_entrypoint` in Phase 2). Replaces today's process-global env knob with a
role property.

**C3. Read-only parity (cheap).** Confirm the custom-role `promptGen`
(`makeCustomPromptGen`, `agent_cmd.go:256`) includes `ReadOnlyPreamble`
(`prompts.go:407`) when the role is read-only; if not, add it. Since the prompt
flows verbatim via `LOOM_TASK_RUN_PROMPT`, read-only `bug-triage` then behaves on
the TS leaf exactly as on the Go leaf (soft enforcement).

**C4. (Optional stretch) Hard read-only on the TS leaf.** Teach
`local-task-runner.backendArgs` (`:449`) to read `LOOM_READ_ONLY` and drop the
`--dangerously-*` flags + add per-CLI deny (claude `--disallowedTools Write Edit
Bash …`, codex read-only sandbox mode). A real improvement over the Go path's
soft enforcement; can land after C1–C3.

### Workstream D — UI: data-driven gallery + add/edit

**D1. Data-driven catalog.** Turn `agentTemplates.ts`'s `AGENT_TEMPLATES` into
**built-in seed data**; the picker renders built-ins **merged with `GET
/roles`** (saved custom roles). Built-ins stay as the bootstrap defaults.

**D2. `CreateAgentModal`** = "instantiate from a template." Gallery lists
built-ins + custom roles; selecting a custom role creates an agent referencing it
(the `ensureRole` step is a no-op for already-saved roles).

**D3. New "Agent Templates" management surface** (sibling to `AutomationsModal`,
reuse `AetherModal`): list roles; **Add** (form + a raw YAML/TS editor toggle) →
POST/import; **Edit** (load role → editor → PUT); **Delete**. Includes the
`runtime` (Go/TS) toggle.

---

## Phase 2 (outline — not in this build)

- **`ts_entrypoint`**: a user-supplied TS runner module (shape of
  `github-review-task-runner.ts`) that *is* the agent's behavior.
- **Bundle extensibility**: `BuildBuiltinBundle` (`tsruntime.go:262`) currently
  builds a fixed builtin spec set; Phase 2 needs a per-workspace bundle that
  includes registered custom entrypoints.
- **Sandboxing**: running user TS safely — resource limits, no host-secret access
  beyond the task-run lease. The leak-probe pattern in `daytona-task-runner.ts`
  (`:184`) is a reference.
- Supervisor sets `LOOM_DAEMON_LEAF_RUNNER=<custom entrypoint>` via C2.

---

## Recommended sequencing

1. **Increment 1 — Workstream C** (bug-triage on the TS leaf), gated by
   `runtime` (default `go` → zero behavior change). Smallest, proves the leaf
   path, independently shippable.
2. **Increment 2 — Workstream A** (new fields + PUT/DELETE).
3. **Increment 3 — Workstream B** (YAML + TS-config serialization).
4. **Increment 4 — Workstream D** (UI add/edit/template).
5. Phase 2 later.

## Risks / open questions

- **Daemon-leaf unification touches plan/task** (well-tested paths) — regression
  risk; guard with the existing Phase U / U0 conformance tests + keep
  `runtime:"go"` the default.
- **TS-config in Phase 1 is static-parse only** (no user-code execution) — confirm
  that satisfies "TypeScript format" for now (arbitrary TS = Phase 2).
- **Hard read-only (C4)** — ship soft-parity first; decide whether hard gating is
  required for bug-triage before exposing `runtime:"ts"` broadly.
- **Built-in role protection** — `plan`/`task` must be non-deletable / minimally
  editable.

## Testing

- **Go unit**: unified daemon leaf (`runtime:"go"` unchanged; `runtime:"ts"`
  routes through `RunBundledTaskRunner`); roles PUT/DELETE handler tests; field
  round-trip across memstore + fleetdb.
- **TS**: `local-task-runner` read-only arg tests (if C4).
- **E2E (Podman, `make local-mode-up`)**: create a `bug-triage` role with
  `runtime:"ts"`, run live, assert transcript captured + read-only preamble
  present + no file writes.
- **UI**: add / edit / delete / template-instantiate flows.
