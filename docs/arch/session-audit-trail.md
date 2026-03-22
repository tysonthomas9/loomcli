# Session Audit Trail Architecture

## Overview

The Session Audit Trail captures a structured record of every agent invocation across three process boundaries: the parent loom process (lifecycle owner), Claude Code subprocess hooks (transcript writers), and the web server (REST + SSE exposure). The subsystem is file-based and lock-safe with no hard dependency on Redis.

---

## 1. Data Model

### Package: `internal/sessions`

**Session Status:**

```go
type SessionStatus string
const (
    StatusRunning   SessionStatus = "running"
    StatusCompleted SessionStatus = "completed"
    StatusFailed    SessionStatus = "failed"
    StatusAborted   SessionStatus = "aborted"   // set by staleness healer
)
```

**SessionRecord** — index entry in `sessions/index.jsonl`, one per agent run:

```go
type SessionRecord struct {
    SessionID   string
    TaskID      string        // populated at Finalize (unknown at creation)
    EpicID      string
    AgentName   string
    Backend     string        // "claude", "opencode", etc.
    Model       string
    Phase       string        // "planning" or "implementation"
    StartedAt   time.Time
    EndedAt     *time.Time
    DurationS   float64
    Status      SessionStatus
    ExitCode    int
    InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
    EstimatedCostUSD float64
    FilesChanged, LinesAdded, LinesRemoved int
    FilesTouched []string
    AttemptNum   int
    ErrorClass   string
}
```

**TranscriptEntry** — one line in `sessions/<id>/transcript.jsonl`, written via flock from the Claude subprocess:

```go
type TranscriptEntry struct {
    Seq       int       // monotonic (assigned by AppendTranscript via counter file)
    Timestamp time.Time
    Role      string    // "user", "assistant", "system", "tool"
    Type      string    // "text", "tool_use", "tool_result", "session_start", "turn_end", "session_end"
    Content   string
    ToolName  string
    ToolInput string
    Raw       string    // original backend event payload
}
```

**On-disk layout:**

```
<beadsDir>/sessions/
  index.jsonl                      # append-only; all SessionRecords
  <session-id>/
    metadata.json                  # atomic write; final SessionMetadata
    transcript.jsonl               # append-only; TranscriptEntry lines via flock
    prompt.txt                     # initial agent prompt
    diff.patch                     # optional; written at finalization
```

**Session ID format** (`internal/sessions/id.go`):

```
YYYYMMDD-HHMMSS-<agentName>-<taskShort>-<8hexrand>
```

---

## 2. Hook System

### Files

| File | Role |
|------|------|
| `internal/cli/hooks_types.go` | Normalized event types, raw JSON structs |
| `internal/cli/hooks_parse.go` | Stdin parsing, `ParseClaudeHookInput` |
| `internal/cli/hooks_dispatch.go` | Event-to-entry mapping, store dispatch |
| `internal/cli/hooks_install.go` | `settings.json` installer/uninstaller |
| `internal/cli/hooks_cmd.go` | Cobra commands, `runClaudeHook` |

### Hook Installation

`InstallClaudeHooks(worktreePath)` writes to `.claude/settings.json` registering four hooks:

| Claude Code Hook | Loom Command |
|-----------------|--------------|
| `SessionStart` | `loom hooks claude-code session-start` |
| `UserPromptSubmit` | `loom hooks claude-code user-prompt-submit` |
| `Stop` | `loom hooks claude-code stop` |
| `SessionEnd` | `loom hooks claude-code session-end` |
| `PreToolUse[Task]` | `loom hooks claude-code pre-task` |
| `PostToolUse[Task]` | `loom hooks claude-code post-task` |

Idempotent: detects existing entries by prefix match, preserves non-loom hooks.

### Hook Normalization

`ParseClaudeHookInput(hookName, reader)` maps raw hooks to normalized `HookEventType`:

| Hook Name | HookEventType | Key payload |
|-----------|---------------|-------------|
| `session-start` | `HookSessionStart` | `model` |
| `user-prompt-submit` | `HookTurnStart` | `prompt` |
| `stop` | `HookTurnEnd` | session info |
| `session-end` | `HookSessionEnd` | session info |
| `pre-task` | `HookSubagentStart` | task info |
| `post-task` | `HookSubagentEnd` | task info |

Unhandled hook names return `(nil, nil)` — not an error.

### Hook Dispatch

`runClaudeHook` reads `LOOM_SESSION_ID` and `LOOM_BEADS_DIR` from env, parses stdin, calls `dispatchHookEvent`. Always returns `nil` (exit 0) to avoid breaking the agent workflow.

`dispatchHookEvent` → `mapEventToEntry` → `store.AppendTranscript`:

| HookEventType | role | type |
|---------------|------|------|
| `HookSessionStart` | `system` | `session_start` |
| `HookTurnStart` | `user` | `text` |
| `HookTurnEnd` | `assistant` | `turn_end` |
| `HookSessionEnd` | `system` | `session_end` |
| `HookSubagentStart` | `system` | `system/subagent_start` |
| `HookSubagentEnd` | `system` | `system/subagent_end` |

### Performance

Hook Cobra commands set `PersistentPreRunE` to a no-op, bypassing `ResolveAndSetBackend()` and `DefaultDeps()`. Critical: hooks fire on every Claude turn.

---

## 3. Session Store

### File: `internal/sessions/store.go`

`Store` wraps a directory path (`<beadsDir>/sessions/`).

**CreateSession:**
1. Generates session ID via `GenerateSessionID`
2. `MkdirAll` session directory
3. Writes `prompt.txt`, `metadata.json` (atomic via temp+rename)
4. Appends running record to `index.jsonl` (flock)

**AppendTranscript:**
1. Validates session ID (rejects path separators, traversal)
2. Verifies resolved path is under store directory
3. Opens `transcript.jsonl` in append mode
4. Acquires exclusive `flock(2)` before writing
5. Auto-assigns monotonic `Seq` via counter file

**Concurrency:** Both `transcript.jsonl` and `index.jsonl` use POSIX `flock(2)` for exclusive write access. Multiple hook processes from concurrent Claude turns safely append to the same transcript.

---

## 4. Session Lifecycle

Three call sites follow an identical pattern:

### Automode loop (`internal/cli/automode.go`)

```
sessStore.CreateSession(...)
SetActiveSessionEnv(beadsDir, sessionID)  // package-level globals
InvokeAgentNonInteractive(...)            // Claude subprocess with env vars
sess.Finalize(...)                        // exit code, diff stats, task ID
ClearActiveSessionEnv()
```

### Task command (`internal/cli/task.go`)

Same pattern, `Phase: "implementation"`.

### Plan command (`internal/cli/plan.go`)

Same pattern, `Phase: "planning"`.

### Environment Injection

`backend_session_env.go` maintains `sync.RWMutex`-protected globals:

- `SetActiveSessionEnv(beadsDir, sid)` — before agent launch
- `ClearActiveSessionEnv()` — after finalization
- `activeSessionEnvVars() []string` — returns `["LOOM_BEADS_DIR=...", "LOOM_SESSION_ID=..."]`

In `backend_claude.go`, `activeSessionEnvVars()` is appended to subprocess env at every invoke point. Claude inherits these, making them available to `loom hooks claude-code` subprocesses.

### Finalize

`Session.Finalize(opts)`:
1. Sets `Status` (completed/failed based on exit code)
2. Sets `EndedAt`, `DurationS`, diff stats, token usage
3. Writes `metadata.json` atomically
4. Writes `diff.patch` if provided
5. Appends finalized record to `index.jsonl` (second append; query deduplication picks authoritative)

---

## 5. Staleness Detection

### File: `internal/sessions/stale.go`

```go
const StaleSessionThreshold = 4 * time.Hour
```

Detection is lazy — fires on every `Query` call, not via background goroutine.

**Healing flow** (`query.go`):
1. After deduplication, each record tested with `isStale`
2. Stale records: `Status → StatusAborted`, `EndedAt → StartedAt + threshold`
3. `healStaleSession` writes `metadata.json` atomically, appends healed record to index
4. Re-filters results so healed sessions no longer appear under `StatusRunning`

---

## 6. Session Purge

**CLI:** `loom sessions clean --older-than 30d`

Supports Go duration strings and `"Nd"` day suffix shorthand. Calls `store.PurgeOlderThan(dur)`.

**Startup:** `loom serve` runs a background goroutine purging sessions older than 30 days.

**Implementation** (`purge.go`):
1. Calls `Query(Filter{})` — triggers healing of stale sessions
2. Skips `StatusRunning` sessions
3. `os.RemoveAll(sessDir)` for sessions past cutoff
4. Does not rewrite `index.jsonl` — orphan lines remain, ignored by subsequent queries

---

## 7. REST API

### File: `internal/webui/handlers_sessions.go`

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/api/tasks/{taskId}/sessions` | `handleListTaskSessions` | List sessions for task |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}` | `handleGetSession` | Session detail |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}/transcript` | `handleGetSessionTranscript` | Transcript entries |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}/diff` | `handleGetSessionDiff` | Raw diff (text/plain) |
| POST | `/api/sessions/notify` | `handleNotifySessionChange` | SSE broadcast for session status changes (loopback only) |

All return HTTP 503 when `sessStore` is nil. Input validated via `validTaskID` and `validSessionID` regexes (`[a-zA-Z0-9._-]+`).

**Note:** These are distinct from the terminal session history endpoints (`/api/issues/{issueId}/sessions`) which are Redis-backed and track terminal multiplexer sessions.

---

## 8. Frontend Architecture

### TypeScript Types

**File:** `internal/webui/frontend/src/types/session.ts`

```typescript
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

export interface SessionRecord {
  id: string;
  agent_name: string;
  backend: string;
  model?: string;
  phase?: string;
  status: SessionStatus;
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  input_tokens: number;
  output_tokens: number;
  estimated_cost_usd: number;
  files_changed: number;
  lines_added: number;
  lines_removed: number;
  files_touched?: string[];
  has_transcript: boolean;
  has_diff: boolean;
  is_active: boolean;
}

export interface TranscriptEntry {
  seq: number;
  ts: string;
  role: "user" | "assistant" | "system" | "tool";
  type: string;
  content?: string;
  tool_name?: string;
  tool_input?: string;
}
```

### API Client (`api/sessions.ts`)

| Function | Endpoint | Return |
|----------|----------|--------|
| `getTaskSessions(taskId)` | `GET /api/tasks/{taskId}/sessions` | `SessionRecord[]` |
| `getSession(taskId, sessionId)` | `GET .../sessions/{sessionId}` | `SessionRecord \| null` |
| `getSessionTranscript(taskId, sessionId)` | `GET .../transcript` | `TranscriptEntry[]` |
| `getSessionDiff(taskId, sessionId)` | `GET .../diff` | `string \| null` |

404 → null/empty (not thrown). All IDs passed through `encodeURIComponent`.

### Data Fetching Hooks

| Hook | Polling | Strategy |
|------|---------|----------|
| `useTaskSessions(taskId)` | 10s normal, 3s when any session `is_active` | Adaptive `setTimeout` chain + SSE `session_change` refetch |
| `useSessionTranscript(taskId, sid, isActive)` | 3s when active, one-shot when inactive | `setInterval` gated by `isActive` |
| `useSessionDiff(taskId, sid, enabled)` | One-shot | Lazy — only when Diff tab shown and `has_diff` |
| `useIssueSessionMap()` | SSE-triggered | Debounced refetch on `terminal_session_change` |

### Component Hierarchy

```
IssueDetailPanel
└── SessionsTab (taskId)
    ├── SessionTimeline (sessions, selectedId, onSelect, isLoading)
    │   └── SessionTimelineRow × N (session, isSelected, onClick)
    └── SessionDetailView (taskId, session)
        ├── metadata summary (model, exit code, files, lines)
        ├── files touched <details> (collapsible)
        ├── inner tab bar [Transcript | Diff]
        ├── transcript pane (entries with role labels)
        └── diff pane
            └── CodeMirrorEditor (language="diff", readOnly)
```

The Sessions tab is non-closable (`closable: false`), always present alongside the Details tab. Tab restore logic enforces its presence even after persisted state reload.

### CodeMirror Integration (`components/CodeMirrorEditor/`)

Thin React wrapper around CodeMirror 6. Created once on mount, updated via `Compartment` hot-reconfiguration (no teardown on prop change). Supports `go`, `json`, `yaml`, `markdown`, and `diff` languages. The diff pane uses `codemirror-lang-diff` for unified diff syntax highlighting.

### Session Data Update Strategy

Session data uses **SSE push with polling fallback**. Agent processes call `NotifyWebUI` which sends `POST /api/sessions/notify` to the web UI server. The `handleNotifySessionChange` handler broadcasts a `session_change` SSE event to all connected clients. The `useTaskSessions` hook subscribes to these events for immediate refetch, with adaptive polling (3s/10s) as fallback when SSE is unavailable.

---

## 9. Component Dependency Graph

```
loom auto / loom task / loom plan
  │
  ├── sessions.NewStore(beadsDir)
  ├── CreateSession → metadata.json + index.jsonl (running)
  ├── SetActiveSessionEnv → package globals
  ├── backend_claude.go → activeSessionEnvVars() → subprocess env
  │                           │
  │                     Claude Code hooks (per turn)
  │                           │
  │                     loom hooks claude-code <name>
  │                           │
  │                     ParseClaudeHookInput → HookEvent
  │                           │
  │                     dispatchHookEvent → mapEventToEntry
  │                           │
  │                     store.AppendTranscript (flock)
  │
  ├── ClearActiveSessionEnv
  └── sess.Finalize → metadata.json (final) + diff.patch + index.jsonl

loom serve
  └── setupRoutes
        ├── handleListTaskSessions      GET /api/tasks/{taskId}/sessions
        ├── handleGetSession            GET .../sessions/{sessionId}
        ├── handleGetSessionTranscript  GET .../transcript
        └── handleGetSessionDiff        GET .../diff

Frontend
  └── SessionsTab → useTaskSessions (polling)
        ├── SessionTimeline
        └── SessionDetailView
              ├── useSessionTranscript (polling when active)
              ├── useSessionDiff (lazy one-shot)
              └── CodeMirrorEditor (diff syntax)
```

---

## 10. Security

- **Path traversal**: `AppendTranscript` rejects session IDs with `/`, `\`, `..`. Verifies resolved path is under store directory. `validSessionID` regex restricts to `[a-zA-Z0-9._-]+`.
- **Hook exit code**: Always 0 regardless of errors — failures written to stderr only.
- **Concurrency**: `flock(2)` on both `transcript.jsonl` and `index.jsonl`. No TOCTOU gap — index enumerated from `index.jsonl`, not directory listing.

---

## 11. File Map

### Backend

| Path | Description |
|------|-------------|
| `internal/sessions/types.go` | `SessionRecord`, `SessionMetadata`, `TranscriptEntry`, `Filter`, `CreateOptions`, `FinalizeOptions` |
| `internal/sessions/id.go` | `GenerateSessionID` |
| `internal/sessions/session.go` | `Session` handle type |
| `internal/sessions/store.go` | `Store`, `NewStore`, `CreateSession`, `AppendTranscript`, `LoadMetadata`, `LoadTranscript` |
| `internal/sessions/finalize.go` | `Session.Finalize`, `appendIndex` |
| `internal/sessions/query.go` | `Store.Query`, `SessionsByTask`, `healStaleSession` |
| `internal/sessions/stale.go` | `StaleSessionThreshold`, `isStale` |
| `internal/sessions/purge.go` | `Store.PurgeOlderThan` |
| `internal/cli/hooks_types.go` | `HookEventType`, `HookEvent`, raw payload structs |
| `internal/cli/hooks_parse.go` | `ParseClaudeHookInput` |
| `internal/cli/hooks_dispatch.go` | `dispatchHookEvent`, `mapEventToEntry` |
| `internal/cli/hooks_install.go` | `InstallClaudeHooks`, `UninstallClaudeHooks` |
| `internal/cli/hooks_cmd.go` | Cobra commands, `runClaudeHook` |
| `internal/cli/backend_session_env.go` | `SetActiveSessionEnv`, `ClearActiveSessionEnv`, `activeSessionEnvVars` |
| `internal/cli/automode.go` | Session lifecycle in auto mode loop |
| `internal/cli/task.go` | Session lifecycle in task command |
| `internal/cli/plan.go` | Session lifecycle in plan command |
| `internal/cli/sessions_cmd.go` | `loom sessions clean` CLI |
| `internal/webui/handlers_sessions.go` | REST handlers for session endpoints |

### Frontend

| Path | Description |
|------|-------------|
| `frontend/src/types/session.ts` | `SessionRecord`, `TranscriptEntry`, response types |
| `frontend/src/api/sessions.ts` | API client for session endpoints |
| `frontend/src/hooks/useTaskSessions.ts` | Adaptive polling hook for session list |
| `frontend/src/hooks/useSessionTranscript.ts` | Polling hook for transcript |
| `frontend/src/hooks/useSessionDiff.ts` | Lazy one-shot diff fetch |
| `frontend/src/hooks/useIssueSessionMap.ts` | SSE-triggered active session map |
| `frontend/src/components/IssueDetailPanel/SessionsTab.tsx` | Container with timeline + detail |
| `frontend/src/components/IssueDetailPanel/SessionTimeline.tsx` | Session list sorted newest-first |
| `frontend/src/components/IssueDetailPanel/SessionTimelineRow.tsx` | Individual session row |
| `frontend/src/components/IssueDetailPanel/SessionDetailView.tsx` | Transcript + diff viewer |
| `frontend/src/components/CodeMirrorEditor/CodeMirrorEditor.tsx` | CodeMirror 6 wrapper |
