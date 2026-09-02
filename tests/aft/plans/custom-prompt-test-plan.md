# aft test plan — CreateAgentModal "Custom prompt" interactive agent

Scope: one agent template — the **Custom prompt** card in `CreateAgentModal`. Everything
below is derived from source read during this pass; every claim carries a `file:line`.
Companion docs: `tests/aft/README.md` (tiers + how to run), `tests/aft/FINDINGS.md`
(bug/seam tracker), `CONTEXT.md` (actor fidelity, readback, surface-wiring vocabulary).

---

## Revision 3 — final, second-round vetted

Revision 2 went through a second independent read-only pass. Three of revision 2's disputes
were conceded by the reviewer (CUS-D14's narrowing, the `AgentDetailMain` citations,
`expect.attr`), and **one new claim of mine was refuted** — see "Retry semantics, corrected"
below. Thirteen further findings were raised; all were verified in source and all thirteen were
accepted. What changed:

**Retry semantics, corrected (revision 2 was wrong).** Revision 2 claimed `Max: 3` meant three
invocations and that emitting `{"status":"completed"}` was what stopped retries. Both halves are
wrong:
- `RetryPolicy.Max` is documented as "the maximum number of retries **after the first
  attempt**. Max=3 means up to 4 total attempts" (`internal/harness/retry.go:26-28`), and the
  loop retries while `attempt < p.Max` (`:100-113`).
- The wrapper classifies **any successful exit as `StatusIdle`** — `if state.Success() { return
  StatusIdle, 0, "", "" }` (`harness-wrapper@v0.5.3/pkg/wrapper/pty.go:56-58`) — and
  `shouldRetry` returns false for `StatusIdle` (it falls through the switch,
  `retry.go:134-146`). Stdout content plays no part in the decision.

  **So a stub that exits 0 is never retried at all.** `exit 0` is the mechanism, not the JSON.
  CUS-D14's note is rewritten accordingly: the JSON line is kept only because it is what the
  real stub emits (`e2e/stubs/codex:140-146`) and keeps the wrapper's stream parser happy, and
  the multi-record handling is kept as defence against a *non-zero* exit, with a per-invocation
  delimiter so the NUL stream stays parseable.

**Accepted — two structural infeasibilities (both HIGH)**
- **CUS-D14 was in the wrong suite.** It sat in the surface suite but depended on CUS-D5's agent
  and fixture from `zz-custom-prompt`. aft runs each suite's `teardown:` in a `finally` after
  all of that suite's tests, before moving to the next path
  (`testing-app/src/runner.ts:255-272`), so the workspace is already deleted. Worse, D5 never
  created the fixture file D14 reads. **CUS-D14 moves into `zz-custom-prompt` and owns its own
  fixture**, with explicit fixture ownership recorded per case.
- **CUS-D19 could not precede CUS-S3 in one workspace.** D19 creates the `pr-review` role;
  deleting its agent leaves the role (`agent_service.go:594-604`); S3's *first* creation — a
  custom agent named `pr-review` with a prompt — then hits
  `reconcileExistingAgentRole`'s prompt-mismatch branch (`:503-508`) and 400s where the case
  expects 201. **Split into two workspaces**: D19 in the clean-control workspace, S3 in its own
  throwaway poison workspace. The "in this workspace" precondition is gone.

**Accepted — CUS-D8d is deterministic, not a probe**
Only `plan`, `task`, `lead` are seeded (`workspace_store.go:478-506`); `orchestrator` is not.
So `ensureAgentRole` takes the not-found branch, `ResolveRoleKind` resolves interactive
(`domain/role.go:46-56`), and the role is **created → 201**. Pinned to 201 with concrete
assertions, replacing revision 2's "expected outcome unpinned" language. It also exposes a
sharper finding: the created role gets the description `"Lead/orchestrator interactive"`
(`agent_service.go:465-468`), so a custom-prompt agent silently claims the legacy interactive
role name.

**Accepted — mechanics corrections**
- **CUS-D4 trailing newline.** A quoted heredoc appends a final `\n`; `$(cat file)` strips
  trailing newlines; the server also `TrimSpace`s. Byte-equality against the raw file was
  therefore impossible. The fixture is now generated **without** a terminal newline.
- **CUS-D11 worktree path.** Canonical is
  `filepath.Join(workspacePath, "worktrees", repoName, agentName)`
  (`internal/localworkspace/localworkspace.go:40-42`) — *not* revision 2's
  `<workspace>/<repo>-<agent>`, which would have passed even if a real worktree existed.
- **Launch-metadata readbacks must poll.** `ensureAgentTerminalSession` is fired from a
  `useEffect` and consumed in a `.then()` (`useSessionSeeding.ts:123-149`), so a mounted
  terminal does not imply persisted tab metadata. D10/D11/D17/D19 now **poll `/terminal/tabs`
  for the exact `agent_id`**; surface cases POST ensure-session directly.
- **CUS-D12 selector.** `[data-testid=agent-editor-groups] button` also matches the
  "Split editor right" control (`AgentEditorGroups.tsx:213-222`), which renders when
  `groupIndex === 0 && !isSplit && tabs.length >= 2`. Now excluded via
  `button:not([data-testid=agent-editor-split])`.
- **CUS-D15 was vacuous.** `document.body.textContent.includes('cust-')` is already true from
  earlier cases. Now asserts `AgentCard`'s exact `aria-label="Agent: <new-name>"`
  (`AgentCard.tsx:77`).
- **CUS-S1 needs a fresh name** for the whitespace probe: reusing the first name passes role
  reconciliation (`TrimSpace("   ") == ""` skips the mismatch guard) and then 409s at
  `Agents().Create`, not 201.
- **`role_name` is not generally normalized server-side.**
  `normalizeFirstClassAgentRole` (`agent_service.go:516-523`) lowercases only to *test* the
  value and returns `roleName` **unchanged** in the `default` branch — only `lead`/
  `orchestrator` are canonicalized. Only `in.Name` gets `normalizeStoredAgentName` (`:356`).
  The UI is what normalizes custom role names, because it assigns `roleName = trimmedName`
  (`CreateAgentModal.tsx:293,313`). Overview and CUS-D17 corrected, and CUS-D17 gains an
  API-level sub-case: a mixed-case `role_name` reaches fleet-db, whose role-name pattern is
  lowercase-only (`fleet-db internal/models/role.go:12`), so it 400s.
- **CUS-D14 must unset `LOOM_AGENT_EFFORT` and `LOOM_CLAUDE_EFFORT`.**
  `appendCodexEffortArgs` **prepends** `-c model_reasoning_effort="…"`
  (`backend_effort.go:15-22`), so with either set, argv does not begin with `exec`.
- **CUS-D5 is the controlled-runtime pilot.** The deterministic Codex stub now implements
  the narrow app-server bootstrap needed for readiness, holds its remote TUI on stdin, and
  connects that remote process over WebSocket (**S-1/S-7**). The process reports a measured
  prompt-prefix fingerprint and safety-block count; the case requires those values, the
  controlled-runtime banner, an agent-specific connection marker, and no visible start error.
  A same-origin terminal-tabs readback also requires exactly one owned Codex tab with
  `pty_alive=true`. This proves bootstrap and argv fidelity only; it does not claim thread
  discovery or turns.

**Accepted — the real tier was over-blocked**
`run-aft.sh` assigns `AFT_SUITES` with `:=` under `AFT_REAL_BACKEND` and then runs exactly that
path (`run-aft.sh` real-tier block and `AFT_SUITE_PATHS`), so
`AFT_REAL_BACKEND=codex AFT_SUITES=tests/aft/real-interactive-suites-codex tests/aft/run-aft.sh`
works **today**. R1/R2/R4a/R4b are reclassified **ready-to-write (opt-in tier)**; the dedicated
directory plus `make test-aft-real-interactive` is productization, not a seam. Recorded as
**S-8** (productization) rather than a blocker.

**Citations refreshed:** ephemeral gate `AgentDetailMain.tsx:183-185` (was :180-182); custom
card `testId` use `CreateAgentModal.tsx:492` (was :485); modal catch
`CreateAgentModal.tsx:355` (was :344-352).

---

## Revision 2 — codex-vetted

Revision 1 was reviewed by an independent read-only pass (OpenAI Codex) plus four other
reviews. Every finding was re-verified against source before being folded in. What changed:

**Accepted — aft runner mechanics revision 1 assumed but does not have**
- `fill` passes its `value` as a single `agent-browser` argv element via `execFile`
  (`testing-app/src/steps.ts:222-225`, `src/browser.ts:35-43`); there is **no `valueFile`**.
  CUS-D4 / CUS-D7a now drive the textarea with `run:` +
  `agent-browser --session "$AFT_SESSION" find testid … fill "$(cat <fixture>)"` — the
  `$AFT_SESSION` pattern already used by `zz-agent-flow`, and confirmed exported to `run:`
  steps at `testing-app/src/steps.ts:168-181`. One fixture file is the single source of truth
  for both the fill and the comparison.
- `api.body` is object-or-string only, with no `bodyFile`
  (`testing-app/src/types.ts:144-151`, `src/api-step.ts:107-115`). CUS-D7b's 100 KB bodies
  now go through `run:` + `curl --data-binary @file`.
- **`api:` assertions cannot filter arrays.** `lookup` walks `path.split('.')` with
  `hasOwnProperty` (`testing-app/src/api-step.ts:70-78`), so array access is positional only
  (`data.sessions.0.backend`). Every "assert an entry with name == X" readback in CUS-D1 /
  CUS-D10 / CUS-D11 / CUS-D16 is now `run:` + `python3` over a saved response. This compounds
  with the fact that **there is no `GET /api/workspaces/{ws}/agents/{name}` route at all** —
  only list, PATCH, DELETE, and `/queue` (`internal/webui/handlers/agents/module.go:25-29`) —
  so single-agent readbacks are unavoidably list-and-filter.

**Accepted — a wrong runtime claim in CUS-D14**
- Revision 1's probe set `LOOM_LEAD_CONTROLLED=0` **and** `</dev/null`. With non-TTY stdin,
  `defaultCodexInvoker` short-circuits to the non-interactive path
  (`internal/cli/backends/backend_codex.go:63-79`), so the asserted `--no-alt-screen` flag
  (interactive-only, `:41-44`) never appears. Redesigned honestly in CUS-D14: it now asserts
  the **non-interactive** argv shape. The CUS-D5 pilot now covers the controlled Codex path's
  positional-prompt append (`codex_runtime.go:152`) through the remote process's measured
  prompt fingerprint; a focused Go test separately pins the full argv log. The harness-wrapper
  path (`harness_runtime.go:210-216`) remains outside the pilot. The fallback
  half remains useful: `buildCodexNonInteractiveArgs`
  (`backend_codex.go:108-116`) still passes the prompt as the final positional argument, with
  an explicit comment saying so — so DB → `LOOM_AGENT_ROLE` → argv is still provable.

**Accepted — a wrong claim of my own about cwd**
- P-4 / CUS-R1 said an empty `launch.cwd` means the PTY inherits `loom serve`'s cwd. Wrong:
  `spawnSession` sets `cmd.Dir = m.cwd` and overrides it only for a non-empty `launch.Cwd`
  (`internal/webui/terminal/pty_manager.go:349-367`), and `m.cwd` is the **registered
  workspace path** passed to `NewPTYManager` (`multi_pty_manager.go:293`). The two coincide in
  the aft stack only because `start-e2e-server.sh` `cd`s into `tmp/e2e-workspace` before
  launching `loom serve` and registers that same directory. Corrected in both places.

**Accepted — determinism and honesty**
- The deterministic Codex stub now has a deliberately narrow `app-server` implementation and a
  remote client that completes a real WebSocket initialize handshake. CUS-D5 may therefore
  assert a connected controlled Codex process in the mounted terminal and its prompt argv. It still
  cannot claim thread/turn or model behavior. The remaining boundary is recorded as **S-7**.
- CUS-D6 is reclassified from a product-correctness case to an explicitly-labelled
  **negative-rendering regression fence**, and is no longer counted as sanitizer coverage.
- CUS-D11's "no worktree exists under the workspace" is narrowed to the agent-scoped path;
  a workspace-wide glob can be contaminated by other suites' agents.
- CUS-R4a could never have run: `run-aft.sh` hard-exits when the tier's credentials are
  missing (`run-aft.sh` real-tier preflight, `~/.codex/auth.json` / `.credentials.json` /
  `cursor-agent status`). Redesigned around an *unavailable backend* instead of absent
  credentials.

**Two missing cases added**
- **CUS-D19** — a clean built-in PR Review template baseline that must pass *before* CUS-S3
  poisons the `pr-review` role, so the poison case's failure cannot be misattributed.
- **CUS-D20** — the `prompt` + `prompt_file` precedence contract: `ensureAgentRole` stores
  **both** (`agent_service.go:470-478`), launch adds `--prompt` only when inline `Prompt` is
  empty (`agent_session.go:393-395`), and `loadLeadRolePrompt` returns `role.Prompt`
  (`lead.go:197`). Precedence is implicit and untested.

**Rejected, with evidence**
- *"aft has no `expect.attr`; rewrite those to `wait.fn`."* **False, and acting on it would
  have made the plan worse.** `AttrExpectSchema` exists (`testing-app/src/types.ts:86-88`), is
  wired into `ExpectSchema` (`:105`), and executes via `agent-browser get attr`
  (`src/steps.ts:328-330`). `tests/aft/suites/zz-agent-flow.test.yaml:224` uses it today in a
  passing suite, flanked by `expect.enabled` (`:223`) and `expect.count` (`:225`). The full
  assertion vocabulary is `url, text, notText, title, visible, count, attr, value, enabled,
  checked` (`types.ts:97-112`). **Rule for authors of this plan:** prefer these first-class
  assertions and reach for `wait.fn` only when the predicate genuinely needs JS (multi-element
  DOM queries, `window.__aftXss`, or waiting on a store to settle) — that is how CUS-D2/D3 use
  `expect.enabled`, CUS-D3/D4 use `expect.value`, and CUS-D6/D15 use `wait.fn`.
- *"CUS-D18's name-render citation should be `AgentDetailMain.tsx:275-283`."* Also wrong:
  that range is inside `EphemeralWorkerSummary`, reachable only when
  `mode === "ephemeral" && !isInteractiveAgent` (`AgentDetailMain.tsx:183-185`) — never for a
  custom-prompt agent. Revision 1's `:466-471` was the *role label*, not the name. Narrowed to
  the correct pair: name at `:573`, role label at `:464-468`.

**Also folded in:** `gemini` is registered as a backend (`backend_gemini.go:144`) but has **no
stub in any farm** (`e2e/stubs`, `e2e/stubs-real-*`), so any case that boots a gemini terminal
would invoke the operator's real CLI — every such case now carries a PATH guard. The
`echo` backend is build-tagged out (`backend_echo.go:1` `//go:build testbackend`), so it is
not a determinism escape hatch and the backend-list assertion stays an *includes*, never an
exact set (`backend_external.go:174` can register arbitrary extra names).

---

## Overview

### What the template is

`CreateAgentModal` renders five template cards. The fifth, **Custom prompt**
(`create-agent-template-custom-prompt`, `CreateAgentModal.tsx:63-72`), is the only one
that lets a human type free-form agent instructions. Selecting it reveals a textarea
(`create-agent-interactive-prompt`, `CreateAgentModal.tsx:544-563`) and submits:

```jsonc
{
  "name":       "<normalized name>",
  "role_name":  "<the same normalized name>",   // CreateAgentModal.tsx:313
  "kind":       "interactive",                   // CreateAgentModal.tsx:314-317
  "prompt":     "<textarea text, client-trimmed>",
  "auto":       false,
  "cross_repo": <bool>,
  "repos":      [...],
  "backend":    "<select value, default codex>"
}
```

to `POST /api/workspaces/{ws}/agents`. Submit gating is `hasPromptSelection`
(`CreateAgentModal.tsx:270-278`): for the custom card, `customPrompt.trim() !== ""`.

The decisive structural fact is **`role_name === name`**. A custom-prompt agent does not
reuse a shared role; it *mints a workspace-scoped Role named after itself*. Every
collision, contamination, and lifecycle oddity below flows from that one decision.

### Where the prompt is stored

| Hop | Code | Effect |
|---|---|---|
| Handler | `internal/webui/handlers/agents/handlers.go:54-88` | decodes `AgentCreateInput`; body capped at 1MB by `handler.ReadJSON` (`internal/webui/server/handler/request.go:29`, 413 `request body too large (max 1MB)`) |
| Service | `internal/webui/svcimpl/agent_service.go:351-386` | `name` lowercased+trimmed (`:356`), `kind` lowercased (`:357`), **`prompt` server-side `strings.TrimSpace`** (`:358`). **`role_name` is *not* generally normalized** — `normalizeFirstClassAgentRole` (`:516-523`) lowercases only to test the value and returns it **unchanged** in the `default` branch; only `lead`/`orchestrator` are canonicalized. Custom role names arrive pre-normalized only because the UI assigns `roleName = trimmedName` (`CreateAgentModal.tsx:293,313`) |
| Validation | `internal/webui/svcimpl/agent_service.go:616-631` | name charset, `role_name` non-empty, `kind ∈ {"", interactive, worker}`. **No prompt validation at all** |
| Role upsert | `internal/webui/svcimpl/agent_service.go:451-489` `ensureAgentRole` | creates Role `{name, kind: interactive, description: "Interactive terminal agent", prompt: <text>}` — or reconciles against a pre-existing role (`:496-510`) |
| Persistence | `internal/infra/fleetdb/role.go:64-105` → fleet-db `POST /api/v1/{ws}/roles` | `Role.Prompt` column |
| fleet-db cap | `internal/models/role.go:29-31,164-169` (harness checkout `~/.cache/loom-aft/fleet-db-github-main`) | **`MaxRolePromptBytes = 100_000`**, over → 400 `role prompt must be at most 100000 bytes` |

Then `store.Agents().Create` writes the agent row. Interactive agents skip worktree
creation entirely (`agent_service.go:389-395` returns early on `RoleKindInteractive`).

### How the prompt reaches the spawned process

1. Human opens `/ws/{ws}/agents/{name}`; `AgentDetailMain` mounts `TerminalView`, whose
   `useSessionSeeding` calls `POST /api/workspaces/{ws}/agents/{name}/terminal/session`
   (`api/terminal/terminal.ts:98-117`).
2. `buildAgentLaunchSpec` (`terminal/agent_session.go:319-337`) builds argv+env.
   `agentLaunchCommandArgs` (`:390-406`) for an interactive role returns `["lead"]`, and
   **appends `--prompt <PromptFile>` only when `role.Prompt` is empty** (`:393-395`). A
   custom-prompt agent therefore launches a bare `lead` — the prompt is *not* in argv.
3. `agentLaunchEnv` (`:430-444`) sets `LOOM_AGENT_NAME`, **`LOOM_AGENT_ROLE = agent.RoleName`**,
   `LOOM_AGENT_TERMINAL_ID`, `LOOM_WORKSPACE`, `LOOM_BACKEND`.
   `agentLaunchCwd` (`:339-347`) is **empty for interactive agents** (no remembered worktree).
4. Spec is persisted as `TabMetadata.Launch{Argv,Env,Cwd}` (`internal/webui/tabmeta/store.go:36-63`),
   argv shell-quoted by `ShellArgvForCommand` (`internal/webui/terminal/session_command.go:39-46`)
   into `["-c", "'<loom>' '--workspace' 'WS' '--backend' 'codex' 'lead'"]`.
5. The PTY runs `loom lead`. `loadLeadRolePrompt` (`internal/cli/agent/lead/lead.go:160-198`)
   reads `LOOM_AGENT_ROLE` from env, fetches that Role from the store, and returns
   `role.Prompt` (`:197`).
6. `GenerateTerminalPromptText` (`internal/cli/agent/prompts.go:399-403`) returns
   `text + "\n\n" + buildSafetyGuardrailsBlock()`. Its doc comment states inline text is
   **"intentionally not parsed as a Go template"** — the contract CUS-D5 pins.
7. `applyLeadPromptContext` (`lead.go:200-210`) may append a backend-assignment block.
8. `backends.RunControlledLeadRuntime` (`internal/cli/backends/harness_lead_runtime.go:36-70`)
   dispatches: codex → `RunCodexLeadRuntime` (prompt is the **final positional argv** of the
   codex TUI, `internal/leadcontrol/codex_runtime.go:145-153`); claude/gemini/opencode/cursor →
   `runHarnessLead` (prompt appended as **final positional argv**,
   `internal/leadcontrol/harness_runtime.go:210-216`).
9. `LOOM_LEAD_CONTROLLED=0` (`harness_lead_runtime.go:14-26`) falls back to `cli.InvokeAgent`,
   which for codex **branches on whether stdin is a TTY**
   (`defaultCodexInvoker`, `internal/cli/backends/backend_codex.go:63-79`):
   - TTY → `codex --no-alt-screen --dangerously-bypass-approvals-and-sandbox <prompt>` (`:41-52`)
   - **not** a TTY → `codex exec --json --dangerously-bypass-approvals-and-sandbox <prompt>`
     under the in-process harness wrapper (`:80-116`, `backend_wrapper.go:66-95`)

   This branch is why CUS-D14 has to be careful: a `run:` step has no TTY, so a probe launched
   from one *always* takes the second form. Under the UI's PTY, stdin is terminal-shaped, so
   the real path is the controlled runtime at step 8.

So the prompt travels **DB → env-keyed role lookup → positional argv** in every one of these
four shapes. There is no file and no stdin hop — which is what makes a byte-exact argv
assertion the right proof, and what makes the argv-quoting and per-argument-length concerns in
CUS-D4 / P-3 real rather than theoretical.

### Current coverage: zero

`grep -rn "custom-prompt|create-agent-interactive-prompt|create-agent-backend|create-agent-error"`
over `suites/ surface-suites/ real-suites*/ real-terminal-suites/` returns nothing.
`zz-agent-flow.test.yaml` clicks only `create-agent-template-lead` and
`create-agent-template-task`. The census confirms `create-agent-interactive-prompt`,
`create-agent-backend`, and `create-agent-error` exist as UI testids and are untouched
(`reports/work/*/census.json`). The template-card testids are census-**invisible**
because they reach `AgentTemplateCard` through `testId={CONST.testId}` rather than a
literal `data-testid=` — see blocker **S-6**.

### Readback surfaces available today

| Fact | Surface | Notes |
|---|---|---|
| agent row (`name`, `role_name`, `backend`, `repos`, `cross_repo`) | `GET /api/workspaces/{ws}/agents` (**list only**) | `domain.Agent` has **no** `kind`/`prompt` field (`internal/domain/agent.go:42-78`). There is **no `GET …/agents/{name}`** — only list, PATCH, DELETE, `/queue` (`handlers/agents/module.go:25-29`) |
| resolved role kind | `GET /api/workspaces/{ws}/monitor/status` → `agents[].role_kind` | filled by `ResolveRoleKind` (`internal/cli/serve/metricscmd/monitor_store_data_source.go:178`) |
| launch argv/env/cwd | `GET /api/workspaces/{ws}/terminal/tabs` | full `TabMetadata` incl. `launch`; live in the e2e stack (seen in `reports/server.log`). Written by the ensure-session call **without spawning a PTY**, so launch-spec inspection stays deterministic even when the runtime later fails (see **S-7**) |
| **prompt bytes** | ❌ no HTTP route | there is **no `/roles` endpoint in `internal/webui`** at all |
| prompt bytes (workaround) | `LOOM_CONFIG_DIR=$AFT_LOOM_CONFIG_DIR $AFT_LOOM_BIN --workspace <WS> role show <name> --json` | `internal/cli/role/role_cmd.go:182-190`; `--workspace` → `LOOM_WORKSPACE` → `ResolveActiveWorkspaceKey` (`internal/bootstrap/mode.go:76-91`). No `LOOM_TESTSUPPORT` gate. Same binary/config the `seed-log` steps already use. |

**Mechanism constraint that shapes every readback below.** `api:` assertions are dot-path
scalar checks with positional-only array access (`testing-app/src/api-step.ts:70-78,160-182`) —
they cannot say "some element whose `name` is X". Combined with the missing single-agent GET,
**every per-agent readback in this plan is `run:` + `curl` + `python3` over the list
response**, in the style already used by `zz-agent-flow.test.yaml`. `api:` steps are still the
right tool for fixture provisioning, status-code assertions, and `save:` of scalars.

**Second constraint: launch metadata must be polled, never read once.** The ensure-session call
is fired from a `useEffect` and consumed in a `.then()`
(`useSessionSeeding.ts:123-149`), so a mounted `TerminalView` does **not** imply the tab
metadata exists yet. Every case that reads `launch.*` (D10, D11, D17, D19, D20) must **poll
`GET /terminal/tabs` until a tab with the exact `agent_id` appears**, in the bounded-retry
`for i in $(seq 1 N); do … sleep 1; done` style the existing suites use — never a single GET
after a `wait.fn` on the terminal mount. Surface-tier cases may instead `POST
…/agents/{name}/terminal/session` directly and read its response body, which is synchronous.

### Suite shape recommended

New file `tests/aft/suites/zz-custom-prompt.test.yaml`, own workspace **`E2E-WS-CUSTOM`**
(zz- prefix + dedicated workspace, per README: suites creating persistent agent state must
not leak into `E2E-WS`). `setup:` creates the workspace with one git repo (copy the
`zz-agent-flow` setup block); `teardown:` deletes every `*-${RUN_ID}` agent, then the
workspace. Plus `tests/aft/surface-suites/custom-prompt-contracts.test.yaml` for the
API-only probes (D7b, D14, D20, S1, S2, S3). All names `${RUN_ID}`-scoped.

**Three placement rules that revision 3 exists to enforce. Violating any of them makes cases
fail for reasons unrelated to the product.**

1. **No case may depend on another suite's workspace or fixtures.** aft runs each suite's
   `teardown:` inside a `finally` after all of that suite's tests, before it opens the next
   path (`testing-app/src/runner.ts:255-272`). A case in `surface-suites/` therefore cannot use
   an agent created in `suites/zz-custom-prompt`. `$AFT_WORK_DIR` *files* do survive (it is
   per-run, set once in `run-aft.sh`), but the workspace they describe does not. This is why
   **CUS-D14 lives in the zz suite, not the surface suite**, and why each case below names its
   own fixture owner.
2. **Three workspaces, not one.**
   - `E2E-WS-CUSTOM` — everything in `suites/zz-custom-prompt.test.yaml` (D1–D18, D20's UI half
     if any, D14).
   - `E2E-WS-CUSTOM-CTL` — the clean-control workspace for **CUS-D19**, which must observe a
     virgin `pr-review` role.
   - `E2E-WS-CUSTOM-POISON` — a throwaway for **CUS-S3**, created and deleted in the surface
     suite's own hooks. S3 permanently ruins its workspace's `pr-review` role, and D19 ruins it
     for S3 in the other direction: D19 creates the role, agent-delete leaves it
     (`agent_service.go:594-604`), and S3's first creation then hits the prompt-mismatch branch
     (`:503-508`) with a 400 where it expects 201. Separate workspaces make the two independent
     and order-free.
3. **Role rows outlive agents**, so every case needs a `${RUN_ID}`-unique name or a second run
   inside one stack will 400 on a stale role. Deleting the whole workspace in `teardown:` is the
   only complete cleanup available (**S-5**).

---

## Part 1 — Deterministic tier (stub AI backend)

Common preconditions unless stated: `E2E-WS-CUSTOM` exists with ≥1 repo; browser at
`/ws/E2E-WS-CUSTOM/agents`; `+ Add agent` clicked; `create-agent-overlay` present;
`create-agent-template-custom-prompt` clicked.

### CUS-D1 — happy path: create a custom-prompt agent and read back kind + prompt bytes

- **Tier:** product-correctness
- **Intent:** An operator defines a terminal teammate with inline instructions through
  CreateAgentModal, and confirms the workspace persisted it as an interactive agent whose
  role carries exactly the text they typed.
- **Steps**
  1. `open: /ws/E2E-WS-CUSTOM/agents` → `wait.fn` on `[data-testid=agents-page]`
  2. `click: { role: button, name: "+ Add agent", exact: true }` → `wait.fn` on `create-agent-overlay`
  3. `click: { testid: create-agent-template-custom-prompt }`
  4. `expect: { visible: { testid: create-agent-interactive-prompt } }` ← the reveal is itself the contract
  5. `fill: { testid: create-agent-name, value: "cust-${RUN_ID}" }`
  6. `fill: { testid: create-agent-interactive-prompt, value: "You are a release-notes reviewer. AFT-PROMPT-MARKER-${RUN_ID}" }`
  7. `click: { testid: create-agent-submit }`
  8. `wait.fn`: overlay gone **and** `document.body.textContent.includes('cust-')` (SSE `agent.refresh`, `handlers/agents/handlers.go:236-250`)
- **Assertions** — all three are `run:` steps, because the agent list is an unordered array
  and `api:` cannot filter it (see the mechanism constraint above):
  - `run:` `curl -sf "$AFT_BASE_URL/api/workspaces/E2E-WS-CUSTOM/agents" > $AFT_WORK_DIR/d1-agents.json`
    then `python3` selecting `a["name"] == "cust-${RUN_ID}"` and asserting
    `role_name == name`, `backend == "codex"`, `not a.get("auto")` — plus
    `assert len([...]) == 1` so a duplicate cannot pass silently
  - `run:` same shape against `/monitor/status`, selecting the agent by `name` and asserting
    `role_kind == "interactive"` and `role == cust-${RUN_ID}`
    (**the only HTTP proof that `kind` landed**)
  - `run:` `LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" --workspace E2E-WS-CUSTOM role show cust-${RUN_ID} --json`
    → `.kind == "interactive"`, `.prompt` equals the typed text byte-for-byte,
    `.prompt_file` empty/absent, `.description == "Interactive terminal agent"`
    (`agent_service.go:465-468`)
- **Edge rationale:** without the `role show` readback, nothing in the product proves the
  prompt was stored at all — the HTTP API is write-only for this field. The `len(...) == 1`
  guard matters because the only alternative to filtering is a positional index, which is
  order-sensitive and would pass against the wrong agent.

### CUS-D2 — empty prompt keeps the submit button disabled

- **Tier:** product-correctness
- **Intent:** An operator selecting the Custom prompt template cannot create an agent until
  they have written instructions for it.
- **Steps:** select the custom card → `fill create-agent-name` with a valid name → leave
  the textarea untouched → assert → type one character → assert → clear it → assert.
- **Assertions**
  - `expect: { enabled: { testid: create-agent-submit, equals: false } }` while empty
  - `expect: { enabled: { testid: create-agent-submit, equals: true } }` after typing
  - back to `false` after clearing (`fill` with `""`)
  - `expect: { count: { testid: create-agent-error, equals: 0 } }` — gating, not an error path
- **Edge rationale:** pins `hasPromptSelection` (`CreateAgentModal.tsx:270-278`), the only
  thing standing between a user and the silent-Lead-fallback described in CUS-S1.

### CUS-D3 — whitespace-only prompt is treated as empty

- **Tier:** product-correctness
- **Intent:** An operator who types only spaces and newlines into the custom prompt still
  cannot submit.
- **Steps:** select card, valid name, `fill create-agent-interactive-prompt` with
  `"   \n\t   \n  "` (YAML double-quoted so escapes survive).
- **Assertions:** submit disabled; `expect: { value: { testid: create-agent-interactive-prompt, ... } }`
  confirms the textarea really holds the whitespace (i.e. the disable is the trim, not a failed fill).
- **Edge rationale:** two independent trims exist (client `customPrompt.trim()`
  `:270-274`, server `strings.TrimSpace` `agent_service.go:358`). This pins the client one;
  CUS-S1 pins what happens when the client one is bypassed.

### CUS-D4 — multiline + shell/markdown metacharacters persist byte-exact

- **Tier:** product-correctness
- **Intent:** An operator pastes a realistic multi-paragraph prompt containing quotes,
  backticks and shell metacharacters, and the workspace stores it unchanged.
- **Fixture owner:** this case. Written to `$AFT_WORK_DIR/cus-d4.prompt` by its own first
  `run:` step, so the *same bytes* are both filled and compared.
- **Trailing-newline trap (revision 3).** A quoted heredoc (`<<'EOF'`) appends a final `\n`;
  `$(cat file)` strips **all** trailing newlines; and the server `TrimSpace`s
  (`agent_service.go:358`). Byte-equality against the raw file is therefore impossible unless
  the fixture has no terminal newline. Generate it with `printf '%s'` (or
  `python3 -c 'open(p,"w").write(TEXT)'` with no trailing `\n`) — **not** a heredoc. Then
  filled bytes == file bytes == stored bytes, and the comparison is a plain `==`.
- **Fixture** (single line shown escaped; author it as a YAML block scalar):
  ```
  Line one: review PRs.
  Line two: run `make gate` and $(echo not-substituted).
  Line three: quotes "double" 'single' and a backslash \ plus |&;><*?[]{}$
  Line four: unicode ✅ emoji 🚀 accents éü and CJK 漢字
  Line five: **markdown** _emphasis_ and a [link](https://example.test)
  ```
  (No leading/trailing whitespace — CUS-D9 owns trimming.)
- **Steps** — the textarea is filled by a `run:` step, **not** a `fill:` step, so the fixture
  file is the single source of truth for both the input and the comparison:
  1. `run:` write the fixture **without a terminal newline** into `$AFT_WORK_DIR/cus-d4.prompt`
  2. select the custom card; `fill: { testid: create-agent-name, value: "cust-multi-${RUN_ID}" }`
  3. `run:` `agent-browser --session "$AFT_SESSION" find testid create-agent-interactive-prompt fill "$(cat "$AFT_WORK_DIR/cus-d4.prompt")"`
     — `AFT_SESSION` is exported to `run:` steps (`testing-app/src/steps.ts:168-181`) and this
     `agent-browser --session "$AFT_SESSION"` pattern is already used in `zz-agent-flow`
     `intent:` — "An operator pastes a multi-paragraph prompt containing shell metacharacters."
  4. `expect: { enabled: { testid: create-agent-submit, equals: true } }` — proves the fill
     landed in React's controlled state before submitting
  5. `click: { testid: create-agent-submit }` → wait for the overlay to close
- **Assertions**
  - `run:` `role show --json > $AFT_WORK_DIR/cus-d4.json`, then
    `python3 -c 'import json,sys; got=json.load(open(sys.argv[1]))["prompt"]; want=open(sys.argv[2],encoding="utf-8").read(); assert got==want, (len(got),len(want))'`
  - assert `len(prompt.encode())` equals the fixture's byte length (catches a silent
    re-encode that a `==` on decoded strings would still pass in some Python configs)
- **Why not a `fill:` step with an inline YAML scalar:** it would work, but the expected bytes
  would then have to be duplicated in the `run:` comparison step and could drift. There is no
  `valueFile` variant of `fill` — `value` is passed as one argv element to `execFile`
  (`testing-app/src/steps.ts:222-225`, `src/browser.ts:35-43`). See **S-4**.
- **Edge rationale:** the prompt ends up as a **positional argv element** for codex/claude
  (`codex_runtime.go:152`, `harness_runtime.go:212-213`) and as a shell-quoted string in the
  tab launch spec (`session_command.go:17-22`). A single dropped/expanded metacharacter is an
  argv-injection or corruption bug. `$(...)` and backticks are the argv-injection canaries.

### CUS-D5 — Go-template-looking text reaches a connected controlled Codex agent verbatim

- **Tier:** product-correctness
- **Intent:** An operator whose prompt happens to contain Go template syntax gets that text
  delivered to the agent literally, not silently substituted with Loom's internals.
- **Fixture:** `Refer to {{ .AgentName }} and {{.Role}}. Unclosed: {{ if .SafetyBlock }}`
- **Steps:** create via UI as in CUS-D1 with name `cust-tmpl-${RUN_ID}` → open its agent detail.
  The terminal-state checks below are one grouped Verify card so the identical final UI is shown
  once while a reviewer can expand the individual checks.
- **Assertions**
  - `role show --json` `.prompt` equals the fixture verbatim (`{{ .AgentName }}` still present)
  - the UI-created terminal's remote client connects to the controlled app-server without an
    agent-start error and remains mounted under the expected custom-agent identity
  - a same-origin `/terminal/tabs` readback finds exactly one matching `agent_id`, with
    `kind=agent`, `role=<agent name>`, `backend=codex`, and `pty_alive=true`
  - the remote process reports the SHA-256 fingerprint of the prompt prefix it actually received
    plus `safety-blocks=1`; the expected fingerprint represents the literal `{{ .AgentName }}`
    fixture rather than `cust-tmpl-${RUN_ID}` — this is the assertion that pins
    `GenerateTerminalPromptText`'s "intentionally not parsed as a Go template"
    (`prompts.go:399-403`), and distinguishes it from the `prompt_file` path, which **is**
    template-rendered (`prompts.go:371-395` → `renderPrompt`/`LoadPromptTemplate`)
  - no 5xx: an unclosed `{{` would panic `renderPrompt` (`prompts.go:63-68`) if the inline
    path ever routed through the template engine — **expect 201 and no server error**
- **Fixture owner:** this case. Its prompt fixture is written to
  `$AFT_WORK_DIR/cus-d5.prompt` (no terminal newline, same rule as CUS-D4) and is **also read
  by CUS-D14**, which is why both cases must live in the same suite file.
- **Edge rationale:** this is a *regression fence*. Today the inline path bypasses
  `text/template`; if someone "unifies" the two prompt paths, an unclosed `{{` becomes a
  panic at agent boot and `{{ .AgentName }}` becomes an information leak.

### CUS-D6 — XSS-shaped prompt text is inert in the agents UI

- **Tier:** product-correctness, but explicitly labelled a **negative-rendering regression
  fence — NOT sanitizer coverage.** It asserts "the prompt is not rendered", which is what the
  product does today; it does not and cannot demonstrate safe rendering. Do not count it
  toward XSS coverage in any census or report narrative.
- **Intent:** An operator's prompt containing executable HTML never becomes executable
  markup anywhere in the agents surface.
- **Fixture:**
  `Review PRs.\n<img src=x onerror="window.__aftXss=1">\n<script>window.__aftXss=1</script>\n[click](javascript:window.__aftXss=1)`
- **Steps:** create via UI (`cust-xss-${RUN_ID}`) → `open /ws/E2E-WS-CUSTOM/agents/cust-xss-${RUN_ID}`
  → `wait.fn` on `agents-page` → visit the Info and Logs tabs → `wait: { ms: 250 }`.
- **Assertions**
  - `wait.fn`: `window.__aftXss === undefined && !document.querySelector('script[data-injected], img[onerror]') && !document.querySelector('a[href^="javascript:"]')`
  - `expect: { notText: "onerror" }` on the agents page
  - reload once and repeat (SSE re-render path)
  - **not asserted:** anything about the terminal pane. CUS-D5 now proves the narrow controlled
    Codex bootstrap, but this XSS fence intentionally asserts only DOM safety and does not turn
    backend output into part of its contract.
- **Edge rationale + honesty note:** **this currently passes vacuously** — the prompt is not
  rendered on any surface (no `/roles` route, `WorkspaceAgentInfo` has no `prompt` field,
  `api/workspace/workspace.ts:33-40`). Write it anyway and say so in the suite comment: it is
  the fence that fires the day a "show prompt" surface lands (see CUS-D12 / **P-5**).
  The field that *is* rendered is the agent **name** — `AgentDetailMain.tsx:573` in the
  header, `AgentCard.tsx:107` in the rail (`:464-468` renders the *role label*, which for a
  custom-prompt agent happens to equal the name) — and it is charset-constrained by
  `ValidStoredAgentName` (`internal/webui/service/agent.go:172`), covered by CUS-D18.

### CUS-D7a — long prompt (32 KB) survives the UI round trip

- **Tier:** product-correctness
- **Intent:** An operator pastes a long, detailed prompt and the agent is created with all
  of it intact.
- **Steps:** `run:` generates a 32 KB deterministic fixture
  (`python3 -c '...' > $AFT_WORK_DIR/cus-d7a.prompt`), then the **same `run:` +
  `agent-browser … fill "$(cat …)"` mechanism as CUS-D4** — a `fill:` step cannot read a file.
- **Assertions:** submit enabled; 201; `role show --json` `len(prompt.encode()) == 32768` and
  the first/last 64 bytes match the fixture.
- **Edge rationale:** 32 KB is comfortably under `MAX_ARG_STRLEN` (131 072 B, the Linux
  per-argument ceiling the launch path will eventually hit) and well inside what
  `execFile` can pass to `agent-browser`, so it tests the product without testing the harness.
  It is also the largest size this plan is confident a *browser* fill will survive; the
  boundary itself is CUS-D7b's job.

### CUS-D7b — prompt length boundary at the fleet-db cap

- **Tier:** surface (API-only; the UI cannot reliably drive a 100 KB textarea fill through
  agent-browser argv). Promotion condition: promote when aft grows a file-backed text-entry
  step (**S-4**), or when a server-side prompt-length validator with a UI-surfaced message
  lands (**P-2**).
- **Intent:** The workspace API accepts an inline prompt at the persistence cap and rejects
  one byte over it with an actionable message.
- **Mechanism:** `run:` + `curl`, **not** `api:` — `api.body` is object-or-string with no
  `bodyFile` (`testing-app/src/types.ts:144-151`, `src/api-step.ts:107-115`), and the asserts
  cannot express `len(prompt)` or "no row with this name"
  (`src/api-step.ts:70-78,160-182`). Each sub-case: `python3` writes the JSON payload to a
  file, `curl -sS -o resp.json -w "%{http_code}" --data-binary @payload.json`, then `python3`
  asserts the code and body.
- **Steps/assertions**
  - 100 000-byte prompt → `201`; then `role show --json` → `len(prompt.encode()) == 100000`
  - fresh name, 100 001-byte prompt → `400`, body contains
    `role prompt must be at most 100000 bytes`
    (`fleet-db internal/models/role.go:164-169` → `domain.ErrInvalid`
    → `classifyStoreError` `svcimpl/validators.go:60-75` → `ErrValidation` → 400)
  - readback the agent list and assert **no row** for the rejected name — `ensureAgentRole`
    runs *before* `Agents().Create` (`agent_service.go:363-372`), so the failure must be clean
  - >1 MB body → `413`, body contains `request body too large`
    (`server/handler/request.go:29-36`)
- **Edge rationale:** three different limits (100 KB role prompt, 1 MB request body,
  ~128 KB single-argv) live on the same field with no single owner. See **P-3**.

### CUS-D8 — name/role collisions with the auto-seeded roles

Every workspace is seeded with roles `plan`, `task`, and `lead`
(`internal/cli/serve/workspacemgr/workspace_store.go:478-506`). Because
`role_name === name`, a custom-prompt agent named after one of them collides.

- **Tier:** product-correctness (all sub-cases drivable from the modal)
- **Intent:** An operator who names a custom agent after a built-in role is told exactly
  why it cannot be created, instead of silently getting the wrong runtime.
- **Sub-cases** (each: select card, fill name, fill a distinct prompt, submit, assert the
  error alert, assert the modal stays open, assert no agent row appeared):

  | id | name | expected HTTP | expected `create-agent-error` text |
  |---|---|---|---|
  | D8a | `task` | 400 | `role "task" already exists and is not interactive; choose a different agent name` (`agent_service.go:500`) |
  | D8b | `plan` | 400 | same shape |
  | D8c | `lead` | 400 | `role "lead" already exists with a different prompt; choose a different agent name or reuse its prompt` (`agent_service.go:503-505`; `normalizeFirstClassAgentRole` `:516-523` maps the name to the first-class `lead` role) |
  | D8d | `orchestrator` | **201 — deterministically succeeds** | see the dedicated assertions below; this row does **not** use the shared error-alert assertions |

  **CUS-D8d is not a probe (corrected in revision 3).** Only `plan`, `task`, and `lead` are
  seeded (`workspace_store.go:478-506`), so `ensureAgentRole` takes the **not-found** branch:
  `ResolveRoleKind(&Role{Kind:"interactive"}, "orchestrator")` resolves interactive
  (`domain/role.go:46-56`), `kind != "worker"`, and the role is created (`:470-478`). The
  outcome is a deterministic **201**. Its assertions are therefore the *opposite* of D8a–c:
  - `wait.fn` the overlay closes; `expect: { count: { testid: create-agent-error, equals: 0 } }`
  - `run:` list filter → an agent named `orchestrator` exists with `role_name == "orchestrator"`
  - `run:` `role show orchestrator --json` → `kind == "interactive"`, `prompt` == the typed text,
    and **`description == "Lead/orchestrator interactive"`** — because `isLeadAgentRole`
    → `IsInteractiveRoleName("orchestrator")` is true (`domain/role.go:58-66`,
    `agent_service.go:465-468`). A *custom* agent gets the lead role's description
  - `run:` `/terminal/tabs` (polled) → `launch.env.LOOM_AGENT_ROLE == "orchestrator"`
  This is the sharpest form of **P-1**: the legacy first-class interactive role name is
  claimable by any custom prompt, and nothing warns. Contrast D8c, where `lead` *is* seeded and
  so 400s — the guard is presence of a seeded row, not reservation of the name.

- **Assertions (D8a–D8c only; D8d has its own, above):** `expect: { visible: { testid: create-agent-error } }`,
  `expect: { visible: { testid: create-agent-overlay } }` (modal not closed —
  `handleSubmit` returns via the catch at `CreateAgentModal.tsx:355-362`), and a `run:` +
  `python3` readback over `GET /agents` proving the agent count did not change.
- **Edge rationale:** this is the sharpest consequence of `role_name === name`. It is also
  where the error message is a UX liability: "choose a different agent name" is the only
  remedy offered, and it is correct only by accident.

### CUS-D9 — leading/trailing whitespace is trimmed, interior whitespace preserved

- **Tier:** product-correctness
- **Intent:** An operator who pastes a prompt with stray blank lines around it gets the
  instructions stored without them, and the interior formatting untouched.
- **Fixture:** `"\n\n   AFT-TRIM-START-${RUN_ID}\n\n    indented line kept\n\nAFT-TRIM-END   \n\n"`
- **Assertions:** stored prompt starts with `AFT-TRIM-START` and ends with `AFT-TRIM-END`
  (no surrounding whitespace), and still contains `\n\n    indented line kept\n\n`.
- **Edge rationale:** double trim (client `:270-274` and `:316`; server `:358`). Pins that
  the trim is exactly `TrimSpace` on the whole string, not a per-line strip.

### CUS-D10 — backend selection reaches both the agent row and the launch spec

- **Tier:** product-correctness
- **Intent:** An operator who picks a non-default AI backend for their custom agent gets a
  terminal session that actually launches on that backend.
- **Steps:** custom card → name `cust-be-${RUN_ID}` → `select: { testid: create-agent-backend, value: "claude" }`
  → prompt text → submit → `open /ws/E2E-WS-CUSTOM/agents/cust-be-${RUN_ID}` → wait for the
  terminal mount (`TerminalView` triggers the ensure-session POST).
- **Assertions** — one `run:` step **polling** `/terminal/tabs` until a tab with this exact
  `agent_id` exists (the ensure-session promise is async — `useSessionSeeding.ts:123-149`), then
  a `python3` block selecting that tab by `agent_id` (arrays again — `api:` cannot filter):
  - agent list readback → `backend == "claude"`
  - the selected tab has `kind == "agent"`, `role == cust-be-${RUN_ID}`, `backend == "claude"`
  - `launch.argv[0] == "-c"`; `launch.argv[1]` contains `'--backend' 'claude'` and ends with
    `'lead'`, and **does not contain `--prompt`** — this is the inline-prompt branch of
    `agentLaunchCommandArgs` (`:393-395`). Every element is single-quoted by `shellQuote`
    (`session_command.go:17-22`), so match on the quoted forms
  - `launch.env.LOOM_AGENT_ROLE == cust-be-${RUN_ID}` and `launch.env.LOOM_AGENT_NAME == cust-be-${RUN_ID}`
    — the env key that `loadLeadRolePrompt` uses to find the prompt (`lead.go:161-197`)
  - `launch.cwd == ""` — interactive agents get no worktree (`agent_service.go:389-395`,
    `agentLaunchCwd:339-347`). Pin it; see **P-4**
  - a companion default-backend agent asserts `backend == "codex"` and `'--backend' 'codex'`
  - `api: GET /api/backends` → the list **includes** `codex`, `claude`, `cursor`, `opencode`,
    `gemini` (registration sites: `backend_codex.go:234`, `backend_claude.go:200`,
    `backend_cursor.go:167`, `backend_opencode.go:137`, `backend_gemini.go:144`). This one
    *can* stay an `api:` step: `assert: [{path: "data", contains: "codex"}, …]` works because
    `contains` JSON-stringifies non-string values before the substring test
    (`printable`, `testing-app/src/api-step.ts:20-24`). Assert *inclusion only*, never an exact
    set — `backend_external.go:174` can register arbitrary configured names. (`echo` will not
    appear: `backend_echo.go:1` is `//go:build testbackend`.)
  - **not asserted:** `pty_alive`, terminal rows, or any backend output. This case is a launch-
    specification comparison, not a runtime-health case. The launch spec is
    written by the ensure-session handler (`ensureAgentTerminalSession` → `PutTab` → `GetTab`,
    `agent_session.go:76-137`) with no dependency on a successful spawn. CUS-D5 separately pins
    the deterministic Codex bootstrap; this case's non-default Claude path does not implement
    the same controlled-session handshake. Choose `claude` because its executable is stubbed —
    **never `gemini`**, which has no stub in any farm and would reach the operator's real CLI
- **Edge rationale:** the argv+env pair is the *entire* spawn contract for a custom-prompt
  agent, and it is asymmetric (backend in argv, prompt identity in env). It is also
  free to assert — `GET /terminal/tabs` is a read.

### CUS-D11 — repo scoping for a custom interactive agent

- **Tier:** product-correctness
- **Intent:** An operator scopes their custom agent to one repo, then to the whole
  workspace, and each choice persists.
- **Steps:** (a) leave the modal's preselected first repo chip and submit; (b) second agent,
  click the chip to deselect (`[data-testid='create-agent-repo-chips'] button`, first) and submit.
- **Assertions:** (a) `cross_repo == false`, `repos == ["<repo>"]`; (b) `cross_repo == true`,
  `repos` empty (both via the `run:` + `python3` list filter). In **both** cases
  `launch.cwd == ""`.
  The worktree assertion is **scoped to this agent's own expected path**, not a workspace-wide
  glob (which any other suite's background agents would contaminate). The canonical path is
  `filepath.Join(workspacePath, "worktrees", repoName, agentName)` — i.e.
  **`<workspacePath>/worktrees/<repo>/<agent>`** (`internal/localworkspace/localworkspace.go:40-42`).
  Revision 2 wrote `<workspace>/<repo>-<agent>`, which is wrong and would have passed even if a
  real worktree existed. Resolve `workspacePath` from the workspace API payload; never hard-code
  it.
- **Edge rationale:** the repo chips are shown for a template whose runtime ignores them for
  worktree purposes (`agent_service.go:389-395` returns before worktree creation for
  interactive roles). Pinning the divergence keeps it honest and documents **P-4**.

### CUS-D12 — the prompt is not editable (and not visible) after creation

- **Tier:** product-correctness (negative assertion) + FINDINGS entry
- **Intent:** An operator returning to their custom agent finds no way to read or revise the
  instructions they gave it.
- **Steps:** open the agent detail for a CUS-D1 agent; enumerate the tab strip with
  `[data-testid=agent-editor-groups] button:not([data-testid=agent-editor-split])`.
- **Assertions**
  - the tab labels are exactly `Terminal, Info, Git, Logs, Diff, Files`
    (`views/AgentEditorGroups.tsx:33-40`) — no prompt/settings tab.
    **The `:not()` is required:** the bare `button` selector also matches the "Split editor
    right" control (`AgentEditorGroups.tsx:213-222`), which renders whenever
    `groupIndex === 0 && !isSplit && group.tabs.length >= 2` — i.e. in exactly this scenario —
    so revision 2's exact-label assertion would have failed on a seventh button
  - `expect: { notText: "AFT-PROMPT-MARKER" }` — the prompt text appears nowhere
  - `AgentUpdateInput` has no `Prompt`/`Kind` field (`internal/webui/service/agent.go:95-107`),
    so even the API cannot revise it; assert
    `PATCH /agents/{name}` with `{"prompt":"x"}` returns 200 **and** `role show --json`
    shows the prompt unchanged (silently ignored — pin the no-op)
- **Edge rationale:** the only escape hatch is the CLI (`loom role set <name> prompt <text>`,
  `role_cmd.go:282-285`), which the web UI never surfaces. Product gap **P-5**.

### CUS-D13 — recreating a deleted custom agent with a different prompt is blocked by its orphan role

- **Tier:** product-correctness
- **Intent:** An operator deletes a custom agent and recreates it with new instructions, and
  discovers the workspace still holds the old role.
- **Steps:** create `cust-recycle-${RUN_ID}` with prompt P1 (UI) → `api: DELETE /agents/{name}`
  → recreate through the modal with prompt **P2** → assert; then recreate with prompt **P1** → assert.
- **Assertions**
  - P2 attempt → 400, `create-agent-error` contains `already exists with a different prompt`
    (`agent_service.go:503-505`) — `DeleteAgent` (`agent_service.go:594-604`) removes only the
    agent row, never the role
  - P1 attempt → 201 (reconcile passes on identical prompt)
- **Edge rationale:** a real user-visible dead end, and a **teardown hazard for this suite**:
  role rows survive `DELETE /agents/{name}`, so every case must use a `${RUN_ID}`-unique name
  or a rerun inside one stack will fail on a stale role. FINDINGS **P-1**.

### CUS-D14 — the custom prompt actually reaches the backend process argv (non-interactive path)

- **Tier:** surface **in classification, but it must physically live in
  `suites/zz-custom-prompt.test.yaml`** — see the placement note below. It remains a focused
  fallback-path contract. CUS-D5 now makes the corresponding assertion about the UI-triggered,
  server-spawned controlled Codex process through **S-1/S-7**.
- **Placement (revision 3, was broken).** Revision 2 put this case in
  `surface-suites/custom-prompt-contracts.test.yaml` while depending on CUS-D5's agent in
  `E2E-WS-CUSTOM`. aft runs each suite's `teardown:` in a `finally` after that suite's tests and
  before opening the next path (`testing-app/src/runner.ts:255-272`), so by the time the surface
  suite ran, the workspace and the agent were gone. It also read a `cus-d5.prompt` fixture that
  CUS-D5 never wrote. Fixed two ways: the case moves into the zz suite immediately after CUS-D5,
  and **CUS-D5 is now the declared owner of `$AFT_WORK_DIR/cus-d5.prompt`**. (Fixture *files*
  under `$AFT_WORK_DIR` do survive across suites — it is per-run — but the workspace they refer
  to does not, so co-location is what actually matters.) Keep the "surface" label in reports:
  the case is a CLI-runtime contract probe, not a user journey.
- **Scope honesty — read this before writing it.** Revision 1 asserted `--no-alt-screen`,
  which is wrong. `defaultCodexInvoker` checks `isTerminal(os.Stdin)` and, when stdin is not a
  TTY, delegates to `defaultCodexNonInteractiveInvoker`
  (`internal/cli/backends/backend_codex.go:63-79`). A `run:` step has no TTY, so the probe
  necessarily takes the **non-interactive** branch:
  `codex exec --json --dangerously-bypass-approvals-and-sandbox <prompt>`
  (`buildCodexNonInteractiveArgs`, `:108-116`). `--no-alt-screen` belongs only to
  `buildCodexInteractiveCmd` (`:41-44`) and the controlled remote TUI
  (`codex_runtime.go:143-159`).
  **What this case therefore does and does not prove:**
  - ✅ proves: stored prompt → `LOOM_AGENT_ROLE` lookup → `role.Prompt` →
    `GenerateTerminalPromptText` → **final positional argv element**, byte-exact, guardrails
    appended once. `buildCodexNonInteractiveArgs` passes the prompt positionally with an
    explicit comment saying so (`:108-116`), so the load-bearing chain is fully covered.
  - ❌ does **not** itself prove either *controlled* runtime's positional append. CUS-D5 now
    covers `codex_runtime.go:152` (app-server + remote client); the harness-wrapper path at
    `harness_runtime.go:210-216` remains unproved. Do not describe CUS-D14 itself as covering
    the UI-spawned path.
- **Intent:** The local runtime resolves the custom agent's stored prompt and hands it to the
  backend as the final positional argument of its boot invocation.
- **Preconditions:** a CUS-D5 agent exists (`cust-tmpl-${RUN_ID}` is the best subject — it
  doubles as the template-expansion proof) and its fixture file is still in `$AFT_WORK_DIR`.
- **Step sketch** — one `run:` step, everything scoped to that command:
  ```
  d="$AFT_WORK_DIR/stubbin"; mkdir -p "$d";
  cat > "$d/codex" <<'SH'
  #!/bin/sh
  # capture argv NUL-delimited (the prompt contains newlines), then emit the
  # completion event the harness wrapper expects so it does not retry.
  # one record per invocation: NUL between args, then a lone "--\0" terminator, so
  # the log stays parseable if a non-zero exit ever causes a retry.
  printf '%s\0' "$@" >> "$CUS_ARGV_LOG"
  printf -- '--\0' >> "$CUS_ARGV_LOG"
  printf '{"status":"completed","output":"stub"}\n'
  exit 0
  SH
  chmod +x "$d/codex";
  CUS_ARGV_LOG="$AFT_WORK_DIR/codex.argv" PATH="$d:$PATH" \
    LOOM_AGENT_EFFORT= LOOM_CLAUDE_EFFORT= \
    LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" LOOM_LEAD_CONTROLLED=0 \
    LOOM_AGENT_NAME="cust-tmpl-${RUN_ID}" LOOM_AGENT_ROLE="cust-tmpl-${RUN_ID}" \
    timeout 60 "$AFT_LOOM_BIN" --workspace E2E-WS-CUSTOM --backend codex lead \
    </dev/null >"$AFT_WORK_DIR/cus-d14.out" 2>&1;
  python3 - "$AFT_WORK_DIR/codex.argv" "$AFT_WORK_DIR/cus-d5.prompt" <<'PY'
  ...split on b"\0"; take the LAST non-empty element of the FIRST invocation record...
  PY
  ```
- Implementation correction (verified live): Passing the argv-log path through a custom env var
  fails because `buildBackendEnv` rebuilds the child env from the envfilter allowlist and strips
  it; bake the absolute path into the stub script as a literal.
- **Assertions**
  - the captured argv begins `exec`, `--json`, `--dangerously-bypass-approvals-and-sandbox`
    (`backend_codex.go:108-116`) — **not** `--no-alt-screen`. This holds **only** with
    `LOOM_AGENT_EFFORT` and `LOOM_CLAUDE_EFFORT` cleared: `appendCodexEffortArgs` *prepends*
    `-c model_reasoning_effort="…"` when either is set (`backend_effort.go:8-22`), which would
    shift `exec` off position 0. The step sketch clears both
  - the **final** argv element starts with the exact stored prompt (byte-compared against the
    fixture file)
  - it then contains `\n\n### Multi-Agent Safety Rules` **exactly once**
    (`prompts.go:402` + `buildSafetyGuardrailsBlock`)
  - for the D5 subject: it contains the literal `{{ .AgentName }}` and **not** the agent name
  - it does **not** contain the built-in lead prompt's signature text (proving no fallback)
- **Risks to encode in the step**
  - **Retries — corrected in revision 3.** Revision 2 said `Max: 3` meant three invocations and
    that the `{"status":"completed"}` line was what stopped retries. Both were wrong.
    `RetryPolicy.Max` is "the maximum number of retries **after the first attempt**. Max=3 means
    up to 4 total attempts" (`internal/harness/retry.go:26-28`, loop at `:100-113`). And the
    wrapper maps **any** successful exit to `StatusIdle`
    (`harness-wrapper@v0.5.3/pkg/wrapper/pty.go:56-58`), which `shouldRetry` does not retry — it
    falls through the switch to `return false` (`retry.go:134-146`). Stdout content is never
    consulted.
    **Therefore: a stub that `exit 0`s is invoked exactly once.** Keep the JSON line anyway —
    it mirrors the real stub (`e2e/stubs/codex:140-146`) and keeps the wrapper's stream parser
    fed — but do not claim it suppresses retries. Keep the per-invocation `--\0` delimiter and
    the "all records byte-identical" check purely as defence in depth: they only matter if the
    stub is ever changed to exit non-zero, in which case up to **4** records can appear.
    Assert on the **first** record.
  - **No external wrapper binary is required** — `runHarness` uses the in-process
    `harness-wrapper` Go library and passes `BinaryPath: "codex"`
    (`internal/cli/backends/backend_wrapper.go:66-95`), so the per-test stub on `PATH` is what
    gets exec'd. Under the wrapper's PTY the stub's own stdin is terminal-shaped; the stub
    reads no stdin, so that is irrelevant.
  - `runLead` drops into an interactive shell on backend-health or prompt-load failure
    (`lead.go:97-103,118-123`) — hence `timeout 60` and `</dev/null`, plus a non-zero-exit
    branch that dumps `cus-d14.out` and the captured argv before failing.
  - the stub dir is per-test and prepended only for this command, so aft's own real `claude`
    (used by `--strict`/`--heal`) is untouched — matching run-aft.sh's deliberate rule that
    `e2e/stubs` never enters the harness PATH.
- **Edge rationale:** this is the only deterministic proof that DB→env→argv works at all.
  Without it, CUS-D1 proves storage and CUS-D10 proves the launch-spec shape, but nothing
  joins them.

### CUS-D15 — created custom agent appears without a reload (SSE)

- **Tier:** product-correctness
- **Intent:** An operator sees their new custom teammate in the agents rail the moment the
  modal closes.
- **Assertions:** after submit, without `reload:`, wait for the **new agent's own card**:
  `expect: { visible: { selector: "[aria-label='Agent: cust-sse-${RUN_ID}']" } }`, plus the
  modal being gone.
- **Revision 3 correction — the original probe was vacuous.** Revision 2 waited on
  `document.body.textContent.includes('cust-')`, which is **already true** once any earlier case
  in the suite has created a `cust-…` agent, so it proved nothing about the new one.
  `AgentCard` exposes `aria-label={`Agent: ${agent.name}`}` when it is clickable
  (`AgentCard.tsx:77`) — a unique, name-exact anchor. Use a `${RUN_ID}`-unique name
  (`cust-sse-${RUN_ID}`) so the assertion cannot be satisfied by any prior agent.
- **Edge rationale:** `broadcastAgentRefresh` (`handlers/agents/handlers.go:85,236-250`) is
  the only push for agent creation; the store's poll is 5 s, so a name-exact assertion that
  resolves under the 15 s step timeout without a reload is meaningful (zz-agent-flow's monitor
  step needs a reload; the rail should not). A substring probe cannot distinguish "SSE
  delivered it" from "the poll happened to fire" — a name-exact one can.

### CUS-D16 — duplicate custom agent name is refused with a visible error

- **Tier:** product-correctness
- **Intent:** An operator who reuses an existing agent name is told the name is taken and
  the first agent is untouched.
- **Steps:** create `cust-dup-${RUN_ID}`; open the modal again, same name, **same** prompt,
  submit.
- **Assertions:** 409 → `create-agent-error` visible with the conflict message
  (`classifyStoreError` maps `ErrAlreadyExists` → `ErrConflict`, `validators.go:66-67`);
  modal stays open; a `run:` + `python3` filter over `GET /agents` finds exactly one such
  agent, and `role show --json` shows the prompt unchanged.
  Note the ordering subtlety worth pinning: the *role* is reconciled successfully before the
  *agent* create fails, so a same-name/different-prompt duplicate fails at the **role** step
  with a 400 (that variant is CUS-D13's P2 case), while same-name/same-prompt fails at the
  **agent** step with a 409. Two different codes for what a user reads as one mistake.

### CUS-D17a / CUS-D17b — who normalizes `role_name`, and what happens when nobody does

- **Tier:** product-correctness
- **Intent:** An operator typing a mixed-case name gets one canonical agent, addressable at
  the lower-cased route.
- **D17a (UI, product-correctness).** Name `CustMix-${RUN_ID}` (with `${RUN_ID}` digits), valid
  prompt, submit.
  **Assertions:** `run:` filter over `GET /agents` → `name == custmix-${RUN_ID}` **and**
  `role_name == custmix-${RUN_ID}`; `role show custmix-${RUN_ID} --json` resolves;
  `open /ws/.../agents/custmix-${RUN_ID}` renders; polled `/terminal/tabs` →
  `launch.env.LOOM_AGENT_ROLE == custmix-${RUN_ID}`.
- **Where the normalization actually happens — corrected in revision 3.** Revision 2 said the
  server re-normalizes `role_name`. It does not. `normalizeFirstClassAgentRole`
  (`agent_service.go:516-523`) lowercases the value only to *compare* it, and its `default`
  branch returns `roleName` **unchanged**; only `lead` and `orchestrator` are canonicalized.
  Only `in.Name` is normalized (`normalizeStoredAgentName`, `:356`). The lower-casing of a
  custom `role_name` is done **entirely by the modal**, which assigns
  `roleName = trimmedName` where `trimmedName = normalizeStoredAgentName(name)`
  (`CreateAgentModal.tsx:293,313`). So D17a proves a **client-side** invariant.
- **D17b (API, surface).** The server-side gap that follows: `api: POST .../agents` with
  `{name: "custmix2-${RUN_ID}", role_name: "CustMix2-${RUN_ID}", kind: "interactive", prompt: "x"}`
  → expect **400**. The un-normalized `role_name` reaches fleet-db's role create, whose
  name pattern is lowercase-only
  (`roleNamePattern = ^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`,
  `fleet-db internal/models/role.go:12`), so `Roles().Create` rejects it and
  `classifyStoreError` maps `ErrInvalid` → 400. Assert no agent row was created.
- **Edge rationale:** the client is the only thing keeping `name` and `role_name` in sync, and
  `LOOM_AGENT_ROLE` is derived from `role_name` (`agent_session.go:430-444`). If they ever
  diverge, `loadLeadRolePrompt` gets `ErrNotFound`, returns `""` (`lead.go:186-196`), and the
  agent **silently boots on the built-in Lead prompt** — no error, nothing visible in the UI.
  D17b shows the divergence is currently caught only incidentally, by fleet-db's charset rule
  rather than by any loom-side check.

### CUS-D18 — invalid names keep submit disabled; XSS-shaped names never reach the DOM

- **Tier:** product-correctness
- **Intent:** An operator cannot name a custom agent anything the workspace cannot store or
  the page cannot safely render.
- **Sub-cases** (valid prompt present throughout, so the *only* gate under test is the name):
  `-lead`, `lead-`, `a b`, `Ünicode`, `<img src=x onerror=1>`, `../escape`, 101 chars.
- **Assertions:** submit disabled for each (`validateStoredAgentName`,
  `utils/agentName.ts:8-17` mirroring `ValidStoredAgentName`, `service/agent.go:172`);
  `window.__aftXss === undefined`; no `create-agent-error` (this is gating, not submission);
  one valid name at the end re-enables submit.
- **Edge rationale:** the name is the field that *is* rendered — header at
  `AgentDetailMain.tsx:573`, rail at `AgentCard.tsx:107` (`AgentDetailMain.tsx:464-468` renders
  the *role label*, and `:275-283` is the ephemeral-worker branch, unreachable for interactive
  agents per `:183-185`) — and, via `role_name`, the field that becomes a filesystem-adjacent
  identifier. The charset regex is the whole defence; pin it from the UI.

### CUS-D19 — built-in PR Review template baseline (control for CUS-S3)

- **Tier:** product-correctness
- **Intent:** An operator creates a PR Review agent from the built-in template in a clean
  workspace, establishing that the template works before any custom prompt touches its role.
- **Workspace isolation, not ordering (corrected in revision 3).** Revision 2 required D19 to
  run before CUS-S3 *in the same workspace*. That is impossible in either direction:
  D19 creates the `pr-review` role, deleting its agent leaves the role behind
  (`agent_service.go:594-604`), and S3's **first** creation — a custom agent named `pr-review`
  carrying a prompt — then hits `reconcileExistingAgentRole`'s prompt-mismatch branch
  (`:503-508`) and 400s where S3 expects 201.
  **Resolution:** D19 runs in its own clean-control workspace **`E2E-WS-CUSTOM-CTL`**, and S3
  runs in a throwaway **`E2E-WS-CUSTOM-POISON`** created and deleted by the surface suite's own
  hooks. The two are then independent and order-free. D19 remains the *logical* control for S3
  — it is what makes an S3 failure attributable to the namespace collision rather than to a
  broken template — but there is no runtime dependency between them.
- **Steps:** open the modal → `click: { testid: create-agent-template-interactive-pr-review }`
  → name `rev-${RUN_ID}` → submit (no textarea is revealed; the modal sends
  `prompt_file: "builtin:pr-review"` and no `prompt`, `CreateAgentModal.tsx:318-323`).
- **Assertions**
  - `expect: { count: { testid: create-agent-interactive-prompt, equals: 0 } }` before submit —
    the textarea belongs to the custom card only (`CreateAgentModal.tsx:544-563`)
  - 201; `run:` list filter → `role_name == "pr-review"`, `name == rev-${RUN_ID}`
  - `run:` `role show pr-review --json` → `kind == "interactive"`,
    `prompt_file == "builtin:pr-review"`, `prompt` empty
  - `run:` `/terminal/tabs` after opening the agent → `launch.argv[1]` **does** contain
    `'--prompt' 'builtin:pr-review'` — the *other* branch of `agentLaunchCommandArgs`
    (`:393-395`), and the exact contrast CUS-D10 asserts the absence of
  - `GET /interactive-prompts` still lists `pr-review` (registry unchanged)
- **Edge rationale:** it is the control arm. It also gives the plan its only positive coverage
  of the `--prompt builtin:` launch branch, which is what makes CUS-D10's
  "does not contain `--prompt`" assertion meaningful rather than trivially true.

### CUS-D20 — `prompt` + `prompt_file` precedence is implicit and undefined

- **Tier:** surface (the modal never sends both — `CreateAgentModal.tsx:310-325` sets exactly
  one — so this is an API-only contract). Promotion condition: promote when the server picks a
  documented winner and rejects or normalizes the other, at which point a UI path could exist.
- **File placement:** the create + `role show` + `/terminal/tabs` assertions belong in the
  surface file, but the **fourth assertion reuses CUS-D14's argv probe**, which lives in
  `zz-custom-prompt` and needs that suite's workspace alive. Split accordingly: keep the
  storage/launch-spec assertions in `sf`, and add the argv assertion as a final step of the
  CUS-D14 block in `zz` (creating D20's agent there via `api:` in the same test). Do **not**
  write it as a cross-suite dependency — `runner.ts:255-272` tears each suite down first.
- **Intent:** The workspace API's behaviour when an interactive role is created with both an
  inline prompt and a prompt-file selector is pinned, so the implicit precedence cannot
  silently flip.
- **Steps/assertions**
  - `api: POST .../agents` `{name: n, role_name: n, kind: "interactive", prompt: "AFT-INLINE-${RUN_ID}", prompt_file: "builtin:pr-review"}`
    → expect **201** (neither field is validated against the other,
    `validateAgentCreateInput` `agent_service.go:616-631`)
  - `run:` `role show --json` → **both** are stored: `prompt == "AFT-INLINE-${RUN_ID}"`
    **and** `prompt_file == "builtin:pr-review"` (`ensureAgentRole` copies both into
    `RoleCreate`, `agent_service.go:470-478`)
  - `run:` `/terminal/tabs` → `launch.argv[1]` **omits** `--prompt`, because the flag is added
    only when inline `Prompt` is empty (`agent_session.go:393-395`)
  - `run:` the CUS-D14 argv probe against this agent → the delivered prompt is the **inline**
    text, because `loadLeadRolePrompt` returns `role.Prompt` unconditionally (`lead.go:197`)
    and `generateLeadTerminalPrompt` prefers it over the file (`lead.go:150-157`)
  - conclusion to assert in the comment: **inline `prompt` wins, `prompt_file` is stored but
    dead** — three independent code sites conspire to produce that, none of them says so
- **Edge rationale:** a stored-but-ignored field is a latent misconfiguration that surveys
  clean in the database. If anyone "fixes" `agentLaunchCommandArgs` to always pass `--prompt`
  when `PromptFile` is set, every such agent silently switches persona with no data change.

### CUS-S1 — API accepts `kind: interactive` with an empty prompt (server validation gap)

- **Tier:** surface. Promotion condition: promote to product-correctness when the server
  rejects it and the modal surfaces the message — until then no human can reach this state.
- **Intent:** The workspace API's contract for an interactive agent with no instructions is
  pinned, so the silent-Lead fallback is a documented behaviour rather than a surprise.
- **Steps/assertions**
  - `api: POST .../agents` `{name: n1, role_name: n1, kind: "interactive", prompt: ""}` → **201**
    (`validateAgentCreateInput` checks name/role/kind only, `agent_service.go:616-631`)
  - **a second, distinct name `n2`** with `prompt: "   \n  "` → **201** (server-trimmed to
    `""`, `:358`). **The fresh name is mandatory.** Revision 2 reused `n1`: role reconciliation
    would pass (`TrimSpace("   \n  ") == ""`, so the prompt-mismatch guard at `:503` is skipped),
    execution would reach `Agents().Create` with an existing name (`:365-372`), and the result
    is a **409 duplicate**, not the 201 the case asserts
  - `role show --json` for both → `kind == "interactive"`, `prompt` empty
  - `POST .../agents/{name}/terminal/session` (synchronous, so no polling needed here) →
    `launch.argv` has neither `--prompt` nor a prompt
    (`agentLaunchCommandArgs:393-395` needs a non-empty `PromptFile` to add the flag)
  - therefore `loadLeadRolePrompt` returns `""` and `GenerateTerminalPrompt("")` falls back to
    `GenerateLeadPrompt()` (`prompts.go:371-374`): the agent **runs as a Lead**. Assert this
    directly by reusing the CUS-D14 argv probe and matching the built-in lead prompt's
    signature text.
- **Edge rationale:** the entire guarantee that a "custom prompt" agent has a custom prompt
  lives in one React expression. Gap **P-2**.

### CUS-S2 — built-in interactive prompt list contract

- **Tier:** surface (the modal falls back to hard-coded defaults when this call fails, so the
  endpoint is partly UI-orphaned). Promotion condition: promote when the modal surfaces the
  degraded state distinguishably (today it only prints a generic hint,
  `CreateAgentModal.tsx:494-498`).
- **Intent:** The interactive-prompt registry the modal renders its cards from stays stable,
  and hidden prompts stay hidden.
- **Assertions:** `GET /api/workspaces/{ws}/interactive-prompts` → `prompts` is exactly
  `[{id: lead}, {id: pr-review}]`; `pr-review-checkout` is **absent**
  (`internal/domain/interactive_prompt.go:11-15`, filtered by `visibleInteractivePrompts`
  `handlers/agents/handlers.go:42-52`).
- **Edge rationale:** the Custom prompt card is rendered *after* this list
  (`CreateAgentModal.tsx:479-492`); a registry change reorders the card grid and would break
  every positional selector a future test might use.

### CUS-S3 — a custom agent named after a built-in prompt poisons that template

- **Tier:** surface (needs two creates in a specific order; the second failure is a *latent*
  consequence, not something the first operator observes). Promotion condition: promote once
  role naming is namespaced (**P-1**) and the scenario becomes an ordinary error path.
- **Intent:** The workspace's role namespace is shared between custom prompts and built-in
  templates, and the plan records what happens when they collide.
- **Preconditions (mandatory):** runs in its **own throwaway workspace
  `E2E-WS-CUSTOM-POISON`**, created and deleted by the surface suite's hooks, with a virgin
  `pr-review` role. **CUS-D19 is the logical control** — it proves the template works in a clean
  workspace — but it must **not** share this workspace, because its own agent leaves a
  `pr-review` role behind that would make this case's first step 400 instead of 201. The
  poisoned role cannot be cleaned up through the web UI, which is why the workspace is
  disposable.
- **Steps/assertions**
  - `api: POST .../agents` custom-prompt agent named **`pr-review`** with prompt P → 201; role
    `pr-review` now has `Prompt: P`, `PromptFile: ""`
  - `api: POST .../agents` the PR-Review template payload
    `{name: "reviewer-${RUN_ID}", role_name: "pr-review", kind: "interactive", prompt_file: "builtin:pr-review"}`
    (`CreateAgentModal.tsx:318-323`) → **400**, `already exists with a different prompt`
    (`reconcileExistingAgentRole:506-508`)
  - conclusion to assert in the comment: **the built-in PR Review template becomes
    un-creatable in that workspace, permanently, with no UI remedy** (no role delete in the
    web UI)
- **Edge rationale:** the highest-severity consequence found in this pass. It is a workspace-
  scoped denial of a product feature triggered by a legal user action. FINDINGS **P-1**.

---

## Part 2 — Real-backend tier (non-deterministic)

The existing real tiers all exercise the **epic-runner / worker** path. A custom-prompt agent
is an **interactive terminal** agent — `loom lead` under a PTY — which no real tier covers
today. `real-terminal-suites/` is the closest precedent (it spawns a real codex *task* agent
for the Logs tab) and supplies the tmux/PTY preflight.

**Recommended home:** `tests/aft/real-interactive-suites-<backend>/`, gated by
`AFT_REAL_BACKEND=<backend>`. Keeping it out of `real-suites-<backend>/` preserves the rule that
`make test-aft-real` discovers exactly the epic-runner scenario.

**These cases are NOT blocked on a seam — revision 2 over-blocked them.** `run-aft.sh` assigns
the real tier's suite path with `:=` (`: "${AFT_SUITES:=$SCRIPT_DIR/real-suites-$AFT_REAL_BACKEND}"`),
so an explicitly exported `AFT_SUITES` wins, and the runner then executes exactly that one path
(`AFT_SUITE_PATHS=("$AFT_SUITES")`). So today, with no framework change:

```
AFT_REAL_BACKEND=codex AFT_SUITES=tests/aft/real-interactive-suites-codex tests/aft/run-aft.sh --no-agent
```

runs the whole tier — correct stub farm, correct credential preflight, correct `AFT_TIMEOUT`
default. A `make test-aft-real-interactive` target is **productization** (discoverability, CI
hygiene, the README table row), tracked as **S-8**, not a prerequisite. R1/R2/R4a/R4b are
therefore **ready-to-write**; what gates them is operator credentials and rate-limit budget,
which is true of every real tier.

### CUS-R1 — real backend boots on the custom prompt and produces the instructed artifact

- **Intent:** A real agent CLI, launched as an operator's custom-prompt terminal teammate,
  follows the operator's inline instructions at boot and leaves an artifact that no built-in
  prompt would have produced.
- **Discriminator design (the HELLO.md analogue for an interactive agent).** The prompt is the
  *first positional argument* of the codex TUI / claude harness (`codex_runtime.go:145-153`,
  `harness_runtime.go:210-216`), so the agent acts on it **without any typed input** — that is
  what makes an interactive probe cheap. Prompt fixture:
  ```
  Immediately, before saying anything and without waiting for a user message, create a file
  named MARKER-${RUN_ID}.md in the current working directory whose entire contents are
  exactly the single line: AFT-CUSTOM-${RUN_ID}
  Do not create or modify any other file. Then stop and wait.
  ```
  The token `AFT-CUSTOM-${RUN_ID}` appears **only** in the custom prompt — a built-in Lead or
  PR-Review prompt cannot produce it. That is the real-vs-builtin discriminator, exactly as
  `HELLO.md` is the real-vs-stub discriminator in the epic-runner tiers.
- **Where the file lands (corrected in revision 2).** `launch.cwd` is empty for interactive
  agents (`agentLaunchCwd:339-347`), and `spawnSession` then uses the **PTY manager's** cwd:
  `cmd.Dir = m.cwd`, overridden only for a non-empty `launch.Cwd`
  (`internal/webui/terminal/pty_manager.go:349-367`). `m.cwd` is the **registered workspace
  path** handed to `NewPTYManager` (`multi_pty_manager.go:293`) — *not* `loom serve`'s process
  cwd. In the aft stack the two coincide only because `start-e2e-server.sh` `cd`s into
  `tmp/e2e-workspace` before launching serve and registers that same directory. So the test
  must resolve the target directory as: `launch.cwd` if non-empty, else the workspace's
  registered path read from the workspace API — never `pwd`, and never hard-coded.
  (If **P-4** is ever fixed, this becomes the agent worktree.)
- **Step sketch**
  1. `api: PATCH /api/workspaces/{ws}/config/backend {backend: <b>}`; `api: GET` it back
     (same first step as `zz-real-claude-epic`)
  2. create the custom agent **through the modal** (this tier should still exercise the real
     UI path — it is the only place the template is reachable)
  3. `open /ws/{ws}/agents/{name}` and wait for the terminal to mount (this is what fires the
     ensure-session POST and starts the PTY)
  4. `run:` + `python3` over `GET /terminal/tabs`, selecting the tab by `agent_id` (arrays are
     positional-only in `api:` asserts) → save `launch.cwd`, assert `pty_alive == true`.
     `pty_alive` is required **here** because this tier goes on to prove real CLI behavior;
     deterministic CUS-D5 proves only controlled bootstrap and connection (**S-7**)
  5. chained `run:` grace polls (≤55 iterations each, ≤120 s per step, as the real tiers do)
     for `MARKER-${RUN_ID}.md` on disk
  6. `run:` assert the file's contents are exactly `AFT-CUSTOM-${RUN_ID}\n`
  7. `wait.fn` on `[data-testid=terminal-wrapper] .term-row` rendering non-empty output
     (mirrors `real-terminal-suites`), proving the human-visible half
- **Teardown:** kill the tab (`DELETE /terminal/tabs/{session}`), delete the agent, remove the
  marker, plus `scripts/real-backend-teardown.sh <backend>`.

### CUS-R2 — prompt fidelity: the agent behaves as the custom role, not the built-in Lead

- **Intent:** The custom instructions *replace* the built-in lead persona rather than being
  appended to it.
- **Probe:** a second prompt that asks for a self-description artifact:
  `write ROLE-${RUN_ID}.txt containing exactly the word RELEASE-NOTES-REVIEWER and nothing else`.
  Then assert the artifact **and** assert the transcript/scrollback does not contain the
  built-in lead prompt's signature phrases (`GenerateLeadPrompt`, `prompts.go:365-369` →
  `prompts/lead.md`) — pick one stable phrase from that template at authoring time.
- **Also assert the guardrails survived:** the scrollback/transcript should still show the
  safety-rules block, since `GenerateTerminalPromptText` appends it unconditionally
  (`prompts.go:402`). Custom prompt **replaces the persona, keeps the guardrails** — that is
  the contract worth pinning end to end.

### CUS-R3 — per-backend matrix

| backend | runtime path | recommendation |
|---|---|---|
| `codex` | `RunCodexLeadRuntime` — app-server + TUI, prompt positional (`codex_runtime.go:152`) | **required first** — cheapest, matches `make test-aft-real` preflight |
| `claude` | `runHarnessLead` — harness-wrapper PTY, prompt positional (`harness_runtime.go:212-213`), args `--session-id <uuid> --dangerously-skip-permissions` (`harness_lead_runtime.go:93-100`) | **required for parity** — it is the *other* runtime; codex-only coverage leaves half the dispatch untested |
| `opencode` | `opencode run --dir <wd> --dangerously-skip-permissions … <prompt>` (`:103-105`) | defer — `run` is one-shot, so the "interactive teammate" semantics differ enough to need its own scenario design |
| `cursor` | `cursor-agent --force <prompt>` (`:106-108`) | defer — same reasoning, plus login preflight |
| `gemini` | `gemini --approval-mode=yolo <prompt>` (`:101-102`) | defer — no existing real tier |

Recommendation: **codex + claude parity, opencode/cursor/gemini explicitly out of scope**,
documented in the suite header the way `run-aft.sh`'s table documents the epic-runner tiers.

### CUS-R4 — failure variants

- **R4a unavailable backend (redesigned in revision 2).** The original "unset the credentials"
  design was impossible: `run-aft.sh`'s real-tier preflight **hard-exits** before aft starts
  when `~/.codex/auth.json` (or claude's `.credentials.json`, or `cursor-agent status`) is
  missing, so a credential-less real run never reaches a suite. Instead, exercise the
  *unavailable backend* branch, which is the same product code path and needs no credentials:
  create the custom agent with `backend: "gemini"` — registered
  (`backend_gemini.go:144`) but **stubbed in no farm** (`e2e/stubs`, `e2e/stubs-real-*`) — then
  open its terminal. `GeminiBackend.HealthCheck` is a `exec.LookPath("gemini")` probe
  (`backend_gemini.go:117-126`); when it fails, `runLead` prints
  `Error: gemini backend is not installed` and `execShell`s (`lead.go:97-103`).
  **Assert:** the launch spec carries `'--backend' 'gemini'`, the terminal mounts, and the
  rendered rows contain that install error followed by a usable shell prompt — Loom degrades
  visibly rather than showing a blank tab.
  **Guard (mandatory):** the case is only valid when `gemini` is absent from the server's
  PATH. Precede it with a `run:` step that skips-or-fails explicitly on
  `command -v gemini` — otherwise a host with gemini installed would invoke the operator's
  real CLI with the test's prompt. Same guard belongs anywhere else a non-stubbed backend is
  selected. **S-7** proposes a deliberately-unhealthy stub so this can move to the
  deterministic tier and stop depending on host state.
- **R4b maximum-length prompt actually launches (ARG_MAX).** Create a custom agent with a
  100 000-byte prompt (the fleet-db cap) and start it. The prompt becomes **one** positional
  argv element, so Linux's `MAX_ARG_STRLEN` (131 072 B) is the real ceiling, not `ARG_MAX`.
  Budget: 100 000 (prompt) + ~1 500 (`buildSafetyGuardrailsBlock`) + the optional backend
  assignment block (`lead.go:200-206`) + the optional `--message` block. Assert the agent
  boots and the marker is produced. **The current cap leaves roughly 29 KB of headroom on
  Linux and no explicit invariant guarding it** — see **P-3**. This case is the guard.
- **R4c prompt over the cap** is already deterministic (CUS-D7b); do not spend a real run on it.

---

## Part 3 — Blockers & new seams needed

Numbering follows `FINDINGS.md` conventions: **P-n** are candidate §1 product entries,
**S-n** are candidate §3 stack/test-framework entries.

### P-1 — Custom prompts share the workspace role namespace with built-in templates

**Severity:** HIGH · **Fix:** loom / product-decision · **Status:** OPEN (candidate §1.21)

`role_name = trimmedName` (`CreateAgentModal.tsx:313`) means a custom-prompt agent named
`pr-review` claims the built-in `pr-review` role and permanently blocks the PR Review template
in that workspace (CUS-S3). Names `task`/`plan`/`lead` are blocked with a message that offers
only "choose a different agent name" (CUS-D8a–c). `orchestrator` **is** unguarded and
deterministically succeeds — a custom-prompt agent claims the legacy first-class interactive
role name and even inherits its `"Lead/orchestrator interactive"` description
(CUS-D8d, verified against `workspace_store.go:478-506` + `agent_service.go:458-478`).
And `DeleteAgent` (`agent_service.go:594-604`) leaves the role behind, so recreating a deleted
custom agent with new instructions 400s (CUS-D13) with no UI remedy — the web UI has no role
delete and no `/roles` route at all.

Concrete fixes to evaluate: namespace custom roles (`custom:<name>` or `agent:<name>`),
reserve the built-in ids at the modal's name validator, and/or delete an
auto-created custom role when its sole agent is deleted.

### P-2 — Server accepts an interactive agent with an empty prompt (silent Lead fallback)

**Severity:** MED · **Fix:** loom · **Status:** OPEN (candidate §1.22)

`validateAgentCreateInput` (`agent_service.go:616-631`) validates name, role_name, and kind —
never the prompt. `{kind: "interactive", prompt: ""}` creates an interactive role with no
prompt; at boot `loadLeadRolePrompt` returns `""` and `GenerateTerminalPrompt("")` falls back
to the built-in Lead (`prompts.go:371-374`). The only guard is `hasPromptSelection` in React.
**Spec:** reject `kind == "interactive"` with both `prompt` and `prompt_file` empty **unless**
`role_name` resolves to a first-class interactive role (`lead`/`orchestrator`) — that
exception is required so the existing Lead template payload keeps working. Pinned by CUS-S1.

### P-3 — Three uncoordinated ceilings on one field

**Severity:** MED · **Fix:** loom + fleet-db · **Status:** OPEN (candidate §1.23)

100 000 B (fleet-db `MaxRolePromptBytes`), 1 MB (`handler.ReadJSON`), and ~131 072 B
(Linux `MAX_ARG_STRLEN`, because the prompt is a positional argv element). Only the first two
are enforced; the third is satisfied today only by luck, and the appended guardrails +
assignment context eat into it. **Spec:** assert the invariant in code —
`MaxRolePromptBytes + len(guardrails) + maxAssignmentBlock < 131072` — as a Go test, and give
the 400 a message the modal renders next to the textarea rather than in the generic
`create-agent-error` banner. Pinned by CUS-D7b and CUS-R4b.

### P-4 — Custom interactive agents get repo chips but no worktree and no cwd

**Severity:** LOW-MED · **Fix:** product-decision · **Status:** OPEN (candidate §1.24)

The modal offers repo chips (`CreateAgentModal.tsx:565-600`) and persists `repos`/`cross_repo`,
but `ensureLocalAgentWorktrees` returns early for interactive roles
(`agent_service.go:389-395`), so `agentLaunchCwd` yields `""` (`:339-347`). The PTY then runs
in the **per-workspace PTY manager's cwd** — `cmd.Dir = m.cwd`, overridden only by a non-empty
`launch.Cwd` (`internal/webui/terminal/pty_manager.go:349-367`) — where `m.cwd` is the
registered workspace path (`multi_pty_manager.go:293`). *(Revision 1 said "loom serve's cwd";
that is wrong, and only looks right in the aft stack because `start-e2e-server.sh` `cd`s into
the same directory it registers.)* Net effect: the operator's repo choice has no runtime
effect, and every interactive agent in a workspace shares one working directory instead of an
isolated worktree. Pinned by CUS-D10/CUS-D11; it also forces CUS-R1 to resolve its marker path
at runtime.

### P-5 — The custom prompt is write-only from the web UI

**Severity:** MED · **Fix:** loom · **Status:** OPEN (candidate §1.25)

No `/roles` route exists in `internal/webui`; `WorkspaceAgentInfo`
(`api/workspace/workspace.ts:33-40`) and `domain.Agent` (`internal/domain/agent.go:42-78`)
both omit `prompt`; `AgentUpdateInput` (`service/agent.go:95-107`) has no `Prompt` field; the
agent detail has no prompt tab (`views/AgentEditorGroups.tsx:33-40`). An operator cannot read
back, review, or revise what they told their agent — only `loom role show/set` can.
**Spec:** a read-only `GET /api/workspaces/{ws}/agents/{name}/prompt` (or `role_prompt` on the
monitor status payload) would let CUS-D1/D4/D5/D9 drop the CLI shell-out entirely and would
make CUS-D6's XSS assertions non-vacuous. This is the single highest-leverage seam in the plan.

### S-1 — Stub argv/stdin observation (needed to observe the spawned process contract)

**Severity:** stack · **Status:** PARTIAL — codex pilot implemented for CUS-D5

`e2e/stubs/codex` now appends an atomic, locked NUL-delimited record
`pid \0 argv0 \0 arg1 \0 … \0\0` to `$STUB_ARGV_LOG` when set. The focused Go test uses that
record to pin the exact app-server and remote argv without newline ambiguity. In the AFT pilot,
the remote stub reports a SHA-256 fingerprint measured from its actual final prompt argument and
the number of appended safety blocks. A same-origin browser readback pins the owned tab and its
live PTY. Those browser-visible contracts let all final UI checks live inside one grouped
terminal-state card instead of adding runner-only cards with duplicate stills.

These seams make the Codex controlled-runtime argv observable for the CUS-D5 pilot.
CUS-D14 still covers the non-interactive fallback (`backend_codex.go:63-79,108-116`) via its
per-test stub, and harness-wrapper prompt delivery (`harness_runtime.go:210-216`) remains
outside this pilot. Extending the shared capture contract to the other stubs remains open.

### S-2 — `seed-session` (ADR-0001 family)

**Severity:** stack · **Status:** already the named next candidate (`FINDINGS.md:381-388`)

Not strictly required by this plan, but it would let a custom-prompt scenario assert a
recorded orchestration session for the agent
(`createLeadOrchestratorSession`, `terminal/agent_session.go:238-256`) without spawning a PTY —
turning CUS-R1's session assertions into deterministic ones.

### S-3 — Missing/indirect testids in `CreateAgentModal`

**Severity:** stack (cheap) · **Status:** OPEN

Add a `data-testid` on the template-card **container** (e.g. `create-agent-template-list`) and
on the custom-prompt field group, and — more importantly — make the card testids
census-visible (**S-6**). Also worth a stable testid on the interactive-prompt group so a test
can assert card *ordering* without positional selectors.

### S-4 — No file-backed text entry or request body in aft

**Severity:** stack · **Status:** OPEN

Two symmetric gaps, both confirmed in the runner source:
- `fill` takes a literal `value` and passes it as one `agent-browser` argv element through
  `execFile` (`testing-app/src/steps.ts:222-225`, `src/browser.ts:35-43`). There is **no
  `valueFile`**, so large or fixture-file-backed input must go through `run:` +
  `agent-browser --session "$AFT_SESSION" find testid … fill "$(cat …)"` — which works
  (`AFT_SESSION` is exported at `src/steps.ts:168-181`) but bypasses aft's step reporting,
  retry, and healing.
- `api.body` is object-or-string with no `bodyFile` (`testing-app/src/types.ts:144-151`,
  `src/api-step.ts:107-115`), so large payloads need `run:` + `curl --data-binary @file`.

**Spec:** `fill: { …, valueFile: <path> }` and `api: { …, bodyFile: <path> }`, both resolved
relative to `$AFT_WORK_DIR`. That would let CUS-D4/D7a use first-class steps and would promote
CUS-D7b's boundary probe from surface to product-correctness.

### S-5 — Role rows are not cleaned up by any teardown script

**Severity:** stack · **Status:** OPEN

`scripts/close-open-issues.sh` handles issues; agents are deleted per-suite with
`DELETE /agents/{name}`. Nothing removes roles, and there is no HTTP route to do so
(P-1/P-5). **Spec:** extend the suite teardown to shell out to
`$AFT_LOOM_BIN --workspace <WS> role remove <name>` (`role_cmd.go:226-236`) for each
`*-${RUN_ID}` role, or simply delete the whole workspace — which this plan's suite does, and
which is the reason it must own `E2E-WS-CUSTOM` rather than borrow `E2E-WS`.

### S-6 — Census blind spot: indirect `testId={CONST.testId}` props

**Severity:** stack · **Status:** OPEN

`scripts/gen-census.py` collects `data-testid=` literals and `testId=` props, so
`create-agent-template-custom-prompt` — passed as `testId={CUSTOM_PROMPT_TEMPLATE.testId}`
(`CreateAgentModal.tsx:492`) — is absent from the census, as are the lead/task/planner card
ids. Coverage of the card therefore never shows up in the join. **Spec:** teach the census to
resolve `testId={IDENT.testId}` and `testId={\`…${x}\`}` against module-level const objects,
or (cheaper) inline the literal `data-testid` onto `AgentTemplateCard`'s root in addition to
the prop.

### S-7 — Deterministic controlled-Codex bootstrap; unhealthy backend still missing

**Severity:** stack · **Status:** PARTIAL — bootstrap pilot implemented

Controlled codex starts `codex app-server --listen <endpoint>` and then connects to it
(`internal/leadcontrol/codex_runtime.go:107-135`). The deterministic Codex stub now delegates
that command to a bootstrap-only WebSocket helper: it accepts `initialize`, returns an empty
`thread/list`, and rejects unsupported methods. Its `--remote` mode makes its own WebSocket
connection, completes `initialize`, emits an agent-specific connection marker, and then keeps
both that socket and stdin open. CUS-D5 can therefore assert bootstrap, a connected remote
process, an owned live PTY, and prompt argv without claiming model behavior. The helper
intentionally does not create a thread or implement `thread/read`, `turn/start`, or message
delivery.

No stub yet reports *unhealthy*, so the backend-unavailable branch (`lead.go:97-103`) is still
reachable only by relying on a binary being absent from the host (see CUS-R4a's guard).

**Remaining spec:** add an `e2e/stubs/unhealthy-<name>` farm entry (or
`STUB_*_HEALTH=missing`) so the unavailable-backend case becomes host-independent. Promote
additional cases only when their assertions match the bootstrap-only boundary above.

### S-8 — Productize the real interactive tier (not a blocker)

**Severity:** stack (low) · **Status:** OPEN

The tier is runnable today via `AFT_REAL_BACKEND=<b> AFT_SUITES=<dir> tests/aft/run-aft.sh`
because `run-aft.sh` sets the real-tier suite path with `:=` and then runs exactly
`AFT_SUITE_PATHS=("$AFT_SUITES")`. What is missing is only ergonomics: a
`make test-aft-real-interactive[-<backend>]` target, a row in the README's real-tier table
alongside the four epic-runner targets, and a teardown script that kills leftover agent terminal
tabs (`DELETE /terminal/tabs/{session}`) in addition to what
`scripts/real-backend-teardown.sh` already does. Recorded so the plan does not mislabel
convenience work as a framework seam — revision 2 did exactly that.

---

## Coverage table

`Mech` = the aft mechanism the case's readbacks require: **step** = ordinary
`open`/`click`/`fill`/`expect`/`api:` steps only; **run** = needs a `run:` step because the
readback filters an array, polls, compares bytes, or fills from a file.
`WS` = which workspace the case runs in (**C** = `E2E-WS-CUSTOM`, **CTL** =
`E2E-WS-CUSTOM-CTL`, **P** = `E2E-WS-CUSTOM-POISON`, **E** = `E2E-WS` real tier).
`File` = **zz** = `suites/zz-custom-prompt.test.yaml`, **sf** =
`surface-suites/custom-prompt-contracts.test.yaml`, **ri** =
`real-interactive-suites-<backend>/`.

| Case | Kind | Tier | Mech | WS | File | Status |
|---|---|---|---|---|---|---|
| CUS-D1 create + kind/prompt readback | happy | product-correctness | run | C | zz | ready-to-write |
| CUS-D2 empty prompt → submit disabled | edge | product-correctness | step | C | zz | ready-to-write |
| CUS-D3 whitespace-only prompt → submit disabled | edge | product-correctness | step | C | zz | ready-to-write |
| CUS-D4 multiline + metacharacters byte-exact | edge | product-correctness | run (fill + compare) | C | zz | ready-to-write (fixture must have **no** terminal newline) |
| CUS-D5 `{{ .AgentName }}` not expanded | edge | product-correctness | run | C | zz | implemented pilot; owns `cus-d5.prompt`; proves controlled bootstrap + remote connection + prompt-contract fingerprint |
| CUS-D6 XSS-shaped prompt inert in agents UI | edge | product-correctness | step | C | zz | ready-to-write — **negative-rendering fence only, vacuous until P-5; not sanitizer coverage** |
| CUS-D7a 32 KB prompt via the UI | edge | product-correctness | run (fill + compare) | C | zz | ready-to-write |
| CUS-D7b 100 000 / 100 001 / >1 MB boundaries | edge | surface | run (curl `--data-binary`) | C | sf | ready-to-write; UI variant needs **S-4** |
| CUS-D8a `task` collision → 400 | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D8b `plan` collision → 400 | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D8c `lead` collision → 400 | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D8d `orchestrator` → **201, pinned** | edge | product-correctness | run | C | zz | ready-to-write (was an unpinned probe; now deterministic, sharpens **P-1**) |
| CUS-D9 whitespace trimming semantics | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D10 backend select → agent row + launch spec | happy | product-correctness | run (**poll tabs**) | C | zz | ready-to-write; deliberately launch-spec only (CUS-D5 owns controlled Codex runtime health) |
| CUS-D11 repo scoping + empty cwd + no worktree | happy/edge | product-correctness | run (**poll tabs**) | C | zz | ready-to-write (path = `<ws>/worktrees/<repo>/<agent>`) |
| CUS-D12 prompt not visible/editable after create | edge | product-correctness | step + run | C | zz | ready-to-write (selector excludes `agent-editor-split`) |
| CUS-D13 recreate-after-delete blocked by orphan role | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D14 prompt reaches backend argv (**non-interactive path only**) | happy | surface | run (per-test stub) | C | **zz** | retained for fallback coverage; controlled Codex argv is independently pinned by the CUS-D5 pilot |
| CUS-D15 new agent appears via SSE | happy | product-correctness | step | C | zz | ready-to-write (name-exact `aria-label`, was vacuous) |
| CUS-D16 duplicate name → 409 in error alert | edge | product-correctness | run | C | zz | ready-to-write |
| CUS-D17a UI normalizes name **and** role_name | edge | product-correctness | run (**poll tabs**) | C | zz | ready-to-write (client-side invariant) |
| CUS-D17b mixed-case `role_name` via API → 400 | edge | surface | run | C | sf | ready-to-write (**new**; server does not normalize `role_name`) |
| CUS-D18 invalid / XSS-shaped names gated | edge | product-correctness | step | C | zz | ready-to-write |
| CUS-D19 built-in PR Review baseline (control for S3) | happy | product-correctness | run (**poll tabs**) | **CTL** | zz | ready-to-write — own clean workspace, **no ordering dependency** |
| CUS-D20 `prompt` + `prompt_file` precedence | edge | surface | run | C | sf | ready-to-write (argv half reuses D14's probe, so runs in `zz`) |
| CUS-S1 API accepts empty interactive prompt | edge | surface | run | C | sf | ready-to-write (**second probe needs a fresh name**); pins **P-2** |
| CUS-S2 interactive-prompt registry contract | happy | surface | step | C | sf | ready-to-write |
| CUS-S3 custom name poisons a built-in template | edge | surface | run | **P** | sf | ready-to-write — own throwaway workspace; pins **P-1** |
| CUS-R1 real backend produces the instructed marker | happy | real (`codex`, then `claude`) | run | E | ri | **ready-to-write (opt-in)** — runnable today via `AFT_REAL_BACKEND` + `AFT_SUITES` |
| CUS-R2 prompt fidelity vs built-in Lead persona | happy | real (`codex`, then `claude`) | run | E | ri | ready-to-write (opt-in) |
| CUS-R3 per-backend matrix (codex+claude; others out of scope) | — | real | — | — | — | recommendation only |
| CUS-R4a unavailable-backend degradation | edge | real (`codex`) | run | E | ri | ready-to-write (opt-in) **+ mandatory `command -v gemini` guard**; **S-7** removes the guard |
| CUS-R4b max-length prompt launches (MAX_ARG_STRLEN) | edge | real (`codex`) | run | E | ri | ready-to-write (opt-in); guards **P-3** |

**Totals (revision 3, final).**

**28 deterministic cases** — up one from revision 2 (CUS-D17 split into D17a/D17b):
**21 product-correctness** (D1–D6, D7a, D8a–d, D9–D13, D15, D16, D17a, D18, D19) +
**7 surface** (D7b, D14, D17b, D20, S1, S2, S3). All 28 are defined in the current AFT corpus
against one `loom serve` stack. Three carry explicit scope limits rather than clean coverage —
CUS-D6 is vacuous until **P-5**, CUS-D7b's UI variant needs **S-4**, and CUS-D14 deliberately
covers only the non-interactive fallback while CUS-D5 owns the controlled Codex pilot — and
none has an unpinned expected outcome any more (CUS-D8d is now pinned to 201).

**22 of the 28 need a `run:` step**; 6 are pure step-vocabulary cases (D2, D3, D6, D15, D18, S2).
**Five of the `run:` cases must poll** rather than read once (D10, D11, D17a, D19, and D20's
launch half), because ensure-session is asynchronous.

**Three workspaces**: `E2E-WS-CUSTOM` (25 cases), `E2E-WS-CUSTOM-CTL` (D19),
`E2E-WS-CUSTOM-POISON` (S3). **Two files** plus the real tier: `zz-custom-prompt.test.yaml`
carries every case that needs the shared browser session or D5's fixture — including CUS-D14,
which revision 2 wrongly put in the surface file.

**4 real-tier cases** (R1, R2, R4a, R4b) + one recommendation (R3), all **ready-to-write as an
opt-in tier** — reclassified from "blocked-on-seam", because `run:`-level gating already exists
(`AFT_REAL_BACKEND=<b> AFT_SUITES=<dir> tests/aft/run-aft.sh`). They are gated by operator
credentials and rate-limit budget, not by missing framework support.

**5 candidate product findings** (**P-1**…**P-5**) and **8 candidate stack/seam items**
(**S-1**…**S-8**). The CUS-D5 pilot now proves deterministic controlled-Codex bootstrap and
prompt delivery. Generalizing argv capture to other backends, modeling Codex threads/turns,
and deterministic unavailable-backend coverage remain intentionally separate work.
