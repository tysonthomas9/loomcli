# aft findings — product bugs & stack improvements

A living tracker for what the live aft E2E work has surfaced: **product bugs** in Loom
(to fix or triage upstream) and **stack / test-framework improvements** (to make the
harness sturdier and the coverage deeper). Companion to `README.md` (how to run) and
`COVERAGE-PLAN.md` (what to test next).

**Legend**
- Severity: `HIGH` (breaks a headline feature / data loss) · `MED` · `LOW` (cosmetic / log noise).
- Status: `OPEN` · `FIXED <sha>` · `ACCEPTED` (intended behavior) · `DISCONFIRMED`.
- Fix lands in: `loom` (this repo) · `fleet-db` (the data layer) · `product-decision`.
- ✅ = root cause personally verified against source during this pass; others are traced
  with the cited file:line evidence and should be re-confirmed at fix time.

---

## 0. The dominant theme — fleetdb-migration contract drift

The most serious bugs below are **one species**: the migration to the fleetdb backend
changed a **string or field contract**, the reader side (frontend or a serve handler)
was never updated to match, and **no test caught it because the tests assert structure
and counts, not content**. Five instances are confirmed (the already-fixed
issue-create-status bug was the sixth). The single highest-leverage improvement is
**contract/content tests** that assert the actual rendered text / fetched bytes, plus a
shared source-of-truth for the event-kind enum. See §3.1.

---

## 0a. Resolution — 2026-07-09

All eleven §1 items are now fixed (or reclassified). Implemented via codex, then put
through four adversarial Opus reviews (which caught a HIGH SSRF, a HIGH dead-feature, and
a net-negative "fix" — none visible to the green test runs), re-fixed, and validated end to
end: full aft **34/34** and the real-codex tier passing with the transcript now returning
200 and the session diff containing the created file, both against the hardened fleet-db.

| # | Item | Resolution |
|---|---|---|
| 1.1 | Activity feed all-generic + comment double-render | **FIXED** loom `c0636010d`/`668a80a8c` — kind normalization + old/new/field values + per-action text + drift-guard test |
| 1.2 | Transcript 500 | **FIXED** fleet-db `b7cb667` (GET content route) + loom `c0636010d` (405 fallback) |
| 1.3 | Diff tab permanently disabled | **FIXED** loom `c0636010d` — `patch_artifact_id` + control-plane fallback |
| 1.4 | Token/cost/files show 0 (cloud) | **FIXED** loom `c0636010d` — TaskRun→session projection, symmetric fill |
| 1.5 | Epic board hides completed children | **FIXED** loom `668a80a8c` — closed-only lanes render (default-on, consistent with flat board) |
| 1.6 | "Duplicate titles merge" | **RECLASSIFIED** — it's fleet-db's intentional soft-duplicate guard; the real bug was the UI dropping the warning. **FIXED** loom `c0636010d`/`668a80a8c` — warning surfaced + "Create anyway" (force) |
| 1.7 | Reorder silently reverts | **FIXED** loom `c0636010d`/`668a80a8c` — persisted as a local-settings pref; error toast on failure |
| 1.8 | Table multi-select dead end | **FIXED** loom `668a80a8c` — bulk close/status/priority/assign |
| 1.9 | No inline metadata editing | **FIXED** loom `668a80a8c` — priority/type/labels/owner inline (labels incremental) |
| 1.10 | Repo can't be removed | **FIXED** loom `c0636010d`/`668a80a8c` — `DELETE /repos/{repo}` + confirm dialog |
| 1.11 | Double-complete 409 | **ACCEPTED / WON'T FIX** — the unification was reverted; the benign 409 is a defensive signal guarding against a payload-losing replay |

**Found by the reviews (fixed):**
- **SSRF + object-store token exfiltration** on the new artifact-content route (HIGH) — **FIXED** fleet-db `a390f50`: URI-base allowlist, no credentials off-base, redirect refusal, create-time validation.
- **Cross-tenant local `file://` read** (MED) — **FIXED** fleet-db `a390f50`: reads scoped to `<baseDir>/<workspace>`.
- **Soft-dup warning dropped at the loom backend seam** (HIGH — the "Create anyway" feature was dead) — **FIXED** loom `c0636010d`.
- **Inline labels used full-replace** (MED data loss) — **FIXED** loom `c0636010d`/`668a80a8c`: incremental add/remove.
- **Token zero-conflation**, **404-vs-500 contract**, **unbounded TaskRun list** — **FIXED** loom `c0636010d`.

**DISCONFIRMED / ACCEPTED** (unchanged): BlockedBadge renders on cards; rename keeps id; PR queue degrades gracefully; comments XSS-safe; `/terminal` auto-spawn accepted. The swim-lane board now shows completed lanes **by default** — a deliberate consistency choice with the flat board, not a regression.

fleet-db lives on branch `fix/artifact-content-get-and-title-dupes` (`b7cb667` → `a390f50`),
left unpushed for a PR. The stack-improvement items in §3 remain open follow-ups.

---

## 1. Product bugs — OPEN (see §0a for resolution status)

### 1.1 Activity feed renders every event as "Someone performed an action" ✅
- **Severity:** HIGH (UX) · **Fix:** loom
- fleet-db emits present-tense action strings — `issue.create`, `issue.close`,
  `issue.reopen`, `issue.update`, `issue.assign`, … (`fleet-db internal/models/event.go:18-31`),
  passed through verbatim as the event kind (`internal/backend/fleet/fleet_batch_mutations.go:75`
  `Kind: e.Action`).
- The panel's `describeEvent` switch matches **past-tense / renamed** kinds — `issue.created`,
  `issue.closed`, `issue.reopened`, `issue.updated`, `issue.dependency_added`, … (`internal/webui/frontend/src/components/IssueDetailPanel/sections/ActivityLog.tsx:25-61`).
  **Zero overlap** → every event hits the `default:` branch.
- Compounding: `eventDataToTypesEvent` copies only `ID, IssueID, EventType, Actor, CreatedAt`
  and **drops `old_value`/`new_value`** (`internal/webui/service/issue_backend_helpers.go:318-327`),
  so even a matched status-change/dependency/label event would lose its detail.
- **Failure scenario:** open any issue's activity log after create/close/assign/comment — it
  reads "Someone performed an action" for every row, with no status transition, no dependency
  name, no label. Comments also double-render.
- **Fix:** normalize the kinds in `eventDataToTypesEvent` (`issue.create`→`issue.created`, etc.)
  **and** pass through `old_value`/`new_value`. Cheaper and single-point vs editing the switch.
- **Why untested:** `comments.test.yaml` counts `activity-event` nodes, never asserts their text.

### 1.2 Runs-tab transcript view 500s for every real agent run ✅
- **Severity:** HIGH · **Fix:** fleet-db (+ loom hardening)
- fleet-db registers only `PUT /api/v1/{ws}/artifacts/{id}/content` — **no GET**
  (`fleet-db internal/api/control_plane.go:60`). loom reads artifact-backed transcripts with
  `GET` on that path (`internal/infra/fleetdb/control_plane.go:405`). Go's method-aware mux
  returns **405**, which loom maps to `ErrConflict` (`internal/infra/fleetdb/client.go`), but
  `readTranscriptRef`'s `file://` fallback only fires on `ErrNotFound`
  (`internal/webui/svcimpl/session_service.go:~503`) — so it short-circuits to a 500 instead of
  recovering.
- **Failure scenario:** open the Runs tab on a task an agent worked, select the session →
  transcript pane errors ("failed to load transcript"). Reproduced live in the real-codex tier.
- **Fix:** fleet-db adds the `GET .../artifacts/{id}/content` route + a content-store read; loom
  widens the fallback to treat 405 like not-found. This is the **linchpin** — it also unblocks
  the diff work (§1.3) and lets the real-codex suite's transcript xfail tighten to a hard assert.

### 1.3 Runs-tab Diff sub-tab is permanently disabled for driver runs ✅
- **Severity:** HIGH · **Fix:** loom
- `has_diff` is computed from `metadata["diff_artifact_id"]` / `["diff_path"]`
  (`internal/webui/svcimpl/session_service.go:291`), but the driver **writes**
  `patch_artifact_id` / `patch_path` (`internal/driver/task_bridge.go:840`, `:127`). The keys
  never match → `has_diff:false` → the Diff sub-tab renders `disabled`.
- Even if force-enabled, `GetSessionDiff` has **no control-plane fallback** (only a local-store
  read) → 404 for a driver session.
- **Failure scenario:** you can never see what an agent changed from the Runs tab.
- **Fix:** read `patch_artifact_id`/`patch_path` for `has_diff`, and add a control-plane
  (artifact) fallback to `GetSessionDiff` (same GET-content route as §1.2).

### 1.4 Token/cost and files-changed show 0 on the Runs tab (cloud topology)
- **Severity:** MED (deployment-topology-dependent) · **Fix:** loom
- The control-plane `AgentSession` projection carries no token/diff-stat fields
  (`fleet-db internal/.../control_plane.go:81`); usage is captured on the TaskRun but never
  projected onto the session.
- **Masked locally:** `ListTaskSessions` backfills these from on-disk session metadata via
  `enrichSessionListItemsFromFileStores` → `enrichSessionRecordFromLocal`
  (`internal/webui/svcimpl/session_service.go:157+`) — which is why our **local** real-codex run
  saw `files_changed≥1`. In a **standalone-fleet-db / cloud** deployment (no local session files
  on the serve host) the Runs tab shows **"0 tok · $0.00 · 0 files"**.
- **Fix:** project TaskRun usage + diff-stats onto the AgentSession record. **Test gap:** our
  real tier runs local only, so it can't catch this — see §3.3 (cloud-topology tier).

### 1.5 Epic-grouped board hides completed children
- **Severity:** MED-HIGH · **Fix:** loom
- A swim-lane whose tasks are all `closed` renders **no lane**, so finished work disappears from
  the default (epic-grouped) board — a user watching a task run never sees it complete. The card
  leaves Ready at *claim* time and never reappears. Verified live. The flat (`?groupBy=none`) and
  Ungrouped views render closed cards in Done correctly.
- **Fix:** render lanes with closed-only children (or add a "show done" affordance) in
  `SwimLaneBoard`.

### 1.6 Duplicate issue titles silently merge
- **Severity:** MED (data loss) · **Fix:** product-decision (fleet-db)
- Two creates with the same title return the **same id**; the second is a silent no-op/merge
  (fleet-db origin/main upserts on title). Behavior change vs the historical WP-001.
- **Failure scenario:** a user creating two genuinely distinct issues that happen to share a
  title loses the second. Decide whether title-upsert is intended; if not, key on id.

### 1.7 Sidebar workspace reorder silently reverts
- **Severity:** MED · **Fix:** loom
- `ReorderWorkspaces` returns `ErrNotImplemented` (`internal/webui/service/workspace_impl.go:437-440`);
  the sidebar reorders optimistically then refetches the old order back with **no error toast**
  (`components/WorkspaceTree/.../OtherWorkspacesSection.tsx:81-123`).
- **Fix:** implement reorder, or disable the affordance + surface the not-implemented state.

### 1.8 Table multi-select is a dead end
- **Severity:** MED · **Fix:** loom
- `TablePage` mounts `BulkActionToolbar` with **no `actions`** (`internal/webui/frontend/src/views/TablePage.tsx:64`),
  so selecting rows yields only "Deselect all" — no bulk close/status/priority/assign/delete.
- **Fix:** wire the bulk actions, or remove the checkboxes.

### 1.9 No inline editing of priority/type/labels/owner on the issue detail panel
- **Severity:** MED · **Fix:** loom / product-decision · **Status:** RESOLVED 2026-07-21
- `PriorityDropdown`, `TypeDropdown`, `LabelEditor`, and `OwnerDropdown` are mounted in
  `IssueDetailPanel.tsx:1362-1409` and wired to the panel's PATCH-backed save handlers.
- The tier-1 `issue-detail` aft suite now drives `PriorityDropdown` and `LabelEditor` through
  their real browser controls, then uses an API readback only to verify persistence.

### 1.10 A wrongly-added repo can't be removed
- **Severity:** MED (gap) · **Fix:** loom
- `POST`/`GET /repos` exist, **no `DELETE`** (`internal/webui/handlermux/handlermux.go:86-87`);
  the UI is add-only.

### 1.11 Double-complete logs a spurious 409 every epic-runner run
- **Severity:** LOW (log noise; latent idempotency smell) · **Fix:** loom
- The loom worker completes a task-run with `CompletionID: worker-complete-<id>`
  (`internal/driver/task_request.go:914`); the epic-runner redundantly completes with
  `complete-<id>` (`internal/driver/task_mutation.go:89`, `internal/workflows/builtin/epic-runner.ts:417`).
  Mismatched keys defeat fleet-db idempotency, so the second hits the terminal-state guard and
  409s. Tolerated (task closes fine), but every run logs it.
- **Fix:** unify the CompletionID so the redundant call is an idempotent 200 replay.

### 1.12 Workspace rename UI is dead code — no browser path to rename
- **Severity:** MED (feature gap) · **Fix:** loom · **Status:** RESOLVED 2026-07-18
- `OtherWorkspacesSection` (sidebar workspace list with per-workspace overflow → rename/remove,
  incl. `SortableWorkspaceEntry` + `WorkspaceContextMenu`) was exported from
  `WorkspaceTree/index.ts` but nothing in the app mounted it — only its own unit test imported
  it. `PATCH /api/workspaces/{ws}/name` worked; the UI to reach it never rendered.
- **Fix:** rather than re-mount the orphaned sidebar section (the Aether-V3 redesign had moved
  switching into the top selector + quick-switcher overlay), workspace **rename + remove** were
  wired into the mounted `WorkspaceSwitcher` overlay itself — a per-workspace overflow (⋯) →
  context menu on every row, reusing `WorkspaceContextMenu` + `ConfirmDialog` and a new
  `useWorkspaceManagement` hook (keyed by workspace **id**, sourcing `refetch` from context so
  both the sidebar-selector and global Cmd/Ctrl+K switcher instances work). The **active**
  workspace can be renamed (routes are id-based — `/ws/:workspaceId` — so rename never breaks the
  current URL) but not removed (its `Remove` action is hidden via `showRemove`, since deleting the
  workspace you're viewing would strand the route). The dead `OtherWorkspacesSection` +
  `SortableWorkspaceEntry` (+ tests/exports) were deleted. The workspace-mgmt aft suite's rename
  step was promoted from an API PATCH to a real browser flow through the overflow menu.
- **Reorder** was left UI-unreachable **by design** — drag/positional reorder is a poor fit for a
  search-filtered quick-switcher; the endpoint stays API-covered (see §1.7, which is implemented,
  not the stale `ErrNotImplemented`).
- Its intentionally API-only aft coverage now lives in
  `tests/aft/surface-suites/workspace-order.test.yaml`.

### 1.13 Per-issue `design_format` — loom↔fleet-db contract drift + whole-PATCH amplification
- **Severity:** HIGH (no longer latent — observed destroying a real Planner's entire output) · **Fix:** fleet-db (Part B) + loom (tolerant PATCH) · **Status:** OPEN (found 2026-07-18 by the settings-design-format suite; **confirmed live 2026-08-04**)
- **2026-08-04 — the predicted sender arrived.** This entry called the failure "latent today:
  no first-party sender … but any agent/API client setting the field … hits the hard failure."
  A real codex Planner is that client. In the live worker tier (`lw-supervised-workers`, LW-2)
  the Planner ran to completion and its write-back 400'd:
  `backend error in PatchIssue issue_id=E2E-WS-2 err="backend [validation_error] Update:
  unknown field \"design_format\""`. The amplification is what makes it HIGH: the PATCH
  carried the design *and* the status transition, so **both were lost** — the issue ended
  `has_design: false`, still `in_progress`, and the Planner's whole run was discarded with no
  user-visible error. LW-1 (Task Runner, `E2E-WS-1`) hit the identical 400.
- Why no existing test caught it: this is §0's theme exactly. The deterministic stub backends
  never set `design_format`, so the 150-case corpus stays green while the real-agent path
  fails 100% of the time. Only a live backend exercises the sender.
- Sender is `internal/backend/fleet/params.go:329` (`setStrField(req, "design_format", …)`,
  added by `02da9dc4e`, PR #150); fleet-db's `UpdateIssueRequest`
  (`internal/api/request.go:48-57`) accepts only title/description/priority/status/type/
  design/notes/due_at. `owner` and `external_ref` are sent on the same PATCH and are likewise
  absent from that struct — same latent trap, not yet observed firing.
- fleet-db main has `design_format` **only on the workspace model**
  (`internal/models/workspace.go` — the #87 "Part A" change); the issue model/update path has
  no such field. loom v5 exposes per-issue `design_format` on the PATCH surface anyway
  (`internal/webui/handlers/issues/issues.go:20`) and forwards it; fleet-db strict-decodes
  update payloads, so the **entire PATCH 400s** — every other field in the same request is
  lost. Same strict-decode family as `unknown field "kind"` (roles) and
  `unknown field "external_ref"` (create — which loom already compat-handles in
  `internal/backend/fleet/create_compat.go`; no equivalent exists for update).
- Latent today: no first-party sender (frontend writes only the workspace-level toggle; the
  HTML renderer auto-detects inline HTML per issue) — but any agent/API client setting the
  field on a fleetdb-backed workspace hits the hard failure.
- **Fix:** (a) fleet-db: add issue-level `design_format` (finish Part B); (b) loom: strip or
  compat-fallback unsupported update fields against fleet-db so one drifted field cannot
  poison a whole PATCH. (b) is worth doing regardless of (a).
- **2026-08-04 — (b) landed; (a) still open.** `internal/backend/fleet/update_compat.go`
  makes the PATCH tolerant: when fleet-db rejects a named field, loom drops **only that
  field** and retries, so the rest of the update lands. Deliberately reactive — loom does
  not keep its own copy of fleet-db's schema, which would drift the other way and silently
  stop sending fields a newer fleet-db does accept. If *every* field is rejected it still
  errors, because reporting success there would be the same silent data loss in a new
  costume. Covered by four cases in `update_compat_test.go`, including the two failure
  modes worth guarding: an unrelated validation error must not be retried, and a server
  naming a field loom never sent must not spin the retry loop.
- **Still true after (b):** per-issue `design_format` does not round-trip — it is dropped,
  not stored. Anything that needs the value persisted still depends on (a).

### 1.25 No working spend ceiling for claude — the flag is accepted and ignored ✅
- **Severity:** HIGH (uncapped spend on a paid account) · **Fix:** product-decision · **Status:** OPEN (found 2026-08-04, confirmed by codex review)
- `claude --help` documents `--max-budget-usd` as **"only works with `--print`"**.
  `buildClaudeRunTurnArgs` deliberately omits `-p`
  (`internal/cli/backends/backend_claude.go`), yet the worker path still appends the
  flag — and its comment claimed the cap "carries over to the RunTurn path". It does
  not. The flag is inert on every interactive and worker invocation.
- Consequence: a claude Lead, reviewer, Task Runner or Planner can exceed the
  advertised ceiling without being stopped. `LOOM_MAX_BUDGET_USD` and the
  `DefaultMaxBudgetUSD = 50.0` constant both describe a guarantee that does not exist.
- The misleading comment and the Makefile's "$5/session ceiling" claim are corrected;
  the flag is left in place so the intent survives if Claude ever honors it
  interactively. **A real ceiling needs a product decision** — either drive claude
  through `--print` where the flag works, or enforce the budget loom-side.
- The live tiers here run codex, so no run has spent against this gap; it matters the
  moment `LIVE_BACKEND=claude` is used.

### 1.28 `delivery_state` silently regresses from `delivered` to `pending` forever ✅
- **Severity:** HIGH (a delivered assignment reports as undelivered; makes the state unusable for orchestration decisions) · **Fix:** loom · **Status:** OPEN (found 2026-08-04 by the live lead tier)
- The version compared is a **mutable timestamp**. `monitorLeadAssignmentVersion` returns
  `agent.UpdatedAt.UTC().Format(time.RFC3339Nano)`
  (`internal/cli/serve/metricscmd/monitor_store_data_source.go:271-279`), while the lead
  process stores whatever `assignment.AssignmentVersion` it read when it built its prompt
  (`internal/cli/agent/lead/lead.go:232-257`). The monitor reports `delivered` only when
  the two are equal (`:281-286`).
- Therefore **any** later write to the agent row — a status change, a heartbeat, a
  `desired_state` update, an unrelated PATCH — bumps `UpdatedAt`, the stored marker
  becomes permanently stale, and `delivery_state` reverts to `pending`. The lead never
  re-reads, so it never re-converges.
- **Observed:** LL-1 passed twice, then failed with the identical suite logic and only
  comment changes — `parent` set, `orchestrator_session_id` present, `delivery_state:
  pending`. The two green runs were timing luck, not correctness.
- **Indistinguishable from a real failure over the API:** `pending` from this race and
  `pending` from "the lead never read the assignment" look identical to every client;
  the durable marker lives in orchestration-session metadata, which no HTTP route exposes.
- **Fix:** version the assignment by something immutable — the epic id plus an explicit
  assignment counter/uuid written at assign time — rather than the row's `UpdatedAt`.
- **Consequence for this tier:** LL-1 is inherently flaky until this is fixed. Its
  failure message now names this finding so a real regression is distinguishable from the
  known race, but the case cannot be made reliably green from the test side.

### 1.27 Real codex worker/planner sessions capture no transcript ✅
- **Severity:** MED (Runs-tab transcript is empty for every codex agent run) · **Fix:** loom · **Status:** OPEN (found 2026-08-04 by the live worker tier)
- Observed: a completed codex Task Runner session reports `status: completed`,
  `exit_code: 0`, `files_changed: 1`, `files_touched: ["LW1-<run>.txt"]`, `has_diff: true`
  — and **`has_transcript: false`**. Same for the Planner.
- **MECHANISM CORRECTED 2026-08-05 — my first diagnosis was wrong.** I wrote that "for a
  codex run nothing materializes it". False: a codex capture path exists and runs on every
  daemon finalize — `sessionfinalize.WithWorktree` calls `SyncLatestCodexRollout` when the
  backend is codex (`internal/cli/sessionfinalize/finalize_worktree.go:51-60`), mirroring the
  matching rollout to `agent_transcript.jsonl`. Verified on this machine: **160 non-empty**
  `agent_transcript.jsonl` files across 686 session dirs. Capture works.
- **The real cause is still unproven, and two candidate explanations were BOTH rejected on
  evidence.** (a) "Writer and reader resolve different session-store roots" does not by
  itself explain it: the reader also searches the fleet-db workspace root and every repo
  path (`internal/webui/svcimpl/session_service.go:70-84`). (b) `sameCleanPath` lacking
  symlink resolution (`internal/sessions/codex_rollout.go:151-189`) is plausible hardening
  but nothing retained demonstrates the mismatch for these runs.
- **Confirmed adjacent defect, found while vetting the above:** the supervisor reads the
  transcript bytes at `session_finalize.go:126` (`readLeafTranscript`) **before** the codex
  sync runs inside `WithWorktree`, then uploads those bytes as the control-plane
  `transcript_ref` artifact. So control-plane transcript materialization can be empty even
  when the later local sync succeeds — an ordering bug independent of whatever causes the
  local `has_transcript:false`.
- **Next step is instrumentation, not a fix.** Every error on this path is discarded
  (`_, _ =` at finalize_worktree.go:54-59), which is exactly why the cause cannot be
  established from the artifacts. Log the `(path, err)` of the sync and the resolved store
  root, run the live worker tier once, and let that decide the fix.
- **Not §1.2.** That finding is fleet-db's missing `GET .../artifacts/{id}/content` —
  a serving bug one layer up, listed as fixed in §0a. This is a *capture* gap and
  survives regardless. An earlier revision of the live suites attributed the false
  `has_transcript` to §1.2; that attribution was wrong and is corrected in place.
- The evidence is not lost — the full codex event stream is written to
  `~/.loom/logs/<ws>/agents/<agent>.log` (`internal/cli/agent/archive_log.go:24-31`),
  which is what the live tier reads for clause 4. It simply never reaches the session
  record, so the product's own Runs tab shows no transcript for any codex agent run.
- Pinned as a tripwire in `lw-supervised-workers.test.yaml` and
  `tests/aft/scripts/live-session-evidence.py`: both assert `has_transcript is False`
  and fail loudly if it ever becomes true, forcing the stronger assertion back.

### 1.26 Known limits of the live tier's read-only and kill proofs (not defects — scope)
- **Severity:** LOW/MED · **Status:** DOCUMENTED 2026-08-04 (codex review)
- Recorded so nobody reads these assertions as stronger than they are:
  - **LP-1 read-only** does not inspect `.git` internals (config, hooks, index flags,
    reflogs), does not see **ignored** files (`git status --porcelain` omits them), and
    `for-each-ref` on the bare upstream cannot detect unreachable objects written by
    `git hash-object -w`. A reviewer doing any of those stays green. The refs delta is
    now pinned exactly; the rest is stated in the suite's own intent text.
  - **Clause 4 native evidence** is per-AGENT, not per-session: the event log is
    append-only (`cli/agent/archive_log.go:24-31`) and agent names embed the run id, so
    it cannot leak across runs, but a restarted sibling session of the same agent could
    supply the events. The session-scoped half is `files_touched` plus the run token in
    that session's diff.
  - **`killProcessTree`** falls back to `proc.Kill()` when the group kill fails and
    returns that result, so a caller can receive `nil` while descendants survive; a
    descendant that calls `setsid` escapes the group entirely
    (`internal/webui/terminal/pty_session.go`). `live-sweep.sh`'s process-table diff is
    the backstop that catches this in the live tiers.
  - **`DeleteTab`** deletes metadata and `killSession` drops the session from the
    manager map before `close`, so after a kill failure the error is surfaced but the
    orphan is no longer addressable — recovery is manual (the sweep reports the pid).
  - **§1.24's SIGTERM diagnosis** is a race, not a deterministic signal difference: root
    subscribes to INT and TERM identically, so which handler wins is timing. SIGHUP is
    deterministically safe only because root does not subscribe to it — which means the
    harness's SIGINT workaround is itself racy, and observed-graceful 3/3 is evidence,
    not a guarantee.

### 1.24 `loom daemon` dies on SIGTERM without draining — the root CLI handler defeats it ✅
- **Severity:** HIGH (no graceful stop under any process manager) · **Fix:** loom · **Status:** OPEN (found 2026-08-04 by the live worker tier)
- **Root cause, verified against source.** `internal/cli/root.go:198-211` installs a
  trace-flush handler for SIGINT **and SIGTERM**. On receipt it ends the root span, then:
  `signal.Reset(s)` — restoring the **default** disposition process-wide — followed by
  `syscall.Kill(getpid(), s)` to re-raise. The re-raise now terminates the process
  immediately, so any command-level handler registered for the same signal never gets to
  finish. `setupSignalHandler` (`daemon_cmd.go:167-180`) registers INT/TERM/HUP and is
  installed correctly; it simply loses the race to a process that has already been killed.
- **Reproduced directly**, fully-started daemon (supervisor banner + 15s settle), one
  signal each, `exec`'d so the pid under test is loom itself:

  | signal | exit | shutdown markers |
  |---|---|---|
  | SIGINT  | 0   | 3 (graceful) |
  | SIGHUP  | 0   | 3 (graceful) |
  | SIGTERM | **143** | **0** |

  SIGHUP survives *because root.go does not register it* — only INT and TERM. That
  asymmetry is the tell, and it rules out "the daemon's handler is misregistered".
- **Why it matters:** systemd, launchd, Docker, Kubernetes and every process supervisor
  stop services with SIGTERM. In all of them the daemon is killed outright: agents are
  never drained, and the deferred PID-file / lock-file / state-file cleanup in
  `runDaemonBody` does not run, so it exits leaving a stale lock that the next start must
  break. Ctrl-C (SIGINT) is the only path that shuts down cleanly, and only by luck of the
  race.
- **Fix:** stop the root handler from defeating command-level handlers — gate it on
  tracing actually being enabled (`LOOM_TRACE=1` / `OTEL_EXPORTER_OTLP_ENDPOINT`, the same
  condition `initCLITracing` already uses), so it is not installed at all in the
  overwhelmingly common untraced case. Re-raising after `signal.Reset` is only safe for a
  command with no shutdown logic of its own.
- Applies to every long-running loom command sharing this root, not just `daemon`:
  `serve` and `worker` install their own SIGTERM handlers too.
- The live worker tier works around it by sending **SIGINT** (verified graceful 3/3) and
  will keep doing so until this is fixed; that workaround is why `daemon=clean` is
  achievable at all today.

### 1.23 A Task Runner cannot reach `closed` in a workspace whose only remote is local
- **Severity:** MED · **Fix:** product-decision · **Status:** OPEN (found 2026-08-04 by the live worker tier)
- Observed, not inferred. In LW-1 the real Task Runner did everything right: wrote the
  byte-exact marker, passed the acceptance assertion and the repo gate, committed
  `bb510ac`. It then reached its stacked-publish step, and loom rejected the origin
  because a local bare repository is not a hosted remote. The agent correctly refused to
  claim success and set the task to **`blocked`** — so the task never closes, and the
  daemon's `[recover] … exited cleanly (code 0), trusting agent's task status` path
  leaves it parked there.
- The agent's own words from `.loom/logs/E2E-WS/agents/<agent>.log`: *"This is an external
  tooling blocker under Step 8a, not an implementation failure: the repository's only
  origin is a local bare repo."* That is the right call by the model; the gap is that the
  Task Runner workflow treats publishing as mandatory.
- Consequence: any fully-local/offline workspace (no hosted remote) cannot complete a Task
  Runner run end to end, regardless of the model used.
- Worked around in the live suite by stating in the task design that publishing is out of
  scope for a local-only origin. That unblocks the test but not the product: a user with a
  local-only workspace still hits this with the default template prompt.

### 1.14 CreateWorkspaceModal cannot create local or empty workspaces
- **Severity:** MED (feature gap, inconsistent) · **Fix:** loom · **Status:** RESOLVED 2026-07-21
- The original modal was structurally clone-only: it hardcoded `type:"clone"`, required a clone
  URL, and exposed no local-repo or empty branch. `CLONE_URL_RE` belongs to `AddRepoModal`, not
  `CreateWorkspaceModal`; the file-URL rejection pinned by aft is server-side validation in
  `internal/webui/service/workspace_validate.go:28`. The backend already accepted `repos` with
  `type:"empty"`.
- **Fix:** `CreateWorkspaceModal` now exposes Clone, Local repos, and Empty modes. Clone still
  surfaces the server's validation error, Local repos submits paths as `repos` on an empty-type
  workspace, and Empty submits without repositories. Tier-1 `workspace-mgmt` and `workspaces`
  drive the Local and Empty modes respectively.

### 1.15 Create-time `status` on issue POST fails outright (second failure mode)
- **Severity:** MED (API contract) · **Fix:** loom/fleet-db (decide the contract) · **Status:** OPEN (found 2026-07-18 by the pr-workspace-degraded suite)
- `POST /issues` with `status:"review"` in the body returns non-2xx (observed as a silent
  empty response under `curl -sf`; exact code not captured before switching patterns).
  Companion to the already-logged defect where the Create modal's `status:"deferred"` is
  silently **dropped** — so create-time status is broken two different ways depending on the
  value. Create-then-PATCH works and is what suites use. Decide one contract: honor, reject
  loudly, or document-and-drop — not a mix.

### 1.16 Bulk status change: no optimistic update or post-apply refetch
- **Severity:** LOW (UX) · **Fix:** loom frontend · **Status:** OPEN (found 2026-07-18, screenshot-vetted)
- After the bulk Status apply, the success toast fires while the table's Status column still
  shows the old value for the affected rows; a fresh load shows the new status. The fan-out
  PATCHes land — the table just never updates in place.

### 1.17 Issue/session detail panel clips past the viewport edge
- **Severity:** LOW/MED (responsive layout) · **Fix:** loom frontend · **Status:** OPEN (found 2026-07-18, screenshot-vetted)
- At the aft harness window size, the right-hand issue/session detail panel extends beyond the
  right viewport edge: the DESIGN heading and the Runs tab's transcript/cost/token content
  render partially off-screen (seen independently in the settings-design-format and
  zz-agent-flow session captures). Content exists and asserts pass; a human at this width
  cannot read it.

### 1.18 Idle-lead work panel hid completed history
- **Severity:** MED (reachability) · **Fix:** loom frontend · **Status:** RESOLVED 2026-07-21
- `groupOpenByEpic` omitted closed child tasks, so the idle Lead work panel offered no user path
  from an open epic to a completed task's session detail. The old aft test had to inject
  `selectedTaskId` into localStorage to reach the Runs tab.
- **Fix:** closed children of open epics are now included in `groupOpenByEpic`; epic cards remain
  collapsed by default so pickup work stays visually primary. The tier-1 `zz-agent-flow` suite
  now expands the epic and clicks the closed task's real `role=button` path.

### 1.19 Review queue actions lack reviewable content on a gh-less stack
- **Severity:** TEST-GAP · **Fix:** seeding seam / harness · **Status:** OPEN (found 2026-07-21)
- A review-status issue can render Approve and Request changes, but without a PR, branch, commit,
  or diff the reviewer has nothing to inspect. Treating those clicks as product-correct review
  scenarios overclaims a hollow fixture.
- The two action tests moved to the `tests/aft/surface-suites/review-actions.graph/`
  package. Promote them
  when branch/commit review-content seeding exists; see §3.9.

### 1.20 Workspace resolution by name is inconsistent across routes — reviewer route 500s
- **Severity:** LOW (contract inconsistency) · **Fix:** loom · **Status:** OPEN (found 2026-07-22 by the pr-contracts surface suite)
- `POST /api/workspaces/{ws}/repos` and `POST .../issues` accept the workspace **name**
  (`aft-prc-<run>`) and return 201, but `POST .../pull-requests/{o}/{r}/{n}/reviewer` passes the
  same value into a fleet-db admin lookup (`GET /api/v1/admin/workspaces/<name>`) and surfaces a
  raw **500** instead of a clean 404. Either every workspace-scoped route should resolve names
  like repos/issues do, or none should — and an unknown workspace should be a 404, never a 500.
- The pr-contracts suite now creates its workspace via an `api:` step and uses the
  server-assigned id (`${var:prcWs}`) everywhere, which is the correct client behavior regardless.

### Suspected / low-confidence (re-validate before acting)
- **Local-task-runner stub sessions record zero diff evidence** — `has_diff:false`,
  `files_changed:0` on completed stub runs, while all four real-CLI tiers project
  `files_changed>=1`. Either the stub genuinely commits nothing through the runner path
  (likely) or local-runner diff-evidence projection has a gap. The zz-agent-flow suite pins
  the observed contract (`404 diff not found`), so a change here will surface. MED/LOW.
- **`/ws/:id/workspace` cold-load redirect race** — the single-repo→kanban redirect keys off
  `isMultiRepo`, which is false while repos are still loading, so a deep link to `/workspace`
  on a **multi-repo** workspace can bounce to kanban on cold load. Observed only indirectly
  (single-repo stack); multi-repo case inferred from `App.tsx`. LOW.
- **PR approve/reject are loom-status-only** — Approve can push a merged GitHub PR back to
  `open`; Request-changes hard-demotes `in_progress`→`open`
  (`components/.../PRReviewWorkspace.tsx:182-201`). MED/LOW, needs `gh`.
- **Monitor "0% · 0 total" with no stale banner** when disconnected and the workspace has no
  agents (`ProjectHealthPanel.tsx:117`, `MonitorDashboard.tsx:68`). LOW.
- **Retry loses attempt-1 evidence** — deterministic `flue-<taskRunId>` session ids, `attempt_num`
  never bumped; attempt 2 overwrites attempt 1's failed session/transcript. MED/LOW.
- **Running session error-storms** a 500 transcript fetch every ~3s (falls out of §1.2). LOW.
- **Suspected orphaned per-task-run worktree** on terminal failure (masked in aft by the
  workspace wipe on next boot). LOW.

### 1.21 Posted comment vanishes from the panel: issue-prop sync clobbers the optimistic append
- **Severity:** MED (data appears lost) · **Fix:** loom frontend · **Status:** FIXED (found 2026-07-29 by the issue-detail graph's comment branch, fixed same day)
- `CommentForm` POSTs the comment (201) and appends it to `localComments` optimistically —
  nothing ever refetches comments afterwards. The sync effect in `IssueDetailPanel`
  replaced `localComments` wholesale with `issue.comments` on **every issue-prop identity
  change**, and SSE/store refreshes deliver stale pre-POST snapshots constantly, so the
  just-posted comment disappeared from Activity until a full reload. Evidence: `POST
  /comments` 201 with zero subsequent GETs, Activity stuck at (1) for 15s+. Intermittent
  under light SSE traffic; near-deterministic once the graph suite ran 12+ concurrent cases.
- Fix: the sync effect now merges — snapshot first, then optimistic comments (matched by
  `issue_id`, deduped by `id`) the snapshot doesn't know about yet.

### 1.22 Saved metadata visually reverts in the panel under SSE churn (owner observed)
- **Severity:** MED (server saved, UI shows old value) · **Fix:** loom frontend · **Status:** OPEN (found 2026-07-29 by the issue-detail graph's owner branch)
- `set-owner` PATCH returned 200 (owner persisted; the fresh-reload card badge proves it),
  the panel rendered the new owner — then reverted to "Unassigned" within the same second.
  The board→details sync effect (`App.tsx:575-604`) has an `updated_at` staleness guard,
  but it is bypassed when either timestamp fails to parse, and the field-diff then
  overwrites `issueDetails` with the board snapshot wholesale. Reproduces with cases
  running strictly sequentially — the 2026-07-29 green run's own screenshots catch the
  owner rendering and reverting ~50ms later (c02 s37→s38) — so a single board's SSE/store
  refresh suffices; the instant expect lost the race in ~1 of 4 runs. Same family as
  §1.21; sibling exposure: every panel field with an instant post-save display (type,
  priority, title, status, labels).
- Until fixed, `owner-saved` in the issue-detail graph pins the live render in its `wait`
  (proves the new owner rendered at least once) and its `expect`s assert the stable
  no-error contract; tighten back to an instant display assertion once the overwrite is
  ordering-safe.

### 1.23 Dependency section never shows the dependency it just created
- **Severity:** LOW (UX; state is correct after reload) · **Fix:** loom frontend · **Status:** OPEN (found 2026-07-29 verifying the issue-detail graph's dependency branch)
- `POST /issues/{id}/dependencies` returns 200 and the picker form closes, but the
  "Blocked By" section still reads "No blocking dependencies" — `handleAddDependency`
  (`IssueDetailPanel.tsx`) neither refetches nor optimistically appends, and screenshots
  125ms after the 200 confirm it. The board reflects the blocked state after reload, so
  the graph asserts there; add a panel-side assertion to `dependency-recorded` once the
  section refreshes in-session.

### 1.24 Activity log misses removal/defer entries
- **Severity:** LOW (audit-trail gap) · **Fix:** loom · **Status:** OPEN (found 2026-07-29, screenshot-vetted across graph cases)
- "added label" and "created this issue" get timeline entries, but removing a label
  (PATCH `remove_labels` 200) and deferring an issue (defer 200) leave the activity log
  untouched. If mutations are supposed to be audited in the timeline, these two paths
  are silent.

---

## 2. Product bugs — resolved this session (traceability)

| Bug | Fix |
|---|---|
| Issue create dropped `status` (client omitted the field) | `cc6211dc6` |
| Close reasons captured but never rendered | `db0f866a4` |
| SSE close evicted the card instead of moving it to Done | `db0f866a4` |
| Agent detail features (Logs / Open-in-editor / diff-stat) unreachable | `e96dddc13` |
| Logs tab loaded scrolled to the top, hiding newest lines | `29bd3f7fe` |
| 7 dead components + callerless API fns + default-workspace chain shipped | `c30b9d989` |
| Terminal session-history feature dead (writer gone, linkage extinct) | `abab1359b` |

## 2a. Disconfirmed & accepted (retire these)
- **DISCONFIRMED** — BlockedBadge **does** render on board cards (not only the graph);
  workspace rename keeps the id immutable; the PR queue degrades to a warning banner when GitHub
  is absent; comments use the same sanitized renderer as descriptions (XSS-safe).
- **ACCEPTED** — visiting `/ws/:id/terminal` auto-spawns a real interactive lead session; a
  cost/safety property to keep in mind for production, not a bug to fix.

---

## 3. Stack & test-framework improvements

### 3.1 Contract / content tests (highest leverage)
Every HIGH bug in §1 is a silent contract mismatch that structural assertions missed. Add tests
that assert **content, not shape**: the rendered activity text, the fetched transcript bytes, the
diff actually rendering. Back the event-kind mapping with a **shared source of truth** (generate
the frontend's expected kinds from the fleet-db `Action` enum, or a normalization table with a
round-trip test) so tense/rename drift can't recur.

### 3.2 aft should shell-lint `run:` steps at load
Three separate YAML-to-shell folding failures this session (`>-` block scalars producing embedded
newlines; a `|` pipe on a continuation line) — each wasted a real-codex run before codex was even
invoked. Run `bash -n` over each `run:` step when a suite loads and fail fast with the offending
step. Cheap, high-value.

### 3.3 Cloud-topology real-codex tier (phase 3)
The real-codex tier runs **local/embedded** topology, which masks §1.4 (and the local-store
enrichment hides other forensics gaps). A **standalone-fleet-db** topology (the orthogonal axis
the aether-test-framework already models) would catch the cloud-only projection bugs.

### 3.4 Auto-tighten the transcript xfail
`real-suites/zz-real-codex-epic.test.yaml` tolerates the §1.2 500 as an xfail. Once the fleet-db
GET-content route lands, flip it to a hard assert (marker already in the suite comment).

### 3.5 Port isolation
Demo stacks and test runs share frontend port 3100; a leftover agent from a prior stack
contaminated two runs this session (pages-suite false failures, the zz monitor-reload transient).
Parameterize demo stacks onto a different port, or have `run-aft.sh` refuse to start when 3100 is
already owned.

### 3.6 Census hygiene + the coverage campaign
Teach `scripts/gen-census.py` to detect unrendered/dead components (now that 7 were removed) so
dead surface leaves the denominator instead of reading as permanently-uncovered. Then execute the
queued coverage tranches (modals/graph/workspace-mgmt; agent-detail; board-chrome; the
IssueDetailPanel deep cluster) — see `COVERAGE-PLAN.md`.

### 3.7 Sturdier suite isolation
The "alphabetical + `zz-` runs last" ordering invariant is implicit and is exactly what let a
prior suite's agent artifacts leak into a later suite. Per-suite workspace isolation (or an
explicit ordering/isolation config) removes a whole class of flakiness.

### 3.8 Real-codex phase 2 — live terminal
The Logs tab's live-tmux / embedded-terminal mode is the one path still only unit-tested; a
scenario behind `AFT_REAL_TERMINAL=1` (spawns a real lead) would close it. Riskier (real
auto-spawn); ship after phase 3.

### 3.9 Testability gaps found while landing the 2026-07-18 coverage set
- **No deterministic staleness seam for PR review** — `pr-review-stale-banner` needs a real
  PR head to move (stale-subject 409) and `pr-chat-unavailable` needs a reviewer in a
  failed/unsupported state; neither is reachable on a gh-less stack. A fake-connector hook
  (or an injectable stale-subject response) would let aft cover both.
- **Missing stable testids** — AddRepoModal inputs (suites match by label), and
  DiffTab/DiffFileRow/DiffFileViewer (suites match by role/text + API proof). Cheap adds.
- **`/config/design-format` is PATCH-only** — no GET; suites assert persistence via the
  workspace GET payload. Minor API asymmetry.
- **No gh-less review-content seed** — status-only review fixtures have no branch, commit, PR, or
  diff for a reviewer to inspect. Extend the artifact seam so review-action scenarios can seed
  reviewable branch/commit content before returning them to tier 1 (§1.19).

### 3.10 Seeding seam extended for agent artifacts (2026-07-21)
ADR-0001's hidden, `LOOM_TESTSUPPORT=1`-gated command family grew beyond
`seed-transcript`: `seed-log` appends through the product archive-log writer/resolver, and
`seed-worktree` creates/registers worktrees through the runtime's own flow and can commit a file
as an agent-change stand-in. The remaining high-value candidate is `seed-session`, which would
create a full session record rather than only transcript content.

---

## 4. Suggested sequencing

1. **fleet-db `GET /artifacts/{id}/content`** (§1.2) — the linchpin; unblocks transcript, the
   diff fix, cloud-topology work, and the xfail tighten.
2. **Activity-feed enum + old/new value passthrough** (§1.1) — near-trivial, restores a core panel.
3. **Diff field mismatch + fallback** (§1.3), **token/files projection** (§1.4) — same subsystem.
4. **Contract/content tests** (§3.1) to lock 1–3 and prevent the next drift.
5. Board/reorder/table/repo UX gaps (§1.5, 1.7, 1.8, 1.10) and the double-complete cleanup (§1.11).
6. Product decisions: duplicate-title upsert (§1.6), inline-edit + filter UI, PR status semantics.
