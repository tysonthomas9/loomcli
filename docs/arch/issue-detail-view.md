# Issue Detail View Architecture (Epic fghge)

> **Status:** Current · *audited 2026-08-03*
>
> The 2026-08-03 pass corrected four things the 2026-07-23 banner had wrongly
> vouched for: the tab model (log tabs are auto-derived `task-log` tabs, not a
> `"logs"` tab), the agent state layer (a zustand store, not a React context),
> the panel's CSS positioning, and `AgentStatusBadge`, which is no longer
> mounted by anything. The 2026-07-23 pass rewrote every route (all issue APIs
> are now workspace-scoped), the deep-link URL, the Redis key names, and the
> file map (components and hooks moved into subdirectories). The terminal rows
> changed most — see [terminal-system.md](terminal-system.md) for why spawn/kill
> no longer exist.
>
> **Not re-verified on 2026-08-03:** the remainder of §2's hierarchy, §3's tab
> lifecycle, and §4's session-start flow. Spot checks found more drift there —
> `addTab` no longer exists anywhere in the frontend, and the tab bar renders no
> `+` adder (`IssueDetailPanel.tsx:1294-1349`) — so treat those sections as
> stale until someone audits them properly.

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

The panel renders as `position: absolute` anchored right (`IssueDetailPanel.module.css:28-32`) inside a `position: fixed` overlay that is inset by the unified header height and nav-rail width (`:6-11`) — which is why the panel is absolute rather than fixed. Width is `min(100%, 840px)` — `IssueDetailPanel.module.css:33-34` against `--panel-width-max: 840px` (`styles/variables.css:218`). Focus management via `useFocusReturn`, `useFocusTrap`, and `useRegisterEscapeLayer` (all in `hooks/ui/`).

### IssueDetailView (Full-page)

**Trigger:** route navigation to `/ws/:workspaceId/issues/:issueId` (`router.tsx:154-155`, mounted under the `/ws/:workspaceId` layout).

URL: `/ws/:workspaceId/issues/:issueId` — plural `issues`.

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
      |   +-- StartWorkButton (agent picker popover)
      |   +-- EditableDescription
      |   +-- DesignPanel (collapsible H2 sections, fullscreen mode)
      |   +-- Notes (CollapsibleSection)
      |   +-- DependencySection (editable, navigable chips)
      |   +-- EpicRollup (epics only: progress + child tickets)
      |   +-- PRSection (linked pull requests)
      |   +-- SessionHistorySection
      |   +-- ActivityLog (unified comment+event timeline)
      |   +-- CommentForm
      |   +-- LabelEditor
      +-- [activeTab === "sessions"] SessionsTab
      |   +-- SessionTimeline (sorted newest-first, adaptive polling)
      |   |   +-- SessionTimelineRow × N (status dot, duration, tokens, cost)
      |   +-- SessionDetailView (transcript + diff)
      |       +-- metadata summary (model, exit code, files, lines)
      |       +-- inner tab bar [Transcript | Diff]
      |       +-- transcript pane (role-labeled entries)
      |       +-- CodeMirrorEditor (language="diff", readOnly)
      +-- [activeTab === "task-log-{phase}"] TaskPhaseLogPanel (one per phase)
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
  type: "details" | "terminal" | "sessions" | "task-log";
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

The union is declared at `IssueDetailPanel.tsx:257-264`.

- Details tab: always `"details"`, permanent (`closable: false`)
- Sessions tab: `"sessions"`, permanent (`closable: false`)
- Task-log tabs: `"task-log"`, id `task-log-{phase}`, permanent
  (`closable: false`). There is no `"logs"` tab type and no `LogViewer`
  component. Log tabs are not user-added: `getTaskLogPhases(workspaceId,
  issue.id)` is fetched for `task`-type issues and filtered to `planning` /
  `implementation` (`IssueDetailPanel.tsx:506-523`), and the `visibleTabs` memo
  derives one tab per returned phase and splices them in after Details
  (`:717-731`). Both can therefore be present at once. They are stripped before
  persistence (`:695`) and rendered by `TaskPhaseLogPanel`
  (`:390-424`, mounted at `:1522-1528`), which polls the task log every 500ms.
- Terminal tabs: `"terminal-{sessionName}"` — one per unique session

### Tab Lifecycle

**Adding:** `addTab("terminal", { sessionName, backend, ... })` generates ID, appends, activates.

**Closing connected terminal:**
1. `handleTabClose(tabId)` checks `connectionState === "connected"`
2. If connected: shows `ConfirmDialog`
3. On confirm: `closeTab(tabId)` → `deleteTabMetadata(workspaceId, sessionName)` only. There is no server-side kill call — the PTY dies with its WebSocket (`IssueDetailPanel.tsx:541-543`).

### Tab Persistence (Redis)

Tab state persisted per-issue via `useIssueTabPersistence` (`hooks/issues/`):

- **Write:** debounced 300ms → `PUT /api/workspaces/{ws}/issues/{issueId}/tabs` (`internal/webui/handlers/issues/tab_module.go:33`) → Redis key `ws:{workspaceID}:issue:tabs:{issueId}`, 24h TTL (`internal/webui/issuetabs/store.go:1-5`)
- **Read:** `GET /api/workspaces/{ws}/issues/{issueId}/tabs` (`tab_module.go:32`) → server-side `ValidateAndFilter` removes terminal tabs whose sessions no longer exist
- **SSE invalidation:** `mutation.type === "issue_tabs"` triggers refetch (debounced 100ms)

---

## 4. Terminal Integration

### EmbeddedTerminal Component

`forwardRef` wrapper around `TerminalInstance` (xterm.js). Adds `TerminalHeader` — backend brand dot, connection dot, worktree breadcrumb, maximize button, git action buttons. Clipboard handling is native (xterm.js + browser); the `useClipboard` / `CopyToast` / `PasteConfirmDialog` / `TerminalContextMenu` layer no longer exists anywhere in the frontend.

### Session Start Flow

There is no spawn endpoint. A session is created implicitly by connecting:

```
User clicks "New Terminal" or agent badge
  -> addTab("terminal", { sessionName, backend, ... })
  -> EmbeddedTerminal renders
  -> TerminalInstance fetches a one-time token
         GET /api/workspaces/{ws}/terminal/token
  -> connects                                     (internal/webui/handlers/terminal/module.go:93)
         GET /api/workspaces/{ws}/terminal/ws     (module.go:96)
  -> server attaches/creates the PTY; command derived from the session name
  -> onConnectionStateChange("connected")
  -> tab connection dot turns green
```

### Connection State Indicators

`TerminalInstance` emits `ConnectionState` — `"disconnected" | "connecting" | "connected" | "error" | "crashed" | "session_ended"` (`components/TerminalView/instances/TerminalInstance.tsx:67-76`):
1. `EmbeddedTerminal` stores local state for `TerminalHeader` display
2. Bubbles up to `DefaultContent` → updates `tabs[i].connectionState` → tab bar renders colored dot

### Session History

`SessionHistorySection` lists past sessions via `GET /api/workspaces/{ws}/issues/{issueId}/sessions` (`internal/webui/handlers/issues/session_module.go:51`). Active sessions show "Jump to tab", completed sessions show "View scrollback" (`.../sessions/{recordId}/scrollback`, `session_module.go:52`).

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

Split ratio managed by `useSplitRatio` (`hooks/ui/useSplitRatio.ts`), persisted to `localStorage["cortex:detail-panel-split-ratio"]` (`:8`). Bounds 15%–85%, default 50%, maximize 5% (`:9-12`). (`cortex` here is a dead UI codename that survives only as a storage-key prefix — see `docs/loom-glossary.md`.)

---

## 6. Inline Editing

All inline edits follow the optimistic update pattern:

```
setOptimisticValue(newValue)
try { await onSave(newValue) }
catch { setOptimisticValue(previousValue); setError(msg) }
```

Every field patches the same route, `PATCH /api/workspaces/{ws}/issues/{id}`
(`internal/webui/handlers/issues/module.go:45`); only the body differs.

| Field | Component | Patch body |
|-------|-----------|------------|
| Title | `EditableTitle` | `title` |
| Status | `StatusDropdown` | `status` |
| Priority | `fields/PriorityDropdown` | `priority` |
| Type | `fields/TypeDropdown` | `issue_type` |
| Assignee | `fields/AssigneeDropdown` | `assignee` |
| Owner | `fields/OwnerDropdown` | `owner` |
| Repo | `fields/RepoDropdown` | `repo:` label |
| Description | `sections/EditableDescription` | `description` |
| Labels | `fields/LabelEditor` | `add_labels` / `remove_labels` |

All updates call `onIssueUpdate(updatedIssue)` → `App.updateIssueDetails()` to merge without re-fetching.

---

## 7. Agent Integration

### AgentStatusBadge (not mounted)

The component still exists (`header/AgentStatusBadge.tsx`) and is re-exported
from the `header/` barrel (`header/index.ts:8-9`), but no application component
renders it: every JSX usage in the tree is in its own test file, and
`IssueDetailPanel.tsx:70` imports only `IssueHeader` from `./header`. What it
would do if mounted:

- Reads `agents` off the shared agent store (`useAgentStoreInstance()` +
  `useStore`, `AgentStatusBadge.tsx:41-42`) and resolves the row with the
  module helper `resolveAgentByName(agents, agentName)` (`:48`)
- Polls `fetchGitStatus(workspaceId, agentName)` every 30s
  (`PR_POLL_INTERVAL`, `:30,64,74`) — for PR-branch link detection, not status
- Clicking fires the optional `onOpenTerminal(agentName)` callback (`:26,83,90`);
  no caller supplies it outside tests, so the click is a no-op

### StartWorkButton

Visible for `open` issues with no assignee. Lists agents from its `agents` prop (`actions/StartWorkButton.tsx:27`), categorized as available/warning/busy. It prefers agents matching `preferredRole` (`"task" | "plan"`, defaulting to `"task"` — `actions/StartWorkButton.tsx:34-35`) but **falls back to the full roster when no agent matches**, so a plan-stage issue in a task-only workspace still gets a picker (`StartWorkButton.tsx:130-133`). Selecting calls `updateIssue(id, { assignee, status: "in_progress" })`.

### AssigneeDropdown

Includes agent list with reassignment confirmation dialog. Human assignments stored with `[H] ` prefix. Recent assignees persisted via `useRecentAssignees()`.

---

## 8. Navigation and Deep Links

### Back Navigation

From `IssueDetailView`: `window.history.back()` returns to the previous route. Scroll position is restored from `scrollPositionCache` via `requestAnimationFrame`.

### Deep-link URLs

`/ws/:workspaceId/issues/:issueId` is built by `buildShareUrl()` (`utils/buildShareUrl.ts:16,23`). On page load, route params provide the issue id and the view fetches the issue.

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

## 11. Server routes and APIs

Every route below is workspace-scoped. There are no unscoped `/api/issues/*`
or `/api/terminal/*` routes.

### Issue data — `internal/webui/handlers/issues/module.go`

| Method | Path (under `/api/workspaces/{ws}`) | Registered at |
|--------|------|---------------|
| GET | `/issues/{id}` | `module.go:42` |
| GET | `/issues` | `module.go:43` |
| PATCH | `/issues/{id}` | `module.go:45` |
| POST | `/issues/{id}/close` · `/reopen` · `/claim` · `/move` | `module.go:46-49` |
| DELETE | `/issues/{id}` | `module.go:50` |
| GET/POST | `/issues/{id}/comments` | `module.go:53-54` |
| GET | `/issues/{id}/events` | `module.go:57` |
| GET/POST | `/issues/{id}/dependencies` | `module.go:60-61` |
| DELETE | `/issues/{id}/dependencies/{depId}` | `module.go:62` |

### Tab persistence — `internal/webui/handlers/issues/tab_module.go`

| Method | Path | Registered at |
|--------|------|---------------|
| GET | `/api/workspaces/{ws}/issues/{issueId}/tabs` | `tab_module.go:32` |
| PUT | `/api/workspaces/{ws}/issues/{issueId}/tabs` | `tab_module.go:33` |
| DELETE | `/api/workspaces/{ws}/issues/{issueId}/tabs` | `tab_module.go:34` |

### Session history — `internal/webui/handlers/issues/session_module.go`

| Method | Path | Registered at |
|--------|------|---------------|
| GET | `/api/workspaces/{ws}/issues/{issueId}/sessions` | `session_module.go:51` |
| GET | `/api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback` | `session_module.go:52` |
| GET | `/api/workspaces/{ws}/tasks/{taskId}/sessions[/{sessionId}[/transcript\|/diff]]` | `session_module.go:56-65` |

### Terminal — `internal/webui/handlers/terminal/`

| Method | Path | Registered at |
|--------|------|---------------|
| GET | `/api/workspaces/{ws}/terminal/token` | `module.go:93` |
| GET | `/api/workspaces/{ws}/terminal/ws` | `module.go:96` |
| DELETE | `/api/workspaces/{ws}/terminal/tabs/{session}` | `tab_module.go:31` |

`POST /api/terminal/spawn` and `POST /api/terminal/sessions/{s}/kill` were
deleted with the tmux removal; `spawnTerminalSession` and `scheduleSessionKill`
have no server route behind them. See
[terminal-system.md](terminal-system.md) §12.

### Backend registry

| Method | Path | Registered at |
|--------|------|---------------|
| GET | `/api/backends` | `internal/webui/app/routes.go:54` |

---

## 12. State Management

- **Agent store:** `StoreProvider` (`hooks/common/useStoreContext.tsx:207`) creates one zustand `agentStore` (`createAgentStore()`, `stores/agentStore.ts:196`) and drives a single polling loop at `AGENT_REFRESH_INTERVAL_MS = 30_000` (`useStoreContext.tsx:79,184`), shared by all consumers via `useAgentStoreInstance()` + `useStore(store, selector)`. There is no `AgentProvider` / `useAgentContext`; `getAgentByName` is a store action (`agentStore.ts:120,471`), not a context method.
- **Panel exclusivity:** `usePanelManager` manages single `activePanel` with 300ms transition
- **Issue loading:** `useIssueDetail` uses request counter to prevent race conditions
- **Optimistic updates:** immediate local state change, rollback on error
- **Escape layering:** `useRegisterEscapeLayer` with priorities — dropdowns close before panel

---

## 13. File Map

### Frontend components (`internal/webui/frontend/src/`)

| Path | Description |
|------|-------------|
| `components/IssueDetailPanel/IssueDetailPanel.tsx` | Main panel + DefaultContent |
| `components/IssueDetailPanel/SplitDetailSummary.tsx` | Condensed detail for split view |
| `components/IssueDetailPanel/CollapsibleSection.tsx` | Shared collapsible wrapper |
| `components/IssueDetailPanel/PRFilesTab.tsx` | Changed-files tab for a linked PR |
| `components/IssueDetailPanel/PRCompareDiffPane.tsx` | PR compare diff pane |
| `components/IssueDetailPanel/header/IssueHeader.tsx` | Sticky header: status, title, PR links |
| `components/IssueDetailPanel/header/AgentStatusBadge.tsx` | Real-time agent status pill — **not mounted anywhere**, see §7 |
| `components/IssueDetailPanel/fields/AssigneeDropdown.tsx` | Agent/human assignment |
| `components/IssueDetailPanel/fields/OwnerDropdown.tsx` | Owner assignment |
| `components/IssueDetailPanel/fields/PriorityDropdown.tsx` | Priority picker |
| `components/IssueDetailPanel/fields/TypeDropdown.tsx` | Issue-type picker |
| `components/IssueDetailPanel/fields/RepoDropdown.tsx` | Repo label picker |
| `components/IssueDetailPanel/fields/LabelEditor.tsx` | Label add/remove |
| `components/IssueDetailPanel/actions/StartWorkButton.tsx` | Agent picker for "Start Work" |
| `components/IssueDetailPanel/actions/ResizeDivider.tsx` | Draggable split divider |
| `components/IssueDetailPanel/actions/MoveIssueDialog.tsx` | Move issue between workspaces/repos |
| `components/IssueDetailPanel/sections/DesignPanel.tsx` | Collapsible markdown with fullscreen |
| `components/IssueDetailPanel/sections/EditableDescription.tsx` | Inline description editor |
| `components/IssueDetailPanel/sections/ActivityLog.tsx` | Comment + event timeline |
| `components/IssueDetailPanel/sections/CommentsSection.tsx` / `CommentForm.tsx` / `RejectCommentForm.tsx` | Comment surfaces |
| `components/IssueDetailPanel/sections/DependencySection.tsx` / `DependencySearchPicker.tsx` | Editable dependencies |
| `components/IssueDetailPanel/sections/EpicRollup.tsx` | Epic progress roll-up + child tickets |
| `components/IssueDetailPanel/sections/PRSection.tsx` | Linked pull requests |
| `components/IssueDetailPanel/sections/MarkdownRenderer.tsx` / `HtmlDesignRenderer.tsx` | Sanitized renderers |
| `components/IssueDetailPanel/sessions/SessionsTab.tsx` | Agent session audit trail container |
| `components/IssueDetailPanel/sessions/SessionTimeline.tsx` / `SessionTimelineRow.tsx` | Session list, newest first |
| `components/IssueDetailPanel/sessions/SessionDetailView.tsx` | Transcript + diff viewer |
| `components/IssueDetailPanel/sessions/SessionHistorySection.tsx` | Past terminal sessions with scrollback |
| `components/IssueDetailPanel/sessions/TaskSessionDiffPane.tsx` | Diff pane for one task session |
| `components/IssueDetailView/IssueDetailView.tsx` | Full-page detail view |
| `components/EmbeddedTerminal/EmbeddedTerminal.tsx` | Terminal wrapper for panel tabs |
| `components/EmbeddedTerminal/TerminalHeader.tsx` | Terminal tab header |
| `components/CodeMirrorEditor/CodeMirrorEditor.tsx` | CodeMirror 6 wrapper (diff syntax) |
| `components/BackendSelectorDropdown/BackendSelectorDropdown.tsx` | Backend picker |

PR surfaces in this panel are specified by `docs/product/pr-review-spec.md`;
this doc does not restate them.

### Frontend hooks

| Path | Description |
|------|-------------|
| `hooks/issues/useIssueDetail.ts` | Fetch/cache issue details, race-safe |
| `hooks/issues/useIssueTabPersistence.ts` | Redis tab state load/save |
| `hooks/issues/useRecentAssignees.ts` | Recently used assignees |
| `hooks/ui/usePanelManager.ts` | Panel mutual exclusivity |
| `hooks/ui/useSplitRatio.ts` | localStorage split ratio |
| `hooks/ui/useFocusReturn.ts` / `useFocusTrap.ts` | Focus management |
| `hooks/terminal/useTaskSessions.ts` | Adaptive polling for session list |
| `hooks/terminal/useSessionTranscript.ts` | Transcript polling |
| `hooks/terminal/useSessionDiff.ts` | Lazy one-shot diff fetch |

### Server

| Path | Description |
|------|-------------|
| `internal/webui/handlers/issues/module.go` | Issue CRUD, comments, events, dependencies |
| `internal/webui/handlers/issues/tab_module.go` | Issue tab state routes |
| `internal/webui/handlers/issues/session_module.go` | Session history and task-session routes |
| `internal/webui/handlers/terminal/module.go` | Terminal token + WebSocket |
| `internal/webui/handlers/terminal/tab_module.go` | Terminal tab metadata routes |
| `internal/webui/issuetabs/store.go` | Redis store for issue tab state |
| `internal/webui/sessionhistory/store.go` | Redis store for session history |

---

## Related

- [terminal-system.md](terminal-system.md) — the terminal subsystem this panel
  embeds, including the PTY lifecycle and the removed session endpoints.
- [file-explorer.md](file-explorer.md) — the third web-UI subsystem map.
- `docs/product/pr-review-spec.md` — the PR surfaces referenced above.
