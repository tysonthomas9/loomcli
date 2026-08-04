# Terminal System Architecture (Epic uffak)

> **Status:** Current · rewritten 2026-07-23 against the post-tmux tree
> (`54b03ac10`, #226, 2026-07-20 "standardize terminals on xterm with
> persistent scrollback"). The previous revision described a tmux-backed
> manager, flat `/api/terminal/*` routes and a slash-command subsystem; none of
> those exist. *audited 2026-08-03*

## Overview

The web UI terminal is a tabbed xterm.js surface relayed over a WebSocket to a
**PTY** on the server. It is not tmux-backed.

There are **two independent terminal paths**, with different lifetimes and
different handlers. Confusing them is the single most common mistake when
reading this code:

| | Main web terminal | Agent terminal viewer |
|---|---|---|
| Backend | `PTYManager` / `MultiPTYManager` (`internal/webui/terminal/pty_manager.go`, `multi_pty_manager.go`) | `AgentTmuxManager` (`internal/webui/terminal/agent_tmux.go`) |
| Who creates the process | the web server, on first attach | the CLI — `loom task --auto` / `loom plan --auto`, and only when tmux is installed (`internal/cli/agent/task.go:97-99`, `plan.go:107`); the session is created by `startTmuxSession` (`internal/cli/automode/automode_tmux_session.go:14`) |
| Session lifetime | outlives the WebSocket; detach-not-kill | outlives everything; the web UI only attaches |
| Direction | read/write | read-only attach (`agent_tmux.go:51` "never creates tmux sessions — only attaches") |
| Route | `GET /api/workspaces/{ws}/terminal/ws` | `GET /api/workspaces/{ws}/agents/{name}/terminal/ws` |
| Scrollback | in-process ring buffer, replayed on attach | `tmux capture-pane` |

tmux survived the migration *only* because auto-mode agent processes must
outlive the CLI invocation that spawned them
(`internal/webui/handlers/terminal/agent.go:140-144`).

---

## 1. Component Hierarchy

```
NavRail (components/NavRail/NavRail.tsx:137 id: "terminal")

TerminalView (components/TerminalView/TerminalView.tsx)   <- orchestrator
  +-- useWorkspaceTabState   (tabs/)      <- per-workspace tab set isolation
  +-- useTabInit             (tabs/)      <- cold-start tab creation from metadata
  +-- useTabActions          (tabs/)      <- close / duplicate / rename
  +-- useTabOrdering         (tabs/)      <- pin / reorder / close-others
  +-- useUnreadTracking      (tabs/)      <- per-tab unread output flags
  +-- useTabEditorGroups     (layout/)    <- VS Code-style editor columns
  +-- useSplitView           (layout/)    <- split ratio + right-pane tab
  +-- useSessionSeeding      (instances/) <- issue-context and agent-tab seeding
  +-- useConnectionState     (instances/) <- per-tab connection state fan-in
  +-- useSessionRestore      (hooks/terminal/) <- restore active tab on load
  +-- useTerminalMetadata    (hooks/terminal/) <- Redis tab metadata + SSE refetch
  |
  +-- TerminalTabBar (tabs/)              <- WAI-ARIA tablist
  |    +-- SortableTab (dnd-kit)
  |    +-- TabContextMenu
  |
  +-- TerminalPaneArea (instances/)       <- group/split layout host
  |    +-- TerminalPane (instances/)
  |         +-- TerminalInstance (instances/)
  |              +-- XTermRenderer        <- xterm.js mount + FitAddon
  |              +-- terminalConnection   <- token fetch, WS lifecycle, resize
  |              +-- TerminalConnectionOverlay / ReconnectingOverlay / CrashOverlay
  |
  +-- layout/: BackendPickerPrompt, NewTerminalTabMenu, NoBackendsEmptyState,
  |            SessionNamePrompt, SplitDivider, SplitPaneSelector
  +-- controls/: HelpPopover, NotesBar

EmbeddedTerminal (components/EmbeddedTerminal/)  <- reuses TerminalInstance in issue panels
  +-- TerminalHeader
```

`TerminalView` also renders inside the Agents view with `hideTabs` set, where
the parent supplies agent selection and split controls
(`TerminalView.tsx` `hideTabs` / `onSplitControlsChange` props).

---

## 2. Terminal Tab Management

### Tab Model

```typescript
// components/TerminalView/tabs/terminalTabUtils.ts:22
interface TabState {
  id: string;            // == sessionName
  label: string;         // display label, e.g. "lead-claude-1"
  sessionName: string;   // PTY session key, e.g. "myws--lead-claude-1"
  connectionState: ConnectionState;
  backendName: string;
  pinned?: boolean;
  crashReason?: string | null;
  kind?: string;         // "agent" for agent-harness PTYs
  role?: string;
  agentName?: string;
  writable?: boolean;
}
```

`MAX_TABS = 40` (`terminalTabUtils.ts:14`) — deliberately equal to the PTY
manager's default per-workspace cap, `defaultPTYMaxSessions = 40`
(`internal/webui/terminal/pty_manager.go:50`).

`isAgentTab` / `isAgentMetadata` (`terminalTabUtils.ts:43,56`) classify a tab as
an agent PTY from persisted `kind`/`agent_id`, falling back to an `agent-`
session-name prefix. The user-editable label is deliberately never consulted.

### TerminalTabBar

`TerminalTabBar` implements the WAI-ARIA `tablist` pattern; each tab is a
`SortableTab` (dnd-kit `useSortable`) wrapping a `div[role="tab"]`.

**Reordering and pinning:** the drop handler calls `reorderTabMeta`
(`tabs/useTabOrdering.ts:69`), which issues parallel `PATCH … {sort_order: i}`
calls (`hooks/terminal/useTerminalMetadata.ts:216-224`). Listings come back
sorted pinned-first then by sort order (`internal/webui/tabmeta/store.go:176-179`).

**Overflow:** a `ResizeObserver` on the tab list (`TerminalTabBar.tsx:205`)
drives scroll arrows that scroll by 150 px (`TerminalTabBar.tsx:566,582`).

### Keyboard

There is **no global terminal keyboard-shortcut layer**. `TerminalView` installs
no `keydown` listener; keys go to xterm.js and the browser. The only in-app
keyboard handling is Escape-to-close inside `TabContextMenu` and `HelpPopover`,
plus WAI-ARIA arrow navigation inside `TerminalTabBar`.

`HelpPopover` advertises exactly three shortcuts
(`controls/HelpPopover.tsx:15-19`), all handled by the browser or xterm.js, not
by loom: Ctrl+F (browser find-in-page), Ctrl+Shift+C, Ctrl+Shift+V. The two
slash commands it lists (`/help`, `/clear`, `HelpPopover.tsx:21-24`) are the
*shell's* — loom does not intercept terminal input.

### Backend Brand Color Dots

Each tab renders `span.statusDot` with `data-status={connectionState}` and, when
known, a `--brand-color` custom property. The palette lives outside the terminal
component graph so non-terminal consumers can import it —
`BACKEND_BRAND_COLORS` is defined in `utils/workspace` and re-exported from
`tabs/terminalTabUtils.ts:11`.

`hasUnread` renders a pulsing `span.unreadDot` on inactive tabs with new output.

### Tab Context Menu

`TabContextMenu` is a positioned `div[role="menu"]`, closed by click-outside,
Escape, or scroll. Actions: Duplicate (`getNextDuplicateName`, returns null at
`MAX_TABS` — `terminalTabUtils.ts:150-154`), Rename, Pin/Unpin, Close, Close
Others. There is no "Close All" server call: the close-all endpoint was removed
with tmux.

---

## 3. Backend Selection

`BackendPickerPrompt` / `NewTerminalTabMenu` (`layout/`) take an
`availableBackends` prop. `TerminalView` passes `selectableBackends`, built as
`["shell", ...config.available.filter(b => b !== "shell")]`
(`TerminalView.tsx:611-614`, handed to the two components at `:863` and `:967`).
`config` comes from `useBackendConfig` (`TerminalView.tsx:159`), which fetches
`GET /api/workspaces/{ws}/config/backend`
(`hooks/workspace/useBackendConfig.ts:42`, registered at
`internal/webui/app/routes.go:155`). The server-side allow-list is
`terminal.ValidBackends = ["claude", "codex", "opencode", "gemini", "cursor"]`
(`internal/webui/terminal/session_command.go:13`); `shell` is additionally
selectable in the UI.

`GET /api/config/terminal` (`internal/webui/app/routes.go:47`) is a different
endpoint and carries no backend list. It returns
`TerminalLifecycleConfig{grace_period_ms, idle_timeout_ms, max_sessions}`
(`internal/webui/handlers/terminal/terminal_config.go:12-16`), and its only
frontend consumer is `TerminalInstance`, which uses the grace period to bound
auto-reconnect retries (`api/common/terminalConfig.ts:38-41`,
`instances/TerminalInstance.tsx:166`).

There is no spawn request. The command for a session is resolved at attach time
by `launchSpecForTerminalSession` (`handlers/terminal/ws.go:370-399`), which has
two sources and a strict precedence:

1. **Persisted `tabmeta.LaunchSpec`** (`{Argv, Env, Cwd}`,
   `internal/webui/tabmeta/store.go:56-62`). Its doc comment states the intent:
   "Agent tabs persist this instead of deriving behavior from the human-facing
   tab name." Used whenever the tab's metadata carries a non-empty spec. For
   `kind == "agent"` tabs it is **mandatory** — a missing spec is
   `errAgentLaunchSpecMissing` (`ws.go:29,388-392`), never a name-derived
   fallback. `HandleEnsureAgentTerminalSession` writes it
   (`agent_session.go:122,127`) and refreshes it when stale (`:141-156`).
2. **Name derivation, legacy only.** `ArgvForSession`
   (`internal/webui/terminal/session_command.go:62`, reached through the
   deliberately named `legacyLaunchSpecForSession`, `ws.go:406-412`) strips an
   optional `{workspace}--` prefix and a trailing `-{n}` counter, matches
   `lead-{backend}` against `ValidBackends`, and returns the shell argv. It is
   consulted only for non-agent tabs with no persisted spec. UUID-style
   `term_*` session names (`isUUIDTerminalSession`, `ws.go:402-404`) are
   excluded outright: without metadata they fail with
   `errTerminalLaunchMetaMissing` rather than falling back to the name.

`ArgvForSession` returning nil yields a nil `LaunchSpec`, and the session starts
under `PTYManager`'s default argv (the login shell).

So the session name still selects the backend for ordinary `lead-{backend}-{n}`
tabs, but it is the fallback, not the mechanism. Do not reason about agent tabs
from their names.

On cold start `useTabInit` creates one tab for the configured default backend.

---

## 4. Session Lifecycle (main web terminal)

### Ownership model

`internal/webui/terminal/pty_manager.go:1-23` is the authoritative statement.
Summarized:

- A PTY is owned by `(workspace, session)`, **not** by a WebSocket.
- WebSocket disconnect **detaches**, it does not kill. A grace timer is armed.
- Re-attach within the grace window cancels the timer; the client receives a
  screen reset followed by the session's scrollback, then live output.
- A session dies when: the grace timer fires with nothing attached; the idle
  reaper finds no output and no attachment; the child exits; or `Kill` is called.

Defaults matter here and are **not** what the package prose implies for local
use: `defaultGracePeriod = 0` and `defaultIdleTimeout = 0`
(`pty_manager.go:56-57`) — local `loom serve` keeps detached sessions alive
indefinitely, on purpose (one developer per server; a leaked PTY beats a killed
shell). Remote `loom-agentd` sets non-zero values through `SetGracePeriod` /
`SetIdleTimeout` (`pty_manager.go:216,224`).

`MultiPTYManager` (`multi_pty_manager.go:62`) holds one `PTYManager` per
registered workspace, each rooted at that workspace's directory. It is what
`internal/webui/app/server_app.go:195` constructs. Unregistered or
invalid-path workspaces produce `ErrWorkspaceNotRegistered` /
`ErrInvalidWorkspacePath` (`multi_pty_manager.go:17,22`), which the WS handler
turns into a clean error rather than a panic.

The handler talks to the backend only through the `PTYSource` interface
(`internal/webui/terminal/source.go:20-22`) — `AttachSession` / `Detach` / `Kill` —
so a remote agentd client can be substituted without touching
`handlers/terminal/ws.go`.

### Scrollback

Each session owns a `ringBuffer` (`pty_session.go:89,126`) of
`defaultRingCapacity = 256 * 1024` bytes (`ringbuf.go:12` — ~2000 lines; 40
sessions ≈ 10 MB). Because PTY output is stateful, the ring keeps a checkpoint
of persistent private modes (alternate buffer, mouse protocols) at its head so a
fresh browser emulator can replay a long-lived TUI correctly
(`ringbuf.go:14-25`). `ReplaySnapshot` returns `(checkpoint, body)` and the
attach path emits `checkpoint + screen-reset + body`
(`pty_session.go:206-215`).

### Session Naming Convention

```
{wsPrefix}lead-{backend}-{n}     wsPrefix = "{workspaceName}--" (omitted for "default")
agent-…                          agent harness PTYs
issue-{sanitized-issueId}        issue-linked tabs (sanitizeSessionName)
```

Names are validated server-side against `^[a-zA-Z0-9_-]+$`
(`internal/webui/terminal/service_impl.go:15`,
`internal/webui/tabmeta/store.go:26`).

### WebSocket connection

```
TerminalInstance mounts with sessionName
  -> GET /api/workspaces/{ws}/terminal/token?session=…
         (internal/webui/handlers/terminal/module.go:93)
  -> new WebSocket(".../api/workspaces/{ws}/terminal/ws?session=…&token=…")
         (module.go:96 -> HandleTerminalWS, handlers/terminal/ws.go:98)

Server: HandleTerminalWS
  -> TerminalAuth.ValidateToken(token, session, workspace)   [single use]
  -> PTYSource.AttachSession(key, cols, rows, launchSpec)
       reattached==true  -> replay scrollback
       reattached==false -> start the child from the resolved LaunchSpec (§3)
  -> realtime.AttachmentToWS  (binary output frames)   ws.go:334
  -> realtime.WSToPTY         (text input frames)      ws.go:340  [blocks]
```

The agent-terminal path uses the older `realtime.PtyToWS`
(`terminal_relay.go:90`, called from `handlers/terminal/agent.go:340`) because
it reads a tmux PTY directly. The main path uses `AttachmentToWS`
(`terminal_relay.go:204`), which consumes the session's fan-out channel rather
than the fd — the session's own drain goroutine is the sole PTY reader.

### Resize protocol

Resize is an **in-band text control message**, not a binary frame:

```
"\x1b[RESIZE:<cols>;<rows>]"
```

Client: `encodeResize` (`instances/terminalConnection.ts:67`).
Server: `resizeRE` (`internal/webui/server/realtime/terminal_relay.go:57`),
dispatched at `terminal_relay.go:156`.

### Banners written into the PTY on attach

Two server-side banners are written into the session on attach. Neither is a UI
element — both are bytes in the terminal stream, so they appear in scrollback
replay like any other output.

**Project-context banner.** A session named exactly `talk-to-lead` gets a
project-status banner written ahead of the shell prompt, but only on a *fresh*
spawn and only when `loomServerURL` is set (`handlers/terminal/ws.go:309-311`);
on re-attach the banner is already in the replayed ring.
`injectTerminalContextBanner` (`ws.go:465`) calls
`webuterminal.FetchTerminalContext` (`internal/webui/terminal/context.go:50`),
which GETs **`/api/monitor/status`** with a 3 s timeout (`context.go:13,54`) —
note the path, an earlier revision of this doc said `/api/status`. The result
is rendered by `FormatContextBanner` (`context.go:78`) as Unicode box-drawing
borders around three lines built in `buildBannerLines` (`context.go:95-122`):
`Tasks:` open/blocked/review/in-progress counts, `Agents:` name (status) pairs
or "none active", and `Planning:` need-plans / ready-to-implement counts. A
fetch failure is logged and the banner skipped; it never blocks the attach.

**Stale-restart notice.** `maybeEmitStaleRestartBanner` (`ws.go:317,450-461`)
compares the tab's stored `CreatedAt` against `serverStartedAt`; if the tab
predates the current server process it writes the yellow line
`[loom] Previous shell did not survive a server restart. This is a fresh
session.` The tab metadata survived the restart (§7) but the PTY did not.

This banner is the *fallback* signal, not the primary one. The tab DTO carries
`pty_alive` (`internal/webui/tabmeta/store.go:52`, computed at read time by
`ptyAttachable`, `internal/webui/terminal/service_impl.go:63-68`, and annotated
onto every tab at `service_tabs.go:34,55,76`). When it is `false` the frontend
never opens the WebSocket at all: `TerminalInstance` skips auto-connect and
forces `session_ended` (`instances/TerminalInstance.tsx:344-345,362-366`, fed
from `TerminalView.tsx:789,795-796`), and `TerminalConnectionOverlay` explains
the loss in words (`TerminalConnectionOverlay.tsx:42-50`). `ws.go:446-449` calls
that gate "the authoritative block" and this banner "the reliable fallback", for
clients that reached the attach anyway — browsers drop app-defined WebSocket
close codes right after upgrade.

---

## 5. WebSocket Connection State Machine

```
disconnected --connect()--> connecting --open--> connected
connected --close 1000 (child exited / detach)--> session_ended  (no auto-reconnect)
connected --close 4002 (session killed)---------> session_ended  (no auto-reconnect)
connected --close 1001 "workspace unavailable"--> error
connected --close 4001 (agent path only, below)-> crashed        (no auto-reconnect)
connected --error / abnormal close--------------> disconnected -> startAutoReconnect
```

`ConnectionState` = `"disconnected" | "connecting" | "connected" | "error" |
"crashed" | "session_ended"` (`instances/TerminalInstance.tsx:67-76`).
`session_ended` exists because tab metadata outlives the PTY across a server
restart; the overlay prompts before opening a fresh shell so scrollback loss is
explicit rather than silent (`TerminalInstance.tsx:73-75`).

Close codes are declared on both sides and must stay in sync:
`instances/terminalConnection.ts:72-79` and
`internal/webui/server/realtime/terminal_relay.go:37,43`.

| Code | Meaning | Source |
|---|---|---|
| 1000 | clean server-side close | standard |
| 1001 | workspace runtime unavailable | `terminalConnection.ts:78-79` |
| 4001 | backend process exited — **agent-terminal path only** | `WSCloseBackendExited` |
| 4002 | session explicitly killed | `WSCloseSessionKilled` |

The server picks the code from the attachment's exit reason —
`ExitReasonKilled` / `ExitReasonExited` / `ExitReasonShutdown`
(`internal/webui/terminal/pty_session.go:26-28`), matched as string literals at
`terminal_relay.go:215-223` (deliberately not imported: `terminal` already
imports `realtime`).

**4001 is unreachable on the main web terminal.** `AttachmentToWS` maps
`"killed"` to 4002 and `"exited"` to `websocket.StatusNormalClosure` (1000),
returning `CrashInfo{Killed: true}` or `CrashInfo{}` — never
`CrashInfo{Crashed: true}` (`terminal_relay.go:215-223`) — and the handler's
close code is `(<-crashCh).WSClose()` (`handlers/terminal/ws.go:346`,
`CrashInfo.WSClose` at `terminal_relay.go:70-82`). A child process that simply
exits therefore closes 1000 → `session_ended`, not 4001 → `crashed`. The sole
producer of `CrashInfo{Crashed: true}` is `PtyToWS`, when its tmux monitor
reports the session gone or the pane dead (`terminal_relay.go:111-118`) — the
agent-terminal viewer. The client still branches on 4001
(`terminalConnection.ts:233-237`) and `CrashOverlay` still ships, but nothing on
the main path triggers them.

Reconnect backoff (`utils/reconnectBackoff.ts`): first-connect failures use a
tighter config than post-connect reconnects; delay is
`min(baseDelay * 2^attempt, maxDelay) * jitter`.

### Token security

One-time HMAC-SHA256 token, `TerminalTokenExpiry = 60s`
(`internal/webui/server/realtime/terminal_auth.go:17-18`), 16-byte random nonce
(`:62`). `ValidateToken` (`:91`) checks signature, expiry, workspace/session
match and single use; used nonces are swept by a background cleanup loop
(`:156-179`).

---

## 6. Copy / paste and search

xterm.js and the browser own these. Selection, copy, paste and find-in-page are
native; loom installs no clipboard interception, no copy-on-select debounce, no
paste-confirmation dialog and no terminal context menu in the main terminal view
(`useClipboard`, `TerminalContextMenu`, `SearchBar` and `WelcomeBanner` are all
absent from `internal/webui/frontend/src` — the only surviving mention is a
stale header comment in `components/TerminalView/__tests__/TerminalView.test.tsx:6`).

The slash-command subsystem (`SlashCommandInterceptor`, `parseSlashCommand`, the
`/create-issue`, `/assign`, `/status` registry) was removed with the tmux
migration and has no replacement.

### Unread output indicator

`useUnreadTracking` (`tabs/useUnreadTracking.ts`) flags inactive tabs that
receive output; `TerminalTabBar` renders the pulsing dot and `TerminalView`
reports aggregate unread via `onUnreadChange` to drive the NavRail badge.

Separately, `TerminalView` reports the *connected tab count* via
`onActiveSessionCountChange` (`TerminalView.tsx:336`, reset to 0 at `:350`).
`App.tsx` holds it in `activeSessionCount` (`:485`), passes it to
`TerminalView` (`:1427`) and down to `NavRail` as `sessionCount` (`:1378`).
`NavRail` renders a numeric badge on the terminal item when the count is
non-zero, and sets an `aria-label` of "N active sessions"
(`NavRail.tsx:244,259-261`). This is a different indicator from the unread dot
and both can be present at once.

`components/TalkToLeadButton/` (the floating action button and its
`SessionBadge`) still exists on disk and is re-exported from
`components/index.ts:60`, but nothing renders it — `App.test.tsx:2450,2461`
asserts it is absent from both the app shell and the terminal view under Aether
V3. Treat it as dead code, not as a documented surface.

---

## 7. Persistence

### Tab metadata (Redis)

```
terminal:meta:{workspace}:{session_name}     (hash)
```

`internal/webui/tabmeta/store.go:3,24`. Fields: label, notes, sort_order,
created_at, updated_at, plus agent linkage. `pty_alive` and `attached_clients`
ride the same DTO but are **not** persisted — the service layer fills them in at
read time from the in-process `PTYManager` (`store.go:31-35,52-53`). Metadata
survives session death, so a tab remembers its label after the PTY is gone.
*Note:* the package comment at
`tabmeta/store.go:3` still says "Each tmux session" — that wording is a
leftover; the key is per PTY session now.

### Terminal UI state (Redis)

```
terminal:ui-state:{workspaceID}              (hash, currently just active_tab)
```

`internal/webui/terminal/service_tabs.go:17-18`.

### Issue tab state (Redis)

```
ws:{workspaceID}:issue:tabs:{issueId}        (JSON blob, 24h TTL)
```

`internal/webui/issuetabs/store.go:1-5`. Owned by the issue detail view — see
[issue-detail-view.md](issue-detail-view.md).

### Browser storage

| State | Key | Scope |
|---|---|---|
| Active tab (fast path) | `sessionStorage["terminal-active-tab"]` | Browser tab |
| Split view on/off | `sessionStorage["terminal-split-view"]` | Browser tab |
| Split ratio | `sessionStorage["terminal-split-ratio"]` | Browser tab |
| Split right-pane tab | `sessionStorage["terminal-split-right-tab"]` | Browser tab |

The three split keys are owned by
`components/TerminalView/layout/useSplitView.ts:21-58`.
`terminal-active-tab` lives elsewhere: written by `TerminalView.tsx:319,543`,
read by `tabs/useTabInit.ts:117,256` and `hooks/terminal/useSessionRestore.ts:43`.

### Restore on refresh

`useSessionRestore` (`hooks/terminal/useSessionRestore.ts:35`) reads
`GET /api/workspaces/{ws}/terminal/state` and falls back to `sessionStorage`
(`:42-43`). `useTabInit` then rebuilds `TabState[]` from
`GET /api/workspaces/{ws}/terminal/tabs`.

### Workspace-scoped isolation

`useWorkspaceTabState` saves the current tab set into an in-memory map keyed by
workspace and restores the incoming workspace's set, re-arming initialization.
The server side is already isolated: `MultiPTYManager` keeps one manager per
workspace.

---

## 8. Crash Recovery

**Server:** when the PTY output channel closes, `AttachmentToWS` consults
`Attachment.ExitReason()` and writes the close frame *before* cancelling the
context — cancelling first would poison the websocket state and the browser
would see 1006 instead of the intended 4002 or clean 1000
(`internal/webui/server/realtime/terminal_relay.go:198-203,215-223`).

**Client:** `terminalConnection.ts:233-247` branches on the close code; 4001
sets `crashed` and surfaces `CrashOverlay`, 4002 and 1000 set `session_ended`.
None of the three auto-reconnect. On the main terminal only 4002 and 1000 ever
arrive; 4001 comes from the agent-terminal relay (§5).

`CrashOverlay` (`instances/CrashOverlay.tsx`) offers **Restart** and **Close
Tab**. Restart is purely client-side: `handleCrashRestart`
(`instances/useConnectionState.ts:102-112`) clears `crashReason` and calls
`instance.reconnect()`. There is no restart endpoint — a fresh WebSocket
attaches, and because the old session is gone the server spawns a fresh shell.

Session scrollback is **not** written to disk and there is no export endpoint.
Continuity comes from the in-process ring replayed on re-attach (§4).

---

## 9. Issue-linked and agent-linked tabs

`useSessionSeeding` (`instances/useSessionSeeding.ts`) handles both:

- `pendingIssueContext` → creates/focuses a tab named
  `issue-{sanitizedIssueId}` (`sanitizeSessionName`,
  `tabs/terminalTabUtils.ts:129`). If the tab already exists the user is
  switched to it.
- `pendingAgentName` → calls `ensureAgentTerminalSession`
  (`POST /api/workspaces/{ws}/agents/{name}/terminal/session`,
  `handlers/terminal/module.go:85`) and focuses the resulting agent tab.

The agent launch command is built server-side, flags **before** the subcommand:
`loom --workspace <ws> [--backend <b>] lead [--prompt <file>]` —
`agentLaunchBaseArgs` (`handlers/terminal/agent_session.go:379-388`) emits the
global flags, `agentLaunchCommandArgs` (`:390-396`) appends the subcommand. The
backend is resolved in three steps — `agent.Backend` → `role.Backend` → daemon
profile `AgentBackend` — `agentLaunchBackend`, `agent_session.go:361-377`. It is
never hardcoded.

`POST /api/workspaces/{ws}/terminal/setup` (`tab_module.go:41`,
`handlers/terminal/setup.go:17-21`) starts a *typed* setup command in a
workspace PTY; it never accepts arbitrary shell input.

---

## 10. Split view and editor groups

`useTabEditorGroups` (`layout/useTabEditorGroups.ts:1-3`) implements VS
Code-style columns: split moves the active tab into a new right column, and
tabs drag between columns. It mirrors `views/AgentEditorGroups.tsx`.

`useSplitView` owns the older two-pane split: ratio clamped to
`[MIN_SPLIT_RATIO, MAX_SPLIT_RATIO]` = `[0.2, 0.8]` with default `0.5`
(`tabs/terminalTabUtils.ts:17-19`), auto-disabled below
`MIN_SPLIT_WIDTH_PX = 900` via `matchMedia`
(`terminalTabUtils.ts:20`, `layout/useSplitView.ts:34-37`).

---

## 11. SSE Integration

`useTerminalMetadata` refetches on SSE mutations of type `terminal_metadata`,
debounced 100 ms (`hooks/terminal/useTerminalMetadata.ts:37,303-305`). The
server broadcasts that type from the tab create/update/delete service methods
(`internal/webui/terminal/service_tabs.go:82-83,133-134,169-170`), which is what
keeps multiple browser tabs in sync.

---

## 12. HTTP API

Route shapes and payloads are documented in [../api.md](../api.md); the list
below is derived from the mux registrations and is the authority on *which*
routes exist. The two now agree: `api/openapi.yaml` declares exactly the seven
live terminal paths — token, ws, tabs, tabs/{session}, sessions/by-issue, state,
setup (`api/openapi.yaml:1933,1952,1984,2007,2113,2133,2177`) — so the generated
reference lists no removed tmux-era lifecycle endpoint and its drift table reads
`| Documented but not registered | 0 |` (`docs/api.md:6627`). The appendix
section "Endpoints removed from the server but still in the spec"
(`docs/api.appendix.md:127`) has outlived its premise: the endpoints it names
are now gone from the spec as well as from the server.

### Main web terminal — `internal/webui/handlers/terminal/module.go`

| Method | Path | Registered at |
|---|---|---|
| GET | `/api/workspaces/{ws}/terminal/token` | `module.go:93` |
| GET | `/api/workspaces/{ws}/terminal/ws` | `module.go:96` |

### Agent terminal viewer — same file

| Method | Path | Registered at |
|---|---|---|
| GET | `/api/workspaces/{ws}/agents/{name}/terminal/info` | `module.go:79` |
| GET | `/api/workspaces/{ws}/agents/{name}/terminal/token` | `module.go:81` |
| POST | `/api/workspaces/{ws}/agents/{name}/terminal/session` | `module.go:85` |
| GET | `/api/workspaces/{ws}/agents/{name}/terminal/ws` | `module.go:88` |

### Tabs, state, setup — `internal/webui/handlers/terminal/tab_module.go`

| Method | Path | Registered at |
|---|---|---|
| GET | `/api/workspaces/{ws}/terminal/tabs` | `tab_module.go:27` |
| GET | `/api/workspaces/{ws}/terminal/tabs/{session}` | `tab_module.go:28` |
| PUT | `/api/workspaces/{ws}/terminal/tabs/{session}` | `tab_module.go:29` |
| PATCH | `/api/workspaces/{ws}/terminal/tabs/{session}` | `tab_module.go:30` |
| DELETE | `/api/workspaces/{ws}/terminal/tabs/{session}` | `tab_module.go:31` |
| GET | `/api/workspaces/{ws}/terminal/sessions/by-issue` | `tab_module.go:34` |
| GET | `/api/workspaces/{ws}/terminal/state` | `tab_module.go:37` |
| PATCH | `/api/workspaces/{ws}/terminal/state` | `tab_module.go:38` |
| POST | `/api/workspaces/{ws}/terminal/setup` | `tab_module.go:41` |

### Config

| Method | Path | Registered at |
|---|---|---|
| GET | `/api/config/terminal` | `internal/webui/app/routes.go:47` |

### Removed

`spawn`, `restart`, `kill`, `close-all`, `schedule-kill`, `seed`,
`lead-session`, `scrollback`, `scrollback-info`, `export`, `session-status` and
`GET /api/terminal/sessions` no longer exist under **any terminal prefix**
(`/api/terminal/...` or `/api/workspaces/{ws}/terminal/...`). Other subsystems
still use the same words: the issue-detail session history serves
`GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback`
(`internal/webui/handlers/issues/session_module.go:52`), documented in
[issue-detail-view.md](issue-detail-view.md). The rationale is
stated at `handlers/terminal/module.go:25-28`: each WebSocket attaches to a
PTY session using the terminal wire protocol, so there is no separate session
lifecycle to expose. `internal/webui/app/routes_test.go:673-693` asserts the
old flat paths return 404.

No stale spec or generated-client artifact survives either:
`grep -nE 'terminal/spawn|spawnTerminalSession|scheduleSessionKill|schedule-kill'`
over `api/openapi.yaml` and
`internal/webui/frontend/src/types/generated/openapi.ts` returns nothing.

---

## 13. File Map

### Frontend components (`internal/webui/frontend/src/`)

| File | Responsibility |
|---|---|
| `components/TerminalView/TerminalView.tsx` | Root orchestrator: tabs, groups, seeding, setup flow |
| `components/TerminalView/tabs/TerminalTabBar.tsx` | WAI-ARIA tablist with dnd-kit reorder |
| `components/TerminalView/tabs/SortableTab.tsx` | dnd-kit `useSortable` wrapper |
| `components/TerminalView/tabs/TabContextMenu.tsx` | Right-click tab menu |
| `components/TerminalView/tabs/terminalTabUtils.ts` | `TabState`, `MAX_TABS`, session-name helpers, split constants |
| `components/TerminalView/instances/TerminalInstance.tsx` | One terminal: xterm + connection + overlays |
| `components/TerminalView/instances/XTermRenderer.tsx` | xterm.js mount, FitAddon, theming |
| `components/TerminalView/instances/terminalConnection.ts` | Token fetch, WS lifecycle, `encodeResize`, close codes |
| `components/TerminalView/instances/TerminalPane.tsx` | Single pane wrapper |
| `components/TerminalView/instances/TerminalPaneArea.tsx` | Group/split layout host |
| `components/TerminalView/instances/CrashOverlay.tsx` | Backend-exited overlay |
| `components/TerminalView/instances/ReconnectingOverlay.tsx` | Reconnect countdown |
| `components/TerminalView/instances/TerminalConnectionOverlay.tsx` | Connecting/disconnected/error overlays |
| `components/TerminalView/layout/BackendPickerPrompt.tsx` | Backend picker modal |
| `components/TerminalView/layout/NewTerminalTabMenu.tsx` | "+" menu |
| `components/TerminalView/layout/NoBackendsEmptyState.tsx` | Empty state when no CLI is installed |
| `components/TerminalView/layout/SessionNamePrompt.tsx` | Name prompt for new sessions |
| `components/TerminalView/layout/SplitDivider.tsx` | Draggable divider |
| `components/TerminalView/layout/SplitPaneSelector.tsx` | Right-pane tab picker |
| `components/TerminalView/controls/HelpPopover.tsx` | Keyboard/slash reference popover |
| `components/TerminalView/controls/NotesBar.tsx` | Collapsible per-tab notes with auto-save |
| `components/EmbeddedTerminal/EmbeddedTerminal.tsx` | `TerminalInstance` wrapper for issue panels |
| `components/EmbeddedTerminal/TerminalHeader.tsx` | Backend info + git actions header |

### Frontend hooks and API

| File | Responsibility |
|---|---|
| `components/TerminalView/tabs/useTabInit.ts` | Build tabs from metadata or create the default |
| `components/TerminalView/tabs/useWorkspaceTabState.ts` | Save/restore tab sets across workspace switches |
| `components/TerminalView/tabs/useTabActions.ts` | Close, duplicate, rename |
| `components/TerminalView/tabs/useTabOrdering.ts` | Pin, reorder, close-others |
| `components/TerminalView/tabs/useUnreadTracking.ts` | Per-tab unread flags |
| `components/TerminalView/layout/useSplitView.ts` | Split ratio, right tab, media query |
| `components/TerminalView/layout/useTabEditorGroups.ts` | VS Code-style editor columns |
| `components/TerminalView/instances/useConnectionState.ts` | Connection-state fan-in |
| `components/TerminalView/instances/useSessionSeeding.ts` | Issue-context and agent-tab seeding |
| `hooks/terminal/useTerminalMetadata.ts` | Tab metadata CRUD + SSE-debounced refetch |
| `hooks/terminal/useSessionRestore.ts` | Restore the active tab on load |
| `hooks/terminal/useTerminalFont.ts` | Font family/size preferences |
| `api/terminal/terminal.ts` | Terminal REST calls |
| `api/terminal/sessions.ts` | Session audit-trail queries |
| `api/terminal/sessionHistory.ts` | Issue session history |
| `api/terminal/logs.ts` | Log-streaming endpoints |
| `utils/reconnectBackoff.ts` | `startAutoReconnect` with exponential backoff + jitter |

Also under `hooks/terminal/`: `useTaskSessions.ts`, `useSessionTranscript.ts`,
`useSessionDiff.ts`, `useDiff.ts`, `useTaskLogPolling.ts` — these serve the
issue/session surfaces, not the terminal itself.

### Backend (Go)

| File | Responsibility |
|---|---|
| `internal/webui/terminal/pty_manager.go` | `PTYManager`: session ownership, grace timer, idle reaper, caps |
| `internal/webui/terminal/multi_pty_manager.go` | One `PTYManager` per registered workspace |
| `internal/webui/terminal/pty_session.go` | PTY fd + child + attachments; `ExitReason*` constants |
| `internal/webui/terminal/ringbuf.go` | 256 KB scrollback ring with private-mode checkpointing |
| `internal/webui/terminal/session_command.go` | `ValidBackends`, `ArgvForSession`, shell argv construction |
| `internal/webui/terminal/source.go` | `PTYSource` interface — the backend seam |
| `internal/webui/terminal/agent_tmux.go` | `AgentTmuxManager`: attach to CLI auto-mode tmux sessions |
| `internal/webui/terminal/context.go` | `TerminalContext` project-status fetch + banner formatting |
| `internal/webui/terminal/service_impl.go` | `TerminalService` core, session-name validation |
| `internal/webui/terminal/service_tabs.go` | Tab metadata + `terminal:ui-state:{ws}` |
| `internal/webui/terminal/service_setup.go` | Typed setup-command runner |
| `internal/webui/handlers/terminal/module.go` | Terminal + agent-terminal route registration |
| `internal/webui/handlers/terminal/tab_module.go` | Tab/state/setup route registration |
| `internal/webui/handlers/terminal/ws.go` | `HandleTerminalWS`, WS spans, disconnect-reason enum |
| `internal/webui/handlers/terminal/terminal.go` | Token handler |
| `internal/webui/handlers/terminal/tabs.go` | Tab CRUD handlers + SSE broadcast |
| `internal/webui/handlers/terminal/state.go` | UI-state handlers |
| `internal/webui/handlers/terminal/setup.go` | `HandleStartTerminalSetup` |
| `internal/webui/handlers/terminal/agent.go` | Agent terminal info/token/WS |
| `internal/webui/handlers/terminal/agent_session.go` | Agent launch spec + backend resolution |
| `internal/webui/handlers/terminal/sessions_by_issue.go` | Sessions linked to an issue |
| `internal/webui/handlers/terminal/terminal_config.go` | `GET /api/config/terminal` payload |
| `internal/webui/server/realtime/terminal_relay.go` | `PtyToWS` / `WSToPTY`, resize parsing, close codes |
| `internal/webui/server/realtime/terminal_auth.go` | One-time HMAC token issue/validate |
| `internal/webui/tabmeta/store.go` | Redis tab metadata store |

---

## Related

- [file-explorer.md](file-explorer.md), [issue-detail-view.md](issue-detail-view.md)
  — the other two web-UI subsystem maps.
- [../api.md](../api.md) — HTTP request/response payloads (its terminal section
  predates the PTY migration).
- [../loom-glossary.md](../loom-glossary.md) — "session" has five distinct
  meanings in this repo; this doc uses *terminal session* throughout.
