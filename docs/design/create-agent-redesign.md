---
name: Create Agent Redesign
overview: "Redesign agent creation around the THREE agent lanes that actually exist in the codebase — interactive Lead, supervised-worker templates, and trigger/workflow templates — surfaced as a single kind-tagged template gallery. Phase 1 ships the UX shell on zero new backend (Lead + Planner + Task) and lands the user in the lead's terminal on create. Phases 2–3 add the one backend keystone (role-create API) and the workflow/trigger surface that unlock Bug-triage and Code-review templates."
todos:
  # ── Phase 1 — zero-backend UX shell (build now) ───────────────────────────
  - id: template-descriptor
    content: Define a kind-tagged AgentTemplate descriptor ('lead' | 'builtin-role' | 'custom-role' | 'workflow'); v1 seeds lead/plan/task only
    status: pending
  - id: modal-gallery
    content: Rebuild CreateAgentModal as a two-section template gallery (Background grid Planner/Task + Lead card); replace seg control with derived role state from a single defaultRole prop
    status: pending
  - id: lead-open-terminal
    content: Add lead-gated navigate() to App.tsx CreateAgentModal onSuccess (~L1525) so creating a lead lands in /ws/:id/agents/:name terminal
    status: pending
  - id: styles
    content: Section headers, card grid, selected/hover/divider states in CreateAgentModal.module.css (reuse existing Aether tokens)
    status: pending
  - id: tests
    content: Update CreateAgentModal tests to card-based selection with stable testids; add lead-creates-then-navigates test; keep submit/validation coverage
    status: pending
  - id: aether-wide
    content: (OPTIONAL) Only widen the dialog if 480px proves too tight — prefer dropping the AetherModal change entirely
    status: pending
  # ── Phase 2 — the backend keystone (unlocks custom supervised templates) ──
  - id: role-api
    content: Add a webui role-create + prompt-content-write API (today loom role add is CLI-only); this single endpoint unlocks ALL custom supervised templates
    status: pending
  - id: bug-triage-template
    content: Seed a bug-triage template (prompt file + custom role + task_filter) as the first custom-role gallery entry
    status: pending
  # ── Phase 3 — workflow/trigger surface (unlocks event-driven templates) ───
  - id: workflow-start-ui
    content: De-hardcode epic-runner; add a generic workflow-start surface + input collection (frontend is currently wired only to EPIC_RUNNER_WORKFLOW_NAME)
    status: pending
  - id: trigger-binding-ui
    content: Trigger-binding create/enable UI + HTTP write API (bindings are CLI-only today) so "Code review on PR" is self-serve
    status: pending
isProject: false
---

# Create Agent Redesign — Three Lanes, Phased

> **Status of this rewrite.** The original proposal was a single-screen modal reskin that treated Lead / Plan / Task as peer roles. Verification against the codebase (four deep probes) showed the product reality is richer and the original made a few factual errors. This version corrects them and reframes the work around the three agent lanes that genuinely exist, phased by cost.
>
> **Current implemented contract (2026-07-25).** The gallery has since grown
> to 12 clean-workspace templates across Behavior, Interactive, and Advanced
> sections. See [Agent Creation Templates](../product/agent-creation-templates.md)
> for the current Gherkin contracts and proof-status boundary. The phased
> proposal below remains a historical design record.

---

## What changed from the original proposal (and why)

| Original claim | Reality (evidence) | Consequence |
| --- | --- | --- |
| Submit payload is `{ name, role_name, backend, cross_repo, repos }` | Actual payload includes a hardcoded `auto: false` (`CreateAgentModal.tsx:123-133`) | Doc-level fix; `auto` is also **not** the supervision gate (see below) |
| Lead is a peer role you create here; "one lead per epic" | A conversational lead **already works with no epic** (`loom lead` PTY, ticket ops via `loom data`; `assignment_context.go:40`). "One lead per epic" is **not enforced** anywhere | Lead belongs in the create flow — but as its own lane, not a peer role |
| `defaultRoleName: 'task' \| 'plan'` + add `defaultKind?: 'lead'` | Two props that can contradict | Collapse to a single `defaultRole?: 'lead' \| 'plan' \| 'task'`, derive kind internally |
| Background = two fixed roles (Plan/Task) | Background spans **two different engines** (supervised worker vs flue workflow); roles are extensible via custom Role + prompt | Model templates as a `kind`-tagged catalog, not two hardcoded cards |
| Placeholder "switches with selection" preserves behavior | Placeholder is currently hardcoded `"planner"` (`CreateAgentModal.tsx:197`) | Net-new behavior, fine — just not a "preserve" |
| Widen to 560px via new AetherModal prop | `AetherModal` is shared by 3 modals; 480px likely fits once Lead isn't a co-equal section | Make the widening opt-in and optional; prefer dropping it |

---

## The three lanes (the core model)

"Background agent template" is not one thing. It is three lanes on two engines, and the named templates split across them:

| Lane | Templates | Entity | "Runs automatically" engine | Create surface today |
| --- | --- | --- | --- | --- |
| **1 · Interactive Lead** | Lead | `domain.Agent` role=`lead` | none — a `loom lead` PTY you talk to | `POST /agents` ✓ |
| **2 · Supervised worker** | **Planner**, Task | `domain.Agent` (agentdef) + built-in role | daemon supervisor → `loom plan/task` task-poll loop | `POST /agents` ✓ (roles seeded) |
| **2b · Supervised (custom)** | **Bug triage** | agentdef + custom Role + prompt file | same supervisor → `loom agent --prompt … --task-filter …` | Role/prompt **CLI-only** |
| **3 · Workflow / trigger** | **Code review** (on PR) | flue `.ts` driver + `TriggerBinding` | driver executor (queued `DriverRun`) + webhook/cron/internal triggers | workflows API ✓ backend; **no FE, no binding API** |

**Critical structural fact:** Lanes 1 & 2 are the *same* `POST /agents` entity (the agentdef — `loom agentdef add` and the modal call identical `store.Agents().Create`). **Lane 3 is a separate surface** (workflow driver + trigger binding — not an agent row). A unified gallery must route across both. `code-review` is not an agentdef; `planner`/`bug-triage` are not workflows.

```
kind: 'lead'          → POST /agents (role=lead) + navigate to terminal      [Phase 1]
kind: 'builtin-role'  → POST /agents (role=plan|task)                        [Phase 1, zero backend]
kind: 'custom-role'   → ensure Role+prompt (NEW api) → POST /agents          [Phase 2]
kind: 'workflow'      → ensure workflow driver → create TriggerBinding (NEW) [Phase 3]
```

---

## Phase 1 — zero-backend UX shell (build now)

Ships the whole gallery model and the locked Lead behavior **without any backend change**. `plan`/`task`/`lead` roles are seeded into every workspace (`workspace_store.go:479`), so Planner/Task/Lead are creatable today.

### 1a. Template descriptor

Add a small local descriptor (same folder, not shared yet). v1 only emits the three lanes that work with zero backend:

```ts
type AgentTemplate = {
  id: string;            // 'lead' | 'planner' | 'task'
  kind: 'lead' | 'builtin-role';
  roleName: 'lead' | 'plan' | 'task';
  title: string;         // "Lead" | "Planner" | "Task worker"
  description: string;
  defaultName: string;   // name-field placeholder
};
```

The `kind` field is the seam: Phase 2 adds `'custom-role'`, Phase 3 adds `'workflow'`. The gallery renders uniformly; only `onSubmit` routing differs.

### 1b. Modal layout — two sections

- **Background section** — a card grid: **Planner** (`plan`), **Task worker** (`task`). Supervised workers that run automatically.
- **Lead section** — one full-width card below a divider: **Lead** — an interactive agent you talk to to create, plan, and triage tickets.
- **Shared config** (unchanged): Name (placeholder follows selection), AI Backend (`useBackends()`), Repos/Worktrees chips.

State: replace `ROLE_OPTIONS` seg control with a single `selectedTemplate` (the chosen descriptor). Derived `role_name` = `template.roleName`. Seed from a single **`defaultRole?: 'lead' | 'plan' | 'task'`** prop (collapses the original's `defaultRoleName` + `defaultKind`); default `'task'`. Update the 2 call sites (`App.tsx:1521` onboarding → `'plan'`, `PRReviewWorkspace.tsx:380` → `'task'`).

Cards: entire card is a `button` with `aria-pressed`, descriptive `aria-label`, and a **stable `data-testid`** (e.g. `agent-template-plan`) so tests couple to identity, not copy. Reuse existing Aether/sidebar tokens (`.subgroupHeader`, `.segOption[data-active]`, `.repoChip`).

Invariant: card → `role_name` must emit canonical `plan`/`task`/`lead` (never the display aliases `planner`/`worker`).

### 1c. Lead "create + open terminal" (LOCKED)

Backend lifecycle is 100% present — agent record created synchronously on `POST /agents`; the `term_<uuid>` tab, the `lead-<uuid>` orchestration session, and the PTY are all created lazily on terminal attach; leads intentionally get no worktree (`agent_service.go:386-388`) and that's fine (`agentLaunchCwd` tolerates it).

**The only change:** `App.tsx` `onSuccess` (~L1525) currently does close-modal + `upsertWorkspaceAgent` + toast + refetch, and stops. Add, gated on `isLeadRole(agent.role_name)` (`utils/agentRole.ts:8-11`):

```ts
handleAgentClick(agent.name); // App.tsx:780 — closeAllPanels() + navigate('/ws/:id/agents/:name')
```

Navigating to `/ws/:id/agents/:name` makes `AgentsPage → AgentDetailMain → useSessionSeeding → ensureAgentTerminalSession` auto-connect the terminal. `navigate`, `workspaceId`, and `handleAgentClick` are all already in scope at that JSX site. Decide whether to navigate in the onboarding branch too, or only the normal `else` branch. Leave `PRReviewWorkspace` onSuccess (a `task` assign-reviewer flow) unchanged.

> Note (verify in practice): supervision is gated by role + `DesiredState`, **not** by `auto` (`project.go:124-135`; the modal's hardcoded `auto:false` is cosmetic). So a created Planner with default `DesiredState` should be picked up by the daemon reconciler next tick — provided the daemon is running and a backend CLI is on PATH. Confirm "create Planner → it starts claiming tasks" end-to-end.

### 1d. AetherModal width — optional

With Lead no longer a co-equal third segment, the content (2 cards + 1 card + 3 fields) likely fits the existing `max-width: 480px`. **Prefer dropping the `aether-wide` change** to keep the shared modal untouched. Only add an opt-in `dialogClassName` passthrough if a 480px mockup proves too tight.

### 1e. Tests

- Card selection updates `aria-pressed`; derived `role_name` on submit for plan/task/lead (select by `data-testid`).
- `defaultRole="plan"` pre-selects Planner; omitted defaults to Task worker.
- **New:** creating a lead calls navigation to `/ws/:id/agents/:name`; creating plan/task does not.
- Keep existing repo-scope / validation / submit-payload (`auto:false`) coverage. ~5 existing tests select roles by button text — migrate to testids.

**Files:** `CreateAgentModal.tsx`, `CreateAgentModal.module.css`, `App.tsx` (onSuccess + the `defaultRole` rename), `PRReviewWorkspace.tsx` (prop rename), `CreateAgentModal/__tests__/*`, `CreateAgentModal.test.tsx`.

---

## Phase 2 — the backend keystone (unlocks custom supervised templates)

Today a custom supervised agent is three hand-assembled artifacts: (1) a prompt file on disk, (2) a **Role row — `loom role add`, CLI-only, no webui/HTTP route exists**, (3) the agentdef. The modal can't do (2) and can't set `task_filter`/`mode`.

**The unlock is one endpoint:** a webui **role-create + prompt-content-write API** (mirror `loom role add` + a file write). Once it exists, every custom supervised template becomes a `kind: 'custom-role'` descriptor whose prompt is seeded on first use. This is the single highest-leverage backend item in the whole vision.

- **Bug triage template** = seed `bug-triage.md` prompt + a `bug-triage` Role (`task_filter` for bug-type tickets, read-only or constrained tools) + an agentdef. Existing analog to copy the shape from: `domain.WorkerProfile` (`platform.go:104`) — a preset bundle, though it lives on the driver path, not the supervisor.
- Decision: Bug-triage as Lane 2b (supervised, poll-based) vs Lane 3 (workflow on `internal.issue.created`). Lean 2b — the issue-journal trigger is fleet-db-only and has a self-trigger hazard.

---

## Phase 3 — workflow/trigger surface (unlocks event-driven templates)

Engine exists; product surface doesn't.

- **Code review already exists** as `github-review-agent.ts` (trigger-driven, comment-only PR review). Adding a new flue workflow is cheap: `//go:embed` + one `builtinWorkflows` entry + the `.ts` (`workflows.go:80`).
- **Two real gaps:**
  1. The frontend is hardcoded to `epic-runner` — no generic workflow picker / input form / runs-list page (`startWorkflowRun` is generic but every caller passes `EPIC_RUNNER_WORKFLOW_NAME`). Needs a workflow-start surface.
  2. **Trigger bindings have no UI or HTTP write API** — created by `loom trigger bindings create` + manual GitHub webhook setup. Needs a binding create/enable UI + write API to make "turn on code review for this repo" self-serve.
- This is effectively a new "Automations" surface, not a modal tweak. The trigger engine itself (matcher, fan-out, cron, webhook ingest, internal events) is fully built and reusable.

---

## Open decisions

1. **Template = frontend descriptor (v1) or a backend first-class entity?** Recommend frontend-only now; promote to a backend `AgentTemplate` model at Phase 3 when agent-vs-workflow heterogeneity makes one source of truth pay off.
2. **Bug-triage lane:** supervised worker (2b) vs workflow-on-issue (3). Recommend 2b.
3. **Phase 1 scope:** ship Lead + Planner + Task only (zero backend), or pull the role API forward so Bug-triage lands in the first cut? Recommend the zero-backend shell first.

---

## Out of scope (this iteration)

- Custom agent roles beyond lead/plan/task in the UI (Phase 2).
- Event/cron trigger configuration UI (Phase 3).
- A generic workflow-run browser / runs-list page (Phase 3).
- Illustrations / backend-specific card branding.
- A two-step wizard (single screen retained).
