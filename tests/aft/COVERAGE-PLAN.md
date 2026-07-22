# aft coverage plan for docs/qa/feature-user-stories.tsv

Source: `docs/qa/feature-user-stories.tsv` @ `fe8816d4` — 127 stories, 10 feature groups,
91 P0. Test status today: **all rows "not run"** (coverage exists as unit tests and the
dormant `test/fleetdb/ui/01–19` Playwright specs, but nothing exercises the live product
continuously). aft's role: run the **browser-observable** subset against the real e2e
stack on every PR, with agent diagnosis on failure. CLI/runtime/infra stories stay with
their existing Go/CLI coverage — aft should not duplicate them.

Partition of the 127: ~34 browser-observable (aft target), ~93 CLI/runtime/release-gate
(out of aft scope, listed at the bottom).

Convention: every aft test declares the stories it covers in a `# covers: LCLI-...`
comment at the top of its suite entry, so the TSV's "Automated coverage" column can
reference aft suites.

---

## Tier 1 — implement now (no new aft features, no new runtime deps)

Extends the 5 existing suites and adds 7 new ones. All data seeded via the workspace API
or the New Issue modal; no agents, GitHub, or LLMs involved.

| Suite (new*) | Covers | Tests |
|---|---|---|
| smoke (extend) | OPS-002 | assert `/health` + `/api/config` payloads via `run:` steps |
| views (extend) | DV-002 | apply `?search=` filter, switch Kanban↔List↔table deep link, assert filter/route state survives the switch |
| filters (extend) | DV-004 | deep-link with `?search=`+`?priority=` params applied on load; label/type params seeded via API issues |
| sse-resilience* | DV-006, IW-004, RA-007, OPS-012 | `offline: on` → StaleDataBanner (`role=alert`) appears; create issue via API **while offline**; `offline: off` → banner clears **and the missed issue appears** (cursor catch-up) |
| issue-detail* | WP-002, WP-003, WP-006 | open card → detail panel; edit description (markdown renders), change priority/labels, persist across reload; status change open→in_progress via detail triggers AssigneePrompt; close/reopen |
| create-validation* (extend issue-create-ui) | WP-001 | submit disabled with empty title; full-field create (type/epic/status/description); duplicate title behavior |
| dependencies-graph* | WP-004, DV-005 | seed A-blocks-B via API → B renders in Blocked column; open `/ws/E2E-WS/graph` → nodes+edges present (`.react-flow__node` count); remove dep → B returns to Open |
| comments* | IW-013, RA-006 | POST comment via API → detail timeline shows author/time/order; add second → ordering holds; lifecycle events visible |
| markdown-safety* | SEC-013 | create issue whose description contains `<script>`/`<img onerror>` payloads → open detail → `wait.fn` asserts no injected global fired and content is sanitized but safe formatting kept |
| monitor* | OPS-006, DV-007 (partial) | `/ws/E2E-WS/monitor` loads workspace status/queue sections without error state; empty-queue rendering |
| workspaces* | PS-002 | the stack seeds two workspaces (E2E-WS + e2e-ws-2): switch via workspace selector → route + board scope change; deep link to second workspace |

Estimated: ~20 new tests, ~40s added runtime. No aft vocabulary gaps — everything maps to
existing steps (`run:`, `offline:`, `wait.fn`, `expect.count/attr/visible/notText`).

Deliberate choice: WP-003's ready→in_progress **drag** is replaced by the detail-panel
status change (dnd-kit PointerSensor with 5px activation is a poor fit for deterministic
CLI drags). Note (verified in source + review): the AssigneePrompt is wired ONLY to the
drag path in v5, so panel-based tests cover WP-003's assignment/persistence semantics but
cannot exercise the prompt — WP-003 stays partial until a drag test lands.

Defect found while implementing Tier 1: the Create Issue modal sends create-time
`status: "deferred"` but the server drops it and the issue lands in Open. The
issue-create-ui suite deliberately does not assert status placement until fixed —
candidate "Defect status" entry for LCLI-WP-001 in the TSV.

## Tier 2 — needs seeding scaffolding (still deterministic, no LLM)

| Suite | Covers | Approach |
|---|---|---|
| files* | IW-007 | seed real files into the e2e workspace repo via `run:` git commands → Files view lists tree, opens content; traversal blocked paths return errors |
| diff* | IW-010, RA-009 | seed a branch with commits in the workspace repo → git/diff surfaces show ahead/behind, changed files, patch view |
| review-queue* | RA-001, DV-008 | PR queue page loads with review-stage issues (seed issues with status=review); GitHub enrichment absent → degrades to warning, not error |
| agent-ux* | AD-015, IW-014, DV-007 | issue-detail Start Work with no daemon running → surfaced error (not silent); task-card agent/run indicator empty states |
| onboarding | PS-003 | **blocked by logged defect** ("web-onboarding spec status endpoints are not registered") — write the test, expect failure, use it as the strict-mode diagnosis showcase; promote when fixed |

Also blocked-by-defect: RA-010 (PR detail route not implemented — TSV logs it open).

## Tier 3 — out of aft scope (keep with existing harnesses)

- **Agent runtime execution** (AD-001..014, IW-005/006/008/009/012/015/016, RA-002..005,
  RA-008/011..014, WP-005 runtime side): requires live agents/tmux/GitHub; covered by Go
  tests, `e2e/` CLI harness with LLM stubs, and `.agent-skills/loom-pr-test`.
- **Workflows engine** (AW-001..017): API/SDK-level; Go + SDK tests.
- **Release gates** (RC-001..012): `make gate`, smokes, sandbox acceptance — pipelines,
  not browser tests. Note RC-006 "FleetDB browser release smoke" overlaps aft: once
  Tier 1 is green in CI, aft arguably *is* that smoke.
- **Security runtime** (SEC-001..012/014/015), **Platform CLI setup** (PS-001/004..011),
  **Ops CLI/daemon** (OPS-001/003/004/005/007..011/013), **DV-001, IW-001/002 CLI parts**:
  CLI and backend contracts with existing Go coverage.

## Execution order

1. Tier 1 suites, P0 stories first (sse-resilience → issue-detail → dependencies-graph →
   markdown-safety → create-validation → monitor → workspaces → view/filter extensions).
2. Wire `# covers:` IDs and update the TSV's "Automated coverage" column for covered rows
   (status stays conservative until CI runs them on every PR).
3. Tier 2 scaffolding (git seeding helper in `tests/aft/scripts/`).
4. Revisit blocked stories (PS-003, RA-010) when their defects close.

---

## Next set — 2026-07-18 census, post-v5 merge

Baseline (full deterministic run, 34/34 green): routes **14/14**, components **36/50**,
endpoints **46/80**, testids **166/461**. The v5 merge also landed browser features with
zero aft coverage: workspace design-format, artifact/HTML issue designs, the v3 file
explorer's edit surface, and the PR-native review workspace (discussion panel).
Everything below is deterministic (no gh, no LLMs) — degraded/error paths are asserted
where the real integration needs GitHub.

| # | Suite (new*/extend) | Closes | Tests |
|---|---|---|---|
| 1 | workspace-mgmt* | CreateWorkspaceModal, AddRepoModal, ConfirmDialog components; `/repos`, `/repos/:repo` (DELETE — regression for FINDINGS 1.10), `/name`, `/order`, `jobs/:id` endpoints; create-workspace-\*, add-repo-\*, workspace-rename/context-menu, confirm-dialog-\* testids | create ws via modal (job progress → board), add repo via modal, remove repo through the confirm dialog, rename via context menu, reorder via API + assert sidebar order |
| 2 | table-bulk* | BulkActionToolbar component (regression for FINDINGS 1.8); bulk-action-\*, issue-table-\*, toggle-column-\* testids | multi-select rows → bulk status + bulk close → assert via API; selection-count and clear |
| 3 | settings-design-format* | `/config/design-format` endpoint (new in v5); design-format-panel/select/save, design-panel, design-html-content testids; UserMenu component | toggle markdown→html in settings, assert persisted via GET; issue with design content renders design-html-content sanitized (script payload inert); user menu opens |
| 4 | dependencies-graph (extend) | BlockedBadge, GraphControls, NodeTooltip, StatusColumn components; `issues/:id/move` endpoint; blocked-badge, zoom/fit buttons, node-tooltip, move-dialog-\* testids | blocked card shows badge; graph zoom-in/out/fit; hover node → tooltip; move issue to e2e-ws-2 via detail-panel dialog → appears on the other board |
| 5 | zz-agent-flow (extend) | `agents/:name`, `agents/:name/diff/files`, `diff/file`, `issues/:id/git/diff-stat`, `tasks/:id/sessions/:id` (+`/diff`, `/transcript`), `runs/:id` endpoints; CreateAgentModal, CodeMirrorEditor components | after the stub epic-run: open agent Diff tab (file list + file patch), session detail (diff + transcript sub-tabs), run status via API; create a second agent through the modal (no start) |
| 6 | pr-workspace-degraded* | PRDiscussionPanel component; `pull-requests/:o/:r/:n` detail/conversation endpoints (degraded 4xx path); pr-discuss-button, pr-discussion-error/retry, pr-chat-unavailable, pr-review-stale-banner testids | review issue with a github-shaped external_ref: Discuss PR opens the panel, conversation fails gracefully (error + retry, no crash); stale banner via forced refresh hook if reachable |
| 7 | smoke (extend) | `/api/client-errors`, `/api/monitor/status`, `/api/editors`, `/api/workspaces/order`, `onboarding/first-task` endpoints | pure `run:` curl assertions (POST a synthetic client error → accepted; editors list shape; order round-trip; first-task payload) |

Estimated ~28 tests / ~60s added runtime. Expected census after: components ≥ 47/50,
endpoints ≥ 65/80, testids ≥ 230/461.

**Status 2026-07-18:** items 1, 2, 3, 5, 6 implemented (plus Daytona settings folded
into item 3) — suites `workspace-mgmt`, `table-bulk`, `settings-design-format`,
`pr-workspace-degraded`, and the `zz-agent-flow` extension. 43/43 green; all nine new
tests screenshot-vetted. Census moved 36→42/50 components, 46→62/80 endpoints,
166→215/461 testids. Notes from the live runs: CreateWorkspaceModal requires an
https/git@ clone URL (file:// is covered as validation-error UX; workspace created via
API), workspace rename has no mounted UI (FINDINGS §1.12 — endpoint covered via API),
the deterministic stub session pins the no-diff contract (`has_diff:false`, session
diff 404), and the agent Diff tab is fed by a seeded `worktrees/<repo>/nova` checkout.
Items 4 and 7 remain open.

Deliberately deferred: `terminal/ws` + `terminal/setup` deep coverage (needs a ws step in
the aft runner), `git/push-all` (mutates a remote; needs a seeded bare remote first),
OpenInEditor click-through and `/api/editors/open` (launches a real editor on the host —
needs an EDITOR stub), EmbeddedTerminal (tmux tier already covers it via
test-aft-terminal), task quarantine (supervisor runtime, not browser-observable
deterministically).

---

## 2026-07-21 — two-tier restructure

The deterministic suite now separates claim strength by directory while preserving one run and
one combined census:

- `tests/aft/suites/` is product correctness. Every test-body mutation follows actor fidelity:
  agent/API-client actors use the API; human actors use a mounted UI control. Suite-level
  setup/teardown remains fixture provisioning, and API readbacks may verify a UI result.
- `tests/aft/surface-suites/` guards wiring without claiming a faithful user scenario. Workspace
  reorder moved there because it is intentionally UI-unreachable; Approve/Request changes moved
  because the gh-less review fixtures have no reviewable content; legacy inline HTML moved because
  per-issue `design_format` is blocked by FINDINGS §1.13; standalone health/config probes moved
  because they are API contracts rather than browser scenarios.
- The default harness passes both directories to one aft invocation. Coverage remains combined:
  touching a surface in either directory marks the census item covered.

Tier-1 scenarios were tightened around real actors: Lead creation now uses CreateAgentModal;
priority/labels, comments, duplicate bypass, Local/Empty workspace creation, and workspace rename
use their mounted controls; completed task sessions open by expanding the idle-lead epic rather
than injecting localStorage. The terminal page records which healthy state rendered and asserts
that branch's full contract.

The test-only seeding seam now supplies the two agent-artifact stand-ins that previously required
path fabrication:

- `loom daemon seed-log --workspace <ws> --agent <name> --content <file|->` appends through the
  product archive-log writer/resolver.
- `loom daemon seed-worktree --workspace <ws> --agent <name> [--repo <r>] [--file <rel>
  --content <file|-> --message <msg>]` uses the runtime worktree creation/registration flow and
  can commit an agent-change stand-in.

Two product fixes unlocked faithful paths: FINDINGS §1.14 is resolved by Clone, Local repos, and
Empty modes in CreateWorkspaceModal; §1.18 is resolved by keeping closed children of open epics in
the idle-lead grouping while leaving cards collapsed by default.

Items **4** (the deeper dependencies/graph move-and-controls extension) and **7** (the remaining
client-errors/monitor/editors/onboarding API set) from the 2026-07-18 set remain open. This
restructure deliberately does not count standalone contract probes as product-correct tier-1
coverage.

## 2026-07-22 — api: steps, mandatory intents, and the readable report

The aft runner (pinned in `.github/workflows/aft.yml` like flue; currently `257fb9d`) gained a
first-class `api:` step — runner-executed HTTP with JSON-path `assert:`, `save:` into per-test
`${var:name}` variables mirrored to `$AFT_WORK_DIR/<name>` for shell interop — and a loader lint
that refuses any `run:`, `api:`, or `wait.fn` step without an `intent:`. The HTML report headlines
intents (auto-labels for browser steps) over the raw mechanism, shows api request/response bodies
truncated behind a disclosure (no redaction — deliberate; the captured layer only ever carries the
deterministic stack's synthetic values), and attributes page network requests to steps by
timestamp.

All eight suite tiers were converted in one sweep: single-request curl+python steps became `api:`
steps with their assertions carried over; polls, pipelines, and git/seed-command steps stay `run:`
with actor-named intents. Validated 46/46 live. The conversion itself surfaced FINDINGS §1.20
(workspace-name resolution inconsistency, 500 instead of 404 on the reviewer route) — the
structured `status:` expectation caught what `curl -sf` used to swallow.
