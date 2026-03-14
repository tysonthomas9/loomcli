# Cortex V6 — User Stories

Reference design: https://designs.magicpath.ai/v1/swift-year-6949

---

## Epic 1: Workspace Selector & Kanban Filtering

### Story 1.1: Switch between projects
**As a** project lead managing multiple repos (e.g., Platform Core + Dev Tools),
**I want to** select a workspace from the sidebar and see only that workspace's issues on the kanban,
**so that** I can focus on one project at a time without noise from other repos.

**Acceptance criteria:**
- Sidebar shows "Workspaces (N)" header with a count of configured workspaces
- Each workspace is a collapsible group with a radio-select indicator
- Clicking a workspace filters the kanban, table view, and work queue stats to that workspace's repos
- Selection persists across page reloads (localStorage)
- Breadcrumb above the board shows `● WorkspaceName / Kanban`

**Open questions:**
- Should the work queue stats (Backlog 6, Open 3, etc.) update per-workspace or stay global?
- Can a user see "All Workspaces" as an option, or must they always pick one?

---

### Story 1.2: See my agents grouped by workspace
**As a** project lead,
**I want to** see which agents belong to which workspace,
**so that** I know who's working on what project.

**Acceptance criteria:**
- Agents are nested under their workspace group in the sidebar
- Each agent shows: name, role label (Developer / QA / Architecture), change count (+366), status dot
- Collapsing a workspace hides its agents
- If an agent is in error state, the workspace group shows a warning indicator

**Open questions:**
- Can an agent belong to multiple workspaces (cross-repo)?
- What happens to agents that aren't assigned to any workspace?

---

### Story 1.3: Add a new workspace
**As a** project lead setting up a new project,
**I want to** click "+ Add" to create a workspace,
**so that** I can start organizing issues and agents for a new repo.

**Acceptance criteria:**
- "+ Add" button next to "Workspaces" header
- Opens a form/modal to configure: workspace name, repo path(s), default branch
- New workspace appears in sidebar immediately after creation
- Alternatively: "+ Add" navigates to Settings where workspace config lives

**Open questions:**
- Is workspace creation a UI operation, or should it always go through loom.yaml config?
- Should we support workspace deletion/rename from the UI?

---

### Story 1.4: Single-repo fallback
**As a** user with a simple single-repo setup (no workspace config),
**I want** the UI to work exactly as before — no workspace selector cluttering the sidebar,
**so that** the feature doesn't complicate simple setups.

**Acceptance criteria:**
- When no workspaces are configured, the sidebar shows agents directly (no "Workspaces" header)
- All existing behavior is preserved: kanban shows all issues, agents listed flat
- No "Workspaces 0" or empty state visible

---

## Epic 2: Detail View with Terminal Tabs

### Story 2.1: Open an issue in full detail view
**As a** project lead reviewing an issue,
**I want to** click a kanban card and see the full issue details in the main content area,
**so that** I have room to read the description, design, and comments without a cramped side panel.

**Acceptance criteria:**
- Clicking a card replaces the kanban with a full-width detail view
- Shows: issue ID, title, "Details" tab (default), "+" button to add tabs
- Details tab contains: assignee, owner, status, blocked-by list, description, design (2-column: left=metadata+description, right=design panel), activity/comments
- Close (X) button or back arrow returns to the kanban
- Escape key returns to kanban
- The sidebar remains visible on the left

**Open questions:**
- Should we keep the old slide-out panel as a "quick peek" (e.g., hover or right-click), or fully replace it?
- What about deep-linking? Should `/issue/bd-abc` be a URL route?

---

### Story 2.2: Add a terminal tab to an issue
**As a** project lead investigating an issue,
**I want to** click "+" and choose a backend (Claude, Codex, OpenCode) to open an embedded terminal tab alongside the issue details,
**so that** I can discuss the issue with an AI agent without leaving the context.

**Acceptance criteria:**
- "+" button opens a dropdown with available backends, each showing: name, provider, brand-colored dot, availability status
- Dropdown is searchable (for when there are many backends)
- Clicking a backend adds a new tab next to "Details"
- Tab shows the backend's brand color dot, name, and a close (X) button
- Terminal connects to a new tmux session running the selected backend CLI
- Multiple tabs can be open simultaneously (max 8)
- Switching tabs preserves terminal state (sessions stay alive in background)

**Open questions:**
- Should the terminal session be pre-seeded with issue context (e.g., `bd show {issueId}` auto-run)?
- What session naming convention? `{issueId}-claude-1` or user-chosen?
- Should unavailable backends (not installed) show as grayed out with "Configure in Settings" link?

---

### Story 2.3: Terminal tab shows worktree context
**As a** project lead using an embedded terminal,
**I want to** see which worktree/branch the terminal is running in and have quick git actions available,
**so that** I can manage the agent's work without switching tools.

**Acceptance criteria:**
- Terminal header shows: breadcrumb (`← / {issueId}`), backend label, branch/worktree path
- "merge" button: merges the worktree branch to main (calls `loom merge`)
- "Review Changes" button: shows the diff of uncommitted changes
- Git action buttons only appear when the terminal is running in a worktree context with changes
- Buttons are disabled with tooltips when no changes exist

**Open questions:**
- Should "Review Changes" open in a new tab (diff viewer), or a modal?
- Should these buttons also appear in the standalone Talk to Lead terminal?

---

### Story 2.4: Close a terminal tab
**As a** project lead done with a terminal session,
**I want to** close a tab and have the session cleaned up,
**so that** I don't accumulate stale sessions.

**Acceptance criteria:**
- Click X on a tab to close it
- If the session has a running process, show confirmation: "Close terminal? This will end the session."
- Closing the last terminal tab returns to just the "Details" tab
- The "Details" tab cannot be closed
- Closed sessions are terminated (tmux kill-session) after a grace period

**Open questions:**
- Should closed sessions be recoverable (e.g., "Recently closed" list)?
- Grace period before kill: 0s (immediate) or 30s (allow reconnect)?

---

### Story 2.5: Tabs persist when navigating away
**As a** project lead who opened terminal tabs on an issue,
**I want** my tabs to still be there when I navigate away and come back,
**so that** I don't lose my terminal sessions just because I checked another card.

**Acceptance criteria:**
- Navigating to kanban and back to the same issue restores all tabs
- Terminal sessions remain connected in the background
- Tab metadata (which tabs, which backends, order) persisted server-side (Redis tabmeta store)
- When reopening an issue, tabs show "reconnecting..." briefly before resuming

**Open questions:**
- How long do background sessions live? Until browser close? Until explicit close?
- Should there be a global "active sessions" indicator somewhere (e.g., badge on NavRail terminal icon)?

---

## Epic 3: Multi-Backend Talk to Lead

### Story 3.1: Open Talk to Lead with multiple AI backends
**As a** project lead,
**I want to** click "Talk to Lead" and get a terminal view with tabs for each configured backend (Claude, Codex, etc.),
**so that** I can interact with different AI tools from one place.

**Acceptance criteria:**
- Clicking the "Talk to Lead" FAB switches to the Terminal view (full main content area)
- Default tabs are auto-created for each available backend on first open
- Claude tab is active by default
- Each tab shows: backend brand color dot, name, close button
- Terminal connects to the backend's CLI in a tmux session
- The Terminal view is also accessible via the NavRail terminal icon (same view)

**Open questions:**
- Should Talk to Lead remember which tab was last active?
- If a backend becomes unavailable mid-session, what happens to its tab?

---

### Story 3.2: Add more terminal tabs
**As a** power user,
**I want to** click "+" in the terminal view to add additional tabs (e.g., a second Claude session, or a Browser tab),
**so that** I can run parallel conversations or tools.

**Acceptance criteria:**
- "+" button opens the same backend selector dropdown as the detail view
- Can have multiple tabs of the same backend (named `claude`, `claude-2`, etc.)
- "Browser" option opens a lightweight embedded browser (future — can be stubbed)
- Max 8 tabs enforced; "+" disabled with tooltip when at limit

**Open questions:**
- Should there be a "Default Terminal" option (plain shell, no AI backend)?
- Should "Browser" be included in v1 or deferred?

---

### Story 3.3: Terminal tabs persist across view switches
**As a** project lead who was chatting with Claude,
**I want** my terminal sessions to stay alive when I switch to the kanban view and back,
**so that** I don't lose conversation context.

**Acceptance criteria:**
- Switching views (kanban, table, terminal) does not kill terminal sessions
- Returning to Terminal view restores all tabs with their sessions intact
- Connection states update in real-time (tab dot changes color)
- Tab order and active tab preserved

---

### Story 3.4: Remove legacy Talk to Lead overlay
**As a** developer maintaining the codebase,
**I want** the old full-screen `TerminalPanel` overlay removed,
**so that** there's one terminal system, not two competing implementations.

**Acceptance criteria:**
- `TerminalPanel.tsx` and related CSS deleted
- `TalkToLeadButton` click handler changed to open TerminalView via NavRail
- The `terminal-backend-changed` CustomEvent pattern removed
- Settings backend dropdown still works but restarts the default Talk to Lead session
- No regression in terminal connectivity

---

## Cross-cutting Stories

### Story X.1: Simplified header with filter dropdowns
**As a** user,
**I want** the header bar to show Priority and Type filter dropdowns instead of view-switching tabs,
**so that** the UI is cleaner and view switching lives in one place (NavRail).

**Acceptance criteria:**
- Header shows: Logo, search bar, Priority dropdown ("All"), Type dropdown ("All")
- ViewSwitcher tabs removed from header
- GroupBy functionality either moves to a "..." more filters menu or a filter panel
- Filters apply to the current workspace's issues

---

### Story X.2: NavRail icon reorganization
**As a** user,
**I want** the NavRail to show Kanban, List, Terminal at the top and Settings at the bottom,
**so that** all primary views are one click away.

**Acceptance criteria:**
- Top icons: Kanban (grid), List (lines), Terminal (monitor)
- Bottom icon: Settings (gear)
- Observability accessible from Settings page or a sub-nav, not a primary NavRail slot
- Active view highlighted with brand color
- Tooltip on hover for each icon

---

### Story X.3: Backend availability check
**As a** user opening the "+" backend dropdown,
**I want to** see which backends are actually installed and available,
**so that** I don't try to open a Codex tab when Codex isn't configured.

**Acceptance criteria:**
- Available backends show with colored dot and full name
- Unavailable backends show grayed out with "Not configured" subtitle
- Clicking an unavailable backend shows "Configure in Settings" link
- Backend list comes from server (GET /api/backends), not hardcoded

---

## Summary Table

| Epic | Stories | Key Dependency |
|------|---------|---------------|
| 1. Workspace Selector | 1.1, 1.2, 1.3, 1.4 | Multi-repo backend (loomcli-3wsub, loomcli-y5vpk) |
| 2. Detail View + Tabs | 2.1, 2.2, 2.3, 2.4, 2.5 | Backend registry API (shared with Epic 3) |
| 3. Multi-Backend Talk to Lead | 3.1, 3.2, 3.3, 3.4 | Existing TerminalView infra (loomcli-jcr0g) |
| Cross-cutting | X.1, X.2, X.3 | — |

**Total: 14 user stories across 3 epics + 3 cross-cutting**

---

## Open Design Decisions (need user input)

1. **Quick peek vs full replace**: Should clicking a card always go to full detail view, or should there be a quick-peek option (hover/right-click)?
2. **Session pre-seeding**: Should terminal tabs auto-run `bd show {issueId}` to give the AI context?
3. **Background session lifetime**: How long do terminal sessions live when not visible?
4. **Browser tab**: Include in v1 or defer?
5. **Default terminal**: Should "+" offer a plain shell option alongside AI backends?
6. **Workspace creation UX**: UI form or config file only?
