# Issue Detail View Architecture (Epic fghge)

## Overview

The Issue Detail View subsystem renders full issue details in two modes: a slide-out panel overlay for quick inspection from list/kanban/graph views, and a full-page replacement view for deeper work. Both modes share the same underlying data hooks and many of the same sub-components.

---

## 1. Two Entry Points: Panel vs Full View

### IssueDetailPanel (Slide-out Overlay)

**Trigger:** clicking any issue card in kanban, table, graph, or monitor views.

```
handleIssueClick(issue)
  -> openPanel({ type: "issue", id: issue.id })   // usePanelManager
  -> fetchIssue(issue.id)                          // useIssueDetail
```

`usePanelManager` enforces mutual exclusivity between issue panels and agent panels:
- Same-panel click = no-op
- Same type, different ID = immediate content swap (no animation)
- Different types = 300ms close animation, then open

The panel renders as `position: fixed` anchored right, width `min(100%, 840px)` (`--panel-width-max`). Focus management via `useFocusReturn`, `useFocusTrap`, and `useRegisterEscapeLayer`.

### IssueDetailView (Full-page)

**Trigger:** `navigateToView("issue-detail", { issueId, previousView })` via `useViewState`.

URL: `?view=issue-detail&issue={id}`

A lighter read-mostly component without the tabbed interface, embedded terminal, session history, or editable fields for owner/assignee. Includes "Open in Terminal" action that switches to Terminal view with issue context seeded via `pendingIssueContext`.

---

## 2. Component Hierarchy

```
IssueDetailPanel (slide-out overlay)
  +-- DefaultContent
      +-- IssueHeader (ID, status dropdown, editable title, PR links)
      +-- MetadataBar (owner, assignee dropdowns)
      +-- ReviewActionBar (approve/reject for review items)
      +-- BlockingBanner (shown when status=blocked with open deps)
      +-- TabBar (dynamic tab strip with "+" adder)
      +-- [activeTab === "details"] scrollable content
      |   +-- PriorityDropdown, TypeDropdown, AssigneeDropdown, RepoDropdown
      |   +-- AgentStatusBadge (clickable, opens logs tab)
      |   +-- StartWorkButton (agent picker popover)
      |   +-- EditableDescription
      |   +-- DesignPanel (collapsible H2 sections, fullscreen mode)
      |   +-- Notes (CollapsibleSection)
      |   +-- DependencySection (editable, navigable chips)
      |   +-- SessionHistorySection
      |   +-- ActivityLog (unified comment+event timeline)
      |   +-- CommentForm
      |   +-- LabelEditor
      +-- [activeTab === "logs"] LogViewer
      +-- [activeTab === "terminal-*"] split view
          +-- SplitDetailSummary (condensed detail in top pane)
          +-- ResizeDivider (draggable, keyboard-accessible)
          +-- EmbeddedTerminal
              +-- TerminalHeader (backend label, connection dot, git actions)
              +-- TerminalInstance (xterm.js WebSocket terminal)

IssueDetailView (full-page)
  +-- headerBar (back button, ID, status, title, "Open in Terminal", copy link)
  +-- metadataBar (read-only owner/assignee/created/priority)
  +-- reviewActionBar
  +-- scrollable contentArea (description, design, notes, deps, comments, labels)
```

---

## 3. Tabbed Interface

### Tab Model

```typescript
interface DetailTab {
  id: string;
  type: "details" | "logs" | "terminal";
  label: string;
  closable: boolean;
  metadata?: {
    sessionName?: string;
    backend?: string;
    agentName?: string | null;
    worktreePath?: string;
  };
  connectionState?: ConnectionState;
}
```

- Details tab: always `"details"`, permanent (`closable: false`)
- Logs tab: `"logs"` (only one allowed)
- Terminal tabs: `"terminal-{sessionName}"` — one per unique session

### Tab Lifecycle

**Adding:** `addTab("terminal", { sessionName, backend, ... })` generates ID, appends, activates.

**Closing connected terminal:**
1. `handleTabClose(tabId)` checks `connectionState === "connected"`
2. If connected: shows `ConfirmDialog`
3. On confirm: `closeTab(tabId)` → `deleteTabMetadata(sessionName)` + `scheduleSessionKill(sessionName)`

### Tab Persistence (Redis)

Tab state persisted per-issue via `useIssueTabPersistence`:

- **Write:** debounced 300ms → `PUT /api/issues/{id}/tabs` → Redis key `issue:tabs:{issueId}` (24h TTL)
- **Read:** `GET /api/issues/{id}/tabs` → server-side `ValidateAndFilter` removes terminal tabs whose sessions no longer exist
- **SSE invalidation:** `mutation.type === "issue_tabs"` triggers refetch (debounced 100ms)

---

## 4. Terminal Integration

### EmbeddedTerminal Component

`forwardRef` wrapper around `TerminalInstance` (xterm.js). Adds:
- `TerminalHeader` — backend brand dot, connection dot, worktree breadcrumb, maximize button, git action buttons
- Clipboard UX — `useClipboard`, `CopyToast`, `PasteConfirmDialog`, `TerminalContextMenu`

### Session Spawn Flow

```
User clicks "New Terminal" or agent badge
  -> spawnTerminalSession(sessionName, backend)    POST /api/terminal/spawn
  -> addTab("terminal", { sessionName, ... })
  -> EmbeddedTerminal renders
  -> TerminalInstance connects via WebSocket        GET /api/terminal/ws
  -> onConnectionStateChange("connected")
  -> tab connection dot turns green
```

### Connection State Indicators

`TerminalInstance` emits `ConnectionState` ("connected" | "disconnected" | "reconnecting"):
1. `EmbeddedTerminal` stores local state for `TerminalHeader` display
2. Bubbles up to `DefaultContent` → updates `tabs[i].connectionState` → tab bar renders colored dot

### Session History

`SessionHistorySection` lists past sessions via `GET /api/issues/{id}/sessions`. Active sessions show "Jump to tab", completed sessions show "View scrollback" (fetches from `/api/issues/{id}/sessions/{recordId}/scrollback`).

---

## 5. Split View (Terminal in Panel)

When `activeTabId` matches `"terminal-*"`, the panel renders a vertical split:

```
splitContainer (flex column)
  +-- splitTop ({ratio*100}%)
  |   +-- SplitDetailSummary (condensed metadata + description + design)
  +-- ResizeDivider (pointer events drag, double-click resets to 50%)
  +-- splitBottom (flex 1)
      +-- EmbeddedTerminal
```

Split ratio managed by `useSplitRatio` hook, persisted to `localStorage["cortex:detail-panel-split-ratio"]`. Bounds: 15%–85%, default 50%. Maximize drops ratio to 5%.

---

## 6. Inline Editing

All inline edits follow the optimistic update pattern:

```
setOptimisticValue(newValue)
try { await onSave(newValue) }
catch { setOptimisticValue(previousValue); setError(msg) }
```

| Field | Component | API |
|-------|-----------|-----|
| Title | `EditableTitle` | `PATCH /api/issues/{id}` with `title` |
| Status | `StatusDropdown` | `PATCH /api/issues/{id}` with `status` |
| Priority | `PriorityDropdown` | `PATCH /api/issues/{id}` with `priority` |
| Type | `TypeDropdown` | `PATCH /api/issues/{id}` with `issue_type` |
| Assignee | `AssigneeDropdown` | `PATCH /api/issues/{id}` with `assignee` |
| Owner | `OwnerDropdown` | `PATCH /api/issues/{id}` with `owner` |
| Repo | `RepoDropdown` | `PATCH /api/issues/{id}` with `repo:` label |
| Description | `EditableDescription` | `PATCH /api/issues/{id}` with `description` |
| Labels | `LabelEditor` | `PATCH /api/issues/{id}` with `add_labels`/`remove_labels` |

All updates call `onIssueUpdate(updatedIssue)` → `App.updateIssueDetails()` to merge without re-fetching.

---

## 7. Agent Integration

### AgentStatusBadge

Shows real-time agent status for non-human assignees:
- Uses `useAgentContext().getAgentByName()` (shared 5s polling)
- Polls `fetchGitStatus(agentName)` every 30s independently
- Clicking opens the "logs" tab

### StartWorkButton

Visible for `open` issues with no assignee. Lists agents from `useAgentContext()`, categorized as available/warning/busy. Only shows `role === "task"` agents. Selecting calls `updateIssue(id, { assignee, status: "in_progress" })`.

### AssigneeDropdown

Includes agent list with reassignment confirmation dialog. Human assignments stored with `[H] ` prefix. Recent assignees persisted via `useRecentAssignees()`.

---

## 8. Navigation and Deep Links

### Back Navigation

From `IssueDetailView`: `window.history.back()` → `popstate` fires → `useViewState.onPopState` restores `previousView`. Scroll position restored from `scrollPositionCache` via `requestAnimationFrame`.

### Deep-link URLs

`?view=issue-detail&issue={issueId}` — built by `buildShareUrl()`. On page load, `useViewState` reads the URL and `App.tsx` fetches the issue.

### Dependency Navigation

Dependency chips are interactive when `onNavigateToIssue` is provided. Clicking routes through `handleIssueClick()` — opens panel from list views, navigates within `issue-detail` from full-page mode.

---

## 9. DesignPanel

Renders markdown design content with:
- **Section splitting:** markdown split on `## ` headings
- **Collapsible sections:** each H2 independently expandable
- **Fullscreen mode:** body scroll locked, Escape exits (capture phase)
- **XSS safety:** via `MarkdownRenderer` sanitization

---

## 10. Responsive Layout

| Breakpoint | Behavior |
|------------|----------|
| `> 1024px` | Two-column: description left, DesignPanel right |
| `<= 1024px` | Single column: design stacks below description |
| `<= 768px` | Panel full screen width, tab bar relative position |
| `<= 520px` | Reduced content padding |

---

## 11. Backend APIs

### Issue Data

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/issues/{id}` | Full IssueDetails (comments, deps, dependents) |
| PATCH | `/api/issues/{id}` | Update fields |
| POST | `/api/issues/{id}/comments` | Add comment |
| GET | `/api/issues/{id}/events` | Event log |
| POST | `/api/issues/{id}/dependencies` | Add dependency |
| DELETE | `/api/issues/{id}/dependencies/{depId}` | Remove dependency |

### Tab Persistence

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/issues/{id}/tabs` | Fetch persisted tab state from Redis |
| PUT | `/api/issues/{id}/tabs` | Save tab state (24h TTL) |
| DELETE | `/api/issues/{id}/tabs` | Delete tab state |

### Terminal

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/terminal/spawn` | Create tmux session |
| GET | `/api/terminal/ws` | WebSocket upgrade for xterm.js |
| DELETE | `/api/terminal/tabs/{session}` | Remove tab metadata |
| POST | `/api/terminal/sessions/{session}/kill` | Deferred session kill |
| GET | `/api/issues/{id}/sessions` | Session history records |

### Backend Registry

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/backends` | List backends with health status |

---

## 12. State Management

- **Agent context:** `AgentProvider` single 5s polling loop shared by all consumers
- **Panel exclusivity:** `usePanelManager` manages single `activePanel` with 300ms transition
- **Issue loading:** `useIssueDetail` uses request counter to prevent race conditions
- **Optimistic updates:** immediate local state change, rollback on error
- **Escape layering:** `useRegisterEscapeLayer` with priorities — dropdowns close before panel

---

## 13. File Map

### Frontend Components

| Path | Description |
|------|-------------|
| `components/IssueDetailPanel/IssueDetailPanel.tsx` | Main panel + DefaultContent |
| `components/IssueDetailPanel/IssueHeader.tsx` | Sticky header with status, title, PR links |
| `components/IssueDetailPanel/DesignPanel.tsx` | Collapsible markdown with fullscreen |
| `components/IssueDetailPanel/ActivityLog.tsx` | Comment + event timeline |
| `components/IssueDetailPanel/AgentStatusBadge.tsx` | Real-time agent status pill |
| `components/IssueDetailPanel/AssigneeDropdown.tsx` | Agent/human assignment |
| `components/IssueDetailPanel/StartWorkButton.tsx` | Agent picker for "Start Work" |
| `components/IssueDetailPanel/SessionHistorySection.tsx` | Past sessions with scrollback |
| `components/IssueDetailPanel/SplitDetailSummary.tsx` | Condensed detail for split view |
| `components/IssueDetailPanel/ResizeDivider.tsx` | Draggable split divider |
| `components/IssueDetailPanel/DependencySection.tsx` | Editable dependencies |
| `components/IssueDetailView/IssueDetailView.tsx` | Full-page detail view |
| `components/EmbeddedTerminal/EmbeddedTerminal.tsx` | Terminal wrapper for panel tabs |
| `components/EmbeddedTerminal/TerminalHeader.tsx` | Terminal tab header |
| `components/BackendSelectorDropdown/BackendSelectorDropdown.tsx` | Backend picker |
| `components/BackendSelectorDropdown/backendDefaults.ts` | Brand metadata for known backends |

### Frontend Hooks

| Path | Description |
|------|-------------|
| `hooks/useIssueDetail.ts` | Fetch/cache issue details, race-safe |
| `hooks/usePanelManager.ts` | Panel mutual exclusivity |
| `hooks/useIssueTabPersistence.ts` | Redis tab state load/save |
| `hooks/useSplitRatio.ts` | localStorage split ratio |

### Backend

| Path | Description |
|------|-------------|
| `internal/webui/handlers_terminal_spawn.go` | POST /api/terminal/spawn |
| `internal/webui/issuetabs/store.go` | Redis store for tab state |
| `internal/webui/sessionhistory/store.go` | Redis store for session history |
