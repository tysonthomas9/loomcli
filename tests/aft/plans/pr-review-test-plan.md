# PR Review agent template — aft test plan

Scope: the **PR Review** interactive agent template offered by `CreateAgentModal`
(built-in prompt `pr-review`) **and** its hidden sibling `pr-review-checkout`, which is
what the PR review workspace's "Discuss PR" flow stands up. One plan, because the two
prompts are the same runtime mechanism (`loom lead --prompt builtin:<id>` in a PTY) with
two different entry points, and the interesting bugs live in the seam between them.

Nothing here is written yet. Case ids `PRR-D*` (deterministic) and `PRR-R*` (real backend)
are stable handles; the coverage table at the bottom is the ledger.

---

## Revision 3 — final (second codex round folded in)

Codex re-vetted revision 2 and **conceded all three disputed items** (`expect.attr` is real, the
PRR-D20 upgrade is correct, the `~` probe is sound hardening). Nine new findings; all nine
verified in code and accepted, four of them changing a case's status. Per the convergence rule,
anything still uncertain is marked blocked rather than ready.

**Two hermeticity gates now precede everything.** This is the round's biggest structural change.
Revision 2 scheduled the 20 no-seam cases first; that was unsafe.

- **Gate G1 — `LOOM_WEBUI_GITHUB_TOKEN` unset** (S2). An inherited token doesn't just weaken the
  degraded cases, it inverts them: `resolveGitHubToken` returns the env token before reading
  settings (`seed.go:127-129`), the connector then dispatches to the **default**
  `https://api.github.com` (`registry_default.go:34`, `providers/github.go:53`), and
  `503 egress_unavailable` becomes `502 upstream_error`. D8a–D8d and D10b assert the 503, so they
  fail — and they fail by sending the operator's real PAT to real GitHub. Nothing that touches a
  `pull-requests` route may be written before G1 lands.
- **Gate G2 — a deterministic `gh`** (new seam S8). The stack is **not** gh-less, which revision 2
  assumed. `run-aft.sh:292-296` prepends `e2e/stubs` but keeps the host `$PATH` tail, there is no
  `gh` stub (`e2e/stubs/` holds claude, codex, cursor-agent, opencode), and `gh` is present on a
  typical dev machine (`/opt/homebrew/bin/gh` here). So `ghListFallback` → `CheckGhInstalled`
  (`svcimpl/agent_service.go:266`, `internal/cli/git/pr.go:347-353`) → real
  `gh pr list --state … --json …` against `acme/widgets` (`internal/cli/git/pr_list.go:42-49`).
  The observable result differs by machine: CI (no gh) yields the "gh CLI not installed" warning;
  a dev box makes a live API call for a repo that does not exist. **This is a pre-existing hole in
  `suites/review-queue.test.yaml` and `suites/pr-workspace-degraded.test.yaml`, not something this
  plan introduces** — worth fixing on its own merits.

**Accepted — status changes**

1. **Build order** (finding N1). Groups A/B/D no longer lead. New order in the closing section:
   G1 → G2 → template cases → degraded cases → S1 → the rest.
2. **PRR-D16 tested behavior that does not exist** (N3, high). The UI's Approve calls
   `applyReviewDecision` → `POST /issues/{id}/review-decision`
   (`PRReviewWorkspace.tsx:357-363`, `api/issues/issues.ts:514-520`), and that service
   **deliberately refuses GitHub-linked issues**: `if isGitHubPullRequestRef(issue.ExternalRef) {
   return nil, ErrUnavailable("GitHub review execution is not configured; Loom state was not
   changed") }` (`internal/webui/service/review_decision.go:63-65`, matcher at `:151-154`
   — `https://github.com/…/pull/…`, exactly this plan's fixture `external_ref`). No UI path
   reaches `POST /pull-requests/{o}/{r}/{n}/review` at all. Split into **PRR-D16a** (surface probe
   of the connector route directly, blocked on S1+S2) and **PRR-D16b** (pin today's honest
   refusal through the UI — ready after G1, and the more valuable of the two).
3. **PRR-D22 misstated UI causality** (N4). `PRsPage` hardcodes `usePullRequests({ state: "all" })`
   (`views/PRsPage.tsx:208-210`) and filters rows locally from `useState<PRFilter>("all")` (`:211`),
   so a filter-pill click **never** changes the server query. `state=review` and `state=merged` are
   therefore API-only paths the UI cannot reach. Also `url.Values.Encode()` sorts keys, so the
   wire query is `page=1&per_page=100&state=open` — not the order revision 2 asserted
   (`providers/github.go:286-307, 495-499`). Rewritten as API-surface query coverage plus separate
   local-filter UI assertions.
4. **PRR-D8e blocked on S8** (N2). Its whole assertion is the warning text, which is exactly what
   varies by machine. Moved from ready to blocked.
5. **PRR-D21 status gains S2** (N9-adjacent, original finding #9 partial). Forcing an upstream
   status only happens *after* `ensureConnectorAndGrants` succeeds (`seed.go:36-49`), which needs a
   configured credential. Status corrected to S1+S2.
6. **PRR-D18's assertion was vacuous** (N8). "No stale runtime metadata" was asserted as an absent
   terminal tab, which is trivially true if no tab ever launched. Now requires seeded pre-migration
   metadata and an observable post-migration provider — status gains S3.

**Accepted — mechanics that were not executable**

7. **`${var:…}` interpolates only inside `api:` steps** (N6). `interpolateApiValue` is called
   solely from `executeApiStep` (`../testing-app/src/api-step.ts:95`); the runner passes `vars` to
   the api branch and **not** to `executeStep` (`src/runner.ts:518-524`). So PRR-D7's
   `expect: { value: … equals: "review-<id>" } }` cannot work, and there is no lowercase transform.
   Rewritten around the fact that `save:` **mirrors each value to `$AFT_WORK_DIR/<name>`**
   (`api-step.ts:79-83`) — the same file-passing `pr-contracts`' teardown already relies on — so a
   `run:` step can read the id and compare it against the live input value via
   `agent-browser … eval`.
8. **`api:` has no negative `contains`** (N7). `ApiAssertSchema` requires exactly one of
   `exists`/`equals`/`contains` (`types.ts:128-137`); there is no negation. PRR-D3's
   "serialized body does not contain `pr-review-checkout`" is replaced by pinning both ids and
   `prompts.2 exists: false`, which excludes a third prompt by construction.
9. **PRR-D14's Settings round trip destroys the state it tests** (N5). `discussOpen` is
   `PRReviewWorkspace` component state (`:153`), so routing to Settings unmounts the panel;
   returning resets it to closed, and re-opening runs a *fresh* ensure
   (`usePRReviewConversation.ts:127-177`) that now succeeds — the Retry button is never involved.
   aft has no tab/window action (`actionKeys`, `types.ts:157-162`). Redesigned to install the
   credential over the API while the panel stays mounted, with the actor-fidelity caveat stated.
10. **Group count fixed** (N9): Groups A+B+D hold **20** ready cases, not 22 (PRR-D7 is Group C).

Case count moves from 41 to **42**: PRR-D16 splits into D16a + D16b, and PRR-D8e moves from ready
to blocked. Final split: **21 ready-to-write, 13 blocked, 8 real** — counted off the table.

---

## Revision 2 — codex-vetted

Revision 1 was reviewed read-only by OpenAI Codex against the same checkout, plus four
parallel reviews of adjacent plans. Every finding was re-verified against the code before
being folded in; two were rejected with evidence. What changed:

**Accepted — corrections**

1. **`GET /api/workspaces/{ws}/agents/{name}` does not exist.** `internal/webui/handlers/agents/module.go:24-35`
   registers only `GET /agents` (list), `POST /agents`, `PATCH|DELETE /agents/{name}`,
   `GET /agents/{name}/queue`, and the stop/start/restart/yield controls. A `GET` on
   `/agents/{name}` matches a registered path with no `GET` method → 405, not 404. Every
   agent readback in this plan now goes through `GET /agents` (envelope
   `{success, data:[…], total}`, `internal/webui/server/dto/common.go:22-40`) and filters in a
   `run:` step. Affected: PRR-D1, D6a, D7, D13, D18, D19.
2. **PRR-D20 could not prove what it claimed.** `POST /terminal/session` only builds the
   launch spec (`agent_session.go:315-336`); the interactive branch appends
   `--prompt <promptFile>` with **no** validation (`:389-398`), so an unknown builtin returns
   200. The rejection happens later, inside `loom lead`
   (`internal/cli/agent/prompts.go:376-381`), and — better than codex noticed — it is
   **user-visible**: `lead.go:118-124` prints `Error loading terminal prompt: …` to stderr and
   then `execShell`s, so the PTY stays alive showing the error. Redesigned around that.
3. **PRR-D5 must prove controlled bootstrap, not just a mounted grid.** The deterministic
   Codex stub now implements both sides of the controlled runtime: `app-server --listen`
   accepts the loopback WebSocket initialization and `--remote` reports an agent-specific
   connection plus a hashed prompt contract while remaining interactive. PRR-D5 groups that
   visible connection with the exact live `/terminal/tabs` record, launch selector, role/name
   attribution, and terminal wrapper. A launch spec or mounted grid alone is not success.
4. **`LOOM_WEBUI_GITHUB_TOKEN` is a hard prerequisite, not cleanup.** `resolveGitHubToken`
   returns the env var **before** consulting saved local settings
   (`internal/webui/handlers/prreview/seed.go:127-129`), and the non-real branch of
   `run-aft.sh:292-296` has no `env` prefix at all — it inherits the operator's shell
   wholesale. So an exported token both un-degrades the degraded suites *and* shadows the
   fixture credential, pointing the connected suite at real GitHub with a real token. Promoted
   to a blocking item in S2.
5. **PRR-D17 must use a stubbed provider; PRR-R3 needs new harness plumbing.** `run-aft.sh:157-159`
   rejects any `AFT_REAL_BACKEND` outside `codex|claude|opencode|cursor`, and there is **no
   `gemini` stub** in `e2e/stubs/` or in any `e2e/stubs-real-*/` farm (verified by listing all
   five). `gemini` *is* a real controlled lead backend
   (`harness_lead_runtime.go:101-102`) with no launch-pinned session id, which is exactly why
   the reviewer reports `unsupported` (`harness_read.go:81-84`) — the premise is sound, the
   plumbing is missing. PRR-D17 switches to `opencode` (stubbed, and also reader-less →
   `harness_read.go:66-69`); PRR-R3 is gated on adding the arm + stub farms.
6. **Settings save copy.** The success toast is `"Runtime credentials saved"`
   (`SettingsView.tsx:351`), not `"Credential saved."` — the latter is the persistent
   configured-state helper text at `:700`. Both are now used, the durable one as the wait target.
7. **PRR-D9d's invalid-path example** used a raw space (`wid gets`), which Node's WHATWG URL
   parser percent-encodes before the handler ever sees it. Swapped for a character that is
   legal in a URL path but outside `ownerRepoSegmentRE` (`membership.go:13`).

**Accepted — new cases**

8. PRR-D21 — `rate_limited` (429) and `upstream_error` (502) response classes from the fake
   upstream (`errors.go:41-49`, `providers/github.go:539-568`).
9. PRR-D22 — PR-list query params, `state` normalization, and the `merged` connector bypass
   (`list.go:45-48, 118-145, 184-189, 243-259`).
10. PRR-D10b — case-insensitive repo membership canonicalization (`membership.go:77-92`,
    `handlers.go:151-167`). **Ready to write today** — no seam needed, because a
    casing-mismatched request that *passes* membership fails later with `503 egress_unavailable`
    rather than `404 repo_not_registered`, and those two codes are already distinguishable on
    the degraded stack.

**Rejected**

11. **"`expect.attr` is pseudo-syntax" — wrong.** It is a first-class aft assertion:
    `AttrExpectSchema = { selector|testid, name, equals }` at `../testing-app/src/types.ts:86-88`,
    wired into `ExpectSchema` at `:105`, implemented at `src/steps.ts:83, 333`, and **already used
    in passing suites in this repo** at `tests/aft/suites/zz-agent-flow.test.yaml:224` and
    `tests/aft/suites/issue-detail.test.yaml:132`. `ExpectSchema` (`:97-112`) also carries
    `value` (`:90`), `enabled` and `checked` (both `BoolStateExpectSchema`, `:92-95`). All kept,
    and tightened to the exact schema shapes in the Part 1 conventions block.
    The genuinely-absent features are different ones, and are listed there: no `fill` `valueFile`
    (`ValuedLocatorSchema`, `:45`), no `api:` body-from-file (`:144-153`), no array-filtering
    `api:` asserts (finding 12).
12. **"aft `api:` asserts cannot touch arrays" — narrowed, not accepted.** `lookup`
    (`../testing-app/src/api-step.ts:70-77`) splits the path on `.` and uses
    `hasOwnProperty`, so a numeric segment **does** index an array (`data.0.role_name`
    resolves). What it cannot do is *filter* by predicate, and `contains` is a string
    operation (`types.ts:133`). So: numeric indexing is legal when cardinality is separately
    pinned (`total`), and `run:` + `python3` is required whenever order or multiplicity is not
    pinned — which, for the agent list, it is not.

**Also folded from the cross-cutting reviews**

- There is no HTTP roles API (already stated in revision 1; re-verified — no `/roles` route
  exists anywhere under `internal/webui/`).
- aft has no body-from-file or value-from-file: `ApiSchema.body` is object-or-string only
  (`types.ts:144-153`). This plan never needed either; noted so a writer does not reach for them.
- Real-tier credential cases must not disturb `run-aft.sh`'s own preflight. Verified that the
  preflight checks only backend credentials (`~/.codex/auth.json`,
  `${CLAUDE_CONFIG_DIR}/.credentials.json`, `cursor-agent status` — `run-aft.sh:180-199`) and
  never GitHub, so PRR-R5a is safe; it now restores the credential in teardown regardless.

Case count moved from **38 to 41** (+PRR-D10b ready, +PRR-D21 and +PRR-D22 blocked). Revision 1's
*stated* split of "21 ready / 10 blocked / 7 real" was wrong in both directions — its table
actually held 20 ready and 8 real. The revision-2 figures are counted off the table rows.

---

## Overview

### Two prompts, one mechanism

| | visible template | hidden checkout flow |
|---|---|---|
| prompt id | `pr-review` | `pr-review-checkout` |
| registry | `internal/domain/interactive_prompt.go:12-16` (`Hidden: false`) | same file (`Hidden: true`) |
| markdown | `internal/cli/agent/prompts/pr-review.md` | `internal/cli/agent/prompts/pr-review-checkout.md` |
| first line | `PR-REVIEW-READY` (sentinel) | `## READ-ONLY PR REVIEWER` |
| who creates the agent | a human, in `CreateAgentModal` | the server, in `ensureReviewer` |
| agent name | operator-chosen | `review-<owner>-<repo>-<sha8>-pr-<n>` (`reviewer.go:44-55`) |
| role name | `pr-review` (shared per workspace) | `pr-reviewer` (`reviewer.go:32`) |
| role `prompt_file` | `builtin:pr-review` | `builtin:pr-review-checkout` (`reviewer.go:33`) |
| cwd | the agent's normal worktree | detached PR-head worktree `<ws>/.loom/pr-worktrees/<repo>/pr-<n>` |
| behavior | conversational, asks for a PR, never mutates GitHub unmasked | read-only, auto-reviews on boot from `git config loom.reviewBase` |

### Runtime path (identical for both)

1. `POST /api/workspaces/{ws}/agents` with `{kind: "interactive", prompt_file: "builtin:<id>"}`.
   `agentServiceImpl.CreateAgent` (`internal/webui/svcimpl/agent_service.go:351-386`) calls
   `ensureAgentRole`, which creates/reconciles a **role** carrying `prompt_file`. There is
   **no HTTP roles API** — the role is only observable indirectly (see readbacks below).
2. Opening the agent (`/ws/{ws}/agents/{name}`) mounts `AgentDetailMain` →
   `TerminalView pendingAgentName` (`components/AgentDetailMain/AgentDetailMain.tsx:146-155`)
   → `useSessionSeeding` → `POST /api/workspaces/{ws}/agents/{name}/terminal/session`
   (`internal/webui/handlers/terminal/module.go:79`).
3. `agentLaunchCommandArgs` (`internal/webui/handlers/terminal/agent_session.go:389-398`)
   builds `["lead", "--prompt", role.PromptFile]` for interactive roles; base args add
   `--workspace`/`--backend` (`agent_session.go:377-387`). The argv, env and cwd are
   persisted as `TabMetadata.Launch` (`internal/webui/tabmeta/store.go:35-63`) and are
   **readable over HTTP** via `GET /api/workspaces/{ws}/terminal/tabs`. This is the single
   best deterministic proof that `builtin:pr-review` reached the spawned process.
4. `loom lead --prompt builtin:pr-review` → `internal/cli/agent/lead/lead.go:152` →
   `agent.GenerateTerminalPrompt` (`internal/cli/agent/prompts.go:371-386`) renders the
   embedded markdown with `{AgentName, Role, SafetyBlock}` and hands it to the backend CLI
   as the positional first turn.
5. **`PR-REVIEW-READY` is asserted in exactly one place today**: `internal/cli/agent/prompts_test.go:674`
   (Go unit). It appears nowhere in the aft tree, and the `e2e/stubs/codex` stub discards its
   positional prompt (`e2e/stubs/codex:27-34, 137-146`), so the sentinel is **not** observable
   in a stubbed terminal. See seam **S5**.

### Reviewer (pr-review-checkout) lifecycle

`internal/webui/handlers/prreview/` — routes registered at `module.go:97-105`:

```
GET    …/pull-requests                              list.go:26
GET    …/pull-requests/{o}/{r}/{n}                  handlers.go:33
GET    …/pull-requests/{o}/{r}/{n}/diff             handlers.go:59
POST   …/pull-requests/{o}/{r}/{n}/review           handlers.go:108
POST   …/pull-requests/{o}/{r}/{n}/reviewer         reviewer.go:166   ← ensure
POST   …/pull-requests/{o}/{r}/{n}/messages         reviewer.go:562
GET    …/pull-requests/{o}/{r}/{n}/stream           stream.go:57      (SSE, loopback only)
GET    …/pull-requests/{o}/{r}/{n}/conversation     stream.go:209     ← the UI polls this
```

`ensureReviewer` in order (`reviewer.go:166-213`):

1. `resolveAuthorizedPR` → `workspaceHasRepo` matches the request `{owner}/{repo}` against
   each workspace repo's **stored** `RemoteURL` parsed by `parseGitHubOwnerRepo`
   (`membership.go:36-50` — accepts only `github.com` https/ssh or `git@github.com:`).
   Miss → `404 repo_not_registered`.
2. `fetchPullRequestHead` → `ensureConnectorAndGrants` (`seed.go:36-70`) → needs a GitHub
   token from `LOOM_WEBUI_GITHUB_TOKEN` **or** the sealed local-settings credential
   (`seed.go:127-147`). Missing → `errEgressUnavailable` → `503 egress_unavailable`
   (`errors.go:13, 36-37`). **This is the only reason today's stack is degraded.**
   Then dispatches `github.pull_request.read` for `headSha`/`title`/`baseRef`.
3. `prepareReviewerCheckout` (`reviewer.go:278-334`) →
   `localworkspace.EnsureDetachedGitWorktreeAtPRHead` (`internal/localworkspace/localworkspace.go:209-244`),
   which runs `git fetch <remote> +refs/pull/<n>/head:refs/loom/pr/<n>/head` and compares the
   fetched tip with the connector's `headSha`. Mismatch → `PRHeadChangedError` →
   `409 stale_subject` (`reviewer.go:285-289, 311-313`). Then `RememberAgentWorktree` and
   `RecordPRReviewContext` (`localworkspace.go:385-428`) which writes per-worktree
   `loom.reviewBase`, `loom.reviewPr`, `loom.reviewTitle`, `loom.reviewUrl`, `loom.reviewHead`.
4. `ensureReviewerAgentAndRetireLegacy` → creates role `pr-reviewer`
   (`prompt_file: builtin:pr-review-checkout`) + agent, migrates an existing reviewer whose
   backend/role drifted (`reviewer.go:482-495`), and deletes the two legacy name shapes
   (`reviewer.go:64-82, 383-414`).
5. Backend: `reviewerBackend` (`reviewer.go:469-475`) = the workspace's configured backend
   when `leadcontrol.IsControlledLeadBackend` (codex/claude/gemini/opencode/cursor), else codex.

Conversation states (`stream.go:135-172`, `harness_read.go:21-122, 178-191`):

- provider empty or `codex` → live app-server read; no endpoint/thread yet → `starting`;
  dial/read failure → `reconnecting`; else `idle`/`running`.
- provider `claude`/`gemini` → harness transcript on disk (`harness_read.go:48-51`).
  Missing worktree → `failed`; provider without a reader (opencode, cursor) or gemini with no
  session id → `unsupported`.
- `unsupported`/`failed` are what render `pr-chat-unavailable` (`PRDiscussionPanel.tsx:41, 116`).

### Current coverage map

| surface | covered by | gap |
|---|---|---|
| `create-agent-template-interactive-pr-review` | **nothing** | template has zero coverage; `grep` over `tests/aft/` finds only `-template-lead` and `-template-task` |
| `GET …/interactive-prompts` | **nothing** | hidden-prompt exclusion unasserted |
| "Prompt list unavailable; showing built-in defaults." (`CreateAgentModal.tsx:496-500`) | **nothing** | |
| interactive terminal launch argv | **nothing** | `zz-agent-flow` creates a Lead but never opens its terminal |
| `pr-review-workspace`, `pr-discuss-button`, `pr-discussion-panel`, `pr-discussion-error`, `pr-discussion-retry` | `suites/pr-workspace-degraded.test.yaml` | degraded state only; retry is clicked by nobody |
| `POST …/reviewer` → 503 `egress_unavailable`; `GET …/conversation` → 404 `reviewer_not_started` | `surface-suites/pr-contracts.test.yaml` | no messages/stream/repo-not-registered/invalid-path contracts |
| approve / request-changes on hollow fixtures | `surface-suites/review-actions.test.yaml` | no PR, branch, or diff behind the decision — **and** its fixtures have no `external_ref`, so it only ever exercises the non-GitHub branch of `ReviewDecisionService.Apply`. The GitHub-linked branch (an outright refusal, `review_decision.go:63-65`) is untested: see PRR-D16b and B5 |
| `/prs` empty state | `suites/review-queue.test.yaml` | `prs-github-warning` never asserted |
| `pr-review-stale-banner`, `pr-chat-unavailable`, `pr-chat-composer`, `pr-chat-send`, `pr-chat-open-terminal`, `pr-discussion-tab-*`, `review-agent-button` (partly), `new-review-agent`, `pr-create-ticket` | **nothing** | FINDINGS §3.9 blocker |
| `PRReviewWorkspace.tsx:637-650` "+ New review agent…" prefill | **nothing** | |

---

## Part 1 — Deterministic tier (stub AI backend)

Conventions assumed throughout: `# covers:` comment per test, `intent:` on every
`run:`/`api:`/`wait.fn` step, `${RUN_ID}` scoping, `teardown: scripts/close-open-issues.sh`,
own workspace + `zz-` prefix for any suite that leaves an agent behind, actor fidelity
(humans click mounted controls; API clients mutate over HTTP; API steps in the test body
are readbacks only).

**Vocabulary constraints a writer must respect** (verified against `../testing-app/src/`):

- **Agent readbacks go through the list route.** There is no `GET /agents/{name}`
  (`internal/webui/handlers/agents/module.go:24-35`); a `GET` there returns 405. Use
  `GET /api/workspaces/{ws}/agents` and filter. Since agent-list order is not pinned, prefer a
  `run:` step:
  ```
  run: >-
    code="$(curl -sS -o "$AFT_WORK_DIR/prr-agents.json" -w "%{http_code}" "$AFT_BASE_URL/api/workspaces/$WS/agents")";
    [ "$code" = 200 ] || { cat "$AFT_WORK_DIR/prr-agents.json"; exit 1; };
    python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); a=next((x for x in d["data"] if x["name"]==sys.argv[2]), None); assert a, d; assert a["role_name"]=="pr-review", a'
      "$AFT_WORK_DIR/prr-agents.json" "prr-${RUN_ID:-local}"
  intent: "A reviewer's readback confirms the created agent carries the PR-review role"
  ```
  This is the same shape `zz-agent-flow.test.yaml:229` and `pr-workspace-degraded`'s setup
  already use. An `api:` step with `path: "data.0.role_name"` **does** resolve (numeric
  segments index arrays — `../testing-app/src/api-step.ts:70-77`) but is only acceptable when a
  sibling `total`/`count` assertion pins the list to one element.
- **There is no HTTP roles API**, so `role.prompt_file` is never directly readable. It is proven
  through the terminal tab's `launch.argv` (PRR-D5, PRR-D13).
- **Assertion shapes in use here** — all schema-verified against `ExpectSchema`
  (`../testing-app/src/types.ts:97-112`), whose full key set is
  `url`, `text`, `notText`, `title`, `visible`, `count`, `attr`, `value`, `enabled`, `checked`:
  - `expect.attr: { testid|selector, name, equals }` — `AttrExpectSchema` at `types.ts:86-88`,
    wired at `:105`, implemented at `src/steps.ts:83, 333`, and already used in passing suites at
    `tests/aft/suites/zz-agent-flow.test.yaml:224` and `tests/aft/suites/issue-detail.test.yaml:132`.
  - `expect.value: { testid|selector, equals }` — `ValueExpectSchema` at `types.ts:90`.
  - `expect.enabled` / `expect.checked: { testid|selector, equals?: <bool> }` —
    `BoolStateExpectSchema` at `:92-95`; `equals` defaults to `true` when omitted.
  - `expect.visible` requires a CSS-able locator — no `first`/`last`/`nth` (`types.ts:113-115`).
- **`${var:name}` resolves ONLY inside `api:` steps.** `interpolateApiValue` is called solely from
  `executeApiStep` (`../testing-app/src/api-step.ts:95`), and the runner hands `vars` to the api
  branch but not to `executeStep` (`src/runner.ts:518-524`). It will **not** expand inside
  `expect.value`, `expect.attr`, `fill.value`, `wait.text`, or a `wait.fn` body. Environment
  interpolation (`${RUN_ID:-local}`) is separate and does work in browser steps — that is why the
  existing suites can write `wait: { text: "pr degraded ${RUN_ID:-local}" }`.
  **The bridge**: every `save:` also mirrors its value to `$AFT_WORK_DIR/<as>`
  (`api-step.ts:79-83`), which is how `pr-contracts`' teardown reads `$AFT_WORK_DIR/prcWs`. So a
  runtime-derived value reaches the browser only through a `run:` step:
  ```
  run: >-
    want="review-$(tr 'A-Z' 'a-z' < "$AFT_WORK_DIR/reviewIssueId")";
    got="$(agent-browser --session "$AFT_SESSION" eval "document.querySelector('[data-testid=create-agent-name]').value")";
    [ "$got" = "$want" ] || { echo "prefill mismatch: want=$want got=$got"; exit 1; }
  intent: "A reviewer confirms the new-agent name was prefilled from the PR's ticket id"
  ```
- **Genuinely absent, do not reach for these**:
  - **`fill` has no `valueFile`**: `ValuedLocatorSchema` is `{ ...locatorShape, value: string }`
    (`types.ts:45`) — the value is a required inline string.
  - **`api:` has no body-from-file**: `ApiSchema.body` is object-or-string (`:144-153`). Build
    JSON inline, or write it in a `run:` step and `curl -d @file` (the pattern
    `pr-workspace-degraded`'s setup already uses).
  - **`api:` asserts cannot filter arrays** — see the agent-readback note above.

Proposed suite layout:

- `suites/pr-review-template.test.yaml` — PRR-D1…D6 (no persistent agents? see note)
- `suites/zz-pr-review-agents.test.yaml` — PRR-D5, D7, D20 (creates agents → own workspace `E2E-WS-PRR`)
- `suites/pr-workspace-degraded.test.yaml` (extend) — PRR-D8a…D8e
- `surface-suites/pr-contracts.test.yaml` (extend) — PRR-D9a…D9f
- `suites/zz-pr-review-connected.test.yaml` — PRR-D11…D19 (**needs seam S1+S2**, own workspace, clears the credential in teardown)

### Group A — template creation (ready to write)

#### PRR-D1 — Modal creates a PR Review interactive agent and wires `builtin:pr-review`
- **Tier**: product-correctness (`suites/zz-pr-review-agents.test.yaml`, workspace `E2E-WS-PRR`).
- **Intent**: An operator creates a PR Review terminal agent from the agent templates and the
  created agent is registered with the built-in PR-review prompt.
- **Preconditions**: own workspace with one empty git repo (mirror `zz-agent-flow`'s setup
  block); no agents.
- **Steps**
  1. `open: /ws/E2E-WS-PRR/agents`
  2. `wait.fn` `agents-page` present.
  3. `click: { role: button, name: "+ Add agent", exact: true }`
  4. `wait.fn` `create-agent-overlay` present.
  5. `click: { testid: create-agent-template-interactive-pr-review }`
  6. `fill: { testid: create-agent-name, value: "prr-${RUN_ID}" }`
  7. `click: { testid: create-agent-submit }`
  8. `wait.fn` overlay gone **and** `document.body.textContent.includes('prr-')`.
- **Assertions / readbacks**
  - `expect.visible` the new agent name in the rail.
  - `run:` readback over `GET /api/workspaces/E2E-WS-PRR/agents` (the snippet in the
    conventions block above): the entry named `prr-${RUN_ID}` exists with
    `role_name == "pr-review"` and a non-empty `backend`. **Not** `GET /agents/{name}` — that
    route does not exist (`internal/webui/handlers/agents/module.go:24-35`).
  - Role `prompt_file` is **not** exposed over HTTP; it is proven in PRR-D5 through the
    terminal tab's `launch.argv`. Do not fake a readback here.
- **Edge rationale**: this is the modal's most prominent card and has never been created by
  any test; it is also the only path that sends `kind: "interactive"` + `prompt_file`
  together (`CreateAgentModal.tsx:311-338`).

#### PRR-D2 — Template selection semantics (and the real default)
- **Tier**: product-correctness (`suites/pr-review-template.test.yaml`, workspace `E2E-WS`,
  no agent created — modal is cancelled).
- **Intent**: An operator opening the agent templates sees Task Runner preselected and can
  switch to the PR Review template, which re-labels the name field.
- **Correction to the brief**: PR Review is **not** the selected card on open.
  `resolveInitialSelection(undefined, "task")` returns `{kind: "background", role: "task"}`
  (`CreateAgentModal.tsx:105-113, 141-152`), so `create-agent-template-task` carries
  `aria-pressed="true"`. What *is* pre-armed is `selectedBuiltinPromptID = "pr-review"`
  (`:155-156`, reset to the same value on every open and after every successful create at
  `:251` and `:353`) — but that state is invisible until the user picks an interactive card.
  The test should pin the truth, not the folklore.
- **Steps**: open agents page → `+ Add agent` →
  `expect: { attr: { testid: create-agent-template-task, name: aria-pressed, equals: "true" } }` →
  `expect: { attr: { testid: create-agent-template-interactive-pr-review, name: aria-pressed, equals: "false" } }` →
  `expect: { attr: { testid: create-agent-name, name: placeholder, equals: "worker" } }` →
  `click: { testid: create-agent-template-interactive-pr-review }` → the same three
  assertions inverted (`aria-pressed` `"true"` on PR Review, `"false"` on Task Runner,
  `placeholder` `"reviewer"`) → `expect: { text: Selected }` for the selected-card mark
  (`AgentTemplateCard.tsx:50-54`) → `click: { testid: create-agent-close }`.
- Implementation correction (verified live): `expect: { text: Selected }` fails because CSS
  `text-transform` renders `SELECTED` and aft text match is case-sensitive; assert
  `aria-pressed` instead.
- **Assertions**: `expect.attr` only — schema `{ selector|testid, name, equals }`
  (`../testing-app/src/types.ts:86-88`), the same shape already used at
  `tests/aft/suites/issue-detail.test.yaml:132` and `tests/aft/suites/zz-agent-flow.test.yaml:224`.
  `data-active` is *absent* rather than `"false"` when unselected
  (`AgentTemplateCard.tsx:32` uses `selected || undefined`), so assert on `aria-pressed`,
  which is always present as `"true"`/`"false"` (`:33`).
- **Edge rationale**: guards the accent/placeholder mapping in `interactivePromptCard`
  (`CreateAgentModal.tsx:86-94`) and documents the default so a future default change is a
  test failure rather than a silent product shift.

#### PRR-D3 — Interactive-prompts contract: exactly the two visible prompts
- **Tier**: product-correctness for the UI half (same suite as PRR-D2), with an `api:`
  readback for the payload.
- **Intent**: An operator sees only the built-in prompts Loom publishes, and the hidden
  PR-review-checkout prompt is never offered as a template.
- **Steps**: open the modal (state from PRR-D2 is fine) →
  `wait.fn` that `document.querySelectorAll('[data-testid^=create-agent-template-interactive-]').length === 2`
  → `expect.visible` `create-agent-template-lead`, `create-agent-template-interactive-pr-review`
  → `expect.count 0` for `[data-testid=create-agent-template-interactive-pr-review-checkout]`
  → `expect.visible` `create-agent-template-custom-prompt`.
- Implementation correction (verified live): `[data-testid^=create-agent-template-interactive-]`
  matches only the PR Review card; the Lead card's testid is the literal
  `create-agent-template-lead`. Count both testids explicitly.
- **Readback**: `api: GET /api/workspaces/E2E-WS/interactive-prompts`, `status: 200`, asserts:
  ```
  - { path: "prompts.0.id",    equals: "lead" }
  - { path: "prompts.1.id",    equals: "pr-review" }
  - { path: "prompts.1.label", equals: "PR Review" }
  - { path: "prompts.2",       exists: false }
  ```
  Pinning both ids **and** the absence of a third element excludes `pr-review-checkout` by
  construction. Revision 2 proposed "the serialized body does not contain `pr-review-checkout`";
  that is not writable — `ApiAssertSchema` permits exactly one of `exists`/`equals`/`contains` and
  has no negation (`../testing-app/src/types.ts:128-137`). Source: `handlers.go:41-51` filters
  `Hidden`, and the field is `json:"-"` (`interactive_prompt.go:6`) so it never appears either way.
- **Edge rationale**: the registry is ordered and the modal renders it verbatim; a new
  built-in prompt (or an un-hidden checkout prompt) must fail this test loudly.

#### PRR-D4 — Prompt-list fallback UI
- **Tier**: product-correctness (same suite; uses aft's existing `offline:` step, already
  used by `suites/sse-resilience.test.yaml:31,48` — **no new product seam needed**).
- **Intent**: An operator whose prompt list fails to load still sees the built-in templates
  and an honest notice instead of an empty template group.
- **Preconditions**: agents page already loaded and idle (so only the modal's own fetch is
  in flight while offline).
- **Steps**: `offline: on` → `click: { role: button, name: "+ Add agent" }` →
  `wait.fn` `create-agent-overlay` → `wait: { text: "Prompt list unavailable; showing built-in defaults." }`
  → `expect.visible` `create-agent-template-interactive-pr-review` (the
  `DEFAULT_INTERACTIVE_PROMPTS` fallback, `CreateAgentModal.tsx:33-36, 167-173`) →
  `click: { testid: create-agent-close }` → `offline: off`.
- Implementation correction (verified live): Prompts load at app mount, so offline-after-open
  cannot unload them; `offline:` also has no failure-safe restore. Use a per-test
  `routes:` abort for the prompt-list request.
- **Assertions**: notice text present; both fallback cards render; `expect.notText`
  "Something went wrong".
- **Edge rationale**: the fallback list is a hard-coded duplicate of the registry — this is
  the only thing that will notice when the two drift.

#### PRR-D5 — Starting the pr-review terminal proves the prompt reached the process
- **Tier**: product-correctness (`suites/zz-pr-review-agents.test.yaml`, continues PRR-D1).
- **Intent**: An operator opening their PR Review agent gets a live terminal launched with
  the built-in PR-review prompt.
- **Steps**: `open: /ws/E2E-WS-PRR/agents/prr-${RUN_ID}-${AFT_CASE_ID}` followed by one grouped
  state assertion. The state waits for the agent-specific controlled Codex marker and prompt
  contract (`safety-blocks=1`), fails immediately on an agent-start/backend error, reads the
  same-origin public terminal-tabs endpoint, and keeps the mounted wrapper visible.
- **Readbacks** — browser `fetch` over `GET /api/workspaces/E2E-WS-PRR/terminal/tabs`, selecting
  exactly one tab with the case-isolated `agent_id` (the response is a list; order is not pinned):
  - `kind == "agent"`.
  - `launch.argv` (joined) contains `lead`, `--prompt`, and `builtin:pr-review`. Note
    `argv` is the **shell-wrapped** form (`webuterminal.ShellArgvForCommand`,
    `agent_session.go:333`), so match on the joined string rather than exact array positions.
  - `launch.env.LOOM_AGENT_ROLE == "pr-review"` and `launch.env.LOOM_AGENT_NAME ==
    "prr-${RUN_ID}"` (`agent_session.go:427-437`).
  - `pty_alive == true`. DTO: `internal/webui/tabmeta/store.go:35-63`.
- **Edge rationale**: the launch selector proves which built-in prompt was requested; the
  controlled prompt contract proves the process received a rendered prompt with one safety
  block; the connection marker and `pty_alive` prove that bootstrap did not fail afterward.

#### PRR-D5b — `PR-REVIEW-READY` in terminal output
- **Tier**: the deterministic tier proves a hashed prompt contract; direct sentinel rendering
  remains a real-backend concern delivered by PRR-R1.
- **Intent**: An operator sees the PR reviewer announce itself before asking for a PR.
- **Boundary**: the controlled stub observes prompt bytes through the same remote launch used by
  the product, but intentionally reports a fingerprint and safety-block count rather than
  echoing operator instructions into reviewer-visible output. The literal sentinel remains
  unit-tested and is exercised visibly only by the real-backend tier.

#### PRR-D6 — Role reuse and role-conflict on the shared `pr-review` role
- **Tier**: product-correctness for the happy half; surface for the conflict half
  (`surface-suites/pr-contracts.test.yaml` or a new `surface-suites/agent-roles.test.yaml`).
- **Intent (a)**: A second PR Review agent reuses the workspace's existing PR-review role
  rather than failing.
- **Intent (b)**: An API client that tries to reuse the `pr-review` role name with a
  different prompt is refused with a role-conflict error.
- **Steps (a)**: create a second agent through the modal with the PR Review card
  (`prr2-${RUN_ID}`) → succeeds → single `run:` readback over `GET /agents` asserting **both**
  entries carry `role_name == "pr-review"` (one list fetch, two python assertions).
- **Steps (b)**: `api: POST /api/workspaces/E2E-WS-PRR/agents` with
  `{name: "pr-review", role_name: "pr-review", kind: "interactive", prompt: "totally different"}`
  → expect 4xx and an error message containing `already exists with a different prompt`
  (`svcimpl/agent_service.go:495-511`).
- **Edge rationale**: the template hard-codes `role_name = selectedBuiltinPromptID`
  (`CreateAgentModal.tsx:321`), so every PR Review agent in a workspace shares one role —
  an invariant nothing currently pins.

#### PRR-D20 — Unknown built-in prompt is accepted at create and fails visibly at launch
- **Tier**: product-correctness for the observable half (`suites/zz-pr-review-agents.test.yaml`),
  with an `api:` half documenting the create-time asymmetry.
- **Intent**: An operator whose interactive agent names a built-in prompt that does not exist
  gets a terminal that says so, instead of a silently wrong agent.
- **Where the failure actually is** (this is the fix over revision 1): `POST /terminal/session`
  **succeeds**. It only assembles the launch spec (`buildAgentLaunchSpec`,
  `agent_session.go:315-336`), and the interactive branch appends `--prompt <promptFile>`
  without validating it (`:389-398`). `CreateAgent` does not validate it either
  (`svcimpl/agent_service.go:351-386`). The rejection happens inside the spawned process:
  `leadStartupPrompt` → `GenerateTerminalPrompt` → `unknown built-in interactive prompt %q`
  (`internal/cli/agent/prompts.go:376-381`), and `lead.go:118-124` prints
  `Error loading terminal prompt: …` plus `Dropping into a shell.` to stderr and then
  `execShell`s — so the PTY stays alive with the error on screen. That makes it a real
  browser-observable case, not an API-only one.
- **Steps**
  1. `api: POST …/agents` `{name: "prrbad-${RUN_ID}", role_name: "prrbad-${RUN_ID}", kind:
     "interactive", prompt_file: "builtin:not-a-prompt"}` → expect **201** (assert the
     permissiveness explicitly).
  2. `open: /ws/E2E-WS-PRR/agents/prrbad-${RUN_ID}` → `wait.fn` `[data-testid=terminal-wrapper] .wterm`.
  3. `wait.fn` that the joined `.term-row` text inside `[data-testid=terminal-wrapper]`
     contains `unknown built-in interactive prompt`.
- **Assertions**: the error string is visible; `expect.notText "Something went wrong"`;
  `run:` readback of `GET /terminal/tabs` shows the tab exists with
  `launch.argv` containing `builtin:not-a-prompt` (the bad value really was persisted).
- **Note on the backend health check**: `lead.go:96-105` runs the backend health check *before*
  prompt generation, so the stub codex on PATH must satisfy it or the terminal shows
  "backend is not installed" instead. Verify which message lands before pinning the text.
- **Second half (surface)**: `prompt_file: "builtin:pr-review-checkout"` is **accepted** —
  `IsBuiltinInteractivePrompt` (`interactive_prompt.go:24-31`) does not filter `Hidden`.
  Assert 201 explicitly: hidden means "not listed in the modal", not "rejected by the API".

### Group B — degraded reviewer, beyond what exists (ready to write)

These extend `suites/pr-workspace-degraded.test.yaml`, reusing its workspace/repo/issue
fixture (bare `git init` + `origin https://github.com/acme/widgets` + a `review`-status
issue with `external_ref` `https://github.com/acme/widgets/pull/7`).

#### PRR-D8a — Retry keeps the panel alive
- **Tier**: product-correctness.
- **Intent**: A reviewer whose PR discussion failed can retry and the panel recovers into a
  fresh attempt rather than crashing or blanking.
- **Steps**: from the existing degraded state, `click: { testid: pr-discussion-retry }` →
  `wait.fn` that `pr-discussion-panel` is still mounted **and** `pr-discussion-error`
  re-renders (retry clears `error` then the re-ensure fails again —
  `usePRReviewConversation.ts:62-65, 139-177`).
- **Assertions**: `expect.notText` "Something went wrong"; `pr-discussion-retry` still present
  (it is only rendered while `agentName` is null, `PRDiscussionPanel.tsx:96-105`).
- **Note**: this proves the button is wired and idempotent, not that a second HTTP request
  was issued. The *positive* proof of re-ensure is PRR-D14, which needs seam S1+S2.

#### PRR-D8b — Composer state while the reviewer never started
- **Tier**: product-correctness.
- **Intent**: A reviewer cannot send a message into a reviewer that failed to start.
- **Steps**: `expect: { enabled: { testid: pr-chat-composer } }` (state is `starting`, so
  `chatUnavailable` is false) → `fill: { testid: pr-chat-composer, value: "why did this fail?" }`
  → `expect: { enabled: { testid: pr-chat-send, equals: false } }` (`agentName` is null →
  `canSend` false, `PRDiscussionPanel.tsx:42-46`) → `expect: { count: { testid:
  pr-chat-unavailable, equals: 0 } }`.
- **Assertion shapes**: `expect.enabled` is `BoolStateExpectSchema`
  (`../testing-app/src/types.ts:92-95`), already used at `zz-agent-flow.test.yaml:223`; it
  defaults to expecting `true` when `equals` is omitted.
- **Edge rationale**: distinguishes "no reviewer yet" from "chat unsupported"; today both
  render an unstyled empty area and nothing tells them apart.

#### PRR-D8c — Terminal tab with no reviewer
- **Tier**: product-correctness.
- **Intent**: The Terminal tab of a failed discussion degrades quietly instead of mounting a
  terminal for a nonexistent agent.
- **Steps**: `click: { testid: pr-discussion-tab-terminal }` → assert
  `aria-selected="true"` on it, `expect.count 0` for `[data-testid=terminal-wrapper]` inside
  the panel (the terminal block is gated on `agentName`, `PRDiscussionPanel.tsx:182-189`),
  `expect.notText` "Something went wrong" → click back to `pr-discussion-tab-chat`.

#### PRR-D8d — Closing the discussion restores the diff pane
- **Tier**: product-correctness.
- **Intent**: A reviewer can dismiss the discussion panel and return to the full-width diff.
- **Steps**: `click: { role: button, name: "Close discussion" }` → `wait.fn`
  `pr-discussion-panel` absent → `expect.visible` `pr-review-workspace` → click
  `pr-discuss-button` again → panel returns (proves the toggle at
  `PRReviewWorkspace.tsx:540` is not one-shot).

#### PRR-D8e — PR queue warns instead of failing when GitHub is unreachable
- **Tier**: product-correctness (`suites/review-queue.test.yaml` extension).
  **Status: blocked on S8** (was "ready" in revision 2 — corrected).
- **Intent**: An operator whose workspace has no GitHub access still sees the review queue, with
  an explicit warning instead of an error page.
- **Why it is blocked** (the round-3 correction): the assertion *is* the warning text, and that
  text is machine-dependent because **the stack is not gh-less**. `connectorListAvailable()` is
  false without a credential, so `ghListFallback` runs (`list.go:33-40`) → `CheckGhInstalled`
  (`svcimpl/agent_service.go:266`, `internal/cli/git/pr.go:347-353`):
  - **no `gh` on PATH** (CI): a warning `gh CLI not installed: install from https://cli.github.com/ …`
    (`agent_service.go:269-273`), prefixed by the connector-unavailable string (`list.go:158-161`);
  - **`gh` on PATH** (typical dev box — `/opt/homebrew/bin/gh` on the machine this plan was
    written on): a **real** `gh pr list --state all --limit 500 --json …` against the fixture's
    `acme/widgets` remote (`internal/cli/git/pr_list.go:42-49`) → live network call for a
    nonexistent repo → an error string that depends on gh's version and auth state, surfaced as
    either `warnings` or a 502 (`list.go:169-171`).
  `run-aft.sh:292-296` prepends `e2e/stubs` but preserves the host `$PATH` tail, and no `gh` stub
  exists — so this is not hypothetical.
- **Steps once S8 lands**: `open: /ws/E2E-WS/prs` → `wait.fn` `prs-github-warning` present →
  `expect.visible` `prs-github-warning` → `expect: { text: <the stub's pinned warning> }` →
  `expect.notText "Something went wrong"`.
- **Edge rationale**: `prs-github-warning` is uncovered and is the only user-visible signal that
  the whole PR surface is running blind. Worth doing properly rather than with an
  `assert-either-string` fudge.
- **Note for the existing suites**: `suites/review-queue.test.yaml` and
  `suites/pr-workspace-degraded.test.yaml` already load `/prs` and therefore already make this
  live `gh` call on any machine with `gh` installed. They pass today because their assertions
  happen not to depend on it. S8 is a fix for them too, independent of this plan.

### Group C — PR review workspace agent control (ready to write)

#### PRR-D7 — "+ New review agent…" prefill
- **Tier**: product-correctness (`suites/zz-pr-review-agents.test.yaml`; creates an agent).
- **Intent**: A reviewer creating a review agent from the PR review workspace gets a
  pre-named agent that is assigned to the PR's ticket on creation.
- **Preconditions**: a `review`-status issue in the suite's own workspace (reuse the
  degraded-suite fixture shape so the review workspace opens).
- **Steps**: open `/ws/<ws>/prs` → retry-click the row until `location.search` has `review=`
  (copy the retry loop from `pr-workspace-degraded`) → `wait.fn` `pr-review-workspace` →
  `click: { testid: review-agent-button }` → `click: { testid: new-review-agent }` →
  `wait.fn` `create-agent-overlay`.
- **Assertions**
  - The prefilled name equals `review-<issue-id lowercased>` (`PRReviewWorkspace.tsx:642`).
    **Not** via `expect.value`: the id is only known through the seeding step's `save:`, and
    `${var:…}` does not interpolate outside `api:` steps
    (`../testing-app/src/api-step.ts:95`, `src/runner.ts:518-524`). Use the
    `run:` + `agent-browser … eval` + `$AFT_WORK_DIR/<saved>` comparison shown in the conventions
    block. A weaker `wait.fn` on `.value.startsWith('review-')` is acceptable as a fallback but
    does not pin the id.
  - `create-agent-template-task` is `aria-pressed="true"` — the prefill passes
    `defaultRoleName="task"` and no `defaultKind`, so the "review agent" it offers is a
    **background Task Runner**, not the PR Review interactive template
    (`PRReviewWorkspace.tsx:637-644`). Pin the current behavior and file the mismatch as a
    product question (see Blockers §B4).
  - Submit → `wait.fn` overlay gone → `api:` readback `GET …/issues/{id}` → `data.assignee`
    equals the new agent name (`onSuccess` → `assignReviewer`,
    `PRReviewWorkspace.tsx:645-648, 274-300`). This one is a genuine single-object route, so
    `api:` with `path: "data.assignee"` is correct here.
  - `run:` readback over `GET …/agents` (**not** `GET /agents/{name}`, which does not exist):
    the entry named `review-<issue-id>` has `role_name == "task"`.
- **Teardown**: delete the created agent (mirror `zz-agent-flow`'s teardown `DELETE …/agents/<name>`).

### Group D — reviewer endpoint contracts (ready to write, surface tier)

Extends `surface-suites/pr-contracts.test.yaml`, which already pins
`POST reviewer → 503 egress_unavailable` and `GET conversation → 404 reviewer_not_started`.
Deltas:

| id | request | expected |
|---|---|---|
| PRR-D9a | `POST …/pull-requests/acme/widgets/7/messages` `{text:"hi"}` before any reviewer | `404` `reviewer_not_started` (`reviewer.go:580-583`) |
| PRR-D9b | `GET …/pull-requests/acme/widgets/7/stream` before any reviewer | `404` `reviewer_not_started` (`stream.go:88-91`) |
| PRR-D9c | `POST …/pull-requests/other/repo/7/reviewer` (repo not in workspace) | `404` `repo_not_registered` (`handlers.go:161-165`) |
| PRR-D9d | `POST …/pull-requests/acme/widgets/0/reviewer` and `…/acme/wid~gets/7/reviewer` | `400` `invalid` (`membership.go:22-33`, `handlers.go:21-23`) |
| PRR-D10b | `POST …/pull-requests/ACME/WIDGETS/7/reviewer` with `Acme/widgets`-cased registration | `503` `egress_unavailable`, **not** `404 repo_not_registered` — membership is `EqualFold` and returns the canonical casing (`membership.go:77-92`), which `authorizeRepo` then uses for the grant resource and the dispatch resource so the two can never decouple (`handlers.go:151-167`) |
| PRR-D9e | `POST …/pull-requests/acme/widgets/7/review` `{event:"approve"}` (no `expected_head_sha`) | `428` `precondition_required` — **reachable today**, because `decodeReviewRequest` runs before the connector ensure (`handlers.go:114-122, 179-195`) |
| PRR-D9f | same with `{event:"nonsense", expected_head_sha:"abc"}` | `400` `invalid` (`handlers.go:189-193`, `githubReviewEvent` at `:268-279`) |

- **Intent sentence pattern**: "An API client probes the PR reviewer <x> endpoint on a stack with
  **no GitHub credential** and receives the documented <code> code."
- **Vocabulary correction (revision 3)**: say "no GitHub credential", not "gh-less". The existing
  `surface-suites/pr-contracts.test.yaml` and `suites/pr-workspace-degraded.test.yaml` describe the
  stack as gh-less, which is doubly misleading: the degraded reviewer codes come from the missing
  **credential** (`seed.go:127-147` → `errEgressUnavailable`), and the `gh` CLI is in fact present
  on most developer machines and really does get executed by the PR-list fallback (S8). Two
  independent preconditions were collapsed into one wrong word. Worth fixing those two suite
  headers when G1/G2 land.
- **Promotion note for the file header**: D9e/D9f promote to tier 1 the moment the review
  decision UI can produce them from a mounted control; D9a–D9d and D10b stay surface because no
  UI path constructs those requests.
- **On D9d's path characters**: avoid a raw space — Node's WHATWG URL parser percent-encodes it
  before the request leaves aft, so the test would be asserting on the encoder rather than the
  handler. `~` is legal in a URL path and outside `ownerRepoSegmentRE`'s
  `[A-Za-z0-9._-]` class (`membership.go:13`), so it reaches `parsePullRequestPath` intact.
  Note `.` and `-` **are** allowed, so `wid.gets` would wrongly pass.
- **On D10b's fixture**: register the repo with mixed casing (`git remote add origin
  https://github.com/Acme/widgets`) so the canonical value differs from the request. The
  degraded stack cannot show the grant resource directly, so the discriminator is the error
  code: reaching `503 egress_unavailable` proves membership matched and execution advanced to
  `ensureConnectorAndGrants`. Once S1+S2 land, extend it to assert the fake upstream received
  `/repos/Acme/widgets/…` (canonical), not `/repos/ACME/WIDGETS/…`.

### Group E — connected reviewer (blocked on seams S1 + S2)

All of these live in a new `suites/zz-pr-review-connected.test.yaml` with its own workspace
(`E2E-WS-PRC`) and a teardown that **clears the GitHub credential** (`PATCH /api/local/settings`
`{runtime_credentials:{github:{clear:true}}}`) so later/other suites stay degraded.
`zz-` prefix because it installs process-global credential state and leaves agents behind.

Shared fixture (seam S1, detailed in Part 3):

```
$AFT_WORK_DIR/gh/acme/widgets.git      bare "GitHub" repo, main + refs/pull/7/head
$AFT_WORK_DIR/prc-widgets              working checkout; origin registered as
                                       https://github.com/acme/widgets at POST /repos time,
                                       then repointed at the bare repo
fake-github REST server on 127.0.0.1   LOOM_CONNECTOR_GITHUB_BASE_URL
```

#### PRR-D11 — The PR queue lists a connector-served pull request
- **Tier**: product-correctness. **Status**: blocked on S1+S2.
- **Intent**: An operator who has configured a GitHub credential sees the repository's open
  pull requests in the review queue.
- **Steps**: human installs the token through Settings (`open: /ws/<ws>/settings` →
  `fill: { testid: github-token-input, value: "ghp_aft_${RUN_ID}" }` →
  `click: { testid: github-credential-save-button }`) — a genuine mounted control
  (`components/SettingsView/SettingsView.tsx:677-724`) whose PATCH invalidates the seed cache via
  `OnGitHubRuntimeCredentialChanged` (`internal/webui/modbuilder/modbuilder.go:121-129`,
  `handlers/localsettings/localsettings.go:87-95`). Then `open: /ws/<ws>/prs`.
- **Wait target after saving** (corrected): the success toast is `"Runtime credentials saved"`
  (`SettingsView.tsx:351`) — **not** `"Credential saved."`, which is the persistent
  configured-state helper text at `:700`. Toasts are transient, so wait on the durable signals:
  `wait.fn` for `[data-testid=github-credential-clear-button]`, which only renders once
  `runtime_credentials.github.configured` is true (`:705-712`), and
  `expect: { attr: { testid: github-token-input, name: placeholder, equals: "Saved token unchanged" } }`
  (`:693-696`). Optionally `wait: { text: "Runtime credentials saved" }` first, tolerating the race.
- **Assertions**: row with `aria-label="Review Add widget ${RUN_ID}"` and `#7`;
  `expect.count 0` for `prs-github-warning`; readback
  `GET /api/workspaces/<ws>/pull-requests?state=open` → `data.pull_requests[0].number == 7`,
  `.state == "OPEN"`, `.repo_name == "acme/widgets"`, `data.warnings` empty
  (`list.go:199-259`).
- **Edge rationale**: the entire connector list path (`list.go:79-145`, pagination,
  `normalizePullState`) is unexecuted by any test.

#### PRR-D12 — Compare diff renders in the review workspace
- **Tier**: product-correctness. **Status**: blocked on S1+S2.
- **Intent**: A reviewer opening a GitHub-only pull request sees its file diff.
- **Steps**: click the PR row → `wait: { url: "**review-pr=**" }` (an unlinked PR uses
  `?review-pr=acme/widgets%237`, `views/PRsPage.tsx:270-277, 121-123`) → `wait.fn`
  `pr-review-workspace` → `expect.visible` `pr-create-ticket` (no linked issue) → assert the
  `PRCompareDiffPane` shows the fixture's changed file name.
- **Readback**: `GET …/pull-requests/acme/widgets/7/diff` → `data.files[0].path` and a
  non-empty `data.diff` (`handlers.go:59-106`, `providers/github.go:331-413`).
- **Edge rationale**: exercises the two-dispatch read path (PR read then compare read with
  `ExpectedHeadSha` precondition) that nothing touches today.

#### PRR-D13 — Reviewer happy path: detached PR-head checkout + checkout prompt
- **Tier**: product-correctness. **Status**: blocked on S1+S2. **Highest value case in the plan.**
- **Intent**: A reviewer opening Discuss PR gets a review agent booted read-only in a
  detached checkout of the pull request's head commit.
- **Steps**: from the review workspace, `click: { testid: pr-discuss-button }` → `wait.fn`
  `pr-discussion-panel` present **and** `pr-discussion-error` absent → `wait.fn` the panel
  status text is one of `starting|idle|running` (`PRDiscussionPanel.tsx:61`).
- **Assertions / readbacks**
  - `POST …/reviewer` readback: `data.agent_name` matches
    `^review-acme-widgets-[0-9a-f]{8}-pr-7$`, `data.checked_out_sha` equals the fixture head
    sha, `data.seeded == true`.
  - `GET …/conversation` → `200`, `state == "starting"`, `messages == []` (stub codex never
    opens an app-server endpoint, so `readCodexReviewerSnapshot` short-circuits —
    `stream.go:157-163`). Compare with today's 404.
  - `GET …/terminal/tabs` → a tab with `agent_id` = the reviewer name, `launch.argv`
    containing `"--prompt"`, `"builtin:pr-review-checkout"`, and
    `launch.cwd` ending in `/.loom/pr-worktrees/<repoName>/pr-7`.
  - `run:` git readbacks against the worktree: `git -C <cwd> rev-parse HEAD` equals the head
    sha; `git -C <cwd> symbolic-ref -q HEAD` fails (detached); `git -C <cwd> config --worktree
    loom.reviewBase` resolves to the base commit; `git -C <cwd> config --worktree loom.reviewPr`
    is `7`; `git -C <cwd> status --porcelain` is empty.
  - `run:` readback over `GET …/agents`: an entry whose name matches the reviewer regex exists
    with `role_name == "pr-reviewer"` and `desired_state == "running"` (`reviewer.go:341-347`).
    The name is derived, not known in advance, so this must be a python match — there is no
    per-agent GET to fetch it directly.
- **Edge rationale**: proves the whole `ensureReviewer` chain — connector read, PR-head fetch,
  worktree materialization, self-describing git config, role/agent creation, launch spec.

#### PRR-D14 — Degraded → recovery proves Retry re-ensures
- **Tier**: product-correctness. **Status**: blocked on S1+S2.
- **Intent**: A reviewer who hit the degraded discussion state, then configured GitHub access,
  can retry and reach a working reviewer without reloading.
- **Round-3 correction — the Settings round trip cannot work.** `discussOpen` is
  `PRReviewWorkspace` component state (`:153`), so routing to `/ws/<ws>/settings` unmounts the
  workspace and the panel with it; coming back resets `discussOpen` to `false`, and re-opening
  mounts a **fresh** `PRDiscussionPanel` whose `usePRReviewConversation` runs ensure from scratch
  (`usePRReviewConversation.ts:127-137` resets on key change, `:139-177` re-ensures) — which now
  succeeds. The Retry button is never exercised. aft also has no tab/window action to work around
  it: `actionKeys` is `open, click, dblclick, fill, type, select, check, press, scroll, drag,
  hover, focus, upload, reload, back, forward, offline, run, api, wait, expect`
  (`../testing-app/src/types.ts:157-162`).
- **Steps (redesigned — the panel never unmounts)**
  1. Credential **not** installed. Open the review workspace → `click: { testid: pr-discuss-button }`
     → `wait.fn` `pr-discussion-error` **and** `pr-discussion-retry` present.
  2. `api: PATCH /api/local/settings` with
     `{runtime_credentials: {github: {token: "ghp_aft_${RUN_ID}"}}}` → 200. This runs in the
     harness, not the browser, so the page is untouched. It also fires
     `InvalidateCredentialSeeds` (`modbuilder.go:121-129`), which is precisely what the retry needs.
  3. `click: { testid: pr-discussion-retry }` → `wait.fn` `pr-discussion-error` absent **and**
     `pr-discussion-retry` absent (the button only renders while `agentName` is null —
     `PRDiscussionPanel.tsx:96-105`), so its disappearance *is* the proof that ensure succeeded.
- **Assertions**: readback `GET …/conversation` → 200 (it was 404 before the retry);
  `run:` readback that a reviewer agent now exists.
- **Actor-fidelity caveat, stated deliberately**: step 2 is an API mutation inside the test body,
  which the product-correctness tier normally reserves for the persona named in `intent:`. The
  justification is that the case under test is the **Retry control** — a human click — and the
  credential install is standing in for "someone configured access in another window", which aft
  cannot express. Write the intent as *"An operator retries the PR discussion after GitHub access
  is configured out of band, and the reviewer starts"* so the split is explicit. If a reviewer
  objects, move the case to `surface-suites/` rather than faking a second browser.
- **Edge rationale**: the only way to prove the retry issues a *new* ensure rather than just
  clearing local error state. Closes the honest gap left by PRR-D8a.

#### PRR-D15 — Stale PR head raises the stale banner
- **Tier**: product-correctness. **Status**: blocked on S1+S2. Closes **FINDINGS §3.9** item 1.
- **Intent**: A reviewer whose pull request moved while they were reading it is told the diff
  was refreshed rather than silently reviewing a stale head.
- **Steps**: with a working reviewer (PRR-D13 state), `run:` advance the fixture:
  commit a second change in the working repo, push it to the bare repo, and
  `git -C <bare> update-ref refs/pull/7/head <new sha>` **without** updating the fake REST
  server's reported `headSha` (or the mirror image: bump the REST head and leave the ref).
  Either way `EnsureDetachedGitWorktreeAtPRHead` returns `PRHeadChangedError`
  (`localworkspace.go:239-241`) → `409 stale_subject` (`reviewer.go:285-289`).
  Then close and reopen the discussion panel (a fresh mount re-runs ensure —
  `usePRReviewConversation.ts:139-177`).
- **Assertions**: `wait.fn` `pr-review-stale-banner` present; banner text contains "was
  updated after you opened it"; a warning toast appears
  (`PRReviewWorkspace.tsx:261-272, 569-583`); `click` its Dismiss button → banner gone.
- **Readback**: `POST …/reviewer` directly → `409`, `code == "stale_subject"`,
  `retryable == true`.
- **Edge rationale**: the stale path is the one place where the connector's view and the git
  view can disagree, and it is the highest-risk untested branch in the reviewer.

#### PRR-D16a — Review submission through the connector route (no UI caller exists)
- **Tier**: **surface**. **Status**: blocked on S1+S2.
- **Round-3 correction**: revision 2 had this as "click Approve → assert the review POST reached
  GitHub". That behavior **does not exist**. The workspace's Approve button calls
  `applyReviewDecision` → `POST /api/workspaces/{ws}/issues/{id}/review-decision`
  (`PRReviewWorkspace.tsx:341-370`, `api/issues/issues.ts:514-520`), and
  `ReviewDecisionService.Apply` **refuses GitHub-linked issues outright**:
  `if isGitHubPullRequestRef(issue.ExternalRef) { return nil, ErrUnavailable("GitHub review
  execution is not configured; Loom state was not changed") }`
  (`internal/webui/service/review_decision.go:63-65`; the matcher accepts
  `https://github.com/…/pull/…` at `:151-154` — precisely this plan's fixture `external_ref`).
  Meanwhile `POST /pull-requests/{o}/{r}/{n}/review` (`handlers.go:108-149`) has **no frontend
  caller at all** — `grep` over `internal/webui/frontend/src` finds no reference to it. The two
  halves of PR review are wired to different, unconnected paths.
- **Intent**: An API client submitting a review through the connector route posts exactly one
  review upstream, after a liveness read, with an idempotency key.
- **Steps**: `api: POST …/pull-requests/acme/widgets/7/review` with
  `{event: "approve", expected_head_sha: <fixture sha>}` → 200 → readback the fake upstream's
  `/__requests` log: exactly one `POST /repos/acme/widgets/pulls/7/reviews` carrying
  `{"event":"APPROVE"}`, preceded by the liveness `GET /repos/acme/widgets/pulls/7`
  (`providers/github.go:169-251`), with an `Idempotency-Key` header present
  (`github.go:515-517`, required by `requireIdempotencyKey` at `:640-645`).
- **Variants**: fixture reports `state: "closed"` → `409 stale_subject`; fixture reports a
  different `head.sha` → `409 stale_subject` (`github.go:232-249`).
- **Why surface and not product-correctness**: no mounted control produces this request. The file
  header must say so, and name the promotion condition: a UI that routes GitHub-linked review
  decisions to the connector route.

#### PRR-D16b — Approving a GitHub-linked review issue reports the honest refusal
- **Tier**: product-correctness (extends `suites/pr-workspace-degraded.test.yaml`).
  **Status: ready after gate G1.** New in revision 3, and the more valuable half.
- **Intent**: A reviewer approving a pull-request-linked ticket is told GitHub review execution is
  not configured, and the ticket is left untouched rather than half-closed.
- **Preconditions**: the degraded suite's existing fixture — a `review`-status issue whose
  `external_ref` is `https://github.com/acme/widgets/pull/7`.
- **Steps**: open the review workspace → `click: { role: button, name: Approve }` →
  `wait: { text: "GitHub review execution is not configured" }` (the service message reaches the
  UI through `showToast(err.message)`, `PRReviewWorkspace.tsx:371-376`).
- **Assertions**
  - The workspace stays open — `expect.visible pr-review-workspace` — because `decide()` only
    calls `onBack()` on success (`:364-370`).
  - `api:` readback `GET …/issues/{id}` → `data.status` is still `"review"` and `data.labels` does
    **not** contain `needs-revision`: the refusal happens before any Loom mutation, which is what
    "Loom state was not changed" promises.
  - Same for `Request changes`.
- **Edge rationale**: this is the current, shipping behavior of the primary decision control on
  the primary PR surface, and nothing covers it. `surface-suites/review-actions.test.yaml` only
  ever approves issues **without** an `external_ref`, so it silently exercises the other branch —
  which is exactly why this gap survived. Pinning the refusal also makes B5 (below) a visible
  product decision instead of a quiet dead end.

#### PRR-D17 — Chat unavailable states
- **Tier**: **needs-new-seam** (S3 `seed-session`).
- **Intent (unsupported)**: A reviewer on a backend whose conversation cannot be read is told
  to use the Terminal tab instead of watching an empty chat.
- **Intent (failed)**: A reviewer whose review worktree disappeared is told to reopen the
  reviewer.
- **Why blocked**: both need `lead_runtime_provider` (and for `failed`, a missing
  `RememberedAgentWorktree`) in the reviewer's orchestration-session metadata; nothing in the
  aft toolbox can write that today. Transcript and worktree seeding alone are **not** enough —
  `readReviewerSnapshot` dispatches on the metadata provider *first* (`stream.go:140-150`), and
  an absent provider takes the codex branch and reports `starting` forever.
- **With S3**
  - `unsupported`: seed `provider=opencode` → `harnessTranscriptReaders` has no opencode entry
    (`harness_read.go:48-51`) → the `!ok` branch (`:66-69`) → assert `pr-chat-unavailable`
    visible, detail text contains `not available for the opencode backend`,
    `pr-chat-open-terminal` visible, `expect: { enabled: { testid: pr-chat-composer, equals: false } }`.
  - `failed`: seed `provider=claude` + a harness session id, then remove the worktree
    registration → `:86-91` → assert the `failed` copy ("Close and reopen the reviewer") and
    that `pr-chat-open-terminal` is **absent** (it renders only for `unsupported`,
    `PRDiscussionPanel.tsx:123-131`).
- **Use `opencode`, not `gemini`** (corrected): the metadata-only variant runs no CLI at all, so
  any provider string works — but if the case is ever extended to boot a real reviewer, only
  claude/codex/cursor-agent/opencode have stubs (`e2e/stubs/`). There is **no `gemini` stub** in
  `e2e/stubs/` or in any `e2e/stubs-real-*` farm, so a gemini-backed agent silently resolves the
  host's `gemini` — nondeterminism plus possible real spend. opencode also gives the simpler
  copy to assert.
- **Real-tier alternative**: PRR-R3 reaches `unsupported` for real with gemini — but only after
  the harness plumbing in that case's preconditions exists.

#### PRR-D18 — Reviewer backend migration
- **Tier**: surface. **Status**: blocked on **S1+S2+S3** (revision 2 said S1+S2 — corrected).
- **Intent**: An API client that changes the workspace backend and re-ensures the reviewer gets a
  reviewer rebuilt on the new backend, with the previous runtime's identity keys cleared.
- **Round-3 correction — the interesting assertion was vacuous.** Revision 2 asserted the agent's
  `backend` flipped and that no agent terminal tab remained. The second half proves nothing if no
  tab was ever launched, and neither half touches what `migrateReviewer` actually exists to do:
  strip `lead_runtime_*`, `codex_*`, and `lead_harness_*` from the orchestration session so a
  leftover endpoint or session id cannot point the conversation reader at the **previous** backend's
  transcript (`reviewer.go:477-495`, prefixes at `:525`, clearing at `:527-551`).
  Proving that requires *seeded* pre-migration metadata — hence S3.
- **Steps**: ensure reviewer on backend A → **S3-seed** `lead_runtime_provider` +
  a `codex_*`/`lead_harness_session_id` key on the reviewer's session → confirm
  `GET …/conversation` reflects that provider (e.g. `unsupported` for opencode) →
  `PATCH /api/workspaces/<ws>/config/backend` to B (the pattern the `real-suites-<backend>` suites
  use) → `POST …/reviewer` again.
- **Assertions**: `run:` readback over `GET …/agents` → the reviewer's `backend` is B; the
  conversation's state **changes away** from the seeded provider's signature (the observable proxy
  for cleared metadata, since session metadata has no HTTP surface); and, if a tab was launched,
  no `kind == "agent"` tab survives for that reviewer.
- **Backend choice matters**: A and B must both be stubbed in `e2e/stubs/` (claude, codex,
  cursor-agent, opencode — there is no `gemini` stub, see S7), or the migrated reviewer resolves a
  real host CLI. `reviewerBackend` also silently rewrites anything uncontrolled to codex
  (`reviewer.go:469-475`), so pick from the controlled ∩ stubbed intersection.

#### PRR-D19 — Legacy reviewer retirement
- **Tier**: surface. **Status**: blocked on S1+S2.
- **Intent**: An API client ensuring a reviewer removes the previous-generation reviewer
  agents for the same pull request.
- **Steps**: pre-create agents named `review-widgets-pr-7` (legacy, `reviewer.go:76-82`) and
  `review-acme-widgets-pr-7` (intermediate, `reviewer.go:64-74`) via `POST …/agents` →
  `POST …/reviewer` → `run:` readback over `GET …/agents` → neither legacy name appears in
  `data`, and the hashed name does (one fetch, three python assertions).

#### PRR-D21 — Upstream error classes map to their documented codes
- **Tier**: surface. **Status**: blocked on **S1+S2** (revision 2 said S1 alone — corrected:
  forcing an upstream status only matters *after* `ensureConnectorAndGrants` succeeds
  (`seed.go:36-49`), which requires a configured credential, so S2 is in the critical path too).
  Needs the fixture's `POST /__fixture` status+header control. Added in revision 2.
- **Intent**: An API client whose GitHub upstream rate-limits or errors receives the documented
  retryable code rather than a generic 500.
- **Cases**, driven by forcing the fake upstream's response to the PR read:
  | forced upstream response | expected | source |
  |---|---|---|
  | `429` | `429` `rate_limited`, `retryable: true` | `errors.go:44-45`, `github.go:539-547` |
  | `403` + `Retry-After: 1` | `429` `rate_limited` (GitHub's 403-shaped rate limit) | `github.go:563-568` |
  | `403` + `X-RateLimit-Remaining: 0` | same | `:564` |
  | `403` + body `{"message":"API rate limit exceeded"}` | same (message sniff) | `:567` |
  | `500` | `502` `upstream_error`, `retryable: true` (`ClassServerError`) | `errors.go:47-48`, `github.go:549-559` |
  | `422` | `502` `upstream_error`, `retryable: false` (`ClassClientError`) | same |
  | `200` with a body missing `head.sha` | `502` `upstream_error`, message `pull request read response missing head sha` | `reviewer.go:245-249` |
- **Fixture requirement**: `POST /__fixture` must be able to set the status **and** response
  headers, not just the body — three of the seven rows are header-driven. Fold that into S1's spec.
- **Edge rationale**: `writePRReviewError`'s seven-way mapping (`errors.go:30-60`) is the shared
  error contract for every PR route and nothing exercises any branch except
  `egress_unavailable`. The retryable flag matters to the UI, which shows Retry off it.

#### PRR-D22 — PR list: upstream query contract (API) + local filtering (UI)
- **Tier**: **surface** for the query contract; product-correctness for the local-filter half.
  **Status**: blocked on S1+S2, and the UI half additionally on S8. Rewritten in revision 3.
- **Round-3 correction — the UI cannot drive any of this.** Revision 2 framed these as
  "an operator filtering the review queue causes query X". `PRsPage` hardcodes
  `usePullRequests({ state: "all" })` (`views/PRsPage.tsx:208-210`) and filters rows **client-side**
  from local `useState<PRFilter>("all")` (`:211`, pills at `:420-470`). A pill click issues no
  request. So `state=review` and `state=merged` are reachable **only** by calling the API directly,
  and the honest split is two separate cases.
- **22a — upstream query contract (surface, API only)**
  - `GET …/pull-requests?state=open` → the fixture log shows one
    `GET /repos/acme/widgets/pulls?page=1&per_page=100&state=open`. **Alphabetical key order**:
    `url.Values.Encode()` sorts, and `page` < `per_page` < `state`
    (`providers/github.go:286-307`, appended at `:495-499`). Revision 2 asserted
    `state=open&per_page=100&page=1`, which would never match. `pullsListPerPage = 100` (`list.go:22`).
  - `?state=review` → upstream receives `state=open` (`connectorListState`, `list.go:184-189`).
  - `?state=merged` → **zero** `/repos/…/pulls` requests in the log: it short-circuits to the gh
    path to avoid N failing 422s (`list.go:44-48`). Needs S8, or the assertion is "no connector
    request" while a live `gh` runs underneath.
  - `?state=all` (what the UI actually sends) → upstream receives `state=all`.
  - Pagination: a 101-PR fixture proves page 2 is requested at all (the loop stops when a page
    returns fewer than `perPage` — `list.go:136-144`). A >500-PR fixture additionally trips
    `pullsListTruncationWarning` (`:147-149`). Both optional; the 101-PR variant is the cheap one.
- **22b — state normalization reaches the row chrome (product-correctness)**
  - Fixture PR with `state: "closed"` + non-null `merged_at` → `MERGED`; an open one → `OPEN`
    (`normalizePullState`, `list.go:243-259`; `pullMerged`, `github.go:630-635`).
  - Assert the rendered row's `[data-pr-state]` (`views/PRsPage.tsx:307-310`) plus the API
    readback. This matters because the frontend's filter counts key off these exact uppercase
    strings — a lowercase leak renders an empty list.
  - Then assert the **local** filter: click the `Merged` pill and confirm the open row disappears
    while **no** new `pull-requests` request is made (the point of the correction).
- **Edge rationale**: revision 1's single happy row proved none of the parameter contracts, and the
  `merged` bypass is a silent behavioral fork a refactor could lose. Splitting the case also
  documents that the PR list's `state` parameter is effectively dead UI-side — itself worth a
  FINDINGS note.

---

## Part 2 — Real-backend tier (non-deterministic)

Placement: `tests/aft/real-suites-claude/` and `tests/aft/real-suites-gemini/` (new dir; add
the `gemini` arm to `run-aft.sh`'s backend switch if it is worth it — see note in R3), gated
by `AFT_REAL_BACKEND` exactly as the existing arms are. **Design rule: no external GitHub.**
The S1 fixture (local bare repo + fake REST) is backend-agnostic, so every real case below
runs against it. Only PRR-R6 needs the internet, and it gets its own gate.

Backend selection rationale: `claude` and `gemini` are the only backends with harness
transcript readers (`harness_read.go:48-51`), so they are the only ones whose reviewer
conversation is readable from disk; `codex` is readable live over its app-server socket.
`opencode`/`cursor` are useful only as `unsupported` fixtures.

#### PRR-R1 — Real backend drives a PR Review terminal
- **Tier**: real (`real-suites-claude/zz-real-pr-review-terminal.test.yaml`).
- **Intent**: A real Claude backend launched with the built-in PR-review prompt announces
  itself and reviews a locally seeded branch on request.
- **Preconditions**: `AFT_REAL_BACKEND=claude`; the S1 working repo added to the workspace
  with a seeded feature branch containing one obvious defect (e.g. an off-by-one in a tiny
  file) so the model has something concrete to find.
- **Steps**: create the agent through the modal (PRR-D1 flow) with backend `claude` → open
  `/ws/<ws>/agents/<name>` → `wait.fn` `.term-row` text contains `PR-REVIEW-READY` (the real
  CLI echoes its first turn; if it does not, fall back to asserting the model's own greeting)
  → send a turn asking it to review `git diff main...feature` → `wait.fn`, with the tier's
  600s timeout, that the terminal text mentions the seeded file name.
- **Assertions**: terminal `launch.argv` readback identical to PRR-D5 (deterministic part);
  non-empty model output referencing the seeded path; **no mutation**: `git -C <repo> status
  --porcelain` empty and `git -C <repo> rev-parse HEAD` unchanged (the visible pr-review
  prompt is allowed to edit, but this scenario asks only for a review, so drift is a finding).
- **Non-determinism policy**: assert on the presence of the file path and the absence of
  writes, never on review wording.

#### PRR-R2 — Real pr-review-checkout reviewer is read-only
- **Tier**: real (`real-suites-claude/`). The flagship real case.
- **Intent**: A reviewer discussing a pull request with a real Claude backend receives a
  grounded review in the chat panel and the checkout is left untouched.
- **Steps**: S1 fixture + credential installed (Settings UI, as PRR-D11) → open the review
  workspace → `pr-discuss-button` → poll `GET …/conversation` until `messages.length > 0`
  (this is the harness-transcript path: `readHarnessReviewerSnapshot` →
  `claudecode.New()` reader, `harness_read.go:63-122`) → type a follow-up into
  `pr-chat-composer`, press `pr-chat-send`, poll until a new assistant message appears.
- **Assertions**
  - At least one `role == "assistant"` message whose text contains the fixture's changed file
    path; the prompt preamble bubble is trimmed (`trimReviewerPreamble`, `stream.go:278-289`)
    so `messages[0].role` must not be a `user` bubble starting with `## READ-ONLY PR REVIEWER`.
  - The user follow-up appears as a `role == "user"` message (proves
    `postReviewerMessage` → `DeliverLeadMessageWithOptions` → transcript round trip,
    `reviewer.go:585-606`).
  - **Read-only proof** (the point of the hidden prompt): in the reviewer worktree,
    `git status --porcelain` empty, `git rev-parse HEAD` still the PR head sha,
    `git rev-list --count HEAD` unchanged, `git stash list` empty; in the bare "GitHub" repo,
    `git rev-parse refs/pull/7/head` unchanged and no new refs; and the fake REST server's
    `/__requests` log contains **no** `POST …/reviews` and no `POST …/issues/7/comments`.
  - Claude's session-id rotation path (`newestClaudeSessionSince`, `harness_read.go:129-162`)
    is exercised implicitly — a fresh worktree always trips the folder-trust rotation, so if
    messages never arrive, that reconciliation is the first suspect.

#### PRR-R3 — Gemini reviewer surfaces `unsupported` honestly
- **Tier**: real (`real-suites-gemini/`, new arm) — **or** skip the new arm and reach the same
  state deterministically once seam S3 lands (PRR-D17). Recommend S3 first; add this arm only
  if a real gemini credential is already part of the operator's kit.
- **Harness work this case requires** (verified in revision 2; larger than it looks):
  1. `run-aft.sh:157-159` hard-fails on any `AFT_REAL_BACKEND` outside
     `codex|claude|opencode|cursor` — a `gemini` arm must be added, with its own preflight
     (gemini has no documented credential file to check, so binary-presence only) and its
     `REAL_UNSET_FLAGS` (likely `-u GEMINI_API_KEY`) so the run uses account auth.
  2. `e2e/stubs-real-gemini/` must be created as a symlink farm covering claude, codex,
     cursor-agent, and opencode.
  3. A `gemini` stub must be added to `e2e/stubs/` **and** symlinked into the four existing
     `stubs-real-*` farms — none of the five contains one today, so every other tier currently
     leaks to a host `gemini` if a workspace is ever configured for it.
  4. A `make test-aft-real-gemini` target, and a decision about `make test-aft-real-all`.
  The product side is already coherent: gemini is a controlled lead backend with no
  launch-pinned session id (`internal/cli/backends/harness_lead_runtime.go:101-102`), which is
  precisely why the reviewer reports `unsupported` (`harness_read.go:78-84`). The cost is all
  harness plumbing, which is why S3 is the better first move.
- **Intent**: A reviewer on a backend that does not expose its session id is told the chat
  view cannot read the conversation and is pointed at the Terminal tab.
- **Assertions**: `pr-chat-unavailable` visible; detail text contains "does not expose its
  session id" (`harness_read.go:81-84`); `pr-chat-open-terminal` visible and clicking it
  selects the Terminal tab, which mounts a live `terminal-wrapper` for the reviewer agent.

#### PRR-R4 — Codex reviewer conversation over the app-server socket
- **Tier**: real (`real-suites/` — the existing codex arm).
- **Intent**: A reviewer discussing a pull request with a real codex backend sees turns stream
  into the chat panel.
- **Assertions**: `GET …/conversation` transitions `starting` → `running`/`idle`
  (`reviewerThreadState`, `stream.go:243-248`); at least one assistant message; the same
  read-only proofs as PRR-R2.
- **Value**: codex is the *default* reviewer backend (`reviewerBackend` falls back to codex,
  `reviewer.go:469-475`), so this is the shipping default path.

#### PRR-R5 — Real failure variants
- **R5a — credential removed mid-flight**: with a working reviewer, clear the GitHub
  credential through Settings (`github-credential-clear-button`), then close/reopen the
  discussion → ensure returns `503 egress_unavailable` → `pr-discussion-error` +
  `pr-discussion-retry` return. Proves `InvalidateCredentialSeeds` actually invalidates
  (`module.go:80-90`) — an assertion no test makes today.
  **Two guard rails** (from the cross-cutting review): (i) this clears the **GitHub** runtime
  credential only — never a backend credential. `run-aft.sh`'s own preflight checks
  `~/.codex/auth.json`, `${CLAUDE_CONFIG_DIR}/.credentials.json`, and `cursor-agent status`
  (`run-aft.sh:180-199`) and never GitHub, so this case cannot trip it; a case that cleared a
  backend credential would poison the operator's *next* run, since the preflight runs before
  the suites. (ii) The suite teardown must re-install the fixture token (or clear it and leave
  it cleared deliberately) so state does not leak between runs — local settings are global, not
  workspace-scoped.
- **R5b — egress cut mid-conversation**: stop the fake REST server while the reviewer is
  mid-turn → the *conversation* keeps serving from the transcript/socket (it does not touch
  the connector), so assert messages persist and only a subsequent `ensure`/`diff`/`review`
  fails. This pins the boundary between "connector is down" and "reviewer is down", which the
  UI currently conflates in one error strip.
- **R5c — reviewer runtime killed**: kill the reviewer PTY (`DELETE …/terminal/tabs/{session}`)
  and poll the conversation → expect `reconnecting` (codex) or a `failed`/`starting` harness
  state; assert the panel does not crash and the composer locks when `chatUnavailable`.

#### PRR-R6 — Against real github.com (separate opt-in gate)
- **Tier**: real, gated behind a **new** `AFT_REAL_GITHUB=1` in addition to
  `AFT_REAL_BACKEND`, with its own suite dir `real-suites-github/`.
- **Intent**: The reviewer's `refs/pull/<n>/head` fetch and the connector's PR read agree
  against the real GitHub API.
- **Why separate**: it needs a real PAT, a real repository with an open PR, and network
  egress; it is the only case the local fixture cannot prove (that `refs/pull/N/head` exists
  server-side at all). Keep it small: ensure reviewer, assert `checked_out_sha` equals the
  PR head from `gh pr view`, assert read-only, delete the reviewer.
- **Do not** add it to any `test-aft-real*` aggregate target.

---

## Part 3 — Blockers and new seams

### S1 — Hermetic GitHub: fake REST upstream + local "GitHub" bare repo
**Blocks**: PRR-D11…D19, PRR-R2…R5. Resolves the first bullet of **FINDINGS §3.9**.

Two halves, both already half-built in the repo.

**S1a — connector egress.** `LOOM_CONNECTOR_GITHUB_BASE_URL` is an existing, documented
seam (`internal/connector/registry_default.go:15-22, 34`), already used by
`deploy/podman-stack/compose.e2e.yaml:33`. The existing `deploy/podman-stack/stub-upstream/server.mjs`
is a **generic echo recorder**, not GitHub-shaped: it answers every request with
`{ok, recorded, echo}`, which `pullRequestRead` would reject as "missing head sha"
(`reviewer.go:245-249`). Build a GitHub-shaped fixture server — suggested location
`tests/aft/fixtures/fake-github/server.mjs`, started and stopped by `run-aft.sh` alongside the
stack, on a port exported as `AFT_FAKE_GH_PORT`:

| route | response |
|---|---|
| `GET /repos/{o}/{r}/pulls` | array of PR objects (honor `state`, `per_page`, `page`) |
| `GET /repos/{o}/{r}/pulls/{n}` | `{number, state, title, draft, merged_at, user:{login}, updated_at, head:{sha,ref}, base:{sha,ref}}` |
| `GET /repos/{o}/{r}/compare/{base}...{head}` | `{status, ahead_by, behind_by, total_commits, files:[{filename,status,additions,deletions,patch}]}` |
| `POST /repos/{o}/{r}/pulls/{n}/reviews` | `{id, state}` (201) |
| `POST /repos/{o}/{r}/issues/{n}/comments` | `{id}` (201) |
| `GET /__requests`, `POST /__reset` | request log (copy from the podman stub, incl. header redaction). Must record the **query string**, not just the path — PRR-D22 asserts `state`/`per_page`/`page` |
| `POST /__fixture` | **control plane**: set/patch the PR fixture — head sha, state, draft, files — **or** force a response `status` **and arbitrary response headers**. Headers are not optional: three of PRR-D21's seven rows are header-driven (`Retry-After`, `X-RateLimit-Remaining`), and one needs a 200 whose body omits `head.sha` |

Field names must match `pullSummary` / `compareFiles` (`internal/connector/providers/github.go:381-413, 613-635`).
`POST /__fixture` is what makes PRR-D15 (stale) and the rate-limit/upstream error mappings
(`errors.go:30-60`) reachable.

**S1b — the git side.** `ensureReviewer` does a **real** `git fetch <remote>
+refs/pull/<n>/head:refs/loom/pr/<n>/head` (`localworkspace.go:225-231`). A local **bare**
repository can hold `refs/pull/7/head` — verified by experiment:

```
git init --bare gh/acme/widgets.git
# push main + a PR branch, then:
git -C gh/acme/widgets.git update-ref refs/pull/7/head <pr tip sha>
```

The constraint is that `parseGitHubOwnerRepo` only accepts `github.com` URLs
(`membership.go:36-50`), while the fetch must resolve locally. Two options, one of which is
a trap:

- **Do not use `url.<base>.insteadOf`.** Verified experimentally: `git remote get-url origin`
  **expands** insteadOf, and `gitRemoteURL` (`internal/cli/serve/workspacemgr/workspace_store.go:359-368`)
  uses exactly that command — so the stored `RemoteURL` would become `file://…` and every
  reviewer request would 404 `repo_not_registered`.
- **Use register-then-repoint.** `RemoteURL` is captured **once**, at repo-registration time
  (`workspace_store.go:127, 336, 447`), and read back from the store afterwards
  (`storeadapter.go:154`); there is no PATCH-repos route to recompute it. So:
  1. `git -C prc-widgets remote add origin https://github.com/acme/widgets`
  2. `POST /api/workspaces/<ws>/repos` (stored `RemoteURL` = the github.com URL, `Remote` = `origin`)
  3. `git -C prc-widgets remote set-url origin "file://$AFT_WORK_DIR/gh/acme/widgets.git"`

  Add a `run:` readback right after step 3 asserting `GET …/repos` still reports the
  github.com `remote_url` — that guard is what will catch a future resync that recomputes it.

**Wiring**: export `LOOM_CONNECTOR_GITHUB_BASE_URL` in **both** `env` invocations in
`tests/aft/run-aft.sh` (the real-backend branch and the default branch, around the
`start-e2e-server.sh` launch). This is safe for the existing degraded suites: without a
credential, `ensureConnectorAndGrants` fails before any HTTP call
(`seed.go:36-49`, `resolveGitHubToken` at `:127-147`), so `pr-contracts` and
`pr-workspace-degraded` keep seeing `503 egress_unavailable`.

### S2 — Credential install/clear as a first-class fixture step (mostly exists)
**Blocks**: the same set as S1.

No new product seam is needed — `PATCH /api/local/settings`
(`internal/webui/app/routes.go:50-51`) with
`{"runtime_credentials":{"github":{"token":"…"}}}` installs the PAT and triggers
`InvalidateCredentialSeeds`, and the Settings UI exposes it through mounted controls
(`github-token-input`, `github-credential-save-button`, `github-credential-clear-button`,
`github-settings-panel` — `SettingsView.tsx:677-724`). Prefer the **UI** path in tier 1
(actor fidelity: an operator configures their own credential) and the API path in setup/teardown.

What **is** missing is hermeticity — and revision 2 promotes this from a nicety to a **hard
prerequisite that must land before any suite in Group E is written**:

1. `LOOM_WEBUI_GITHUB_TOKEN` (`prreview/module.go:22`) is read straight from the server's
   environment, and `resolveGitHubToken` checks it **first, returning before it ever looks at
   saved local settings** (`seed.go:127-129`). `run-aft.sh` inherits the operator's shell
   wholesale. Two distinct failures, the second serious:
   - a developer with that variable exported silently un-degrades `pr-contracts` and
     `pr-workspace-degraded` (both assert `503 egress_unavailable`);
   - in the connected suite it **shadows** the fixture credential entirely, so the run points a
     **real** PAT at whatever `LOOM_CONNECTOR_GITHUB_BASE_URL` names — or at real github.com if
     that variable is also unset. A test suite that can reach production GitHub with the
     operator's token is not acceptable.

   **Fix**: unset it on the server launch in **both** branches of `run-aft.sh:285-296`.
   Note the shapes differ: the real branch already has an `env $REAL_UNSET_FLAGS …` prefix to
   extend, but the **default branch has no `env` prefix at all** (`:292-296` uses bare
   `VAR=value` assignments), so it needs one introduced:
   `env -u LOOM_WEBUI_GITHUB_TOKEN E2E_PORT=… bash …`. Add a `run:` guard in the connected
   suite's `setup:` that fails fast if the variable is set in the *server's* view — cheapest
   proxy: assert `GET …/pull-requests` reports the connector-unavailable warning before the
   credential is installed.
2. The credential lands in `bootstrap.LoomDir()`, which honors `LOOM_CONFIG_DIR`
   (`internal/bootstrap/paths.go:39-51`) and is already isolated to
   `tmp/e2e-workspace/.loom-config` by `scripts/start-e2e-server.sh` — good, but the
   connected suite must still clear it in teardown, because local settings are **global**
   (not workspace-scoped) and aft runs suites alphabetically in one process. Hence the `zz-`
   prefix on `zz-pr-review-connected`.

### S3 — `loom daemon seed-session`: orchestration-session runtime metadata
**Blocks**: PRR-D17 (`pr-chat-unavailable`, both `unsupported` and `failed`), and any
deterministic reviewer-conversation content.

Why nothing cheaper works: `readReviewerSnapshot` dispatches on
`sess.Metadata[lead_runtime_provider]` **before** touching any transcript or worktree
(`stream.go:140-150`). An absent provider takes the codex branch and reports `starting`, so
seeding a transcript or removing a worktree changes nothing observable. The metadata is the gate.

This is exactly the "remaining high-value candidate" named in **FINDINGS §3.10**. Spec,
following ADR-0001 and the existing `seed-log` / `seed-worktree` shape
(`internal/cli/daemon/seed_log_cmd.go`, `seed_worktree_cmd.go`, gated by
`internal/cli/daemon/seed_gate.go:14-16`):

```
LOOM_TESTSUPPORT=1 loom daemon seed-session \
  --workspace <ws> --agent <name> \
  [--provider claude|codex|gemini|opencode|cursor] \
  [--runtime-status active|idle|waiting_user_input|disconnected|starting] \
  [--harness-session-id <uuid>] \
  [--codex-endpoint <addr> --codex-thread <id>] \
  [--started-at <rfc3339>]
```

It must go through the product's own writer — `store.OrchestrationSessionFor` plus
`AgentSessions().Update` with the metadata keys owned by
`internal/leadcontrol/codex_metadata.go:15` (`lead_runtime_provider`) and
`internal/leadcontrol/harness_metadata.go:22` (`lead_harness_session_id`, and the
`lead_runtime_*` family) — never hand-written map keys in the test, or the seam drifts from
the runtime exactly the way ADR-0001 was written to prevent.

With S3 plus the existing `seed-transcript`, a deterministic reviewer conversation becomes
possible: seed `provider=claude` + a harness session id, write a claude-format transcript
into the worktree's project dir, and assert real chat bubbles with no model call.

### S4 — gh-less review-content seed
**Blocks**: promoting `surface-suites/review-actions.test.yaml` back to tier 1 (FINDINGS §3.9
bullet 4, §1.19).

S1b largely delivers this: the bare-repo + working-checkout fixture *is* reviewable branch and
commit content, and `seed-worktree` (`--file/--content/--message`) already commits
agent-attributed changes. The missing piece is a fixture helper that both suites can share.
Recommend `tests/aft/scripts/seed-pr-fixture.sh` emitting a small JSON manifest into
`$AFT_WORK_DIR` (`{bare, repo, owner, repo_name, pr_number, head_sha, base_sha, changed_file}`)
so `pr-workspace-degraded`, `pr-contracts`, `review-actions`, and the new connected suite all
build the same world instead of four copies of an inline `run:` block. Today those four
suites each re-implement the same 15-line git+curl incantation.

### S5 — Controlled PR Review prompt observation without a real model
**Status**: delivered for PRR-D5 at the prompt-contract boundary.

The deterministic Codex stub now speaks the controlled app-server/remote protocol. The prompt
therefore crosses the production launch seam, and the remote side reports a stable fingerprint,
safety-block count, and exact built-in PR Review identity before holding the PTY open. This proves
delivery without echoing custom or secret-shaped prompts into screenshots. The identity check is
derived from the non-secret built-in prompt header (`PR-REVIEW-READY` plus the PR Review mode
heading); PRR-R1 still owns literal human-visible model behavior.

### S6 — Missing testids and small product gaps
- `PRReviewWorkspace` decision buttons (`✗ Request changes` / `✓ Approve`,
  `PRReviewWorkspace.tsx:550-565`) have no testids; both existing suites match them by
  accessible name. Add `pr-review-approve` / `pr-review-request-changes`.
- The stale banner's Dismiss button (`PRReviewWorkspace.tsx:579-581`) has no testid.
- `PRDiscussionPanel`'s Close button is matched only by `aria-label="Close discussion"`.
- The reviewer agent chip (`PRReviewWorkspace.tsx:437-460`) has no testid, so
  "who is reviewing this PR" is only assertable through text.
- `AddRepoModal` inputs and the diff components are already logged in FINDINGS §3.9; the PR
  plan depends on the diff ones for PRR-D12.

### S7 — The `gemini` stub gap (harness hermeticity, found in revision 2)
**Blocks**: PRR-R3; also a latent hazard for PRR-D17/D18 if either is ever written to boot a
real reviewer on gemini.

`gemini` is a first-class controlled lead backend (`internal/cli/backends/harness_lead_runtime.go:101-102`)
and a first-class reviewer backend (`leadcontrol.IsControlledLeadBackend` accepts it, so
`reviewerBackend` will hand it straight through — `reviewer.go:469-475`). But there is **no
`gemini` stub anywhere in the test tree**: `e2e/stubs/` holds claude, codex, cursor-agent,
opencode, and each of `e2e/stubs-real-{claude,codex,cursor,opencode}/` holds the other three.
A workspace configured with backend `gemini` therefore resolves whatever `gemini` is on the host
PATH — in *every* tier, including the deterministic one that `make test-aft` runs in CI.

**Fix** (small, and worth doing independently of this plan): add `e2e/stubs/gemini` following the
`claude` stub's shape, and symlink it into all four `stubs-real-*` farms. That closes the leak
for any current or future suite that touches the gemini backend, and is a prerequisite for a
`real-suites-gemini` arm.

### S8 — A deterministic `gh` (gate G2, found in revision 3)
**Blocks**: PRR-D8e, PRR-D22's merged-bypass and UI halves. **Also fixes two existing suites.**

The plan assumed a "gh-less stack" throughout revisions 1–2. That is false. The chain:

1. `run-aft.sh:292-296` sets `PATH="$REPO_ROOT/e2e/stubs:$PATH"` — the host tail is preserved.
2. `e2e/stubs/` contains `claude`, `codex`, `cursor-agent`, `opencode`. **No `gh`.**
3. Without a credential, `listPullRequests` falls back to `ghListFallback` (`list.go:33-40`) →
   `agentSvc.ListPullRequests` → `CheckGhInstalled` runs `gh --version`
   (`svcimpl/agent_service.go:266`; `internal/cli/git/pr.go:347-353`).
4. If that succeeds, `ListWorkspacePullRequests` runs a real
   `gh pr list --state <s> --limit 500 --json number,title,url,…` in each repo
   (`internal/cli/git/pr_list.go:42-49`).

On CI (no `gh`) the result is a stable warning. On a developer machine — `gh` is at
`/opt/homebrew/bin/gh` on the box this plan was written on — it is a **live GitHub API call for
`acme/widgets`**, a repository the fixture invented. Nondeterministic, network-dependent, and
noisy against someone else's namespace.

**Fix**, cheapest first:

- **Option A (preferred): add `e2e/stubs/gh`.** A small script mirroring the existing stubs' shape:
  `--version` prints a fixed version and exits 0; `pr list … --json …` prints `[]` (or a fixture
  array driven by an env var / file); anything else exits non-zero with a recognizable message.
  This makes the fallback path *deterministic and assertable* rather than merely absent, which is
  what PRR-D8e needs. Symlink it into all four `e2e/stubs-real-*` farms too, exactly as S7 does
  for `gemini`.
- **Option B: hide `gh`.** Have the harness prepend a directory containing a `gh` that always
  fails, forcing the "not installed" branch. Simpler, but it pins the *less* interesting message
  and cannot express "gh worked and returned rows".

Option A also unlocks a case this plan does not currently list: the **gh-backed** PR list path
(`pullRequestFromSummary`'s sibling in `internal/cli/git/pr_list.go:70-90`, plus `ops` shaping),
which is the path most users on a gh-authenticated machine actually hit and which no test covers.
Worth a follow-up case once the stub exists.

### B4 — Product question surfaced by this plan (not a test blocker)
`PRReviewWorkspace`'s "+ New review agent…" opens `CreateAgentModal` with
`defaultName="review-<issue-id>"` and `defaultRoleName="task"` and **no** `defaultKind`
(`PRReviewWorkspace.tsx:637-644`), so the agent it offers to create is a background Task
Runner, not the PR Review interactive template it is named after. PRR-D7 pins the current
behavior; whether it should pass `defaultKind="interactive"` and pre-select `pr-review`
belongs in FINDINGS §1 as a product decision, not in the test.

---

### B5 — The two halves of PR review are not connected (found in revision 3)
Not a test blocker — a product finding this plan surfaced, and the reason PRR-D16 had to split.

There are two review-submission paths and no bridge between them:

- `POST /api/workspaces/{ws}/issues/{id}/review-decision` — what the **UI** calls
  (`PRReviewWorkspace.tsx:341-370`, `api/issues/issues.ts:514-520`). It refuses any issue whose
  `external_ref` is a GitHub PR URL: *"GitHub review execution is not configured; Loom state was
  not changed"* (`internal/webui/service/review_decision.go:63-65, 151-154`).
- `POST /api/workspaces/{ws}/pull-requests/{o}/{r}/{n}/review` — a complete, connector-backed,
  idempotent, precondition-checked implementation (`handlers.go:108-149`) with **no frontend
  caller** (no reference anywhere under `internal/webui/frontend/src`).

So on the PR review workspace — the surface built for reviewing GitHub PRs — the Approve button
cannot approve a GitHub PR, while the endpoint that can is unreachable from the UI. PRR-D16b pins
the current refusal so the behavior is at least honest and observed; PRR-D16a covers the working
endpoint at the surface tier. Wiring `review-decision` to dispatch through the connector route for
GitHub-linked issues would collapse both into one product-correctness case and is the natural
promotion condition to record in the suite header.

---

## Coverage table

`ready` = writable against today's stack once its **gate** has landed. `S1`/`S2`/`S3`/`S5`/`S8` =
blocked on that seam. "Existing" names what already pins the surface, so deltas are visible.

**Gates** (revision 3 — these are harness fixes, not seams, but no affected case may be trusted
before they land):

- **G1** — `LOOM_WEBUI_GITHUB_TOKEN` unset in both `run-aft.sh` server launches (S2). Required by
  every case that touches a `pull-requests` route or opens `/prs`, because an inherited token turns
  `503 egress_unavailable` into a real call to `api.github.com`.
- **G2** — a deterministic `gh` (S8). Required by any case whose assertion depends on the PR-list
  fallback; recommended for every case that loads `/prs`, since they all shell out to real `gh`
  today.

| id | case | happy / edge | tier | status | already pinned by |
|---|---|---|---|---|---|
| PRR-D1 | modal creates a PR Review interactive agent | happy | product-correctness | ready | — (template has zero coverage) |
| PRR-D2 | template selection semantics + true default | edge | product-correctness | ready | — |
| PRR-D3 | interactive-prompts contract; checkout prompt hidden | edge | product-correctness + readback | ready | — |
| PRR-D4 | prompt-list fallback notice (`offline:`) | edge | product-correctness | ready | — |
| PRR-D5 | connected controlled runtime receives `builtin:pr-review` contract | happy | product-correctness | ready | controlled Codex app-server/remote stub |
| PRR-D5b | `PR-REVIEW-READY` visible in terminal | edge | real-backend | PRR-R1 | `prompts_test.go:674` (unit) |
| PRR-D6a | second PR Review agent reuses the role | edge | product-correctness | ready | — |
| PRR-D6b | role-name conflict with a different prompt | edge | surface | ready | — |
| PRR-D7 | "+ New review agent…" prefill + assignment | happy | product-correctness | ready **(G1)** | — |
| PRR-D8a | Retry keeps the degraded panel alive | edge | product-correctness | ready **(G1)** | `pr-workspace-degraded` (panel/error/retry rendered, never clicked) |
| PRR-D8b | composer/send state with no reviewer | edge | product-correctness | ready **(G1)** | — |
| PRR-D8c | Terminal tab with no reviewer | edge | product-correctness | ready **(G1)** | — |
| PRR-D8d | close + reopen the discussion panel | edge | product-correctness | ready **(G1)** | `pr-workspace-degraded` (open only) |
| PRR-D8e | `prs-github-warning` when GitHub is unreachable | edge | product-correctness | **S8** *(was ready in rev 2 — the stack is not gh-less)* | `review-queue` (empty state only) |
| PRR-D9a | `POST messages` → 404 `reviewer_not_started` | edge | surface | ready **(G1)** | `pr-contracts` (conversation 404 only) |
| PRR-D9b | `GET stream` → 404 `reviewer_not_started` | edge | surface | ready **(G1)** | — |
| PRR-D9c | unregistered repo → 404 `repo_not_registered` | edge | surface | ready **(G1)** | — |
| PRR-D9d | invalid owner/number → 400 `invalid` | edge | surface | ready **(G1)** | — |
| PRR-D9e | review without `expected_head_sha` → 428 | edge | surface | ready **(G1)** | — |
| PRR-D9f | invalid review event → 400 | edge | surface | ready **(G1)** | — |
| PRR-D10b | case-insensitive repo membership canonicalization | edge | surface | ready **(G1)** **(new in rev 2)** | — |
| PRR-D20 | unknown `builtin:` prompt → visible launch error; hidden id accepted | edge | product-correctness + surface | ready **(redesigned in rev 2)** | — |
| PRR-D11 | PR queue lists connector PRs, no warning | happy | product-correctness | **S1+S2** | — |
| PRR-D12 | compare diff renders in the review workspace | happy | product-correctness | **S1+S2** | — |
| PRR-D13 | reviewer happy path: detached PR-head checkout | happy | product-correctness | **S1+S2** | `pr-contracts` pins only the 503/404 |
| PRR-D14 | degraded → credential → Retry recovers | happy | product-correctness | **S1+S2** | — |
| PRR-D15 | stale head → `pr-review-stale-banner` | edge | product-correctness | **S1+S2** | — (FINDINGS §3.9) |
| PRR-D16a | review submission through the connector route (no UI caller) | happy | surface | **S1+S2** *(rev 2 wrongly had this as a UI approve click)* | `review-actions` (loom-issue side, non-PR issues only) |
| PRR-D16b | approving a PR-linked issue reports the honest refusal | edge | product-correctness | ready **(G1)** **(new in rev 3)** | — |
| PRR-D17 | `pr-chat-unavailable` (unsupported / failed) | edge | product-correctness | **S3** | — (FINDINGS §3.9) |
| PRR-D18 | reviewer backend migration clears runtime metadata | edge | surface | **S1+S2+S3** *(rev 2 assertion was vacuous)* | — |
| PRR-D19 | legacy reviewer retirement | edge | surface | **S1+S2** | — |
| PRR-D21 | `rate_limited` / `upstream_error` class mappings (7 rows) | edge | surface | **S1+S2** (needs header-forcing fixture) **(new in rev 2)** | — |
| PRR-D22 | upstream query contract (22a) + local filtering (22b) | edge | surface + product-correctness | **S1+S2**, 22b also **S8** *(rev 2 misstated UI causality)* | — |
| PRR-R1 | real backend drives a pr-review terminal | happy | real (claude) | needs S1b fixture | — |
| PRR-R2 | real pr-review-checkout reviewer, read-only proven | happy | real (claude) | needs S1+S2 | — |
| PRR-R3 | gemini reviewer → `unsupported` | edge | real (gemini, new arm) | needs S1+S2 **+ S7 + a new `run-aft.sh` arm**; prefer S3 instead | — |
| PRR-R4 | codex reviewer conversation over app-server | happy | real (codex) | needs S1+S2 | — |
| PRR-R5a | credential cleared mid-flight → 503 returns | edge | real | needs S1+S2 | — |
| PRR-R5b | egress cut mid-conversation | edge | real | needs S1+S2 | — |
| PRR-R5c | reviewer PTY killed → reconnecting/failed | edge | real | needs S1+S2 | — |
| PRR-R6 | against real github.com | happy | real, separate `AFT_REAL_GITHUB=1` gate | opt-in only | — |

**Totals (revision 3)**: 42 cases — **21 deterministic ready-to-write** (13 of them behind gate
G1), **13 deterministic blocked on a seam** (9 on S1+S2, 1 on S1+S2+S3, 1 on S8, 1 on S3, 1 on S5),
**8 real-backend**. Counted off the rows above.

Revision history of the count: rev 1 held 38 rows (20 ready / 10 blocked / 8 real, mis-stated as
21/10/7); rev 2 reached 41 (+D10b ready, +D21 +D22 blocked); rev 3 reaches 42 — D16 splits into
D16a (blocked) + D16b (ready), and D8e moves from ready to blocked.

**Order of work (revised — the gates come first).** Revision 2 put the 20 no-seam cases first;
that was unsafe, because 13 of them touch a `pull-requests` route or `/prs` and an inherited
credential inverts their expected behavior while sending real traffic.

1. **Gate G1 — unset `LOOM_WEBUI_GITHUB_TOKEN`** (part of S2). Two lines in `run-aft.sh:285-296`,
   noting the default branch needs an `env` prefix introduced rather than extended. Treat as a
   **bug fix**: today a developer with that variable exported has a test suite that reaches
   production GitHub with their own PAT (`seed.go:127-129`).
2. **Gate G2 — a deterministic `gh`** (S8). Also a bug fix: `suites/review-queue.test.yaml` and
   `suites/pr-workspace-degraded.test.yaml` already shell out to real `gh pr list` for
   `acme/widgets` on any machine with `gh` installed (`internal/cli/git/pr_list.go:42-49`).
3. **The 8 gate-free template cases** — D1, D2, D3, D4, D5, D6a, D6b, D20. No GitHub surface, no
   gate, no seam. This is where writing actually starts, and it takes the PR Review template from
   zero coverage to pinned.
4. **The 13 gated cases** — D7, D8a–D8d, D9a–D9f, D10b, D16b. All ready once G1 lands.
   (Groups A + B + D together hold **20** ready cases; D7 is Group C, giving 21 — revision 2's
   "22" was wrong.)
5. **S1 + S2 in full** — one fixture server, one bare repo, the credential flow, one teardown.
   Unblocks 10 cases and retires the FINDINGS §3.9 staleness blocker (PRR-D15) plus its
   gh-less review-content bullet (S4 rides along).
6. **S7** — the missing `gemini` stub. Small, independent, closes another live hermeticity leak.
7. **S3 `seed-session`** — unblocks D17, repairs D18, and makes deterministic reviewer
   conversations possible; the last item FINDINGS §3.10 has queued.
8. **S5** — decline unless someone specifically wants the sentinel on screen without a model.

**Product findings to file regardless of test work**: B5 (the UI's Approve cannot approve a GitHub
PR while the endpoint that can is unreachable), B4 (the "New review agent" prefill creates a Task
Runner), and the observation under D22 that the PR list's `state` parameter is dead UI-side.
