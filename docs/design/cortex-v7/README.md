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

### Epic Detail Panel
3/4-width slide-out from right. Triggered by clicking an epic in the sidebar tree.

**Tab bar:**

| Tab | Type | Description |
|-----|------|-------------|
| **Overview** | Fixed | Epic summary, description, progress bar (N/M tasks done) |
| **Tasks** | Fixed | List of all child tasks with status, assignee, priority. Click to open task detail. |
| **Dependency Graph** | Fixed | XY Flow (React Flow) visualization of task dependencies within the epic |
| **Sessions** | Fixed | Aggregated agent session logs across all tasks in this epic |
| **Diff** | Fixed | Combined diff of all task branches vs main for this epic |
| **Files** | Fixed | Aggregated file browser showing all files touched across epic tasks |
| **[Claude]** | Dynamic | Terminal session scoped to this epic |
| **[+]** | Action | Opens backend selector dropdown to create a new terminal tab |

**Overview tab:**
- Epic title, description (markdown)
- Progress: "5/12 tasks done" with progress bar
- Status breakdown: N open, N in progress, N blocked, N review, N done
- Labels, owner, created/updated dates

**Tasks tab:**
- Table/list of all child tasks under this epic
- Columns: status dot, title, assignee, priority badge, branch, git stats
- Click a task row → opens Task Detail Panel (replaces epic panel)
- Bulk actions: select multiple → assign, change priority, close
- Filter by status (open/blocked/done)

**Dependency Graph tab:**
Interactive React Flow (xyflow) graph showing task dependencies within the epic.

Purpose: Visualize which tasks block which, so the lead can identify **waves** of parallelizable work — tasks with no unresolved dependencies can be sent to agents simultaneously.

- Each node = a task (shows: ID, title truncated, status color, assignee)
- Edges = dependency relationships (A → B means A blocks B)
- Node colors match status: green (done), blue (in progress), yellow (open/ready), red (blocked), grey (backlog)
- **Wave highlighting**: Tasks with all dependencies satisfied are highlighted as "ready wave" — these can be dispatched to agents in parallel
- Click a node → opens that task's detail panel
- Zoom, pan, fit-to-view controls
- Layout: dagre left-to-right (dependencies flow left → right, parallel tasks stack vertically)
- Mini-map for large epics

**Wave dispatch workflow:**
1. Open epic → Dependency Graph tab
2. See which tasks are in the current "ready wave" (all deps satisfied)
3. Select wave tasks → bulk assign to available agents
4. As agents complete tasks, the next wave becomes ready automatically

### Task Detail Panel (same in both views)
3/4-width slide-out from right. Triggered by:
- Clicking a kanban card (Kanban View)
- Clicking a task in the sidebar tree (Sidebar View)
- Clicking a task row in the Epic Detail Panel's Tasks tab

**Tab bar:**
The panel has a tabbed interface. Fixed tabs on the left, dynamic terminal tabs in the middle, and a "+" button to create new terminal sessions.

| Tab | Type | Description |
|-----|------|-------------|
| **Details** | Fixed | Issue detail view (default active tab) |
| **Sessions** | Fixed | Agent session logs — shows all agent sessions for this task (design agent, task agent, review agent, etc.) |
| **Diff** | Fixed | Diff between task branch and main |
| **Files** | Fixed | File browser for the agent's worktree |
| **[Claude]** | Dynamic | Terminal session with Claude backend (brand color #d4a574) |
| **[Codex]** | Dynamic | Terminal session with Codex backend (brand color #10a37f) |
| **[+]** | Action | Opens backend selector dropdown to create a new terminal tab |

Multiple terminal tabs can be open simultaneously (e.g. a Claude tab and a Codex tab side by side). Each terminal session is scoped to the task.

**Backend selector dropdown** (from "+" button):
- Claude (Anthropic) — #d4a574
- Codex (OpenAI) — #10a37f
- OpenCode (Open Source) — #6366f1
- Gemini (Google) — #8e24aa
- Cursor (Anysphere) — #00e5ff
- Browser (System) — #f59e0b
- Terminal/Shell (System) — #6b7280

Only available/configured backends are shown. Each backend has a brand color used for the tab indicator.

**Details tab layout:**
- **Left pane (~60%)**:
  - Assignee with avatar + email
  - Status badge (e.g. "CRITICAL" red) + tags
  - Approve / Reject buttons (green/red, shown for review-status tasks)
  - Description section (markdown rendered)
  - Activities section
  - Comment form: "Write a message..." textarea + "Post Comment" button
- **Right pane (~40%)**:
  - "Summary" metadata section
  - Child issues section
  - Additional context fields

**Sessions tab:**
Lists all agent sessions that have run against this task. Each session shows:
- Agent name + backend (e.g. "falcon — Claude")
- Session type: design, task, review
- Start/end time, duration
- Token usage / cost
- Expandable transcript

**Diff tab:**
Side-by-side or unified diff between the task's branch and main. Shows:
- File list with change counts
- Expandable file diffs with syntax highlighting
- Commit selector for comparing specific commits

**Files tab:**
File browser for the agent's worktree. Shows:
- Directory tree
- File viewer with syntax highlighting (CodeMirror)
- Read-only by default

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

---

## Planned Improvements

### 1. Live Agent Activity

Static status dots replaced with real-time activity indicators.

**Sidebar live ticker:**
Each agent/task entry in the sidebar shows a one-line scrolling activity preview beneath the branch name:
- "Reading handlers_auth.go" → "Running go test ./..." → "Writing middleware.go (47 lines)"
- Updates in real-time via SSE from the agent's session transcript
- Truncated to one line, fades in/out on change
- Click to expand to the agent's full terminal

**Progress indicator on tasks:**
Replace the status dot with a progress ring when an agent is actively working:
- Design agent: progress through plan steps (e.g. 3/7 sections written)
- Task agent: progress through workflow steps (Step 1-9 from task.md)
- Review agent: shows test pass rate (12/15 passing)
- Ring fills clockwise, color matches status (green/orange)

**Pulse animation:**
- Active dots pulse subtly (CSS animation, 2s ease-in-out) when the agent process is running
- Static dot = agent idle or waiting for API response
- Immediately visible which agents are "hot" at a glance

### 2. Command Palette (Cmd+K Universal)

Extend the existing Cmd+K workspace switcher into a universal command palette.

**Search modes:**
- Default: fuzzy search across tasks, epics, agents, files, sessions
- `>` prefix: run commands ("assign falcon to bd-xyz", "close bd-abc", "merge nova")
- `@` prefix: jump to agent ("@ember" → opens agent panel)
- `#` prefix: jump to task/epic ("#bd-xyz" → opens task detail)
- `/` prefix: run loom commands ("/merge falcon", "/sync all", "/plan nova")

**Results layout:**
- Grouped by type: Tasks, Epics, Agents, Files, Commands
- Each result shows: icon, title, context (epic name, status), keyboard shortcut if applicable
- Arrow keys to navigate, Enter to select, Esc to close
- Recent items shown on empty query

**Quick actions in results:**
- Hover/select a task result → see inline actions: Assign, Approve, Close
- Hover an agent → see: View Logs, Open Terminal, Reset

**Implementation:**
- Component: `CommandPalette` using `cmdk` library pattern
- Layered at `LAYER_COMMAND_PALETTE = 55` (above modals, below confirm dialogs)
- Focus trap, returns focus on close
- Indexes all tasks, epics, agents on mount, updates via SSE

### 3. Notification / Alert System

**Toast notifications for agent events:**
- Task completed: "falcon completed bd-xyz — Auth middleware" (green)
- Agent error: "ember crashed on bd-abc — exit code 1" (red, persistent until dismissed)
- Plan ready for review: "nova's plan for bd-def is ready" (amber, with "Review" action button)
- Merge conflict: "spark has conflicts on bd-ghi" (red, with "Resolve" action)
- Agent idle: "cobalt has been idle for 10 minutes — no tasks available" (grey, dismissable)

**Notification bell (header):**
- Bell icon in header with unread count badge
- Click → dropdown showing recent notifications grouped by time
- Mark all as read, click notification to navigate to relevant panel
- Persisted to localStorage, cleared on read

**Sound alerts (optional):**
- Settings toggle: "Play sound on agent errors"
- Subtle notification sound for errors only (not completions)
- Uses Web Audio API, no external sound files

**NavRail badges:**
- Kanban icon: badge showing count of tasks in "Needs Review" status
- Terminal icon: badge showing count of active terminal sessions
- Badges are real-time via SSE

### 4. Kanban Board Improvements

**Swimlanes by epic:**
- Group-by dropdown in header: None (flat) | Epic | Assignee | Priority
- Epic swimlanes: horizontal lanes, each with the epic name as header
- Collapsible lanes with task count
- Persisted to localStorage per workspace

**Card hover preview:**
- Hover a card for 500ms → popover appears with:
  - Description preview (first 3 lines)
  - Design status (has design? needs revision?)
  - Assignee + agent status
  - Dependencies (blocked by X, blocks Y)
- Popover positioned to avoid overflow, dismisses on mouse leave
- No preview on touch devices (click opens panel instead)

**Drag with agent auto-assign:**
- Drag card from Open → In Progress
- If no assignee: shows AssigneePrompt with available agents
- If already assigned: moves directly, agent picks up via polling
- Visual: drop zone highlights when dragging over valid column

**Column WIP limits:**
- Configurable per column in Settings (default: unlimited)
- When at limit: column header turns amber, drag into column shows warning
- When over limit: column header turns red
- Display: "In Progress (3/5)" in column header

**Quick agent filter:**
- Click agent name in sidebar → kanban instantly filters to only that agent's tasks
- Active filter shown as chip in header: "Filtered: falcon ×"
- Click × or press Esc to clear filter
- Multiple agent selection via Cmd+click

### 5. Dependency Graph Improvements

**Cross-epic dependency view:**
- New tab in the main Kanban view header: "Dependencies"
- Shows ALL tasks across ALL epics in a single graph
- Epic clusters: tasks within same epic are visually grouped (dashed border)
- Cross-epic edges highlighted in a different color (orange vs grey for intra-epic)
- Useful for identifying cross-epic blockers that aren't visible in individual epic graphs

**Critical path highlighting:**
- Toggle button: "Show Critical Path"
- The longest dependency chain from any open task to any leaf task
- Highlighted with a bold red edge color and thicker line
- Node labels on critical path get a subtle red glow
- Tooltip: "Critical path: 5 tasks, estimated X hours"

**Time estimates on edges:**
- If task has `estimated_hours` field or historical average, show on edges
- Cumulative time shown on critical path
- "This wave will take ~4h based on average task completion"

**"What if" simulation mode:**
- Toggle: "Simulate"
- Click a task node to mark it as "simulated done"
- Graph re-renders: dependent tasks change from blocked (red) to ready (yellow)
- Shows: "Completing this task would unblock 3 others"
- Click "Apply" to actually close the task, or "Reset" to exit simulation

### 6. Agent Workload Dashboard

New view accessible from NavRail (third icon, chart/monitor icon) or from Monitor header tab.

**Agent utilization chart:**
- Horizontal bar chart, one bar per agent
- Bar segments: working (green), reviewing (orange), idle (grey), error (red)
- Time range selector: Last 1h | 6h | 24h | 7d
- Shows percentage utilization: "falcon: 87% active"

**Cost tracker:**
- Table: Agent | Tokens In | Tokens Out | Sessions | Est. Cost
- Grouped by: Agent | Epic | Day
- Running total at bottom with budget line if configured
- Spark line showing cost trend over time
- Alert threshold: configurable in Settings, turns row red when exceeded

**Error rate panel:**
- Per-agent error count and classification
- Error types: Rate Limited, Code Error, Test Failure, Timeout, Crash
- Click error → navigates to the session where it happened
- Trend: "ember: 3 errors in last 24h (up from 1)"

**Throughput chart:**
- Line chart: tasks completed per day over the last 30 days
- Stacked by agent (each agent a different color)
- Average line overlay
- "This week: 14 tasks completed (↑ 23% vs last week)"

### 7. Session Replay

Enhancements to the Sessions tab in task/epic detail panels.

**Diff timeline:**
- Horizontal scrubber showing the session duration
- Markers on the timeline for each file write/edit
- Scrub to a point → see the cumulative diff at that moment
- "At 5:23, the agent had modified 3 files with +127/-34 lines"
- Playback speed: 1x, 2x, 5x, 10x

**Tool call visualization:**
- Timeline view showing each tool call as a colored bar
- Colors by tool type: Read (blue), Write (green), Bash (yellow), Grep (purple), Agent (orange)
- Bar width = duration of the tool call
- Hover → shows tool input/output preview
- Patterns visible: "agent is stuck in a read-grep-read loop" or "agent wrote 10 files in rapid succession"
- Filter by tool type to isolate specific patterns

**Fork from session:**
- Button on any tool call in the timeline: "Fork from here"
- Creates a new terminal session pre-loaded with:
  - The task context
  - A summary of what the agent did up to that point
  - The current file state at that moment
- Use case: agent went wrong at step 5, fork and manually guide it from step 5

### 8. Review Queue View

Dedicated view for the lead's primary workflow: reviewing agent output.

**Accessed from:** NavRail badge on Kanban icon, or header "Review Queue" button when review count > 0.

**Queue layout:**
- Left column (~30%): list of tasks needing review, sorted by priority then age
- Right column (~70%): selected task's review content
- "Next" / "Previous" buttons to advance through the queue without going back

**Review content (right column):**
- Split view: Design (top) | Diff (bottom)
- Design section: rendered markdown of the task's design field
- Diff section: file changes from the agent's branch vs main
- Side-by-side, resizable divider

**Review actions:**
- Approve (green button) → sets status to open, task available for implementation
- Request Changes (amber) → opens feedback form, adds comment, sets needs-revision label
- Reject (red) → closes task with reason
- Skip → moves to next in queue, comes back later
- Keyboard: `a` approve, `c` request changes, `r` reject, `n` next, `p` previous

**Review checklist:**
- Configurable per task type in Settings
- Auto-checks:
  - "Design field present?" — checks design is non-empty
  - "Tests specified?" — checks design mentions test strategy
  - "Dependencies listed?" — checks design has dependencies section
  - "No TODOs?" — greps design for TODO markers
- Manual checks: lead ticks off items (e.g. "Architecture makes sense", "Scope is reasonable")
- Checklist state persisted per task, shown in review queue

**Batch review:**
- Select multiple tasks from same epic → "Review batch"
- Shows tasks sequentially with shared context (epic description pinned at top)
- Batch approve: approve all selected with one click

### 9. Sidebar Improvements

**Drag to reorder epics:**
- Drag handle on epic rows (6-dot grip icon)
- Drag to reorder within workspace
- Order persisted to workspace config
- Keyboard: Alt+ArrowUp/Down to move

**Epic progress bar inline:**
- Thin progress bar (3px) under each epic name
- Fills proportionally to done/total tasks
- Color: green segment for done, transparent for remaining
- Hover → tooltip: "5/12 tasks done (42%)"

**Sidebar search/filter:**
- Search icon at top of sidebar, or `/` keyboard shortcut
- Type to fuzzy-filter the tree: matching tasks/epics highlighted, non-matching hidden
- Searches across: task title, epic title, branch name, task ID
- Clear with Esc or × button

**Collapse all / expand all:**
- Two small buttons next to "WORKSPACES" header: expand-all (↕) and collapse-all (⊟)
- Expands/collapses all workspace and epic entries simultaneously
- Persisted to localStorage

**Recently viewed:**
- "Recent" section at top of sidebar (above WORKSPACES)
- Shows last 5 items opened (tasks, epics, agents)
- Each entry: icon + title + time ago ("2m ago")
- Click to re-open
- Collapsible, hidden by default if sidebar is narrow

### 10. Keyboard-First Workflow

Full keyboard navigation for power users who want to never touch the mouse.

**Navigation (vim-style):**
- `j` / `k`: move selection down/up in kanban cards, sidebar items, or review queue
- `h` / `l`: move between kanban columns (left/right)
- `Enter`: open selected item's detail panel
- `Esc`: close panel → deselect → collapse sidebar (progressive, layered)
- `g g`: jump to first item, `G`: jump to last item

**Quick actions:**
- `a`: assign selected task → opens agent picker
- `p`: change priority → opens priority picker (0-4)
- `s`: change status → opens status picker
- `c`: add comment → focuses comment form in detail panel
- `d`: view diff for selected task
- `e`: edit description

**Review shortcuts:**
- `r`: jump to next review item (opens review queue if not open)
- `Shift+A`: approve current review item
- `Shift+R`: reject with comment
- `n` / `p`: next/previous in review queue

**View switching:**
- `1`: Kanban view
- `2`: Sidebar view
- `3`: Monitor/Dashboard view
- `0`: Settings
- `?`: toggle keyboard cheatsheet

**Search:**
- `/`: focus sidebar search
- `Cmd+K`: command palette
- `Cmd+F`: search within current view (kanban text filter or terminal search)

**Selection:**
- `Space`: toggle selection on current item (for bulk actions)
- `Shift+j` / `Shift+k`: extend selection up/down
- `Cmd+A`: select all visible items
- After selection: `a` to bulk assign, `p` to bulk change priority, `x` to close selected

**Visual feedback:**
- Selected item has a visible focus ring (2px accent color border)
- Current position indicator in kanban: highlighted card with subtle glow
- Keyboard shortcut hints appear on hover for buttons (e.g. "Approve [Shift+A]")

**Cheatsheet (? key):**
- Full-screen overlay organized by section: Navigation, Actions, Review, Views, Selection
- Each shortcut shows: key combo, description, context (where it works)
- Search within cheatsheet to find a shortcut
- "Press any key to dismiss"

---

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
