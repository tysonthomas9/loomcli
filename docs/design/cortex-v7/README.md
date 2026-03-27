# Cortex V7 Design Spec

Design mocks:
- Kanban View: https://designs.magicpath.ai/v1/swift-year-6949
- Sidebar View: https://designs.magicpath.ai/v1/bright-minute-1588

## Layout

### NavRail (left icon bar, ~60px wide)
- Icon 1 (grid): Kanban View
- Icon 2 (terminal/monitor): Sidebar View (dedicated terminal + task view)
- Settings gear (bottom): Settings

### Sidebar (shared across both views, ~280px wide)
The sidebar is **identical** in both views. It does NOT change when switching NavRail views.

**Structure:**
- "WORKSPACES" header + "+" add button
- Workspace entries (collapsible):
  - "Platform Core" workspace
    - "Talk to Lead" entry (Claude Opus backend, green status dot)
    - Epic "Auth & Identity" (collapsible, green status dot)
      - Task "use any agents" — branch: `use-any-agents`, git stats: +46 -1, green dot (working)
      - Task "create parallel branch" — branch: `create-parallel-b...`, git stats: +193, orange dot (reviewing)
      - "+ Add Task" inline button
    - Epic "Cloud Workspaces" (collapsed, green dot)
    - "+ Add Epic" inline button
  - "Dev Tools" workspace (collapsed)
  - "+ New Workspace" button
- Status bar at bottom: "3 working · 1 reviewing · 1 idle"

**Task row format:** task icon | task title | (right-aligned) status dot
Below title: branch name | git stats (+N green / -N red)

**Status dot colors:** green = working, orange = reviewing, grey = idle, red = error

### Main Content Area

**Kanban View (NavRail icon 1):**
- Breadcrumb: "Platform Core > Kanban"
- Kanban board columns: Backlog | Open | Blocked | In Progress | Needs Review | Done
- Issue cards show: ID (top-left), priority badge (top-right, P0 red/P2 yellow/P3 blue), title
- In Progress / Needs Review cards also show: agent name + current action text below title
- "Talk to Lead" FAB button (bottom-right)

**Sidebar View (NavRail icon 2):**
- Empty state: "Nothing selected — Select Talk to Lead, an Epic, or a Task from the sidebar"
- Click Talk to Lead → terminal/Claude session fills the content area
- Click task → task detail fills the content area

### Task Detail Panel (same in both views)
3/4-width slide-out from right. Triggered by:
- Clicking a kanban card (Kanban View)
- Clicking a task in the sidebar tree (Sidebar View)

**Panel layout:**
- **Left pane (~60%)**:
  - "Details" tab (active)
  - Assignee with avatar + email
  - Status badge (e.g. "CRITICAL" red) + tags
  - Approve / Reject buttons (green/red, shown for review-status tasks)
  - Description section (markdown rendered)
  - Activities section
  - Comment form: "Write a message..." textarea + "Post Comment" button
- **Right pane (~40%)**:
  - "Summary" metadata section
  - Additional context fields

### Agent Panel (Kanban View only)
3/4-width slide-out from right, triggered by clicking an agent card in the sidebar.

**Two modes:**

1. **Agent Info** (default):
   - Header: agent name (large), colored initial badge, status dot + "Has Changes"
   - Tabs: Info | Logs
   - Info tab: Current Task (or "No active task"), Recent Activity (timestamped log entries), Agent Info (name, role badge, status)

2. **Task Review** (when agent has a review-status task):
   - Shows the full task detail panel instead of agent info
   - Same layout as the Task Detail Panel above

## Reference Screenshots
- `kanban-view.png` — Full kanban view with sidebar
- `sidebar-view.png` — Sidebar view with empty state
- `task-detail-panel.png` — Task detail panel from kanban card click
- `agent-panel-info.png` — Agent panel (ember, info mode)
- `agent-panel-review.png` — Agent panel (nova, review mode with approve/reject)
- `sidebar-task-detail.png` — Task detail from sidebar click
- `talk-to-lead-terminal.png` — Talk to Lead terminal session

## Design Tokens (from mock)
- Background: dark theme (#1A1A1A base)
- Card background: slightly lighter than base
- Text: white with opacity variants (0.8 secondary, 0.6 tertiary)
- Priority P0: red badge
- Priority P2: yellow/amber badge
- Priority P3: blue badge
- Status working: green dot
- Status reviewing: orange dot
- Status idle: grey dot
- Status error: red dot + "Error" text
- Git stats: green for additions, red for deletions
- Approve button: green
- Reject button: red
- "Talk to Lead" FAB: green/accent
- "Post Comment" button: yellow/amber
