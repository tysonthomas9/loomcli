# Task Runner (role `task`) — aft test plan

Scope: the **Task Runner** background-agent template offered by `CreateAgentModal`, from the
moment an operator clicks its card to the moment the agent it defines claims, works, and closes
a task — plus every edge the supervisor and the repo-scope model can put in between.

This plan is written to be turned into YAML directly. It does not contain YAML.

---

## Revision 3 — final

Second codex pass, again re-verified in source. This is the last review round; where a case could
not be shown feasible, it is now marked **blocked** rather than optimistically ready.

**The Revision 2 regression, reverted.** Revision 2 accepted a codex finding that
`last_error_class` renders on issue cards. Codex retracted it, and the retraction is correct:
production `IssueCard` imports **only** `resolveCardFooterBadge` (`IssueCard.tsx:27`, used at
`:188`); `resolveCardAgent` and the error-class `AgentRow` are never mounted. There is even a vitest
suite whose stated purpose is to pin the agent row as permanently gone —
"IssueCard no longer renders an inline AgentRow (Aether V3 alignment) … These tests pin that the
agent row stays gone across the column states that used to show it"
(`__tests__/IssueCard.agentRow.test.tsx:6-10`, `:35-40`). The only frontend readers of
`last_error_class` are dead code and its own unit tests. **Revision 1's B6 was right**; B6 is
restored, TSK-D19 is rebuilt as a wire-contract probe, and TSK-R3 loses its UI assertion. A browser
assertion here would have contradicted an existing passing test.

**Cases demoted to blocked (5)**
- **TSK-D12 → blocked on new B13.** `--actor` is captured "for command-line parity" and never used
  (`internal/cli/data/claim.go:9-12`), and re-claim by the same actor is explicitly idempotent
  (`internal/webui/service/issue_impl.go:365-368`). With `LOOM_SERVER_URL` set the CLI uses the HTTP
  API backend (`internal/cli/issue_backend_resolve.go:52-56`) and the server claims "for the
  server-side actor" — so both racers are one actor and both exit 0. No CLI-side env fixes it.
- **TSK-D11a → blocked on B10.** A guard step turns a paid-call hazard into host-dependence, which
  is not "ready" for a deterministic tier.
- **TSK-D14 split**: D14a (delete-after-create, ready) / D14b (delete-with-history, blocked on B1).
  Revision 2's table said ready while its body depended on blocked D9.
- **TSK-R4 → blocked on B10 and narrowed to claude only.** Cursor's auth is probed by *executing*
  `cursor-agent status` (`backend_cursor.go:135-137`, `:150-160`), the same thing the harness
  preflight requires to succeed (`run-aft.sh:191-196`); no server-scoped env makes it report logged
  out.
- **TSK-D9's blocker widened.** Two gaps beyond the fixture: (a) it does **not** prove claiming —
  `runTaskDaemon` trusts `LOOM_ASSIGNED_TASK_ID` without claiming (`internal/cli/agent/task.go:131`,
  `:140-146`) and the stub skips its claim call when that var is set (`e2e/stubs/codex:83-85`), so
  the run can commit and close an *unclaimed* task; the case now says so honestly instead of
  implying claim coverage. (b) Its Logs assertion is **removed**: archive-log wiring lives only in
  the supervisor's `setupAgentLogFile`, whose own comment states "Daemon-mode agents bypass tmux, so
  we write it directly" and "Without it, daemon-supervised agents 404 in the Logs tab"
  (`supervisor/spawn.go:271-285`). A directly launched agent writes no archive log; TSK-D16 already
  covers that tab through `seed-log`.

**Other corrections**
- **TSK-R3's task was unsafe.** Path A launches codex with
  `--dangerously-bypass-approvals-and-sandbox` on both the interactive and non-interactive paths
  (`backend_codex.go:42`, `:108`), so "ask it to modify `/etc/hosts`" could really modify the host.
  Replaced with a repo-local contradictory acceptance condition plus a **gate oracle** that gives a
  stable pass/fail instead of Revision 2's "inconclusive" escape hatch.
- **TSK-R2 must seed real dependencies.** Designs that merely narrate A→B→C do not order anything:
  `SelectBestTask` ranks by score, then priority, then alphabetical id
  (`internal/cli/task_router.go:139-150`). The precedent uses `--depends-on`
  (`e2e/epic_runner_codex.sh:153`). `depends_on` is **not** a field on `IssueCreateRequest`, so seed
  it with `POST /issues/{B}/dependencies {depends_on_id: A}` — the path
  `dependencies-graph.test.yaml:1-2` documents.
- **TSK-D8a loses its "nothing queued" assertion** — there is no AgentCommand list endpoint to read
  (`handlers/agents/module.go:20-35`); the 404 half stays.
- **`expect.attr` count corrected to 10** across tracked YAML (8 of them in
  `suites/issue-detail.graph/states.yaml`), not 2. Revision 2 counted only top-level suite files and
  missed the graph fragments, which `README.md:63-76` explains are imported, not discovered.

**Confirmed resolved from round 1** (no further change): findings 2, 3, 5, 7, 8, 9, 10 — write path
under `epic-runner-output/`, git identity provisioned, D8 split, `source_repo` supported, exact
`backend == "codex"`, the B11 zero-repo contradiction, and the B5 wording.

---

## Revision 2 — codex-vetted

Revision 1 was reviewed read-only by OpenAI Codex against the same checkout. Every finding was
re-verified against source before being folded in; two were rejected with evidence. Changes:

**Corrected factual claims**
- **B6 was wrong.** `last_error_class` *is* rendered — on issue cards, not on the agents page
  (`components/IssueCard/cardAgentView.ts:177-180`, `AgentRow.tsx:51-63`, `:119-126`, which maps
  `BackendUnavailable → "backend unavailable"`). B6 is narrowed to the agents page, and the
  correction **unlocks two cases**: new **TSK-D19** (deterministic orphaned-agent card slot) and a
  real UI assertion for **TSK-R3**.
- **B9 dissolved.** `source_repo` is accepted on `POST /issues` (`handlers/issues/issues.go:106`),
  mapped (`write_ops.go:77`), and enforced (`internal/backend/fleet/deferred.go:124`). **TSK-D13**
  moves from "ready after B9" to ready.
- **TSK-D1** now asserts `backend == "codex"` exactly; the modal always sends a non-empty backend
  (`CreateAgentModal.tsx:138`, `:226-232`, `:341`), so "absent or codex" was too loose.
- **B5** narrowed: `deleteWorkspaceAgent` *does* exist as an API wrapper
  (`api/workspace/workspace.ts:329`); what is missing is any mounted control.

**Redesigned infeasible cases**
- **TSK-D9 fixture**: the proposed `STUB_CODEX_WRITE=task-output/…` would abort under `set -e`
  before `make gate`, because the stub's `order.log` append (`e2e/stubs/codex:112`) is
  unconditional while only `dirname "$write_path"` is created (`:103`). The write path must live
  **under `epic-runner-output/`**. Fixture also needs **git identity** (the stub commits at `:117`).
- **TSK-D12 redesigned and upgraded to ready.** The old design could not test claim exclusivity for
  two compounding reasons: with `LOOM_ASSIGNED_TASK_ID` set the stub skips its claim call entirely
  (`e2e/stubs/codex:83-85`), *and* the call it makes is `loom claim`, which is lock-file bookkeeping
  for `loom monitor` whose "failures do not affect the actual task claim"
  (`internal/cli/agent/claim.go:16-32`) — not the atomic `loom data claim`
  (`internal/cli/data/claim.go:15-28`). Now races two `loom data claim` invocations directly, which
  is the agent's real path per `prompts/task.md:47`, and needs no B1 fixture.
- **TSK-D8 split**: D8a (404 contract, ready) and D8b (the rollback, blocked on a new seam). The
  stale-dropdown DELETE trick is race-prone — the button's list derives from the polled `agents`
  prop (`AssigneeDropdown.tsx:142-157`) and DELETE broadcasts an agent refresh (`handlers.go:124`).
- **TSK-R4 split** from the harness's own credential preflight, which fires *before* the server
  starts (`run-aft.sh:185-189`) — new blocker **B10**.

**New**
- **TSK-D11c** — deterministic `local_backend_auth_missing`, which B10 makes reachable with a stub
  backend instead of a real one.
- **TSK-D19** — orphaned-agent card slot (from the B6 correction).
- **TSK-D6** gains the copy-vs-behavior contradiction as an explicit assertion (new **B11**).
- **B10** (server-scoped credential env), **B11** (zero-repo copy contradiction), **B12**
  (injectable start failure). **B9 removed.**
- A **Mechanics constraints** section pinning the verified aft vocabulary — most importantly that
  **there is no `GET /agents/{name}`**, so every per-agent readback is a `run:` + python list filter.

**Rejected**
- "aft has no `expect.attr`": `AttrExpectSchema` is defined at `../testing-app/src/types.ts:86-88`
  and wired into `ExpectSchema` at `:105`, alongside `value`, `enabled`, and `checked`; the tracked
  suites already use `attr` twice (e.g. `zz-agent-flow:224`), `value` four times, `enabled` twice,
  and `title` once. It also accepts a raw `selector:`, not only `testid:`. See *Mechanics
  constraints* for the schema detail and the one case where `attr` genuinely does not fit.
- A podman variant (TSK-R6) stays declined; nothing in Revision 1's reasoning changed.

---

## Mechanics constraints (verified against the tracked suites)

Pin these before writing YAML; each was measured, not assumed.

- **There is no `GET /api/workspaces/{ws}/agents/{name}`.** The only per-agent routes are `PATCH`
  and `DELETE` (`internal/webui/handlers/agents/module.go:27-28`). Every "read this agent's fields"
  assertion must `GET /agents` and filter in a `run:` step. `zz-agent-flow:255` is the exact
  template to copy — a bounded `for` loop, `curl -sf` into `$AFT_WORK_DIR`, then a `python3 -c`
  one-liner that finds the agent by name and asserts on it.
- **`api:` `assert:` can index but not filter.** `path: "data.sessions.0"` works
  (`zz-agent-flow:150`); "the element whose name == X" does not. Anything predicate-shaped is
  `run:` + python.
- **There is no `/roles` HTTP route at all.** Role config (`max_budget_usd`, `task_filter`,
  `effort`) is reachable only through the CLI: `loom role list --json`, `loom role show <NAME>
  --json`, `loom role set <NAME> <KEY> <VALUE>` (`internal/cli/role/role_cmd.go:37-121`).
- **`expect:` schema** (`ExpectSchema`, `../testing-app/src/types.ts:97-114`): `url`, `text`,
  `notText`, `title`, `visible`, `count`, `attr`, `value`, `enabled`, `checked`. `attr` is used **10
  times** across tracked YAML — 8 of them in `suites/issue-detail.graph/states.yaml`, which a
  top-level-only grep misses because graph fragments are imported rather than discovered
  (`tests/aft/README.md:63-76`). `checked` is schema-supported but unused so far. Two details that
  matter when writing assertions:
  - `attr`, `value`, `count`, `enabled`, and `checked` all take `cssLocShape` and require **exactly
    one of `selector:` or `testid:`** (`types.ts:67-77` `checkCssLoc`, `AttrExpectSchema` at
    `:86-88`). So `expect.attr` on a plain CSS selector is valid — an element without a testid is
    **not** a reason to fall back to `wait.fn`. (`expect.visible` is the exception: it additionally
    requires `isCssable`, so no `first`/`last`/`nth`, `types.ts:110-112`.)
  - `AttrExpectSchema` requires `name` **and** an exact `equals: string` — there is no "attribute
    exists" or regex form. An assertion whose expected value is not known up front (e.g. "the
    `title` holds one of nine error classes") genuinely does need `wait.fn`.
- **Not available**: `fill` from a file, `api:` request bodies loaded from a file, array-filtering
  `api:` asserts, and any `ws:` step. Cross-step state travels through `$AFT_WORK_DIR` files read by
  `run:` steps.
- **`run:` steps do not inherit the server's stub PATH.** `e2e/stubs` is prepended only to the
  server process (`run-aft.sh:292-295`), so any `run:` step that launches an agent must prepend it
  itself.
- **Never assert Terminal-tab content.** The Terminal tab can spawn a backend session
  (`components/TerminalView/instances/useSessionSeeding.ts:3` → `ensureAgentTerminalSession`), and
  the stubs have no app-server mode, so a booted terminal's content is not deterministic. Assert
  the tab strip, then click away to Logs/Info/Diff — the pattern `zz-agent-flow` already uses
  successfully.
- **Every `run:`, `api:`, and `wait.fn` step needs an `intent:`**; the loader rejects the suite
  otherwise. Keep each `run:` poll under the 120 s step ceiling.

---

## Overview

### What the template is

`CreateAgentModal` offers five template cards. Two are *background* (supervised worker) roles,
three are *interactive* (terminal teammate) roles:

| Card | testid | `role_name` submitted |
|---|---|---|
| Planner | `create-agent-template-planner` | `plan` |
| **Task Runner** | **`create-agent-template-task`** | **`task`** |
| Lead | `create-agent-template-lead` | `lead` |
| PR Review | `create-agent-template-interactive-pr-review` | `pr-review` |
| Custom prompt | `create-agent-template-custom-prompt` | `<agent name>` |

Definition: `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx:38-65`
(`BACKGROUND_TEMPLATES`), description string "Claims and runs ready tasks under daemon
supervision.", name placeholder `worker`.

Submitted payload (`CreateAgentModal.tsx:331-342`):

```
POST /api/workspaces/{ws}/agents
{ name, role_name: "task", auto: false, cross_repo: <bool>, repos: [...], backend?: "codex" }
```

Three things about that payload matter for testing:

- `auto` is **hard-coded `false`** (`CreateAgentModal.tsx:334`). There is no auto toggle in the
  modal. It is also display-only at runtime — nothing in the supervisor branches on it (the real
  supervision gate is `ShouldSuperviseWithRoles`, `internal/cli/config/project.go:132-152`,
  which refuses only interactive role kinds and `desired_state ∈ {stopped, draining}`).
- `cross_repo` is **derived, not toggled**: `const crossRepo = selectedRepos.length === 0`
  (`CreateAgentModal.tsx:220`), and `repos: crossRepo ? [] : selectedRepos`. The modal preselects
  the first repo (`:215-218`), so the only way to reach cross-repo scope through the UI is to
  deselect every chip.
- `backend` is sent **only when non-empty after trim** (`:341`). Options come from
  `GET /api/backends` via `useBackends()`; default value is `defaultBackend?.trim() || "codex"`
  (`:138`, `:226-232`).

Creation is **transactional with worktree materialization**:
`agentServiceImpl.CreateAgent` (`internal/webui/svcimpl/agent_service.go:349-385`) writes the
fleet-db row, then calls `ensureLocalAgentWorktrees` (`:383`, body at `:405-437`); if worktree
creation fails the agent row is deleted (`:384`). Worktrees land at
`<workspacePath>/worktrees/<repoName>/<agentName>` (`internal/localworkspace/localworkspace.go:40-42`)
with the branch named after the agent. `SelectAgentRepos` (`localworkspace.go:491-528`) means
**`cross_repo: true` creates a worktree in every workspace repo**, while an empty repo set falls
back to `repos[0]`.

Runtime prompt: `internal/cli/agent/prompts/task.md` ("disciplined software engineer … ONE task"),
pre-assigned variant `internal/cli/agent/prompts/fleet_task.md`. The prompt's Step 1 filter is
`status == "open" && not epic && has_design && !needs-revision` (`task.md:30-46`) — the same
`ReadyToImplement` predicate the Go side applies (`internal/webui/handlers/issues/ready.go:226-230`).
**A task with no `design` is invisible to a task agent.**

### Runtime path — there are two, and only one involves the agent definition

This is the single most important fact for this plan.

**Path A — supervisor / named agent.** The agent definition created by the modal is the config
for this path. Either `loom daemon` supervises it (`internal/cli/daemon/daemon.go:308-322` →
`supervisor.Start`, `internal/cli/daemon/supervisor/supervisor.go:148-218`) and spawns
`loom task <worktreePath> --auto --daemon-mode --backend <b>`
(`internal/cli/daemon/supervisor/spawn.go:92-100`), or an operator runs `loom task <name>`
directly (`internal/cli/agent/task.go:27-119`). Sessions are attributed to the agent name;
the agent runs in its own worktree; claim filtering is by resolved source repos.

**Path B — driver / task worker.** `POST /api/workspaces/{ws}/workflows/epic-runner` runs
in-process inside `loom serve` (Engine B) and routes each child task to the bundled
`local-task-runner` (`internal/workflows/builtin/local-task-runner.ts`), which is driven by
`LOOM_WORKTREE_PATH` and **knows nothing about any Loom agent definition**
(`internal/driver/task_bridge.go:29`, `internal/runtimepreflight/preflight.go:27`).

`zz-agent-flow` case 2 and all four `real-suites-*` tiers exercise **path B**. The Task Runner
template configures **path A**. Nothing in the browser suites currently connects the two.

### Current coverage — three angles, and what each leaves open

**(a) `tests/aft/suites/zz-agent-flow.test.yaml`** — deterministic, workspace `E2E-WS-AGENT`.
- Case 1 (`:25-91`): Lead template through the modal, agents-page + monitor visibility, `seed-log`
  archive refresh. Not a task agent.
- Case 2 (`:93-158`): epic-runner with the stub backend — **path B**, no agent definition involved.
  The `runner: "local-task-runner"` field is passed explicitly at `:131`.
- Case 3 (`:160-178`): `seed-worktree` → Diff tab. Artifact seam, not a run.
- Case 4 (`:180-230`): session detail / transcript / disabled-diff contract for the path-B session.
- Case 5 (`:232-257`): **Task Runner card**, name `iris-${RUN_ID}`, one repo chip deselected,
  submit, then an API readback that the definition exists. Explicitly titled "without starting it".

**(b) `tests/aft/real-suites/`, `real-suites-claude/`, `real-suites-cursor/`, `real-suites-opencode/`**
— the HELLO.md epic scenario, one test each, all **path B**. Already pinned: `sessions.0.backend`,
`exit_code == 0`, `evidence.status == "ok"`, `evidence.conflicts == []`, `files_changed >= 1`,
the `usage_status ∈ {reported, unavailable}` all-null-or-all-numeric contract, `has_transcript`,
`has_diff`, a transcript "real signature" check, the session diff containing `HELLO.md`, the closed
card in `section[data-status=done]` under `?groupBy=none`, and `HELLO.md` on disk containing
exactly `hello world`.

**(c) `tests/aft/real-terminal-suites/zz-real-terminal-logs.test.yaml`** — the only **path A**
browser test. It registers the agent by **raw `curl` POST inside a `run:` step** (`:41-43`,
`{"name":"real-codex-term","role_name":"task","backend":"codex"}`), starts it with a detached
`loom --workspace E2E-WS --backend codex task real-codex-term --auto` (`:44-49`), polls
`GET .../agents/real-codex-term/terminal/info` until `data.mode == "tmux"`, and then asserts only
the Logs tab's live-terminal rendering (`embedded-terminal`, `terminal-wrapper .wterm`, non-empty
`.term-row`). It never asserts a claim, a close, a session, or a diff.

**The gap, stated precisely:**
1. A **UI-created** task agent is never started, by any tier.
2. A **started** task agent is never UI-created — the only running one comes from a raw POST.
3. **Path A never demonstrates work**: no claim, no close, no session, no diff, in any tier.
4. UI lifecycle (start/stop/restart/yield/delete) is untested — see the next point for why.

### Key code references

| Concern | Reference |
|---|---|
| Modal, task card, payload | `internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx:38-65`, `:220`, `:275-278`, `:331-342` |
| Client name rule | `internal/webui/frontend/src/utils/agentName.ts:2-17` (`/^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$/`) |
| Server name rule + duplicate | `internal/webui/svcimpl/validators.go:39-52`, `:59-70` (`ErrAlreadyExists` → `ErrConflict` → HTTP 409) |
| Create + worktree transaction | `internal/webui/svcimpl/agent_service.go:349-385`, `:405-437` |
| Agent wire model (assertable fields) | `internal/domain/agent.go:42-78` — `state`, `desired_state`, `live_status`, `active_task_id`, `active_phase`, `last_error_class`, `backend`, `fallback_backends`, `repos`, `cross_repo`, `auto`, `role_name` |
| Agent states | `internal/domain/agent.go:11-19` (`idle`/`active`/`stopped`/`backend_unavailable`), `internal/domain/control_plane.go:15-18` (desired: `stopped`/`idle`/`running`/`draining`) |
| Lifecycle routes | `internal/webui/handlers/agents/module.go:20-35`; handlers `internal/webui/handlers/agents/handlers.go:129-227` |
| Lifecycle semantics | `internal/webui/svcimpl/agent_service.go:557-589` — writes state + **queues an `AgentCommand`**; it does not spawn |
| Command consumer | `internal/cli/daemon/daemon_command_poller.go:38-121` (**daemon only**) |
| `loom task` CLI | `internal/cli/agent/task.go:27-71` (flags), `:73-119` (four execution paths), `:122-175` (`--daemon-mode`), `:194-238` (single-task) |
| Supervisor spawn env | `internal/cli/daemon/supervisor/spawn.go:41-76` (`LOOM_AGENT_NAME`, `LOOM_WORKTREE_PATH`, `LOOM_SOURCE_REPOS`, `LOOM_ASSIGNED_TASK_ID`), `:121-144` (`LOOM_AGENT_REPO`, `LOOM_MAX_BUDGET_USD`) |
| Backend-unavailable gate | `internal/cli/daemon/supervisor/backend.go:20`, `:31-84`; runtime recheck `restart.go:250-265` (30 s fixed, uncounted) |
| Restart budget | `internal/cli/daemon/supervisor/restart.go:35-93`, `:168-219`, `:270-343`; defaults `:354-447` (`max_retries=3`, `no_work_backoff=30`, `output_timeout=900`) |
| Error classes | `internal/agenterr/outcome.go:12-33` (`NoWork`, `LockConflict`, `SpawnFailure`, `BackendUnavailable`) + harness classes via `internal/agenterr/classify.go:85-92` (`RateLimited`, `Auth`, `Billing`, `ModelNotFound`, `ContextOverflow`, `Timeout`, `Transient`) |
| Task quarantine | `internal/cli/daemon/supervisor/quarantine.go:22-40`, `:374-460`; label `loom:quarantined`, threshold 3, env `LOOM_TASK_QUARANTINE_THRESHOLD` |
| Local-runner preflight (path B) | `internal/runtimepreflight/preflight.go:77-106`; gate wiring `internal/webui/handlers/workflows/preflight.go:23-39` |
| Claim repo filter | `internal/cli/daemon/supervisor/claim.go:96-112`, `internal/cli/config/repos.go:14-60`, `internal/cli/automode/automode_poller.go:64-94` |
| Session model | `internal/sessions/types.go:6-13` (`running`/`completed`/`failed`/`aborted`), `:30-75` |
| `usage_status` | `internal/webui/svcimpl/session_evidence.go:98-115` — `unavailable` / `reported` / `conflict`; nulls on the wire at `internal/webui/server/dto/session_response.go:85-91` |
| `has_diff` | `internal/webui/svcimpl/session_service.go:191-193`, `:218-220`, `:382-407`, `:518`, `:573-579` — reduces to `sessions/<id>/diff.patch` existing |
| Seed commands | `internal/cli/daemon/seed_log_cmd.go:22-62`, `seed_worktree_cmd.go:38-199`, `seed_transcript_cmd.go:29-113`; gate `seed_gate.go:13-18` |
| Stub codex | `e2e/stubs/codex:51-123` (epic-runner flow), `:125-136` (default canned JSON) |
| Stub env allowlist | `internal/cli/envfilter/envfilter.go:38-39` — only `STUB_CODEX_EPIC_RUNNER` and `STUB_CODEX_INVOCATIONS` survive into agent subprocesses |
| Harness env for suites | `tests/aft/run-aft.sh:292-295` (server PATH), `:322-331` (`AFT_*` exports) |

### Three findings that reshape this plan

1. **The agents page has no lifecycle controls.** No start, stop, restart, yield, delete, or auto
   button exists anywhere in `internal/webui/frontend/src`. `AgentCard` has no testid at all
   (`components/AgentCard/AgentCard.tsx:71-90` — only `data-status`, `data-selected`,
   `role="button"`, `aria-label="Agent: <name>"`). `agent-card-<name>` appears **only in vitest
   mocks**; do not use it in browser tests. The frontend has `startAgent` but **no**
   `stopAgent`/`restartAgent`/`yieldAgent` client at all
   (`internal/webui/frontend/src/api/agents/agents.ts:33-42` is the whole lifecycle surface).
2. **The only mounted human start gesture is delegation.** `startAgent` is called from
   `IssueDetailPanel.tsx:921` when an agent is assigned to an `open`/`review` issue, and from
   `views/PRReviewWorkspace.tsx:282`. This matches CONTEXT.md's *Delegation*: "assignment and
   starting are one gesture." The relevant testids live on `AssigneeDropdown`:
   `assignee-dropdown-trigger`, `assignee-dropdown-menu`, `agent-assignee-<name>`,
   `assignee-saving`, `assignee-error`, `assignee-unassign`
   (`components/IssueDetailPanel/fields/AssigneeDropdown.tsx:320,345,416,506,520,528`).
   `StartWorkButton` (`start-work-button`, `agent-option-<name>`) is **dead code — not mounted
   anywhere**; do not write tests against it. Note the `agent-assignee-<name>` buttons render only
   for agents present in the polled `agents` prop (`AssigneeDropdown.tsx:142-157`) — a fact that
   kills the "delete then click a cached button" trick, see TSK-D8b.
3. **The aft stack runs `loom serve` only — there is no `loom daemon`**
   (`scripts/start-e2e-server.sh:190-208`). So `POST /agents/{name}/start` writes
   `state=active, desired_state=running` and queues an `AgentCommand` that **nothing consumes**.
   Path B works anyway because the driver executor lives inside `loom serve`. Any test that wants
   a *running* task agent must start it itself from a `run:` step, exactly as
   `real-terminal-suites` does.

---

## Part 1 — Deterministic tier (stub AI backend)

**Proposed files**

- `tests/aft/suites/zz-task-runner.test.yaml` — new product-correctness suite. Owns workspace
  **`E2E-WS-TASK`** (per README's rule that agent-creating suites take their own workspace) and
  seeds **two** repos so cross-repo and repo-scope cases are expressible. `zz-` prefix so it runs
  after the empty-state suites.
- `tests/aft/surface-suites/agent-lifecycle-contracts.test.yaml` — new surface suite for the four
  UI-orphaned lifecycle endpoints, the not-found contract (TSK-D8a), the delete contract (TSK-D14),
  and the zero-repo create rejection (TSK-D6's API half). Promotion condition: mount start/stop/
  restart controls on the agents page.

**Shared setup sketch** (suite-level, fixture provisioning — API and shell are both fine here).
Every element below is load-bearing for TSK-D9; see blocker **B1** for why each one is needed:

1. `mkdir` + `git init -q` `$AFT_WORK_DIR/task-repo-a` and `task-repo-b`.
2. **`git -C <repo> config user.name "Loom AFT"` and `user.email "aft@example.test"` on each repo.**
   The stub runs `git commit` (`e2e/stubs/codex:117`) under `set -e`; without identity it aborts.
   Repo-local config is shared with linked worktrees, so configuring the source repo covers the
   agent's worktree. `scripts/start-e2e-server.sh:170-171` does exactly this for the stock
   workspace — the new repos must not be the exception. (`envfilter.go:21-22` allowlists
   `GIT_AUTHOR_*`/`GIT_COMMITTER_*` as an alternative, but repo config is simpler.)
3. `git commit --allow-empty -m init -q` on each.
4. A `Makefile` in each repo with a no-op `gate:` target — the stub runs `make gate`
   (`e2e/stubs/codex:114`).
5. **`mkdir -p <repo>/epic-runner-output` and commit it (with a `.gitkeep`).** The stub appends to
   `epic-runner-output/order.log` unconditionally (`:112`) but only creates
   `dirname "$write_path"` (`:103`) — so the directory must pre-exist *or* every
   `STUB_CODEX_WRITE` value must sit inside it. Do both; belt and braces.
6. `git init --bare $AFT_WORK_DIR/task-remote-a`, added as `origin` on repo A — the stub runs
   `loom push` (`:118`).
7. `POST /api/workspaces {"name":"e2e-ws-task","type":"empty","repos":[<a>,<b>]}` → id
   `E2E-WS-TASK`.

**Shared teardown sketch**: `AFT_WS=E2E-WS-TASK scripts/close-open-issues.sh`, then
`DELETE /agents/<each ${RUN_ID}-scoped name>`, then `DELETE /api/workspaces/E2E-WS-TASK`,
then a best-effort `pkill`-by-signature of any `loom … task <agent>` left running, modelled on
`tests/aft/scripts/real-codex-teardown.sh:71-93`.

---

### Group A — modal creation

#### TSK-D1 — Task Runner created with a single repo scope
- **Tier**: `suites/zz-task-runner.test.yaml` (product correctness).
- **Intent**: *An operator creates a Task Runner scoped to one repo through CreateAgentModal and
  confirms its stored scope and its materialized worktree.*
- **Preconditions**: `E2E-WS-TASK` with repos A and B.
- **Steps**: `open /ws/E2E-WS-TASK/agents` → `wait.fn agents-page` →
  `click {role: button, name: "+ Add agent", exact: true}` → `wait.fn create-agent-overlay` →
  `click {testid: create-agent-template-task}` → `fill {testid: create-agent-name, value: "worker-a-${RUN_ID:-local}"}`
  → (the first repo chip is preselected; leave it) → `click {testid: create-agent-submit}` →
  `wait.fn` overlay gone and body text contains the name → `api GET /agents` readback.
- **Assertions**
  - Readback — **`run:` + python over `GET /agents`**, not `api: assert:`. There is no
    `GET /agents/{name}` (`handlers/agents/module.go:27-28`) and `api:` cannot filter an array, so
    copy the bounded-poll + `python3 -c` shape from `zz-agent-flow:255`. Assert
    `role_name == "task"`, `auto == false`, `cross_repo == false`, `repos == [<repoA name>]`, and
    **`backend == "codex"` exactly** — the modal seeds the select with
    `defaultBackend?.trim() || "codex"` (`CreateAgentModal.tsx:138`), always keeps a non-empty
    option (`:226-232`), and includes `backend` on submit whenever it is non-empty (`:341`), so an
    absent backend would itself be a bug.
  - `run:` readback that `<E2E-WS-TASK path>/worktrees/<repoA>/worker-a-${RUN_ID}` exists, is a git
    worktree, and its checked-out branch equals the agent name
    (`git -C <path> rev-parse --abbrev-ref HEAD`). This proves the create/worktree transaction at
    `agent_service.go:383` end to end.
  - Rail visibility: `wait.fn document.body.textContent.includes('worker-a-')` (the rail
    CSS-uppercases names, so match `textContent`, as `zz-agent-flow:54` notes).
- **Edge rationale**: zz-agent-flow case 5 asserts only that the *definition* persists. The
  worktree half of the transaction — the part that makes the agent runnable at all — is unasserted
  anywhere today.
- **Status**: ready to write.

#### TSK-D2 — Task Runner created with workspace-wide (cross-repo) scope
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator deselects every repo chip so the Task Runner takes workspace-wide scope,
  and confirms a worktree was created in every repo.*
- **Steps**: same modal flow; `click {selector: "[data-testid='create-agent-repo-chips'] button", first: true}`
  to clear the preselected chip; assert the hint text flips to
  `No repo selected — the agent gets workspace-wide scope.` (`CreateAgentModal.tsx:368-370`); submit.
- **Assertions**
  - Readback: `cross_repo == true`, `repos == []`.
  - `run:` readback that **both** `worktrees/<repoA>/<name>` and `worktrees/<repoB>/<name>` exist —
    the observable consequence of `SelectAgentRepos` returning all repos
    (`localworkspace.go:495-497`).
  - Chip `aria-pressed` is `false` after the deselect click.
- **Edge rationale**: zz-agent-flow case 5 performs this click in a **one-repo** workspace, where
  cross-repo and single-repo are indistinguishable on disk. Two repos make the behavior visible.
- **Status**: ready to write.

#### TSK-D3 — Backend dropdown selection is honored
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator picks a non-default AI backend for a Task Runner and confirms it is
  stored on the agent.*
- **Steps**: open modal, select the task card, fill a name, `select {testid: create-agent-backend, value: claude}`,
  submit.
- **Assertions**
  - Readback: `backend == "claude"` on the created agent.
  - Before selecting: assert the option set is non-degenerate — at least the four stubbed backends
    (`codex`, `claude`, `cursor`, `opencode`) are present, and `codex` is the initially selected
    value. The options are fed by `GET /api/backends`
    (`api/workspace/backends.ts:53`), which in the aft stack reports the four `e2e/stubs/`
    binaries as installed and `gemini` as not installed.
- **Edge rationale**: `create-agent-backend` is currently an uncovered testid, and per-agent backend
  is the input to `GetEffectiveBackend` (agent > role > daemon default) that every failover and
  preflight path keys on.
- **Status**: ready to write.

#### TSK-D4 — Name validation and normalization
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator is prevented from creating a Task Runner with an invalid name and sees
  the entered name normalized when it is valid.*
- **Steps / assertions** (one test, four probes; the modal never closes between them):
  1. Empty name → `expect {enabled: {testid: create-agent-submit, equals: false}}` (canSubmit gate,
     `CreateAgentModal.tsx:275-278`).
  2. `fill "Worker One"` (space) → submit still disabled.
  3. `fill "-leading"` → still disabled (punctuation-leading rejected by
     `STORED_AGENT_NAME_RE`).
  4. `fill "WORKER-B-${RUN_ID:-local}"` (uppercase) → submit enabled; submit; readback that the
     stored `name` is **lowercase** `worker-b-${RUN_ID}` (`normalizeStoredAgentName`,
     `utils/agentName.ts:4-6`, mirrored server-side at `validators.go:39-41`).
- **Edge rationale**: the client blocks invalid names by disabling submit rather than by rendering
  `create-agent-error`, so the disabled-button contract is the only observable. Case-normalization
  is a silent transform that a later `loom task <name>` lookup depends on.
- **Status**: ready to write.

#### TSK-D5 — Duplicate name is surfaced, not swallowed
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator who reuses an existing agent name sees the server's conflict in the
  dialog, and no second agent is created.*
- **Preconditions**: TSK-D1's `worker-a-${RUN_ID}` exists.
- **Steps**: open modal → task card → fill the **same** name → submit.
- **Assertions**
  - `expect {visible: {testid: create-agent-error}}` and the text contains `already exists`
    (server body `create agent: agent "x" in workspace "y": already exists`, HTTP **409**, from
    `validators.go:66-67` + `internal/webui/server/handler/errors.go:64-67`).
  - `expect {visible: {testid: create-agent-overlay}}` — the modal stays open.
  - `expect {value: {testid: create-agent-name, equals: "worker-a-${RUN_ID:-local}"}}` — the form
    is **not** reset on error (`CreateAgentModal.tsx:355-365` resets only on success).
  - Readback: `GET /agents` still contains exactly one agent with that name.
- **Edge rationale**: FINDINGS §1.6 was exactly this failure mode for issues (server warned, UI
  dropped it). The agent path has the same shape and no test.
- **Status**: ready to write.

#### TSK-D6 — Task Runner in a repo-less workspace fails closed
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml` (fabricated fixture: a workspace
  deliberately created with no repos; promote to tier 1 if repo-less workspaces become a real
  operator state).
- **Intent**: *An API client creating a task agent in a workspace with no repos gets a validation
  error rather than an agent that can never run.*
- **Steps**: `api POST /api/workspaces {"name":"e2e-ws-norepo","type":"empty","repos":[]}` in setup;
  `api POST /agents {"name":"orphan-${RUN_ID}","role_name":"task","auto":false,"cross_repo":true,"repos":[]}`.
- **Assertions**: `status: 400`, error text `workspace has no repos for agent`
  (`agent_service.go:421`); readback that `GET /agents` is empty — proving the compensating delete
  at `agent_service.go:384` ran.
- **Browser half — the contradiction, and the point of the case (tier 1)**: opening the modal in
  that workspace renders `create-agent-no-repos` (`CreateAgentModal.tsx:569-576`) whose copy
  promises *"This agent will run with workspace scope."* Then submitting a Task Runner **fails**,
  because `SelectAgentRepos` returns nothing (`internal/localworkspace/localworkspace.go:491-528`)
  and `ensureLocalAgentWorktrees` rejects the empty set (`agent_service.go:416-422`). Assert both
  halves in one test: the reassuring copy is visible, *and* clicking submit surfaces
  `create-agent-error` with `workspace has no repos for agent`. A test that asserts only the 400
  hides the UX defect; a test that asserts only the copy hides the failure.
- **Edge rationale**: this is the highest-value cheap case in Group A — it is simultaneously a
  contract test and a logged product defect (**B11**).
- **Status**: ready to write.

---

### Group B — starting a UI-created Task Runner

#### TSK-D7 — Delegation starts the Task Runner (control-plane truth)
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator delegates an open task to the Task Runner they just created, and the
  issue and the agent both record that work was started.*
- **Preconditions**: TSK-D1's agent; one open issue created by an API-client actor with a `design`
  field so it is genuinely claimable.
- **Steps**: `open /ws/E2E-WS-TASK/kanban` → open the issue's detail panel →
  `click {testid: assignee-dropdown-trigger}` → `wait.fn assignee-dropdown-menu` →
  `click {testid: "agent-assignee-worker-a-${RUN_ID:-local}"}` → `wait.fn` the saving indicator
  clears (`assignee-saving` absent) and `assignee-error` is absent.
- **Assertions**
  - Issue readback: `assignee == "worker-a-${RUN_ID}"` **and** `status == "in_progress"`
    (`IssueDetailPanel.tsx:916-919` sends both in one PATCH for an `open` issue).
  - Agent readback: `state == "active"`, `desired_state == "running"`
    (`handlers.go:129-137` → `RequestAgentLifecycle`).
  - Board: the card leaves `section[data-status=ready]` and appears under
    `section[data-status=in_progress]` via SSE.
  - **Explicitly do not assert** that a process started. With no `loom daemon` in the stack the
    queued `AgentCommand` is never consumed. Put that limitation in a comment above the test so the
    next reader does not "fix" it.
- **Edge rationale**: this is the only mounted UI gesture that starts an agent, and it is currently
  untested for any role. It is also the gesture CONTEXT.md names *Delegation*.
- **Status**: ready to write.

#### TSK-D8a — Starting a missing agent is a clean 404
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml`.
- **Intent**: *An API client that starts an agent which no longer exists gets a not-found error, not
  a silent success.*
- **Steps**: `api POST /agents/no-such-agent-${RUN_ID}/start` (no agent by that name).
- **Assertions**: `status: 404`, and nothing more. The chain is `RequestAgentLifecycle` →
  `UpdateAgent` → `store.Agents().Update` → `domain.ErrNotFound` → `service.ErrNotFound`
  (`agent_service.go:570-576`, `validators.go:64-65`).
- **Dropped assertion**: Revision 2 also wanted "no `AgentCommand` was created". It is true by
  construction — the command write is sequenced after the update (`agent_service.go:577-587`) — but
  **unobservable**: there is no AgentCommand list endpoint anywhere in the web module
  (`handlers/agents/module.go:20-35`). Asserting it would require a new read seam for a fact the
  sequencing already guarantees; not worth it.
- **Status**: ready to write.

#### TSK-D8b — Failed start rolls the issue back
- **Tier**: `suites/zz-task-runner.test.yaml` once **B12** lands.
- **Intent**: *When starting the delegated Task Runner fails, the operator's issue does not stay
  falsely in progress.*
- **Target assertions**: `assignee-error` visible; issue readback shows `assignee` back to its
  previous value and `status` back to `open` — the compensating PATCH at
  `IssueDetailPanel.tsx:924-934`.
- **Why the obvious approach does not work**: Revision 1 proposed opening the dropdown, deleting the
  agent from a `run:` step, then clicking the "cached" button. That is not stable. The
  `agent-assignee-<name>` button is rendered from `filteredAvailable`, derived by `useMemo` from the
  polled `agents` prop (`AssigneeDropdown.tsx:142-157`), and `DELETE /agents/{name}` broadcasts an
  `agent.refresh` mutation (`handlers/agents/handlers.go:124` → `broadcastAgentRefresh`, `:236-250`).
  The button therefore disappears on the next tick, and the test becomes a race between the SSE
  broadcast and the click. Going `offline:` first does not help either: the rollback PATCH would
  fail for the same reason as the start.
- **What it needs (B12)**: a deterministic way to make `POST /agents/{name}/start` fail while the
  agent stays listed. Options: a `LOOM_TESTSUPPORT=1`-gated header or query flag that forces the
  lifecycle handler to return 500; or an agent state from which `start` is legitimately rejected.
- **Interim authority**: the frontend unit suite already covers the assignee save path
  (`components/IssueDetailPanel/__tests__/IssueDetailPanel.test.tsx:905-918`); extend it there with
  a mocked `startAgent` rejection rather than faking it in the browser.
- **Status**: **blocked on B12.**

#### TSK-D9 — A UI-created Task Runner actually claims, works, commits, and closes ★
- **Tier**: `suites/zz-task-runner.test.yaml`. **This is the headline delta of the whole plan.**
- **Intent**: *An operator creates a Task Runner through the modal, and that agent — started
  through the product CLI with a stub backend — claims the ready task, commits its change, closes
  the task, and leaves a recorded session the operator can inspect.*
- **Preconditions**
  - The full seven-step fixture from *Shared setup* — Makefile, git identity, committed
    `epic-runner-output/`, and the bare `origin` (see **B1**).
  - Epic + child task seeded by an API-client actor. The child's `design` carries the stub's
    instruction channel: **`STUB_CODEX_WRITE=epic-runner-output/${RUN_ID}.txt`** plus a prose line
    (`e2e/stubs/codex:36-39` parses `KEY=` lines out of the design). The write path **must sit inside
    `epic-runner-output/`**: the stub appends to `epic-runner-output/order.log` unconditionally
    (`:112`) while creating only `dirname "$write_path"` (`:103`), so a path anywhere else aborts
    the run under `set -euo pipefail` before `make gate` is ever reached. This is the same shape
    `e2e/epic_runner_codex.sh:139` uses, and the reason it uses it.
- **Steps**
  1. Modal-create `runner-${RUN_ID}` with repo A selected (human actor).
  2. `api POST /issues` epic, then child task with `parent`, `priority: 2`, and the `design` above
     (API-client actor).
  3. `open /ws/E2E-WS-TASK/kanban`, `wait {text: <task title>}` — proves the board saw it before the
     run.
  4. `run:` — start the agent through the product CLI, mirroring
     `real-terminal-suites:44-49` but headless and bounded:
     `PATH="$AFT_TESTS_DIR/../../e2e/stubs:$PATH" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR"
      LOOM_SERVER_URL="$AFT_API_URL" LOOM_WORKSPACE_ID=E2E-WS-TASK
      LOOM_ASSIGNED_TASK_ID="$(cat "$AFT_WORK_DIR/tskTaskId")" STUB_CODEX_EPIC_RUNNER=1
      "$AFT_LOOM_BIN" --workspace E2E-WS-TASK --backend codex task runner-${RUN_ID} --daemon-mode`
     — redirect output to `$AFT_WORK_DIR/tsk-agent.log` so later steps can `cat` it on failure.
     Note the deterministic tier's stub PATH is **server-scoped only** (`run-aft.sh:292-295`), so
     the `run:` step must prepend `e2e/stubs` itself.
  5. Bounded poll (`for i in $(seq 1 40) … sleep 2`, under aft's 120 s ceiling) until the task
     reads `"status":"closed"`.
- **Assertions**
  - Task closed; `assignee` is the agent name.
  - Board: the closed card leaves `section[data-status=ready]`; under `?groupBy=none` it appears in
    `section[data-status=done]` (the grouped board hides closed children — README v5 anchors).
  - `api GET /tasks/<id>/sessions` → `data.sessions.0` exists with `agent_name` (or the DTO's
    equivalent) equal to the modal-created name, `status == "completed"`, `exit_code == 0`, and
    **`files_changed >= 1`** — the discriminator that separates this from the path-B stub session,
    which is always `files_changed == 0` / `has_diff == false`.
  - `usage_status`: assert it is one of `reported`/`unavailable`, and that the five token/cost
    fields are all `null` when `unavailable` and all numeric when `reported`
    (`session_evidence.go:98-115`, `session_response.go:85-91`). Reuse the real tier's python
    assertion verbatim; do not hardcode which branch the stub takes.
  - Agents page, Diff tab: `GET /agents/<name>/diff/files?to=HEAD` lists
    `epic-runner-output/${RUN_ID}.txt` **and** `epic-runner-output/order.log` (the stub stages both,
    `:116`), so assert `2 files changed` rather than `1`; click the file button; the marker renders.
  - **No Logs-tab assertion.** A directly launched `loom task --daemon-mode` writes **no** archive
    log: that wiring lives only in the supervisor's `setupAgentLogFile`, whose comment states
    "Daemon-mode agents bypass tmux, so we write it directly" and "Without it, daemon-supervised
    agents 404 in the Logs tab" (`supervisor/spawn.go:271-285`). Redirecting the run's stdout to
    `$AFT_WORK_DIR` — as this case does — puts it nowhere the UI reads. TSK-D16 already covers the
    archive Logs tab through the supported `seed-log` seam; proving that a *live* agent's own output
    reaches that tab requires a supervisor launch (**B2**). `agent.OpenAgentArchiveLog`
    (`internal/cli/agent/archive_log.go:23-30`) resolves the canonical path and could be used to
    route output there, but doing so from a test would be fabricating the artifact rather than
    observing it.
  - `run:` readback that the agent's worktree HEAD moved and carries a commit whose message
    contains the task id (`git -C <worktree> log -1 --format=%s`).
- **What this case does NOT prove — state it in the test comment.** It does **not** demonstrate
  claiming. `runTaskDaemon` reads `LOOM_ASSIGNED_TASK_ID` and trusts it, generating the pre-assigned
  prompt without any claim of its own (`internal/cli/agent/task.go:131`, `:140-146`); the stub in turn
  skips its claim call when that variable is set (`e2e/stubs/codex:83-85`). So the run can legitimately
  commit and close a task that was never claimed. That is acceptable — the case's subject is
  *execution and artifact production by a UI-created agent*, not claim arbitration — but it must be
  labelled honestly, because a reader would otherwise assume the headline case covers claiming.
  Claim arbitration is TSK-D12's job, and TSK-D12 is blocked on **B13**.
- **Edge rationale**: closes gaps 1 and 2 of the four named in the Overview, and the *execution* half
  of gap 3. Without it, "the Task Runner runs tasks" is an untested claim in every tier.
- **Status**: **blocked on B1**. Note that B1 option 2 (a stub mode that calls the atomic
  `loom data claim`) would also let this case drop `LOOM_ASSIGNED_TASK_ID` and cover claiming for
  real — which is why option 2 is now the recommended route rather than the fixture-only path.

#### TSK-D10 — Task Runner with no ready work goes idle, quietly
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *A Task Runner started when no designed task is ready reports that there is nothing to
  do and leaves no session behind.*
- **Preconditions**: board empty of open designed tasks (guaranteed by the suite's teardown
  contract); one open task **without** a `design` present, to prove the filter rather than an empty
  board.
- **Steps**: modal-create `idle-${RUN_ID}`; `run:` the CLI in **single-task** mode (no
  `--daemon-mode`, no `--auto`, no `LOOM_ASSIGNED_TASK_ID`), capturing stdout.
- **Assertions**
  - Exit code **0** and stdout contains `No tasks available for implementation.` and
    `Tasks must be: open status, has design, no needs-revision label, not epics`
    (`internal/cli/agent/task.go:202-206`).
  - No backend process ran: the design-less issue is untouched (`status` still `open`,
    `assignee` still empty).
  - `GET /tasks/<designless id>/sessions` returns an empty list.
  - Agent readback: `state` unchanged (`idle` or unset), `live_status` not `working`.
- **Edge rationale**: the availability check short-circuits *before* any backend invocation
  (`task.go:195-206`), so this case needs no stub work at all — it is the cheapest supervision-edge
  test available and it pins the `has_design` gate that FINDINGS-adjacent regressions have broken
  before (`ready.go:225-230` documents a real "perpetual NoWork" incident).
- **Status**: ready to write.

#### TSK-D11 — Backend binary missing → fail closed
Two sub-cases, deliberately in different tiers because they exercise different gates.

**TSK-D11a — path B preflight, binary absent**
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator whose workspace default backend has no CLI installed is told the run
  cannot start, instead of getting a silent fake completion.*
- **Steps**: `api PATCH /api/workspaces/E2E-WS-TASK/config/backend {"backend":"gemini"}` → `api POST
  /workflows/epic-runner` with a seeded epic → restore the backend in a following step and in
  teardown (`GET /config/backend` exists too, `internal/webui/app/routes.go:153`, so the restore is
  verifiable).
- **Why `gemini` specifically**: `PATCH /config/backend` validates against
  `{claude, codex, opencode, gemini, cursor}` (`internal/webui/handlers/workspace/backend.go:83-89`
  → `webuiterminal.ValidBackends`, `internal/webui/terminal/session_command.go:13`), so a
  deliberately bogus name gets a 400 instead of reaching preflight. `gemini` is the only valid name
  with no stub in `e2e/stubs/`.
- **Host-dependency guard (required)**: gemini's health needs the binary on PATH **and**
  `GEMINI_API_KEY` or `GOOGLE_API_KEY` (`internal/cli/backends/backend_gemini.go:117-141`), and the
  server's PATH is `e2e/stubs:$PATH` — the operator's PATH is still behind it. On a machine with a
  real `gemini` CLI *and* a key, this case would flip from "fails closed" to **really invoking a
  paid CLI from the deterministic tier**. Two mitigations, both wanted: (i) **B10** — the harness
  unsets `GEMINI_API_KEY`/`GOOGLE_API_KEY` for the server process, symmetric with how real mode
  unsets keys; (ii) a first `run:` guard step that fails with a clear message if `command -v gemini`
  succeeds, so the hazard is loud rather than silent.
- **Assertions**: non-2xx; the error text contains `local task runner cannot start` and the backend
  name, and matches **either** `local_backend_unavailable` **or** `local_backend_auth_missing`
  (`internal/runtimepreflight/preflight.go:94-101`) — with B10 in place the class depends only on
  whether a `gemini` binary happens to exist, and both are the same fail-closed contract. Readback
  that **no** workflow run was created and the child task is still `open`
  (`internal/webui/handlers/workflows/module.go:149`).
- **Status**: **blocked on B10.** Revision 2 called this ready-with-a-guard; that was wrong. The guard
  converts a paid-call hazard into *host-dependence* — the case would pass on CI and skip on any
  developer machine with a gemini CLI — and a deterministic-tier case whose outcome depends on the
  operator's PATH is not ready. B10 is one change at `run-aft.sh:285-295`; do it first.

**TSK-D11c — path B preflight, auth missing (deterministic)**
- **Tier**: `suites/zz-task-runner.test.yaml` once **B10** lands.
- **Intent**: *An operator whose backend CLI is installed but not logged in is told so before
  anything is queued.*
- **Steps**: set the workspace default backend to `claude` — whose stub **is** on the server PATH,
  so `Installed: true` — with the server's `CLAUDE_CONFIG_DIR` pinned to an empty directory and
  `ANTHROPIC_API_KEY`/`CLAUDE_CODE_OAUTH_TOKEN` unset for the server process only. Then `POST
  /workflows/epic-runner`.
- **Assertions**: the message contains `is missing auth` and the class token
  `local_backend_auth_missing` (`preflight.go:98-101`); no run created; the task untouched.
- **Why this matters**: it makes the *auth* half of the two-branch fail-closed contract
  deterministic and free. Revision 1 could only reach it through the real-CLI tier (TSK-R4), which
  costs an account and — as **B10** explains — is blocked by the harness's own preflight anyway.
  The seam is the same one TSK-R4 needs, so it pays for itself twice.
- **Status**: **blocked on B10.**

**TSK-D11b — path A supervisor gate (partial)**
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml`.
- **Intent**: *An agent whose backend CLI is absent is recorded as `backend_unavailable`, not as
  stopped or failed.*
- **Steps**: `api PATCH /agents/<name> {"state":"backend_unavailable"}`… — **this is the problem**:
  `validAgentState` (`handlers.go:252-259`) accepts only `""`, `idle`, `active`, `stopped`, so the
  API **cannot** even express `backend_unavailable`; only the supervisor writes it
  (`supervisor/backend.go:66-83` → `markControlPlaneAgentState`). With no daemon in the stack there
  is no browser-observable path.
- **Status**: **blocked on B2 and B6, both.** B2 because no daemon in this stack can set the state;
  B6 because even if it were set, nothing renders it — the `agent missing · backend unavailable`
  label lives in the unmounted `AgentRow` (**B6**). Revision 2 briefly claimed that label was
  reachable and softened this to "B2 only"; that was the retracted finding. So if B2 lands alone,
  this case is still browser-untestable and only the API readback becomes available. Keep the Go
  coverage (`supervisor/backend_unavailable_test.go`) as the authority.

#### TSK-D12 — Two Task Runners race one task; exactly one claim wins
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *Two Task Runners racing the same ready task do not both take it — the claim is atomic
  and the loser is rejected.*
- **Target assertions**: exactly one claim succeeds and one is rejected with a conflict; the issue's
  `assignee` is the winner; no duplicate work follows.
- **Why neither attempted design works.** Two dead ends, recorded so they are not re-attempted:
  1. *Two full agent runs* (Revision 1). The stub skips its claim call entirely when
     `LOOM_ASSIGNED_TASK_ID` is set (`e2e/stubs/codex:83-85`), and the call it would otherwise make
     is `loom claim` — lock-file bookkeeping for `loom monitor`, whose own help says "failures do not
     affect the actual task claim" (`internal/cli/agent/claim.go:16-32`). The stub never calls the
     atomic `loom data claim` at all.
  2. *Two racing `loom data claim` invocations* (Revision 2). `--actor` is captured **for
     command-line parity and never used** — the flag's own comment says "Backend implementations
     derive the effective actor from their configured environment/session"
     (`internal/cli/data/claim.go:9-12`; registered at `:31-33`, never read in `RunE`). And with
     `LOOM_SERVER_URL` set, which every aft `run:` step needs, the CLI selects the **remote HTTP API
     backend** (`internal/cli/issue_backend_resolve.go:52-56`), so the claim executes inside
     `loom serve` "for the server-side actor" and re-claim by the same actor is **explicitly
     idempotent**: "Re-claim by the same actor is idempotent and returns the issue without error"
     (`internal/webui/service/issue_impl.go:365-368`). Both racers are one actor; both exit 0. No
     CLI-side environment variable changes this, because the actor is resolved server-side.
  3. Separately, the `LockConflict` classification Revision 2 expected belongs to the **supervisor's**
     conflict policy (`supervisor/claim.go:155`), not to the raw CLI command — so even a genuine
     conflict would not surface under that name here.
- **Rejected escape hatch**: bypassing `LOOM_SERVER_URL` to reach fleet-db directly with two distinct
  `LOOM_FLEET_DB_ACTOR` values *would* yield distinct actors
  (`internal/bootstrap/openstore.go:176-185`), but it routes around the server the suite exists to
  test and breaks actor fidelity. Not worth it for one edge case.
- **What it needs (B13)**: an actor-scoped claim path reachable from a test — either `--actor` honored
  end to end, or a claim endpoint that adopts the request's `X-Actor`. The header already travels on
  every fleet-db call (`internal/infra/fleetdb/client.go:10-11`) and lifecycle handlers already read
  it (`handlers/agents/handlers.go:85`), so the plumbing exists.
- **Interim authority**: the Go-level coverage. The invariant is real and tested there; only the
  aft-level demonstration is blocked.
- **Status**: **blocked on B13** (was optimistically "ready" in Revision 2).

#### TSK-D13 — Repo scope is enforced at claim time
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *A Task Runner scoped to repo A does not pick up work scoped to repo B.*
- **Preconditions**: two-repo workspace; agent `scoped-${RUN_ID}` created through the modal with
  **only repo B's chip selected**; one designed, ready task attributed to repo A.
- **Steps**: run the agent in single-task mode with the source-repo scope the supervisor would give
  it (`LOOM_SOURCE_REPOS=<repo B id>`, `supervisor/spawn.go:50-56`).
- **Assertions**
  - Exit 0 with `No tasks available for implementation.` — the repo-A task is filtered out by
    `fetchReadyIssues` → `opts.SourceRepos` (`automode_poller.go:64-94`,
    `internal/backend/fleet/deferred.go:124`).
  - Control: re-run with `LOOM_SOURCE_REPOS=<repo A id>` and assert the same task **is** available
    (or, if TSK-D9's fixture is in place, that it gets claimed). Without the control the first
    assertion is satisfied by any bug that makes the queue empty.
- **How to attribute the task to a repo** (Revision 1 flagged this as an open question; it is not):
  `POST /issues` accepts `source_repo` (`internal/webui/handlers/issues/issues.go:106`), maps it
  into `service.CreateIssueParams` (`write_ops.go:77`, `service/issue.go:103`), and the fleet
  backend filters ready candidates on it (`internal/backend/fleet/deferred.go:124` —
  `len(opts.SourceRepos) > 0 && !hasAnyString(opts.SourceRepos, issue.SourceRepo)`). So seed the
  task with `"source_repo": "<repo A name>"` directly.
- **Caveat to respect while authoring**
  - **Do not** test the `repo:<name>` *label* path. `agentEntryFromDomain`
    (`internal/cli/config/project.go:320-336`) never populates `Entry.Repo` for fleet-db-loaded
    agents, so `LOOM_AGENT_REPO` is never set for a UI-created agent and the label filter at
    `supervisor/claim.go:105-107` is dead code in practice. See **B7** — this is a suspected defect,
    not a test target.
- **Status**: ready to write (upgraded from "ready after B9" in Revision 1; B9 dissolved).

#### TSK-D14a / TSK-D14b — Deleting a Task Runner
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml` (no delete control exists in the
  UI; promotion condition: a delete affordance on the agents page).
Revision 2 called this one case "ready" while its body required blocked TSK-D9. Split:

**TSK-D14a — delete a freshly created Task Runner**
- **Intent**: *An API client deletes a Task Runner; the definition disappears from every operator
  surface and its name becomes reusable.*
- **Preconditions**: a modal-created agent, nothing run.
- **Steps**: `api DELETE /agents/<name>` → 200 `{"message":"agent deleted"}`; then browse.
- **Assertions**
  - `GET /agents` no longer lists it; the agents rail and the monitor `agent-activity-panel` no longer
    contain the name (allow one 5 s store-poll cycle plus a reload, as `zz-agent-flow:78-86` does).
  - The worktree directory still exists on disk. Document this as the current contract; if it is
    considered a leak, that is a FINDINGS entry, not a test failure.
  - Re-creating an agent with the same name now succeeds (the 409 from TSK-D5 is gone).
- **Status**: ready to write.

**TSK-D14b — delete a Task Runner that has run history**
- **Intent**: *Deleting a Task Runner does not cascade away the work it recorded.*
- **Preconditions**: **TSK-D9 has run** — hence the dependency.
- **Assertions**: on top of D14a's, the closed task is still closed and
  `GET /tasks/<id>/sessions` still returns the session with its transcript.
- **Status**: **blocked on B1** (inherits TSK-D9's blocker).

#### TSK-D15 — Lifecycle endpoint contracts (start / stop / restart / yield)
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml`. Justification for the surface tier:
  three of these four endpoints have **no UI caller at all**
  (`internal/webui/frontend/src/api/agents/agents.ts` exposes only `startAgent`). Promotion
  condition: agents-page lifecycle controls land.
- **Intent**: *The agent lifecycle contract the daemon reconciles against stays stable even while
  the UI has no controls for it.*
- **Assertions** (one agent, sequenced)
  - `POST /start` → **200**, body message `agent "x" started`; readback `state=active`,
    `desired_state=running`.
  - `POST /stop` → **200**, `agent "x" stopped`; readback `state=stopped`, `desired_state=stopped`.
  - `POST /restart` → **202**, `agent "x" restart requested`; readback `state=active`,
    `desired_state=running`.
  - `POST /yield` → **202**, `agent "x" yield requested`; readback `state=idle`,
    `desired_state=draining`.
  - `POST /start` with `{"task_id":"<id>"}` → 200 (the payload merge at `handlers.go:207-212`).
  - `POST /start` on an unknown agent → 404.
  - `PATCH /agents/<name> {"state":"bogus"}` → 400 `invalid state`; `{"desired_state":"bogus"}` →
    400 `invalid desired_state` (`handlers.go:97-104`).
  - `GET /agents/<name>/queue` → **501** with the `use monitor task queues` message
    (`handlers.go:169-171`).
- **Status**: ready to write.

---

### Group C — surfaces a Task Runner exposes

#### TSK-D16 — Archive Logs branch for a role=`task` agent
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *An operator opens a Task Runner's Logs tab, sees the honest empty state, and sees
  runtime-produced log content after the agent writes to its archive.*
- **Steps**: `open /ws/E2E-WS-TASK/agents/<name>` → `click {role: button, name: Logs, exact: true}` →
  assert `archive-empty` + copy `No logs available for this agent yet.` → `run:`
  `LOOM_TESTSUPPORT=1 … loom daemon seed-log --workspace E2E-WS-TASK --agent <name> --content -` →
  click the refresh button (`{selector: "div:has(> [data-testid='log-viewer']) > button"}` — it has
  no testid, **B4**) → `wait {text: AFT-TASK-LOG-MARKER}` → `expect {visible: {testid: terminal-container}}`.
- **Additional assertion (the delta over `zz-agent-flow` case 1, which does this for a *lead*)**:
  `api GET /agents/<name>/terminal/info` → `data.mode == "archive"`, and
  `expect {attr: {selector: "[data-testid=log-viewer] span[data-state]", name: "data-state",
  equals: "connected"}}`. The span has no testid, which is fine: `AttrExpectSchema` accepts a raw
  `selector:` (`../testing-app/src/types.ts:86-88`), and the expected value here is a single known
  string. The heading text reads `Archive snapshot`, not
  `Live terminal` (`AgentLogsTab.tsx:87-89`). This is the explicit negative of the
  `make test-aft-terminal` tier.
- **Status**: ready to write.

#### TSK-D17 — Task Runner appears on every agent surface with the right identity
- **Tier**: `suites/zz-task-runner.test.yaml` (fold into TSK-D1 rather than a standalone test if
  runtime budget is tight).
- **Intent**: *A newly created Task Runner is discoverable from the agents rail, the workspace tree,
  and the monitor.*
- **Assertions**: name present in `agents-page`; present under
  `[data-testid=agent-section-background]` in the workspace tree (the Background subgroup — proof
  the role was classified as a background agent, `WorkspaceTree/AgentSection.tsx:130`); present in
  `agent-activity-panel` on `/monitor` with `expect {notText: No agents found}`; the deep link
  `/ws/E2E-WS-TASK/agents/<name>` resolves (`expect {url: ...}`) and `agent-editor-groups` renders
  the six tabs `Terminal, Info, Git, Logs, Diff, Files`.
- **Status**: ready to write.

#### TSK-D18 — Quarantined task is visibly quarantined
- **Tier**: `suites/zz-task-runner.test.yaml`.
- **Intent**: *A task the daemon quarantined after repeated no-progress kills is visibly marked on
  the board, so an operator can see why an agent stopped making progress on it.*
- **Steps**: an API-client actor sets the terminal state the supervisor would write —
  `status: "blocked"`, `assignee: ""`, labels `+["loom:quarantined"]`, exactly the single update at
  `supervisor/quarantine.go:435-460` — then the operator browses the board.
- **Assertions**: the card renders the badge — selector `[aria-label="Task quarantined"]` or text
  `Quarantined` (`components/IssueCard/IssueCard.tsx:310-319`, **no testid**, see **B4**) — and the
  card sits in `section[data-status=blocked]`. Assert the `title` attribute carries the kill-timeline
  explanation.
- **Edge rationale**: `COVERAGE-PLAN.md:125` deferred quarantine as "supervisor runtime, not
  browser-observable deterministically". That is true of *entering* quarantine; the *rendering* is
  browser-observable once an API actor writes the same state, and it is the operator-facing half.
  Actor fidelity holds: the daemon really does mutate via the store.
- **Status**: ready to write.

#### TSK-D19 — Agent health fields are exposed on the wire (and rendered nowhere)
- **Tier**: `surface-suites/agent-lifecycle-contracts.test.yaml`. Deliberately a surface probe: there
  is **no mounted UI** for these fields (**B6**), so this cannot claim a user scenario. Promotion
  condition: any surface mounts agent health — at which point this becomes a real browser case.
- **Intent**: *The agent-health fields an operator surface would need are present and correctly
  shaped on the agents API, so the contract does not rot before a UI arrives.*
- **Preconditions**: a modal-created task agent; one task moved to `in_progress` and assigned to it
  by an API-client actor while nothing has started the agent.
- **Steps**: `run:` + python over `GET /agents` (no `GET /agents/{name}`, see *Mechanics*).
- **Assertions**
  - `live_status` is absent or `idle` — **not** `working`; `active_task_id` and `active_phase` are
    empty, since fleet-db sets them only when `live_status == working`
    (`internal/domain/agent.go:65-71`).
  - `last_error_class` is absent for an agent that never ran.
  - The assigned issue reads `status == "in_progress"` with `assignee == <agent>` — i.e. the API can
    express "assigned but nobody is working it", which is the state a future UI must explain.
- **Explicitly not asserted**: anything in the browser. `agent missing` and
  `agent missing · <class>` are unreachable — `resolveCardAgent`/`AgentRow` are unmounted
  (`IssueCard.tsx:27`) and a vitest suite pins the row as permanently absent
  (`__tests__/IssueCard.agentRow.test.tsx:6-10`, `:35-40`). Revision 2 asserted the opposite; do not
  restore it.
- **Status**: ready to write (surface tier, low value until B6 is fixed — write it last).

---

## Part 2 — Real-backend tier (non-deterministic)

### Already pinned by the existing HELLO.md suites — do not duplicate

`tests/aft/real-suites/zz-real-codex-epic.test.yaml` and its `-claude`, `-cursor`, `-opencode`
siblings each already assert, for **path B**: the workspace default backend round-trip
(`PATCH`/`GET /config/backend`), board delivery before the trigger, `epic-runner` completion within
three chained ~110 s polls, the closed card in `section[data-status=done]` under `?groupBy=none`,
`sessions.0.backend`, `exit_code == 0`, `evidence.status == "ok"`, `evidence.conflicts == []`,
`files_changed >= 1`, the `usage_status` all-null-or-all-numeric contract, `has_transcript == true`,
`has_diff == true`, a transcript real-signature check, the session diff containing `HELLO.md`, and
`HELLO.md` on disk with contents exactly `hello world`.
`tests/aft/real-terminal-suites/` additionally pins the live-tmux Logs branch for a raw-POST task
agent. New cases below assume all of that and add only what is missing.

#### TSK-R1 — UI-created Task Runner, real backend, end to end ★
- **Tier**: new `tests/aft/real-suites-task-agent/` behind the existing `AFT_REAL_BACKEND` gate,
  invoked through a new `make test-aft-real-task` target (`AFT_SUITES=` that directory, mirroring
  `test-aft-terminal`, `Makefile:386-388`).
- **Intent**: *An operator creates a Task Runner through CreateAgentModal, delegates a real task to
  it, and the real backend CLI writes, commits, and closes it.*
- **Steps**: modal-create the agent (human actor, real browser); seed epic + child task whose
  `design` is the HELLO.md instruction, reused verbatim so the assertions transfer; start the agent
  with `loom --workspace <WS> --backend <b> task <name> --auto -m 1 -t 5` from a detached `run:`
  step in the `real-terminal-suites` style (`setsid`, `&`, log to `$AFT_WORK_DIR`); chained bounded
  polls until closed.
- **Assertions** (the *new* part is the agent identity and the path-A session, not the file):
  - Session `agent_name` equals the **modal-created** name, `status == "completed"`,
    `files_changed >= 1`, `has_transcript`, `has_diff`.
  - `HELLO.md` exists inside **that agent's worktree** (`worktrees/<repo>/<name>/HELLO.md`), which
    is a stronger placement claim than the existing `find tmp/e2e-workspace` guard.
  - The agents page Diff tab lists `HELLO.md` and opens its patch; the Logs tab reports
    `mode == "tmux"` while running and yields archive content after exit.
  - `usage_status` contract, reusing the real tier's python assertion unchanged.
- **Parity**: **run on all four backends.** `backendArgs()` differs per backend
  (`local-task-runner.ts:449-492` for path B; `internal/cli/backends/backend_*.go` for path A), and
  path A uses a *different* invoker (`InvokeNonInteractive`/PTY, `task.go:161`) than path B, so
  per-backend arg and stream-parsing bugs are exactly what this catches. Note claude does **not**
  self-degrade to non-interactive without a TTY the way codex/cursor/gemini do
  (`backend_codex.go:63-70`) — expect claude to need `--auto`/tmux, not the bare single-task path.
- **Status**: ready to write; needs the new tier directory + make target.

#### TSK-R2 — One Task Runner, multi-task epic, in dependency order
- **Tier**: `real-suites-task-agent/`, **codex only**.
- **Intent**: *A Task Runner in continuous mode works an epic's tasks one at a time and respects the
  order its designs imply.*
- **Steps**: epic with three children; child B's design requires the file child A writes; child C
  requires B's. **Then seed real dependencies** — B depends on A, C on B — with
  `POST /api/workspaces/{ws}/issues/{B}/dependencies {"depends_on_id": "<A>"}`, the path
  `dependencies-graph.test.yaml:1-2` documents. `depends_on` is **not** a field on
  `IssueCreateRequest` (`handlers/issues/issues.go:82-110`), so it cannot be set at create time.
  Start `loom … task <name> --auto -m 3 -t 10`.
- **Why the dependencies are mandatory**: narrating "A then B then C" in the designs orders nothing.
  `SelectBestTask` ranks candidates by score, then priority number, then alphabetical id
  (`internal/cli/task_router.go:139-150`) — with equal priorities the agent would pick alphabetically
  and the ordering assertion would pass or fail on the RUN_ID's id sort, not on dependency logic. The
  precedent gets this right by passing `--depends-on` (`e2e/epic_runner_codex.sh:153`); Revision 2's
  version dropped it.
- **Assertions**: all three closed; three sessions, each with `files_changed >= 1`; commit order in
  the worktree's `git log` matches A→B→C; the agent's `active_task_id` changes over time and is
  empty at the end. A blocked child must not be claimable before its blocker closes — assert that B
  is absent from the ready queue while A is open.
- **Parity**: codex only — this is about the supervisor's task loop, not backend wire formats, and
  it costs three real runs.
- **Status**: ready to write; long (budget ~10 min).

#### TSK-R3 — Impossible design: the agent cannot complete, and says so
- **Tier**: `real-suites-task-agent/`, **codex only**, marked advisory.
- **Intent**: *A task the agent cannot satisfy does not silently close.*
- **Safety constraint that drives the design.** Revision 2 proposed "modify `/etc/hosts` and verify
  the change". **Do not do this.** Path A launches codex with
  `--dangerously-bypass-approvals-and-sandbox` on both the interactive
  (`internal/cli/backends/backend_codex.go:42`) and non-interactive
  (`:108`) paths, so the agent has no sandbox standing between it and the operator's machine. A test
  must never ask a real agent to touch anything outside its worktree.
- **Steps**: a child task confined to repo A whose acceptance condition is **objectively
  self-contradictory and repo-local**. Concretely: add a `gate:` prerequisite to repo A that runs a
  checked-in script asserting a sentinel file satisfies two mutually exclusive predicates (e.g.
  `value > 10` **and** `value < 5`). The design instructs the agent to make `make gate` pass. Run
  `--auto -m 1 -t 5`.
- **Assertions — a single concrete pass/fail, no "inconclusive"**: the repo's own gate is the oracle.
  After the agent exits, run `make gate` in its worktree; it must still fail. Then assert **the
  product did not report success over a failing gate** — i.e. **either** the task is not `closed`,
  **or** it is closed and that is a product failure the test reports. Expressed as one condition:
  `gate_fails AND task_closed` ⇒ **fail the test**. This is stable regardless of what the model
  chose to do, which is what Revision 2's "treat a clean close as inconclusive" could not deliver.
- **Supporting API readbacks**: a session exists and is terminal; if the agent gave up cleanly, its
  `status ∈ {failed, aborted}` or `exit_code != 0`, and `error_class` is recorded
  (`internal/sessions/types.go:30-75`). Record these as evidence, not as the pass condition — which
  class a real failure produces is not deterministic.
- **No UI assertion.** Revision 2 asserted an `agent missing · <class>` card label. That surface does
  not exist: `resolveCardAgent`/`AgentRow` are unmounted (`IssueCard.tsx:27`) and a vitest suite pins
  the row as permanently gone (`__tests__/IssueCard.agentRow.test.tsx:6-10`). See **B6**.
- **Status**: ready to write, inherently non-deterministic in *how* it fails but not in *whether* it
  passes.

#### TSK-R4 — Auth missing → the preflight class, not a fake completion
- **Tier**: `real-suites-task-agent/`, **claude only.** Revision 2 also listed cursor; that is not
  achievable. Cursor's `APIKeySet` is true when `CURSOR_API_KEY` is set **or** when the binary is
  installed and `cursorAuthStatus()` returns nil — and that function *executes* `cursor-agent status`
  (`internal/cli/backends/backend_cursor.go:135-137`, `:150-160`). The harness preflight requires that
  same command to succeed before launching (`run-aft.sh:191-196`), and real mode already unsets
  `CURSOR_API_KEY`. Both read one process-level login state, so **no server-scoped environment makes
  cursor report logged out** — B10's env controls cannot reach it. A cursor variant would need its own
  seam (a `cursor-agent` shim on the server PATH that fails `status`), which is not worth building for
  one assertion. Codex is excluded for the same class of reason: the tier's own preflight refuses to
  start without `~/.codex/auth.json`.
- **Intent**: *An operator whose backend CLI is installed but not authenticated is told so before
  anything is queued.*
- **Steps**: set the workspace default backend to the real CLI, then re-point **only the server
  process's** credential lookup at an empty directory, and `POST /workflows/epic-runner`.
- **The contradiction Revision 1 missed**: you cannot simply export `CLAUDE_CONFIG_DIR=<empty>`
  around `make test-aft-real-claude`. The harness runs its **own** credential preflight first and
  exits 1 if `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/.credentials.json` is absent
  (`tests/aft/run-aft.sh:184-190`) — so the run dies before the product's preflight is ever
  exercised. The harness's check and the server's environment must be decoupled: the check keeps
  reading the operator's real config, while the server env line (`run-aft.sh:285-289`) receives the
  empty-dir override. That is **B10**.
- **Assertions**: non-2xx whose message contains `is missing auth` and the class token
  `local_backend_auth_missing` (`runtimepreflight/preflight.go:98-101`, backend check at
  `internal/cli/backends/backend_claude.go:97-140`); no run created; the task untouched.
- **Status**: **blocked on B10** (was mislabelled "ready" in Revision 1). Note that **TSK-D11c
  covers the same product contract deterministically** once B10 lands, so this real-CLI variant is
  a lower priority than its Revision 1 billing suggested — its only added value is proving the real
  backend's own `HealthCheck` implementation, not the preflight wiring.

#### TSK-R5 — Budget and watchdog timeout
- **Tier**: deferred. **Blocked on B2.**
- **Intent**: *A Task Runner that produces no output is killed by the watchdog and restarted within
  its budget; a Task Runner over its cost budget stops.*
- **Why blocked**: both levers live on the **daemon supervisor**, which the aft stack does not run.
  `LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS` (`restart.go:384-389`) and `LOOM_MAX_BUDGET_USD`
  (`spawn.go:134-136`) are both supervisor-side. The budget lever is also **not reachable over
  HTTP at all**: it lives on *role* config, `AgentCreateInput` cannot set it, and there is no
  `/roles` route anywhere in `internal/webui` — the only path is the CLI
  (`loom role set <NAME> max_budget_usd <V>`, `loom role show <NAME> --json` to read it back;
  `internal/cli/role/role_cmd.go:37-121`). Restart-budget arithmetic (`restart.go:168-219`) is
  comprehensively covered by Go tests; a browser tier would add cost, not confidence.
- **Recommendation**: keep with the Go suite unless **B2** lands.

#### TSK-R6 — Podman variant
- **Recommendation: not warranted yet.** `tests/aft/real-suites-podman/` proves ModeCloud selection
  and that `/work` is a named volume, both of which are properties of the **path B** driver/worker
  containers. The supervisor that owns path A does not run in that stack, so a podman variant of
  TSK-R1 would have nothing container-specific to assert. Revisit if the daemon is ever hosted in
  the container stack; at that point the interesting claim is that a *named agent's* worktree and
  session artifacts also stay inside the volume.

### Parity summary

| Case | codex | claude | cursor | opencode | rationale |
|---|---|---|---|---|---|
| TSK-R1 | ✅ | ✅ | ✅ | ✅ | per-backend invocation args + stream parsing + session projection |
| TSK-R2 | ✅ | — | — | — | supervisor loop, backend-agnostic; 3× cost |
| TSK-R3 | ✅ | — | — | — | advisory, model-dependent |
| TSK-R4 | — | ✅ | ✅ | — | needs an installed-but-unauthenticated CLI |
| TSK-R5 | deferred | | | | blocked on B2 |

---

## Part 3 — Blockers and new seams needed

### B1 — No deterministic stub that completes a task through path A ★
`e2e/stubs/codex` in its default mode (`:125-136`) drains stdin and prints one canned JSON line.
It claims nothing and writes nothing — which is exactly right for path B, where the driver owns
claiming, and exactly wrong for path A, where the *agent* is supposed to do the work. Its
`STUB_CODEX_EPIC_RUNNER=1` mode (`:51-123`) does the full flow, but it also runs
`make gate` (`:114`) and `loom push "$agent_name"` (`:118`) under `set -euo pipefail`, and the aft
workspace repo (`scripts/start-e2e-server.sh:162-173`) has neither a Makefile nor a remote.

Reaching `make gate` at all requires **five** fixture facts, not the two Revision 1 listed. In
stub execution order:

| # | Stub line | Requirement | Precedent |
|---|---|---|---|
| 1 | `:103` `mkdir -p $(dirname "$write_path")` | the write path must sit **inside `epic-runner-output/`**, or… | `e2e/epic_runner_codex.sh:139` |
| 2 | `:112` `>> epic-runner-output/order.log` | …`epic-runner-output/` must pre-exist — this append is **unconditional** and nothing creates that directory when `write_path` points elsewhere | — |
| 3 | `:114` `make gate` | a `Makefile` with a no-op `gate:` target | — |
| 4 | `:117` `git commit` | **repo-local git identity** (`user.name`/`user.email`) | `scripts/start-e2e-server.sh:170-171` |
| 5 | `:118` `loom push "$agent_name"` | an `origin` remote (a local bare repo suffices) | — |

Requirements 1/2 and 4 are the ones Revision 1 missed; each independently aborts the run under
`set -euo pipefail` *before* anything observable happens, which would have read as an inscrutable
harness failure rather than a fixture gap.

Two ways forward, in preference order:

1. **Fixture-only (no product change).** The seven-step *Shared setup* covers all five requirements.
   `STUB_CODEX_EPIC_RUNNER` is already on the env allowlist
   (`internal/cli/envfilter/envfilter.go:39`), so a `run:` step can export it plus
   `LOOM_ASSIGNED_TASK_ID` and get the real flow. **Verify while authoring**: that `loom push
   <agent>` succeeds against a bare local remote, and that the wrapper-backed
   `InvokeNonInteractive` path (`task.go:161` → `tsruntime.Invoker`) still invokes codex with
   `exec --json …` so the stub's arg matcher (`e2e/stubs/codex:28-34`) fires. If either fails, use
   option 2.
**Two gaps the fixture route cannot close** (found in round 2, and the reason option 2 below is now
the recommendation rather than a fallback):

- **It never proves a claim.** `runTaskDaemon` trusts `LOOM_ASSIGNED_TASK_ID` without claiming
  (`internal/cli/agent/task.go:131`, `:140-146`), and the stub skips its own claim call when that
  variable is set (`e2e/stubs/codex:83-85`) — and even unset, the call is bookkeeping-only
  `loom claim`, never the atomic `loom data claim`. A fixture-only TSK-D9 commits and closes a task
  that was never claimed.
- **It produces no archive log.** Archive wiring lives only in the supervisor's `setupAgentLogFile`
  (`internal/cli/daemon/supervisor/spawn.go:271-285`), so a directly launched agent's output never
  reaches the Logs tab. TSK-D9's Logs assertion was removed for this reason.

2. **New stub mode (now the recommended route).** Add `STUB_CODEX_TASK_AGENT=1` to `e2e/stubs/codex`:
   **`loom data claim`** (the atomic one — see TSK-D12) → read design → write the file → `git
   add`/`commit` → `loom data close` → `loom complete`, **without** `make gate`, **without**
   `loom push`, and with its own `mkdir -p` for whatever path it writes. Add the exact name to the
   allowlist at `envfilter.go:38-39` (the comment there — "Exact matches keep arbitrary STUB_*
   values out" — is the convention to follow). This drops four of the five fixture requirements,
   and because it calls `loom data claim` it also unblocks the end-to-end half of TSK-D12 that the
   current stub structurally cannot reach.

### B2 — The aft stack runs no `loom daemon`
`scripts/start-e2e-server.sh:190-208` starts `loom serve` and nothing else. Consequences:
`POST /agents/{name}/start` writes state and queues a command that is never acked
(`daemon_command_poller.go:38-121`); `backend_unavailable` and quarantine can never be *entered*;
restart budgets never run. Options:

- **(a) Accept it** — assert control-plane truth only (TSK-D7, TSK-D15) and start agents directly
  from `run:` steps (TSK-D9). This is what this plan assumes.
- **(b) Opt-in daemon in the harness** — a `AFT_WITH_DAEMON=1` branch in `run-aft.sh` that launches
  `loom daemon --workspace E2E-WS-TASK` detached with the stub PATH, and a matching teardown by
  process signature. Precedent exists: `real-terminal-suites:44-49` already launches and
  `real-codex-teardown.sh:71-93` already reaps a long-lived detached loom process.
- **(c) A bounded reconcile seam** — a `LOOM_TESTSUPPORT=1`-gated `loom daemon reconcile-once`
  that drains queued `AgentCommand`s and exits. Smallest surface, keeps ADR-0001's shape
  (hidden, env-gated, runs the product's own flow), and would make "the UI Start button actually
  starts something" testable without a long-lived process.

### B3 — `seed-session` is still missing
FINDINGS §3.10 already names it. `seed-transcript` creates a session record but hardcodes
`AgentID: "distributed-smoke-seed"` (`internal/cli/daemon/seed_transcript_cmd.go:75-84`), so it
cannot stand in for *this* agent's run. A `loom daemon seed-session --workspace --agent --task
--status --exit-code --files-changed [--diff <file>]` composing `sessions.Store` the way
`seed-worktree` composes `localworkspace` would let per-agent session, `has_diff`, and
`usage_status` assertions run without a real backend at all — i.e. it would de-risk TSK-D9's
assertions even if B1 slips.

### B4 — Missing testids (all cheap)
Every one of these currently forces a structural or text selector:

| Surface | Today | Ask |
|---|---|---|
| Agent row / card | `AgentCard.tsx:71-90` — no testid; `agent-card-<name>` exists **only in vitest mocks** | `data-testid="agent-card-<name>"` in production |
| Agent status on the agents page | plain text at `views/AgentsPage.tsx:455-462` | reuse `agent-status-badge` or add `agent-detail-status` with `data-status` |
| Logs refresh button | `AgentLogsTab.tsx:90-92` — selected via `div:has(> [data-testid='log-viewer']) > button` in `zz-agent-flow:70` | `data-testid="log-refresh"` |
| Editor tab buttons | `AgentEditorGroups.tsx:197-211` — role+name only | `data-testid="agent-tab-<id>"` |
| Repo chips | `CreateAgentModal.tsx:584-602` — positional `first: true` | `data-testid="create-agent-repo-chip-<repo>"` |
| Quarantine badge | `IssueCard.tsx:310-319` — `aria-label` only | `data-testid="issue-quarantined-badge"` |
Removed from this list in Revision 3: the "card agent row" testid. `AgentRow` is unmounted dead code
(**B6**), so a testid on it would be untestable by construction. The real ask there is a *surface* that
renders agent health, not a testid.

Not on the list: the log viewer's `data-state` span. It has no testid, but `expect.attr` takes a raw
`selector:`, so a testid there would be cosmetic rather than enabling.
| Modal Cancel | `CreateAgentModal.tsx:383-390` — text only | `data-testid="create-agent-cancel"` |

### B5 — Stop / restart / yield / delete / auto have no *mounted control*
Precisely: `api/agents/agents.ts` exports `startAgent` and no other lifecycle wrapper
(`:33-42`), and there is no stop/restart/yield client anywhere. A `deleteWorkspaceAgent` wrapper
**does** exist (`api/workspace/workspace.ts:329`) but is only ever called as rollback after a failed
lead spawn (`IssueDetailPanel.tsx:1138`, `hooks/workspace/startEpicRunnerForIssue.ts:145`,
`views/IssueDetailPage.tsx:173`) — never from an operator-facing control. So the accurate claim is
"no mounted controls", not "no client code". Those endpoints therefore belong in `surface-suites/` with an
explicit promotion condition (TSK-D14, TSK-D15). If mounting controls is on the roadmap, note that
the daemon-socket module (`internal/webui/handlers/agentcontrol/handlers.go:13-57`) has *different*
semantics from the fleet-db module for the same paths — stop without `force` becomes a yield and
returns 202 — so the UI will need to know which module is registered
(`internal/webui/app/server_modules.go:123-154`).

### B6 — `last_error_class` and `backend_unavailable` have no mounted UI, anywhere
**Revision 1 was right; Revision 2 was wrong; this is the settled position.** The field exists on the
wire (`internal/domain/agent.go:73-77`) and in the generated OpenAPI types, and **nothing in the
running app renders it.**

Evidence, in the order it should be checked by anyone tempted to re-litigate this:

- The only frontend readers of `last_error_class` are `cardAgentView.ts:180` and that file's own unit
  tests. Nothing else in `internal/webui/frontend/src` references it.
- `cardAgentView.ts:180` sits inside `resolveCardAgent`, which **production `IssueCard` does not
  import**: its import list takes only `resolveCardFooterBadge` (`IssueCard.tsx:27`), used once at
  `:188`. `AgentRow` — which holds `ERROR_CLASS_LABELS` and would render
  `agent missing · backend unavailable` — is mounted nowhere.
- The removal was deliberate and is *pinned by a test*: `__tests__/IssueCard.agentRow.test.tsx:6-10`
  states "IssueCard no longer renders an inline AgentRow (Aether V3 alignment) … These tests pin
  that the agent row stays gone across the column states that used to show it", and asserts exactly
  that for all six column states (`:35-40`). **An aft assertion that `agent missing` appears would
  contradict a passing unit test.**
- `AgentState` `backend_unavailable` likewise has no frontend branch of its own.

The product story the field's own doc comment describes — "Lets the UI explain why a stalled agent
stopped instead of showing a bare 'agent missing'" (`domain/agent.go:75-77`) — is **unimplemented**.
That is the FINDINGS entry: not a rendering bug, a missing surface. The comment in
`IssueCard.agentRow.test.tsx` says live agent activity now belongs to "the issue detail panel and the
epic header", so that is where a future implementation would land.

Consequences for this plan: **TSK-D19 is rebuilt as an API wire-contract probe** (surface tier — it
guards the field's shape for the day a UI mounts it), **TSK-R3 loses its error-class UI assertion**
and keeps only API-level readbacks, and **TSK-D11b stays blocked on B2** with no UI assertion
available even if B2 lands.

### B7 — Suspected defect: `Entry.Repo` is never populated for fleet-db agents
`agentEntryFromDomain` (`internal/cli/config/project.go:320-336`) does not set `Repo`. Downstream:
`LOOM_AGENT_REPO` is never exported (`supervisor/spawn.go:122-123`), so the `repo:<name>` label
filter in `buildClaimOpts` (`supervisor/claim.go:105-107`) and in the CLI's own availability check
(`internal/cli/agent/task.go:196`) is dead for every UI-created agent. Repo scoping still works, but
only through the `SourceRepos` path (`internal/cli/config/repos.go:14-60`). Two consequences: TSK-D13
must assert the `SourceRepos` behavior, and this deserves a FINDINGS entry in its own right, because
the two mechanisms are documented as if both were live. Related: `internal/cli/task_router.go:111-119`
scores a repo mismatch at **5, not 0**, and `SelectBestTask` keeps anything above 0
(`task_router.go:146`) — so if the backend `Ready` filter ever stops applying, repo scope degrades
silently from *enforcement* to *deprioritization*. Worth a Go regression test regardless of aft.

### B8 — `StartWorkButton` is dead code
`components/IssueDetailPanel/actions/StartWorkButton.tsx` (with `start-work-button`,
`start-work-popover`, `agent-option-<name>`, `start-work-error`) is imported nowhere; the only
reference is a stale comment at `IssueDetailPanel.tsx:603`. Do not write tests against it. Either
mount it or delete it — the census currently counts its testids as uncovered surface forever.

### B9 — REMOVED (was: how does an API-created issue get a `source_repo`?)
Not a blocker. `POST /issues` accepts `source_repo` (`internal/webui/handlers/issues/issues.go:106`),
`createParamsFromRequest` maps it (`write_ops.go:77` → `service/issue.go:103`), and the fleet backend
filters ready candidates on it (`internal/backend/fleet/deferred.go:124`). TSK-D13 is ready.
Retained as a numbered entry so Revision 1 cross-references still resolve.

### B10 — The harness cannot give the server a different credential environment ★
Two cases need the *server process* to see missing backend credentials while the harness itself keeps
working, and neither is expressible today:

- **TSK-D11c** (deterministic auth-missing) needs a stub-installed backend with no credentials.
- **TSK-R4** (real auth-missing) needs the real CLI installed but unauthenticated — yet
  `run-aft.sh:184-190` runs its own preflight and exits 1 when
  `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/.credentials.json` is missing, so exporting the override
  around `make test-aft-real-claude` kills the run *before* the product preflight is reached.

There is also a live hazard in the **existing** deterministic tier: the server's PATH is
`e2e/stubs:$PATH` (`run-aft.sh:293`), and `gemini` has no stub, so a `gemini`-selecting code path
falls through to the operator's real CLI. Combined with a leaked `GEMINI_API_KEY`/`GOOGLE_API_KEY`
(the deterministic branch unsets nothing — only real mode uses `$REAL_UNSET_FLAGS`,
`run-aft.sh:285-289`), gemini's health check would pass (`backend_gemini.go:117-141`) and the
"deterministic, no model calls" tier could make a real paid call.

**Ask** — one small change at the server-launch site (`run-aft.sh:285-295`), three benefits:
1. In the deterministic branch, `env -u GEMINI_API_KEY -u GOOGLE_API_KEY` for the server. Closes the
   hazard and makes TSK-D11a's fail class stable.
2. Honour optional `AFT_SERVER_ENV_UNSET="VAR1 VAR2"` and `AFT_SERVER_CLAUDE_CONFIG_DIR=<dir>`,
   applied **only** to the server `env` line, never to the harness's preflight. Unblocks TSK-D11c
   and TSK-R4.
3. Document that the harness's credential preflight intentionally reads the operator's real config
   even when the server is being starved — otherwise the next reader will "fix" the asymmetry.

**Scope limit — B10 unlocks claude, not cursor.** Claude's health reads a credential *file* whose
path is env-controlled, so a server-only `CLAUDE_CONFIG_DIR` starves it. Cursor's health *executes*
`cursor-agent status` (`backend_cursor.go:135-137`, `:150-160`), the same command the harness preflight
requires to succeed (`run-aft.sh:191-196`) — one process-level login state, no env seam. A cursor
variant needs a failing `cursor-agent` shim on the server PATH, which is out of scope here. TSK-R4 is
narrowed to claude accordingly.

### B11 — Product defect: the zero-repo modal promises a scope the service refuses
`create-agent-no-repos` tells the operator "No repos yet — add one from the sidebar first. This agent
will run with workspace scope." (`CreateAgentModal.tsx:569-576`). Submitting a background agent in
that state fails: `SelectAgentRepos` returns nothing
(`internal/localworkspace/localworkspace.go:491-528`) and `ensureLocalAgentWorktrees` rejects the
empty set with `workspace has no repos for agent` (`agent_service.go:416-422`), after which the
compensating delete removes the row (`:384`). Either the copy should stop promising workspace scope,
or zero-repo background agents should be creatable without a worktree. TSK-D6 asserts both halves so
the contradiction is guarded whichever way it is resolved. Candidate FINDINGS entry.

### B12 — No injectable failure for `POST /agents/{name}/start`
TSK-D8b needs the start call to fail while the agent stays in the dropdown's list. Today the only
way to make it fail is to remove the agent, which also removes the button
(`AssigneeDropdown.tsx:142-157`) and broadcasts a refresh (`handlers/agents/handlers.go:124`), so the
rollback path at `IssueDetailPanel.tsx:924-934` has no stable browser test. Smallest seam consistent
with ADR-0001: a `LOOM_TESTSUPPORT=1`-gated request header (e.g. `X-Loom-Test-Fail: lifecycle`) that
makes `handleLifecycle` return 500 before touching the store. Until then the rollback belongs in the
frontend unit suite alongside the existing assignee tests
(`components/IssueDetailPanel/__tests__/IssueDetailPanel.test.tsx:905-918`).

### B13 — No actor-scoped claim path a test can drive
TSK-D12 needs two claimants with genuinely distinct identities. Today it cannot have them:

- `loom data claim --actor` is captured "for command-line parity" and **never read** — the flag's
  comment states "Backend implementations derive the effective actor from their configured
  environment/session" (`internal/cli/data/claim.go:9-12`; registered `:31-33`, unused in `RunE`).
- With `LOOM_SERVER_URL` set (mandatory for aft `run:` steps) the CLI uses the remote HTTP API backend
  (`internal/cli/issue_backend_resolve.go:52-56`), so the claim runs inside `loom serve` **for the
  server-side actor** — identical for every caller.
- Re-claim by the same actor is deliberately idempotent (`internal/webui/service/issue_impl.go:365-368`),
  so both racers succeed and there is no loser to assert on.

**Ask** (either one suffices): honour `--actor` end-to-end on the claim path, or have the claim
endpoint adopt the request's `X-Actor`. The header is already sent on every fleet-db call
(`internal/infra/fleetdb/client.go:10-11`, `:49`) and lifecycle handlers already read it
(`handlers/agents/handlers.go:85`), so this is wiring rather than new architecture. Until then the
atomic-claim invariant stays a Go-test concern.

---

## Coverage table

Legend — **Status**: `ready` = writable today; `blocked:Bn` = needs the named seam;
`deferred` = deliberately out of the browser tiers.
**Existing** names any test that already covers part of the row.

| ID | Case | Kind | Tier | Existing coverage | Delta | Status |
|---|---|---|---|---|---|---|
| TSK-D1 | Task Runner, single-repo scope | happy | `suites/zz-task-runner` | `zz-agent-flow` case 5 (definition only) | scope readback + worktree on disk | ready |
| TSK-D2 | Task Runner, cross-repo scope | happy | `suites/zz-task-runner` | `zz-agent-flow` case 5 (1-repo ws, indistinguishable) | 2-repo ws; worktree in **every** repo | ready |
| TSK-D3 | Backend dropdown selection | happy | `suites/zz-task-runner` | none | `create-agent-backend`; `/api/backends` option set | ready |
| TSK-D4 | Name validation + normalization | edge | `suites/zz-task-runner` | none | disabled-submit contract; lowercase normalization | ready |
| TSK-D5 | Duplicate name → 409 in dialog | edge | `suites/zz-task-runner` | none | `create-agent-error`; modal stays open; no 2nd agent | ready |
| TSK-D6 | Repo-less workspace: copy vs 400 | edge | `surface-suites` + tier-1 browser half | none | 400 + compensating delete **+ the B11 contradiction** | ready |
| TSK-D7 | Delegation starts the agent | happy | `suites/zz-task-runner` | none | `agent-assignee-<name>`; `state/desired_state` readback | ready |
| TSK-D8a | Start a missing agent → 404 | edge | `surface-suites` | none | 404 (the "nothing queued" half is unobservable) | ready |
| TSK-D8b | Failed start rolls issue back | edge | `suites/zz-task-runner` | vitest `IssueDetailPanel.test.tsx:905-918` (assignee save) | rollback PATCH in a browser | blocked:B12 |
| **TSK-D9** | **UI-created agent claims→commits→closes** | happy | `suites/zz-task-runner` | **none in any tier** | path A with an agent definition; session + diff + logs | **blocked:B1** |
| TSK-D10 | No ready work → idle, no session | edge | `suites/zz-task-runner` | none | `has_design` gate; zero-session contract | ready |
| TSK-D11a | Missing backend binary → preflight | edge | `suites/zz-task-runner` | none | fail-closed class; nothing queued | blocked:B10 |
| TSK-D11b | Missing backend → agent state | edge | `surface-suites` | Go: `supervisor/backend_unavailable_test.go` | — | blocked:B2 |
| TSK-D11c | Auth missing → preflight class | edge | `suites/zz-task-runner` | none | `local_backend_auth_missing`, deterministically | blocked:B10 |
| TSK-D12 | Two agents race the atomic claim | edge | `suites/zz-task-runner` | Go unit tests only | — | blocked:B13 |
| TSK-D13 | Repo scope enforced at claim | edge | `suites/zz-task-runner` | none | `SourceRepos` filter both ways | ready (B9 dissolved) |
| TSK-D14a | Delete a fresh agent | edge | `surface-suites` | `zz-agent-flow` teardown (untested delete) | rail/monitor removal; name reusable | ready |
| TSK-D14b | Delete an agent with history | edge | `surface-suites` | none | sessions + closed task survive | blocked:B1 |
| TSK-D15 | start/stop/restart/yield contracts | edge | `surface-suites` | none | 200/200/202/202 + state pairs + 400/404/501 | ready |
| TSK-D16 | Archive Logs branch for role=task | happy | `suites/zz-task-runner` | `zz-agent-flow` case 1 (lead) | `terminal/info mode == archive`; task role | ready |
| TSK-D17 | Agent visible on all surfaces | happy | `suites/zz-task-runner` | `zz-agent-flow` case 1 (lead) | Background subgroup classification | ready |
| TSK-D18 | Quarantined task badge | edge | `suites/zz-task-runner` | none (deferred in COVERAGE-PLAN) | rendering half is browser-observable | ready |
| TSK-D19 | Agent-health fields on the wire | edge | `surface-suites` | none | guards the contract; **no UI exists** (B6) | ready (low value) |
| **TSK-R1** | **UI-created agent, real backend, e2e** | happy | `real-suites-task-agent` ×4 | `real-suites-*` cover path B only | path A + agent identity + worktree placement | ready (new tier) |
| TSK-R2 | Multi-task epic, dependency-ordered | happy | `real-suites-task-agent` (codex) | `e2e/epic_runner_codex.sh` (CLI harness, stubbed) | real backend, supervisor loop, 3 sessions, real `depends_on` | ready |
| TSK-R3 | Impossible design → no false close | edge | `real-suites-task-agent` (codex) | none | gate-oracle pass/fail; repo-local & safe | ready (advisory) |
| TSK-R4 | Auth missing → preflight class (real CLI) | edge | `real-suites-task-agent` (**claude only**) | none | real backend's own `HealthCheck` | blocked:B10 |
| TSK-R5 | Budget / watchdog timeout | edge | — | Go: `supervisor/restart*_test.go`, `health.go` tests | — | deferred (blocked:B2) |
| TSK-R6 | Podman variant | happy | — | `real-suites-podman` (path B) | — | not warranted |

**Counts (Revision 3, final)**: **23 deterministic rows — 16 ready, 7 blocked**
(D8b→B12, D9→B1, D11a→B10, D11b→B2, D11c→B10, D12→B13, D14b→B1); **5 real-backend cases — 3 ready**
(R1 across four backends, R2, R3), **1 blocked** (R4→B10, claude only), **1 deferred** (R5);
**1 declined** (R6).

Movement across revisions: Revision 1 claimed 16 ready / 4 blocked; Revision 2 claimed 18 / 4;
Revision 3 lands at **16 / 7** after demoting D11a, D12, and D14b and splitting D14. The ready count
barely moved but its *content* is now trustworthy — every remaining "ready" was checked against the
code path it asserts, and three cases that would have failed on first run (D12's idempotent claim,
D19's contradicted UI assertion, D9's absent archive log) were caught before anyone wrote YAML.

**Suite budget estimate**: `zz-task-runner` ≈ 12 tests, ~50 s added to `make test-aft` once the ready
set is in (D9 and D12 are the expensive ones and both are blocked);
`agent-lifecycle-contracts` ≈ 7 tests, ~8 s; `real-suites-task-agent` ≈ 3-5 min per backend.

**Recommended build order**
1. **The nine zero-fixture cases**: D4, D5, D6, D10, D13, D14a, D15, D8a, D19. None needs a stub run,
   a daemon, or a new seam.
2. **The seven fixture-light cases**: D1, D2, D3, D7, D16, D17, D18 — need the two-repo workspace and
   `seed-log`, both already supported.
3. **B10** (one change at `run-aft.sh:285-295`). Highest leverage of any seam: it closes the live
   gemini paid-call hazard in the *existing* deterministic tier and unlocks D11a, D11c, and R4.
4. **B1 option 2** (`STUB_CODEX_TASK_AGENT`, calling the atomic `loom data claim`). Unlocks D9 and
   D14b, and lets D9 legitimately cover claiming.
5. **The real tier** for R1 (four backends), then R2 and R3.
6. **B13, B12, B2** last — one case each (D12, D8b, D11b).

**What is genuinely ready to write today.** Sixteen deterministic cases and three real-backend cases,
and the sixteen split cleanly by cost: nine need nothing but the workspace fixture and the API
(the whole of Group A's validation surface — D4, D5, D6 — plus the idle-agent case D10, the repo-scope
case D13, the delete and lifecycle contracts D8a/D14a/D15, and the D19 wire probe), and seven more need
only the two-repo workspace and the existing `seed-log` seam (D1, D2, D3, D7, D16, D17, D18). Together
they cover the entire modal-creation surface including the worktree transaction, the delegation start
gesture, repo-scope enforcement, the archive Logs tab, quarantine rendering, and every lifecycle
endpoint contract — which is most of the template's *configuration* story and none of its *execution*
story. The execution story is the honest hole: the headline case (**TSK-D9**, a UI-created Task Runner
actually claiming and closing work) and its dependents (D12, D14b) all wait on **B1**, and the two
preflight cases wait on **B10** — a one-line harness change that should be done first regardless,
because until it lands the "deterministic, no model calls" tier can make a real paid gemini call on
any developer machine that has the CLI and a key.

**Census movement expected**: newly covered testids `create-agent-backend`, `create-agent-error`,
`create-agent-no-repos`, `assignee-dropdown-trigger`, `assignee-dropdown-menu`,
`agent-assignee-<name>`, `assignee-error`, `agent-section-background`; newly covered endpoints
`POST /agents/{name}/start|stop|restart|yield`, `PATCH /agents/{name}`, `DELETE /agents/{name}`,
`GET /agents/{name}/queue`, `GET /agents/{name}/terminal/info`, `GET /api/backends`, and
`GET /config/backend` (the read side of the pair TSK-D11a round-trips,
`internal/webui/app/routes.go:153`).

Implementation correction (verified live): direct `/issues/:id` navigation renders
`IssueDetailView` (`issue-detail-view`, `detail-run-epic-button`), while
`issue-detail-panel`, `agent-assignee-*`, `agent-status-badge`, and
`header-run-epic-button` are board-click-only surfaces.

Implementation correction (verified live): `agent-section-background` renders only
when the agent rail has both regular/interactive agents and background agents; Task
Runner tests that assert the Background subgroup must seed an interactive companion.

Implementation correction (verified live): `${var:...}` interpolation is available
only inside `api:` steps. `open:` and navigation-oriented `run:` steps must build
URLs from saved files or use board search/click navigation.
