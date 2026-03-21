# Backend Infrastructure Architecture (Epic 3p2t0 + Diff Viewer)

## Overview

The backend infrastructure encompasses the core abstractions and systems that power the loom CLI: issue tracking interfaces with pluggable backends (beads CLI vs fleet-db RPC), task routing with repo affinity, the web UI server with its middleware stack, SSE real-time hub, diff viewer pipeline, session/tab persistence stores, daemon lifecycle management, auto-mode polling, and structured logging via the observability event system.

---

## 1. IssueTracker Interface and Implementations

### Interface Definition

**File:** `internal/cli/issue_backend.go`

```go
type IssueTracker interface {
    // Query operations
    Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)
    List(ctx context.Context, opts ListOpts) ([]BdIssue, error)
    Blocked(ctx context.Context) ([]BdIssue, error)
    Stats(ctx context.Context) (*BdStats, error)
    GetIssue(ctx context.Context, id string) (*BdIssue, error)
    GetIssueText(ctx context.Context, id string) (string, error)

    // Mutation operations
    UpdateIssue(ctx context.Context, id string, opts UpdateOpts) error
    UpdateExternalRef(ctx context.Context, id, ref string) error
    CloseIssue(ctx context.Context, id, reason string) error

    // Metadata
    BackendName() string
}
```

### ReadyOpts / ListOpts / UpdateOpts

```go
type ReadyOpts struct {
    ParentID    string   // filter by parent epic ID
    Limit       int      // max results (0 = backend default)
    Labels      []string // e.g. ["repo:frontend"]
    SourceRepos []string // maps to --source-repos
}

type ListOpts struct {
    Status   string
    Assignee string
    Type     string
    ParentID string
    Limit    int
}

type UpdateOpts struct {
    Status   string  // empty = don't change
    Assignee *string // nil = don't change, pointer to "" = clear
    Design   string  // empty = don't change
    Claim    bool    // atomically claim the issue
}
```

### Package-Level Singleton

`defaultTracker()` uses double-checked locking with `sync.RWMutex`. Returns `trackerInst` if set, else initializes from `defaultDeps.Tracker`, else falls back to an ephemeral `bdBackend` instance.

### bdBackend Implementation

**File:** `internal/cli/bd_backend.go`

Shell-out implementation that invokes the `bd` CLI binary. Each method constructs a `bd` command (e.g., `bd ready --source-repos repo-a,repo-b --limit 10`), runs it via `BDRunner.Run()`, and parses the JSON output.

### fleetDBBackend Implementation

**Files:** `internal/cli/fleetdb_backend.go`, `internal/cli/fleetdb_backend_tracker.go`

RPC-based implementation that communicates with a fleet-db server. Uses `fleetDBClient` interface for testability. Converts between `BdIssue` and fleet-db's native types.

---

## 2. Task Routing and Repo Affinity

**File:** `internal/cli/task_router.go`

### RoleConstraints

```go
type RoleConstraints struct {
    Role            string
    PathPatterns    []string // glob patterns for file-scope limits
    MaxPriority     int      // only take tasks at this priority or higher
    TaskFilter      string   // "plan", "implement", or "any"
    Skills          []string // role-specific skill tags
    SourceRepos     []string // repo affinity filter
}
```

### MatchTask

`MatchTask(task BdIssue, constraints RoleConstraints) TaskMatch` scores a task:
- **Priority check**: rejects if `task.Priority > constraints.MaxPriority`
- **Task filter check**: uses `IsAvailableForPlanning`, `IsAvailableForImplementation`, or `IsAvailableForAny`
- **Repo affinity**: bonus score when `task.SourceRepo` matches any of `constraints.SourceRepos`

`SelectBestTask(tasks []BdIssue, constraints RoleConstraints) *BdIssue` returns the highest-scoring match.

### AgentEntryFromEnv()

Reconstructs routing context from environment variables:
```go
func AgentEntryFromEnv() AgentEntry {
    var ae AgentEntry
    if v := os.Getenv("LOOM_SOURCE_REPOS"); v != "" {
        ae.SourceRepos = strings.Split(v, ",")
    }
    return ae
}
```

### Task Filters

**File:** `internal/cli/taskfilter.go`

| Function | Logic |
|----------|-------|
| `IsAvailableForPlanning` | status=open, no design, not blocked |
| `IsAvailableForImplementation` | status=open, has design, not blocked |
| `IsAvailableForAny` | status=open, not blocked |
| `IsEpic` | issue_type=epic |
| `IsOpen` | status in {open, in_progress, review} |
| `HasUnclosedBlockers` | any dependency with open status |

---

## 3. Web UI Server Architecture

**File:** `internal/webui/server.go`

### ServerConfig

```go
type ServerConfig struct {
    Port          int
    LoomServerURL string        // upstream loom server for proxy
    Pool          *pool.Pool    // beads connection pool
    MultiPool     *pool.MultiPool
    GitOps        GitOps
    FileOps       FileOps
    Logger        *slog.Logger
    RedisAddr     string
    EventsDir     string
    AgentDir      string
    DevMode       bool
}
```

### Startup Sequence (`StartServer`)

1. Initialize Redis client (if `RedisAddr` set)
2. Create `sessionhistory.Store`, `issuetabs.Store`, `tabmeta.Store`
3. Create `SSEHub`, start `hub.Run()` goroutine
4. Create `MultiWorkspaceSubscriber` (per-workspace mutation polling)
5. Create `TerminalManager`
6. Create metrics cache (30s TTL)
7. Call `setupRoutes(mux, ...)` — registers all handlers
8. Wrap mux with `AuthMiddleware` then `CORSMiddleware` then `RequestLogMiddleware`
9. Start `http.Server` with graceful shutdown (30s timeout)

### Middleware Stack

```
Request -> CORSMiddleware -> AuthMiddleware -> RequestLogMiddleware -> Router
```

- **CORSMiddleware**: allowlist-based origin check, preflight handling
- **AuthMiddleware**: API key validation from `X-API-Key` header or `?api_key` param. SSE and static file routes exempt.
- **RequestLogMiddleware**: structured logging with method, path, status, duration via `slog.Logger`

---

## 4. Route Registration

**File:** `internal/webui/routes.go`

### Issue Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/issues` | `handleListIssues` |
| GET | `/api/issues/graph` | `handleGetGraphData` |
| GET | `/api/issues/{id}` | `handleGetIssue` |
| POST | `/api/issues` | `handleCreateIssue` |
| PATCH | `/api/issues/{id}` | `handleUpdateIssue` |
| GET | `/api/issues/{id}/events` | `handleGetEvents` |
| POST | `/api/issues/{id}/comments` | `handleAddComment` |
| POST | `/api/issues/{id}/dependencies` | `handleAddDependency` |
| DELETE | `/api/issues/{id}/dependencies/{depId}` | `handleRemoveDependency` |
| GET | `/api/issues/{id}/git/diff-stat` | `handleGetIssueDiffStat` |
| GET | `/api/ready` | `handleListReady` |
| GET | `/api/stats` | `handleGetStats` |

### Issue Tab Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/issues/{id}/tabs` | `handleGetIssueTabs` |
| PUT | `/api/issues/{id}/tabs` | `handleSaveIssueTabs` |
| DELETE | `/api/issues/{id}/tabs` | `handleDeleteIssueTabs` |

### Agent Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/agents` | `handleListAgents` |
| GET | `/api/agents/{name}/status` | `handleGetAgentStatus` |
| GET | `/api/agents/{name}/git/status` | `handleGitStatus` |
| POST | `/api/agents/{name}/git/push` | `handleGitPush` |
| POST | `/api/agents/{name}/git/pull` | `handleGitPull` |
| POST | `/api/agents/{name}/git/sync` | `handleGitSync` |
| POST | `/api/agents/{name}/git/pr` | `handleGitPR` |
| POST | `/api/agents/{name}/git/reset` | `handleGitReset` |
| POST | `/api/agents/{name}/git/target-update` | `handleGitTargetUpdate` |
| POST | `/api/agents/git/push-all` | `handleGitPushAll` |

### Diff Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/agents/{name}/diff/commits` | `handleDiffCommits` |
| GET | `/api/agents/{name}/diff/files` | `handleDiffFiles` |
| GET | `/api/agents/{name}/diff/file` | `handleDiffFile` |

### File Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/agents/{name}/files/tree` | `handleFileTree` |
| GET | `/api/agents/{name}/files` | `handleFileRead` |
| PUT | `/api/agents/{name}/files` | `handleFileWrite` |

### Session Audit Trail Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/tasks/{taskId}/sessions` | `handleListTaskSessions` |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}` | `handleGetSession` |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}/transcript` | `handleGetSessionTranscript` |
| GET | `/api/tasks/{taskId}/sessions/{sessionId}/diff` | `handleGetSessionDiff` |
| POST | `/api/sessions/notify` | `handleNotifySessionChange` |

### Log Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/agents/{name}/logs` | `handleGetAgentLog` |
| GET | `/api/tasks/{id}/logs` | `handleListTaskPhases` |
| GET | `/api/tasks/{id}/logs/{phase}` | `handleGetTaskLog` |

### Other Routes

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/events` | `handleSSE` (SSE hub) |
| GET | `/api/health` | `handleHealth` |
| GET | `/api/metrics` | `handleMetrics` |
| GET | `/api/backends` | `handleListBackends` |
| GET | `/api/config/backend` | `handleGetBackendConfig` |
| PATCH | `/api/config/backend` | `handleUpdateBackendConfig` |
| GET | `/api/editors` | `handleListEditors` |
| POST | `/api/editors/open` | `handleOpenEditor` |
| GET/POST | `/api/loom/...` | `newLoomProxy(loomServerURL)` |
| `*` | `/` | SPA catch-all (embedded FS or disk in dev mode) |

---

## 5. SSE Hub Architecture

**File:** `internal/webui/sse.go`

### Internal Structure

```go
type SSEHub struct {
    clients    map[*SSEClient]bool    // protected by mu (RWMutex)
    register   chan *SSEClient        // buffered(16)
    unregister chan *SSEClient        // buffered(16)
    broadcast  chan *MutationPayload  // buffered(256)
    done       chan struct{}
    retryQueue []*MutationPayload     // overflow buffer (cap 1024)
    droppedCount int64               // atomic
}

type SSEClient struct {
    id          int64
    send        chan *MutationPayload  // buffered(64)
    done        chan struct{}
    lastSince   int64
    sourceRepos []string              // empty = all repos
}
```

### Run Loop

`hub.Run()` is a single-goroutine event loop:
- `register`: adds client to `clients` map
- `unregister`: removes client, closes `client.send`
- `broadcast`: fans out to all clients with per-client source repo filtering (non-blocking send)
- `retryTicker` (100ms): drains overflow queue
- `done`: closes all client channels and exits

### Overflow Handling

`Broadcast()` tries non-blocking send to `hub.broadcast`. On failure, appends to `retryQueue` (cap 1024). Beyond that, mutations are dropped and counted in `droppedCount`.

### SSE Handler

`handleSSE(hub, getMutationsSince)`:
1. Sets SSE headers (`text/event-stream`, `no-cache`, `X-Accel-Buffering: no`)
2. Parses `Last-Event-ID` and `?since` for reconnection catch-up
3. Parses `?source_repos` for per-client filtering
4. Sends catch-up mutations, then `retry: 5000` and `connected` event
5. Streams from `client.send`, sends heartbeat comment every 30s
6. Exits on client disconnect or server shutdown

### MultiWorkspaceSubscriber

Creates per-workspace `DaemonSubscriber` goroutines. Each polls the daemon's mutation log and broadcasts with workspace ID tag. Provides `GetMutationsSince(since int64)` for reconnection catch-up.

---

## 6. Diff Viewer Pipeline

### Backend: GitOps Interface

**File:** `internal/webui/gitops.go`

```go
type GitOps interface {
    ResolveMergeBase(worktreePath, branch string) (string, error)
    DiffCommits(worktreePath, mergeBase string, limit int) ([]DiffCommitResult, error)
    DiffFiles(worktreePath, from, to string) ([]DiffFileResult, error)
    DiffFilePatch(worktreePath, from, to, path string) (*DiffFilePatchResult, error)
    DiffStat(worktreePath, branch string) (*DiffStatResult, error)
    // ...other git operations
}
```

`DiffFilePatchResult`: `Patch string`, `IsBinary bool`, `IsTooLarge bool`, `Additions int`, `Deletions int`.

### Backend: Diff Handlers

**File:** `internal/webui/handlers_diff.go`

All responses use `diffResponse{Success bool, Data interface{}, Error string}`.

- **`handleDiffCommits`** (`GET /api/agents/{name}/diff/commits`): resolves agent worktree, computes merge-base if no `?from`, returns commit list
- **`handleDiffFiles`** (`GET /api/agents/{name}/diff/files`): requires `?to=<ref>`, validates against `validGitRef` regex, returns changed file list with status (M/A/D/R)
- **`handleDiffFile`** (`GET /api/agents/{name}/diff/file`): requires `?path` (validated via `validateDiffPath` — rejects `..` traversal) and `?to`, returns unified diff patch

**Security**: `validateDiffPath` rejects absolute paths and `..` traversal. `validGitRef` regex prevents injection.

### Frontend: API Layer

**File:** `internal/webui/frontend/src/api/diff.ts`

- `fetchDiffCommits(agentName, limit?)` -> `DiffCommit[]`
- `fetchDiffFiles(agentName, to, from?)` -> `DiffFile[]`
- `fetchDiffFile(agentName, path, to, from?)` -> `DiffFilePatch`

### Frontend: useDiff Hook

**File:** `internal/webui/frontend/src/hooks/useDiff.ts`

Manages full diff viewer state:
- **File list**: fetched on mount via `fetchDiffFiles(agentName, "HEAD")`
- **Patch cache**: `Map<string, DiffFilePatch>` with deduplication via `inFlightPatchesRef`
- **Viewed files**: `Set<string>` toggled by `markViewed(path)`
- **Summary stats**: derived via `useMemo` (filesChanged, additions, deletions)

### Frontend: DiffFileViewer

**File:** `internal/webui/frontend/src/components/AgentDetailPanel/DiffFileViewer.tsx`

`parsePatch(patchString)` parses unified diff into `Hunk[]`. Each line classified as `"add"`, `"del"`, `"context"`, or `"hunk"`. Renders as `<pre>` with `<div data-type="add|del|context|hunk">` per line.

### CodeMirrorEditor

**File:** `internal/webui/frontend/src/components/CodeMirrorEditor/CodeMirrorEditor.tsx`

CM6-based editor with `Compartment`-based hot-swappable configuration:
- Language support: `go`, `json`, `yaml`/`yml`, `markdown`/`md`, `diff` (via `codemirror-lang-diff`)
- Compartments for language, readOnly, lineNumbers — swap without destroying the view
- `ResizeObserver` triggers debounced `view.requestMeasure()`
- `searchOpen` prop controls built-in search panel

---

## 7. Session and Tab Persistence

### sessionhistory.Store

**File:** `internal/webui/sessionhistory/store.go`

Redis key: `issue:sessions:{issueId}`. No TTL — persists indefinitely.

```go
type SessionRecord struct {
    ID, SessionName, IssueID, Backend string
    Status string          // "active" | "completed"
    Launcher string        // "user" | "start-work"
    StartedAt time.Time
    EndedAt *time.Time
    ScrollbackPath string
}
```

Operations: `Add`, `List` (sorted by StartedAt desc), `Complete` (sets status, EndedAt, ScrollbackPath).

### issuetabs.Store

**File:** `internal/webui/issuetabs/store.go`

Redis key: `issue:tabs:{issueId}`. TTL: 24 hours, refreshed on write.

```go
type IssueTab struct {
    ID string              // "details", "logs", "terminal-{session}"
    Type string            // "details" | "logs" | "terminal"
    Label, SessionName string
    SortOrder int
}
```

`ValidateAndFilter(state, activeSessions)` removes terminal tabs whose sessions no longer exist, falling back active tab to `"details"`.

### tabmeta.Store

Redis-backed terminal tab metadata per workspace. Key: `terminal:meta:{workspace}:{session}`. Supports `MigrateLegacyKeys()` for upgrade from pre-workspace format.

---

## 8. Daemon Lifecycle and Agent Management

### Core Types

**File:** `internal/cli/daemon.go`

```go
type Daemon struct {
    config       *DaemonConfig
    agents       []*AgentProcess    // protected by agentsMu (RWMutex)
    shutdown     chan struct{}
    epicAssigner *EpicAssigner
    concurrency  *ConcurrencyTracker
    eventBus     events.Emitter
    repos        []RepoConfig
    configHash   string             // SHA-256 of current config
    reconcileMu  sync.RWMutex
}

type AgentProcess struct {
    entry        AgentEntry
    cmd          *exec.Cmd
    pid          int
    restartCount int
    stopCh       chan struct{}      // per-agent stop signal
    done         chan struct{}      // closed when superviseAgent exits
    // ...
}
```

### Startup: `Daemon.Start()`

1. `resetWorktreeBranches()` — moves all worktrees to default branches, creates WIP commits for dirty trees
2. Computes initial `configHash`
3. Starts `healthChecker()` goroutine (30s interval)
4. Starts `configReconciler()` goroutine
5. For each agent: creates `stopCh`/`done` channels, starts `superviseAgent()` goroutine

### superviseAgent Loop

Per-agent goroutine cycle:
1. Check shutdown/stop signals
2. `concurrency.Acquire(role)` — blocks at role limit
3. `recoverAgent(ap, 0)` — pre-flight recovery
4. `epicAssigner.AssignWorktree()` — atomic epic assignment
5. `EnsureWorktreeBranch()` — checkout epic or agent branch
6. `spawnAgent(ap)` — `buildCommand()` then `cmd.Start()`
7. `waitForAgent(ap)` — blocks on `cmd.Wait()`
8. `classifyAgentExit()` — sets `lastError`, `lastNoWork`
9. `handleAgentCheckpoint()` — save/clear checkpoint
10. `recoverAgent(ap, exitCode)` — post-mortem cleanup
11. `EnsureEpicPR()` — non-fatal PR creation
12. Release epic and concurrency
13. `handleEpicTransition()` — check exhaustion, reassign
14. Check shutdown, try fallback backend, check restart policy
15. `computeBackoff()` — exponential backoff
16. Interruptible sleep

### Subprocess Construction

**File:** `internal/cli/daemon_spawn.go`

Process group: `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` — enables group kill.

Environment propagated: `BD_ACTOR`, `LOOM_WORKTREE_PATH`, `LOOM_EVENTS_DIR`, `LOOM_SOURCE_REPOS`, `LOOM_ROLE`, `LOOM_ALLOWED_TOOLS`, `LOOM_DENIED_TOOLS`, etc.

### Agent Stop

`stopAgent(ap)`:
1. `syscall.Kill(-pid, SIGTERM)` to entire process group
2. Poll for 5 seconds
3. `syscall.Kill(-pid, SIGKILL)` if still running

### Health Checker

**File:** `internal/cli/daemon_health.go`

Runs every 30 seconds:
- PID alive check via `lockfile.IsProcessRunning()`
- Stale lock detection
- **Output watchdog**: kills hung processes when log file unmodified beyond `OutputTimeout`
- Emits `health_check` event

---

## 9. Hot Reload: Config Reconciler

**File:** `internal/cli/daemon_reconciler.go`

- Uses `fsnotify.NewWatcher()` on project and global config directories
- 500ms debounce for `Write|Create|Rename` events on `loom.yaml` or `config.yaml`
- Fallback: 30s polling when fsnotify unavailable (NFS, containers)

`reloadAndReconcile()`:
1. Load config outside lock (file I/O)
2. Compute SHA-256 hash, compare under lock
3. `diffAgents(old, new)` using `reflect.DeepEqual`
4. `drainAgent()` for removed/modified agents (blocks)
5. `addAgent()` for added/modified agents (starts immediately)
6. Emits `config_reloaded` event

---

## 10. Auto-Mode Poller

**File:** `internal/cli/automode.go`

### Per-Iteration Sequence

```
1. UpdateLockState("idle")
2. Check shutdown signal
3. Check MaxTasks limit
4. hasAvailableTasks() -> if false: check idle timeout, sleep, continue
5. UpdateLockState("active")
6. captureHEADRef() — for post-run diff stats
7. Generate prompt
8. CreateSession() — pre-create session record
9. InvokeAgentNonInteractive(worktreePath, prompt, ...)
10. Finalize session (exitCode, taskID, DiffStats)
11. If error: increment ConsecutiveErrors, backoff
12. If success, no task claimed: increment ConsecutiveNoProgress, exponential backoff
13. If success, task claimed: increment TasksCompleted, emit task_completed
```

Exit conditions: `MaxTasks` reached, `ConsecutiveErrors >= 3`, `ConsecutiveNoProgress >= 3`, `IdleTimeout` exceeded.

---

## 11. Structured Logging and Observability

### Logging

Uses `log/slog` throughout. `RequestLogMiddleware(logger)` emits per-request structured logs. All event emission is best-effort.

### Observability Events

**File:** `internal/cli/serve_observability.go`

JSONL event files in `daemon.events_dir`. Event types:

| Event Type | Emitted By | Data |
|------------|-----------|------|
| `agent_started` | `spawnAgent` | PID |
| `agent_stopped` | `waitForAgent` | PID, ExitCode |
| `agent_restarted` | `superviseAgent` | PID, RestartCount |
| `epic_assigned` | `superviseAgent` | EpicID |
| `task_completed` | `RunAutoModeLoop` | TaskID, Duration, DiffStats |
| `task_failed` | `RunAutoModeLoop` | TaskID, Error |
| `config_reloaded` | `reloadAndReconcile` | Added, Removed, Modified |
| `health_check` | `healthChecker` | AgentCount, HealthyCount |

Metrics replayed from disk into `MetricsStore` (30s TTL cache). `GET /api/metrics` returns SSE hub stats, fleet state, and claim metrics alongside the snapshot.

### Error Response Pattern

All handlers use:
- `respondJSON(w, status, v)` — sets Content-Type, writes status, encodes JSON
- `respondError(w, status, message)` — wraps in `{"error": message}`

`MaxListLimit` (1000) caps list queries. `maxRequestBody` (1MB) enforced via `http.MaxBytesReader`.

---

## 12. File Map

### Core Issue Tracking

| File | Responsibility |
|------|---------------|
| `internal/cli/issue_backend.go` | `IssueTracker` interface, `ReadyOpts`, `ListOpts`, `UpdateOpts` |
| `internal/cli/bd_backend.go` | `bdBackend` — bd CLI shell-out implementation |
| `internal/cli/fleetdb_backend.go` | `fleetDBBackend` struct, type conversions |
| `internal/cli/fleetdb_backend_tracker.go` | `IssueTracker` methods for fleetDBBackend |
| `internal/cli/task_router.go` | `RoleConstraints`, `TaskMatch`, `MatchTask`, `SelectBestTask` |
| `internal/cli/taskfilter.go` | `IsAvailableForPlanning`, `IsAvailableForImplementation`, etc. |
| `internal/cli/task.go` | `BdIssue`, `BdStats`, `Dependency` types |

### Daemon Infrastructure

| File | Responsibility |
|------|---------------|
| `internal/cli/daemon.go` | `Daemon`, `AgentProcess`, `NewDaemon`, `Start`, `Stop` |
| `internal/cli/daemon_spawn.go` | `buildCommand`, `spawnAgent`, `waitForAgent` |
| `internal/cli/daemon_health.go` | `stopAgent`, `healthChecker`, output watchdog |
| `internal/cli/daemon_hotreload.go` | `drainAgent`, `addAgent` |
| `internal/cli/daemon_reconciler.go` | `configReconciler`, `reloadAndReconcile`, `diffAgents` |
| `internal/cli/daemon_config.go` | `DaemonConfig`, `AgentEntry`, `RestartPolicy` |
| `internal/cli/daemon_epic.go` | `EpicAssigner` |
| `internal/cli/daemon_branch.go` | `EnsureWorktreeBranch` |
| `internal/cli/daemon_backend.go` | Backend failover |
| `internal/cli/daemon_restart.go` | `shouldRestart`, `computeBackoff` |
| `internal/cli/daemon_classify.go` | `classifyAgentExit`, `handleEpicTransition` |
| `internal/cli/daemon_repos.go` | `ResolveAgentTarget`, repo resolution |
| `internal/cli/concurrency.go` | `ConcurrencyTracker` |

### Auto Mode

| File | Responsibility |
|------|---------------|
| `internal/cli/automode.go` | `AutoModeOptions`, `RunAutoModeLoop` |
| `internal/cli/automode_poller.go` | `fetchReadyIssues`, `BuildRouterTaskCheck`, `adaptivePoller` |

### Web UI Server

| File | Responsibility |
|------|---------------|
| `internal/webui/server.go` | `ServerConfig`, `StartServer`, startup/shutdown |
| `internal/webui/routes.go` | `setupRoutes`, all route registration |
| `internal/webui/sse.go` | `SSEHub`, `SSEClient`, `MutationPayload`, `handleSSE` |
| `internal/webui/auth.go` | `AuthMiddleware`, API key management |
| `internal/webui/cors.go` | `CORSMiddleware` |
| `internal/webui/respond.go` | `respondJSON`, `respondError` |
| `internal/webui/gitops.go` | `GitOps` interface |
| `internal/webui/fileops.go` | `FileOps` interface |

### Diff Viewer

| File | Responsibility |
|------|---------------|
| `internal/webui/handlers_diff.go` | `handleDiffCommits`, `handleDiffFiles`, `handleDiffFile` |
| `internal/webui/handlers_issue_diff_stat.go` | `handleGetIssueDiffStat` |
| `frontend/src/api/diff.ts` | `fetchDiffCommits`, `fetchDiffFiles`, `fetchDiffFile` |
| `frontend/src/hooks/useDiff.ts` | `useDiff` hook |
| `frontend/src/components/AgentDetailPanel/DiffTab.tsx` | Diff view orchestrator |
| `frontend/src/components/AgentDetailPanel/DiffFileViewer.tsx` | Hunk parser and renderer |
| `frontend/src/components/CodeMirrorEditor/CodeMirrorEditor.tsx` | CM6 editor with diff language |

### Session and Tab Persistence

| File | Responsibility |
|------|---------------|
| `internal/webui/sessionhistory/store.go` | `SessionRecord`, `Store` (Redis, no TTL) |
| `internal/webui/issuetabs/store.go` | `IssueTab`, `Store` (Redis, 24h TTL) |
| `internal/webui/handlers_session_history.go` | Session history endpoints |
| `internal/webui/handlers_issue_tabs.go` | Issue tab CRUD |

### Logging and Observability

| File | Responsibility |
|------|---------------|
| `internal/cli/serve_observability.go` | Metrics cache, event replay |
| `internal/webui/handlers_logs.go` | Agent/task log endpoints |
| `internal/webui/log_streamer.go` | Log tail utilities |
