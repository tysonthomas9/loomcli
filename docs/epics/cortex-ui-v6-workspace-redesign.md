# Epic: Cortex UI V6 — Workspace-Centric Redesign

**Design Reference**: https://designs.magicpath.ai/v1/swift-year-6949
**Status**: Draft
**Date**: 2026-03-14

---

## Summary

Redesign the Cortex web UI to be workspace-centric rather than agent-centric. The key shifts:
- Sidebar: flat agent list → hierarchical workspaces with nested agents
- Task detail: slide-out panel → full detail view with tabbed terminals
- Workspace creation: none → modal with git worktree setup
- Agent assignment: single backend → multi-backend dropdown (Claude, Codex, Gemini, etc.)

## Current State

### What's Built
- Kanban board with drag-drop, group-by, 6 status columns
- IssueDetailPanel (slide-out) with editable fields, design/notes, dependencies, comments
- AgentDetailPanel with Info/Diff/Log/Git tabs
- Flat AgentsSidebar listing all agents as "Agent"
- TerminalPanel (previous slide-out) for Talk to Lead
- TerminalView with multi-tab (comet worktree — in progress)
- NavRail with kanban/table/graph/monitor/observability/settings
- Open in Editor (13 editors), File Explorer + CodeMirror editor (in progress)
- ThemeToggle component (exists but not fully wired)

### What the Design Adds
1. Hierarchical workspace sidebar with collapsible groups
2. Workspace creation modal ("+ Add")
3. Two-column task detail layout with Design panel sidebar
4. Multi-backend assignee dropdown (Claude/Codex/Gemini/Cursor/Browser)
5. Terminal tabs embedded in task detail via "+" button
6. Agent role labels (Developer, QA, Architecture)
7. Dev Tools sidebar section
8. Dark mode theme

### Existing Overlapping Tickets
- `3wsub.9-12` — Workspace view plumbing, WorkspaceTree sidebar (multi-repo focused)
- `jcr0g.9-10` — Terminal tab rename + notes panel (comet, nearly done)
- `lm7aj.8` — Files tab in AgentDetailPanel
- `bfl3f` — Git Panel epic (no children yet)

---

## Tasks

### Track A: Sidebar Redesign

#### T1: Workspace Sidebar (P1)
**Replace flat AgentsSidebar with hierarchical WorkspaceSidebar**

Scope:
- Collapsible workspace groups (e.g., "Platform Core") containing nested agents
- Agent entries show: name, role label, +N changes count, status indicator dot (Ready/Working/Error)
- Click workspace name → shows workspace/epic kanban filtered view
- Work Queue section at bottom with Backlog/Open/Blocked/In Progress/Needs Review/Done counts
- Completion stats bar (e.g., "17 open · 3 closed · 15%")
- "N need push" indicator with "Push All" action
- Timestamp: "Updated 7:04:46 AM"

Files to create/modify:
- New: `components/WorkspaceSidebar/WorkspaceSidebar.tsx`
- New: `components/WorkspaceSidebar/WorkspaceGroup.tsx`
- New: `components/WorkspaceSidebar/AgentRow.tsx`
- New: `components/WorkspaceSidebar/WorkQueue.tsx`
- Modify: `App.tsx` — swap AgentsSidebar for WorkspaceSidebar
- Modify: `hooks/useAgents.ts` — extend to group agents by workspace

Backend dependency:
- `GET /api/workspaces` or extend `GET /api/status` to include workspace grouping

---

#### T2: Workspace Creation Modal (P2)
**"+ Add" button opens a modal to create new workspaces**

Fields:
- **Workspace name** (required) — validates `[a-zA-Z0-9_-]+`
- **Base branch** (optional, default: main) — dropdown of available branches
- **Agent role** (optional) — Developer / QA / Architecture / Custom

No template section. No clone URL (uses `git worktree add` from current repo).

Scope:
- Modal component with form validation
- Calls `POST /api/workspaces` → runs `git worktree add ./worktrees/<name> -b <name>`
- Success: adds new workspace to sidebar, closes modal
- Error: inline error message

Files to create/modify:
- New: `components/WorkspaceCreateModal/WorkspaceCreateModal.tsx`
- New: `components/WorkspaceCreateModal/WorkspaceCreateModal.module.css`
- Backend: New handler `POST /api/workspaces` in `handlers_workspace.go`

Depends on: T1

---

#### T8: Dev Tools Sidebar Section (P3)
**Collapsible "Dev Tools" section in workspace sidebar**

Scope:
- Collapsible section below workspace groups
- Quick-access links to: Terminal sessions, File Explorer, Git operations
- Badge showing count of active terminal sessions

Files to modify:
- Modify: `WorkspaceSidebar/WorkspaceSidebar.tsx` — add DevTools section

Depends on: T1

---

### Track B: Task Detail Redesign

#### T3: Task Detail — Two-Column Layout (P2)
**Redesign IssueDetailPanel to match design's two-column layout**

Current: Single column, everything stacked vertically with collapsible sections.
Design: Two columns — main content left, design panel pinned right.

Left column layout (top to bottom):
- Header: ID badge (e.g., "PC-CRIT1") + priority pill (P0) + status pill (BACKLOG)
- Title: Large, bold
- Tab bar: "Details" tab (active by default) + "+" dropdown to add terminal tabs
- Assignee row: avatar + name + "No status set"
- Owner row: email
- "BLOCKED BY (N)" section: clickable issue cards with lock icon + ID + title + chevron
- "DESCRIPTION" section: styled text box
- "ACTIVITY" section: comment input + post button

Right column (pinned):
- "DESIGN" header
- "Summary" card with green check icon
- "TECHNICAL APPROACH" card
- "DEPENDENCIES" card

Files to modify:
- Modify: `IssueDetailPanel/IssueDetailPanel.tsx` — restructure to two-column
- Modify: `IssueDetailPanel/IssueDetailPanel.module.css` — grid/flex two-column layout
- Modify: `IssueDetailPanel/IssueHeader.tsx` — redesigned header with pill badges
- New: `IssueDetailPanel/DesignSidebar.tsx` — right column design panel
- New: `IssueDetailPanel/BlockedBySection.tsx` — clickable blocker cards

---

#### T4: Assignee Dropdown with Multi-Backend Agents (P2)
**Dropdown for assigning tasks to different AI agent backends**

Design shows dropdown with:
- Search field at top
- Agent options: Claude (Anthropic), OpenCode, Codex (OpenAI), Gemini (Google), Cursor Agent (Cursor), Browser (Open web browser)
- Color-coded status dots per agent (red, gray, green, blue, purple, orange)
- Clicking assigns the agent backend

Scope:
- Dropdown component with search filter
- Agent list from backend configuration
- Color dot indicates connection/availability status
- Selecting updates task assignee via `PATCH /api/issues/:id`

Files to create/modify:
- Already exists: `IssueDetailPanel/AssigneeDropdown.tsx` (atlas) — extend with multi-backend
- Modify: `IssueDetailPanel/IssueDetailPanel.tsx` — wire assignee dropdown
- Backend: May need `GET /api/backends` endpoint listing available agent backends

Depends on: T3

---

#### T5: Terminal Tabs in Task Detail (P2)
**"+" tab on task detail opens agent terminal sessions**

Design shows:
- Tab bar with "Details" + agent tabs (e.g., "claude", "codex", "gemini")
- "+" dropdown to add new terminal tab — shows same agent backend list as T4
- Each tab renders a full terminal (wterm) connected to that agent's tmux session
- Terminal shows breadcrumb: `superset / cloud-ws` with `claude` badge + `merge` + `Review Changes` buttons

Scope:
- Tab bar component on IssueDetailPanel
- "+" opens backend picker dropdown (reuse from T4)
- Selecting creates a tmux session and renders TerminalInstance (from comet)
- Terminal header shows workspace path + agent badge + git action buttons

Files to create/modify:
- New: `IssueDetailPanel/TaskTerminalTabs.tsx` — tab management
- New: `IssueDetailPanel/TaskTerminalTab.tsx` — single terminal tab
- Reuse: `TerminalView/TerminalInstance.tsx` (from comet)
- Modify: `IssueDetailPanel/IssueDetailPanel.tsx` — integrate tab bar

Depends on: T3, T4, comet TerminalView merge

---

### Track C: Independent

#### T6: Dark Mode / Theme System (P3)
**Full dark mode matching the design**

Design is entirely dark-themed:
- Background: #1a1a1a / near-black
- Cards: dark gray with subtle borders
- Text: white/light gray
- Accents: amber/gold (Talk to Lead button), green (status), red (priority)

Scope:
- CSS custom properties for all colors (already partially in `variables.css`)
- ThemeToggle component (already exists at `components/ThemeToggle/`)
- Wire toggle into NavRail or header
- Persist preference in localStorage
- System preference detection (prefers-color-scheme)

Files to modify:
- Modify: `styles/variables.css` — add `[data-theme="dark"]` overrides
- Modify: `ThemeToggle/ThemeToggle.tsx` — wire to context
- New: `hooks/useTheme.ts` (already exists in atlas)
- Modify: `App.tsx` — apply theme to root element
- Modify: All `.module.css` files — use CSS custom properties instead of hardcoded colors

---

#### T7: Agent Role Labels (P3)
**Replace generic "Agent" with specific role labels**

Design shows: Developer, QA, Architecture (not just "Agent").

Scope:
- Configurable per-agent in `.loom.yaml` or workspace config
- Displayed in: sidebar agent row, agent detail panel header, assignee dropdown
- Default: "Agent" (backward compatible)

Files to modify:
- Modify: Agent config parsing in Go backend
- Modify: `AgentRow.tsx` (new from T1) — display role
- Modify: API response to include role field

---

## Dependency Graph

```
T1 (Workspace Sidebar) ──────► T2 (Create Modal)
         │
         └────────────────────► T8 (Dev Tools Section)

T3 (Task Detail Layout) ─────► T4 (Assignee Dropdown)
                                       │
                                       ▼
                                T5 (Terminal Tabs in Detail)
                                       │
                                       ▼
                              [comet merge required]

T6 (Dark Mode) ─── independent
T7 (Agent Roles) ── independent (enhances T1)
```

## Implementation Order

| Phase | Tasks | Priority | Estimate |
|-------|-------|----------|----------|
| 1     | T1 (Workspace Sidebar)      | P1 | Agent-sized |
| 2     | T3 (Task Detail Layout)     | P2 | Agent-sized |
| 3     | T2 (Workspace Create Modal) | P2 | Agent-sized |
| 4     | T4 (Assignee Dropdown)      | P2 | Agent-sized |
| 5     | T5 (Terminal Tabs)          | P2 | Agent-sized (blocked on comet) |
| 6     | T7 (Agent Roles)            | P3 | Small |
| 7     | T6 (Dark Mode)              | P3 | Agent-sized |
| 8     | T8 (Dev Tools Section)      | P3 | Small |

---

## User Stories

### Epic Story
> As a **project lead** managing multiple AI agents across parallel worktrees,
> I want the Cortex UI organized around **workspaces** instead of a flat agent list,
> so that I can see which agents belong to which workspace, quickly spin up new workspaces,
> and interact with agent terminals directly from task details without switching views.

---

### T1: Workspace Sidebar

**US-1.1: View agents grouped by workspace**
> As a project lead, I want to see my agents organized under their workspace groups in the sidebar,
> so that I can quickly understand which agents are working in which workspace and their current status.

Acceptance criteria:
- Sidebar shows "Workspaces (N)" header with count of active workspaces
- Each workspace is a collapsible group showing its name, icon, and notification badge
- Agents nested under their workspace show: name, role label, +N changes, status dot
- Clicking an agent opens the AgentDetailPanel (existing behavior preserved)
- Collapsed workspace hides its agents; expanded shows them

**US-1.2: Filter kanban by workspace**
> As a project lead, I want to click a workspace name to filter the kanban board to only that workspace's tasks,
> so that I can focus on one workspace at a time without noise from other workspaces.

Acceptance criteria:
- Clicking workspace name filters kanban to tasks assigned to that workspace's agents
- Breadcrumb shows "Platform Core / Kanban" indicating active filter
- Clicking away or clicking the workspace again clears the filter

**US-1.3: See work queue summary**
> As a project lead, I want a work queue summary at the bottom of the sidebar showing task counts by status,
> so that I can gauge project health at a glance without opening a separate dashboard.

Acceptance criteria:
- Shows counts for: Backlog, Open, Blocked, In Progress, Needs Review, Done
- Shows completion stats: "N remaining · M closed · X%"
- Shows "N need push" with a "Push All" button when unpushed changes exist
- Shows "Updated HH:MM:SS" timestamp of last data refresh

---

### T2: Workspace Creation Modal

**US-2.1: Create a new workspace**
> As a project lead, I want to click "+ Add" in the sidebar and fill out a simple form to create a new workspace,
> so that I can onboard a new agent to work on tasks without leaving the UI or using the CLI.

Acceptance criteria:
- Clicking "+ Add" opens a centered modal overlay
- Form fields: workspace name (required), base branch (dropdown, default: main), agent role (optional)
- Name validates against `[a-zA-Z0-9_-]+` with inline error
- Submitting creates a git worktree and adds the workspace to the sidebar
- Modal closes on success; shows inline error on failure
- Pressing Escape or clicking outside closes the modal without action

**US-2.2: Avoid duplicate workspace names**
> As a project lead, I want the form to prevent me from creating a workspace with a name that already exists,
> so that I don't accidentally conflict with an active agent's worktree.

Acceptance criteria:
- Real-time validation shows error if name matches an existing workspace
- Submit button disabled while name conflicts

---

### T3: Task Detail — Two-Column Layout

**US-3.1: See task details by default on click**
> As a project lead, I want clicking any task card on the kanban board to immediately show its full details,
> so that I can review a task's description, blockers, and design without extra clicks.

Acceptance criteria:
- Clicking a task card opens the detail panel (already works — preserve this)
- Detail panel shows the "Details" tab as the active default
- Header shows: task ID badge, priority pill, status pill
- Left column shows: assignee, owner, blocked-by section, description, activity/comments
- Panel can be closed with X button or Escape key

**US-3.2: See task design in a pinned sidebar**
> As a project lead, I want the task's design (summary, technical approach, dependencies) shown in a persistent right column,
> so that I can read the implementation plan alongside the task description without scrolling or expanding sections.

Acceptance criteria:
- Right column always visible when detail panel is open (not collapsible)
- Shows three cards: "Summary" (with status icon), "Technical Approach", "Dependencies"
- Content comes from the task's `design` field parsed into sections
- If no design exists, right column shows "No design yet" placeholder

**US-3.3: See and navigate blocker dependencies**
> As a project lead, I want to see which tasks block the current task as clickable cards,
> so that I can quickly navigate to blockers and understand what's holding up work.

Acceptance criteria:
- "BLOCKED BY (N)" section shows each blocker as a card with: lock icon, task ID badge, title, chevron
- Clicking a blocker card navigates to that task's detail view
- Blocker cards visually distinguished (e.g., amber/warning border)

**US-3.4: Comment on tasks**
> As a project lead, I want to add comments to a task from the detail panel,
> so that I can provide feedback to agents or leave notes without using the CLI.

Acceptance criteria:
- Activity section at bottom of left column
- Text input with "Add a comment..." placeholder
- "Post Comment" button submits via API
- New comments appear in the activity feed immediately (optimistic update)
- Existing comments show author, timestamp, and content

---

### T4: Assignee Dropdown with Multi-Backend Agents

**US-4.1: Assign a task to a specific AI backend**
> As a project lead, I want to pick which AI agent backend (Claude, Codex, Gemini, etc.) works on a task,
> so that I can route work to the best-suited agent or distribute load across backends.

Acceptance criteria:
- Clicking the assignee area opens a dropdown
- Dropdown shows searchable list of available backends
- Each option shows: color-coded dot, agent name, provider label (e.g., "Anthropic", "OpenAI")
- Selecting an agent updates the task assignee and closes the dropdown
- Current assignee shown in the task header with avatar and name

**US-4.2: See agent availability status**
> As a project lead, I want to see which agent backends are currently available vs busy,
> so that I can assign tasks to agents that can pick them up immediately.

Acceptance criteria:
- Status dot color indicates: green = available, red = error/offline, gray = busy, blue = connected
- Hovering shows tooltip with status detail

---

### T5: Terminal Tabs in Task Detail

**US-5.1: Open an agent terminal from a task**
> As a project lead, I want to click "+" on a task's tab bar and open a live terminal to an agent backend,
> so that I can interact with the agent working on this task without leaving the task detail view.

Acceptance criteria:
- "+" button next to "Details" tab opens a dropdown of available agent backends
- Selecting one creates a new tab with the agent's name
- Tab renders a live wterm terminal connected to the agent's tmux session
- Multiple terminal tabs can be open simultaneously
- Switching between "Details" and terminal tabs preserves state in both

**US-5.2: See terminal context and actions**
> As a project lead, I want each terminal tab to show the workspace path and provide git action buttons,
> so that I can see which workspace the agent is in and trigger merges or reviews.

Acceptance criteria:
- Terminal header shows: workspace breadcrumb (e.g., `superset / cloud-ws`), agent badge, "merge" button, "Review Changes" button
- Terminal connection status shown (connected/reconnecting)
- Close button on each tab removes it

---

### T6: Dark Mode / Theme System

**US-6.1: Switch to dark mode**
> As a user working late or in a dark environment, I want to toggle between light and dark themes,
> so that the UI is comfortable to use regardless of ambient lighting.

Acceptance criteria:
- Theme toggle accessible in the NavRail or top-right corner
- Dark theme matches design: near-black background, dark gray cards, light text, amber/green/red accents
- Theme preference persists across sessions (localStorage)
- Defaults to system preference (prefers-color-scheme) on first visit
- All components render correctly in both themes (no unreadable text, invisible borders, etc.)

---

### T7: Agent Role Labels

**US-7.1: See what role each agent plays**
> As a project lead, I want to see each agent's role (Developer, QA, Architecture) in the sidebar and detail panels,
> so that I can understand the team composition and assign appropriate work.

Acceptance criteria:
- Agent role shown below agent name in sidebar (e.g., "falcon / Developer")
- Role configurable in `.loom.yaml` per-agent
- Default role is "Agent" for existing behavior
- Role displayed in: sidebar, agent detail panel, assignee dropdown

---

### T8: Dev Tools Sidebar Section

**US-8.1: Quick access to developer tools**
> As a project lead, I want a "Dev Tools" section in the sidebar with shortcuts to terminals, file explorer, and git,
> so that I can access operational tools without navigating away from the kanban view.

Acceptance criteria:
- Collapsible "Dev Tools" section below workspace groups
- Shows count badge (e.g., number of active terminal sessions)
- Links/buttons to: Terminal view, File Explorer, Git operations
- Clicking a link navigates to the corresponding view

---

## Open Questions

1. Should workspace creation support clone URL for multi-repo, or just `git worktree add` from current repo?
2. Should this epic absorb existing `3wsub` workspace tickets or be parallel?
3. Are the agent backend options (Claude, Codex, Gemini, Cursor, Browser) fixed or configurable?
4. Should the "Review Changes" button in terminal header trigger a PR flow?
