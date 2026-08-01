# Feature-Parity Implementation Plan: Closing the Full E2E Agent Loop on the TS/Workflow Plane

**Status:** Proposed (awaiting Tyson's decisions, Part 3)
**Date:** 2026-07-07
**Provenance:** Fable planner agent, root-causes verified in code against the live E2E findings of 2026-07-07.
**Related:** `2026-07-01-unified-agent-ux-proposal.md` (Phase 5, delta E, Decision 4, Config-by-reference), `2026-07-03-unified-agent-ui-test-matrix.tsv` (target: every non-PASS row flips).

Scope basis: design doc `2026-07-01-unified-agent-ux-proposal.md` (Phase 5, delta E, Decision 4, Config-by-reference final section) and the verdict matrix `2026-07-03-unified-agent-ui-test-matrix.tsv`. Paths abbreviated `loomcli/`, `fleet-db/`.

A key context note that shrinks this plan: recent commits already landed most config-by-reference groundwork — fleet-db `b8ecd01` (binding DELETE, newest-first runs, `trigger_binding_id` filter, pg migration 034), fleet-db `34c9cf3` (run-create accepts `trigger_binding_id`), loomcli `00a91efc` (`binding-config` op, binding-scoped run-now, bridge in shared automation scope), loomcli `424e7006` (client decodes `trigger_binding_id`, BindingID filter pushdown, contract guard), loomcli `c5621a00` (server-derived lock actor, claim-ready `type` filter, release-on-skip). The event lane's plumbing is done; what is missing is packaging, prompts, and UI.

---

## Part 1 — Root-cause confirmation per finding

### F1 — TS role-reuse broken (`prompt_agent_missing_prompt`) — hypothesis CONFIRMED, with one addition

Chain, fully verified:

1. Builtin roles are seeded with **no prompt file**: `internal/cli/serve/workspacemgr/workspace_store.go:481-508` (`seedBuiltInRoles` creates `plan`/`task`/`lead` with Description/TaskFilter only — no `PromptFile`).
2. The single prompt-body loader returns `""` for roles without a `PromptFile`: `internal/webui/handlers/roles/module.go:350-359` (`ReadPromptBody`, explicitly documented "empty for builtin roles").
3. The `role-get` driver op returns that empty prompt: `internal/webui/handlers/driverapi/module.go:397-415`.
4. `prompt-agent` resolves the named role, finds no body, and fails closed: `internal/workflows/builtin/prompt-agent.ts:158-165` (`roleResolved: true`, empty prompt) → `:70-77` (`prompt_agent_missing_prompt`).
5. The Go plane never needs the stored body: `internal/cli/daemon/supervisor/spawn.go:92-100` — builtin roles spawn `loom plan|task <worktree> --auto --daemon-mode`; prompts are composed at spawn from **embedded Go templates** (`internal/cli/agent/prompts.go:230` `GeneratePlanningPrompt`, `:261` `GenerateTaskPrompt`, rendering `prompts/planning.md` and `task.md` with per-spawn template data).

**Addition the hypothesis missed (important):** giving the builtin roles a prompt body is necessary but not sufficient for the *planner* lane. Two contract mismatches:

- The Go builtin prompts are **self-claiming loop prompts** (`planning.md:8-21` runs `loom data ready` / `loom data claim`; `task.md` step 9 runs `loom stack publish` + `loom data close`). The prompt-agent → local-task-runner contract is the opposite: the workflow claims the task first (`prompt-agent.ts:88-98`) and the runner scopes the backend CLI to a prepared worktree. The Go template bodies cannot be reused verbatim; TS-contract bodies must be authored.
- The serve task worker **hardcodes close-on-success**: `internal/driver/task_worker.go:110` (`CloseTaskOnSuccess: true`) → `internal/driver/task_request.go:693`. A TS *planner* run would therefore close the task instead of leaving it `design + review`. Plan semantics need a close-suppression control (precedent: the external-worker completion API already has per-completion `closeTask` at `internal/webui/handlers/taskrunapi/module.go:404`).

Also verified: `updateRole` (PATCH) has **no builtin guard** (`roles/module.go:187-242` — only DELETE refuses builtins at `:326`), so builtin roles can already be given prompt files via the existing API; and doing so does **not** perturb the Go plane (spawn checks `BuiltInRoles` before ever reading `RoleConfig.PromptFile`).

### F2 — local-mode can't close past commit — CONFIRMED

- Error origin: `internal/stackpublish/gitutil.go:30-43` — `repoSlug` regex `github\.com[:/]...` against `git remote get-url origin`; the local-mode origin is a bare path (`/workspace/source-repo-origin.git`, set at `test/local-mode/local-mode-entrypoint:8`), so parsing fails.
- Why the coder hits it: the Go `task` role prompt makes stacked publish **mandatory** (`prompts/task.md:193-207`); `loom stack publish` builds `stackpublish.Reconciler{Forge: NewGitHubForge(...)}` (`internal/cli/stack/stack_cmd.go:465-512`) and the reconciler calls `repoSlug` unconditionally (`internal/stackpublish/reconciler.go:163`). Every forge operation is GitHub-only (`internal/stackpublish/forge.go:28-53`). The Forge interface is the natural seam for a local implementation.
- The TS lane does **not** hit stackpublish today: `prompt-agent.ts:105` dispatches with `openPullRequest: false` → patch-back delivery (`local-task-runner.ts:317`); the patch is applied to the per-task worktree (`internal/driver/task_bridge.go:907` `applyPatchBack`) and persisted as an Artifact (`task_bridge.go:829-840`, `patch_artifact_id`). **But nothing pushes to the local origin** — no locally reviewable published ref, which is F5's dependency. (`local-task-runner.ts` push paths at `:869`/`:925` hardcode `https://github.com/owner/repo.git`.)

### F3 — TS auto-pickup loses the race / event lane — CONFIRMED, and closer to done than the doc header implies

- UI-created prompt agents are cron-only: `CreateAgentModal.tsx:382-393` (`source_kind: "cron"`), 10-minute UI floor (`:45`). No UI path creates internal bindings (`agentTemplates.ts:18-20` documents event bindings as CLI-only).
- The task-ready lane exists and is **already enabled in the local-mode stack**: emission is owned by `internal/trigger/issue_journal_bridge_task_ready.go` (payload `{taskId, status}`, route `internal.task.ready`), the `LOOM_TASK_READY_EVENTS` policy is resolved in `internal/cli/serve/serve_runtime_policy.go`, the polling loop is owned by `internal/cli/serve/runtimecomposition/loops.go`, and **`test/local-mode/docker-compose.yml` sets `LOOM_TASK_READY_EVENTS: "1"`**.
- Multi-binding fan-out works: fleet-db resolves exact RouteKey owner ∪ enabled pattern matches (`fleet-db/internal/api/platform.go:1397`, `:1525-1546`); each leg stamps `trigger_binding_id`; the run resolves config via `binding-config` (provenance-derived, body ignored), which `prompt-agent.ts:183-190` consumes. `resolveTargetTaskId` already reads `input.event.taskId` (`prompt-agent.ts:222-230`).
- Remaining true gaps: (a) binding-create HTTP handler requires `route_key` for non-cron kinds (`triggerbindings/module.go:249-251`) — internal bindings need the cron-style derived-route treatment; (b) no role/phase discrimination — planner and coder bindings would both fire on every `task.ready` and race blind claims (payload has no design/phase data); (c) arbitration vs the Go plane: local-mode seeds Go role agents unconditionally (`local-mode-entrypoint:181-187`) which claim in seconds.

### F4 — TS observability parity — CONFIRMED

- Binding detail shows status only: `WorkflowAgentDetail.tsx:410-462` (`RunDetailCard`: status/timestamps/error/summary — no transcript, no stream).
- Per-run events + SSE endpoints exist and are unused by this view: `internal/webui/handlers/workflows/module.go:49-51` (`GET /runs/{runId}`, `/events`, `/stream`).
- The task Runs tab's reusable transcript renderer: `IssueDetailPanel/sessions/SessionDetailView.tsx` (Transcript + Diff; polls every 3s via `useSessionTranscript`). TS task-runs create sessions (`task_bridge.go:249` `startFlueTaskSession`), so the same renderer can serve the binding detail.
- Per-agent Runs tab is per-DRIVER: `WorkflowAgentDetail.tsx:114-115` uses `binding.driver_id`; the HTTP handler filters by `DriverID` only (`workflows/module.go:156-160`) even though `DriverRunFilter.BindingID` exists and is pushed down end-to-end (client `internal/infra/fleetdb/platform.go:349`; server `fleet-db/internal/api/platform.go:964`; already used by binding-health `triggerbindings/module.go:162-165`).
- Linking a run to its transcript: `DriverRun.Output` carries prompt-agent's `issueId`/`taskRunId` (`prompt-agent.ts:129-137`); DriverSteps carry `TaskRunID` (`internal/store/platform_store.go:610`); no HTTP surface exposes steps today (`getRun` returns the bare run, `workflows/module.go:402-409`).

### F5 — local review lane — CONFIRMED; more primitives exist than expected

- `github-review` template hard-gates on the Settings GitHub token (`CreateAgentModal.tsx:297-311`); review-loop-agent only reviews cards whose `external_ref` is a GitHub PR url (`internal/workflows/builtin/review-loop-agent.ts:40-80`).
- The reopen concern from the golden-scenarios doc is **not a fleet-db blocker**: review→open goes through `UpdateIssue` (non-terminal statuses modifiable — `fleet-db/internal/service/status.go:19-24`), and closed→open is routed client-side to the reopen endpoint: `internal/backend/fleet/fleet.go:720-746` (`transitionToOpen`: `case "closed"` → `POST /issues/{id}/reopen` → `fleet-db issue_service.go:389` `ReopenIssue`, closed-only guard satisfied). `bug-fix-agent.ts:114-127` already performs exactly this reopen→review dance. **No fleet-db change needed.**
- Existing primitives: `issue-comment`, `issue-update` (status/labels/assignee/externalRef — `driverapi/module.go:970-1004`), label ops, review-cycle cap logic (`review-loop-agent.ts:57-62`), and `github-review-task-runner` takes the **diff as data** (`github-review-task-runner.ts:33-37`, `input.diff`) — the review brain is not GitHub-coupled; only diff *acquisition* and comment *destination* are.
- Missing: a local diff source (the coder's patch artifact exists server-side but no driver op exposes artifact/diff content), a review workflow variant not gated on GitHub, and a local `external_ref` convention.

### F6 — UI batch — all four located

- (a) Name ignored: name IS persisted (`triggerbindings/module.go:279`; `domain.TriggerBinding.Name` `internal/domain/platform.go:217`) but never displayed — sidebar renders `b.binding_id` (`AgentSection.tsx:205`), detail header renders `binding.binding_id` (`WorkflowAgentDetail.tsx:189`). Display bug, not persistence.
- (b) New Issue covered: button in board toolbar (`App.tsx:1281-1290`); sticky candidates: `BlockedSummary.module.css:83`, `IssueDetailPanel.module.css:112,376`. z-index/stacking fix.
- (c) Assignee dropdown omits TS agents: `CreateIssueModal.tsx:345-364` maps only `agents` from `useWorkspaceContext()`; bindings never enter the options.
- (d) Go role agents lack UI disable/pause: HTTP surface exists (`internal/webui/handlers/agentcontrol/module.go:23-27` stop/start/restart/yield) but no frontend wiring.

### F7 — capability-vs-kind rendering — CONFIRMED

`views/AgentsPage.tsx:588` (`if (selectedBinding)`) mounts `WorkflowAgentDetail` as a separate component; role agents go through `AgentEditorGroups` with hardcoded tabs (`AgentEditorGroups.tsx:18` `ALL_TABS = ["terminal","info","git","diff","files"]`). "Capability-based tabs, not kind-based views" is unimplemented.

---

## Part 2 — Workstreams

Order rationale: front-load the minimal path to "TS plane closes the full loop locally": **WS1 → WS2 → WS3a → WS5**, with WS4/WS6 parallelizable and WS3b/WS7 trailing.

### WS1 — Builtin-role prompt bodies + TS planner/coder semantics (fixes F1) — **M**, no dependencies, do first

**1a. Seed TS-contract prompt bodies for `plan` and `task` (S/M).**
- Author two new markdown bodies written for the prompt-agent/local-task-runner contract (task pre-claimed, worktree prepared, no self-claim loop, no `loom stack publish`):
  - `plan` body: read the assigned task + repo, produce a design, persist it to the task, end in `review`.
  - `task` body: implement the pre-claimed task in the provided worktree, run the gate, commit; delivery handled by the runner (per WS3a).
- Seed at workspace creation: extend `seedBuiltInRoles` (`workspace_store.go:481`) to write prompt files (reuse the `<ws>/.loom/prompts/<name>.md` convention of `writeRolePrompt`, `roles/module.go:375-397` — extract the write helper to a shared package) and set `RoleCreate.PromptFile`.
- Backfill for existing workspaces: idempotent ensure at serve start (only materialize when `PromptFile` is empty — never clobber an operator-customized prompt).
- Embed the default bodies as Go assets (e.g. `prompts/ts-plan.md`, `prompts/ts-task.md`) so they version with the binary.

**1b. Planner completion semantics — close-suppression + design handoff (M).**
- Add per-request close control: `execTaskParams` (`driverapi/module.go:603-629`) gains `closeTask *bool` (default true) → thread through `TaskRunRequestOptions` → persist on the queued TaskRun → `runOnceInWorkspace` reads it instead of hardcoded `CloseTaskOnSuccess: true` (`task_worker.go:110`).
- Teach `prompt-agent.ts` a role-outcome mode driven by the role record (which `roles.get` returns in full, incl. `TaskFilter`): when the worn role's task filter is `needs_plan` (or a new explicit `outcome: "design-review"` role field), dispatch with `closeTask: false`, and after completion set the card's design and `status: "review"` via `issue-update`. Add `Design *string` to the `issue-update` op params (`driverapi/module.go:971-978`) — `backend.UpdateParams` already supports it (`internal/backend/types.go:337`, wire key `:401`). Design content source, decision-gated: (i) planner backend writes the design itself via `loom data update` inside the runner (matches Go behavior; verify-first that the backend CLI env inside local-task-runner reaches loom serve), or (ii) planner's only output file is `.loom/design.md` in the worktree, read from the patch artifact (needs WS5's artifact-read op). Recommend attempting (i) first in the live spike.

**1c. Role dropdown honesty (S).** In `CreateAgentModal.tsx:616-621`, annotate roles with no prompt body and block submit with a precise message. Low priority once 1a lands.

### WS2 — Event-driven pickup + interim arbitration (fixes F3) — **M**, depends on WS1 for a meaningful E2E

**2a. UI creates internal-event bindings (S/M).**
- Backend: in `createBinding` (`triggerbindings/module.go:244-255`), extend the derived-route allowance to `source_kind == "internal"` (the cron `WithDerivedRoute` model) so pattern-matched internal bindings don't fight over the 1:1 route-key space.
- Frontend: `CreateAgentModal.tsx` prompt-agent lane creates `source_kind: "internal"`, `event_type_patterns: ["internal.task.ready"]`, keeping `run_input: {roleName, backend}`. Replace the Cadence dropdown for prompt agents with a trigger picker: "On task ready (recommended)" / cron fallback. Decision-gated: event-only vs event+cron hybrid.
- Sidebar/Info already render non-cron bindings and event patterns — no further visibility work.

**2b. Role-aware claim gating (S/M).**
- Enrich the `task.ready` payload: `toTaskReadyEvent` (`issue_journal_bridge_task_ready.go:116-130`) adds `hasDesign` (and `labels`, `issueType`) from the journal `After` snapshot.
- In `prompt-agent.ts`, before claiming: compare the worn role's `TaskFilter` (`needs_plan` → require no design or `needs-revision`; `has_design` → require design) against the event payload; on mismatch, complete honestly with `claimed: false` — zero codex spend. For the filterless/cron path, fetch the card after claim and release-on-skip via `release-task` on mismatch.
- This yields the loop's alternation: task created (no design) → planner claims; approve (review→open, design present) fires `task.ready` again → coder claims.

**2c. Interim two-plane arbitration in local mode (S).**
- Compose/env switch, e.g. `LOOM_LOCAL_MODE_PLANE=ts`, honored by `local-mode-entrypoint` (skip Go role-agent seeding at `:181-187` and worktree creation at `:501`). Deliberately the dumbest possible mitigation — real task-type arbitration stays a Phase-5 design input. Do NOT attempt router-level routing rules now.

**2d. Live verification of the event lane (no code).** `LOOM_TASK_READY_EVENTS=1` already set; the doc's "EVENT lane red" predates `binding-config` landing. First step: live spike — manually create an internal binding (CLI), create a task, watch a run fire within the 2s bridge cadence + claim by id. If red, debug before building UI on top.

### WS3 — Local publish (fixes F2) — split

**3a. TS-lane local branch delivery (M) — on the minimal path.**
- `local-task-runner.ts`: add `deliveryMode: "local-branch"` (or auto-detect filesystem-path origin). Mirror `deliverStackBranch` (`:904-934`) minus GitHub: commit the isolated worktree's changes to `loom/<taskid>`, `git push origin HEAD:refs/heads/loom/<taskid>` to the actual origin, return `metadata.delivery = "local_branch"`, `local_branch`, `head_sha`.
- `prompt-agent.ts` coder outcome: after a completed local-branch run, stamp `external_ref` via the S2-shaped dance proven in `bug-fix-agent.ts:124-125`: `update({status:"open"})` then `update({status:"review", externalRef:"local-branch:loom/<taskid>@<sha>"})`. Hands the card to the review lane in exactly the S2 shape.

**3b. Go-lane local forge (M/L) — off the minimal path; fixes observed task #19 BLOCKED.**
- New `LocalForge` implementing `Forge` (`stackpublish/forge.go:28-53`): `PushBranches` pushes to the origin path; PR phases skipped via a `PublishOnly`/forge-capability flag on `Reconciler` (phases 1/3/4 skipped, only phase-2 push — `reconciler.go:268-296`).
- Bypass `repoSlug` when the forge is local (`reconciler.go:163` moves behind the capability check).
- Forge selection by origin URL at construction sites: `stack_cmd.go:244,465,508-512`, `epic_reconcile.go:44-48` — GitHub → GitHubForge, local path → LocalForge, neither → current honest error.

**3c. Capability manifest (M) — grows session task #7 / doc Miss 3; trailing.**
- Minimal scope: per-template `capabilities` in `agentTemplates.ts` (e.g. `github-review` needs `github-token`; `stacked-publish` needs `github-origin`) checked at activate-time with precise messages, plus a `loom doctor` section for publish/review capability per workspace repo. Full manifest design stays with task #7.

### WS4 — TS observability parity (fixes F4, D2/D3) — **M**, independent; parallel

**4a. Per-binding run attribution (S).**
- `listWorkflowRuns` (`workflows/module.go:131-173`): accept `?binding=<id>` → `DriverRunFilter.BindingID` (pushdown exists end-to-end).
- `useWorkflowAgentDetail`: accept/forward `bindingId`; `WorkflowAgentDetail.tsx:114-115` passes `binding.binding_id`.

**4b. Live transcript on the binding detail (M).**
- Server: expose run→step linkage — extend `getRun` to embed steps (id, `step_kind`, `task_run_id`, `status`) or add `GET /runs/{runId}/steps` over `DriverStepStore`.
- Frontend: in `RunDetailCard`: (i) subscribe SSE `/runs/{id}/stream` while live; (ii) when a linked task-run/session exists, render the transcript by reusing `SessionDetailView` machinery. Factor the transcript renderer out of `IssueDetailPanel/sessions/` into a shared component — the first concrete step of WS7's convergence.

### WS5 — Local review lane (fixes F5, C8/C9/D1) — **M/L**, depends on WS3a + WS1

- **New driver op `task-run-diff` (or `artifact-get`) (S/M):** run-token-authenticated read returning the patch artifact content for a task-run in the caller's workspace (`patch_artifact_id` from `task_bridge.go:829-840`). Follow the `roles.get` op pattern.
- **New builtin `local-review-agent` (M)** (prefer separate builtin over branching review-loop-agent):
  1. list cards in `review` whose `external_ref` matches `local-branch:...`;
  2. cooperative `review-cycle:N` cap — copy `reviewCycleCount` logic verbatim;
  3. acquire the diff via the new op;
  4. dispatch `github-review-task-runner` with `input.diff` + rubric (diff-as-data; global runner resolution lets builtins dispatch trusted runners);
  5. post findings via `issue-comment`; bump cycle label; `issues.update({status:"open"})`.
- **Rework pickup:** review→open emits `issue.update` → `task.ready` fires (`isTaskReadyEntry`) → WS2b routes to the coder (design present) → C9's auto-fix loop closes.
- **Activation UX (S):** a "Review loop (local)" create-agent template (cron; review batching is fine on a cadence) with no GitHub-token gate; keep `github-review` gated per WS3c messaging.
- Alignment: implements the platform primitives S2 needs locally; the S2 golden scenario (GitHub PR lane) untouched.

### WS6 — UI batch (fixes F6) — **S** each, fully parallelizable

- (a) Render `b.name || b.binding_id` at `AgentSection.tsx:205`, `WorkflowAgentDetail.tsx:186-189`, and `bindingDisplay.ts` labels.
- (b) Sticky footer: live-repro; fix stacking between board toolbar (`App.tsx:1281-1290`) and sticky footers (`BlockedSummary.module.css:83`, `IssueDetailPanel.module.css:112,376`). Keep the change inside the offending module's CSS.
- (c) Assignee dropdown: merge `useAutomations(workspaceId)` bindings into `CreateIssueModal.tsx:345-364` options (grouped); assignee on a TS agent is advisory metadata (claims are lease-driven).
- (d) Go role-agent controls: Stop/Start (+ Restart) buttons in the role-agent detail pane calling existing endpoints (`agentcontrol/module.go:23-26`); tiny API client wrappers.

### WS7 — Capability-based tab convergence (fixes F7) — **L**, last; WS4b is its first installment

- Introduce an agent-capability descriptor at the AgentsPage resolution seam (`AgentsPage.tsx:588`): `{worktree, pty, runs, role}`.
- `AgentEditorGroups.tsx`: replace hardcoded `ALL_TABS` with capability-computed tabs (worktree→Git/Diff/Files; pty→Terminal; runs→Runs; always→Info). `WorkflowAgentDetail`'s Runs/Info become panes inside the same shell; the standalone component dissolves.
- Non-goal: PTY into TS runs (Decision 3: background prompt agents are watch-only).

### Execution order and dependency graph

```
WS1a ──► WS1b ──► WS2b ─┐
WS2a ──► WS2d ──────────┼──► live loop spike (planner→approve→coder)
WS2c (compose toggle) ──┘
WS3a ─────────────► WS5 ──► full-loop E2E rerun
WS4a, WS4b, WS6a-d: parallel anytime
WS3b, WS3c, WS7: trailing
```

Minimal path to "TS plane closes the full loop locally": **WS1a + WS1b + WS2a/b/c + WS3a + WS5** (WS4 needed to *watch* it live — schedule WS4a/4b alongside WS5).

---

## Part 3 — Sequencing vs Phase 5, and decisions needed from Tyson

**What here IS Phase 5 (or Phase-5 input):**
- WS1a is the front half of Phase-5 migration step 3 ("migrate builtin plan/task roles"). The back half — the Go leaf composing FROM the same stored body so one edit updates both planes (Decision 2 in full) — should be **deferred to Phase 5 proper**: the Go templates carry spawn-time template data (`prompts.go:24-40`) that a shared static body cannot express without a template-rendering contract both planes honor. Until then, builtin-role prompt edits affect the TS plane only; label the editor accordingly.
- WS2c is explicitly the interim arbitration the doc priced in; real task-type arbitration is a Phase-5 design item (per both vets — do not design it twice).
- Everything else is pre-Phase-5 parity work.

**Decisions (Tyson, 2026-07-07) — all resolved:**
1. **Builtin prompt bodies (WS1a): DECIDED — new TS-contract bodies now.** Shared-template rendering (Decision-2 full end-state) deferred to Phase 5; until then builtin-role prompt edits affect the TS plane only (label the editor).
2. **Planner design handoff (WS1b): DECIDED — agent-driven.** The planner backend writes the design via `loom data update` inside the runner (matches Go behavior). Verify-first in the live spike that the runner env reaches loom serve; fall back to workflow post-processing (`.loom/design.md` + artifact-read) only if it can't. The `closeTask` control on exec-task is blessed.
3. **Local-publish semantics (WS3): DECIDED — local-branch + LocalForge.** TS lane pushes `loom/<taskid>` to the bare origin with `external_ref: "local-branch:<branch>@<sha>"`; Go lane gets a branches-only LocalForge (PR phases skipped). Strictly gated on filesystem-path origins.
4. **Trigger shape (WS2a): DECIDED — event-only + Run-now.** Prompt agents bind to `internal.task.ready`; cron leaves the prompt-agent create path.
5. **Arbitration toggle default (WS2c): DECIDED — opt-in until the TS loop is green**, then revisit making `LOOM_LOCAL_MODE_PLANE=ts` the local-mode default.

---

## Part 4 — Acceptance mapping (TSV rows → PASS) and live verification

All verification is a real agent-browser E2E rerun on the local-mode codex stack — no stubs, no synthetic green.

| Workstream | Rows flipped | Live verification step |
|---|---|---|
| WS1 | B1, B2 (PARTIAL(BUG)→PASS) | Create prompt agent with existing role `plan`/`task` → Run-now → completes with `promptSource: role:plan`; planner run ends with card in `review` carrying a design; coder run implements. |
| WS2 | C3 (GAP(TS)→PASS) | With Go plane disabled: create a task in the UI; TS planner run appears within seconds, claims by id; approve → coder auto-fires within seconds. Zero cron involvement (`trigger_binding_id` stamped, internal delivery). |
| WS3a | C6 (BLOCKED→PASS, TS lane), prereq for C8 | Coder run pushes `loom/<taskid>` to the bare origin (verify `git branch -a` in-container); card in `review` with `external_ref: local-branch:…`. |
| WS3b | C6 (Go lane) | Go coder path: `loom stack publish` succeeds against the bare origin; task closes. |
| WS5 | B3 (→PASS via local template), B4, C8, C9, D1 (all →PASS) | Local review agent fires, posts findings comment, moves card to `open`; coder auto-claims rework, fixes, republishes; second review passes cap logic; task closes. Full loop, UI-only. |
| WS4 | C4 (GAP(TS)→PASS), D2, D3 (→PASS) | TS planner detail during a live run: SSE step events + transcript updating (3s session poll); per-agent Runs tab shows only this binding's runs (prove with a second agent). |
| WS6 | C2 (FLAKY→PASS), NOTES row items | Named agent shows in sidebar+header; 3 consecutive task creations with detail panel open; Assignee lists TS agents; Go agent Stop/Start works. |
| WS7 | consolidates C4/D2/D3 rendering | Role-agent detail unchanged; binding detail renders through the same shell; tab presence follows capabilities. |

Rows staying non-PASS by design: C7 (Go daemon Terminal noise — supervisor PTY lane; delta E's answer is the run transcript replaces the PTY for background work, which WS4 delivers on the TS plane; Go-lane terminal cleanup dissolves at Phase 5).

---

## Part 5 — Risks and blast radius

**F1/WS1 — Go plane safety.** Verified safe: `buildAgentExecCmd` short-circuits on `BuiltInRoles` (`spawn.go:92-100`) and never reads `RoleConfig.PromptFile` for builtins; the Phase-U TS leaf receives the Go-composed prompt via `LOOM_TASK_RUN_PROMPT` which outranks everything (`local-task-runner.ts:129-140`). Residual: (i) roles UI will show editable prompt bodies for `plan`/`task` that affect the TS plane only until Phase 5 — add explicit copy. (ii) Backfill must be idempotent and never clobber a customized prompt (only materialize when `PromptFile` empty). (iii) `closeTask` touches the task-completion barrier (`task_request.go:693`) — default-true preserves callers; test the false path against stale-recovery.

**F3/WS2 — two planes, one queue.** Until the toggle flips, both planes race; the task lease keeps it safe (one winner; prompt-agent claims before any codex dispatch, so lost races cost no tokens), but the Go plane usually wins — WS2c is required for TS rows to flip, and must stay local-mode-scoped. Event amplification bounded by hop-depth caps (`internal_source.go:49`) and dedup ids; `hasDesign` payload field is additive.

**F2/WS3 — real-GitHub deployments.** Forge selection strictly by origin-URL parse; `github.com` origins take the current path. Guard: a repo with no origin or non-GitHub HTTPS remote must NOT silently "publish locally" in hosted deployments — gate LocalForge on filesystem-path origin (and/or explicit `LOOM_PUBLISH_MODE=local`), else keep the honest error. TS-lane `local-branch` is opt-in via input/binding config; `bug-fix-agent`'s GitHub PR path untouched.

**F5/WS5 — new op surface.** `task-run-diff` exposes patch content to any verified run in the workspace — same trust altitude as `roles.get`; scope to caller's workspace, require run-token auth.

**F4/WS4 — streaming load.** One SSE per open detail page; 3s session poll is the existing cadence. `?binding=` is additive; fleet-db side already released — no cross-repo sequencing.

**F6b — z-index fixes** are the classic modal/toast regression source; scope to the offending sticky element's module and re-verify create-issue modal, toasts, issue detail panel together.

**Cross-repo risk posture:** **no fleet-db changes required** — every workstream is loomcli-side; keep the fleetdb-client contract guard green in CI as the tripwire.

---

### Critical files

- `internal/workflows/builtin/prompt-agent.ts` (WS1b, WS2b, WS3a)
- `internal/cli/serve/workspacemgr/workspace_store.go:481` (WS1a `seedBuiltInRoles`)
- `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx` (WS2a, WS1c, WS6a)
- `internal/workflows/builtin/local-task-runner.ts` (WS3a)
- `internal/webui/frontend/src/components/WorkflowAgentDetail/WorkflowAgentDetail.tsx` (WS4, WS7 seed)
