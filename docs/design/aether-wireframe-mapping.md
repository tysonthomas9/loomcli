# Aether Wireframe → loomcli Component Mapping

> **Status:** Current · delta snapshot re-verified 2026-08-03 · originally
> written 2026-06-10.
>
> The ✅ / 🔧 / 🆕 column is **time-sensitive** — it records the gap between the
> wireframe and the app on a given date, not a permanent property. Items that
> were 🆕 or 🔧-large in the original snapshot and have since shipped are
> marked ✅ **shipped** with a `path:line` below. Re-verify before trusting any
> row.

Source: Claude Design handoff bundle `aether-wireframe-2-v3` (`Aether Wireframe.html`
plus two chat transcripts). The wireframe was reverse-engineered *from* this app
("Aether"/K3), then iterated ~60 times. Most chrome therefore already exists 1:1;
this doc maps every wireframe region to our component and classifies the delta.

Legend: ✅ exists (no work) · 🔧 exists, needs changes · 🆕 missing.

## Chrome

| Wireframe region | Spec | Ours | Status |
|---|---|---|---|
| Top header (46px): logo, theme toggle, profile menu | pins 1–3 | `AppLayout` header — 44px (`styles/variables.css:23` `--unified-header-height`) + `ThemeToggle` + `UserMenu` | ✅ |
| Icon rail (54px): Kanban · Agents · Terminal · PRs, workspace switcher dots, settings | pins 4–5 | `NavRail` — 52px (`styles/variables.css:24` `--nav-rail-width`). Entries are Workspaces, Pull Requests, Terminal, Files, Settings (`components/NavRail/NavRail.tsx:49,98,137,173,198`). No workspace dots (⌘K `WorkspaceSwitcher` + `WorkspaceSelectorBar` covers it) | 🔧 **mostly shipped** — the PRs entry exists (`NavRail.tsx:98`) and workspace dots were deliberately not adopted, but there is **no Agents entry**: the shipped ids are `kanban`/`prs`/`terminal`/`files`/`settings` and the Agents view is reachable only by route, not from the rail. UNVERIFIED whether that omission is a decision or a gap |
| Workspace sidebar (214px): AGENTS roster, REPOS list, add buttons; collapsible to 56px agent rail | pins 6–7, 24 | `WorkspaceTree` — 210px default, user-resizable 160–420px (`hooks/ui/useWorkspaceTreeWidth.ts:10-12`; applied as the inline `--workspace-tree-sidebar-width` at `components/WorkspaceTree/WorkspaceTree.tsx:234`, consumed by `WorkspaceTree.module.css:9-10`. The `--workspace-tree-width` token at `styles/variables.css:26` is dead — nothing reads it): renders WorkspaceSelectorBar, AgentSection, RunningSection, ReposSection (`WorkspaceTree.tsx:372,381,384`). `QueueStatsBar` is exported but never rendered. `AgentIconRail` (60px) already plays the "collapsed agent switcher" role on /agents | ✅ functionally; manual collapse toggle on other views is 🆕 (low value) |

## Board

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Kanban columns Backlog→Done | 6 columns | `KanbanBoard` + `StatusColumn` + `columnConfigs` — same 6 | ✅ |
| Kanban ↔ List toggle | pin 8 | `ViewSubSwitcher` (Kanban ↔ Table routes) + `IssueTable` grouped by epic | ✅ |
| Epic swimlane headers showing running agent / "Unclaimed" | pin 10 | `SwimLaneBoard`/`SwimLane` (default group-by is epic) — runner badge added: lead claims from `agentStore` → green-dot badge or italic "Unclaimed" pill on epic lanes | 🔧 **implemented** |
| Ticket card: id, title, repo chip, PR pill, assignee | pin 11 | `IssueCard` — id, title, priority, type, assignees, blocked badge, plus a repo badge gated on multi-repo (`components/IssueCard/IssueCard.tsx:191,328`) and a PR affordance via `isPRUrl` on `external_ref` (`IssueCard.tsx:23,284`) | ✅ **shipped**, multi-repo-gated exactly as predicted |
| Priority removed, repo shown instead | chat final state | We keep P0–P4 across backend + UI | ⚠️ product decision — NOT adopted; priority is load-bearing in fleet-db |
| New Issue dialog | pin 23 | `CreateIssueModal` | ✅ |
| Repo filter pills in board header | chat phase 7 | `FilterBar` repo filter dropdown | ✅ (dropdown instead of pills) |
| Global search | pin 9 | `SearchInput` | ✅ |

## Card detail

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Slide-over with Details/Runs tabs, Approve/Reject | pins 16–17 | `IssueDetailPanel` (Details, Sessions, Comments, Activity; Approve/Reject actions) | ✅ |
| Maximize to full page (50/50 split) | pin 16 | `IssueDetailPanel` — base width `min(100%, var(--panel-width-max))` with `--panel-width-max: 840px` (`components/IssueDetailPanel/IssueDetailPanel.module.css:33-34`, `styles/variables.css:218`), 100% below 520px (`:43-51`); maximized it fills the content area right of the workspace tree (`.panelMaximized`, `:60-64`) | ✅ **shipped** — header button (`components/IssueDetailPanel/header/IssueHeader.tsx:215-226`, "Expand to full screen"/"Exit full screen") toggling `isMaximized` (`IssueDetailPanel.tsx:1619-1620,1712,1716`) |
| Design-brief column + copy buttons | pin 18 | description copy exists; no design column (no design field in Issue model) | 🆕 needs data model first |
| PR card in detail (state, branch, diff, checks) | pin 26 | Sessions tab shows task-run diffs; no PR object | 🆕 needs PR integration |

## Agents view (the core of this design)

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Agent switcher rail | — | `AgentIconRail` | ✅ |
| Terminal pane w/ agent header (status · role · epic line), inline CLI | pin 12 | `AgentDetailMain` + embedded `TerminalView` (real PTY, not mock) | ✅ |
| Info / Git / Diff / Files tabs | pin 12 | `views/AgentEditorGroups.tsx` — `AgentEditorTab = "terminal" \| "info" \| "git" \| "diff" \| "files"` (`:16`) with split-to-new-column and drag-between-columns (`:1-3`) | ✅ **shipped** (was follow-up item 4) |
| **Open Queue rail, epic mode**: "Open Queue" eyebrow, epic title, claim + done line, **status distribution bar**, **tappable status filter pills**, scrollable task list, **Worker History pinned to bottom third** | pin 20 | `AgentWorkPanel` focused mode: epic tag + title, static count chips, single-fill progress bar, task list, worker history inline | 🔧 **implemented this pass** |
| **Open Queue rail, lead mode**: aggregate distribution bar, summary line, **All/Running/Not running pills**, **collapsed epic cards** ("› N" expand pill, "claimed by X · N open" / "Unclaimed · N open"), unassigned below | pin 20 | `AgentWorkPanel` lead-open mode: always-expanded epic groups, Run button, claim badge, runner strip | 🔧 **implemented this pass** |
| One-lead-per-epic guard ("on epic", "All leads are busy") | pin 31 | `epicClaims` already disables Run + shows "claimed by" | ✅ |

## Terminal / PRs / Settings / Dialogs

| Wireframe | Ours | Status |
|---|---|---|
| Terminal multi-tab + VS-Code split groups | `TerminalView` tabs/PTY plus `components/TerminalView/layout/{SplitDivider,SplitPaneSelector,useSplitView,useTabEditorGroups}` | ✅ **shipped** |
| Pull Requests view + PR detail + PR review agent + dev-server preview | `views/PRsPage.tsx`, `views/PRReviewWorkspace.tsx`, `components/PRDiscussionPanel/`, `components/IssueDetailPanel/{PRFilesTab,PRCompareDiffPane}.tsx`, `sections/PRSection.tsx`; route at `router.tsx:131` | ✅ **shipped** — spec is `docs/product/pr-review-spec.md` (was follow-up item 5) |
| Settings cards (Onboarding, AI CLIs, backends, FleetDB, font, observability) | `SettingsView` covers equivalents | ✅ |
| Add Workspace / Add Agent / Add Repo dialogs | `CreateWorkspaceModal` / `CreateAgentModal` / workspace repo attach | ✅ (Add-Agent epic picker for leads is 🆕 nice-to-have) |
| Dark mode toggle | `ThemeToggle` (we're dark-first) | ✅ |

## Implemented in this pass

`AgentWorkPanel` ("Open Queue" rail) redesigned to the wireframe's pin-20 spec:

1. **Status distribution bar** — proportional segments (in-progress / open / review /
   blocked / done) using existing `--color-status-*` tokens; replaces the
   single-fill progress bar.
2. **Tappable status filter pills** — All · In Progress · Open · Review · Blocked ·
   Done with live counts; filters the task list (tap again or All to reset).
   `review` is now counted separately from `open`.
3. **Lead mode** — All / Running / Not running pills (an epic is *running* when a
   lead claims it or an epic-runner workflow run is active); epic cards collapse
   by default with a "› N" expand pill and a claim line ("claimed by X · N open" /
   "runner active" / "Unclaimed · N open"); Unassigned group stays expanded below.
4. **Worker History pinned** — in focused (assigned/active epic) mode the history
   moves out of the scrolling task list into a footer capped at a third of the
   rail, matching the wireframe.

Second pass (board + epic detail, pins 10 and 25):

5. **Epic lane runner badge** — `SwimLane` headers (kanban, default epic
   grouping) show a green-dot badge with the claiming lead's name, or an
   italic "Unclaimed" pill; claims come from `buildEpicLeadClaims`
   (`src/utils/agentRole.ts`) over the shared agent store.
6. **Epic detail tickets** — `components/IssueDetailPanel/sections/EpicRollup.tsx`
   (Details tab, epics only; the component was named `EpicTicketsSection` in an
   earlier revision of this doc): claim badge, "M of N done" progress, status
   distribution bar, and the epic's child tickets sorted by status; rows
   navigate to the child via `onNavigateToIssue`. Lane-title click on the
   board opens this panel, covering the wireframe's epic-detail expand.
   Shared bucket logic extracted to `src/utils/statusBuckets.ts`.

Deliberately not adopted: priority removal (conflicts with fleet-db model), repo
chips on queue cards (single-repo workspaces dominate), wireframe's mock
terminal (ours is a live PTY).

## Suggested follow-up order — status as of 2026-08-03

1. `IssueDetailPanel` maximize toggle (small). — **shipped**:
   `components/IssueDetailPanel/IssueDetailPanel.tsx:1619-1620,1712,1716`,
   header button at `components/IssueDetailPanel/header/IssueHeader.tsx:215-226`,
   `.panelMaximized` at `IssueDetailPanel.module.css:60-64`.
2. Runner badge in `IssueTable` epic group headers, mirroring the swim lanes
   (small). — **shipped**: `components/table/IssueTable.tsx:14,98` and
   `views/ListPage.tsx:24,71` both build `buildEpicLeadClaims(agents)`.
3. Add-Agent modal: epic picker for Lead role with one-lead-per-epic guard
   (medium). — **not verified**; no epic picker found in the create-agent modal.
4. VS-Code editor groups (Terminal/Info/Git/Diff/Files) on /agents (large). —
   **shipped**: `views/AgentEditorGroups.tsx`.
5. Pull Requests vertical: list view, detail, review agent (large; backend
   first). — **shipped**: see the PR row above; spec at
   `docs/product/pr-review-spec.md`.

## Related

- `docs/product/pr-review-spec.md` — the PR vertical this doc listed as missing.
- [../arch/issue-detail-view.md](../arch/issue-detail-view.md) — the detail panel
  the "Card detail" rows map onto.
- [../arch/terminal-system.md](../arch/terminal-system.md) — the terminal split
  and tab model.
- [../loom-glossary.md](../loom-glossary.md) — "aether" has two senses; this doc
  is sense (1), the UI design system.
