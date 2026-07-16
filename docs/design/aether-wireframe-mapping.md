# Aether Wireframe → loomcli Component Mapping

Source: Claude Design handoff bundle `aether-wireframe-2-v3` (`Aether Wireframe.html`
plus two chat transcripts). The wireframe was reverse-engineered *from* this app
("Aether"/K3), then iterated ~60 times. Most chrome therefore already exists 1:1;
this doc maps every wireframe region to our component and classifies the delta.

Legend: ✅ exists (no work) · 🔧 exists, needs changes · 🆕 missing.

## Chrome

| Wireframe region | Spec | Ours | Status |
|---|---|---|---|
| Top header (46px): logo, theme toggle, profile menu | pins 1–3 | `AppLayout` header (60px) + `ThemeToggle` + `UserMenu` | ✅ |
| Icon rail (54px): Kanban · Agents · Terminal · PRs, workspace switcher dots, settings | pins 4–5 | `NavRail` (58px) — has Kanban/Table, Agents, Terminal, Monitor, Settings, Workspace. No PR view, no workspace dots (we use ⌘K `WorkspaceSwitcher` + `WorkspaceSelectorBar`) | 🔧 minor: PRs view is 🆕; workspace dots optional (⌘K covers it) |
| Workspace sidebar (214px): AGENTS roster, REPOS list, add buttons; collapsible to 56px agent rail | pins 6–7, 24 | `WorkspaceTree` (~350px: ReposSection, RunningSection, AgentSection, QueueStatsBar). `AgentIconRail` (60px) already plays the "collapsed agent switcher" role on /agents | ✅ functionally; manual collapse toggle on other views is 🆕 (low value) |

## Board

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Kanban columns Backlog→Done | 6 columns | `KanbanBoard` + `StatusColumn` + `columnConfigs` — same 6 | ✅ |
| Kanban ↔ List toggle | pin 8 | `ViewSubSwitcher` (Kanban ↔ Table routes) + `IssueTable` grouped by epic | ✅ |
| Epic swimlane headers showing running agent / "Unclaimed" | pin 10 | `SwimLaneBoard`/`SwimLane` (default group-by is epic) — runner badge added: lead claims from `agentStore` → green-dot badge or italic "Unclaimed" pill on epic lanes | 🔧 **implemented** |
| Ticket card: id, title, repo chip, PR pill, assignee | pin 11 | `IssueCard` — id, title, priority, type, assignees, blocked badge. No repo chip / PR pill | 🔧 repo chip only relevant multi-repo; PR pill needs PR data we don't model |
| Priority removed, repo shown instead | chat final state | We keep P0–P4 across backend + UI | ⚠️ product decision — NOT adopted; priority is load-bearing in fleet-db |
| New Issue dialog | pin 23 | `CreateIssueModal` | ✅ |
| Repo filter pills in board header | chat phase 7 | `FilterBar` repo filter dropdown | ✅ (dropdown instead of pills) |
| Global search | pin 9 | `SearchInput` | ✅ |

## Card detail

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Slide-over with Details/Runs tabs, Approve/Reject | pins 16–17 | `IssueDetailPanel` (Details, Sessions, Comments, Activity; Approve/Reject actions) | ✅ |
| Maximize to full page (50/50 split) | pin 16 | fixed 420px | 🆕 candidate, not implemented this pass |
| Design-brief column + copy buttons | pin 18 | description copy exists; no design column (no design field in Issue model) | 🆕 needs data model first |
| PR card in detail (state, branch, diff, checks) | pin 26 | Sessions tab shows task-run diffs; no PR object | 🆕 needs PR integration |

## Agents view (the core of this design)

| Wireframe | Spec | Ours | Status |
|---|---|---|---|
| Agent switcher rail | — | `AgentIconRail` | ✅ |
| Terminal pane w/ agent header (status · role · epic line), inline CLI | pin 12 | `AgentDetailMain` + embedded `TerminalView` (real PTY, not mock) | ✅ |
| Info / Git / Diff / Files tabs | pin 12 | `AgentDetailPanel` (legacy) has logs/diff/files; not tabbed into /agents | 🆕 VS-Code editor groups + split/drag is a large standalone feature |
| **Open Queue rail, epic mode**: "Open Queue" eyebrow, epic title, claim + done line, **status distribution bar**, **tappable status filter pills**, scrollable task list, **Worker History pinned to bottom third** | pin 20 | `AgentWorkPanel` focused mode: epic tag + title, static count chips, single-fill progress bar, task list, worker history inline | 🔧 **implemented this pass** |
| **Open Queue rail, lead mode**: aggregate distribution bar, summary line, **All/Running/Not running pills**, **collapsed epic cards** ("› N" expand pill, "claimed by X · N open" / "Unclaimed · N open"), unassigned below | pin 20 | `AgentWorkPanel` lead-open mode: always-expanded epic groups, Run button, claim badge, runner strip | 🔧 **implemented this pass** |
| One-lead-per-epic guard ("on epic", "All leads are busy") | pin 31 | `epicClaims` already disables Run + shows "claimed by" | ✅ |

## Terminal / PRs / Settings / Dialogs

| Wireframe | Ours | Status |
|---|---|---|
| Terminal multi-tab + VS-Code split groups | `TerminalView` has tabs/PTY; no split panes | 🔧 split = 🆕 large |
| Pull Requests view + PR detail + PR review agent + dev-server preview | nothing | 🆕 entire vertical; needs backend PR model first |
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
6. **Epic detail tickets** — `EpicTicketsSection` in `IssueDetailPanel`
   (Details tab, epics only): claim badge, "M of N done" progress, status
   distribution bar, and the epic's child tickets sorted by status; rows
   navigate to the child via `onNavigateToIssue`. Lane-title click on the
   board opens this panel, covering the wireframe's epic-detail expand.
   Shared bucket logic extracted to `src/utils/statusBuckets.ts`.

Deliberately not adopted: priority removal (conflicts with fleet-db model), repo
chips on queue cards (single-repo workspaces dominate), wireframe's mock
terminal (ours is a live PTY).

## Suggested follow-up order (not started)

1. `IssueDetailPanel` maximize toggle (small).
2. Runner badge in `IssueTable` epic group headers, mirroring the swim lanes (small).
3. Add-Agent modal: epic picker for Lead role with one-lead-per-epic guard (medium).
4. VS-Code editor groups (Terminal/Info/Git/Diff/Files) on /agents (large).
5. Pull Requests vertical: list view, detail, review agent (large; backend first).
