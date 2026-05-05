# Terminal System Architecture (Epic uffak)

## Overview

The Terminal System provides an integrated terminal experience within the web UI, supporting multiple AI backend sessions (Claude, Codex, OpenCode) and plain shell tabs. It spans from the NavRail entry point through a tabbed terminal view with WebSocket-relayed PTY connections to tmux sessions on the backend.

The system supports workspace-scoped tab isolation, drag-and-drop tab reordering, split-pane viewing, slash commands, crash recovery, session scrollback persistence, and issue-linked terminal sessions.

---

## 1. Component Hierarchy

```
NavRail (NavRail.tsx)
  +-- "terminal" view button (position 3, shows active-session badge)

TerminalView (TerminalView.tsx)            <- orchestrator
  +-- useWorkspaceTabState                 <- workspace-scoped tab isolation
  +-- useTerminalMetadata                  <- Redis-backed tab persistence
  +-- useSessionRestore                    <- active-tab restore on refresh
  +-- useTabInit                           <- cold-start tab creation
  +-- useSplitView                         <- split-pane state
  +-- useClipboard                         <- copy/paste with confirmation
  +-- useTabActions                        <- close/duplicate/rename
  +-- useTabOrdering                       <- pin/reorder/close-others
  +-- useSessionManagement                 <- issue-linked tab creation + close-all
  |
  +-- TerminalTabBar (TerminalTabBar.tsx)   <- WAI-ARIA tablist
  |    +-- SortableTab (dnd-kit)           <- drag-and-drop per tab
  |    +-- TabContextMenu                  <- right-click: duplicate/rename/pin/close
  |
  +-- [per tab] TerminalInstance           <- single wterm + WebSocket pane
  |    +-- wterm DOM renderer
  |    +-- Native DOM selection/copy/paste
  |    +-- Auto-resize via wterm
  |
  +-- [per tab overlays]
  |    +-- TerminalConnectionOverlay       <- connecting/disconnected/error states
  |    +-- ReconnectingOverlay             <- exponential backoff countdown
  |    +-- CrashOverlay                    <- backend exited (ws close 4001)
  |    +-- WelcomeBanner                   <- first-time onboarding per backend
  |    +-- NotesBar                        <- collapsible per-tab notes
  |
  +-- SearchBar                            <- Ctrl+F overlay (VS Code style)
  +-- BackendPickerPrompt                  <- modal for "+" new tab
  +-- PasteConfirmDialog                   <- multi-line paste confirmation
  +-- CopyToast                            <- 1.5s copy notification
  +-- TerminalContextMenu                  <- right-click: copy/paste/select-all
  +-- HelpPopover                          <- keyboard shortcut reference
  +-- SplitDivider                         <- draggable resize handle
  +-- SplitPaneSelector                    <- right-pane tab picker

EmbeddedTerminal (EmbeddedTerminal.tsx)    <- reuses TerminalInstance in issue panels
  +-- TerminalHeader                       <- backend info + git actions
```

---

## 2. Terminal Tab Management

### Tab Model

```typescript
interface TabState {
  id: string;           // == sessionName
  label: string;        // display label, e.g. "lead-claude-1"
  sessionName: string;  // tmux session name, e.g. "myws--lead-claude-1"
  connectionState: ConnectionState;
  backendName: string;
  pinned?: boolean;
  crashReason?: string | null;
}
```

### TerminalTabBar

`TerminalTabBar` implements the WAI-ARIA `tablist` pattern. Each tab is a `SortableTab` (dnd-kit `useSortable`) wrapping a `div[role="tab"]`.

**Tab Reordering and Pinning:** Drag-and-drop uses `@dnd-kit/core` with `SortableContext` and `horizontalListSortingStrategy`. Collision detection is zone-restricted: pinned tabs can only be reordered among pinned tabs; unpinned tabs among unpinned tabs. The drop handler calls `onReorderTabs` which triggers `reorderTabMeta` (parallel `PATCH sort_order` calls to Redis). Pinned tabs always sort before unpinned tabs (sorted by `Pinned DESC, SortOrder ASC`).

**Tab Overflow:** `ResizeObserver` on the tab list container detects overflow. Scroll arrows appear at boundaries, clicking scrolls by 150px with `{ behavior: 'smooth' }`. New tabs auto-scroll the list to the right.

### Keyboard Shortcuts

Handled at the `TerminalView` level via `document.addEventListener('keydown')`:

| Shortcut | Action |
|----------|--------|
| Ctrl+Tab / Alt+ArrowRight | Next tab |
| Ctrl+Shift+Tab / Alt+ArrowLeft | Previous tab |
| Ctrl+1..9 / Cmd+1..9 | Switch to tab by index |
| Ctrl+T / Cmd+T | Open BackendPickerPrompt |
| Ctrl+W / Cmd+W | Close active tab |
| Ctrl+F / Cmd+F | Toggle search |
| Escape | Exit search / return to previous view |

Within the tab list, WAI-ARIA keyboard navigation (ArrowLeft/ArrowRight/Home/End/Delete) is handled by `handleKeyDown` inside `TerminalTabBar`.

### Backend Brand Color Dots

Each tab renders a `span.statusDot` with `data-status={connectionState}`. If the tab has a `brandColor`, it is set as the CSS custom property `--brand-color`.

```typescript
const BACKEND_BRAND_COLORS: Record<string, string> = {
  claude:    "#D97706",  // amber
  codex:     "#22c55e",  // green
  opencode:  "#3B82F6",  // blue
  shell:     "#6b7280",  // gray
};
```

The dot also shows `hasUnread` as a pulsing indicator (`span.unreadDot`) when the tab is inactive and has received output.

### Tab Context Menu

`TabContextMenu` is a positioned `div[role="menu"]` rendered inline. Closes on click-outside, Escape, or scroll. Actions:

- **Duplicate**: creates new tab with label `"{base} (N)"` and session `"{sanitized-base}-N"`. Disabled if MAX_TABS (8) reached.
- **Rename**: enters inline edit mode in the tab label.
- **Pin / Unpin**: reorders tab to start/end of pinned zone.
- **Close**: removes tab, switches active tab to adjacent.
- **Close Others**: removes all other tabs, calls `deleteTabMetadata` for each.
- **Close All**: calls `POST /api/terminal/sessions/close-all`.

---

## 3. Backend Selection

### BackendPickerPrompt

Modal (`role="dialog" aria-modal="true"`) that appears when the user clicks "+" or presses Ctrl+T. Shows a `<select>` populated from `config.available` (the `available` field from `/api/config/backend`).

On submit, `handleBackendSelect` fires:
- For `shell` backend: `spawnTerminalSession(sessionName, 'shell')` pre-creates tmux session before WS connects.
- For AI backends: session creation is deferred to `TerminalManager.Attach` on first WS connect.

### Default Tabs from Backend Config

On cold start, `useTabInit` creates exactly one tab: the configured default backend (from `config.backend`, falling back to `backends[0]`). The `shell` backend is excluded from auto-creation. Session name follows `{wsPrefix}lead-{backend}-1`.

`validBackends` (Go): `["claude", "codex", "opencode", "gemini", "cursor"]`
The `available` list also includes `"shell"`.

---

## 4. Session Lifecycle

### Session Naming Convention

```
Pattern:  {wsPrefix}lead-{backend}-{n}
wsPrefix: "{workspaceName}--"  (omitted for "default" workspace)

Examples:
  lead-claude-1              (default workspace)
  myws--lead-claude-2        (workspace "myws")
  myws--lead-shell-1         (plain shell)
  issue-loomcli-fghge-1      (issue-linked session)
```

The workspace prefix prevents cross-workspace leakage when multiple workspaces share a tmux server.

### Session Spawn Flow

```
User presses Ctrl+T, selects "claude"
  -> handleBackendSelect("claude")
  -> generateTabName("claude", tabs, workspace)
      returns { sessionName: "myws--lead-claude-2", label: "lead-claude-2" }
  -> setTabs([...tabs, newTab])
  -> setActiveTabId("myws--lead-claude-2")
  -> createTab("myws--lead-claude-2", "lead-claude-2", tabs.length)
      -> PUT /api/terminal/tabs/myws--lead-claude-2
          -> tabmeta.Store.Set() -> HSET terminal:meta:myws:myws--lead-claude-2 ...
          -> hub.Broadcast({ type: "terminal_metadata" })
```

### WebSocket Connection

```
TerminalInstance mounts with sessionName="myws--lead-claude-2"
  -> connectWebSocket("myws--lead-claude-2", ...)
  -> GET /api/terminal/token?session=myws--lead-claude-2
      -> terminalAuth.GenerateToken() -> HMAC-SHA256 token
  -> new WebSocket("ws://.../api/terminal/ws?session=myws--lead-claude-2&token=...")

Server: handleTerminalWS
  -> auth.ValidateToken(token, session)  [one-time check]
  -> manager.Attach("myws--lead-claude-2", "", 80, 24)
      -> tmuxHasSession? No -> tmuxNewSession(internalName, "loom lead --backend claude", 80, 24)
          -> tmux new-session -d -s {name} -x 80 -y 24 loom lead --backend claude
          -> tmux set-option mouse on
          -> tmux set-option history-limit {scrollbackMaxLines}
      -> tmuxAttach(internalName) -> pty.Start(tmux attach-session -t {name})
      -> pty.Setsize(PTY, 80x24)
  -> go ptyToWS(ctx, conn, session, manager, scrollback)
  -> wsToPTY(ctx, conn, session, manager, connID)   [blocks]

Client ws.onopen:
  -> setConnectionState("connected")
  -> fitAddon.fit()
  -> ws.send(encodeResize(cols, rows))  [binary 5-byte frame]
```

### Resize Protocol

When connected, terminal dimensions are sent as a binary frame:
```
Byte 0: 0x01 (resize marker)
Bytes 1-2: cols (uint16 big-endian)
Bytes 3-4: rows (uint16 big-endian)
```
The server's `wsToPTY` detects `len(data) == 5 && data[0] == 0x01` and calls `manager.Resize(connID, cols, rows)` which calls both `pty.Setsize` and `tmux resize-window`.

---

## 5. WebSocket Connection State Machine

```
disconnected
  -> connect() called
connecting
  <- WebSocket.open
connected
  <- WebSocket.close (normal, code 1000)
disconnected
  <- WebSocket.close (code 4001: backend exited)
crashed
  <- WebSocket.error or abnormal close
disconnected -> startAutoReconnect
```

`ConnectionState` type: `"disconnected" | "connecting" | "connected" | "error" | "crashed"`

The `hasConnected` flag tracks whether the WebSocket has ever been open:
- First connection failure: `INITIAL_CONNECT_CONFIG` (maxAttempts=3, baseDelay=3000ms, maxDelay=15000ms).
- Reconnection after successful first connect: `DEFAULT_RECONNECT_CONFIG` (maxAttempts=10, baseDelay=1000ms, maxDelay=30000ms).

Exponential backoff formula: `min(baseDelay * 2^attempt, maxDelay) * jitter` where jitter is `[0.75, 1.25]` for `jitterFactor=0.5`.

### Token Security

Terminal WebSocket connections are protected by a one-time HMAC-SHA256 token (60s expiry, 16-byte random nonce):
1. `fetchTerminalToken(sessionName)` -> `GET /api/terminal/token?session={name}` (requires API key auth)
2. Token embedded in WebSocket URL: `wss://.../api/terminal/ws?session={name}&token={token}`
3. Server validates before WebSocket upgrade: signature, expiry, session name match, nonce not already used
4. Used nonces stored in-memory with 2-minute expiry, cleaned up every 5 minutes

---

## 6. Terminal Features

### Slash Commands

`SlashCommandInterceptor` is a pure TypeScript class instantiated per `TerminalInstance`. It hooks into the `onInput` callback in `connectWebSocket`.

State machine:
```
IDLE
  '/' received -> enter COMMAND_MODE, buffer = ""
  other -> passthrough to WebSocket

COMMAND_MODE
  printable char -> buffer += char, echo to terminal
  Backspace -> buffer.slice(0,-1), erase on screen; if empty -> IDLE
  Ctrl+C -> write "^C\r\n" -> IDLE
  Enter -> EXECUTING: parseSlashCommand(buffer), run, write result -> IDLE
  ESC sequences -> skip 2 remaining bytes (CSI/SS3 sequences)
```

Command registry:

| Command | Arguments | Action |
|---------|-----------|--------|
| `/create-issue` | `<title> [--priority 0-4] [--type task\|bug\|...]` | `POST /api/issues` |
| `/assign` | `<issue-id> <assignee>` | `PATCH /api/issues/{id}` |
| `/status` | `[issue-id]` | `GET /api/issues/{id}` or `/api/stats` |
| `/help` | `[command]` | Lists commands or shows usage |

Results are written with ANSI coloring (`\x1b[32m` green for success, `\x1b[31m` red for error, `\x1b[36m` cyan for info) prefixed with `[system]`.

### Terminal Search

Search uses the browser's native find-in-page against wterm's DOM-rendered cells.
The app does not intercept Cmd/Ctrl+F.

Search decorations use orange for active match (`#EE8B17`) and gray for other matches (`#515C6A`).

### Copy/Paste/Text Selection

**Copy-on-select**: `terminal.onSelectionChange` fires on any selection change. After a 100ms debounce, selected text is ANSI-stripped and written to `navigator.clipboard`. `CopyToast` shows for 1.5s.

**Ctrl+C smart behavior**: If text is selected, copies it (suppresses SIGINT). If no selection, sends SIGINT to the shell.

**Paste**: `Ctrl+V` calls `onPasteRequest`. If clipboard text contains newlines, `PasteConfirmDialog` shows a preview with Confirm/Cancel. Single-line text pastes immediately via `terminal.paste(text)`.

**Right-click context menu**: `TerminalContextMenu` rendered via `createPortal`. Shows Copy (disabled if no selection), Paste, Select All.

### Unread Output Indicator

`handleOutput` in `TerminalView` is called by each `TerminalInstance` via `onOutput`. If the tab is not active, `tabUnread.get(tabId) = true`. This flows to `TerminalTabBar` as `hasUnread: true`, rendering a pulsing `span.unreadDot`.

`hasAnyUnread` is reported to the parent via `onUnreadChange` and drives the `badges.terminal` dot on the NavRail.

---

## 7. Session Persistence

### Active Tab Persistence

Active tab is persisted on every tab switch (debounced 300ms):
1. **sessionStorage**: `sessionStorage.setItem('terminal-active-tab', tabId)` — survives page navigation
2. **Redis**: `PATCH /api/terminal/state` -> `HSET terminal:ui-state:{ws} active_tab {tabId}` — survives browser close

On startup, `useSessionRestore` fetches `GET /api/terminal/state` (Redis). Falls back to `sessionStorage`.

### Tab Metadata

Tab metadata (labels, notes, sort order, pinned state, issue linkage) is stored per workspace in Redis:
```
terminal:meta:{workspace}:{session_name}  (Redis Hash)
```
Metadata survives session death — tabs remember custom labels even after the tmux process exits.

### Workspace-Scoped Tab Isolation

`useWorkspaceTabState` subscribes to `WorkspaceContext`. When workspace changes:
1. Saves current tab set to in-memory map keyed by workspace name
2. Restores saved tab set for the new workspace (or resets to empty)
3. Sets `initializedRef.current = false` to allow re-initialization

### Session Restore on Browser Refresh

```
useSessionRestore mounts
  -> GET /api/terminal/state
      -> client.HGetAll("terminal:ui-state:{ws}") -> { active_tab: "myws--lead-claude-2" }
  -> setActiveTabId("myws--lead-claude-2")

useTabInit runs (after metaLoading=false, configLoading=false, isViewActive=true)
  -> GET /api/terminal/tabs?workspace=myws (via useTerminalMetadata)
      -> store.EnsureDefaults(ctx, "myws", [active tmux sessions])
      -> returns TabMetadata[] sorted by Pinned DESC, SortOrder ASC
  -> restoredTabs = tabMetadata.map(m => TabState)
  -> setTabs(restoredTabs)
```

---

## 8. Talk to Lead

### First-Time Onboarding

`WelcomeBanner` appears as a full-width overlay inside the terminal pane after the session first connects and is not yet dismissed globally. Dismissal stored in `localStorage["terminal-onboarding-dismissed"]`.

Each backend has custom description and example prompts. Clicking an example calls `instance.pasteText(example)` and dismisses the banner.

### Project Context Banner

When a new talk-to-lead session is created, the server injects a context banner via `FetchTerminalContext(loomServerURL)` against `/api/status`. The banner uses Unicode box-drawing characters and shows workspace name, task counts, agent statuses, and planning pipeline counts.

### Active Session Badge

`TerminalView` reports connected tab count via `onActiveSessionCountChange`:
- `NavRail`: numeric badge on Terminal button when `sessionCount > 0`
- `TalkToLeadButton`: `SessionBadge` inside the FAB
- Both update `aria-label` for screen reader announcement

---

## 9. Crash Recovery and Staleness Detection

### Backend Crash Detection

**Server-side**: `ptyToWS` reads from the PTY. On read error, it checks:
1. `!manager.tmuxHasSession(session.Name)` — session is completely gone
2. `manager.paneDead(session.Name)` — `tmux list-panes -F #{pane_dead}` returns `1`

If either is true, closes WebSocket with code `4001` and captures the last 10 lines of pane output as the close reason (truncated to 123 bytes at UTF-8 boundaries).

**Client-side**: `ws.onclose` checks `event.code === 4001`:
```typescript
const WS_CLOSE_BACKEND_EXITED = 4001;
if (event.code === WS_CLOSE_BACKEND_EXITED) {
    setConnectionState("crashed");
    onBackendCrash?.(event.reason);
    return;  // do NOT auto-reconnect
}
```

### CrashOverlay Actions

- **Restart**: `restartTerminalSession(sessionName, token)` (POST /api/terminal/restart). Server kills old session, updates manager's default command, responds. Then `instance.reconnect()` creates a new WebSocket.
- **Close Tab**: `handleTabClose`.

### Session Scrollback

On reconnect, `fetchScrollback(sessionName)` calls `GET /api/terminal/sessions/{session}/scrollback` which runs `tmux capture-pane -p -S -5000`. Terminal is cleared and scrollback is written before new WebSocket connects, giving continuity.

Scrollback files are persisted to `~/.loom/session-scrollback/{sessionName}.log` when a session is killed (last 10,000 lines via `tmux capture-pane -S -10000`).

Export (`GET /api/terminal/sessions/{session}/export?format=txt|md`) runs `tmux capture-pane -p -S -` (full history), ANSI codes stripped via `StripANSI`.

---

## 10. Issue-Linked Sessions

When `issueId` prop is passed to `TerminalView`, `useSessionManagement` creates a tab with session name `issue-{sanitized-issueId}`.

When `pendingIssueContext` is passed, `TerminalView` calls `POST /api/terminal/sessions/{name}/seed` once the tab connects, which uses `tmux send-keys` to inject the issue context prompt:
```
I need help with issue {issue_id}: {title}

Description: {description (max 800 chars)}

Design: {design (max 500 chars)}

Blockers:
- {id}: {title}
```

If the issue tab already exists, the user is switched to it without re-seeding.

---

## 11. Split View

`SplitDivider` uses pointer capture events:
```
onPointerDown -> setPointerCapture -> listen to document pointermove/pointerup
pointermove   -> compute ratio = (x - rect.left) / rect.width, clamp [0.2, 0.8]
pointerup     -> cleanup, release capture
doubleClick   -> reset to DEFAULT_SPLIT_RATIO (0.5)
```

Split ratio persisted to `sessionStorage["terminal-split-ratio"]`. Split view disabled below `MIN_SPLIT_WIDTH_PX = 900px` via `window.matchMedia`.

---

## 12. SSE Integration

`useTerminalMetadata.handleMutation` listens for SSE events of type `"terminal_metadata"`. On receipt, a 100ms debounced refetch is triggered. This enables multi-browser-tab sync.

The server broadcasts `"terminal_metadata"` events from:
- `handlePutTerminalTab` (new tab created)
- `handlePatchTerminalTab` (label/notes/pin/sort updated)
- `handleDeleteTerminalTab` (tab removed)

A separate `"terminal_session_change"` event is broadcast when issue linkage changes or sessions are closed/reconnected.

---

## 13. State Persistence Model

| State | Storage | Scope | Lifetime |
|-------|---------|-------|---------|
| Active tab ID | Redis `terminal:ui-state:{ws}` hash | Per workspace | Until overwritten |
| Active tab ID (fast) | `sessionStorage["terminal-active-tab"]` | Browser tab | Browser tab close |
| Tab metadata | Redis `terminal:meta:{ws}:{session}` | Per workspace | Until DELETE |
| Onboarding dismissed | `localStorage["terminal-onboarding-dismissed"]` | Browser profile | Until cleared |
| Split view state | `sessionStorage["terminal-split-view"]` | Browser tab | Browser tab close |
| Split ratio | `sessionStorage["terminal-split-ratio"]` | Browser tab | Browser tab close |
| Search term | React state | Component | Navigation away |
| Tab unread flags | React state | Component | Navigation away |

---

## 14. Backend APIs

### Terminal Core

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/terminal/token` | Generate one-time HMAC-SHA256 WS auth token |
| GET | `/api/terminal/ws` | WebSocket upgrade for terminal relay |
| POST | `/api/terminal/spawn` | Pre-create tmux session for shell tabs |
| POST | `/api/terminal/restart` | Kill + recreate tmux session |
| POST | `/api/terminal/kill` | Kill terminal session |

### Tab Metadata

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/terminal/tabs` | List tab metadata for workspace |
| PUT | `/api/terminal/tabs/{session}` | Create/replace tab metadata |
| PATCH | `/api/terminal/tabs/{session}` | Update individual metadata fields |
| DELETE | `/api/terminal/tabs/{session}` | Remove tab metadata |

### Session Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/terminal/sessions` | List active tmux sessions |
| POST | `/api/terminal/sessions/{session}/seed` | Inject issue context via send-keys |
| POST | `/api/terminal/sessions/{session}/schedule-kill` | Deferred session kill |
| POST | `/api/terminal/sessions/close-all` | Kill all sessions |
| GET | `/api/terminal/sessions/{session}/scrollback` | Capture pane scrollback |
| GET | `/api/terminal/sessions/{session}/scrollback-info` | Scrollback line counts |
| GET | `/api/terminal/sessions/{session}/export` | ANSI-stripped scrollback download |
| GET | `/api/terminal/session-status` | Check if session alive / pane dead |
| GET | `/api/terminal/sessions/by-issue` | List sessions linked to an issue |

### UI State

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/terminal/state` | Fetch persisted active tab from Redis |
| PATCH | `/api/terminal/state` | Save active tab to Redis |

---

## 15. File Map

### Frontend Components

| File | Responsibility |
|------|---------------|
| `components/TerminalView/TerminalView.tsx` | Root orchestrator; tab state, unread tracking |
| `components/TerminalView/tabs/TerminalTabBar.tsx` | WAI-ARIA tablist with dnd-kit drag-and-drop |
| `components/TerminalView/tabs/SortableTab.tsx` | dnd-kit `useSortable` wrapper for tab elements |
| `components/TerminalView/tabs/TabContextMenu.tsx` | Right-click context menu |
| `components/TerminalView/instances/TerminalInstance.tsx` | Single wterm terminal with WebSocket |
| `components/TerminalView/instances/terminalConnection.ts` | WebSocket lifecycle: token fetch, URL build, `encodeResize` |
| `components/TerminalView/tabs/terminalTabUtils.ts` | Constants, TabState type, session name generators |
| `components/TerminalView/layout/BackendPickerPrompt.tsx` | Modal for selecting backend for new tab |
| `components/TerminalView/layout/WelcomeBanner.tsx` | First-time onboarding overlay |
| `components/TerminalView/controls/HelpPopover.tsx` | Terminal help popover |
| `components/TerminalView/controls/NotesBar.tsx` | Per-tab notes editor |
| `components/TerminalView/CrashOverlay.tsx` | Backend-exited overlay with Restart / Close |
| `components/TerminalView/ReconnectingOverlay.tsx` | Reconnect countdown overlay |
| `components/TerminalView/TerminalConnectionOverlay.tsx` | Connecting/disconnected/error overlays |
| `components/TerminalView/SearchBar.tsx` | VS Code-style search with N-of-M counter |
| `components/TerminalView/NotesBar.tsx` | Collapsible per-tab notes with auto-save |
| `components/TerminalView/TerminalContextMenu.tsx` | Right-click terminal menu via createPortal |
| `components/TerminalView/HelpPopover.tsx` | Keyboard shortcut reference popover |
| `components/TerminalView/SplitDivider.tsx` | Draggable vertical divider |
| `components/TerminalView/SplitPaneSelector.tsx` | Right split pane tab picker |
| `components/EmbeddedTerminal/EmbeddedTerminal.tsx` | TerminalInstance wrapper for issue panels |
| `components/EmbeddedTerminal/TerminalHeader.tsx` | Backend info + git actions header |

### Frontend Hooks

| File | Responsibility |
|------|---------------|
| `components/TerminalView/useTabInit.ts` | Initialize tabs from Redis or create default |
| `components/TerminalView/useWorkspaceTabState.ts` | Save/restore tab sets on workspace switch |
| `components/TerminalView/useTabActions.ts` | Tab close, duplicate, rename handlers |
| `components/TerminalView/useTabOrdering.ts` | Pin, reorder, close-others handlers |
| `components/TerminalView/useCloseAllSessions.ts` | Issue-linked tab creation + close-all |
| `components/TerminalView/useClipboard.ts` | Copy toast, multi-line paste confirmation |
| `components/TerminalView/useSplitView.ts` | Split-pane state: ratio, right tab, media query |
| `hooks/useTerminalMetadata.ts` | Redis-backed tab metadata CRUD with SSE debounce |
| `hooks/useTerminalSessions.ts` | List active tmux sessions |
| `hooks/useSessionRestore.ts` | Fetch persisted active tab from server |
| `hooks/useTerminalFont.ts` | Font family/size preferences |
| `api/terminal.ts` | All terminal REST API calls |
| `utils/reconnectBackoff.ts` | `startAutoReconnect` with exponential backoff + jitter |

### Backend (Go)

| File | Responsibility |
|------|---------------|
| `internal/webui/terminal.go` | TerminalManager struct, `ErrTmuxNotFound`, `ErrMaxSessionsReached`, core fields |
| `internal/webui/terminal_lifecycle.go` | Shutdown, deferred kill cancellation, session cleanup |
| `internal/webui/terminal_auth.go` | HMAC-SHA256 one-time token (60s expiry, single-use nonce) |
| `internal/webui/terminal_context.go` | FetchTerminalContext + FormatContextBanner |
| `internal/webui/terminal_health.go` | SessionAlive, PaneDead, CapturePane |
| `internal/webui/terminal_sessions.go` | KillAllSessions, CaptureScrollback, captureScrollbackToFile |
| `internal/webui/handlers_terminal.go` | `handleTerminalToken`, `handleTerminalRestart`, `handleTerminalKill`, `handleTerminalSessionStatus`, constants |
| `internal/webui/handlers_terminal_ws.go` | WebSocket relay (`handleTerminalWS`), `ptyToWS`, `wsToPTY`, `crashInfo` |
| `internal/webui/handlers_terminal_tabs.go` | REST: GET/PUT/PATCH/DELETE /api/terminal/tabs |
| `internal/webui/handlers_terminal_sessions.go` | REST: list sessions, seed, schedule-kill, close-all |
| `internal/webui/handlers_terminal_spawn.go` | REST: POST /api/terminal/spawn |
| `internal/webui/handlers_terminal_state.go` | REST: GET/PATCH /api/terminal/state |
| `internal/webui/handlers_terminal_scrollback.go` | REST: GET scrollback |
| `internal/webui/handlers_terminal_export.go` | REST: GET export (ANSI-stripped download) |
| `internal/webui/tabmeta/store.go` | Redis-backed TabMetadata store |
| `internal/webui/handlers_config.go` | attachCommandForSession, shellCommand, validBackends |
