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
- **Severity:** MED · **Fix:** loom / product-decision
- `PriorityDropdown` / `TypeDropdown` / `LabelEditor` exist but are never mounted; type is
  read-only (`internal/webui/frontend/src/components/IssueDetailPanel/IssueDetailPanel.tsx:1208-1243`).
  No other surface edits these post-create.

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
- **Severity:** MED (feature gap) · **Fix:** loom · **Status:** OPEN (found 2026-07-18 by the workspace-mgmt suite)
- `OtherWorkspacesSection` (sidebar workspace list with per-workspace overflow → rename/remove,
  incl. `SortableWorkspaceEntry` + `WorkspaceContextMenu`) is exported from
  `internal/webui/frontend/src/components/WorkspaceTree/index.ts:18` but nothing in the app
  mounts it — only its own unit test imports it. `PATCH /api/workspaces/{ws}/name` works;
  the UI to reach it never renders. The workspace-mgmt aft suite covers the endpoint via API
  and documents the gap in a comment; when the section is re-mounted, promote that step back
  to a UI flow.

### Suspected / low-confidence (re-validate before acting)
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

---

## 4. Suggested sequencing

1. **fleet-db `GET /artifacts/{id}/content`** (§1.2) — the linchpin; unblocks transcript, the
   diff fix, cloud-topology work, and the xfail tighten.
2. **Activity-feed enum + old/new value passthrough** (§1.1) — near-trivial, restores a core panel.
3. **Diff field mismatch + fallback** (§1.3), **token/files projection** (§1.4) — same subsystem.
4. **Contract/content tests** (§3.1) to lock 1–3 and prevent the next drift.
5. Board/reorder/table/repo UX gaps (§1.5, 1.7, 1.8, 1.10) and the double-complete cleanup (§1.11).
6. Product decisions: duplicate-title upsert (§1.6), inline-edit + filter UI, PR status semantics.
