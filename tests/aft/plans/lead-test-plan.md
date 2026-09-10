# Lead agent template — exhaustive AFT test plan

Scope: the **Lead** interactive agent template (`role_name: "lead"`), one of the five
cards in `CreateAgentModal`. Covers creation, the two lead runtimes, the terminal
surfaces that launch them, and the lead-only UI (epic assignment, delivery state,
inbox indicators).

Companion docs: `tests/aft/README.md` (tier rules, step DSL), `tests/aft/FINDINGS.md`
(§2a, §3.8, §3.9, §3.10), `tests/aft/COVERAGE-PLAN.md` (deferred terminal items).

---

## Revision 3 — final (second codex round)

Rev 2 was re-vetted. Codex conceded the aft-DSL dispute (`expect.attr`, `select`,
`routes` are implemented, not just declared — `../testing-app/src/steps.ts:237`,
`runner.ts:359`) and stood by four findings. It also caught **three errors I
introduced in rev 2**. All re-verified in code below. Where doubt remained, the case
is marked **blocked**, per the converge-don't-guess rule.

**My rev-2 errors, corrected**

- **R3-E1 — `lane-run-epic-button` *does* exist.** Rev 2 asserted it did not, based on a
  grep whose `| head -10` truncated the output before the lane files. Verified: the
  testid is on **both** lane controls — `SwimLane.tsx:303` and `ListPage.tsx:259`.
  LED-D20b now uses it; the rev-2 changelog note and B13 are corrected. (Methodology
  note for future rounds: never conclude "absent" from a `head`-truncated grep.)
- **R3-E2 — workspace deletion *does* clean up PTYs.** Rev 2 claimed only tmux sessions
  get swept. Verified full chain: `server_workspace.go:98-100` calls
  `registry.Deregister(wsID)` → `PTYHook.OnDeregister` → `multi.Deregister`
  (`hooks/pty_hook.go:59-62`) → `MultiPTYManager.Deregister` calls
  `entry.mgr.Shutdown()` (`multi_pty_manager.go:157-174`) → `PTYManager.Shutdown`
  "terminates every live session" (`pty_manager.go:482-493`). The leak window is
  **agent-delete → workspace-teardown only**, not past a successful teardown. B8 and the
  suite teardown note are narrowed accordingly.
- **R3-E3 — a failing gemini stub cannot produce `Installed=false`.** `HealthCheck` sets
  `Installed` purely from `exec.LookPath` success (`backend_gemini.go:117-121`); a stub
  that exits nonzero still resolves, and `detectBinaryVersion` failure only yields an
  empty version string (`backend_capabilities.go:101-109`). B9a's "preferred" seam was
  therefore impossible. Reworked with two seams that actually work.

**Codex round-2 findings accepted**

- **LED-D15's probe was not executable.** `/terminal/tabs` returns `Data: tabs` where
  `tabs` is a slice — `data` is a **JSON array** (`handlers/terminal/tabs.go:28-37`), so
  my `(d.get("data") or {}).get("tabs")` raises `AttributeError` on a list before
  reaching `pty_alive`. The after-delete probe also only `print`ed. Both rewritten.
- **LED-D15 poisoned LED-D17's fixture.** `suite.setup` runs once and cases run
  sequentially in file order (`../testing-app/src/runner.ts:203`, `:237`), so deleting
  `atlas-${RUN_ID}` in D15 breaks D17's `PATCH` of that same agent. D15 now creates and
  destroys its own disposable lead.
- **LED-R2 was built on a false premise.** There is no session resume: every launch calls
  `newHarnessSessionID()` (`harness_lead_runtime.go:86`, called at `:94` per invocation)
  and no `--resume` flag is passed anywhere in the lead path (grep over
  `harness_lead_runtime.go`, `harness_runtime.go`, `lead.go` finds only a comment).
  Reframed as *unique launch-pinned transcript identity*.
- **LED-R4/R5 are not runnable.** The preflight reads `${CLAUDE_CONFIG_DIR:-$HOME/.claude}`
  from the script's own env (`run-aft.sh:185-189`) and the server launch line never
  overrides it (`:285-290`) — one environment, so R4's two-value trick is impossible
  without a harness change. R5 is blocked by the same binary preflight plus B9a. The
  "5 runnable now" total was wrong.
- **LED-D13 / LED-D24 metadata halves need B5**, not merely "observation-only".
- **LED-D20c needs a real fixture:** `canRunEpic` additionally requires
  `remainingOpen > 0 && !claimedBy` (`AgentWorkPanel.tsx:513-527`), so D17's bare,
  already-claimed epic cannot satisfy it.
- **LED-D23 needs a seeded file** — shared setup makes only `--allow-empty`, and an empty
  root renders "No files found" (`FileExplorer/FileTree.tsx:659`).
- **LED-R3 must not require observing `pending`.** `epic-runner.ts` binds the parent then
  immediately calls `attemptLeadDelivery` in the same run (`:545-557`), so the pending
  window is transient.
- **LED-D8's rationale and LED-D16's body** still contradicted their own scopes; both
  finished.
- **"Only 2 of 33 overlap" was wrong** — 7 rows carry a non-`none` overlap entry.

---

## Revision 2 — codex-vetted

Revision 1 was reviewed read-only by OpenAI Codex against the same checkout. Every
finding below was re-verified against the cited code before folding in; two claims
from a parallel cross-review were **rejected** with code evidence.

**Accepted and folded in**

1. **Gemini PATH fall-through (was: "free test").** `run-aft.sh:293` *prepends*
   `e2e/stubs` — it does not replace `PATH` — and `GeminiBackend.HealthCheck` uses
   `exec.LookPath("gemini")` (`internal/cli/backends/backend_gemini.go:121`). On a host
   with a real `gemini` installed, LED-D8b takes the **controlled harness path**
   (`harness_lead_runtime.go:101`) instead of the not-installed shell path
   (`lead.go:99`) — and bills a real account. LED-D8b is now
   **blocked-on-seam** pending a failing gemini stub or a PATH guard (new **B9a**).
2. **LED-D20 conflated launch surfaces.** `AgentWorkPanel.tsx:686-692` — my rev-1
   selector — is the **wrong surface**: it queues a workflow for the *already-selected*
   lead and creates nothing (`AgentsPage.tsx:249-283`). Split into **LED-D20a/b/c** by
   surface, and the "second run on the same epic" assertion is **removed** —
   `useRunEpicWorkflow` deliberately keeps the epic in `runningEpicIds` after success so
   the control stays disabled (`useRunEpicWorkflow.ts:62-71`).
   *Corrected in rev 3:* this entry also claimed `lane-run-epic-button` does not exist.
   **That was wrong** (R3-E1) — both lane controls carry it (`SwimLane.tsx:303`,
   `ListPage.tsx:259`), and LED-D20b now uses it. The lane *label* observation still
   holds: `aria-label` interpolates `formatIssueId(id)`, not the raw id
   (`SwimLane.tsx:166-175`, `ListPage.tsx:121-129`) — which is exactly why the testid is
   the better selector.
3. **No `/roles` HTTP route.** Confirmed: `grep` over `internal/webui/` finds none;
   `handlers/agents/module.go:20-36` registers only `/interactive-prompts` and
   `/agents*`. LED-D1 and LED-D6 now read back via `loom role list --json`
   (`internal/cli/role/role_cmd.go:50`, `:120`).
4. **LED-R1/LED-R2 runtime-metadata assertions are blocked, not ready.**
   `tabmeta.TabMetadata` carries launch metadata only (`internal/webui/tabmeta/store.go:40-64`)
   and `/monitor/status` carries three derived fields (`monitor_types.go:48-52`). There
   is also **no CLI** to read agent sessions (only the driver-authed
   `agent-orchestration-session` op). Both cases are now split into a ready
   observable half and a **blocked-on-B5** metadata half.
5. **LED-D13 was shallow.** `UpdateAgent` only patches the agent record
   (`agent_service.go:531-554`), while delivery picks its provider from
   *orchestration-session* metadata (`delivery.go:83-92`, `:127`). PR-review has a
   dedicated cleanup for exactly this (`reviewer.go:521-551`); the generic lead path
   has none. LED-D13 now covers stale runtime metadata + the surviving old PTY, and
   the product gap is filed as **B12**.
6. **LED-D8 proves the launch chain, not prompt resolution.** The banner prints at
   `lead.go:106-109`, *before* prompt loading (`:118`) and runtime dispatch (`:130`).
   Retitled and re-scoped; prompt-resolution proof moved to the real tier.
7. **B6 was overstated.** aft supports `text`, `notText`, `count`, `attr`, `value`,
   `enabled`, `checked`, and `wait.fn` (`../testing-app/src/types.ts:87-114`), so
   LED-D16/D18 are writable today. B6 is reclassified **robustness seam**, not blocker.
8. **Launch-env readbacks were missing.** `LaunchSpec.Env` ships as `launch.env`
   (`tabmeta/store.go:59-64`, frontend mirror `api/terminal/terminal.ts:66-70`), so
   `LOOM_AGENT_NAME/ROLE/TERMINAL_ID/WORKSPACE/BACKEND/ORCHESTRATOR_SESSION_ID` are all
   assertable. Added to LED-D8/D9.
9. **`terminal/info` phrasing.** The endpoint is role-blind and matches on session
   names only (`agent_tmux.go:226-257`); reworded from an absolute property of the
   endpoint to a property of lead PTY naming.
10. **DeleteAgent-leaves-live-PTY promoted.** `DeleteTab` is what kills a PTY
    (`internal/webui/terminal/service_tabs.go:145-166`); `DeleteAgent`
    (`agent_service.go:591-603`) never calls it. LED-D15 is now a first-class product-risk
    case asserting `pty_alive` after delete, not a teardown note.

**Rejected (code evidence)**

- *"aft has no `expect.attr`"* — **false for this checkout.** `AttrExpectSchema` is
  defined at `../testing-app/src/types.ts:87` and wired into `ExpectSchema` at `:105`;
  `zz-agent-flow.test.yaml:224` already uses it in a passing suite. `expect.attr` is
  retained in LED-D22.
- *"`select:` / `routes:` are not aft steps"* — **false.** `select` is in `actionKeys`
  (`types.ts:156`) as a `ValuedLocatorSchema` requiring a cssable locator (`:169`, `:195`);
  test-level `routes` is at `:380` with the `{url, abort|body}` shape (`:223-231`).
  LED-D2 and LED-D7 keep their syntax.

**Narrowed**

- *"aft `api:` asserts cannot filter arrays"* — **correct**: `ApiAssertSchema` allows
  exactly one of `exists|equals|contains` on a JSON path (`types.ts:132-136`). Index
  paths like `data.0.name` parse but are order-dependent, so LED-D1's `api:` assert is
  replaced with `run:` + python filtering.
- *"no `GET /agents/{name}`"* — **correct** (`module.go:20-36`). All agent readbacks
  now explicitly list-and-filter.
- *"real-tier auth cases must not trip `run-aft.sh`'s preflight"* — correct; the
  preflight is `run-aft.sh:176-192`. LED-R4 already noted this; the mechanism is now
  spelled out.

---

## Overview

### What the Lead template is

`internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx:76-84`
renders the Lead card (testid `create-agent-template-lead`, glyph `L`, accent
`#db2777`, description "Orchestrates work interactively in a terminal.",
`aria-label="Lead, built-in interactive prompt"`). The card list itself comes from
`GET /api/workspaces/{ws}/interactive-prompts`
(`internal/webui/handlers/agents/handlers.go:34-52`,
`internal/domain/interactive_prompt.go:12`), with a hard-coded fallback at
`CreateAgentModal.tsx:33-36`.

Submit is special-cased at `CreateAgentModal.tsx:309-342`. Lead is the **only**
template that sends neither `kind` nor `prompt`/`prompt_file`:

```
interactive + LEAD  → role_name = "lead"; interactiveFields = {}
request = { name, role_name:"lead", auto:false, cross_repo, repos, ...(backend?) }
```

Server side, `agentServiceImpl.CreateAgent`
(`internal/webui/svcimpl/agent_service.go:360-387`) calls `ensureAgentRole`
(`:451-488`), which auto-creates a workspace role `lead` with
`kind: "interactive"`, description `"Lead/orchestrator interactive"`, and **empty**
`prompt`/`prompt_file`. Because the role has neither, the launch spec omits
`--prompt` (`internal/webui/handlers/terminal/agent_session.go:390-398`) and
`loom lead` resolves the embedded default prompt
`internal/cli/agent/prompts/lead.md` ("## INTERACTIVE MODE: Project Lead").
`ensureLocalAgentWorktrees` (`agent_service.go:389-396`) returns early for
interactive roles, so **a lead gets no worktree at creation time**.

`domain.ResolveRoleKind` treats `lead`/`orchestrator` as `RoleKindInteractive`
(`internal/domain/role.go:45,62`); the frontend mirror is
`internal/webui/frontend/src/utils/agentRole.ts:8-19`.

### The two launch surfaces

There are **two distinct ways** a `loom lead` process gets started from the web UI,
with different session naming and different launch-spec resolution.

**(A) Anonymous lead tab — Terminal page.**
`useTabInit` auto-creates `{ws}--lead-{defaultBackend}-1` with label
`lead-{defaultBackend}-1` when a workspace has no persisted tabs
(`internal/webui/frontend/src/components/TerminalView/tabs/useTabInit.ts:141-171`).
The `+` menu (`layout/NewTerminalTabMenu.tsx:177`, testid `new-tab-backend-{backend}`)
calls `handleBackendSelect` → `generateTabName`
(`tabs/terminalTabUtils.ts:93-123`), which increments `n` per backend.
The WS handler resolves argv through `legacyLaunchSpecForSession`
(`internal/webui/handlers/terminal/ws.go:406-412`) →
`webuterminal.ArgvForSession` (`internal/webui/terminal/session_command.go:62-88`),
producing `sh -c "'<loom>' lead --backend {backend}"`. `ValidBackends` =
`{claude, codex, opencode, gemini, cursor}` (`session_command.go:13`).
This tab is **not bound to any agent record** — `loom lead` falls back to agent id
`"lead"` (`internal/cli/agent/lead/lead.go:397-402`).

**(B) Agent-bound lead terminal — Agents page.**
`AgentDetailMain` mounts `<TerminalView hideTabs pendingAgentName={agentName}>`
(`components/AgentDetailMain/AgentDetailMain.tsx:148-158`) and `Terminal` is the
**default active tab** of `AgentEditorGroups`. `useSessionSeeding`
(`components/TerminalView/instances/useSessionSeeding.ts:124-200`) calls
`ensureAgentTerminalSession` → `POST /api/workspaces/{ws}/agents/{name}/terminal/session`
(`internal/webui/handlers/terminal/module.go:79`,
`internal/webui/handlers/terminal/agent_session.go:37-133`). The server:

- allocates `term_{uuid}` as the session name (`agent_session.go:184-193`)
- pre-creates the orchestration `AgentSession` `lead-{uuid}` with
  `Kind: orchestration`, `TerminalID: term_{uuid}`, metadata `source: "web-terminal"`
  (`agent_session.go:200-215`, `:446-464`)
- builds argv `<loom> --workspace {ws} --backend {b} lead`
  (`agent_session.go:319-337`, `:379-398`) and env `LOOM_AGENT_NAME`,
  `LOOM_AGENT_ROLE`, `LOOM_AGENT_TERMINAL_ID`, `LOOM_WORKSPACE`, `LOOM_BACKEND`,
  `LOOM_ORCHESTRATOR_SESSION_ID` (`agent_session.go:430-444`)
- persists it as tab metadata with `kind: "agent"`, `agent_id`, `role: "lead"`,
  `writable: true` (`agent_session.go:217-239`)

Backend resolution order is `agent.backend` → `role.backend` → workspace daemon
profile `AgentBackend` (`agent_session.go:361-377`).

**Consequence worth writing down:** because Terminal is the default tab,
`zz-agent-flow` case 1's `open: /ws/E2E-WS-AGENT/agents/nova` **already spawns a
`loom lead` PTY today**. It is entirely unasserted. See FINDINGS §2a for the
matching observation on `/ws/:id/terminal`.

### The two lead runtimes

`loom lead` (`internal/cli/agent/lead/lead.go:88-148`):

1. `backends.CheckBackendHealth(backendName)` — if not installed, print
   `Error: {backend} backend is not installed` and `execShell` (`lead.go:99-104`).
2. Print the banner `Starting LEAD mode (Interactive)` (`lead.go:106-109`).
3. `registerLeadOrchestratorSession` — best-effort; prints
   `Lead session: {sid} (orchestrator linkage active)` (`lead.go:302`) and starts a
   30s heartbeat (`lead.go:35`, `:407-423`).
4. `backends.RunControlledLeadRuntime`
   (`internal/cli/backends/harness_lead_runtime.go:36-70`):
   - `LOOM_LEAD_CONTROLLED` in `{0,false,no,off}` → `handled=false`, plain
     `cli.InvokeAgent` (`harness_lead_runtime.go:14-26`, `lead.go:140-142`)
   - `codex` → `leadcontrol.RunCodexLeadRuntime`
     (`internal/leadcontrol/codex_runtime.go:42-88`): spawns
     `codex app-server --listen ws://127.0.0.1:{port} -c sqlite_home=...`
     (`codex_runtime.go:121`), waits up to 60s for a websocket probe
     (`codex_runtime.go:22`, `:235-262`), then runs the remote TUI
     `codex --remote {endpoint} --no-alt-screen --dangerously-bypass-approvals-and-sandbox -C {workdir} {prompt}`
     (`codex_runtime.go:143-160`).
   - `claude|gemini|opencode|cursor` → `leadcontrol.RunHarnessLeadRuntime`
     (`internal/leadcontrol/harness_runtime.go:90-135`): harness-wrapper PTY
     supervision, the human keeps the TUI, `drainLeadMessageQueue` injects queued
     inbox messages as turns every 2s (`internal/leadcontrol/delivery.go:53`,
     `:380-404`). Per-backend launch specs at
     `harness_lead_runtime.go:91-112` — **claude pins `--session-id <uuid4>`** so
     the transcript location is knowable from boot.
5. Any error → print `Error running agent: ...` +
   `Dropping into a shell.` and `execShell` (`lead.go:143-147`).

Runtime metadata is persisted on the orchestration `AgentSession`
(`internal/leadcontrol/codex_metadata.go:15-38`,
`internal/leadcontrol/harness_metadata.go:19-31`):
`lead_runtime_provider`, `lead_runtime_controlled`, `lead_runtime_status`
(`starting|active|idle|waiting_on_user_input|waiting_on_approval|failed|disconnected`),
`codex_app_server_endpoint`, `codex_provider_thread_id`, `lead_harness_name`,
`lead_harness_session_id`, `lead_harness_chat_session_id`,
`lead_assignment_delivered_version`.

### Lead inbox / delegation flow

Enqueue → claim → inject. `leadcontrol.DeliverLeadMessageWithOptions`
(`delivery.go:162-206`) and `DeliverCurrentAssignment` (`:106-156`) write an
`AgentInboxMessage` via `agentinbox.Enqueue`, then `deliverNextLeadInboxMessage`
(`:280-321`) `ClaimNext`s it and calls the provider's `deliverTurn`.

Producers that actually exist:

| Producer | Path | Auth |
|---|---|---|
| Driver op `deliver-agent-message` | `POST /api/workspaces/{ws}/driver/{op}` (`internal/webui/handlers/driverapi/module.go:149`, `:483-505`) | requires a **running parent DriverRun** (run id + node + lease + fencing token) |
| Driver op `deliver-lead-assignment` | same route (`module.go:148`, `:440-481`) | same |
| PR-reviewer chat | `POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/messages` (`internal/webui/handlers/prreview/module.go:102`, `reviewer.go:562-607`) | reviewer agent must exist; not the Lead template |
| Outbox dispatcher | `internal/driver/outbox_dispatcher.go:55-89` | server-internal retry loop |

**There is no UI and no unauthenticated API to queue a message to a plain lead.**
The frontend has zero send/delegate controls (searched `src/` for
inbox/delegate/handoff/queue — only read-only rendering).

### Lead-only UI

| Surface | Ref | Behavior |
|---|---|---|
| Agent header meta | `AgentDetailMain.tsx:412-525` | lead + `parent` → `Assigned epic` (capital A) + epic id + delivery label; lead without `parent` → `No epic assigned`; idle status **suppressed** for leads (`:424`, `:428`) |
| Delivery label | `AgentDetailMain.tsx:596-609` | `pending→"context pending"`, `delivered→"context sent"`, `acknowledged→"lead acknowledged"` |
| Inbox label | `AgentDetailMain.tsx:416-423`, `:516-525` | `"{n} queued message(s)"` else `"{n} failed message(s)"`, `title={inbox_latest_message}` |
| Work panel | `AgentWorkPanel.tsx:99`, `:182-184`, `:403-430` | unfocused lead → mode `lead-open`; lead-only filter pills **All / Running / Not running** under `role="group" aria-label="Filter epics"`; `canRunEpic` requires a lead (`:518-524`) |
| Agent card | `AgentCard.tsx:64` | hides the "Idle" status line for leads |
| Epic claim badges | `views/ListPage.tsx:71,119` (`lane-runner-badge`, `lane-unclaimed-badge`), `SwimLaneBoard.tsx:252,532`, `table/IssueTable.tsx:228-238` | from `buildEpicLeadClaims` (`agentRole.ts:82-92`) |
| Rail ordering | `agentRole.ts:56-60` | interactive/lead ranks 0 (first) |
| Files tab | `utils/fileTreeView.ts:22` | leads get the workspace primary repo, not a worktree |

Source of those fields: `GET /api/workspaces/{ws}/monitor/status`
(`src/api/agents/agents.ts:61-79`), populated by
`internal/cli/serve/metricscmd/monitor_store_data_source.go:173-199` —
`delivery_state` (`:254-269`), `inbox_queued_count`/`inbox_failed_count`/
`inbox_latest_message` (`:223-252`), `orchestrator_session_id` (`:187`).

### A trap: lead PTY launches never produce the tmux naming shape

`GET /api/workspaces/{ws}/agents/{name}/terminal/info` is **role-blind** — it decides
purely on a session-name match, never on the agent's role
(`internal/webui/svcimpl/agent_service.go:64-81`).
`AgentTmuxManager.FindLatestAgentSession` (`internal/webui/terminal/agent_tmux.go:226-257`)
matches `^loom-{wsShort}-{role}-{agent}-{pid}$`, which is the naming **auto-mode**
(`loom task/plan --auto`) creates. Lead terminals are PTYManager sessions named
`term_{uuid}` or `{ws}--lead-{backend}-{n}`, so no product path makes a lead match.

Consequence: for a lead, `terminal/info` reports `archive` and `AgentLogsTab` never
takes the `EmbeddedTerminal` branch
(`components/AgentDetailPanel/AgentLogsTab.tsx:45-56`). This is a property of lead PTY
**naming**, not an invariant the endpoint enforces — a future lead-in-tmux launcher
would flip it without touching this endpoint.
**The `real-terminal-suites` polling pattern does not transfer to leads** — a lead's
live terminal lives on the Terminal tab (`terminal-wrapper` / `.wterm` / `.term-row`),
not the Logs tab.

### Current coverage

| Where | What | Gap |
|---|---|---|
| `tests/aft/suites/zz-agent-flow.test.yaml:25-91` (case 1) | Creates lead `nova` via the modal; rail visibility; deep link `/ws/E2E-WS-AGENT/agents/nova`; Logs tab archive-empty → `seed-log` → `AFT-LOG-MARKER`; monitor activity | No backend choice, no repo scope, no validation, no readback of `role_name`/`cross_repo`, **nothing about the terminal it silently spawns** |
| `zz-agent-flow.test.yaml:161-178` (case 3) | Reuses nova for the Diff tab via `seed-worktree` | Not lead-specific |
| `zz-agent-flow.test.yaml:181-230` (case 4) | Reuses nova for the idle-lead work panel → session detail | Touches the lead-open panel only incidentally; no filter pills, no delivery state |
| `zz-agent-flow.test.yaml:94-158` (case 2) | epic-runner via `POST /workflows/epic-runner` | Bypasses the UI "Run epic" path that **creates** a lead agent |
| `tests/aft/suites/pages.test.yaml:77-118` | `/ws/E2E-WS/terminal` resolves to *either* a tab bar *or* the no-backends state | Branching assertion; never checks the tab is named `lead-{backend}-1`, never checks the PTY connected |

**Net gap:** no test starts a lead terminal deliberately, exercises the lead-controlled
runtime, verifies the lead prompt/banner reaches the terminal, exercises
`lead-{backend}-{n}` increments, or asserts any lead-only UI (delivery state, inbox,
filter pills).

---

## Part 1 — Deterministic tier (stub AI backend)

Proposed home: **`tests/aft/suites/zz-lead-agent.test.yaml`**, own workspace
`E2E-WS-LEAD` (zz- prefix + dedicated workspace, per README.md:158-161 and
FINDINGS §3.7). Surface-tier cases go in
**`tests/aft/surface-suites/lead-contracts.test.yaml`**.

Shared suite header:

```yaml
suite: zz-lead-agent
baseUrl: "${AFT_BASE_URL:-http://127.0.0.1:3100}"
setup: >-
  d="$AFT_WORK_DIR/lead-repo";
  mkdir -p "$d"; git -C "$d" init -q;
  printf 'lead fixture\n' > "$d/README.md"; git -C "$d" add README.md;
  git -C "$d" -c user.email=e2e@x -c user.name=e2e commit -q -m init;
  curl -sf -X POST "$AFT_BASE_URL/api/workspaces"
  -H "Content-Type: application/json"
  -d "{\"name\":\"e2e-ws-lead\",\"type\":\"empty\",\"repos\":[\"$d\"]}"
  >/dev/null
# NOTE (rev 3): the repo is seeded with a real committed file, not an --allow-empty
# commit. LED-D23 asserts root file entries, and an empty tree renders "No files found"
# (FileExplorer/FileTree.tsx:659).
teardown: >-
  AFT_WS=E2E-WS-LEAD "$AFT_TESTS_DIR/scripts/close-open-issues.sh";
  for s in "$AFT_WORK_DIR"/leadSession*; do [ -f "$s" ] || continue;
  curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/terminal/tabs/$(cat "$s")" >/dev/null || true; done;
  curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD" >/dev/null || true
```

**Why teardown deletes the tabs (corrected in rev 3).** Rev 2 claimed workspace deletion
does not clean up lead PTYs. **That was wrong (R3-E2).** Verified chain: workspace delete
calls `registry.Deregister(wsID)` (`internal/webui/app/server_workspace.go:98-100`) →
`PTYHook.OnDeregister` → `multi.Deregister` (`internal/webui/hooks/pty_hook.go:59-62`) →
`MultiPTYManager.Deregister` calls `entry.mgr.Shutdown()`
(`internal/webui/terminal/multi_pty_manager.go:157-174`) → `PTYManager.Shutdown`
"terminates every live session" (`internal/webui/terminal/pty_manager.go:482-493`). So a
**successful** workspace teardown does reap the lead PTYs.

The tab-delete loop is still worth keeping, for two narrower reasons:
1. **The leak window is real but bounded** — it runs from agent-delete (which never
   touches the PTY, `agent_service.go:591-603`) to workspace teardown. Long-running
   suites keep several `loom lead` processes alive across that window; in the real tier
   those are paid backends.
2. **Teardown is best-effort** — the `curl -X DELETE .../workspaces/...` line ends in
   `|| true`, and a failed or skipped workspace delete (port conflict, harness crash,
   `--filter` run) leaves the PTYs with nothing to reap them.

So: not a hard leak past a clean run, but explicit tab deletion is the cheap belt.
LED-D15 is where this behavior is actually pinned.

### Creation variants

---

**LED-D1 — Lead template creates an interactive lead agent and its role**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator creates a Lead agent through CreateAgentModal and confirms it
persisted as an interactive `lead`-role agent with no worktree.
*Preconditions:* `E2E-WS-LEAD` exists with one repo.
*Steps:*
```yaml
- open: /ws/E2E-WS-LEAD/agents
- wait: { fn: "!!document.querySelector('[data-testid=agents-page]')" }
  intent: "Wait until the agents page is ready"
- click: { role: button, name: "+ Add agent", exact: true }
- wait: { fn: "!!document.querySelector('[data-testid=create-agent-overlay]')" }
  intent: "Wait until the agent creation dialog is open"
- click: { testid: create-agent-template-lead }
- fill: { testid: create-agent-name, value: "atlas-${RUN_ID:-local}" }
- click: { testid: create-agent-submit }
- wait:
    fn: "!document.querySelector('[data-testid=create-agent-overlay]')"
  intent: "Wait until the create-agent dialog closes on success"
- run: >-
    for i in $(seq 1 10); do curl -sf "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/agents" > "$AFT_WORK_DIR/lead-agents.json" &&
    python3 -c 'import json,sys; n=sys.argv[2]; d=json.load(open(sys.argv[1])); data=d.get("data",d); ag=data.get("agents") if isinstance(data,dict) else data; a=[x for x in ag if x.get("name")==n]; assert a, ag; a=a[0]; assert a.get("role_name")=="lead", a; assert a.get("auto") is False, a' "$AFT_WORK_DIR/lead-agents.json" "atlas-${RUN_ID:-local}" && exit 0; sleep 1; done; echo "lead agent never persisted"; exit 1
  intent: "An operator's readback pins role_name=lead and auto=false on the new agent"
- run: >-
    LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" --workspace E2E-WS-LEAD role list --json > "$AFT_WORK_DIR/lead-roles.json";
    python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); roles=d if isinstance(d,list) else d.get("roles",d.get("data",[])); r=[x for x in roles if x.get("name")=="lead"]; assert len(r)==1, roles; assert r[0].get("kind")=="interactive", r[0]; assert not (r[0].get("prompt") or "").strip(), r[0]; assert not (r[0].get("prompt_file") or "").strip(), r[0]' "$AFT_WORK_DIR/lead-roles.json"
  intent: "An operator's readback confirms the auto-created interactive lead role carries no prompt override"
```
*Assertions / readbacks:* `role_name == "lead"`, `auto == false`; exactly one `lead`
role with `kind: interactive` and **empty** `prompt`/`prompt_file` — the condition that
makes `loom lead` fall through to the embedded `prompts/lead.md`
(`agent_session.go:390-398`).
*DSL notes (rev 2):* there is **no `GET /api/workspaces/{ws}/agents/{name}`** route
(`handlers/agents/module.go:20-36`) — every agent readback must list and filter. And
there is **no `/roles` HTTP route at all**, so the role assertion goes through
`loom role list --json` (`internal/cli/role/role_cmd.go:50`, `:120`). An `api:` step
cannot do this work: `ApiAssertSchema` allows exactly one of `exists|equals|contains`
on a single JSON path (`../testing-app/src/types.ts:132-136`), with no array filtering,
and index paths like `data.0.name` are order-dependent across a growing agent list.
*Edge rationale:* pins the one payload shape that omits `kind`/`prompt_file`
(`CreateAgentModal.tsx:318-320`) — a regression there would silently create a
`prompt_file: builtin:lead` agent with different launch args.

---

**LED-D2 — Lead backend dropdown selects the runtime**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator picks a non-default AI backend for a Lead agent and the choice
reaches both the agent record and the terminal launch command.
*Preconditions:* LED-D1's workspace; `useBackends()` lists more than one option.
*Steps:* open modal → `click: { testid: create-agent-template-lead }` →
`fill: { testid: create-agent-name, value: "atlas-cl-${RUN_ID:-local}" }` →
`select: { testid: create-agent-backend, value: claude }` → submit.
*Assertions:*
- readback `agent.backend == "claude"` — via `run:` + python over
  `GET /api/workspaces/E2E-WS-LEAD/agents` filtered by name (no single-agent GET route)
- readback `POST /api/workspaces/E2E-WS-LEAD/agents/atlas-cl-.../terminal/session`
  → `data.backend == "claude"` and `data.launch.argv` joined contains
  `--backend claude` and ` lead` (`agent_session.go:379-398`)
- readback `data.launch.env.LOOM_BACKEND == "claude"` (`agent_session.go:437-439`)
*Edge rationale:* the workspace-default fallback chain
(`agent_session.go:361-377`) means an unset backend still produces a working
command; only an explicit choice proves the dropdown is wired.
*Note:* if the option list is a single entry in the stub stack, degrade this case to
asserting `create-agent-backend` exists and its value round-trips.

---

**LED-D3 — Lead repo scope: workspace-wide vs pinned**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator deselects the preselected repo so a Lead agent gets
workspace-wide scope, and separately pins one repo.
*Steps:* two sub-flows in one case, mirroring `zz-agent-flow.test.yaml:246-256`:
`click: { selector: "[data-testid='create-agent-repo-chips'] button", first: true }`
before submit for the workspace-scope variant; leave it selected for the pinned one.
*Assertions:* readback `cross_repo == true && repos == []` for the first,
`cross_repo == false && repos == ["lead-repo"]` for the second. Also
`expect: { text: "No repo selected — the agent gets workspace-wide scope." }` after
deselect (hint swap at `CreateAgentModal.tsx:368-370`).
*Edge rationale:* `crossRepo` is **derived**, not a toggle (`CreateAgentModal.tsx:220`);
a lead with no worktree makes repo scope purely a record-level property, so it is
easy to regress unnoticed.

---

**LED-D4 — Lead name validation blocks submit**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator typing an invalid Lead agent name cannot submit the form.
*Steps:* open modal, select lead card, then three probes with
`expect: { enabled: { testid: create-agent-submit, equals: false } }`:
empty name; `Atlas` (uppercase — normalized to lowercase, so this one **passes**,
use it as the positive control); `-atlas` (leading punctuation, fails);
`atlas!` (invalid char, fails).
*Assertions:* submit disabled for the two invalid probes, enabled for `Atlas`
(proving `normalizeStoredAgentName` lowercases before validating,
`src/utils/agentName.ts:5-17`).
*Edge rationale:* `canSubmit` gates on `validateStoredAgentName(name) === null`
(`CreateAgentModal.tsx:275-278`) — a lead-specific `hasPromptSelection` term is
always true for lead, so this is the only guard.

---

**LED-D5 — Duplicate lead name surfaces the server error inline**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator retrying an existing Lead agent name sees the conflict in the
dialog instead of a silent no-op.
*Preconditions:* LED-D1's `atlas-${RUN_ID}` exists.
*Steps:* open modal → lead card → same name → submit → `wait: { fn: ... }` for
`[data-testid=create-agent-error]`.
*Assertions:* `expect: { visible: { testid: create-agent-error } }`; overlay is still
open (`expect: { visible: { testid: create-agent-overlay } }`); agent count unchanged
via readback.
*Edge rationale:* the catch at `CreateAgentModal.tsx:355-362` is the only path that
renders `create-agent-error`; nothing exercises it today.

---

**LED-D6 — Second lead reuses the existing `lead` role**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator creates a second Lead agent and both share the workspace's
single interactive `lead` role.
*Steps:* create `atlas-b-${RUN_ID}` through the modal after LED-D1, then re-run
LED-D1's `loom role list --json` readback.
*Assertions:* both agents readback `role_name == "lead"` (list + filter); the role list
still has **exactly one** `lead` entry with `kind: interactive`.
*DSL note (rev 2):* the role assertion must go through
`loom role list --json` — there is no `/roles` HTTP route
(verified by `grep` over `internal/webui/`; `handlers/agents/module.go:20-36`
registers only `/interactive-prompts` and `/agents*`).
*Edge rationale:* `reconcileExistingAgentRole` (`agent_service.go:496-510`) only
rejects when the caller passes `kind: interactive` — and the lead branch passes
nothing, so it takes the `kind != interactive` early-return. That asymmetry is worth
a regression pin.

---

**LED-D7 — Lead card survives an interactive-prompts outage**
*Tier:* surface · *Status:* ready-to-write
*Intent:* An operator whose prompt catalog request fails still sees the built-in Lead
and PR Review cards plus the fallback notice.
*Steps:* per-test `routes:` stub —
`- { url: "**/interactive-prompts", abort: true }` — then open the modal.
*Assertions:* `expect: { visible: { testid: create-agent-template-lead } }`;
`expect: { visible: { testid: create-agent-template-interactive-pr-review } }`;
`expect: { text: "Prompt list unavailable; showing built-in defaults." }`.
*Edge rationale:* `DEFAULT_INTERACTIVE_PROMPTS` (`CreateAgentModal.tsx:33-36`) is
dead code in the happy path; surface tier because it needs a fabricated failure.

---

### Starting a lead terminal

---

**LED-D8 — Opening a lead agent launches `loom lead` in the embedded terminal
(launch-chain coverage)**
*Tier:* product-correctness · *Status:* ready-to-write (assertion text needs one
exploratory run to pin — see Blocker B10)
*Scope note (rev 2):* this case proves the **launch chain only** — agent record →
`ensureAgentTerminalSession` → launch spec → PTY → `loom lead` reached its banner. It
does **not** prove prompt resolution or a working runtime: the banner prints at
`lead.go:106-109`, *before* `leadStartupPrompt` (`:118`) and
`RunControlledLeadRuntime` (`:130`). Prompt-resolution and sustained-runtime proof
belong to LED-R1/LED-R2 (or to a deterministic harness variant once B1 lands).
*Intent:* An operator opens their Lead agent and the embedded terminal attaches to a
`loom lead` process launched for that specific agent.
*Preconditions:* LED-D1's lead exists; stub backends are on the **server's** PATH
(`run-aft.sh:246-259`).
*Steps:*
```yaml
- open: /ws/E2E-WS-LEAD/agents/atlas-${RUN_ID:-local}
- wait: { fn: "!!document.querySelector('[data-testid=agents-page]')" }
  intent: "Wait until the agents page is ready"
- wait: { fn: "!!document.querySelector('[data-testid=terminal-wrapper] .wterm')" }
  intent: "Wait until the lead terminal mounts the wterm grid"
- wait:
    fn: >-
      (() => { const rows = document.querySelectorAll('[data-testid=terminal-wrapper] .term-row');
      return Array.from(rows).map(r => r.textContent).join('\n').includes('Starting LEAD mode'); })()
  intent: "Wait until the lead runtime banner reaches the browser terminal"
- run: >-
    for i in $(seq 1 20); do curl -sf -X POST "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/agents/atlas-${RUN_ID:-local}/terminal/session" > "$AFT_WORK_DIR/lead-tab.json" && python3 -c 'import json,sys; n=sys.argv[3]; d=json.load(open(sys.argv[1]))["data"]; assert d["kind"]=="agent", d; assert d["role"]=="lead", d; assert d["agent_id"]==n, d; assert d["writable"] is True, d; assert d["session_name"].startswith("term_"), d; L=d["launch"]; argv=" ".join(L["argv"]); assert argv.endswith("lead"), argv; assert "--workspace E2E-WS-LEAD" in argv.replace("'"'"'",""), argv; e=L["env"]; assert e["LOOM_AGENT_NAME"]==n, e; assert e["LOOM_AGENT_ROLE"]=="lead", e; assert e["LOOM_WORKSPACE"]=="E2E-WS-LEAD", e; assert e["LOOM_AGENT_TERMINAL_ID"]==d["session_name"], e; assert e["LOOM_ORCHESTRATOR_SESSION_ID"].startswith("lead-"), e; open(sys.argv[2],"w").write(d["session_name"])' "$AFT_WORK_DIR/lead-tab.json" "$AFT_WORK_DIR/leadSession" "atlas-${RUN_ID:-local}" && exit 0; sleep 1; done; echo "lead terminal session never resolved"; exit 1
  intent: "An operator reads back the lead terminal tab and the launch contract the Terminal view opened it with"
```
*Assertions:* `terminal-wrapper` + `.wterm` mounted; `.term-row` text contains
`Starting LEAD mode` (`lead.go:107`); tab metadata `kind=agent`, `role=lead`,
`agent_id`, `writable=true`, `session_name` matches `term_*`, argv ends in `lead`.

**Launch-env readbacks (rev 2, finding 8).** `LaunchSpec.Env` is serialized as
`launch.env` (`internal/webui/tabmeta/store.go:59-64`; frontend mirror
`api/terminal/terminal.ts:66-70`), so every attribution variable is assertable:

| Env var | Expected | Why load-bearing |
|---|---|---|
| `LOOM_AGENT_NAME` | the lead's name | `resolveLeadAgentID` (`lead.go:397-402`) — without it the session registers as the generic `"lead"` and inbox delivery targets the wrong agent |
| `LOOM_AGENT_ROLE` | `lead` | `loadLeadRolePrompt` (`lead.go:160-164`) picks the role whose prompt to load |
| `LOOM_AGENT_TERMINAL_ID` | `== session_name` | recorded as the session's `TerminalID` (`lead.go:336`) |
| `LOOM_WORKSPACE` | the workspace key | store/workspace resolution |
| `LOOM_BACKEND` | the resolved backend | present only when non-empty (`agent_session.go:437-439`) |
| `LOOM_ORCHESTRATOR_SESSION_ID` | `lead-{uuid}` | descendants created from this session attribute back to it (`lead.go:28-31`, `:356-361`) |

A regression in any of these is invisible in the terminal output but silently breaks
lead attribution and inbox delivery — which is exactly why they belong here rather
than in a runtime case.
*Edge rationale (rev 3 — rescope finished):* this is the single highest-value missing
assertion, and what it proves is the chain agent record → `ensureAgentTerminalSession`
→ launch spec (argv **and** env) → PTY → `loom lead` **started**. It stops there. It
does **not** prove prompt resolution, backend dispatch, or a controlled runtime: the
banner is printed at `lead.go:106-109`, before `leadStartupPrompt` (`:118`) and before
`RunControlledLeadRuntime` (`:130`). That ordering is also precisely why the case works
with the stubs today — the banner lands before any backend is invoked.
Prompt-resolution evidence lives in LED-D1's role readback (empty
`prompt`/`prompt_file` ⇒ the embedded `prompts/lead.md` is what `loom lead` will load)
and, end to end, in the real tier.
*Caveat:* with the stub `codex`, the app-server probe fails immediately
(the stub ignores `app-server` and exits 0 → `waitForCodexAppServer` returns
"codex app-server exited before ready", `codex_runtime.go:245-250`), so the terminal
then shows `Error running agent:` + `Dropping into a shell.` and drops to a shell.
Assert only up to the banner and the metadata readbacks.

---

**LED-D8b — Lead terminal reports the missing-backend path**
*Tier:* product-correctness · *Status:* **blocked-on-seam — B9a** (was wrongly marked
"ready, free" in rev 1)
*Intent:* An operator whose Lead agent points at an uninstalled backend sees the
install guidance in the terminal rather than a blank pane.
*Why blocked (rev 2, finding 1):* rev 1 assumed "no gemini stub ⇒ gemini is always
missing". That is **unsafe**. `run-aft.sh:293` *prepends* the stub dir —
`PATH="$REPO_ROOT/e2e/stubs:$PATH"` — so the host `PATH` is still searched, and
`GeminiBackend.HealthCheck` resolves via `exec.LookPath("gemini")`
(`internal/cli/backends/backend_gemini.go:121`). On a developer machine with the real
Gemini CLI installed, `CheckBackendHealth` reports installed, the case takes the
**controlled harness path** instead (`harness_lead_runtime.go:101-103` →
`RunHarnessLeadRuntime`), the assertion fails confusingly, **and the run bills a real
account**. This host happens to have no `gemini` today, which is exactly why the
hazard would have shipped unnoticed.
*Required seam (B9a) — reworked in rev 3, because the rev-2 "preferred" option was
impossible:*

A **failing stub cannot work.** `GeminiBackend.HealthCheck` sets `Installed = true` on
`exec.LookPath("gemini")` succeeding and nothing else (`backend_gemini.go:117-121`); an
executable that exits non-zero still resolves. The only other health input,
`detectBinaryVersion`, swallows the error and returns `""`
(`backend_capabilities.go:101-109`), which does not clear `Installed`. So any stub —
failing or not — flips the case onto the *installed* path.

Workable options:
1. **Server-PATH isolation (preferred).** Give the server launch a `PATH` that does
   **not** inherit the host tail: replace `PATH="$REPO_ROOT/e2e/stubs:$PATH"`
   (`run-aft.sh:293`) with a closed set — the stub dir plus the minimum system bins the
   server needs (`/usr/bin:/bin:/usr/sbin:/sbin`, plus `node`/`git`/`python3` paths).
   Then a backend with no stub is genuinely absent, and the deterministic tier stops
   being able to reach *any* host CLI. This is the real fix and it closes the hazard for
   every suite, not just this case.
2. **Injectable health seam.** Make the binary resolver overridable (e.g. a
   `LOOM_BACKEND_BIN_PATH_OVERRIDE` or a package-level `lookPath` var mirroring
   `internal/webui/terminal/agent_tmux.go:43`'s `lookPathTmux`, which exists exactly so
   tests can force absence). Narrower, but touches product code for a test.
3. **Guard, don't test (fallback).** If neither lands, keep LED-D8b out of the suite and
   add a suite-level `run:` precondition that **fails the run** when
   `command -v gemini` resolves outside `e2e/stubs` — inverting the "resolved to a test
   stub" check at `run-aft.sh:169-175`. This buys safety without buying coverage.

Note there is no "pick an unresolvable backend name" escape: all five entries in
`ValidBackends` (`session_command.go:13`) are real CLIs someone may have installed.
*Steps (once unblocked):* create a lead with `backend: gemini`, open its detail page.
*Assertions:* `.term-row` text contains `gemini backend is not installed`
(`lead.go:100`) and `Dropping into a shell so you can fix this.` (`lead.go:101`).
*Edge rationale:* the health-check branch (`lead.go:99-104`) is the only lead failure
mode a user can actually fix. Prefer seam 1 — it makes the case genuinely
deterministic instead of host-dependent.

---

**LED-D9 — Starting a lead terminal creates its orchestration session**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator's Lead terminal session is linked to an orchestration session
so delegated work can be attributed to it.
*Preconditions:* LED-D8 has run in the same test (or repeat the `open:`).
*Steps:* after the terminal mounts, poll `GET /api/workspaces/E2E-WS-LEAD/monitor/status`.
*Assertions:*
- the lead's entry has non-empty `orchestrator_session_id` matching `^lead-`
  (`agent_session.go:210`), and the value is stable across a second
  `terminal/session` POST
- **cross-check (rev 2):** that value equals `launch.env.LOOM_ORCHESTRATOR_SESSION_ID`
  from the tab metadata (`agent_session.go:440-442`). This is the join that makes lead
  attribution work — the server-created session id and the id the child process
  inherits must be the same string, and they are produced in two different places.
*Edge rationale:* `ensureTerminalOrchestratorLink` (`agent_session.go:200-215`)
short-circuits when a session already exists — a regression there would mint a new
orchestration session per page visit and fan out inbox delivery targets.

---

**LED-D10 — Terminal page names lead tabs `lead-{backend}-{n}` and increments**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator adds a second lead terminal for the same backend and the tab
naming increments instead of colliding.
*Preconditions:* a workspace whose Terminal page resolves to the `tabs` state (see
`pages.test.yaml:91-101` for the branch detection idiom).
*Steps:*
```yaml
- open: /ws/E2E-WS-LEAD/terminal
- wait: { fn: "!!document.querySelector('[data-testid=terminal-tab-bar]')" }
  intent: "Wait until the terminal tab bar renders"
- click: { testid: terminal-new-tab-button }
- wait: { fn: "!!document.querySelector('[data-testid=new-terminal-tab-menu]')" }
  intent: "Wait until the new-session backend menu is open"
- click: { testid: new-tab-backend-codex }
- wait:
    fn: >-
      Array.from(document.querySelectorAll('[data-testid^="terminal-tab-label-"]'))
        .map(n => n.textContent.trim()).includes('lead-codex-2')
  intent: "Wait until the second codex lead tab is labelled lead-codex-2"
```
*Assertions:* first tab label `lead-codex-1`, second `lead-codex-2`
(`terminalTabUtils.ts:93-123`); session names carry the `e2e-ws-lead--` prefix —
readback `GET /api/workspaces/E2E-WS-LEAD/terminal/tabs` and assert
`session_name` matches `.*--lead-codex-2$`.
*Edge rationale:* `generateTabName` matches both prefixed and unprefixed names when
computing `max` (`terminalTabUtils.ts:104-117`); a workspace-prefix regression would
reuse `-1` and collide on the PTY key.

---

**LED-D11 — Shell lead tab is distinct from an AI lead tab**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator adding a plain shell session gets a login shell, not a lead
runtime.
*Steps:* `+` menu → `click: { testid: new-tab-backend-shell }`.
*Assertions:* tab label `lead-shell-1`; readback tab metadata; the `.term-row` text
does **not** contain `Starting LEAD mode`.
*Edge rationale:* `ArgvForSession` branches on the `lead-shell-` prefix before the
backend match (`session_command.go:69-71`); the shared `lead-` prefix makes this an
easy mis-parse.

---

**LED-D12 — Reopening a lead reuses its terminal session**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator returning to their Lead agent reattaches to the same terminal
session rather than spawning a second one.
*Steps:* POST `terminal/session` twice (readback), navigate away to
`/ws/E2E-WS-LEAD/kanban` and back to the agent detail page, POST again.
*Assertions:* all three responses share one `session_name`; `GET terminal/tabs`
contains exactly one tab with `agent_id == <lead>`
(`selectAgentTerminalTab` + `pruneStaleAgentTerminalTabs`, `agent_session.go:282-313`).
*Edge rationale:* the live-PTY short-circuit at `agent_session.go:99-112` is the hot
path for every agent-detail visit.

---

**LED-D13 — Migrating a lead's backend: new launch spec, and what does *not* get cleaned up**
*Tier:* product-correctness · *Status:* **argv/env half ready; metadata half blocked on
B5** (rev 3 — rev 2 called it "observation-only", which understated it: with no readback
surface the stale keys cannot be *seen at all* from a test, so there is nothing to
observe until B5 lands)
*Intent:* An operator who switches their Lead agent's backend gets a terminal on the
new backend, and nothing from the old runtime is left pointing at the wrong provider.
*Steps:* after LED-D12, record the current `session_name` and
`launch.env.LOOM_BACKEND`, then
`api: PATCH /api/workspaces/E2E-WS-LEAD/agents/{lead}` with `{ "backend": "claude" }`,
then POST `terminal/session` again, then `GET /terminal/tabs`.
*Assertions (ready):*
- a **new** `session_name` is returned; `launch.argv` contains `--backend claude`;
  `data.backend == "claude"`; `launch.env.LOOM_BACKEND == "claude"`
- the **old** tab: assert its observed fate. `ensureAgentTerminalSession` calls
  `pruneStaleAgentTerminalTabs`, which skips any tab with `PTYAlive`
  (`agent_session.go:299-313`) — so if the old codex PTY is still alive it is
  **not** pruned and `pty_alive: true` should still be visible on it. Pin whichever
  behavior the run shows.
*Assertions (blocked on B5 — not writable in any form today):*
- the orchestration session still carries `lead_runtime_provider: "codex"` and any
  `codex_app_server_endpoint` / `codex_provider_thread_id` from the previous runtime.
  There is no endpoint, CLI, or UI surface that prints these keys (see B5), so this half
  cannot be written — not even as a reporting-only probe. Write the argv/env half now and
  add this when B5 lands.
*Why this is the real bug surface (rev 2, finding 5):* rev 1 only checked argv, which
misses the harder failure. Generic `UpdateAgent` (`agent_service.go:531-554`) patches
the agent record and nothing else. But delivery chooses its provider strategy from
**orchestration-session metadata**, not from the agent record —
`delivererForSession` reads `MetadataRuntimeProvider` and returns the harness
deliverer for anything non-codex, the codex deliverer otherwise
(`delivery.go:83-92`), and `DeliverCurrentAssignment` gates on it at `:127-141`. So a
lead migrated codex→claude can keep a stale `lead_runtime_provider: codex` and have
messages routed at a dead app-server endpoint. PR-review is the only path that handles
this, via an explicit prefix sweep of `lead_runtime_`, `codex_`, `lead_harness_`
(`reviewer.go:521-551`) preceded by a PTY kill (`:497-519`). The generic lead path has
neither — filed as **B12**.
*Edge rationale:* `agentTerminalLaunchSpecStale` (`agent_session.go:141-161`) carries a
comment describing the exact bug it fixes; this case pins that guard **and** documents
the adjacent gap it does not cover.

---

**LED-D14 — A stopped lead keeps its terminal**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator who stops a Lead agent still gets its terminal pane, unlike a
stopped worker.
*Steps:* `api: POST /api/workspaces/E2E-WS-LEAD/agents/{lead}/stop`, reload the
agent detail page.
*Assertions:* `expect: { visible: { testid: terminal-wrapper } }`;
`expect: { notText: "This agent does not have a live terminal session." }`;
`terminal/session` still returns a non-null `launch`.
*Edge rationale:* `agentTerminalLaunchAllowed` returns true unconditionally for
interactive roles (`agent_session.go:249-257`) and
`shouldResolveLeadTerminal` keeps the pane mounted
(`AgentDetailMain.tsx:97-98`, `:138`). Contrast with the worker path is the whole
point of the branch.

---

**LED-D15 — Deleting a lead leaves its terminal PTY running (product risk)**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator deletes a Lead agent that has a running terminal; the UI stops
offering the agent, and the test records whether the `loom lead` child process and its
tab survive the delete.
*Promoted in rev 2 (finding 10):* rev 1 treated this as teardown hygiene. It is a
first-class product risk. `DeleteAgent` (`agent_service.go:591-603`) removes only the
agent assignment. The **only** thing that kills a PTY on demand is `DeleteTab`, which
calls `ptyMgr.Kill` after removing the metadata precisely so "no orphaned shell outlives
its tab" (`internal/webui/terminal/service_tabs.go:145-166`) — and `DeleteAgent` never
calls it. PR-review demonstrates the intended discipline by deleting every agent tab
before touching runtime state (`reviewer.go:497-519`).

**Rewritten in rev 3 — the rev-2 probe was not executable and poisoned a later case:**
- `/terminal/tabs` returns `Data: tabs` where `tabs` is a slice, so `data` is a **JSON
  array** (`handlers/terminal/tabs.go:28-37`). The rev-2 python called
  `.get("tabs")` on that list, raising `AttributeError` before `pty_alive` was ever
  read — the probe would have failed for the wrong reason, on both good and bad builds.
- The after-delete probe only `print`ed, so it could not fail. It caught nothing.
- Rev 2 deleted `atlas-${RUN_ID}` — the agent LED-D17 later `PATCH`es. `suite.setup` runs
  once and cases execute sequentially in file order
  (`../testing-app/src/runner.ts:203`, `:237`), so D15 would have broken D17, D19, D23.
  **D15 now creates and destroys its own disposable lead**, touching nothing shared.

*Preconditions:* none — this case is self-contained by design.
*Steps:*
```yaml
- api:
    method: POST
    path: /api/workspaces/E2E-WS-LEAD/agents
    body:
      { name: "doomed-${RUN_ID:-local}", role_name: "lead", auto: false,
        cross_repo: true, repos: [], backend: "codex" }
    status: 200
  intent: "An operator creates a disposable lead whose deletion this case will observe"
- open: /ws/E2E-WS-LEAD/agents/doomed-${RUN_ID:-local}
- wait: { fn: "!!document.querySelector('[data-testid=terminal-wrapper] .wterm')" }
  intent: "Wait until the disposable lead's terminal is attached"
- run: >-
    for i in $(seq 1 20); do curl -sf -X POST "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/agents/doomed-${RUN_ID:-local}/terminal/session" > "$AFT_WORK_DIR/doomed-tab.json" && python3 -c 'import json,sys; open(sys.argv[2],"w").write(json.load(open(sys.argv[1]))["data"]["session_name"])' "$AFT_WORK_DIR/doomed-tab.json" "$AFT_WORK_DIR/leadSession-doomed" && exit 0; sleep 1; done; echo "doomed lead session never resolved"; exit 1
  intent: "An operator records the disposable lead's terminal session name"
- run: >-
    s="$(cat "$AFT_WORK_DIR/leadSession-doomed")"; for i in $(seq 1 20); do curl -sf "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/terminal/tabs" > "$AFT_WORK_DIR/tabs-before.json" && python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); tabs=d["data"]; assert isinstance(tabs,list), type(tabs); t=[x for x in tabs if x.get("session_name")==sys.argv[2]]; assert t, [x.get("session_name") for x in tabs]; assert t[0].get("pty_alive") is True, t[0]' "$AFT_WORK_DIR/tabs-before.json" "$s" && exit 0; sleep 1; done; echo "doomed lead PTY never came alive"; exit 1
  intent: "An operator confirms the disposable lead's terminal PTY is alive before the delete"
- api:
    method: DELETE
    path: /api/workspaces/E2E-WS-LEAD/agents/doomed-${RUN_ID:-local}
    status: 200
  intent: "An operator deletes the disposable lead while its terminal is running"
- api:
    method: POST
    path: /api/workspaces/E2E-WS-LEAD/agents/doomed-${RUN_ID:-local}/terminal/session
    status: 404
    assert:
      - { path: "error", contains: "agent not found" }
  intent: "An operator confirms the deleted lead can no longer open a terminal session"
- run: >-
    s="$(cat "$AFT_WORK_DIR/leadSession-doomed")"; curl -sf "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/terminal/tabs" > "$AFT_WORK_DIR/tabs-after.json"; python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); tabs=d["data"]; assert isinstance(tabs,list), type(tabs); t=[x for x in tabs if x.get("session_name")==sys.argv[2]]; assert t, "orphan tab is gone - product behavior changed, re-pin this case"; assert t[0].get("pty_alive") is True, "orphan PTY was reaped - product behavior changed, re-pin this case"' "$AFT_WORK_DIR/tabs-after.json" "$s"
  intent: "An operator confirms the deleted lead still leaves an orphaned tab and live PTY"
- run: >-
    s="$(cat "$AFT_WORK_DIR/leadSession-doomed")"; curl -s -X DELETE "$AFT_BASE_URL/api/workspaces/E2E-WS-LEAD/terminal/tabs/$s" >/dev/null; python3 - <<'EOF'
    import json,os,subprocess,sys
    s=open(os.environ["AFT_WORK_DIR"]+"/leadSession-doomed").read().strip()
    out=subprocess.run(["curl","-sf",os.environ["AFT_BASE_URL"]+"/api/workspaces/E2E-WS-LEAD/terminal/tabs"],capture_output=True,text=True).stdout
    tabs=json.loads(out)["data"] or []
    assert not [x for x in tabs if x.get("session_name")==s], "DeleteTab did not remove the orphan"
    EOF
  intent: "An operator reclaims the orphaned terminal so the disposable lead leaks nothing"
```
*Assertions:* the PTY is alive before the delete; the agent 404s afterwards with
`agent not found` (`agent_session.go:163-175`); the tab **and** its live PTY survive the
agent delete (the leak, asserted positively so a future fix fails the test loudly and
tells the author to re-pin it); and `DeleteTab` then removes it.
*Note on asserting a leak:* pinning current-but-undesirable behavior is deliberate here.
The failure messages say "product behavior changed, re-pin this case" so that fixing
**B8** produces an actionable red test rather than a mystery. If the team would rather
not encode a known bug, invert the last three steps into `notText`-style reporting and
file B8 — but then nothing guards the regression.
*Edge rationale:* the only lead lifecycle teardown path, and the widest window for a
leaked paid-backend process in the real tier.

---

### Lead-only UI, delegation, and inbox

---

**LED-D16 — Unassigned lead reads "No epic assigned"**
*Tier:* product-correctness · *Status:* **ready-to-write** (rev 2 — B6 is a robustness
seam, not a blocker)
*Intent:* An operator viewing a fresh Lead agent sees that it has no epic and no
stale "idle" status noise.
*Steps:* open the lead detail page.
*Assertions (rev 3 — contradiction resolved; both halves are in scope):*
1. `expect: { text: "No epic assigned" }` — the lead-only branch at
   `AgentDetailMain.tsx:494-499`.
2. Idle suppression, asserted **positively** rather than by absence: a `wait.fn` that
   locates the header meta row and asserts the **first** segment is the role or epic
   text, not a status dot + `idle`. `hideIdleLeadStatus` (`AgentDetailMain.tsx:424`)
   omits the whole status segment when the lead parses as idle (`:428-447`), so its
   observable signature is *"the meta row does not begin with a status segment"* —
   checkable without a testid, because the status segment is the only one that renders a
   sibling `span[aria-hidden="true"]` dot inside the meta row.
```yaml
- wait:
    fn: >-
      (() => { const row = Array.from(document.querySelectorAll('[data-testid=agents-page] div'))
        .find(d => /No epic assigned/.test(d.textContent || '') && d.children.length > 0 && d.children.length < 12);
        if (!row) return false;
        const first = row.children[0];
        return !(first && first.tagName === 'SPAN' && first.getAttribute('aria-hidden') === 'true'); })()
  intent: "Wait until the idle lead header omits its status segment entirely"
```
*Rev 2 said* this half could be dropped to a component test, which contradicted the case
name and the coverage table. It is not droppable: `hideIdleLeadStatus` is a lead-only
product branch, it is observable as above, and B6's `agent-header-status` testid would
reduce the whole `wait.fn` to
`expect: { count: { testid: agent-header-status, equals: 0 } }` — a robustness win, not
a precondition.
*Edge rationale:* `hideIdleLeadStatus` (`AgentDetailMain.tsx:424`) is lead-only and
purely cosmetic-looking, but it is what stops a lead from permanently reading "idle".

---

**LED-D17 — Assigning an epic flips the lead to "Assigned epic · context pending"**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator assigning an epic to their Lead agent sees the assignment and
its undelivered state on the agent header.
*Preconditions:* an epic exists in `E2E-WS-LEAD`.
*Steps:*
```yaml
- api:
    method: POST
    path: /api/workspaces/E2E-WS-LEAD/issues
    body: { title: "lead epic $RUN_ID", issue_type: epic, priority: 2 }
    save: [{ from: "data.id", as: "leadEpicId" }]
  intent: "An orchestration client seeds the epic this lead will own"
- api:
    method: PATCH
    path: /api/workspaces/E2E-WS-LEAD/agents/atlas-${RUN_ID:-local}
    body: { parent: "${var:leadEpicId}" }
  intent: "An orchestration client assigns the epic to the lead agent"
- open: /ws/E2E-WS-LEAD/agents/atlas-${RUN_ID:-local}
- wait: { fn: "document.body.textContent.includes('Assigned epic')" }
  intent: "Wait until the lead header reports its assigned epic"
- expect: { text: "context pending" }
```
*Assertions:* header shows `Assigned epic`, the epic id, and `context pending`;
readback `GET /monitor/status` → `delivery_state == "pending"`.
*Edge rationale:* `monitorLeadDeliveryState`
(`monitor_store_data_source.go:254-269`) returns `""` for non-leads and for leads
with no `parent`, so the pill is a genuine lead-only surface. `pending` is reachable
without any live runtime because the session metadata version never matches.

---

**LED-D18 — Lead work panel offers the epic filter pills; a worker does not**
*Tier:* product-correctness · *Status:* **ready-to-write** (rev 2 — the
`role`/`aria-label` selectors are stable; B6 would only make it tidier)
*Intent:* An operator on a Lead agent gets the open-epic queue with its Running /
Not-running filters, while a task agent gets task-status filters instead.
*Steps:* open the lead detail page, then a task agent's detail page.
*Assertions:*
`expect: { count: { selector: "[role=group][aria-label='Filter epics']", equals: 1 } }`
on the lead; `equals: 0` on the worker, where
`[role=group][aria-label='Filter tasks by status']` is `1` instead
(`AgentWorkPanel.tsx:403-430`).
*Edge rationale:* `mode === "lead-open"` (`AgentWorkPanel.tsx:182-184`) is the widest
lead-only rendering branch in the app and is completely untested.

---

**LED-D19 — Lead claim badge appears on the epic's lane**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* A reviewer scanning the board sees which lead is running each epic.
*Preconditions:* LED-D17 assigned `leadEpicId` to the lead.
*Steps:* `open: /ws/E2E-WS-LEAD/list` (or `/kanban` for the swim-lane variant).
*Assertions:* `expect: { visible: { testid: lane-runner-badge } }` and its text
contains the lead name; an unclaimed epic shows `lane-unclaimed-badge`.
*Selector note (corrected in rev 3):* these are real testids
(`views/ListPage.tsx:71,119`, `SwimLane.tsx:252,532`) — **and so is the lane run
button** (`lane-run-epic-button` at `SwimLane.tsx:303`, `ListPage.tsx:259`). Rev 2 said
the run control had none; that was wrong (R3-E1). All three lane controls are
testid-addressable.
*Edge rationale:* `buildEpicLeadClaims` (`agentRole.ts:82-92`) drives four separate
surfaces (`ListPage`, `SwimLaneBoard`, `SwimLane`, `IssueTable`) from one map.

---

**LED-D20a/b/c — The "Run epic" controls (split by launch surface)**

Rev 1 had a single LED-D20 whose selector pointed at the wrong behavior. Two rounds of
verification produced this:

- `AgentWorkPanel.tsx:686-692` (`aria-label="Run epic {group.epicId}"`) is a
  **different behavior**: `AgentsPage.handleRunEpic` (`views/AgentsPage.tsx:249-283`)
  calls `startWorkflowRun` with `leadName: agentName` — the *already-selected* lead.
  It creates no agent. (Confirmed both rounds.)
- The lane run controls **do** carry `data-testid="lane-run-epic-button"`
  (`SwimLane.tsx:303`, `views/ListPage.tsx:259`). Rev 2 claimed otherwise from a
  `head`-truncated grep; corrected in rev 3 (R3-E1). Their `aria-label` does interpolate
  `formatIssueId(issue.id)` — the *display* id, not the raw id
  (`SwimLane.tsx:166-175`, `views/ListPage.tsx:121-129`) — so a test that saved `data.id`
  from an `api:` step and clicked `Run epic ${var:id}` would miss. **Use the testid.**

The surfaces therefore split three ways:

---

**LED-D20a — Issue-detail "Run epic" creates `lead-{epic-slug}`**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator starting an epic run from the issue header gets a purpose-named
Lead agent bound to that epic.
*Preconditions:* an epic with no claiming lead; the issue detail panel open on it.
*Steps:* open the epic's detail panel → `click: { testid: header-run-epic-button }`
(`components/IssueDetailPanel/header/IssueHeader.tsx:150-160` — the one stable testid
among the run controls) → poll the agents list.
*Assertions:* an agent named `lead-{epicLeadNameSlug(epicId)}` exists with
`role_name == "lead"`, `auto == false`
(`hooks/workspace/startEpicRunnerForIssue.ts:114-121`); a success toast names the lead
and the run id (`useRunEpicWorkflow.ts:60-63`).
*Note:* `IssueDetailPanel` wires this to `handleRunEpicWorkflow`
(`IssueDetailPanel.tsx:1353-1354`) and carries its **own inline copy** of
`epicLeadNameSlug`/`nextEpicLeadName` (`IssueDetailPanel.tsx:308-336`) rather than
importing the shared helper — see the rationale below.

---

**LED-D20b — Board/list lane "Run epic" creates the same lead**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator starting the run from a board swim lane or the list view gets
the same purpose-named Lead agent.
*Steps (rev 3 — use the testid):* `open: /ws/E2E-WS-LEAD/list` (or `/kanban` for the
swim-lane variant) → `click: { testid: lane-run-epic-button, first: true }`.
Prefer `/list`: it renders one lane per epic, so scoping is simple; on `/kanban` there is
one control per swim lane, so scope with a `selector` that also matches the lane's own
`aria-label` if the workspace has more than one epic.
*Do not* select by `aria-label="Run epic ${var:epicId}"` — the label carries
`formatIssueId(id)`, not the raw id (`SwimLane.tsx:166-175`, `ListPage.tsx:121-129`).
*Preconditions:* an epic with at least one open child and **no** claiming lead — the same
`showRunEpic` gate the lanes apply. Reuse LED-D20c's fixture epic (see below); do not
reuse LED-D17's, which is already claimed.
*Assertions:* same as LED-D20a.
*Edge rationale:* two independent call sites (`SwimLane.tsx:291-306`,
`views/ListPage.tsx:247-262`) share one hook and one testid but render their own labels;
this case is what keeps the shared testid honest across both.

---

**LED-D20c — Agents-page "Run" reuses the selected lead and creates nothing**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator running an epic from their Lead agent's open queue reuses that
lead instead of minting a second one.
*Fixture (added in rev 3 — the case was infeasible without it).* `canRunEpic` gates on
**five** conditions, not just "is a lead" (`AgentWorkPanel.tsx:513-527`):
`mode === "lead-open"` (or the focused-epic variant), `selectedAgentIsLead`,
`group.epicId !== ORPHAN_EPIC_KEY`, `onRunEpic != null`, **`remainingOpen > 0`**, and
**`!claimedBy`**. So the epic must have at least one open child task
(`remainingOpen = totalCount - doneCount`) and must not be claimed by any lead —
`buildEpicLeadClaims` marks an epic claimed as soon as *some* lead has
`parent == epicId` (`utils/agentRole.ts:82-92`). LED-D17's epic fails **both**: it is bare
(no children) and it is claimed by the lead D17 assigned. Seed a dedicated one:
```yaml
- api:
    method: POST
    path: /api/workspaces/E2E-WS-LEAD/issues
    body: { title: "runnable epic $RUN_ID", issue_type: epic, priority: 2 }
    save: [{ from: "data.id", as: "runnableEpicId" }]
  intent: "An orchestration client seeds an unclaimed epic for the work-panel Run control"
- api:
    method: POST
    path: /api/workspaces/E2E-WS-LEAD/issues
    body: { title: "runnable child $RUN_ID", issue_type: task, priority: 2,
            parent: "${var:runnableEpicId}" }
  intent: "An orchestration client gives the epic one open child so it is runnable"
```
This same fixture serves LED-D20b. Keep it out of LED-D17's epic so the two cases cannot
interfere — setup runs once and cases are sequential
(`../testing-app/src/runner.ts:203`, `:237`).
*Steps:* select a lead in the rail → in the work panel,
`click: { role: button, name: "Run epic ${var:runnableEpicId}" }` (raw id here —
`AgentWorkPanel` uses `group.epicId` unformatted, `AgentWorkPanel.tsx:691`, unlike the
lanes) → poll.
*Assertions:* the **agent count is unchanged**; a run is queued with
`leadName == <the selected lead>` (toast text
`Epic runner queued for {agentName}: {run_id}`, `AgentsPage.tsx:271-273`); the
`canRunEpic` control is absent when a **non-lead** agent is selected
(`AgentWorkPanel.tsx:518-524` requires `selectedAgentIsLead`).
*Edge rationale:* this is the case most likely to be mis-specified — it looks identical
to D20a/b in the UI and does the opposite thing.

---

**Removed from rev 1:** the assertion that "a second run on the same epic yields
`...-2`". `useRunEpicWorkflow` **deliberately** leaves the epic id in `runningEpicIds`
after a successful start, with a comment explaining that the lead has no `parent` yet
so the claim-derived badge has not flipped, and that keeping the button disabled is
what prevents a duplicate run (`useRunEpicWorkflow.ts:62-71`). The lane surfaces then
swap the button for the claim badge (`SwimLane.tsx:294`+, `ListPage.tsx:250`+). To
cover the collision suffix, **pre-create an agent literally named
`lead-{epicLeadNameSlug(epicId)}`** via `POST /agents`, then run the epic once and
assert the new lead is `...-2` (`startEpicRunnerForIssue.ts:33-46`).

*Shared edge rationale:* these are the **second, third, and fourth** production paths
that create or bind a Lead agent, and the create payload is duplicated across
`startEpicRunnerForIssue.ts:114-121`, `IssueDetailPanel.tsx:308-336`, and
`views/IssueDetailPage.tsx:140-184`. Nothing pins them together today, and
`zz-agent-flow` case 2 starts the workflow straight through the API, skipping lead
creation entirely.

---

**LED-D21 — Leads sort ahead of workers in the rail**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator with a mixed fleet finds their Lead agents first in the rail.
*Preconditions:* one lead + one task agent in the workspace.
*Steps:* open `/ws/E2E-WS-LEAD/agents`, read the rail's rendered order via `wait.fn`.
*Assertions:* the lead's index in the rail is lower than the worker's; the worker
falls under the Background grouping and the lead does not
(`agentRole.ts:38-60`).
*Edge rationale:* `agentRailRank` uses `isInteractiveAgent`, which prefers
`role_kind` over the role name — a `role_kind` regression on the lead role would
silently demote leads.

---

**LED-D22 — Queued inbox messages surface on the lead header**
*Tier:* product-correctness · *Status:* **blocked-on-seam** (see Blocker B3)
*Intent:* An operator sees how many messages are waiting for their Lead agent to
pick up.
*Preconditions:* a `seed-inbox` runtime seam that enqueues an `AgentInboxMessage`.
*Steps (once unblocked):*
```yaml
- run: >-
    LOOM_TESTSUPPORT=1 LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" daemon seed-inbox
    --workspace E2E-WS-LEAD --agent "atlas-${RUN_ID:-local}" --body "AFT-INBOX-MARKER ${RUN_ID:-local}"
  intent: "The local runtime queues one inbox message for the lead"
- open: /ws/E2E-WS-LEAD/agents/atlas-${RUN_ID:-local}
- wait: { fn: "document.body.textContent.includes('1 queued message')" }
  intent: "Wait until the lead header reports its queued inbox message"
```
*Assertions:* header text `1 queued message` (singular, `AgentDetailMain.tsx:419-420`);
`title` attribute equals the message body (`:520`); readback `/monitor/status` →
`inbox_queued_count == 1`, `inbox_latest_message` matches the marker.
*Edge rationale:* the singular/plural branch and the `queued > 0 ? … : failed > 0 ? …`
precedence (`AgentDetailMain.tsx:418-423`) are the only inbox rendering in the app.

---

**LED-D23 — Lead Files tab shows the workspace repo, not a worktree**
*Tier:* product-correctness · *Status:* ready-to-write
*Intent:* An operator browsing files from a Lead agent sees the workspace's primary
repo, because leads never get a worktree.
*Fixture (added in rev 3 — the case could not pass as written).* The shared `setup:` makes
only `git commit --allow-empty`, so the repo tree is **empty** and `FileTree` renders
`No files found` (`components/FileExplorer/FileTree.tsx:659`, pinned by
`FileTree.test.tsx:130`) — there would be no root entries to assert. Amend the suite
setup to write and commit one ordinary file:
```
d="$AFT_WORK_DIR/lead-repo"; mkdir -p "$d"; git -C "$d" init -q;
printf 'lead fixture\n' > "$d/README.md"; git -C "$d" add README.md;
git -C "$d" -c user.email=e2e@x -c user.name=e2e commit -q -m init
```
(That replaces the `--allow-empty` commit; nothing else in the plan depends on the tree
being empty.)
*Steps:* open the lead detail page, click the `Files` tab.
*Assertions:* the tree renders the seeded root entry (`README.md`) and **not**
`No files found`; no `worktrees/<repo>/<lead>` path segment appears. Contrast readback:
`GET /agents/{lead}/git/diff-stat` returns 404 `agent worktree "…" not found`
(`agent_service.go:52-62`, `:135-147`) while a seeded worker's does not.
*Edge rationale:* `utils/fileTreeView.ts:22` special-cases leads; the 404 readback
pins `ensureLocalAgentWorktrees`'s interactive early-return
(`agent_service.go:394-396`). Note `zz-agent-flow` case 3 papers over this by using
`seed-worktree` to *create* nova's worktree.

---

**LED-D24 — `LOOM_LEAD_CONTROLLED=0` falls back to a plain interactive launch**
*Tier:* **needs-new-seam** · *Status:* blocked on **B7 and B5** (rev 3: the proposed
"assert **no** `lead_runtime_*` metadata is written" half needs the same absent readback
surface as LED-R1/R2 — B7 alone does not unblock it)
*Intent:* An operator running with lead control disabled still gets a working
terminal, without queued-message delivery.
*Why blocked:* `leadControlDisabled()` reads the env of the `loom lead` **child**
process (`harness_lead_runtime.go:19-26`), which inherits from `loom serve`. Nothing
in the request path can set it per-tab. Observable difference is also thin: with the
stub, both paths end in an error + shell.
*Recommendation:* run the existing suite a second time under a stack started with
`LOOM_LEAD_CONTROLLED=0` (a `make test-aft-uncontrolled` variant) rather than adding
a per-session override; assert only that the terminal still reaches
`Starting LEAD mode` and that **no** `lead_runtime_*` metadata is written.

---

## Part 2 — Real-backend tier (non-deterministic)

Proposed home: **`tests/aft/real-terminal-suites/`** — it is already the "live backend
process behind a browser terminal" tier. Caveat: `make test-aft-terminal`
(`Makefile:386-388`) hard-requires `tmux` on PATH, which a **lead does not need**
(lead terminals are PTYManager sessions). Either relax that guard or add
`make test-aft-lead` pointing `AFT_SUITES` at a new `tests/aft/real-lead-suites/`.
Gating otherwise reuses `AFT_REAL_BACKEND` exactly as
`run-aft.sh:113-205` implements it (real binary must not resolve into `e2e/stubs*`;
per-backend auth preflight; `AFT_TIMEOUT=600000`).

Teardown must reuse `scripts/close-open-issues.sh` plus
`scripts/real-backend-teardown.sh <backend>`, and — **mandatory here, not optional** —
`DELETE /api/workspaces/{ws}/terminal/tabs/{session}` for the lead's `term_*` tab.
`DeleteTab` is the only path that kills the PTY
(`internal/webui/terminal/service_tabs.go:145-166`; the reviewer module relies on the
same call at `prreview/reviewer.go:509`). Deleting the agent or the workspace does
**not** do it (see **B8**), so a real-tier run that skips this leaves a live paid-backend
process attached to a PTY after the harness exits.

---

> **Rev 2 reclassification (finding 4).** Every runtime-metadata assertion below is
> **blocked on B5**, not ready. Verified: `tabmeta.TabMetadata` exposes launch metadata
> only — there is no session-metadata field (`internal/webui/tabmeta/store.go:40-64`);
> `/monitor/status` exposes exactly three derived lead fields, `delivery_state`,
> `inbox_*`, and `orchestrator_session_id` (`monitor_types.go:48-52`,
> `monitor_store_data_source.go:173-199`); and there is **no CLI** that prints agent
> sessions — the only reader is the driver-authed `agent-orchestration-session` op
> (`internal/cli/driver/agent_cmd.go:72`), which returns just the session **id**.
> Each case below is therefore split into a **ready** observable half (terminal text +
> the three monitor fields) and a **blocked** metadata half. Reading fleet-db directly
> would unblock it, but `run-aft.sh` exports no fleet-db base URL to aft
> (`scripts/start-e2e-server.sh` exports only `LOOM_ISSUE_BACKEND`/`LOOM_FLEET_DB_ACTOR`),
> so that is its own plumbing task — prefer B5.

**LED-R1 — Real codex lead renders its TUI in the browser terminal**
*Backend:* codex · *Status:* observable half **ready**; metadata half **blocked on B5**
*Intent:* An operator opens their Lead agent and talks to a real
Codex session running under the controlled app-server runtime.
*Steps:* create a lead via `POST /agents` with `backend: codex` → open
`/ws/{ws}/agents/{lead}` → wait for `[data-testid=terminal-wrapper] .wterm` →
chained bounded `run:` polls (≤120s each, per
`zz-real-terminal-logs.test.yaml:50-57`) on `/monitor/status` until the lead's
`orchestrator_session_id` is set → wait for non-empty `.term-row` text.
*Assertions (ready):* `.term-row` joined text is non-empty, contains
`Launching controlled Codex lead session...` (`codex_runtime.go:144`), and does **not**
contain `Error running agent`; `/monitor/status` reports a non-empty
`orchestrator_session_id`. Reaching the "Launching controlled…" line is itself
meaningful: it prints only *after* `waitForCodexAppServer` succeeded
(`codex_runtime.go:64-78` → `:143-144`), so it proves the app-server came up — which is
exactly what the stub cannot do.
*Assertions (blocked on B5):* `lead_runtime_provider == "codex"`,
`lead_runtime_controlled == "true"`, `codex_app_server_endpoint` matching
`^ws://127\.0\.0\.1:\d+$`, `lead_runtime_status` in
`{starting, active, idle, waiting_on_user_input}`.
*Failure surface to capture:* dump `~/Library/Caches/loom/codex-leads/**/app-server.log`
on failure — `codexAppServerTimeoutError` already tails it into the error text
(`codex_runtime.go:264-275`).

---

**LED-R2 — Real claude lead pins a *unique* transcript identity at every launch**
*Backend:* claude · *Status:* **blocked on B5** — unlike LED-R1 there is no observable
half. The entire assertion is `lead_harness_session_id`, which has no readback surface
today; nothing in the browser terminal reveals the pinned UUID (the harness prints its
own TUI, and `--session-id` is passed as an argv flag the user never sees).

**Reframed in rev 3 — the rev-1/rev-2 premise was false.** Both earlier revisions framed
this as "a stable transcript identity **across** a terminal restart, so the conversation
can be resumed". There is no such thing in this code path. `harnessLeadInvocation` calls
`newHarnessSessionID()` on **every** invocation (`harness_lead_runtime.go:86` defines it
as `uuid.NewString`; `:94` calls it per launch), and no `--resume` flag is passed anywhere
in the lead launch path — a grep across `harness_lead_runtime.go`,
`leadcontrol/harness_runtime.go`, and `cli/agent/lead/lead.go` finds only a *comment*
mentioning "the resume UUID is persisted" (`harness_runtime.go:151`). Persisting the id
is what makes a transcript **locatable after the fact**; it is not a resume mechanism.
*Intent (corrected):* Each launch of an operator's Claude Lead session is pinned to its
own known transcript id from boot, so a past session's transcript can be located without
scraping the TUI.
*Steps:* create a lead with `backend: claude` → open its detail page → read
`lead_harness_session_id` → `DELETE /terminal/tabs/{session}` (kills the PTY) → reload so
a new terminal session is created → read the new `lead_harness_session_id`.
*Assertions:*
- the first id is a UUIDv4 and is present **before** any TUI output — the pin happens at
  launch (`harness_lead_runtime.go:94-100`) and is persisted with the *starting* metadata
  (`harness_runtime.go:98-110`), which is the whole point of the `harnessSessionID` field
  (`harness_lead_runtime.go:74-81`)
- `lead_harness_name == "claude-code"` (`harness_runtime.go:73-84`)
- `lead_harness_started_at` parses as RFC3339Nano and precedes the transcript's mtime —
  the reconciliation the comment at `harness_metadata.go:24-30` describes
- after relaunch the id is a **different** UUID, and **both** ids resolve to distinct
  transcripts on disk. That is the correct expectation: two launches, two transcripts.
*Rationale:* the launch-time pin is what the code comments call out as load-bearing
(`harness_lead_runtime.go:74-81`) — without it, the runtime must scrape the id off the
TUI. A stub that exits immediately cannot exercise it.

---

**LED-R3 — An epic assignment injected mid-session becomes a turn**
*Backend:* codex first, then claude · *Intent:* An operator watching their live Lead
session sees Loom's backend epic assignment arrive as a new turn without touching the
keyboard.
*Steps:* start the real lead terminal (LED-R1/R2) and leave it open → `api: POST
/api/workspaces/{ws}/workflows/epic-runner` with `{ epicId, leadName: <our lead> }`
→ the workflow's `deliver-lead-assignment` op runs against a real DriverRun
(`driverapi/module.go:440-481`) → poll.
*Assertions (ready — terminal + header observables):*
- `.term-row` text grows to include `Loom assigned this lead session an epic through
  backend state.` (`delivery.go:523`)
- `/monitor/status` → `delivery_state` **settles on** `delivered`, and the header pill
  reads `context sent` (`AgentDetailMain.tsx:596-609`)
*Assertions (blocked on B5 / no inbox readback):*
- session metadata gains `lead_assignment_delivered_version` (`codex_metadata.go:24`,
  written by `MarkAssignmentDelivered`)
- the claimed inbox message completes with outcome `delivered` (`delivery.go:353-374`) —
  there is no HTTP or CLI surface that lists `AgentInboxMessage` rows
*Do not assert the `pending` state (rev 3).* The workflow binds the parent and
**immediately** attempts delivery inside the same step —
`loom.agents.updateParent(...)` is followed directly by `attemptLeadDelivery(loom, leadName)`
(`internal/workflows/builtin/epic-runner.ts:545-557`) — so `pending` may never be visible
to a 5s-polling UI. Poll for the terminal-state `delivered` only; a `pending → delivered`
transition assertion is inherently racy.
*Why real-only:* delivery requires a runtime whose status is not
`starting`/`disconnected` (`delivery.go:225-242`,
`harness_delivery.go:176-184`), i.e. a process that stays alive. No stub does.

---

**LED-R4 — Real lead with missing auth surfaces the backend's own error**
*Backend:* codex and claude · *Status:* **blocked on a harness change (B14)** — both
backends, not just codex (corrected in rev 3)
*Intent:* An operator whose backend credentials are missing sees the backend's login
prompt in the terminal instead of an empty pane.
*Why blocked:* rev 2 proposed giving the preflight one `CLAUDE_CONFIG_DIR` and the server
another. **That is not possible.** `run-aft.sh` and the server share one environment: the
preflight reads `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/.credentials.json` from the script's
own env (`run-aft.sh:185-189`), and the server launch line passes an explicit env list
that does **not** override `CLAUDE_CONFIG_DIR` (`:285-290`) — so the server inherits
whatever the preflight validated. Codex is worse still: its check is hard-coded to
`$HOME/.codex/auth.json` with no env indirection at all (`:179-183`).
*Required seam (B14):* an `AFT_SKIP_AUTH_PREFLIGHT=1` escape hatch around
`run-aft.sh:176-192`, plus a way to point the server at an alternate credential home
(`CODEX_HOME` / `CLAUDE_CONFIG_DIR` added to the server launch env list). Then the case
becomes writable for both backends.
*Steps (once unblocked):* start the stack with the preflight skipped and the server's
credential home pointed at an empty dir → open the lead terminal.
*Assertions:* `.term-row` text contains the backend's login guidance; for codex the
app-server exits and the terminal reaches
`Error running agent: codex app-server exited before ready`
then `Dropping into a shell.` (`lead.go:143-147`).
*Note:* keep this in its own suite file so a credential-less CI run can execute it
independently of the normal real tier.

---

**LED-R5 — Real lead with the backend binary removed at launch**
*Backend:* any · *Status:* **blocked — same root cause as LED-D8b (B9a), plus the
real-tier binary preflight** (corrected in rev 3; rev 2 called it ready)
*Intent:* An operator whose backend CLI is gone gets install guidance and a usable shell,
not a dead terminal.
*Why blocked:* two independent gates. (1) The real tier refuses to start unless the
selected backend's binary resolves and is **not** a stub
(`run-aft.sh:164-175`) — so the very condition under test is what the harness rejects.
(2) For any *other* backend the server's `PATH` still carries the host tail
(`run-aft.sh:293`), so "absent" is not guaranteed — the same hazard as B9a. Unblocking
needs server-PATH isolation (B9a option 1) and/or the B14 preflight escape hatch.
*Steps (once unblocked):* create a lead pinned to a backend absent from the server's
`PATH` → open its detail page.
*Assertions:* identical to LED-D8b (`{backend} backend is not installed`,
`Dropping into a shell so you can fix this.`).
*Placement note:* this duplicates LED-D8b. Fix B9a once and the **deterministic** case
covers it; LED-R5 is then optional.

---

**LED-R6 — Harness-path smoke for the non-claude harness backends**
*Backends:* opencode, cursor (gemini if an account exists) · *Intent:* An operator on
a non-flagship backend still gets a controlled Lead session.
*Assertions (thin, one per backend):* terminal reaches
`Launching controlled {backend} lead session...`
(`harness_runtime.go:115`); `lead_runtime_provider == {backend}`;
`lead_harness_name == "generic"` for opencode/cursor and `"gemini"` for gemini
(`harness_runtime.go:73-84`).

### Per-backend matrix recommendation

| Backend | Real lead tier? | Rationale |
|---|---|---|
| **codex** | **Yes — first class** | Only backend with the app-server runtime (`codex_runtime.go`); ~50% of the lead-runtime code is codex-only; already the default `AFT_REAL_BACKEND` |
| **claude** | **Yes — second** | Only backend that pins `--session-id`; exercises the whole harness-wrapper path that gemini/opencode/cursor share |
| opencode | Thin smoke only (LED-R6) | Shares the harness path with claude; `run-aft.sh:189-191` notes it has binary-only health checking, so failures are ambiguous |
| cursor | Thin smoke only (LED-R6) | Same; also the `cursor` vs `cursor-agent` binary split (`harness_lead_runtime.go:106-108`) is worth one assertion |
| gemini | **Skip until a stub exists (B9/B9a)** | No `e2e/stubs/gemini`, no `AFT_REAL_BACKEND=gemini` branch in `run-aft.sh:113-160`, no auth preflight. Worse, the stub dir is only *prepended* to `PATH` (`run-aft.sh:293`) while `HealthCheck` uses `exec.LookPath` (`backend_gemini.go:121`), so a host-installed gemini can be invoked from the **deterministic** tier. Adding the stub is a prerequisite, not a test |

---

## Part 3 — Blockers & new seams needed

**B1 — The claude stub cannot hold an interactive lead session.**
`e2e/stubs/claude:58-75`: with `--dangerously-skip-permissions` **and** a positional
prompt (which `harnessLeadArgs` always appends, `harness_runtime.go:210-216`), the
stub prints one answer and exits. Only the *no-prompt* branch enters the
`while IFS= read -r _line` loop that keeps the PTY alive.
*Seam:* make the prompt branch fall through into the same read loop after emitting its
first answer, or gate it on `STUB_CLAUDE_LEAD=1`. The turn markers the harness needs
(`❯`, `claude --resume <uuid>`, `✻ Baked for Ns`) are already there.
*Unlocks:* a deterministic LED-D8 variant on the **harness** path with a live runtime;
deterministic `lead_runtime_status` transitions; a deterministic version of LED-R3.

**B2 — The codex stub has no `app-server` mode.**
`e2e/stubs/codex:125-150` routes `app-server --listen …` into the plain interactive
branch, so it prints and exits; `waitForCodexAppServer` fails immediately
(`codex_runtime.go:245-250`). Implementing the app-server JSON-RPC/websocket protocol
in shell is not realistic.
*Recommendation:* accept it. Make **claude** the deterministic lead backend once B1
lands, keep codex leads to the real tier, and assert only the pre-backend banner in
LED-D8. Do **not** invest in a fake app-server.

**B3 — No way to enqueue an inbox message for a plain lead.**
Every producer requires either a verified DriverRun (driver ops) or a PR-review agent.
*Seam:* `loom daemon seed-inbox --workspace --agent --body [--source-kind] [--session]`,
implemented next to `seed-log`/`seed-worktree` under `internal/cli/daemon/`, gated by
`requireTestSupport()` (`internal/cli/daemon/seed_gate.go:13-18`) and `Hidden: true`,
calling `agentinbox.Enqueue` (`internal/agentinbox/message.go:25-41`) — the same
composition `leadcontrol` uses, per ADR-0001's "seed through the runtime's own
composition" rule.
*Unlocks:* LED-D22; deterministic `inbox_failed_count` rendering; a deterministic
`delivery_state` regression net.

**B4 — `seed-session` (already named in FINDINGS §3.10).**
A command that fabricates an orchestration `AgentSession` with arbitrary metadata
would let a deterministic test drive every `lead_runtime_status` value and every
`delivery_state` transition (`monitor_store_data_source.go:254-269`) without a live
runtime. Suggested shape:
`loom daemon seed-session --workspace --agent --kind orchestration --meta k=v`.
*Unlocks:* `context sent` / `lead acknowledged` pills; the `unsupported`/`pending`
delivery reasons in `delivery.go:225-242`.

**B5 — No readback surface for agent session metadata. [raised to hard blocker in rev 2]**
`lead_runtime_*` / `lead_harness_*` / `codex_*` keys live only on the orchestration
`AgentSession` in fleet-db, and **nothing reads them out**:
- `/terminal/tabs` carries launch metadata only — `tabmeta.TabMetadata` has no session
  field (`internal/webui/tabmeta/store.go:40-64`)
- `/monitor/status` carries three derived fields: `delivery_state`, `inbox_*`,
  `orchestrator_session_id` (`monitor_types.go:48-52`,
  `monitor_store_data_source.go:173-199`)
- the CLI has no `session show`; the only reader is the driver-authed
  `agent-orchestration-session` op, which returns the session **id** and nothing else
  (`internal/cli/driver/agent_cmd.go:72`, `driverapi/module.go:383-402`)
- fleet-db is not reachable from a test: `run-aft.sh` exports no fleet-db base URL and
  `start-e2e-server.sh` exports only `LOOM_ISSUE_BACKEND=fleetdb` /
  `LOOM_FLEET_DB_ACTOR`

*Blocks:* LED-R2 entirely; the metadata halves of LED-R1, LED-R6, LED-D13.
*Seam:* `GET /api/workspaces/{ws}/agents/{name}/sessions` returning orchestration
sessions with their metadata map — mirrors `tasks/{id}/sessions`, which
`zz-agent-flow` case 4 already leans on. A `lead_runtime` sub-object on the
`/monitor/status` agent entry is the cheaper alternative but leaks a lead-specific
shape into a general endpoint.

**B6 — Missing testids on every lead-only UI element. [reclassified in rev 2:
robustness seam, not a blocker]**
Rev 1 marked LED-D16 and LED-D18 blocked on this. That was wrong: aft's `ExpectSchema`
supports `url`, `text`, `notText`, `title`, `visible`, `count`, `attr`, `value`,
`enabled`, `checked` (`../testing-app/src/types.ts:96-114`), plus arbitrary
`wait: { fn: ... }`. Both cases are writable today — the testids would only make them
less brittle. The real setup blocker in this area is **B3** (inbox count), which no
selector can work around.
All of these are bare `<span>`s inside `AgentDetailMain`'s meta row
(`AgentDetailMain.tsx:426-525`) and are only reachable by whole-body text matching,
which is fragile in a page that also renders epic ids and agent names:

| Suggested testid | Element | Ref |
|---|---|---|
| `agent-header-epic` | "Assigned epic" / "No epic assigned" segment | `AgentDetailMain.tsx:471-499` |
| `agent-header-delivery-state` | delivery pill | `:488-493` |
| `agent-header-inbox` | inbox count segment | `:516-525` |
| `agent-header-status` | status dot + label | `:428-447` |
| `lead-filter-{all\|running\|idle}` | work-panel filter pills | `AgentWorkPanel.tsx:403-430` |
| `agent-tab-{terminal\|info\|git\|logs\|diff\|files}` | agent editor tab buttons | `views/AgentEditorGroups.tsx:16-40` |

Same class of gap as FINDINGS §3.9 (DiffTab/DiffFileRow). Until they land, LED-D16 /
LED-D18 must scope through `role`/`aria-label` selectors, which exist only for the
filter pills.

**B7 — `LOOM_LEAD_CONTROLLED` is server-process env.**
See LED-D24. No per-request seam exists and adding one would be test-only surface in a
security-adjacent path. Prefer a second stack invocation.

**B8 — Agent delete never kills the terminal PTY. [product leak, promoted to a
first-class case in rev 2 — LED-D15]**
`DeleteAgent` (`agent_service.go:591-603`) removes the agent record only. The `term_*`
tab, its orchestration session, and the `loom lead` child process all survive the delete.
The only code that kills a PTY **on demand** is `DeleteTab`, which calls `ptyMgr.Kill`
immediately after the metadata delete with the comment "so no orphaned shell outlives its
tab" (`internal/webui/terminal/service_tabs.go:145-166`). `prreview` shows the intended
discipline by deleting every agent tab *before* touching runtime state
(`reviewer.go:497-519`). Note that `KillWorkspaceSessions` (`agent_tmux.go:368-433`) is
**not** the relevant sweep — it covers tmux sessions, and lead terminals are PTYManager
sessions; the PTYManager cleanup runs through the deregistration chain below.
*Corrected in rev 3 (R3-E2):* rev 2 also claimed workspace deletion fails to clean these
up. It does clean them up — `registry.Deregister` → `PTYHook.OnDeregister` →
`MultiPTYManager.Deregister` → `PTYManager.Shutdown`
(`app/server_workspace.go:98-100`, `hooks/pty_hook.go:59-62`,
`terminal/multi_pty_manager.go:157-174`, `terminal/pty_manager.go:482-493`). The leak is
therefore **bounded**: agent-delete → workspace-teardown, plus any run where teardown
does not complete.
*Consequences:* (a) LED-D15 asserts this as current product behavior so a fix surfaces as
an actionable red test; (b) lead suites should still delete their `term_*` tabs in
`teardown:` to close the window and to survive a skipped teardown; (c) the fix pairs
naturally with **B12** — both want `stopAndClearReviewerRuntime`'s two steps generalized.

**B9 — No `gemini` stub.**
`e2e/stubs/` has `claude`, `codex`, `cursor-agent`, `opencode` only. Add a gemini stub
before writing any *positive* gemini lead case.

**B9a — Gemini can fall through to the host CLI. [new in rev 2 — supersedes rev 1's
"free test" framing of LED-D8b]**
`run-aft.sh:293` prepends rather than replaces: `PATH="$REPO_ROOT/e2e/stubs:$PATH"`.
`GeminiBackend.HealthCheck` resolves with `exec.LookPath("gemini")`
(`internal/cli/backends/backend_gemini.go:117-121`) and `detectBinaryVersion("gemini")`.
On any host with the real Gemini CLI installed, a lead pinned to `gemini`:
1. reports **installed**, skipping the `lead.go:99-104` shell drop LED-D8b asserts;
2. enters the controlled harness runtime (`harness_lead_runtime.go:101-103`);
3. **spends a real account's quota inside the deterministic tier**, which is the exact
   thing the stub farm exists to prevent (`run-aft.sh:246-252`).
The same fall-through applies to *any* backend name without a stub — gemini is just the
only one of the five `ValidBackends` that has none.
*Seam:* see the three options under LED-D8b. **Prefer server-PATH isolation** — a
failing stub does not work, because `Installed` derives from `exec.LookPath` alone
(`backend_gemini.go:117-121`) and `detectBinaryVersion` failure only empties the version
string (`backend_capabilities.go:101-109`). Rev 2 recommended the failing stub; that was
wrong (R3-E3).

**B10 — Exact terminal text needs one exploratory run.**
The `.term-row` assertions in LED-D8 / LED-D8b are derived from source
(`lead.go:107`, `:100`) but PTY line-wrapping at 80 cols and the wterm grid's
row-splitting can break a literal match. Run
`AFT_SUITES=tests/aft/suites/zz-lead-agent.test.yaml make test-aft AFT_ARGS="--screenshots"`
once and pin the observed substrings — prefer short, wrap-safe fragments
(`Starting LEAD mode`, `not installed`) over full lines.

**B11 — No `ws:` step in the aft DSL** (COVERAGE-PLAN.md:121-126).
Not a blocker for this plan: the browser is the actor for every lead terminal case, so
the WebSocket is exercised through `terminal-wrapper` rather than directly.

**B12 — Backend migration leaves stale lead runtime metadata (product gap, new in rev 2).**
Generic `UpdateAgent` patches the agent record only (`agent_service.go:531-554`). But
delivery selects its provider strategy from the **orchestration session's**
`lead_runtime_provider` — `delivererForSession` returns the harness deliverer for any
non-codex provider and the codex deliverer otherwise (`delivery.go:83-92`), and
`DeliverCurrentAssignment` gates on `hasRuntimeMetadata`/`unsupportedReason` at
`:127-141`. So a lead migrated codex→claude can retain `lead_runtime_provider: codex`
plus a dead `codex_app_server_endpoint`, and messages get routed at the old runtime.
PR-review is the only code path that handles this: it kills the tab's PTY, then sweeps
metadata keys prefixed `lead_runtime_`, `codex_`, `lead_harness_`
(`reviewer.go:497-551`), with a comment stating why order matters. The generic lead
path has neither step.
*Not a test seam — a product fix.* LED-D13 documents it; the fix is to lift
`stopAndClearReviewerRuntime`'s two steps into the agent-update path (or into
`ensureAgentTerminalSession` when it detects a stale spec) so every lead benefits.

**B14 — `run-aft.sh` gives the preflight and the server one shared environment (new in
rev 3).** The credential preflight reads `${CLAUDE_CONFIG_DIR:-$HOME/.claude}` and
`$HOME/.codex/auth.json` from the script's own env (`run-aft.sh:176-192`); the server
launch passes an explicit env list that overrides neither (`:285-290`). There is
therefore no way for a test to present valid credentials to the preflight and absent
credentials to the server. Likewise the binary preflight (`:164-175`) refuses to start
when the selected real backend is missing or resolves to a stub.
*Seam:* `AFT_SKIP_AUTH_PREFLIGHT=1` / `AFT_SKIP_BIN_PREFLIGHT=1` guards, plus
`CODEX_HOME` and `CLAUDE_CONFIG_DIR` added to the server launch env list so they can be
pointed at an empty home independently.
*Blocks:* LED-R4 (both backends), LED-R5 (with B9a).

**B13 — aft DSL facts confirmed in rev 2, re-confirmed in rev 3** (recorded so later
plans don't re-litigate; codex conceded this dispute, citing the implementations at
`../testing-app/src/steps.ts:237` and `runner.ts:359`):
- `expect.attr` **exists** — `AttrExpectSchema` at `../testing-app/src/types.ts:87`,
  wired at `:105`; already used by `zz-agent-flow.test.yaml:224`.
- `select:` **exists** as an action key (`types.ts:156`, `:169`) and requires a
  selector/testid locator without `first`/`last`/`nth` (`:195-199`).
- test-level `routes:` **exists** (`types.ts:380`) with the shape
  `{ url, abort: true }` xor `{ url, body }` (`:223-231`).
- `api.assert` takes **exactly one** of `exists|equals|contains` per JSON path
  (`:132-136`) — no array filtering, so multi-field checks over a list must use
  `run:` + python.
- `/api/workspaces/{ws}/terminal/tabs` returns `data` as a **JSON array**, not an object
  with a `tabs` key (`handlers/terminal/tabs.go:28-37`) — rev 2's LED-D15 probe got this
  wrong and would have crashed. Index it as a list.
- there is no `fill.valueFile` and no file-sourced `api.body` — `api.body` is an object
  or a string (`:148`).

---

## Coverage table

Legend — **Tier**: PC = product-correctness (`suites/`), SF = surface
(`surface-suites/`), RT = real tier, NS = needs-new-seam.
**Overlap**: what `zz-agent-flow` / `pages.test.yaml` already covers.

| ID | Case | Kind | Tier | Status | Existing overlap |
|---|---|---|---|---|---|
| LED-D1 | Lead template creates an interactive lead + role | happy | PC | ready | zz-agent-flow c1 covers the clicks; **no readback** of `role_name`/`auto`/role kind |
| LED-D2 | Backend dropdown reaches record + launch argv + env | happy | PC | ready | none |
| LED-D3 | Repo scope: workspace-wide vs pinned | edge | PC | ready | zz-agent-flow c5 does this for **task**, not lead |
| LED-D4 | Name validation disables submit | edge | PC | ready | none |
| LED-D5 | Duplicate name renders `create-agent-error` | edge | PC | ready | none |
| LED-D6 | Second lead reuses the `lead` role | edge | PC | ready *(via `loom role list --json`)* | none |
| LED-D7 | Lead card survives prompts-endpoint outage | edge | SF | ready | none |
| LED-D8 | Opening a lead launches `loom lead` (launch chain + launch env) | happy | PC | ready (pin text, B10) | **spawned but unasserted** by zz-agent-flow c1 |
| LED-D8b | Missing-backend guidance in the terminal | edge | PC | **blocked — B9a** *(needs server-PATH isolation; a failing stub cannot work)* | none |
| LED-D9 | Terminal start creates the orchestration session (+ env join) | happy | PC | ready | none |
| LED-D10 | `lead-{backend}-{n}` naming increments | happy | PC | ready | pages.test.yaml asserts only "some tab has a label" |
| LED-D11 | `lead-shell-N` is a shell, not a lead runtime | edge | PC | ready | none |
| LED-D12 | Reopening a lead reuses its session | edge | PC | ready | none |
| LED-D13 | Backend migration: new spec + stale runtime metadata | edge | PC | **partial** — argv/env half ready; metadata half **blocked — B5** *(gap = B12)* | none |
| LED-D14 | Stopped lead keeps its terminal | edge | PC | ready | none |
| LED-D15 | Deleting a lead leaves its PTY running (product risk) | edge | PC | ready *(rev 3: probe rewritten for the array shape; own disposable lead)* | none |
| LED-D16 | "No epic assigned" + idle suppression | happy | PC | ready *(rev 3: both halves in scope)* | none |
| LED-D17 | Assigned epic → `context pending` | happy | PC | ready | none |
| LED-D18 | Lead-only epic filter pills | happy | PC | ready *(aria selectors are stable)* | zz-agent-flow c4 uses the panel, asserts nothing lead-specific |
| LED-D19 | Lead claim badge on the epic lane | happy | PC | ready | none |
| LED-D20a | Issue-detail `header-run-epic-button` creates `lead-{slug}` | happy | PC | ready | zz-agent-flow c2 starts the workflow via API, **skips lead creation** |
| LED-D20b | Board/list lane run control creates the same lead | happy | PC | ready *(rev 3: use `lane-run-epic-button`; needs D20c's fixture)* | none |
| LED-D20c | Agents-page work-panel "Run" reuses the selected lead | edge | PC | ready *(rev 3: needs an unclaimed epic with ≥1 open child)* | none |
| LED-D21 | Leads sort first in the rail | edge | PC | ready | none |
| LED-D22 | Queued inbox count on the header | happy | PC | **blocked — B3** | none |
| LED-D23 | Lead Files tab uses the workspace repo (no worktree) | edge | PC | ready *(rev 3: setup must commit a real file)* | zz-agent-flow c3 *creates* a worktree for nova, masking this |
| LED-D24 | `LOOM_LEAD_CONTROLLED=0` fallback | edge | NS | **blocked — B7 + B5** | none |
| LED-R1 | Real codex lead renders its TUI | happy | RT | **partial** — observable half ready; metadata half **blocked — B5** | none |
| LED-R2 | Real claude lead: unique launch-pinned transcript id | happy | RT | **blocked — B5** *(rev 3: reframed — there is no session resume)* | none |
| LED-R3 | Assignment injected mid-session becomes a turn | happy | RT | **partial** — terminal + header observables ready; metadata/inbox **blocked — B5**; must not assert transient `pending` | none |
| LED-R4 | Missing auth surfaces the backend error | edge | RT | **blocked — B14** *(both backends; rev 2 wrongly called claude ready)* | none |
| LED-R5 | Backend binary missing at launch | edge | RT | **blocked — B9a + B14** *(rev 2 wrongly called it ready)* | none |
| LED-R6 | Harness smoke: opencode / cursor | happy | RT | **partial** — observable half ready; provider metadata **blocked — B5** | none |

**Totals (rev 3, final).** **27 deterministic** cases: **23 fully ready**, **1 partial**
(LED-D13 — write the argv/env half now, add the metadata half with B5), **3 blocked**
(LED-D8b/B9a, LED-D22/B3, LED-D24/B7+B5). **6 real-tier** cases: **0 fully ready**,
**3 partial** (LED-R1, R3, R6 — each has a runnable observable half), **3 blocked**
(LED-R2/B5, LED-R4/B14, LED-R5/B9a+B14). **33 cases total.**

Rev 2 → rev 3 movement, all downward on the real tier: LED-R4 and LED-R5 lost their
"ready" labels (shared preflight environment, B14), LED-R1/R3/R6 became explicitly
partial rather than "ready with a blocked half", LED-R2 was reframed around a corrected
premise, and LED-D13/D24 gained their B5 dependencies. Rev 2's "5 real-tier cases
runnable now" was wrong; the honest number is **3 half-cases**.

**Overlap with existing coverage: 7 of 33 rows**, not 2 as rev 2 claimed —
LED-D1, LED-D3, LED-D8, LED-D10, LED-D18, LED-D20a, LED-D23. The most important is
LED-D8: `zz-agent-flow.test.yaml:47` opens `/ws/E2E-WS-AGENT/agents/nova`, which lands on
the default Terminal tab and therefore **already launches a lead PTY on every run** — so
LED-D8 is not new behavior coverage, it is the missing *assertion* over behavior the
suite already triggers blind. All 7 overlaps are partial in that same sense: the existing
suite exercises the surface without pinning it.

### Suggested write order

1. LED-D8, LED-D9 — highest value, no seams; proves the launch chain **and** the
   launch-env attribution contract.
2. LED-D1, LED-D2, LED-D3, LED-D5, LED-D6 — creation contract, cheap, same suite.
3. LED-D16, LED-D18 — pure UI, no seams.
4. LED-D17, then the D20c/D20b fixture epic (unclaimed, ≥1 open child), then LED-D20a,
   LED-D20b, LED-D20c, LED-D19.
5. LED-D10 – LED-D14 — terminal session mechanics.
6. LED-D15 — write it after D14 but keep its fixture disposable; ordering matters
   because setup runs once and cases are sequential.
7. LED-D21, LED-D23 (amend the suite setup to commit a real file first).
8. LED-D13's argv/env half.
9. **B9a** (server-PATH isolation), then LED-D8b — and LED-R5 becomes redundant.
10. **B1** (stub TUI holds a session), then a harness-path LED-D8 variant.
11. **B3** (`seed-inbox`), then LED-D22.
12. **B5** (agent-sessions readback) — the single highest-leverage seam: it completes
    LED-D13, LED-D24, LED-R1, LED-R2, LED-R3, and LED-R6. Then the real tier.
13. **B14** (preflight escape hatches), then LED-R4.

Product fixes surfaced by this plan, worth filing independently of any test:
**B12** (stale lead runtime metadata survives a backend migration, so delivery can be
routed at a dead runtime) and **B8** (agent delete does not kill the terminal PTY;
bounded by workspace teardown, but real while it lasts).

**Seam leverage, by cases unblocked:** B5 → 6 · B9a → 2 (D8b, R5) · B14 → 2 (R4, R5) ·
B3 → 1 · B7 → 1 · B1 → 0 blocked cases but converts LED-D8 from launch-chain-only into
real deterministic runtime coverage, which is the biggest qualitative gain available.

Implementation correction (verified live): `launch.argv` is serialized through
`ShellArgvForCommand`, so the stored shell command has single-quoted tokens such as
`'--backend' 'claude'` and ends with `'lead'`; assertions must parse or match the
quoted joined string.

Implementation correction (verified live): LED-D10/LED-D11 terminal session names
embed the uppercased workspace key, so session-name assertions should be
case-insensitive or suffix-oriented (for example `.*--lead-codex-2$`).

Implementation correction (verified live): `${var:...}` interpolation is available
only inside `api:` steps. `open:` and navigation-oriented `run:` steps must build
URLs from saved files or use visible UI navigation.
