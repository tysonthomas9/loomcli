# aft test plan — the Planner background agent (`role_name: "plan"`)

Exhaustive coverage plan for **one** CreateAgentModal template: **Planner**. Companion to
`../README.md` (tiers and how to run), `../COVERAGE-PLAN.md` (what to test next), and
`../FINDINGS.md` (product bugs / stack gaps). Vocabulary follows `CONTEXT.md`:
*seeding seam*, *seed command*, *actor fidelity*, *readback*, *surface suite*, *delegation*.

No YAML is written by this document. Every case is specified to the point where the suite
file can be typed from it.

---

## Revision 3 — final round

Second independent review pass, again re-verified line by line. Revision 2's 15 ready-to-write
deterministic cases survived unchanged; the corrections below all land on the blocked and
real tiers, plus one retraction of my own.

1. **PLN-B14 REMOVED — it was a false finding of mine.** I compared the frontend `AgentState`
   union against `domain.AgentState` (the control-plane assignment enum). The union's own comment
   names a *different* type, and it is accurate: `internal/types/enums.go:140-153` defines
   `types.AgentState = idle | spawning | running | working | stuck | done | stopped | dead`, an
   exact match for `types/agent/agent.ts:18-27`. That union types `Issue.agent_state`; the
   control-plane state travels separately as `LoomAgentStatus.state?: string`. There is no drift.
   B14 is kept only as a tombstone so the numbering stays stable.
2. **PLN-B5 gains a third wall (understated, now corrected).** A seed command plus a frontend
   mapping is *not* enough. `monitor.AgentStatus` has **no `state` field at all**
   (`internal/cli/monitor/monitor_types.go:35-70` — it carries `Status`, `DesiredState`, `Mode`,
   `LiveStatus`, but never `State`), and the projection flattens every non-`active` assignment
   state to `status:"idle"` (`monitorStatusFromAgentState`,
   `internal/cli/serve/metricscmd/handlers.go:470-477`, called at
   `monitor_store_data_source.go:176`). The Agents page reads that monitor response. So
   `backend_unavailable` is erased *server-side*, before any frontend concern. B5 now requires
   **server projection first**, then rendering. This also has a live consequence for **PLN-D10**
   — noted there.
3. **PLN-B1: the `${RUN_ID}` markers could never work.** The stub runs as a backend subprocess
   under `cli.FilteredEnv()` (`backend_codex.go:115-124`), and `RUN_ID` is neither exactly
   allowlisted nor `LOOM_`/`DAYTONA_`-prefixed (`internal/cli/envfilter/envfilter.go:8-40`, prefixes at `:83-91`), so it arrives empty and every marker assertion in PLN-D13/D14/D16 would compare
   against a truncated string. Switched to **`LOOM_AFT_RUN_ID`** (survives on the `LOOM_` prefix)
   with the task ID as a belt-and-braces secondary marker.
4. **PLN-B1 must honour parent scope, or PLN-D24 is not a real test.** B1 specified an unscoped
   `loom data ready`. The CLI's own precheck is scoped
   (`HasAvailablePlanningTasks(planParentID, …)`, `plan.go:199-201`, → `fetchReadyIssues`,
   `automode_poller.go:64`), but the *stub* does its own selection and would happily pick the
   other epic's task — D24 would pass or fail for the wrong reason. B1 now extracts the scope
   from the prompt's `**Epic scope: <id>**` line (`prompts.go:254-262`). D24 also now states what
   it does **not** cover: passing `--parent` by hand tests the flag, not the stored
   `agent.Parent` → argv plumbing (`agent_session.go:423-428`, `spawn.go:98-99`).
5. **PLN-B2(b) "unbounded" was wrong.** `runPlan` checks `--daemon-mode` **before** `--auto`
   (`plan.go:98-101`) and `runPlanDaemon` invokes exactly one backend run and returns
   (`plan.go:130-180`) — which my own Overview §2 already said. The real objection is only the
   cross-test race. Corrected.
6. **PLN-R5 reclassified ready → blocked.** Its error class is not observable as written.
   One-shot finalization records the exit code but not the class
   (`finalizeAgentSession`, `plan.go:286-318`); the class exists only on the emitted `TaskFailed`
   event (`plan.go:415-431`). The agent bus writes to `LOOM_EVENTS_DIR` or
   `<loomDir>/events` (`internal/cli/agent_event_bus.go:40-50`), while serve reads a
   *daemon-config-derived* dir — `ResolveEventsDir` → `<workspaceRuntimeDir>/.loom/events`, and
   **`""` when no daemon config exists** (`observability.go:336-350`, `project.go:199`,
   `supervisor.go:981-986`), which is the aft stack's likely state. Two concrete unblock paths
   are now spelled out; new blocker **PLN-B15**.
7. **PLN-R6 reclassified deferred → blocked.** aft runs each `run:` step under
   `execFileP('bash', …, { timeout: 120_000 })` and turns a timeout into a step failure that
   aborts the test (`../testing-app/src/steps.ts:168-186`), so there is no place to hang the
   post-kill assertions the case depends on. It needs a background-process kill-and-readback
   protocol first.
8. **PLN-D17 was shallower than it claimed.** It asserted "all three rejection arms" of
   `IsAvailableForPlanning` but only exercised epic-exclusion and has-design. Added a
   design-less **non-work-type** fixture for the third arm (`IsNonWorkType`,
   `taskfilter.go:22-30`); the non-open arm is covered incidentally by D18.
9. **PLN-D16 now has a real oracle.** "Assert the observed label state" is not a test. Verified:
   `applyChangeRequest` adds `needs-revision` (`review_decision.go:126-129`) and nothing removes
   it — not the prompt (`planning.md:150-157` only says to document changes in notes), not the
   stub. Expected post-run-2 state is therefore that the label **remains**, pinned as a hard
   assertion.
10. **Stale citations refreshed:** the zero-repo rejection in `agent_service.go` `:399-401` →
    **`:416-421`**;
    `envfilter.go:37-38` → **`:39`** for the exact stub-var line; FINDINGS §3.9's
    review-content bullet `:377-379` → **`:422-424`**. The changelog's self-referential
    plan-line number was removed rather than maintained.
11. **Withdrawn by the reviewer:** the earlier "plan mislocates the badge synthesis" finding —
    the bad location was in the task brief, never in this document. Revision 2's item 10 is
    reduced to a pointer.

## Revision 2 — codex-vetted

Revision 1 was reviewed by an independent read-only pass (OpenAI Codex) against the same
checkout. Every finding was re-verified against source before acceptance; two were rejected or
narrowed on code evidence. Material changes:

1. **PLN-D15 rewritten (was wrong).** `/prs?review=<id>` mounts `PRReviewWorkspace`
   (`views/PRsPage.tsx:353-366`), whose Approve calls `applyReviewDecision`
   (`PRReviewWorkspace.tsx:361-370`) → `ReviewDecisionService.applyApproval` → **`CloseIssue`**
   (`internal/webui/service/review_decision.go:110-121`). It does **not** reopen a plan review.
   The `plan → open` branch lives in `App.handleApprove` (`App.tsx:713-730`), reachable only
   through the **issue detail panel's** review action bar (`panel-approve-button`,
   `IssueDetailPanel.tsx:1431-1449`). D15 now drives that surface, and a new **PLN-D15b**
   pins the divergence (two Approve buttons on one plan review → opposite outcomes). D15b needs
   no seam: a fabricated `design:` + `status: review` fixture reproduces it, so it lands in the
   surface suite and can be written today.
2. **PLN-D16 rewritten** onto the same panel surface (`panel-reject-button` →
   `reject-comment-form` / `reject-textarea` / `reject-submit`,
   `sections/RejectCommentForm.tsx:103-146`) instead of `/prs`. Reject is *consistent* across
   both surfaces (`applyChangeRequest`, `review_decision.go:124-149`), so only Approve diverged.
3. **PLN-D6 reclassified** from happy/201 to **edge/400 product-defect**. Empty-workspace
   creation always `MkdirAll`s a path (`workspacemgr/workspace_store.go:82-84`), so
   `ws.Path != ""`, and a zero-repo workspace makes `SelectAgentRepos` return empty
   (`localworkspace/localworkspace.go:492-495`) → `ErrValidation("workspace has no repos for
   agent")` (`svcimpl/agent_service.go:416-421`) → HTTP 400 for the non-interactive `plan` role.
   The modal hint promises the opposite (`CreateAgentModal.tsx:569-576`) — now logged as
   **PLN-B13**.
4. **PLN-D23 narrowed** to the assignee dropdown only. `StartWorkButton` is exported from
   `actions/index.ts:5` but **mounted nowhere** — the only production reference is a stale
   comment (`IssueDetailPanel.tsx:603`). Its `start-work-button` / `start-work-popover` /
   `agent-option-*` testids are unreachable in a browser. **PLN-B6** upgraded accordingly:
   the gap is a dead component, not just a dead prop.
5. **PLN-B3 / PLN-D20 narrowed.** `seed-session` is **not** required: `ListTaskSessions`
   falls back to local session stores across the runtime dir, workspace root, and every repo
   path (`svcimpl/session_service.go:156-197`, `:56-90`). The real requirements are
   (a) the planner CLI writes into a runtime root serve searches, and (b) the session carries
   the task ID, which `finalizeAgentSession` recovers from the **lock file**
   (`plan.go:291-294`) — so the stub must run `loom claim <id>` in its own CWD
   (`internal/cli/agent/claim.go:55`). D20 moves from "blocked" to "ready once PLN-B1/B2 land".
6. **PLN-B5 wording corrected.** A writer *does* exist — the supervisor
   (`supervisor/backend.go:66-83`). The gap is "no deterministic aft writer/API seam and no
   frontend state renderer", not "no writer".
7. **PLN-D7 precondition fixed.** `agent-section-background` needs one **regular/interactive**
   agent plus one background agent (`WorkspaceTree/AgentSection.tsx:104-108,127-145`); a
   planner is already background, so the companion must be a **lead**, not a task agent.
8. **New PLN-D24** — parent-scoped planning. `--parent` rewrites the prompt's
   `loom data ready --parent <epic>` and injects an `**Epic scope:**` directive
   (`prompts.go:254-262`), and the launch argv picks it up from `agent.Parent`
   (`agent_session.go:423-428`, `spawn.go:98-99`), which is PATCHable
   (`service/agent.go:103`).
9. **Step-vocabulary section added** (below) after verifying the pinned aft runner's schema.
   Net effect on this plan: every `agents`-list readback moves to `run:` + `python3` (dot-path
   asserts cannot filter arrays), while `expect.attr`, `expect.count.atLeast`, and `select:`
   are all confirmed **available** — the PLN-D4 `<select>` caveat is removed.
10. **Rejected (and since formally withdrawn by the reviewer):** the claim that this plan
    mis-locates the planning-badge synthesis. It is cited correctly at
    `internal/cli/serve/metricscmd/handlers.go:497-502`; every `handlers/agents/handlers.go`
    citation here is about PATCH/DELETE validation, which is what lives there
    (`:252-268`, `:236-250`).

### aft step vocabulary — verified against the pinned runner

Checked in the aft checkout (`$AFT_DIR`, default `../testing-app`) so no case is written
against a step that does not exist:

- **`api:` `assert:` is a dot-path scalar lookup only** — `lookup()` splits on `.` and walks
  `hasOwnProperty` (`src/api-step.ts:70-78`). Numeric segments index arrays (hence the existing
  `data.sessions.0.backend`), but there is **no predicate/filter**. Consequence for this plan:
  `GET /api/workspaces/{ws}/agents` returns an **unordered list**, so *every* agent readback
  must be a `run:` step that curls the list and selects by name in `python3` — the shape
  `zz-agent-flow.test.yaml:255` already uses. Assertions written as `GET …/agents → field == x`
  below all mean that, never an `api: assert:`.
- **There is no `GET /api/workspaces/{ws}/agents/{name}`.** Only `PATCH` and `DELETE` are
  registered for that path (`internal/webui/handlers/agents/module.go:27-28`); the list route
  is `GET /api/workspaces/{ws}/agents` (`:25`). No case may read a single agent by URL.
- **There is no `/roles` HTTP route** (nothing registers one). Role readbacks, if ever needed,
  go through `loom role … --json` in a `run:` step. This plan needs none.
- **Available and used here:** `expect.attr {selector|testid, name, equals}`
  (`src/types.ts:86-88`, `src/steps.ts:328-333`; `toCss` accepts either locator form,
  `src/steps.ts:125-127`) — exact string compare only, no `contains`;
  `expect.count {equals|atLeast|atMost}` (`src/types.ts:78-83`, `src/steps.ts:321-322`);
  `expect.{url,text,notText,title,visible,value,enabled,checked}` (`src/types.ts:97-112`);
  `select: {testid, value}` for `<select>` elements (`src/types.ts:169`, with the schema
  requiring a selector/testid locator without `first/last/nth`, `src/types.ts:195-199`).
- **Genuinely absent** (confirmed by grep over `src/types.ts`, `src/api-step.ts`, `src/steps.ts`):
  no `fill`/`select` **value-from-file** option (`ValuedLocatorSchema` takes a literal
  `value: string`, `src/types.ts:45`) and no `api:` **body-from-file** option (`body` is an inline
  object or string, `src/types.ts:148`). Consequence for this plan: the long design strings live
  inside the **stub**, never inside a suite step — PLN-B1 composes the design in bash, and the
  suite only asserts on markers. Nothing here needs to feed a multi-kilobyte literal through a
  step.
- **`run:` gets no aft interpolation** (`src/types.ts:184`) — `${var:x}` is unavailable there.
  Cross-step values arrive as files under `$AFT_WORK_DIR/<name>` (mirrored by `save:`) or as
  exported shell env (`$RUN_ID`, `$AFT_WS`).
- **Backend-selection guard:** only `codex`, `claude`, `cursor-agent`, and `opencode` have
  stubs (`e2e/stubs/`). Other registered backends — notably `gemini`
  (`internal/cli/backends/backend_gemini.go:19,121`) — would resolve to the operator's **real**
  host CLI through the prepended stub dir. Any case that picks a backend from
  `create-agent-backend` must pick a stubbed one, and any case that *runs* an agent must pin
  `--backend codex`.

---

## Overview

### What the template is

`CreateAgentModal` offers five template cards in two groups. The **Planner** card is the
first of the two *Background agents*:

| field | value | source |
|---|---|---|
| testid | `create-agent-template-planner` | `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx:53` |
| title / description | `Planner` / `Breaks epics into tasks under daemon supervision.` | `CreateAgentModal.tsx:50-51` |
| glyph / accent | `P` / `#0d9488` | `CreateAgentModal.tsx:52,55` |
| aria-label | `Planner, background agent` | `CreateAgentModal.tsx:438` |
| name placeholder | `planner` | `CreateAgentModal.tsx:52` |
| submitted role | `role_name: "plan"` | `CreateAgentModal.tsx:328,333` |

Submit payload (`CreateAgentModal.tsx:331-341`), sent as `POST /api/workspaces/{ws}/agents`
(`internal/webui/frontend/src/api/workspace/workspace.ts:309-318`, 120 s client timeout):

```json
{ "name": "<lowercased, trimmed>", "role_name": "plan", "auto": false,
  "cross_repo": <selectedRepos.length === 0>, "repos": [...], "backend": "<optional>" }
```

Note the description on the card ("Breaks epics into tasks") is **inaccurate**: the planning
prompts never call `loom data create`; decomposition is a *lead* responsibility
(`internal/cli/agent/prompts/lead.md:43,109`). A planner writes a design onto **one existing
non-epic task**. See §3 blocker **PLN-B9**.

### How the role actually executes

1. **Role registry.** `plan` is one of exactly two built-in supervisable roles —
   `internal/cli/daemon/supervisor/types.go:154-158` (`BuiltInRoles = {plan, task}`). Its only
   built-in property is `task_filter: needs_plan`
   (`internal/cli/daemon/supervisor/role.go:59-68`). Built-in roles may not set `prompt_file`
   (`role.go:22-24`, `MergeRoleConfig` deliberately drops it, `role.go:116`), which is what pins
   `plan` to the embedded prompts. Every workspace also gets a seeded fleet-db `plan` role record
   (`internal/cli/serve/workspacemgr/workspace_store.go:479-486`, `ReadOnly: true`).
2. **Launch argv.** Both the daemon supervisor
   (`internal/cli/daemon/supervisor/spawn.go:92-100`) and the web-UI terminal launch spec
   (`internal/webui/handlers/terminal/agent_session.go:400,407-410`) build the identical command:
   `loom plan <worktree|agentName> --auto --daemon-mode [--backend X] [--parent EPIC]`.
3. **The command.** `internal/cli/agent/plan.go:35-68`. `runPlan` (`plan.go:81-127`) checks
   `--daemon-mode` **before** `--auto`, so the supervisor/terminal argv above runs **one task per
   process**; repetition comes from the supervisor restart loop. Paths: daemon
   (`runPlanDaemon`, `plan.go:130-180`), auto+tmux (`plan.go:105-113`), auto-without-tmux
   (`runPlanAutoFallback`, `plan.go:183-195`), and plain single-task
   (`runPlanSingleTask`, `plan.go:198-242`).
4. **Which issues qualify.** `HasAvailablePlanningTasks`
   (`internal/cli/automode/automode_poller.go:115-123`) → `IsAvailableForPlanning`
   (`internal/cli/taskfilter.go:105-107`) = `IsWorkableTask ∧ NeedsPlan` where
   `IsWorkableTask` = `status == "open" ∧ ¬epic ∧ ¬non-work-type` (`taskfilter.go:71-73`) and
   `NeedsPlan` = `¬HasDesign ∨ needs-revision label` (`taskfilter.go:58-60`). `HasDesign` is
   `has_design ∨ design != "" ∨ design_artifact_id != ""` (`taskfilter.go:39-41`).
   **Epics never qualify.** The supervisor-side twin is `applyTaskFilter("needs_plan")`
   (`internal/cli/task_router.go:231-252`). When nothing qualifies the CLI prints
   `No tasks available for planning.` + `Tasks must be: open status, no design (or has
   needs-revision label), not epics` and exits 0 (`plan.go:206-210`).
5. **Prompt.** `GeneratePlanningPrompt` → `prompts/planning.md` when `LOOM_ASSIGNED_TASK_ID`
   is empty; `GenerateFleetPlanningPrompt` → `prompts/fleet_planning.md` when the supervisor
   pre-assigned a task (`plan.go:150-153`, `spawn.go:61-63`). Both open
   `You are a disciplined software architect. Your job is to CREATE PLANS, not implement them.`
   `DesignFormat` is `html` only when the workspace config says so
   (`internal/cli/agent/prompts.go:49-54`).
6. **Artifacts a planning run produces** — all on the *issue*, none on disk:
   - `loom data update <id> --design=… --design-format=…` (`planning.md:141-145`,
     `internal/cli/data/update.go:86-89,118-134,189-190`)
   - `loom data update <id> --status review --assignee=""` (`planning.md:150-157`)
   - `loom complete`, which **releases the fleet claim** (`internal/cli/agent/complete.go:61`,
     `complete_release.go:19-45` — the LOOM-1 planner-leaks-claim fix)
   - a session with `Phase: "planning"` (`plan.go:154,224`; `internal/sessions/types.go:43`;
     supervisor twin at `supervisor.go:489-491`), transcript, and `TaskClaimed` /
     `TaskCompleted`/`TaskFailed` events (`plan.go:359-436`).
   - **No child issues, no files, no commits.** That absence is the sharpest planner-vs-task
     discriminator and Part 2 asserts it explicitly.
7. **Supervision states.** `domain.AgentState` = `idle | active | stopped | backend_unavailable`
   (`internal/domain/agent.go:11-20`). `backend_unavailable` is set only by the supervisor gate
   (`supervisor/backend.go:20,78`) and rechecked on a fixed 30 s interval without eroding the
   restart budget (`supervisor/restart.go:20,244-265`). Restart budget default `max_retries: 3`
   (`restart.go:354-360`); failover walks `Entry.Backend → FallbackBackends[…]`
   (`supervisor/backend.go:88-171`).
8. **`auto: false` is inert.** `AgentEntry.Auto` is stored and displayed but never read by the
   supervisor; supervision is gated by `desired_state ∉ {stopped, draining}`
   (`internal/cli/config/project.go:133-142`, applied at `daemon.go:311`).

### The aft stack constraint that shapes this whole plan

`tests/aft/run-aft.sh` starts **`loom serve` only** — `scripts/start-e2e-server.sh:200-207`
launches `loom serve` and never a daemon. So in every deterministic tier run:

- there is **no supervisor**, therefore no `backend_unavailable`, no restart budget, no failover;
- a modal-created planner is a **definition only**; nothing spawns it;
- the only in-process execution engine is the serve-side driver + `POST /workflows/epic-runner`,
  and **epic-runner has no planning phase** — it claims ready tasks and runs
  `local-task-runner` with the *task* prompt (`internal/workflows/builtin/epic-runner.ts`; its
  only role logic is `isLeadRole`, `epic-runner.ts:491-496,616-624`).

Consequently a deterministic planning **run** needs a new seam (§3), while a large amount of
planner surface — creation, scoping, rail/monitor rendering, the `planning:` status branch,
the stopped-terminal branch — is reachable **today** with no new seam at all.

### Current coverage: zero

- No suite in `tests/aft/suites/`, `surface-suites/`, `real-suites*/`, or
  `real-terminal-suites/` clicks `create-agent-template-planner`. `zz-agent-flow` uses the
  **lead** card (`suites/zz-agent-flow.test.yaml:38`) and the **task** card (`:246`).
- No suite POSTs `role_name: "plan"`; the only API-created agent is
  `{"role_name":"task"}` (`real-terminal-suites/zz-real-terminal-logs.test.yaml:42`).
- No suite exercises a design-writing run, the `review` plan-review stage produced by a
  planner, or the `needs-revision` re-plan loop.
- Go-side, `internal/cli/agent/plan_smoke_test.go` and `plan_test.go` cover the command well,
  but `internal/cli/daemon/daemon_planner_smoke_test.go` is a **one-line empty stub**
  (`package daemon` and nothing else) — there is no daemon-level planner smoke test either.

---

## Part 1 — Deterministic tier (stub AI backend)

Placement key: **PC** = product-correctness (`tests/aft/suites/`), **SF** = surface wiring
(`tests/aft/surface-suites/`), **SEAM** = blocked on a new seam from §3.

Suite hosting: everything that creates a persistent agent definition belongs in a `zz-`
suite with its **own workspace** (README.md:158-161). Proposal:

- `tests/aft/suites/zz-planner-agent.test.yaml`, workspace **`E2E-WS-PLAN`** (name
  `e2e-ws-plan`), created in `setup:` exactly like `zz-agent-flow`'s
  `E2E-WS-AGENT` (`suites/zz-agent-flow.test.yaml:10-17`), torn down with
  `AFT_WS=E2E-WS-PLAN "$AFT_TESTS_DIR/scripts/close-open-issues.sh"` plus
  `DELETE /agents/<name>` and `DELETE /workspaces/E2E-WS-PLAN`.
- `tests/aft/suites/zz-planner-run.test.yaml`, workspace **`E2E-WS-PLANRUN`**, for the
  cases that actually execute a stubbed planning run (PLN-D13–D20 **except D15b**, plus D24) —
  separate so a seam regression cannot take the creation cases down with it.
- Surface cases go into `tests/aft/surface-suites/planner-contracts.test.yaml`: PLN-D15b, D21,
  D22, D23. (D15b is *documented* next to PLN-D15 for readability, but the directory is the
  tier — README.md:42 — so it lives in the surface file and uses a fabricated design fixture
  rather than the stub run.)

All titles carry `${RUN_ID:-local}`; agent names carry it too (`planner-${RUN_ID:-local}`),
matching the `iris-${RUN_ID:-local}` convention (`zz-agent-flow.test.yaml:249`).

---

### Group A — creation through the modal

#### PLN-D1 — Planner template creates a `plan`-role agent (happy path + readback)

- **Tier:** PC · **Kind:** happy
- **Intent:** `An operator creates a Planner background agent through CreateAgentModal and confirms the workspace records it with the plan role and no auto-start`
- **Preconditions:** `E2E-WS-PLAN` exists with exactly one repo (setup).
- **Steps**
  1. `open: /ws/E2E-WS-PLAN/agents`
  2. `wait.fn: !!document.querySelector('[data-testid=agents-page]')` — *Wait until the agents page is ready*
  3. `click: { role: button, name: "+ Add agent", exact: true }` (`AgentSection.tsx:158-160`; no testid)
  4. `wait.fn: !!document.querySelector('[data-testid=create-agent-overlay]')`
  5. `expect: { visible: { testid: create-agent-template-planner } }`
  6. `click: { testid: create-agent-template-planner }`
  7. `expect: { attr: { testid: create-agent-template-planner, name: aria-pressed, equals: "true" } }`
     (`AgentTemplateCard.tsx:29-38`)
  8. `fill: { testid: create-agent-name, value: "planner-${RUN_ID:-local}" }`
  9. `click: { testid: create-agent-submit }`
  10. `wait.fn: !document.querySelector('[data-testid=create-agent-overlay]') && document.body.textContent.includes('planner-${RUN_ID:-local}')`
- **Assertions (readback)** — `api: GET /api/workspaces/E2E-WS-PLAN/agents`, assert on the
  matching element: `role_name == "plan"`, `auto` falsy, `cross_repo == false`,
  `repos` length 1 (modal preselects the first repo, `CreateAgentModal.tsx:215-218`),
  `state` empty-or-`idle`, `mode` empty. Because the list is enveloped
  (`{"success":true,"data":[…],"total":N}`) and unordered, use a `run:` step with `python3`
  to select by name — the same shape `zz-agent-flow.test.yaml:255` already uses.
- **Edge rationale:** pins the exact create contract for the one template with zero coverage,
  and pins `auto:false` (the modal hardcodes it — `CreateAgentModal.tsx:334`).

#### PLN-D2 — invalid agent name keeps Create disabled

- **Tier:** PC · **Kind:** edge
- **Intent:** `An operator typing an invalid agent name into the Planner form sees Create stay disabled and no agent is created`
- **Rules:** `STORED_AGENT_NAME_RE = /^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$/`
  (`internal/webui/frontend/src/utils/agentName.ts:2`); the name is `.trim().toLowerCase()`d
  first, so **uppercase input is valid** — the invalid probes must be punctuation-edged or
  out-of-charset. `canSubmit` gates the button (`CreateAgentModal.tsx:275-278`), so an invalid
  name produces a **disabled button, not an error box**.
- **Steps:** open modal → click Planner → `fill: { testid: create-agent-name, value: "-bad" }`
  → `expect: { enabled: { testid: create-agent-submit, equals: false } }` → repeat for
  `"has space"` and `"trailing."` and `""` → finally fill a valid name and assert the button
  enables again.
- **Assertions:** submit disabled for each invalid probe; `GET …/agents` still contains no
  agent named with the invalid probe; enabled once valid.
- **Edge rationale:** the "1–100 lowercase…" message (`agentName.ts:14`) is only reachable
  through `handleSubmit`, which `canSubmit` prevents — asserting the disabled button is the
  honest browser-observable contract. Worth a comment in the YAML so a future reader does not
  "fix" the test by looking for `create-agent-error`.

#### PLN-D3 — duplicate planner name surfaces the server 409 in the modal

- **Tier:** PC · **Kind:** edge
- **Intent:** `An operator who reuses an existing agent name sees the workspace's duplicate error inside the Planner form and no second agent is created`
- **Preconditions:** PLN-D1 has created `planner-${RUN_ID}`.
- **Steps:** open modal → Planner card → same name → submit →
  `wait.fn: !!document.querySelector('[data-testid=create-agent-error]')`
- **Assertions:** `expect: { visible: { testid: create-agent-error } }`;
  `expect: { attr: { testid: create-agent-error, name: role, equals: "alert" } }`;
  the text contains `already exists` (server message is
  `create agent: agent "<name>" in workspace "<ws>": domain: already exists`, from
  `svcimpl/agent_service.go` → `classifyStoreError` → `KindConflict` → HTTP 409,
  `webui/server/handler/errors.go:13-30`); overlay stays open; `GET …/agents` still returns
  exactly one agent with that name.
- **Edge rationale:** there is **no client-side duplicate check**; this is the only test that
  proves the 409 body reaches `ApiError.message` (`types/common/errors.ts:29-41`) instead of
  the generic `Failed to create agent` fallback.

#### PLN-D4 — backend dropdown selection is persisted on the planner

- **Tier:** PC · **Kind:** happy
- **Intent:** `An operator picks a non-default AI backend for the Planner and the workspace records that backend on the agent`
- **Steps:** open modal → Planner card → fill name `planner-be-${RUN_ID}` →
  `select: { testid: create-agent-backend, value: "claude" }` → submit → readback.
- **Assertions:** the agents-list `run:` readback shows `backend == "claude"`. Also assert
  (before submit) `expect: { value: { testid: create-agent-backend, equals: "claude" } }` and
  that the option set is non-empty — options come from `useBackends()` / `/api/backends`
  (`CreateAgentModal.tsx:226-232`); the stub stack has `codex`, `claude`, `cursor-agent`,
  `opencode` on serve's PATH (`e2e/stubs/`), so both `codex` (default,
  `CreateAgentModal.tsx:138`) and `claude` must be present.
- **Edge rationale:** the backend choice is the input to the terminal launch spec
  (`agent_session.go:360-376`) and to the supervisor failover chain; nothing pins it today.
- **Backend-selection guard:** pick `claude` specifically — it has a stub. Never select an
  unstubbed registered backend (e.g. `gemini`, `backend_gemini.go:19,121`), which would resolve
  to the operator's real host CLI if this agent were ever started. This case never starts it.
- **Runner note (r2):** `select:` is a first-class aft action (`src/types.ts:169`), so no
  `agent-browser eval` workaround is needed. The earlier caveat about `<select>` is withdrawn.

#### PLN-D5 — repo scoping: chip deselect flips the planner to workspace scope

- **Tier:** PC · **Kind:** edge
- **Intent:** `An operator deselects the preselected repo so the Planner gets workspace-wide scope, and the workspace records cross-repo with no repos`
- **Steps:** open modal → Planner card → assert the hint reads
  `Pick every repo this agent works in. Leave all unselected for workspace scope.` →
  `click: { selector: "[data-testid='create-agent-repo-chips'] button", first: true }`
  (mirrors `zz-agent-flow.test.yaml:252`) → assert the chip's `aria-pressed` is now `false`
  and the hint flipped to `No repo selected — the agent gets workspace-wide scope.` →
  fill name → submit.
- **Assertions:** readback `cross_repo == true` and `repos == []`. (`crossRepo` is derived,
  not a toggle: `CreateAgentModal.tsx:220`; hint strings `CreateAgentModal.tsx:368-370`.)
- **Edge rationale:** `cross_repo` is a real supervisor input (`SelectAgentRepos` decides how
  many worktrees `POST /agents` materializes, `svcimpl/agent_service.go:388-434`) and the only
  way to express "workspace scope" is an absence of selection — a genuinely surprising UI
  contract worth pinning.

#### PLN-D6 — the no-repos hint promises workspace scope, but creating the planner fails

- **Tier:** PC · **Kind:** edge · **Revision 2: was "creates with cross-repo scope (201)" — that
  expectation is wrong; the create returns 400.**
- **Intent:** `An operator creating a Planner in a workspace with no repos is told the agent will get workspace scope, then sees the create fail`
- **Preconditions:** its own workspace created in-test via `api: POST /api/workspaces` with
  `{"name":"e2e-ws-planempty-<run>","type":"empty"}` and **no** `repos` (the Empty branch of
  `CreateWorkspaceModal`, FINDINGS §1.14, is already proven by `workspaces`).
- **Steps:** open that workspace's `/agents` → `+ Add agent` → Planner card →
  `expect: { visible: { testid: create-agent-no-repos } }` →
  `expect: { text: "No repos yet — add one from the sidebar first. This agent will run with workspace scope." }`
  → `expect: { count: { testid: create-agent-repo-chips, equals: 0 } }` → fill name → submit →
  `wait.fn` on `create-agent-error`.
- **Assertions:** `expect: { visible: { testid: create-agent-error } }` with text containing
  `workspace has no repos for agent`; the overlay stays open; the agents-list `run:` readback
  shows **no** agent with that name. Teardown deletes only the workspace.
- **Verified mechanism (r2):** empty-type creation still `os.MkdirAll`s the workspace dir
  (`workspacemgr/workspace_store.go:82-84`) and persists it via `saveLocalWorkspaceState`
  (`:138-141`), so `ws.Path != ""` and `ensureLocalAgentWorktrees` does **not** take its
  distributed/no-path early return (`svcimpl/agent_service.go:396-402`). With zero repos,
  `SelectAgentRepos` returns `nil, nil` (`localworkspace/localworkspace.go:492-495`), so
  `len(repos) == 0` → `service.ErrValidation("workspace has no repos for agent")`
  (`agent_service.go:416-421`) → HTTP 400. The `plan` role is non-interactive
  (`domain.ResolveRoleKind`), so the interactive bypass at `agent_service.go:389-394` does not
  apply either.
- **Edge rationale:** the modal states the opposite of what the server does
  (`CreateAgentModal.tsx:569-576`). This case pins the real behaviour *and* is the regression
  guard for whichever way **PLN-B13** is resolved (fix the hint, or let a repo-less workspace
  create a workspace-scoped agent). Note `PLN-D5` is unaffected: with one repo present,
  `cross_repo: true` makes `SelectAgentRepos` return that repo (`localworkspace.go:496-498`) —
  which is exactly why `zz-agent-flow.test.yaml:246-256` already passes for the task template.

---

### Group B — the planner in the agent surfaces (no run required)

#### PLN-D7 — the planner appears in the rail, the background subgroup, and the monitor

- **Tier:** PC · **Kind:** happy
- **Intent:** `An operator sees the newly created Planner in the agents rail with its Plan role and idle status, and in the monitor's agent activity panel`
- **Preconditions (corrected in r2):** PLN-D1's planner exists, **plus one interactive agent —
  a lead**. `showBackgroundGroup = regular.length > 0 && background.length > 0`
  (`WorkspaceTree/AgentSection.tsx:108`, subgroup at `:127-131`), and `isBackgroundAgent` is
  true for both `plan` and `task` (`utils/agentRole.ts:21-41`) — so a *second background*
  agent would leave `regular` empty and the subgroup would **not** render. Only an interactive
  role lands in `regular` (`agentRole.ts:13-19,36-41`). Create the lead through the modal in the
  same suite (reuse the `create-agent-template-lead` gesture from
  `zz-agent-flow.test.yaml:38`).
- **Steps**
  1. `open: /ws/E2E-WS-PLAN/agents/planner-${RUN_ID:-local}` (deep link — the bare `/agents`
     auto-select races the store's first fetch; see the comment at
     `zz-agent-flow.test.yaml:45-47`)
  2. `wait.fn` on `agents-page`; `expect: { url: "**/agents/planner-*" }`
  3. `wait.fn: document.body.textContent.includes('planner-${RUN_ID:-local}')` — the rail
     capitalizes via CSS `text-transform`, so match `textContent`, never rendered text
     (`zz-agent-flow.test.yaml:54`)
  4. `expect: { visible: { testid: agent-section-background } }`
  5. Role badge — **no testid exists on `AgentCard`**. Assert via
     `wait.fn: !!Array.from(document.querySelectorAll('[aria-label="Agent: planner-${RUN_ID:-local}"]')).length`
     and read the card's role text with a `wait.fn` that checks
     `el.textContent.includes('Plan')`. `AgentCard` renders
     `role.charAt(0).toUpperCase()+slice(1)` → `Plan`
     (`components/AgentCard/AgentCard.tsx:61-63`); root carries
     `role="button"`, `aria-label="Agent: <name>"`, `data-status={parsed.type}`
     (`AgentCard.tsx:71-89`).
  6. `expect: { attr: { selector: "[aria-label='Agent: planner-${RUN_ID:-local}']", name: data-status, equals: "idle" } }`
     — a store-only agent with empty `State` maps to `"idle"`
     (`internal/cli/serve/metricscmd/handlers.go:470-477`,
     `monitor_store_data_source.go:176`), and `getStatusLabel` renders `Idle`
     (`utils/agent/agentStatusPresentation.ts:47-48`).
  7. Info tab: click `Info` in `agent-editor-groups` and assert the `<dl>` shows
     `Role` → `Plan` and `Scope` → the repo name, above the `<p>` reading `Plan agent`
     (`views/AgentsPage.tsx:420-422,454-487`).
  8. `open: /ws/E2E-WS-PLAN/monitor` → the 5.5 s poll + `reload: true` dance from
     `zz-agent-flow.test.yaml:81-86` → `expect: { visible: { testid: agent-activity-panel } }`,
     `expect: { notText: "No agents found" }`, and body text contains the planner name.
- **Edge rationale:** first coverage of the plan-vs-task *presentation* split
  (`utils/agentRole.ts` `BACKGROUND_ROLE_NAMES`, `AgentCard` role label, monitor projection).
- **Testid gap:** log **PLN-B7** (§3) — `AgentCard` / `AgentIconRail` / `AgentRail` carry no
  `data-testid`, so this test is forced onto `aria-label` + `data-status`.

#### PLN-D8 — a modal-created planner stays idle (auto:false, no supervisor)

- **Tier:** PC · **Kind:** edge
- **Intent:** `An operator confirms that creating a Planner definition does not start a worker process: the design-less task stays open, unassigned, and without a session`
- **Steps:** with the planner from PLN-D1 present, `api: POST …/issues` creating a **task**
  (`issue_type: "task"`, no `design`) titled `planner-idle ${RUN_ID}` → open the board →
  `wait: { text: planner-idle }` → `wait: { ms: 8000 }` (one agent-store poll cycle plus
  margin) → readbacks.
- **Assertions:** `api: GET …/issues/{id}` → `status == "open"`, `assignee` empty,
  `has_design` false / `design` empty (all dot-path scalars, so a real `api: assert:`);
  the agents-list `run:` readback → the planner's `state` is still empty-or-`idle` and
  `active_task_id` is absent; `api: GET …/tasks/{id}/sessions` → `data.sessions.0` does **not**
  exist (`exists: false` — a dot-path check, valid as an `api: assert:`).
- **Edge rationale:** this is the *negative* that makes every later run-case meaningful, and it
  documents a real trap: `auto` is **not** what gates supervision
  (`internal/cli/config/project.go:133-142`), the absent daemon is.

#### PLN-D9 — an assigned task projects the **Planning** badge without running a planner

- **Tier:** PC · **Kind:** happy · **No new seam required**
- **Intent:** `An orchestration client assigns an in-progress task to the Planner and an operator reviews the synthesized Planning projection; this does not assert a Planner process ran`
- **Mechanism (verified):** `mergeStoreAgentsWithRuntime` synthesizes the status string when a
  store agent has no runtime entry but *does* own an in-progress task:
  ```go
  } else if task, ok := agentTasks[agent.Name]; ok && task.Status == "in_progress" {
      prefix := "working"
      if agent.Role == "plan" { prefix = "planning" }
      agent.Status = fmt.Sprintf("%s: %s", prefix, task.ID)
  }
  ```
  (`internal/cli/serve/metricscmd/handlers.go:497-502`). `AgentTasks` is keyed
  *agent name → task, from assignee* (`internal/cli/monitor/monitor_types.go:16`).
  The frontend then parses `planning: <id>` (`types/agent/agent.ts:241-252`) and labels it
  `Planning` with the working dot colour (`utils/agent/agentStatusPresentation.ts:18,42-43`).
- **Steps:** `api: POST …/issues` (task, no design) → `api: PATCH …/issues/{id}` with
  `{"assignee":"planner-${RUN_ID}","status":"in_progress"}` → `open: /ws/E2E-WS-PLAN/agents`
  → poll/reload for the agent store → assert.
- **Assertions:**
  `expect: { attr: { selector: "[aria-label='Agent: planner-…']", name: data-status, equals: "planning" } }`
  and the card's status line text is `Planning`. Then the same badge on the issue detail panel:
  open the issue, `expect: { visible: { testid: agent-status-badge } }` and
  `expect: { attr: { testid: agent-status-badge, name: data-status, equals: "planning" } }`
  (`data-status={parsed.type}` at
  `components/IssueDetailPanel/header/AgentStatusBadge.tsx:105`, testid at `:106`). The badge
  also carries `title="<name>: Planning"` and
  `aria-label="Agent <name>: Planning. Click to view logs."` (`:111-112`) — assert the `title`
  too, since it is a content assertion in the FINDINGS §3.1 sense rather than a shape check.
  Contrast case in the same test: repeat with the **task**-role agent and assert `working` —
  proving the branch is role-driven, not incidental.
- **Edge rationale:** the single most plan-specific rendering branch in the whole UI, reachable
  with two API calls and zero new infrastructure. Highest value-per-line case in this plan.

#### PLN-D10 — a stopped planner offers no terminal

- **Tier:** PC · **Kind:** edge
- **Intent:** `An operator who stops the Planner sees the agents page explain that there is no live terminal session instead of attaching to one`
- **Steps:** `api: PATCH /api/workspaces/E2E-WS-PLAN/agents/planner-${RUN_ID}` with
  `{"desired_state":"stopped"}` (valid per `handlers/agents/handlers.go:101-104,261-268`) →
  `open: /ws/E2E-WS-PLAN/agents/planner-${RUN_ID}` → wait.
- **Assertions:** `expect: { text: "Agent is stopped" }` and
  `expect: { text: "This agent does not have a live terminal session. Start the agent before attaching to its PTY." }`
  (`components/AgentDetailMain/AgentDetailMain.tsx:166-181`; no testid — see PLN-B7).
  Readback: the agents-list `run:` step → `desired_state == "stopped"`. Then `PATCH` back to
  `{"desired_state":"running"}` and assert the empty state disappears.
- **Why `desired_state` and not `state` (r3 — load-bearing).** `isTerminalUnavailable` tests
  three arms: `state === "stopped" || state === "dead" || desiredState === "stopped"`
  (`AgentDetailMain.tsx:166-170`). Only the **third** can ever fire on the agents page, because
  the page reads `/api/monitor/agents` and `monitor.AgentStatus` carries **no `state` field at
  all** (`internal/cli/monitor/monitor_types.go:35-70`; `DesiredState` is projected at
  `monitor_store_data_source.go:190`, `State` is not). Patching `{"state":"stopped"}` would
  therefore change nothing visible. Use `{"desired_state":"stopped"}` and say why in the YAML
  comment, or a future reader will "simplify" it into a no-op test.
- **Edge rationale:** this is the **substitute** for the unreachable `backend_unavailable`
  state (PLN-D12 / PLN-B5): a deterministic, browser-observable "this agent will not run"
  branch. It also pins `agentTerminalLaunchAllowed`
  (`handlers/terminal/agent_session.go:249-257`), which strips the stored launch spec for a
  stopped worker. And the projection gap it documents is exactly PLN-B5's third wall: the two
  cases share one root cause.

#### PLN-D11 — delete and recreate the planner

- **Tier:** PC (with an SF note) · **Kind:** edge
- **Intent:** `An operator removes the Planner through the workspace API and recreates it from the modal under the same name`
- **Steps:** `api: DELETE /api/workspaces/E2E-WS-PLAN/agents/planner-${RUN_ID}` → assert 200
  and `{"success":true,"message":"agent deleted"}` → `open: /ws/E2E-WS-PLAN/agents` →
  `wait.fn` that the name is gone from `document.body.textContent` → reopen the modal, click
  the Planner card, retype the same name, submit → readback 201/list contains it again.
- **Assertions:** delete removes it from the rail (SSE/refresh broadcast is
  `{type:"refresh", entity_type:"agent", action:"agent.refresh"}`,
  `handlers/agents/handlers.go:236-250`); the post-recreate agents-list `run:` readback shows
  `role_name == "plan"` again. Note the delete response is a dot-path scalar, so `success` /
  `message` may be asserted with a real `api: assert:`.
- **Actor-fidelity note:** the delete leg is API-only **because there is no delete-agent UI
  control anywhere** (`deleteWorkspaceAgent` has exactly three callers, all epic-runner
  rollback paths: `IssueDetailPanel.tsx:1138`, `hooks/workspace/startEpicRunnerForIssue.ts:145`,
  `views/IssueDetailPage.tsx:173`). Record that in the YAML comment and as **PLN-B4** (§3); if
  the gap is closed, promote the delete leg to a UI gesture.

#### PLN-D12 — planner blocked by a missing backend binary (`backend_unavailable`)

- **Tier:** SEAM (blocked) · **Kind:** edge
- **Intent (target):** `An operator sees the Planner reported as blocked because its backend CLI is not installed`
- **Why blocked (three independent walls). A writer *does* exist — see the r2 correction in
  PLN-B5; what is missing is a writer reachable from the deterministic stack.**
  1. No daemon runs in the aft stack, and the **only** writer is the supervisor's
     backend-availability gate (`supervisor/backend.go:66-83` → `markControlPlaneAgentState`
     at `:78`).
  2. `PATCH /agents/{name}` **rejects** it: `validAgentState` accepts only
     `"" | idle | active | stopped` (`handlers/agents/handlers.go:252-259`), so the state a
     first-class domain constant defines cannot be written through the API.
  3. The frontend has **no mapping for `backend_unavailable`** at all — grep across
     `internal/webui/frontend/src` returns zero hits. It would render as raw snake-case only
     inside `EphemeralWorkerSummary`'s `State` row (`AgentDetailMain.tsx:197-201`).
- **Nearest reachable proxy today (write this one, in SF):** the *derived* label path.
  `AgentRow` maps `last_error_class` → human text, including
  `BackendUnavailable → "backend unavailable"` (`components/IssueCard/AgentRow.tsx:51-67`),
  rendered as `agent missing · backend unavailable`. `last_error_class` is a read-only
  passthrough from fleet-db (`internal/domain/agent.go:73-77`), so it also needs a seam —
  see **PLN-B5**.
- **Recommendation:** treat PLN-D12 as the acceptance test for PLN-B5, and until then rely on
  PLN-D10 for the "planner will not run" user-visible branch.

---

### Group C — a full stubbed planning run

Everything in this group **except PLN-D15b** depends on **PLN-B1 + PLN-B2** (§3): a
`STUB_CODEX_PLAN_RUNNER=1` mode in `e2e/stubs/codex`, its env-allowlist entry, and a launch
gesture. (PLN-D15b is documented here for continuity with PLN-D15 but is a seam-free surface
case — see its **File** note.) The steps below are
written against the recommended launch (a `run:` step invoking the real `loom plan` with a
per-run bin dir, exactly the shape `real-terminal-suites/zz-real-terminal-logs.test.yaml:47-48`
already uses for `loom … task … --auto`).

Shared preamble for the group (suite `zz-planner-run`, workspace `E2E-WS-PLANRUN`):

```
# fixture: an epic and a design-less child task
api POST …/issues  {title: "planner epic $RUN_ID", issue_type: epic, priority: 2}      -> planEpicId
api POST …/issues  {title: "planner task $RUN_ID", issue_type: task, priority: 2,
                    parent: ${var:planEpicId}, description: "STUB_PLAN_SUMMARY=…"}      -> planTaskId
# an agent definition so the CLI resolves a stable worktree + name
api POST …/agents  {name: "planner-$RUN_ID", role_name: "plan", auto: false, cross_repo: false, repos: [<repo>], backend: "codex"}
# launch (run: step)
bin="$AFT_WORK_DIR/pl-bin"; mkdir -p "$bin"
ln -sf "$AFT_TESTS_DIR/../../tmp/loom-e2e"      "$bin/loom"
ln -sf "$AFT_TESTS_DIR/../../e2e/stubs/codex"   "$bin/codex"
LOOM_CONFIG_DIR="$AFT_TESTS_DIR/../../tmp/e2e-workspace/.loom-config" \
LOOM_SERVER_URL="$AFT_API_URL" LOOM_WORKSPACE_ID=E2E-WS-PLANRUN \
LOOM_AFT_RUN_ID="$RUN_ID" \
STUB_CODEX_PLAN_RUNNER=1 PATH="$bin:$PATH" \
"$AFT_TESTS_DIR/../../tmp/loom-e2e" --workspace E2E-WS-PLANRUN --backend codex \
  plan "planner-$RUN_ID" > "$AFT_WORK_DIR/pl-agent.log" 2>&1
```

**`LOOM_AFT_RUN_ID`, never `RUN_ID` (r3 — this bit the plan once already).** `RUN_ID` is exported
by `run-aft.sh:328` and *is* visible to `run:` steps (aft passes `...process.env`,
`../testing-app/src/steps.ts:169-183`), so the launch line above can read it. But the **stub does
not run in that environment** — it is a backend subprocess launched by loom through
`buildBackendEnv` → `cli.FilteredEnv()` (`backend_codex.go:115-124`), which admits only exact
allowlist entries plus the `LOOM_`/`DAYTONA_` prefixes
(`internal/cli/envfilter/envfilter.go:8-40`, prefixes at `:83-91`). `RUN_ID` matches neither, so inside the stub
it is the empty string and every `…-${RUN_ID}` marker assertion would compare against a truncated
literal that still *looks* plausible. Re-export it as `LOOM_AFT_RUN_ID`, which survives on the
prefix, and have the stub embed **both** that value and the selected **task ID** so a marker
mismatch is unambiguous. `STUB_CODEX_PLAN_RUNNER` itself needs an exact-name allowlist entry —
see PLN-B1.

Why plain `loom plan <agent>` (no `--auto`, no `--daemon-mode`): it takes
`runPlanSingleTask` (`plan.go:198-242`), which self-selects one qualifying task, renders
`planning.md`, and exits — bounded, tmux-free, TTY-free. Because aft's `run:` step has no TTY,
`defaultCodexInvoker` falls through to the headless branch
(`internal/cli/backends/backend_codex.go:63-70`) and invokes
`codex exec --json --dangerously-bypass-approvals-and-sandbox "<prompt>"`
(`backend_codex.go:105-112`) — i.e. exactly the `IS_EXEC && IS_JSON` branch the existing stub
already dispatches on (`e2e/stubs/codex:125-136`).

#### PLN-D13 — a stubbed planner writes a design and moves the task to review

- **Tier:** PC (SEAM-gated) · **Kind:** happy
- **Intent:** `A planning agent claims the design-less task, writes its design, and hands it to review while an operator watches the board`
- **Steps:** preamble → `open: /ws/E2E-WS-PLANRUN/kanban` → `wait: { text: planner task }` →
  launch `run:` step → bounded poll `run:` step (`for i in $(seq 1 30); … grep -q
  '"status":"review"' … sleep 2`, intent *Wait until the planned task reaches review status*)
  → board assertions.
- **Assertions**
  - `api: GET …/issues/{planTaskId}` → `status == "review"`, `assignee` empty (the prompt
    clears it, `planning.md:156`), `has_design == true`, and `design` contains the stub's
    marker (`AFT-PLAN-MARKER $LOOM_AFT_RUN_ID` — **not** `${RUN_ID}`, see the preamble note) plus
    the selected task ID, and the `## Summary` heading. Note the `${var:}`/`$RUN_ID` form is fine
    in the *suite's* `expect`/`wait` steps; the constraint is only on what the stub can read.
  - Board: the card leaves the Ready column and appears under
    `section[data-status=review]` — assert with the same `Array.from(...).some(...)` shape as
    `zz-agent-flow.test.yaml:156` and `real-suites/zz-real-codex-epic.test.yaml:70-74`. Use the
    **flat** board (`?groupBy=none`) if the epic-grouped lane proves flaky (FINDINGS §1.5 is
    fixed, but the flat board is the settled assertion surface).
  - agents-list `run:` readback → the planner's `state` is not `active` after the process exits.
- **Edge rationale:** the headline scenario. It is also the fixture every other case in Group C
  reuses.

#### PLN-D14 — the design renders in the issue detail Design panel

- **Tier:** PC (SEAM-gated) · **Kind:** happy
- **Intent:** `A reviewer opens the planned task and reads the design the planning agent wrote, section by section`
- **Steps:** after PLN-D13, open the board, click the card (or deep-link the issue), wait for
  `[data-testid=issue-detail-panel][data-state=open]`.
- **Assertions:** `expect: { visible: { testid: design-section } }` (only rendered when
  `issue.design` is truthy — `IssueDetailPanel.tsx:1617-1627`);
  `expect: { visible: { testid: design-panel } }`;
  `expect: { count: { testid: design-empty, equals: 0 } }`;
  `expect: { count: { testid: design-panel-section, atLeast: 3 } }` — the stub's design must
  contain at least three `## ` headings so `splitIntoSections`
  (`sections/DesignPanel.tsx:54-86`) produces collapsible sections; then
  `click` one section header button and assert `aria-expanded` toggles;
  `expect: { visible: { testid: markdown-content } }` and the marker text is present.
- **Edge rationale:** proves the write→render round trip end to end, and gives the
  `design-panel-section` collapse control its first real content (today `settings-design-format`
  only exercises the HTML branch with a fabricated fixture).

#### PLN-D15 — the planned task is a plan review, and panel Approve returns it to `open`

- **Tier:** PC (SEAM-gated) · **Kind:** happy
- **Revision 2:** rewritten. Revision 1 drove Approve from `/prs`, which **closes** the issue,
  not reopens it (see PLN-D15b). The `plan → open` branch is only reachable from the **issue
  detail panel's review action bar**.
- **Intent:** `A reviewer opens the planned task, sees it presented as a plan review, and approves the design so it returns to open for implementation`
- **Mechanism (verified):** `getReviewType` returns `"plan"` for `status === "review"` with no
  PR URL (`utils/issue/issueCategory.ts:110-130`). `IssueDetailPanel` renders its review action
  bar for any review item when both `onApprove` and `onReject` are wired
  (`IssueDetailPanel.tsx:1431-1449`) — `App.tsx:1449-1461` wires them. `App.handleApprove` then
  branches on the review type and, for `"plan"`, calls `updateIssueStatus(issue.id, "open")`
  (`App.tsx:713-730`).
- **Steps:** open the board → click the planned card → `wait.fn` that
  `[data-testid=issue-detail-panel]` has `data-state="open"` →
  `expect: { visible: { testid: review-action-bar } }` →
  `expect: { visible: { testid: panel-approve-button } }` →
  `click: { testid: panel-approve-button }` → `wait.fn` that the panel closed
  (`handlePanelClose` runs on success, `App.tsx:735`).
- **Assertions:** `api: GET …/issues/{planTaskId}` → `status == "open"`, `has_design` still
  true, `assignee` still empty — i.e. the task is now *implementable*, which is the whole point
  of the plan-review stage. Also assert the card returns to the Ready column on the board.
- **Rationale:** this is the only path in the product that closes the planner→implementer
  handoff loop, and it is the one Approve semantic that differs by review type.

#### PLN-D15b — the same plan review approves to a **different outcome** on `/prs`

- **Tier:** SF · **Kind:** edge · **New in r2** · **Needs no seam**
- **File:** `surface-suites/planner-contracts.test.yaml` (fabricated fixture — a design set
  directly by an `api:` step, no stub and no planning run; that is precisely what the surface
  tier is for per `CONTEXT.md`'s *surface suite*). It therefore does **not** depend on
  PLN-B1/PLN-B2 and can be written today.
- **Intent:** `An API client and a reviewer confirm that approving a plan review from the PR review workspace closes the issue instead of returning it to open`
- **Mechanism (verified):** `/prs?review=<id>` mounts `PRReviewWorkspace`
  (`views/PRsPage.tsx:353-366`), whose `decide("approve")` calls `applyReviewDecision`
  (`PRReviewWorkspace.tsx:361-370`) → `POST /issues/{id}/review-decision` →
  `ReviewDecisionService.applyApproval` → **`CloseIssue`**
  (`internal/webui/service/review_decision.go:110-121`). There is no review-type branch: only a
  GitHub-PR guard (`:62-64`). The toast even reads `<id> approved and closed`
  (`PRReviewWorkspace.tsx:364`).
- **Steps:** `api: POST …/issues` with a `design:` body and `issue_type: "task"` →
  `api: PATCH …/issues/{id}` `{"status":"review"}` (the fabricated stand-in for a planner
  outcome; no `external_ref`, so `getReviewType` returns `"plan"`) → `open: /ws/…/prs` → assert
  the row label is `Plan review` (`views/PRsPage.tsx:115-118`) → click through to
  `pr-review-workspace` (retry loop from `surface-suites/review-actions.test.yaml:38-44`) →
  `click: { role: button, name: Approve }` → read back.
- **Assertions:** `status == "closed"` — deliberately the **opposite** of PLN-D15 on the same
  kind of issue.
- **Why surface:** it pins an inconsistency rather than a coherent user scenario. **File as a
  FINDINGS entry:** one plan review, two Approve buttons, opposite outcomes (`open` vs
  `closed`). Whichever is intended, the other is a bug; when unified, this case merges into
  PLN-D15.
- **Narrowed promotion claim (r2):** revision 1 said a planner design promotes
  FINDINGS §1.19/§3.9 outright. That is too broad — §1.19/§3.9 ask for **branch, commit, PR, or
  diff** content for a reviewer to inspect (`FINDINGS.md:260-266`, `:377-379`), which a design
  does not supply. The accurate claim: a planner design makes the *plan-review* fixture
  non-hollow (there is now something to read before approving), so the plan-review scenario can
  live in the product-correctness tier; the **code-review** half still needs the git seeding
  §3.9 describes, and §1.19 stays open for it.

#### PLN-D16 — Reject re-arms the planner (the `needs-revision` loop)

- **Tier:** PC (SEAM-gated) · **Kind:** edge
- **Revision 2:** moved off `/prs` onto the issue detail panel's reject form, which is the
  same surface as PLN-D15 and has full testid coverage.
- **Intent:** `A reviewer rejects the design with feedback, the task returns to the planning queue with the needs-revision label, and a second planning run replaces the design`
- **Mechanism (verified):** panel Reject opens `RejectCommentForm`
  (`IssueDetailPanel.tsx:1443-1465`), whose submit calls `App.handleReject`
  (`App.tsx:761-782`) → `applyReviewDecision("request_changes")` →
  `applyChangeRequest`, which sets `status = "open"`, appends the `needs-revision` label, and
  appends a `[review-decision:<id>] <actor> requested changes: <reason>` note
  (`review_decision.go:124-149`). Reject is therefore **consistent** with the `/prs` surface —
  only Approve diverges (PLN-D15b). `NeedsPlan` then returns true again *despite* the design
  (`taskfilter.go:58-60`), so the planner re-selects it (`planning.md:23-34` Step 1.5).
- **Steps:** after PLN-D13, open the planned card → `click: { testid: panel-reject-button }` →
  `wait.fn` on `reject-comment-form` →
  `fill: { testid: reject-textarea, value: "AFT-PLAN-FEEDBACK ${RUN_ID}: tighten the testing strategy" }`
  → `click: { testid: reject-submit }` → readback (`status == "open"`, `labels` contains
  `needs-revision`, `notes` contains `AFT-PLAN-FEEDBACK`) → run the launch step a
  second time → poll for `status == "review"` again.
- **Assertions (hard oracle, fixed in r3):** after run 2 —
  1. `design` contains the **second-run marker** `AFT-PLAN-REVISION $LOOM_AFT_RUN_ID` (the stub
     detects the `needs-revision` label and writes a distinguishable design; see PLN-B1 step 4),
     and no longer contains the first-run marker;
  2. `status == "review"`;
  3. **`labels` still contains `needs-revision`.** This is a definite expectation, not an
     observation: `applyChangeRequest` adds the label (`review_decision.go:126-129`) and nothing
     removes it — the planning prompt only instructs the agent to *document* changes in notes
     (`planning.md:150-157`), and the stub must not remove it either. Revision 1's "assert the
     observed label state and comment it" was not an oracle; this is.
  4. `notes` still contains the run-1 rejection marker (the note is appended, not replaced —
     `review_decision.go:131-141`).
- **Follow-on invariant worth asserting:** because the label survives and the status is `review`,
  the task is **not** re-selectable by a third run (`IsWorkableTask` requires `status == "open"`,
  `taskfilter.go:71-73`). A third launch must report no candidate — cheap to add and it proves
  the loop terminates.
- **Edge rationale:** the only loop in the product where a *human* UI action re-queues an
  *agent*. It is also the one case that proves `NeedsPlan`'s second disjunct is live.

#### PLN-D17 — the planner skips epics and already-designed tasks

- **Tier:** PC (SEAM-gated) · **Kind:** edge
- **Intent:** `A planning agent leaves the epic and the already-designed task untouched and plans only the design-less one`
- **Preconditions (r3 — a fourth fixture added):** four issues in one epic —
  (a) the epic itself; (b) a task created with `design: "pre-existing design $RUN_ID"`;
  (c) a design-less task; **(d) a design-less issue of a non-work type** — `issue_type: "gate"`
  (or `"message"`), which `IsNonWorkType` excludes (`taskfilter.go:22-30`, seven types:
  `merge-request, gate, molecule, message, agent, role, rig`).
- **Steps:** launch one planning run → poll until (c) is `review`.
- **Assertions:** (a) epic `status` unchanged and `design` empty; (b) `status` unchanged (`open`)
  and `design` **byte-identical** to the seeded string; (c) planned; **(d) `status` unchanged and
  `design` empty**. Board: only (c) leaves the Ready column.
- **Edge rationale (claim corrected in r3):** revision 1 said "all three rejection arms" while
  testing two. With (d) this pins **three of the four** arms of `IsWorkableTask ∧ NeedsPlan`
  (`taskfilter.go:71-73,105-107`): not-an-epic, not-a-non-work-type, and already-has-design. The
  fourth — `status == "open"` — is covered incidentally by **PLN-D18** (every remaining candidate
  closed or designed) and by PLN-D16's terminating third launch. A drift here (a planner
  overwriting an approved design, or planning a `gate` record) is silent data loss.
- **Fixture caveat — verify (d) on the first run.** No `issue_type` whitelist exists on the loom
  create path (`service/issue.go:85`), so `gate` is expected to pass through to fleet-db, but that
  is not proven here. If the create is rejected, drop arm (d), note the rejection as the
  *reason* the arm is unreachable from the API, and leave the non-work-type filter to its Go
  coverage. Do not silently weaken the test's stated claim again.

#### PLN-D18 — nothing to plan: the planner exits cleanly and mutates nothing

- **Tier:** PC (SEAM-gated) · **Kind:** edge
- **Intent:** `A planning agent started against a board with no design-less work exits without claiming or changing anything`
- **Steps:** ensure every open task has a design (or close them) → launch the run → assert the
  process exit code is 0 and its stdout contains
  `No tasks available for planning.` and
  `Tasks must be: open status, no design (or has needs-revision label), not epics`
  (`plan.go:206-210`).
- **Assertions:** no issue changed status; `GET …/tasks/*/sessions` still empty; no lock file
  remains (the Go smoke test `plan_smoke_test.go:107` asserts the same invariant at unit
  level — this is the live-stack twin).
- **Edge rationale:** the "empty queue" path is where a badly written stub would silently plan
  the wrong thing; it also proves the stub is not fabricating work.

#### PLN-D19 — HTML design format flows through the planning prompt to the renderer

- **Tier:** PC (SEAM-gated) · **Kind:** edge
- **Intent:** `An operator who set the workspace design format to HTML sees the planning agent's design render as sanitized HTML`
- **Mechanism:** `resolveDesignFormat` reads the workspace config
  (`internal/cli/agent/prompts.go:49-54`), the prompt then instructs semantic HTML
  (`planning.md:85-90`), the agent passes `--design-format=html`
  (`planning.md:144`, validated at `internal/cli/data/update.go:118-134`), and
  `DesignPanel` picks `HtmlDesignRenderer` when `format === "html"`
  (`sections/DesignPanel.tsx:103-108,232-233`).
- **Steps:** drive the real settings toggle (`design-format-select` + `design-format-save-button`,
  already exercised by `suites/settings-design-format.test.yaml`) to `html` → seed a design-less
  task → launch the run → open the issue.
- **Assertions:** `expect: { visible: { testid: design-html-content } }`;
  `expect: { count: { testid: markdown-content, equals: 0 } }`; readback
  `design_format == "html"` and `design` starts with an HTML block tag; and — reusing the
  markdown-safety discipline — a `wait.fn` asserting no injected global fired.
- **Blocker note:** per-issue `design_format` on PATCH is FINDINGS §1.13 (whole-PATCH 400 on
  fleetdb). This case deliberately uses the **workspace-level** toggle, which is the supported
  path, and the agent's own `loom data update` writes the per-issue value through the CLI
  backend rather than the webui PATCH surface. If that write 400s, that is a *new* datapoint
  for §1.13 and should be logged.

#### PLN-D20 — the planning run records a session whose phase is `planning`

- **Tier:** PC (SEAM-gated) · **Kind:** happy
- **Intent:** `A reviewer opens the planned task's Runs tab and inspects the recorded planning session and its transcript`
- **Steps:** after PLN-D13, open the issue → `click: { role: tab, name: Runs, exact: true }` →
  `wait.fn` on `sessions-tab` + `session-timeline` → click `session-row-<id>`.
- **Assertions:** `api: GET …/tasks/{planTaskId}/sessions` → `data.sessions.0` exists;
  `data.sessions.0.backend == "codex"`; the row's phase badge carries `data-phase="planning"`
  (`sessions/SessionTimelineRow.tsx:88-125`); `session-detail-view` visible;
  `session-inner-tab-transcript` visible. **Do not** assume `has_diff` — a planner produces no
  diff by design, so assert `has_diff == false`, `files_changed == 0`, and the disabled Diff
  sub-tab exactly as `zz-agent-flow.test.yaml:222-225` pins it for the stub task run.
- **Edge rationale:** `files_changed == 0` is *correct* for a planner (unlike the task runner,
  where FINDINGS' "suspected: local-task-runner stub sessions record zero diff evidence" flags
  it as a possible gap). Pinning it here separates the two meanings.
- **Seam caveat, narrowed in r2 — no `seed-session` needed.** Revision 1 assumed a CLI-launched
  planner might be invisible to `…/tasks/{id}/sessions` because the supervisor is what creates
  the control-plane `AgentSession` (`supervisor.go:517-563`). Verified: `ListTaskSessions` tries
  the control plane **first, then falls back to local session stores**
  (`svcimpl/session_service.go:156-197`), and `storesForWorkspace` searches the serve runtime
  dir, the workspace root, and **every repo path** (`:56-90`). A local session written by the
  CLI planner is therefore visible. The two real requirements, which the suite must satisfy
  explicitly:
  1. **Shared runtime roots.** `createAgentSession` writes to `cli.GetWorkspaceRuntimeDir()`
     (`plan.go:264`), so the launch step must use the same
     `LOOM_CONFIG_DIR=tmp/e2e-workspace/.loom-config` / workspace root serve itself resolves —
     which the Group C preamble already does.
  2. **The session must carry the task ID.** `finalizeAgentSession` recovers it from the **lock
     file** at the worktree (`plan.go:291-294`), and `SessionsByTask` is what the endpoint
     queries. `persistAssignedTaskToLock` only runs in daemon mode (`plan.go:147`), so in
     single-task mode the linkage depends entirely on the stub running `loom claim <id>`, which
     writes the lock at its **CWD** (`internal/cli/agent/claim.go:41-57`). The backend is
     invoked with `cwd = worktreePath` (`backend_codex.go:86`, `buildBackendEnv`), so the stub
     must not `cd` anywhere. Captured as a hard requirement in **PLN-B1** step 3.
  Status therefore moves from *blocked-on-seam PLN-B3* to *ready once PLN-B1/B2 land*. If the
  live run still shows no session, that is a **new** finding about local-store scoping
  (`sessionStoreIsWorkspaceScoped`, `session_service.go:167`), not a missing seed command.

#### PLN-D24 — an epic-bound planner plans only inside its epic

- **Tier:** PC (SEAM-gated) · **Kind:** edge · **New in r2**
- **Intent:** `A planning agent bound to one epic plans a design-less task inside that epic and leaves an equally eligible task in another epic untouched`
- **Mechanism (verified):** binding is `agent.Parent`, which is PATCHable
  (`service/agent.go:103`) and is exactly what epic-runner sets via `loom.agents.updateParent`
  (`epic-runner.ts:547-556`). Both launch paths append it: `appendParentArg`
  (`handlers/terminal/agent_session.go:405,423-428`) and `spawn.go:98-99`. Inside the command,
  `--parent` (`plan.go:77`) flows into `HasAvailablePlanningTasks(planParentID, …)`
  (`plan.go:199-201`), into the session's `EpicID` (`plan.go:224` →
  `createAgentSession(agentName, parentID, …)` → `EpicID: parentID`), and into the prompt, where
  it rewrites task discovery to `loom data ready --parent <epic> --limit 200 --output json` and
  injects
  `**Epic scope: <id>** — You MUST only select tasks from this epic. Do not work on tasks from other epics.`
  (`prompts.go:254-262`).
- **Preconditions:** **two** epics, each with one design-less child task, both priority 2 so
  neither is the obvious global winner. `PATCH /agents/{planner} {"parent":"<epicA>"}`.
- **Steps:** patch the parent → launch with `--parent "$(cat "$AFT_WORK_DIR/epicAId")"` (or rely
  on the argv builder if launching through the terminal path) → poll until epic A's child is
  `review`.
- **Assertions:** epic A's child → `status == "review"` with a design; **epic B's child →
  `status == "open"`, `design` empty** (the load-bearing negative); the recorded session's
  `epic_id` equals epic A; agents-list readback → `parent == "<epicA>"`.
- **Stub requirement (r3):** the launch flag alone does **not** make this test meaningful. The
  CLI's precheck is scoped (`HasAvailablePlanningTasks(planParentID, …)`, `plan.go:199-201` →
  `fetchReadyIssues`, `automode_poller.go:64`), but the **stub does its own selection**, so an
  unscoped `loom data ready` inside the stub could pick epic B's task and the assertions would
  fire for the wrong reason. PLN-B1 step 2a is now required: the stub extracts the scope from the
  prompt's `**Epic scope: <id>**` line and passes `--parent` to its own `loom data ready`.
- **Scope of the claim (r3):** launching with `--parent` on the command line covers the **flag**,
  the availability filter, and the prompt's scope directive. It does **not** cover the
  `agent.Parent` → argv plumbing (`appendParentArg`, `agent_session.go:423-428`;
  `spawn.go:98-99`) — only the terminal/supervisor launch paths build argv from the stored field.
  Set `agent.Parent` via PATCH anyway and assert it reads back, but state plainly in the YAML
  comment that the plumbing itself is covered by Go tests, not by this case. Promoting that half
  requires PLN-B2 option (b).
- **Edge rationale:** epic scoping is how the product runs more than one planner concurrently
  without collisions, and it is the only planner input that changes both the *selection query*
  and the *prompt text*. A regression here has no other detector — and it is a
  cross-epic-contamination bug class, i.e. one planner overwriting another epic's work.

---

### Group D — surface tier

#### PLN-D21 — planner create-contract probes

- **Tier:** SF · **Kind:** edge
- **File:** `surface-suites/planner-contracts.test.yaml`
- **Intent:** `An API client probes the agent-create contract for the plan role and gets the documented validation errors`
- **Cases (each an `api:` step with an explicit `status:`):**
  | body | expect |
  |---|---|
  | `{"name":"","role_name":"plan"}` | 400, error contains `missing agent name` |
  | `{"name":"-bad-","role_name":"plan"}` | 400, error contains `invalid agent name` |
  | `{"name":"p1","role_name":""}` | 400, error contains `role_name required` |
  | `{"name":"p2","role_name":"plan","kind":"bogus"}` | 400, `invalid role kind` |
  | `{"name":"p3","role_name":"plan","workspace_key":"OTHER"}` | 400, `workspace_key must match request workspace` |
  | `{"name":"p4","role_name":"plan","backend":"not-a-backend"}` | **201** — there is *no* unknown-backend validation on create |
- **Why surface:** these are standalone contract probes with no user scenario
  (`CONTEXT.md`: *surface suite*). Promotion condition: none — they are contracts by nature.
- **Value:** the last row documents a real asymmetry worth a FINDINGS line (a planner can be
  created pointing at a backend that can never resolve; the failure only surfaces later as
  `backend_unavailable`, which the UI cannot render — PLN-B5).
- **Safety note (r2):** the `not-a-backend` row is safe precisely because nothing ever *starts*
  that agent. Never use a real-but-unstubbed backend name (e.g. `gemini`) for this row — if a
  later case launched it, `exec.LookPath("gemini")` would find the operator's host CLI
  (`backend_gemini.go:121`); `not-a-backend` cannot resolve to anything.
- **Assertion mechanics:** each row's `status:` and top-level `error` are dot-path scalars, so
  these are genuine `api:` steps; the 201 row must then be cleaned up with a `DELETE` in the
  same test (or the suite teardown).

#### PLN-D22 — `backend_unavailable` is not writable through the agent PATCH surface

- **Tier:** SF · **Kind:** edge
- **Intent:** `An API client confirms the agent state surface rejects backend_unavailable even though the domain defines it`
- **Steps:** `api: PATCH …/agents/{planner}` with `{"state":"backend_unavailable"}` →
  `status: 400`, error `invalid state`.
- **Why surface:** it asserts a *gap*, not a scenario. It exists so that closing **PLN-B5**
  (making the state writable or seedable) breaks this test loudly and forces PLN-D12 to be
  written. Cross-reference `handlers/agents/handlers.go:252-259` and
  `internal/domain/agent.go:19` in the YAML comment.

#### PLN-D23 — the planner is absent from the assignee picker

- **Tier:** SF (regression guard for a product gap) · **Kind:** edge
- **Revision 2:** the Start Work half is **removed as infeasible**. `StartWorkButton` is
  exported (`actions/index.ts:5-6`) but **mounted nowhere** — the only production reference is a
  stale comment, `IssueDetailPanel.tsx:603` ("Agent data for StartWorkButton"). Its
  `start-work-button`, `start-work-popover`, and `agent-option-<name>` testids are therefore
  **unreachable in a browser**, and any step targeting them would fail on a missing element
  rather than prove anything. The case is now assignee-dropdown only.
- **Intent:** `An operator opening the assignee picker on a design-less task finds no Planner to delegate to`
- **Mechanism (verified):** `AssigneeDropdown` hard-filters the planner out —
  `if (agent.role && agent.role !== "task") continue;`
  (`components/IssueDetailPanel/fields/AssigneeDropdown.tsx:119-120`) — and it is the component
  the panel actually mounts (`IssueDetailPanel.tsx:1380-1386`).
- **Steps:** with both a planner and a task agent present, open a design-less **open** issue →
  open the assignee dropdown → assert `agent-assignee-<taskAgent>` is visible
  (`AssigneeDropdown.tsx:416`) and `expect: { count: { testid: "agent-assignee-<planner>", equals: 0 } }`.
- **Why this is important:** per `CONTEXT.md`, *delegation* ("assignment and starting are one
  gesture, on every surface") is a first-class product concept — and the plan stage has **no**
  delegation surface at all: one filter excludes the planner, and the component that *has* a
  `preferredRole: "plan"` option is dead code. Log as **PLN-B6**; when fixed, this test inverts
  and moves to the product-correctness tier as "an operator delegates a design-less task to the
  Planner and the planner starts planning it".

---

## Part 2 — Real-backend tier (non-deterministic)

Mirrors the HELLO.md pattern: `real-suites/zz-real-codex-epic.test.yaml` seeds an epic + child
task carrying a tiny deterministic DESIGN, triggers execution, polls the API for a terminal
state, then asserts session/transcript/diff/file-on-disk. The planner analogue inverts the
artifact: **the design is the deliverable and the working tree must stay clean.**

New files:
- `tests/aft/real-suites/zz-real-codex-plan.test.yaml` (codex; `make test-aft-real`)
- `tests/aft/real-suites-<claude|cursor|opencode>/zz-real-<backend>-plan.test.yaml` (PLN-R1 only)

Gating and preflight are unchanged (`run-aft.sh:116-207`): `AFT_REAL_BACKEND`, per-tier stub
farm `e2e/stubs-real-<backend>/`, `AFT_TIMEOUT` default `600000`, provider API keys unset.
Teardown: `close-open-issues.sh` + `scripts/real-backend-teardown.sh <backend>` (extend its
agent-name regex to also match `^real-<backend>-plan($|-)`).

**Launch:** the real tier cannot use epic-runner (no planning phase). Use the same isolated
`loom … plan <agent>` invocation as Group C, minus the stub symlink so the operator's real CLI
resolves — the shape already proven by
`real-terminal-suites/zz-real-terminal-logs.test.yaml:47-48`. Budget the poll in chained
`run:` windows (each ≤ 120 s, aft's per-step ceiling) the way
`real-suites/zz-real-codex-epic.test.yaml:59-67` does; a planning run reads more of the repo
than the HELLO.md task, so budget **~8 min** (four windows: three grace + one asserting).

### PLN-R1 — a real planner writes a design and hands the task to review

- **Kind:** happy · **Backends:** codex (primary), claude / cursor / opencode variants
- **Intent:** `The real <backend> CLI, running as the workspace Planner, selects the seeded design-less task, writes a design, moves it to review, and leaves the working tree untouched`
- **Fixture:** epic + child task whose **description** (not design — the design must stay
  empty or the task is filtered out) carries a small, checkable ask, e.g.
  `Add a file HELLO.md at the repository root containing exactly: hello world.`
  Priority 2, so it is the unambiguous highest-priority candidate.
  First step per tier: `PATCH /api/workspaces/E2E-WS/config/backend` to force the default
  backend (README.md:104-107).
- **Poll:** `status == "review"`.
- **Assertions**
  1. `data.status == "review"`, `assignee` empty, `has_design == true`.
  2. **Design is substantive, not boilerplate:** length ≥ 400 chars; contains at least three of
     `Summary`, `Technical Approach`, `Files to Create`, `Files to Modify`, `Testing Strategy`,
     `Acceptance Criteria`; mentions `HELLO.md`.
  3. **Board:** the card renders in the Review column (`section[data-status=review]`).
  4. **Detail panel:** `design-section` + `design-panel` visible with ≥ 2
     `design-panel-section` nodes.
  5. **Review queue:** the row's state label is `Plan review` (`views/PRsPage.tsx:115-118`).
  6. **Session evidence** (mirroring `zz-real-codex-epic.test.yaml:77-124`):
     `data.sessions.0.backend == "<backend>"`, `exit_code == 0`,
     `evidence.status == "ok"`, transcript 200 with ≥ 1 non-system entry containing tool
     activity, `usage_status ∈ {reported, unavailable}` with the symmetric token-field check.
- **Real-vs-stub discriminators (all required):**
  - the design contains prose the stub cannot produce — assert it references at least one
    **real path from the repo** (e.g. the seeded repo's own file name) rather than a fixed
    marker string;
  - the transcript contains ≥ 1 `role == "tool"` entry or a `tool_use` blob;
  - `files_changed == 0` **and** `has_diff == false` (see PLN-R4 — for the planner the
    *absence* of a diff is the discriminator, because the stub plan-runner writes none either;
    so the design-content and transcript checks carry the discrimination weight here).
- **Per-backend judgement:** run PLN-R1 on **all four** — it is a single bounded run and the
  prompt's `loom data update --design=<very large string>` argv is exactly the kind of thing
  that breaks differently per CLI (argv length limits, quoting, stdin vs positional prompt —
  `local-task-runner.ts` `backendUsesStdinPrompt`). Everything else below is **codex-only**.

### PLN-R2 — a real planner revises a rejected design

- **Kind:** edge · **Backends:** codex only
- **Intent:** `A reviewer requests changes on the real planner's design and a second real planning run produces a revised design that addresses the feedback`
- **Steps:** PLN-R1 → in the browser, `Request changes` in the review workspace (readback:
  `status == "open"`, `labels` contains `needs-revision`, `notes` contains `requested changes`)
  → relaunch the real planner → poll to `review`.
- **Assertions:** the design **changed** (compare a hash of run-1's design to run-2's), run 2's
  design references the rejection note text, and the task is back in `review`.
- **Rationale:** `planning.md:23-34` Step 1.5 is an entire prompt branch with no coverage of any
  kind; it is also the only place the planner *reads* a human's feedback.

### PLN-R3 — a real planner honours the HTML design format

- **Kind:** edge · **Backends:** codex only
- **Intent:** `With the workspace design format set to HTML, the real planner emits semantic HTML and the board renders it sanitized`
- **Assertions:** `design_format == "html"`; the design starts with a block-level tag and
  contains ≥ 2 `<h2>`; `design-html-content` is visible and `markdown-content` is absent; the
  design contains **no** `<script>`, `<img>`, or `data:` URI (`planning.md:89` forbids them);
  if an inline `<svg>` is present, it carries explicit `width`/`height`/`viewBox`.
- **Rationale:** the HTML branch of the planning prompt is the only prompt text that steers
  *rendered output*; today only a fabricated fixture exercises `HtmlDesignRenderer`
  (`surface-suites/design-format-legacy.test.yaml`).

### PLN-R4 — a real planner does **not** implement

- **Kind:** edge · **Backends:** codex only · **Highest-signal negative in the whole plan**
- **Intent:** `The real planner leaves the repository unchanged: no new files, no commits, and no code diff, only a design`
- **Assertions (filesystem-level, the same class of proof as the HELLO.md check at
  `zz-real-codex-epic.test.yaml:135-141`, inverted):**
  - `find "$AFT_TESTS_DIR/../../tmp/e2e-workspace" -name HELLO.md` returns **nothing**;
  - `git -C <workspace repo> status --porcelain` in every agent worktree is empty;
  - `git -C <repo> rev-parse HEAD` matches the pre-run HEAD captured in an earlier `run:` step;
  - session `files_changed == 0`, `has_diff == false`, session diff endpoint returns 404 with
    `{"success":false,"error":"diff not found"}` (the contract `zz-agent-flow` already pins).
- **Rationale:** `planning.md:169-183` ("CRITICAL: STOP — DO NOT IMPLEMENT") is enforced **only
  by prose**: the `read_only` flag seeded on the fleet-db `plan` role
  (`workspacemgr/workspace_store.go:486`) and the `LOOM_READ_ONLY` / `LOOM_ALLOWED_TOOLS` /
  `LOOM_DENIED_TOOLS` env vars the supervisor exports (`supervisor/spawn.go:126-132`) have **no
  production reader** — `ReadOnlyPreamble()` (`prompts.go:520-527`) is called from no non-test
  file. This test is the only mechanism that would catch a model deciding to implement anyway.
  Log the dead flag as a FINDINGS entry (**PLN-B10**).

### PLN-R5a / PLN-R5b — missing backend auth fails fast, and carries an error class

- **Status (r3): split.** **R5a** (fail-fast, task untouched) is **ready-to-write**. **R5b** (the
  classified error) is **BLOCKED on PLN-B15** — it was listed as ready in revision 2, but the
  class is not observable through either readback that revision proposed. Assertions are split
  into parts A and B below; write A now, B after PLN-B15.
- **Kind:** edge · **Backends:** codex only (an explicitly *unauthenticated* variant)
- **Intent:** `A planning run started with no backend credentials fails immediately with an auth error class instead of hanging or silently doing nothing`
- **How:** launch the planner with the credential directory redirected to an empty temp dir
  (`CODEX_HOME=$AFT_WORK_DIR/empty-codex`, which is on the env allowlist —
  `internal/cli/envfilter/envfilter.go:37` (exact-name entry)) and every provider key unset.
- **Harness-preflight compatibility (r2 — required design property):** `run-aft.sh` refuses to
  start the codex tier unless `$HOME/.codex/auth.json` exists (`run-aft.sh:180-184`), and that
  check runs **before** aft launches. Redirecting `CODEX_HOME` inside the suite's own `run:`
  step affects only that child process, so the harness preflight still passes and this case
  exercises the **product's** preflight, not the harness's. Do **not** implement this case by
  moving/renaming `~/.codex` or by unsetting `HOME` — either would trip `run-aft.sh` first and
  the suite would never run.
- **Assertions — R5a (part A), writable today:** the process exits non-zero within the first poll
  window; the task is **still** `open` with no design and no assignee; the recorded session, if
  one exists, has `exit_code != 0`; the captured stderr names the missing credential. This much is
  a genuine fail-fast test and can ship as **PLN-R5a** without PLN-B15.
- **Assertions — R5b (part B), blocked:** the classified `error_class`. Revision 2 proposed reading it
  from `last_error_class` via the agents list or "the process stderr otherwise". Neither works:
  - `finalizeAgentSession` records the exit code and usage but **never** the class
    (`plan.go:286-318`), so the session projection cannot carry it;
  - the class exists only on the emitted `TaskFailed` event (`plan.go:415-431`), which the agent
    bus writes to `LOOM_EVENTS_DIR` / `<LoomDir>/events`
    (`internal/cli/agent_event_bus.go:40-50`) while serve reads a daemon-config-derived directory
    that resolves to `""` without a daemon config (`observability.go:336-350`);
  - `last_error_class` is a fleet-db passthrough sourced from the agent's most recent terminal
    **control-plane** session (`internal/domain/agent.go:73-77`) — a local CLI run does not
    populate it;
  - the class is a *bucket name* (`agenterr.ClassifyFromOutput`), not the raw message, so stderr
    text is not a substitute — asserting on stderr tests the CLI's phrasing, not the classifier.
  R5b unblocks via either option in **PLN-B15**; option (2) (record `error_class` on the
  session) also makes it assertable in the deterministic tier.
- **Cost:** ~zero — the CLI fails before any model call, which makes **R5a** the cheapest
  real-tier case and a good candidate to run first as a preflight canary.

### PLN-R6 — a real planner respects the timeout budget

- **Status: BLOCKED (reclassified in r3, was "ready-to-write, defer").** Not implementable with
  aft's current step model — see the harness wall below. Do not attempt it until a
  background-process protocol exists.
- **Kind:** edge · **Backends:** codex only · **Manual tier at best**
- **Intent:** `A planning run that exceeds its budget is terminated and leaves the task unclaimed rather than half-planned`
- **Product-lever gap:** `LOOM_LOCAL_TASK_TIMEOUT_MS` governs the *task-runner* path
  (`internal/workflows/builtin/local-task-runner.ts` `execBackend`); for the CLI planner path the
  equivalent lever is the supervisor's `LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS`
  (`supervisor/restart.go:378-395`), which only applies **under a daemon**. So there is no
  planner timeout to test in the daemonless stack.
- **Harness wall (the reason this is blocked, not merely flaky):** revision 2 said "without a
  daemon the bound is the harness's own poll window" and then hung assertions off that kill. aft
  runs every `run:` step as `execFileP('bash', …, { timeout: 120_000 })` and converts a timeout
  into a thrown step failure (`../testing-app/src/steps.ts:168-186`), which **aborts the test** —
  the post-kill readback steps never execute. A test whose assertions are unreachable by
  construction is not "deferred", it is unwritable.
- **What would unblock it:** a background-process protocol — launch the planner detached with
  `setsid … &` (the shape `real-terminal-suites/zz-real-terminal-logs.test.yaml:47-48` already
  uses), record its PID to `$AFT_WORK_DIR`, return 0 immediately, then kill it and read back in
  *separate* steps. Only then are the interesting assertions reachable: the task is not left
  `in_progress` with a stale assignee, i.e. `loom complete`'s claim release
  (`complete_release.go:19-45`) ran or the claim TTL expired. Worth building once — the same
  protocol would serve any future "agent killed mid-run" case.

---

## Part 3 — Blockers & new seams needed

Numbered so they can be lifted into `FINDINGS.md` §3 style entries.

### PLN-B1 — `STUB_CODEX_PLAN_RUNNER=1` in `e2e/stubs/codex` (+ env allowlist)

**Pattern to follow:** `STUB_CODEX_EPIC_RUNNER=1` (`e2e/stubs/codex:9,51-123,125-129`).

Add a sibling function dispatched from the same `IS_EXEC && IS_JSON` branch. Required behaviour,
so the stub is a faithful stand-in for `planning.md`:

1. Read the prompt (positional argv in this path — `backend_codex.go:105-112` — with an stdin
   fallback for parity with the epic runner).
2. **Extract the epic scope first (new in r3 — required by PLN-D24).** Grep the prompt for the
   `**Epic scope: <id>**` directive that `GeneratePlanningPrompt` injects when `--parent` is set
   (`prompts.go:257-262`), e.g.
   `scope="$(sed -n 's/.*\*\*Epic scope: \([^*]*\)\*\*.*/\1/p' "$prompt_file" | head -n1)"`.
   Carry it into step 2a as `--parent "$scope"` when non-empty.
2a. Select the task: `loom data ready [--parent <scope>] --limit 200 --output json | jq` with the
   **same** filter the prompt teaches (`planning.md:10`) — `status == "open"`, not an epic, and
   (`has_design == false ∧ design_artifact_id == "" ∧ design == ""`) **or** labels contain
   `needs-revision`. Pick the highest priority. If none: print
   `no planning candidate`, run `loom complete`, exit 0.
   **Do not skip the `--parent` pass-through.** The CLI's own precheck is scoped
   (`plan.go:199-201` → `fetchReadyIssues`, `automode_poller.go:64`), but it only gates *whether*
   the agent runs; selection happens inside the stub. An unscoped `loom data ready` here lets the
   stub plan the wrong epic's task, which makes PLN-D24 pass or fail for a reason unrelated to
   scoping. Also exclude non-work types explicitly rather than relying on `loom data ready` to do
   it — PLN-D17 arm (d) depends on that exclusion being real.
3. `loom data claim <id>` then `loom claim <id>` (the epic-runner stub already does the
   `LOOM_ASSIGNED_TASK_ID`-vs-self-claim dance, `e2e/stubs/codex:83-85` — reuse it).
   **This step is load-bearing and the stub must NOT `cd` (r2).** `loom claim` writes the agent
   lock at its **CWD** (`internal/cli/agent/claim.go:41,55`), the backend is invoked with
   `cwd = worktreePath` (`backend_codex.go:86`), and `finalizeAgentSession` recovers the session's
   task ID from that lock (`plan.go:291-294`). Skip `loom claim`, or `cd` first, and the session
   is recorded with an empty task ID — invisible to `GET …/tasks/{id}/sessions`, silently
   breaking PLN-D20 while PLN-D13 still passes.
4. Detect the revision case: if labels contain `needs-revision`, prefix the design body with
   `AFT-PLAN-REVISION $LOOM_AFT_RUN_ID`; otherwise `AFT-PLAN-MARKER $LOOM_AFT_RUN_ID`. Append the
   selected task ID to both. **Never `$RUN_ID`** — it does not survive `FilteredEnv` (see below).
5. Compose a design with **≥ 3 `## ` sections** (Summary / Technical Approach / Testing
   Strategy) plus the marker — so `design-panel-section` (PLN-D14) has real structure. When
   `--design-format` resolves to `html` (readable from the rendered prompt text, which contains
   `Design format: HTML.`), emit `<h2>`/`<p>`/`<ul>` instead.
6. `loom data update <id> --design="…" --design-format=<markdown|html>`
7. `loom data update <id> --status review --assignee=""`
8. `loom complete`
9. Emit the epic-runner stub's result line shape:
   `jq -nc '{"status":"completed","output":"planned <id>"}'`
10. Optional `STUB_CODEX_PLAN_INVOCATIONS` append file, mirroring
    `STUB_CODEX_INVOCATIONS` (`e2e/stubs/codex:74-81`) so a test can prove exactly one run.

**Mandatory companion change — the env allowlist.** `internal/cli/envfilter/envfilter.go:39`
allowlists stub vars **by exact name** (the comment above it at `:38` says why):

```go
// E2E test stubs. Exact matches keep arbitrary STUB_* values out.
"STUB_CODEX_EPIC_RUNNER": true, "STUB_CODEX_INVOCATIONS": true,
```

`STUB_CODEX_PLAN_RUNNER` (and `STUB_CODEX_PLAN_INVOCATIONS`) must be added there, or
`buildBackendEnv` → `cli.FilteredEnv()` (`backend_codex.go:115-124`) strips them and the stub
silently falls back to its generic echo response — a failure mode that looks like "the planner
did nothing". Add a Go test asserting the two names survive `FilterEnv`, next to the existing
allowlist tests.

**No allowlist change is needed for the run marker** — use `LOOM_AFT_RUN_ID`, which passes on the
`LOOM_` prefix (`envfilter.go:83-91`, applied at `:126-133`). Do **not** add `RUN_ID` to the exact allowlist: it is a
generic name with no Loom meaning, and the prefix convention already provides a safe channel.
This is the r3 fix for the marker bug described in the Group C preamble.

**Also add the same plan mode to `e2e/stubs/claude`** if any deterministic case needs a
non-codex planner (PLN-D4 creates a claude-backed planner but never runs it, so this is
optional today).

### PLN-B2 — a launch gesture for a deterministic planning run

Three candidates, in descending preference:

| option | mechanism | pros | cons |
|---|---|---|---|
| **(a) `run:` step invoking `loom plan <agent>`** | per-run bin dir with `loom` + `codex` symlinks, `LOOM_CONFIG_DIR`/`LOOM_SERVER_URL`/`LOOM_WORKSPACE_ID` set — exactly `real-terminal-suites/zz-real-terminal-logs.test.yaml:47-48` | no product change; bounded, one-shot; no tmux; no TTY needed (headless codex branch) | the *actor* is a shell step, not the browser; needs a clear intent sentence naming the planning agent |
| **(b) browser-driven via the agent terminal** | navigate to `/ws/<ws>/agents/<planner>`; `AgentDetailMain` sets `pendingAgentName` → `useSessionSeeding` POSTs `/agents/{name}/terminal/session` (`instances/useSessionSeeding.ts:141`) → `buildAgentLaunchSpec` stores argv `loom … plan <name> --auto --daemon-mode` (`handlers/terminal/agent_session.go:400,407-410`); the PTY starts when the browser opens the terminal WS | perfect actor fidelity — a human clicking an agent starts it | `--daemon-mode` takes the wrapper/PTY path (`plan.go:161-168`) and needs `termSvc` + a PTY manager wired; and it makes every *other* planner test racy because merely visiting the agent page launches work. **(r3 correction: the run is NOT unbounded — `runPlan` checks `--daemon-mode` before `--auto` (`plan.go:98-101`), so `runPlanDaemon` performs exactly one backend run and returns (`plan.go:130-180`). The objection is the cross-test race, not unboundedness.)** **Do not adopt for the deterministic tier**; it is, however, exactly the mechanism the live-terminal tier would use for a planner variant of `zz-real-terminal-logs`. |

**Stub-compatibility note on option (b) (r2).** A related caveat applies to *leads*, not
planners: the codex **lead** runtime is an app-server protocol
(`internal/cli/backends/codex_lead_runtime.go:10-27`, `harness_lead_runtime.go:32`) and
`e2e/stubs/codex` implements no `app-server` mode, so a stub-backed interactive lead terminal
cannot boot deterministically. A **planner** terminal launch does not hit that path: `plan
--daemon-mode` goes through `InvokeNonInteractive` → `codex exec --json …`
(`plan.go:168`, `backend_codex.go:80-112`), which the stub already dispatches on
(`e2e/stubs/codex:125-136`). So (b) is stub-compatible for `plan`; it is rejected above purely
for unboundedness and cross-test interference, not for a stub gap.
| **(c) new `loom daemon seed-plan` seed command** | a seed command that performs steps 6-8 of PLN-B1 through the product's own issue-mutation path | fully deterministic, no backend at all | it seeds the *outcome*, not the *run* — it proves the UI, not the agent. Useful for PLN-D14/D15/D16 in isolation; not a substitute for D13/D17/D18. |

**Recommendation:** adopt (a) for Group C, and additionally build (c) as a cheap fixture so
PLN-D14/D15 (design rendering, plan-review approval) can run in the **plain** product-correctness
suite without depending on the stub at all. Document in the suite header that the run: step is
the planning agent's real entry point, satisfying actor fidelity the same way the epic-runner
`api:` trigger does in `zz-agent-flow`.

### PLN-B3 — `seed-session` (already the named next candidate)

**Downgraded in r2: this is a nice-to-have, not a blocker for any case in this plan.**

FINDINGS §3.10 records: *"The remaining high-value candidate is `seed-session`, which would
create a full session record rather than only transcript content."* Revision 1 claimed
**PLN-D20** needed it, on the theory that a CLI-launched planner writes only a local session
(`internal/sessions`) and no control-plane `AgentSession` (which the supervisor creates at
`supervisor.go:517-563`). **That theory is wrong:** `ListTaskSessions` reads the control plane
first and then **falls back to local session stores** (`svcimpl/session_service.go:156-197`),
searching the serve runtime dir, the workspace root, and every repo path (`:56-90`), matching by
`SessionsByTask(taskID)`. A local session is therefore surfaced by the very endpoint PLN-D20
asserts on. The two conditions that actually matter are recorded in PLN-D20's seam caveat
(shared runtime roots; `loom claim` in the stub's own CWD so the lock carries the task ID) and in
PLN-B1 step 3.

`seed-session` remains worth building for a different reason: it would let PLN-D14/D15/D15b/D16
run **without the stub or a launch step at all** (seed a design + a planning session, then drive
the UI), which is the same "outcome fixture" role as PLN-B2 option (c). Treat it as an
optimization of suite runtime and isolation, not a prerequisite.

Shape, following `seed-transcript` (`internal/cli/daemon/seed_transcript_cmd.go:75-109`, which
already creates the session record) and `seed-worktree`
(`internal/cli/daemon/seed_worktree_cmd.go:38-55`):

```
loom daemon seed-session --workspace <ws> --agent <name> --task <id> \
    --phase planning --backend codex --status completed [--exit-code 0]
```

Hidden, `LOOM_TESTSUPPORT=1`-gated via `requireTestSupport()`
(`internal/cli/daemon/seed_gate.go:12-18`), composing `store.AgentSessions().Create` so no path
or ID format is fabricated (ADR-0001, `docs/adr/0001-seed-commands-in-the-loom-binary.md:3`).
The `--phase planning` flag is what makes it planner-relevant: it is the value
`SessionTimelineRow`'s `data-phase` badge renders.

### PLN-B4 — no delete/recreate UI for agents

`deleteWorkspaceAgent` exists in the API client but has **only** epic-runner-rollback callers
(`IssueDetailPanel.tsx:1138`, `hooks/workspace/startEpicRunnerForIssue.ts:145`,
`views/IssueDetailPage.tsx:173`); no rail, sidebar, or agents-page control removes an agent, and
no frontend caller exists for `stop` / `restart` / `yield` either (only `startAgent`,
`api/agents/agents.ts:33-42`). PLN-D11's delete leg is therefore API-only and must say so.
FINDINGS-worthy as a **feature gap** in the same family as §1.10 ("a wrongly-added repo can't be
removed"), which was fixed by adding the DELETE + confirm dialog — the identical fix applies here.

### PLN-B5 — `backend_unavailable` has no deterministic writer, no PATCH path, and no renderer

**Wording corrected in r2.** A writer *does* exist: the supervisor's backend-availability gate
sets the state when the agent's backend CLI is absent from PATH
(`internal/cli/daemon/supervisor/backend.go:66-83`, `markControlPlaneAgentState` at `:78`), and
the domain constant is first-class (`internal/domain/agent.go:14-19`). The gap is narrower and
still real: **(i)** the only writer is the supervisor, which never runs in the deterministic aft
stack (`scripts/start-e2e-server.sh:200-207` starts `loom serve` alone); **(ii)** the API cannot
express it — `validAgentState` accepts only `"" | idle | active | stopped`
(`handlers/agents/handlers.go:252-259`); **(iii — expanded in r3, this is the real wall)** the
state is destroyed **server-side, in the monitor projection**, before any frontend concern:
`monitor.AgentStatus` has **no `state` field at all** (`internal/cli/monitor/monitor_types.go:35-70`
— `Status`, `Role`, `Mode`, `DesiredState`, `LiveStatus`, `ActivePhase`, but never `State`), and
`monitorStatusFromAgentState`'s projection collapses every non-`active` assignment state to the
single string `"idle"`:

```go
func monitorStatusFromAgentState(state domain.AgentState) string {
    switch state {
    case domain.AgentStateActive: return "ready"
    default:                      return "idle"     // backend_unavailable lands here
    }
}
```

(`internal/cli/serve/metricscmd/handlers.go:470-477`, called at
`monitor_store_data_source.go:176`.) The agents page consumes that monitor response
(`views/AgentsPage.tsx`), so a blocked planner is byte-for-byte identical to a healthy idle one
on the wire. **(iv)** only then does the frontend gap matter: `agent.state` is read purely as a
boolean (`AgentDetailMain.tsx:166-170`, `AgentIconRail.tsx:95-104`,
`AgentWorkPanel.tsx:1168-1177`) and `LoomAgentStatus.state` is an untyped `string?`.

*Revision 2 asserted a Go↔TS enum divergence here as a "bonus finding". **That was false and has
been retracted — see PLN-B14 (tombstone).*** The frontend `AgentState` union matches
`internal/types/enums.go:140-153` exactly; it simply is not the enum this state travels in.

Minimum viable fixes — **note the ordering changed in r3: projection first, rendering second**:

1. **Server projection (now the prerequisite):** either add a `state` field to
   `monitor.AgentStatus` and carry `assignment.State` through unmodified, or give
   `monitorStatusFromAgentState` a distinct status string for `backend_unavailable` (e.g.
   `"error"`, which `parseLoomStatus` already recognises, `types/agent/agent.ts:255-259`, and
   which `getStatusLabel` renders as `Error` with the error dot colour,
   `agentStatusPresentation.ts:22-23,50-51`). The second
   option needs **no** frontend change at all and is the cheapest honest fix.
2. **Frontend rendering** (only needed if fix 1 adds a field rather than reusing a status): map
   the state to the label that already exists on the *derived* path
   (`AgentRow.tsx:51-67`: `BackendUnavailable → "backend unavailable"`) — reuse that string
   rather than inventing copy.
3. **Test-only writer:** extend the seeding seam — `loom daemon seed-agent-state --workspace <ws>
   --agent <name> --state backend_unavailable` composing the same
   `markControlPlaneAgentState` path the supervisor uses (`supervisor.go:571-583`). Useless on
   its own until fix 1 lands, which is precisely the correction: **revision 2 listed this first
   and implied it was nearly sufficient.**
4. **Contract:** decide whether `PATCH state` should accept it (`handlers.go:252-259`). Arguably
   not — it is supervisor-derived — which strengthens the case for (3).

### PLN-B6 — the Planner has no delegation surface (upgraded in r2: dead *component*, not a dead prop)

Two walls, both verified:

1. `AssigneeDropdown` — the component the panel actually mounts
   (`IssueDetailPanel.tsx:1380-1386`) — excludes every non-`task` role outright:
   `if (agent.role && agent.role !== "task") continue;` (`AssigneeDropdown.tsx:119-120`).
2. `StartWorkButton` — which is the component that *has* the `preferredRole?: "task" | "plan"`
   option (`actions/StartWorkButton.tsx:35,57,126-133`) — is **not mounted anywhere**. It is
   exported from `actions/index.ts:5-6`, imported by nothing in production, and referenced only
   by a stale comment (`IssueDetailPanel.tsx:603`). Revision 1 described this as "a prop with no
   caller"; it is the stronger case — an entire unrendered component, in the family FINDINGS
   §2 already recorded ("7 dead components … shipped", fixed in `c30b9d989`).

Net effect: a design-less task cannot be handed to a Planner from any issue surface, which
contradicts `CONTEXT.md`'s *Delegation* definition ("assignment and starting are one gesture, on
**every** surface"). Fix: either mount `StartWorkButton` and pass
`preferredRole={getOpenStatus(issue) === "needs_plan" ? "plan" : "task"}`, or move that rule into
`AssigneeDropdown` and delete `StartWorkButton`. Note `getOpenStatus`
(`utils/issue/issueCategory.ts:49-56`) already computes exactly this predicate and is
**exported but called from nowhere** — it is the missing half of this feature, not dead code.
Whichever way it is resolved, PLN-D23 inverts.

### PLN-B7 — missing testids on the agent rail/card surface

`AgentCard`, `AgentIconRail`, `AgentRail`, `SortableAgentRow`, `AgentDetailMain`'s empty states,
`DiffTab`/`GitTab` and the whole agent-detail Git/Diff family carry **no** `data-testid`. Every
Group B case above is forced onto `aria-label="Agent: <name>"` + `data-status` + class-name
matching. Same family as the FINDINGS §3.9 note ("Missing stable testids — AddRepoModal inputs,
DiffTab/DiffFileRow/DiffFileViewer. Cheap adds."). Requested minimum for this plan:
`agent-card-<name>` on the `AgentCard` root, `agent-role-badge` on its `.role` span,
`agent-status-line` on `.statusLine`, and `agent-terminal-unavailable` on the stopped empty
state.

### PLN-B8 — *partial* review-content promotion (FINDINGS §1.19 / §3.9) — narrowed in r2

The two review-action tests sit in the surface tier because "a review-status issue can render
Approve and Request changes, but without a PR, branch, commit, or diff the reviewer has nothing
to inspect" (`FINDINGS.md:260-266`), and §3.9 asks specifically for a *gh-less review-content
seed* of "branch, commit, PR, or diff" (`:422-424`).

Revision 1 over-claimed that a planner design satisfies that. It does not — a design is not a
branch, commit, PR, or diff. The accurate, narrower claim:

- For a **plan** review (`getReviewType(issue) === "plan"`, i.e. review status with no PR URL),
  the thing a reviewer is supposed to inspect *is* the design. A planner-written design makes
  that fixture non-hollow, so **PLN-D15 (panel Approve → `open`) and PLN-D16 (panel Reject →
  `needs-revision`) belong in the product-correctness tier** once PLN-D13 exists.
- For a **code** review, §1.19/§3.9 stand unchanged and still require the git seeding.

So the FINDINGS note to add is a *scope split*, not a closure: §1.19 shrinks to the code-review
half, and the plan-review half moves to tier 1. `surface-suites/review-actions.test.yaml` keeps
its two tests as the generic-review-status guards (and PLN-D15b now pins the Approve divergence
between the two surfaces).

### PLN-B9 — the Planner card's description is wrong

`Breaks epics into tasks under daemon supervision.` (`CreateAgentModal.tsx:51`) describes a
**lead**. The planning prompts never create issues; they write one design onto one existing
non-epic task (`planning.md` Steps 1–6; decomposition lives in `prompts/lead.md:43,109`).
Cosmetic (LOW) but it will actively mislead anyone writing these tests. Suggested copy:
`Designs one ready task at a time and sends the plan to review.`

### PLN-B10 — the plan role's read-only constraint is write-only

The fleet-db `plan` role is seeded `ReadOnly: true`
(`workspacemgr/workspace_store.go:486`); the supervisor exports `LOOM_READ_ONLY=1`,
`LOOM_ALLOWED_TOOLS`, `LOOM_DENIED_TOOLS` (`supervisor/spawn.go:126-132`); **no production code
reads any of them** (`ReadOnlyPreamble()`, `prompts.go:520-527`, has zero non-test callers).
"Do not implement" is enforced purely by prose. PLN-R4 is the only test that would catch a
violation. MED — either wire the flags into the prompt/backend invocation or delete them.

### PLN-B11 — `needs_plan` vs `needs_design` filter-name split

Role configs and the router use `needs_plan` (`supervisor/role.go:63`,
`task_router.go:236`, `workspace_store.go:485`), but `loom agent --task-filter` accepts only
`needs_design | has_design | any` and rejects `needs_plan` before the router override
(`internal/cli/agent/agent_cmd.go:82-86,243-251`), while the daemon and web UI pass a custom
role's `TaskFilter` straight through as `--task-filter` (`spawn.go:108-110`,
`agent_session.go:417-419`). A **custom** plan-like role therefore fails to launch.
Out of scope for the Planner template itself (built-in `plan` never takes that path), but it is
the same subsystem and belongs in the same FINDINGS entry.

### PLN-B12 — `daemon_planner_smoke_test.go` is an empty stub

`internal/cli/daemon/daemon_planner_smoke_test.go` contains only `package daemon`. Not an aft
blocker, but it means the supervisor→`loom plan` seam has **no** Go-level smoke test either, so
aft is currently the *only* place a planner regression could be caught end to end. Worth filing
alongside this plan.

### PLN-B13 — the no-repos hint contradicts the server (new in r2)

`CreateAgentModal` tells the operator, for a workspace with no repos:
`No repos yet — add one from the sidebar first. This agent will run with workspace scope.`
(`CreateAgentModal.tsx:569-576`). The server does the opposite for any non-interactive role: an
empty-type workspace still gets a real local path (`workspacemgr/workspace_store.go:82-84,138-141`),
so `ensureLocalAgentWorktrees` proceeds past its no-path early return
(`svcimpl/agent_service.go:396-402`), `SelectAgentRepos` returns nothing for zero repos
(`localworkspace/localworkspace.go:492-495`), and the create fails with
`workspace has no repos for agent` (`agent_service.go:416-421`) → HTTP 400.

MED (feature gap + misleading copy). Pick one contract: (a) honour the hint — let a
zero-repo workspace create a workspace-scoped agent and defer worktree creation until a repo is
added, or (b) fix the copy to say a repo is required and disable submit. PLN-D6 pins the current
behaviour either way and will fail loudly when this is resolved.

### PLN-B14 — WITHDRAWN in r3: the claimed Go/TypeScript agent-state drift does not exist

**Do not re-file this.** Revision 2 claimed the frontend `AgentState` union had drifted from Go.
It has not. The union
`idle | spawning | running | working | stuck | done | stopped | dead | ""`
(`types/agent/agent.ts:18-27`) is an **exact** match for `internal/types/enums.go:140-153`
(`StateIdle | StateSpawning | StateRunning | StateWorking | StateStuck | StateDone | StateStopped
| StateDead`), which is the type its own comment names, and it types `Issue.agent_state`
(`types/issue/issue.ts:36`). The control-plane assignment state — `domain.AgentState`
(`internal/domain/agent.go:11-19`), the one carrying `backend_unavailable` — is a *different*
enum and travels as the untyped `LoomAgentStatus.state?: string`
(`types/agent/agent.ts:94`). Revision 2 compared the union against the wrong Go type.

The kernel of truth (the state is unrenderable) is real but is a **projection** problem, now
recorded correctly as PLN-B5 wall (iii). This tombstone is kept so the numbering in earlier
revisions still resolves; **it is not a live blocker and is excluded from the count.**

### PLN-B15 — a one-shot planner's error class is not observable (new in r3)

Discovered while reclassifying PLN-R5. A failing planning run classifies its error
(`agenterr.ClassifyFromOutput` → `TaskFailedData.ErrorClass`, `plan.go:415-431`), but nothing an
aft suite can read exposes it:

- **Session metadata does not carry it.** `finalizeAgentSession` records the exit code and usage,
  never the class (`plan.go:286-318`).
- **The event sink and the reader disagree on the directory.** The agent bus writes to
  `LOOM_EVENTS_DIR`, or `<bootstrap.LoomDir()>/events` when unset
  (`internal/cli/agent_event_bus.go:40-50`). Serve's `/api/observability/events` handler is built
  over `observability.ResolveEventsDir()`, which is *daemon-config*-derived —
  `<cli.GetWorkspaceRuntimeDir()>/.loom/events` via the `EventsDir: ".loom/events"` default
  (`observability.go:336-350`, `config/project.go:199`, `supervisor.go:981-986`) — and returns
  **`""`** when no daemon config is loadable, which is the deterministic aft stack's state.
  The supervisor normally aligns the two by exporting `LOOM_EVENTS_DIR` to the same
  config-derived path (`supervisor/spawn.go:44`); a bare `loom plan` has no such alignment.
- `last_error_class` on the agent record is a fleet-db-derived passthrough
  (`internal/domain/agent.go:73-77`), populated from the agent's most recent *terminal
  control-plane session* — not from a local CLI run.

Two ways to unblock, either sufficient for PLN-R5:

1. **Align the dirs and assert the event.** Export `LOOM_EVENTS_DIR` (a `LOOM_`-prefixed var, so
   it survives `FilteredEnv`) on the planner launch, pointing at the exact directory serve
   resolved, and ensure a daemon config with `events_dir` exists in the e2e workspace so
   `ResolveEventsDir()` is non-empty. Then assert `error_class` through
   `GET /api/observability/events`. Cheap, but it depends on stack config the harness does not
   currently guarantee — which is why R5 is now **blocked**, not ready.
2. **Record the class on the session.** Add `error_class` to the session metadata written by
   `finalizeAgentSession`, next to the exit code. Strictly better: it makes the class visible on
   the Runs tab for *every* failed run, deterministic and real tiers alike, with no directory
   coupling — and it is the kind of forensics gap FINDINGS §1.4 already complains about in a
   neighbouring subsystem.

Prefer (2). MED.

---

## Coverage table

| id | case | kind | tier | status |
|---|---|---|---|---|
| PLN-D1 | Planner template creates a `plan` agent + readback | happy | PC `zz-planner-agent` | ready-to-write |
| PLN-D2 | invalid name keeps Create disabled | edge | PC | ready-to-write |
| PLN-D3 | duplicate name surfaces the 409 in `create-agent-error` | edge | PC | ready-to-write |
| PLN-D4 | backend dropdown persists on the planner | happy | PC | ready-to-write (r2: `select:` confirmed; caveat withdrawn) |
| PLN-D5 | repo chip deselect ⇒ `cross_repo:true, repos:[]` | edge | PC | ready-to-write |
| PLN-D6 | no-repos hint promises workspace scope, create returns **400** | edge | PC | ready-to-write (r2: reclassified from happy/201; guards PLN-B13) |
| PLN-D7 | planner in rail + background group + monitor, role `Plan`, status `Idle` | happy | PC | ready-to-write (r2: companion must be a **lead**; testid friction — PLN-B7) |
| PLN-D8 | no supervisor ⇒ Planner definition stays idle; task stays unassigned with no session | edge | PC | ready-to-write |
| PLN-D9 | API-assigned in-progress task ⇒ synthesized **Planning** badge (vs task agent's **Working**) | happy | PC | ready-to-write |
| PLN-D10 | stopped planner ⇒ "Agent is stopped" / no terminal | edge | PC | ready-to-write (r3: must patch `desired_state`; `state` is not projected) |
| PLN-D11 | delete (API) + recreate (modal) | edge | PC | ready-to-write (delete leg API-only — PLN-B4) |
| PLN-D12 | `backend_unavailable` rendered in the UI | edge | PC (target) | blocked-on-seam PLN-B5 (r3: needs **server projection** first, not just a seed) |
| PLN-D13 | stubbed planner writes a design, task ⇒ review | happy | PC `zz-planner-run` | blocked-on-seam PLN-B1 + PLN-B2 |
| PLN-D14 | design renders in `design-panel` with collapsible sections | happy | PC | blocked-on-seam PLN-B1/B2 (or PLN-B2(c) `seed-plan`) |
| PLN-D15 | panel `panel-approve-button` on a plan review ⇒ back to `open` | happy | PC | blocked-on-seam PLN-B1/B2 (r2: rewritten off `/prs`) |
| PLN-D15b | `/prs` Approve on a plan review ⇒ **closed** (divergence from D15) | edge | SF `planner-contracts` | ready-to-write (r2: new; fabricated design fixture, no seam) |
| PLN-D16 | panel Reject ⇒ `needs-revision` ⇒ re-plan (label **remains**) | edge | PC | blocked-on-seam PLN-B1/B2 (r3: hard oracle added) |
| PLN-D17 | skips epics, non-work types, and already-designed tasks | edge | PC | blocked-on-seam PLN-B1/B2 (r3: +non-work-type fixture — 3 of 4 arms) |
| PLN-D18 | no candidates ⇒ clean exit, zero mutation | edge | PC | blocked-on-seam PLN-B1/B2 |
| PLN-D19 | workspace HTML format ⇒ `design-html-content` | edge | PC | blocked-on-seam PLN-B1/B2 |
| PLN-D20 | Runs tab: session `phase=planning`, no diff | happy | PC | blocked-on-seam PLN-B1/B2 only (r2: PLN-B3 dependency removed — local-store fallback exists) |
| PLN-D24 | epic-bound planner plans only inside its epic | edge | PC `zz-planner-run` | blocked-on-seam PLN-B1/B2 (r3: B1 must honour parent scope) |
| PLN-D21 | agent-create contract probes for `plan` | edge | SF `planner-contracts` | ready-to-write |
| PLN-D22 | PATCH `state=backend_unavailable` ⇒ 400 | edge | SF | ready-to-write (gap guard for PLN-B5) |
| PLN-D23 | planner absent from the **assignee** picker | edge | SF | ready-to-write (r2: Start Work half dropped — component unmounted; gap guard for PLN-B6) |
| PLN-R1 | real planner writes a substantive design ⇒ review | happy | real `zz-real-<backend>-plan` | ready-to-write (all 4 backends) |
| PLN-R2 | real planner revises after Request changes | edge | real (codex) | ready-to-write |
| PLN-R3 | real planner honours HTML design format | edge | real (codex) | ready-to-write |
| PLN-R4 | real planner writes **no** files, commits, or diff | edge | real (codex) | ready-to-write |
| PLN-R5a | missing backend auth ⇒ fails fast, task untouched | edge | real (codex) | ready-to-write (r3: split from R5) |
| PLN-R5b | …and the failure carries an `error_class` | edge | real (codex) | **blocked-on-seam PLN-B15** (r3: reclassified from ready) |
| PLN-R6 | timeout budget ⇒ no half-planned task | edge | real (manual) | **blocked** (r3: no daemonless lever; aft's 120s `run:` kill aborts before the readback) |

**Totals (revision 3 — final)**

| tier | total | ready-to-write | blocked |
|---|---|---|---|
| deterministic — product-correctness (`suites/`) | 21 | 11 (D1–D11) | 10 (D12–D20, D24) |
| deterministic — surface (`surface-suites/`) | 4 | 4 (D15b, D21, D22, D23) | 0 |
| **deterministic subtotal** | **25** | **15** | **10** |
| real-backend (`real-suites*/`) | 7 | 5 (R1, R2, R3, R4, R5a) | 2 (R5b, R6) |
| **all** | **32** | **20** | **12** |

**14 live blocker/seam items** — PLN-B1…B13 (13) plus **PLN-B15** (1). **PLN-B14 is withdrawn as
false** and retained only as a tombstone, so it is excluded from the count: removing it took the
list from 14 to 13, and PLN-B15 (discovered while reclassifying PLN-R5) brought it back to 14.

Dependency concentration is unchanged and remains the headline: **PLN-B1 (stub plan runner + env
allowlist + parent scope) and PLN-B2 (launch gesture) together unblock 9 of the 10 blocked
deterministic cases** — every one except PLN-D12, which needs PLN-B5's server projection.
On the real side, PLN-B15 unblocks R5b and a background-process protocol unblocks R6.

Revision-3 delta: deterministic case count and ready/blocked split **unchanged** (25 / 15 / 10) —
every r3 correction landed on already-blocked cases or on assertion quality. Real tier 6 → 7
(PLN-R5 split into R5a ready + R5b blocked); real ready 5 → 5, real blocked 1 → 2 (R6 reclassified
from "ready, defer" to blocked). Blockers: 14 → 14 live via two moves (−B14, withdrawn as false; +B15, the error-class observability gap).

### Suggested sequencing

1. **Group A + B (PLN-D1…D11) and the four surface cases (D15b, D21…D23)** — no product or
   harness change needed; takes the Planner from zero to the best-covered template in the modal,
   and lands PLN-D9, the plan-specific `Planning` badge, at near-zero cost. PLN-D6 and PLN-D15b
   are worth writing early: both are cheap and each pins a live product contradiction
   (PLN-B13, and the Approve divergence). PLN-D10 must patch `desired_state`, not `state`.
2. **PLN-B1 + PLN-B2(a)** — stub plan runner, the `envfilter.go:39` allowlist entry, the
   `LOOM_AFT_RUN_ID` marker channel, parent-scope extraction, and the launch `run:` step.
   Then Group C in this order: **D13** (the fixture the rest reuse) → D17, D18, D24 (selection
   semantics) → D14 (rendering) → D15, D16 (the review loop) → D19, D20.
3. **PLN-R1 on codex**, then the other three backends, then R4 (the do-not-implement negative),
   then R2/R3. **R5a** is the cheapest real case and can land any time. R5b waits on PLN-B15;
   R6 waits on a background-process protocol.
4. **FINDINGS entries** for PLN-B4, B5 (with its projection wall), B6, B9, B10, B11, B12, B13,
   **B15**, the Approve divergence from PLN-D15b, and the §1.19 *scope split* from PLN-B8
   (plan-review half promotes; code-review half stays open). **Do not file B14** — it was false.

Implementation correction (verified live): direct `/issues/:id` navigation renders
`IssueDetailView` (`issue-detail-view`, `detail-run-epic-button`), while
`issue-detail-panel`, `agent-assignee-*`, `agent-status-badge`, and
`header-run-epic-button` are board-click-only surfaces.

Implementation correction (verified live): `${var:...}` interpolation is available
only inside `api:` steps. `open:` and navigation-oriented `run:` steps must build
URLs from saved files or avoid the route by opening the board and clicking the card.

Implementation correction (verified live): PLN-D6 currently pins the repo-less
no-repos copy/400 contradiction and should flip when PLN-B13 lands. PLN-D22
cross-checks the `backend_unavailable` validation gap against the adjacent
Planner backend/create contract probes rather than proving a UI projection.
