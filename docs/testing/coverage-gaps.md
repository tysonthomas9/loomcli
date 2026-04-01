# Test Coverage Gaps

Untested scenarios identified by comparing production code against test files, organized by severity.

**Last analyzed**: 2026-02-11

---

## Table of Contents

1. [Critical Gaps](#critical-gaps)
2. [Go Backend Gaps](#go-backend-gaps)
3. [Frontend Gaps](#frontend-gaps)
4. [Summary](#summary)

---

## Critical Gaps

The highest-priority untested scenarios that could cause security issues, data loss, or production outages.

### 1. WebUI: Comment Handler - ZERO TESTS

**File**: `internal/webui/handlers_comments.go`

No test file exists. The entire comment creation API is untested:
- `handleAddComment()` - input validation, text length limits (64KB), empty text, missing issue ID
- Author field defaulting to "web-ui"
- RPC error handling and pool timeout scenarios

**Risk**: Comment creation bugs silently break a core user-facing feature.

### 2. WebUI: Log Streaming Handlers - ZERO TESTS

**File**: `internal/webui/handlers_logs.go`

Five handlers with zero test coverage:
- `handleGetAgentLog()` - agent log retrieval
- `handleAgentLogStream()` - SSE log streaming
- `handleListTaskPhases()` - task phase listing
- `handleGetTaskLog()` - task log retrieval
- `handleTaskLogStream()` - SSE task log streaming

**Risk**: **Path traversal prevention is untested** - malformed agent/task IDs could escape the log directory. FSNotify watcher and SSE debouncing logic are also uncovered.

### 3. WebUI: Log Streamer - Mostly Untested

**File**: `internal/webui/log_streamer.go`

Only basic file reading is tested. Missing:
- `NewLogStreamer()` - fsnotify watcher creation
- `LogStreamer.Stream()` - real-time file tailing via SSE
- File rotation, debouncing (50ms), heartbeats (30s), 32KB chunk reads
- Concurrent access to mutex
- File deleted or permissions changed during streaming

**Risk**: Real-time log streaming has complex concurrency edge cases.

### 4. RPC: Socket Path Ownership Validation

**File**: `internal/rpc/socket_path.go`

- `EnsureSocketDir()` line 119-121: ownership mismatch check not tested
- Race condition in `Mkdir` (line 91-92) when multiple processes create directory

**Risk**: Security vulnerability - symlink attack on `/tmp/beads-*` directories could redirect daemon communication.

### 5. RPC: Response Size Limit

**File**: `internal/rpc/client.go`

- `executeWithTimeout()`: no test for responses exceeding `maxClientMessageSize` (10MB)
- Write failure paths during request send (lines 253-260)

**Risk**: Malicious or malfunctioning daemon could cause memory exhaustion.

### 6. Frontend: API Logs Module - ZERO TESTS

**File**: `internal/webui/frontend/src/api/logs.ts`

Three functions with no test coverage:
- `getTaskLogPhases(taskId)` - 404 handling, auth token injection
- `getAgentLogStreamUrl(agentName)` - URL encoding of special chars
- `getTaskLogStreamUrl(taskId, phase)` - multiple query parameters

**Risk**: Log viewer broken without detection.

### 7. Frontend: Issues API - 4 Untested Functions

**File**: `internal/webui/frontend/src/api/issues.ts`

- `getBlockedIssues()` - query parameter building, filter combinations
- `addDependency()` - default depType, URL encoding
- `removeDependency()` - DELETE request handling
- `addComment()` - text sanitization, empty text

**Risk**: Dependency management and comments broken without detection.

---

## Go Backend Gaps

### internal/cli

#### Daemon Lifecycle (daemon.go)

| Function | Lines | Untested Scenario | Severity |
|---|---|---|---|
| `spawnAgent()` | 290-376 | Log directory creation failure path | Medium |
| `spawnAgent()` | 308-331 | Custom role vs built-in role command construction | Medium |
| `waitForAgent()` | 378-415 | Log file close failure | Low |
| `shouldRestart()` | 422-437 | Boundary: exit 0 but ran < 1 minute | Medium |
| `computeBackoff()` | 440-460 | Integer overflow protection (count > 30) | Medium |
| `stopAgent()` | 487-538 | SIGTERM→SIGKILL escalation, process already exited | Medium |
| `healthChecker()` | 541-553 | Shutdown during health check tick | Low |
| `checkAgentHealth()` | 556-580 | Stale lock detection with live process | Medium |

#### Auto-mode (automode.go)

| Function | Untested Scenario | Severity |
|---|---|---|
| `runAutoMode()` | Full loop with daemon RPC interaction | Medium |
| `selectNextTask()` | No available tasks after filtering | Low |
| `hasAvailablePlanningTasks()` | Malformed JSON from `bd ready` | Medium |

#### Prompts (prompts.go)

| Function | Untested Scenario | Severity |
|---|---|---|
| `GenerateFleetPlanningPrompt()` | Fleet-specific prompt with taskID | Low |
| `GenerateFleetTaskPrompt()` | Fleet-specific prompt with taskID | Low |
| `GenerateLeadPrompt()` | Lead agent prompt generation | Low |
| `buildWorkspaceContextBlock()` | Workspace config nil vs populated | Low |

#### Git Operations (git.go)

| Function | Untested Scenario | Severity |
|---|---|---|
| `gitPush()` | Push failure, auth failure, network timeout | Medium |
| `gitPull()` | Merge conflict during pull | Medium |
| `gitCheckout()` | Checkout with uncommitted changes | Medium |

#### Worktree (worktree.go)

| Function | Untested Scenario | Severity |
|---|---|---|
| `CreateWorktree()` | Worktree already exists at path | Medium |
| `RemoveWorktree()` | Worktree in use by running process | Medium |
| `RecoverWorktree()` | Partial recovery (lock removed but branch dirty) | Medium |

### internal/webui

#### Server Lifecycle (server.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `StartServer()` | Graceful shutdown timeout enforcement | Medium |
| `StartServer()` | Component stop ordering (fleet → rate limiter → terminal → subscriber → hub → pool) | Medium |
| `StartServer()` | Port fallback exhaustion (all ports occupied) | Low |
| `StartServer()` | Fleet JWT key generation vs pre-provisioned | Medium |
| `StartServer()` | Dev mode frontend directory validation | Low |

#### Route Registration (routes.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `setupRoutes()` | Fleet routes conditional registration | Medium |
| `setupRoutes()` | Terminal routes when termManager is nil | Medium |
| `setupRoutes()` | SSE route when hub is nil | Medium |
| `setupRoutes()` | All HTTP method restrictions (GET vs POST vs PATCH vs DELETE) | Medium |

#### SSE Hub (sse.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `handleSSE()` | `Last-Event-ID` header parsing for reconnection | High |
| `handleSSE()` | `since` query parameter handling | Medium |
| `handleSSE()` | Client disconnection during catch-up send | Medium |
| `handleSSE()` | Event ID monotonicity guarantees | Medium |

#### Subscription (subscription.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `externalChangeLoop()` | External DB change polling (entire function) | High |
| `pollDBChanges()` | Count-based change detection | High |
| `pollDBChanges()` | Fallback mode when wait_for_mutations unavailable | Medium |
| Subscription loop | Race between subscription and external loop | Medium |

#### Fleet Handlers (fleet_handlers.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `handleFleetClaim()` | Task claim collision (another worker grabbed it) | High |
| `handleFleetClaim()` | Timeout (claim takes >5s) | Medium |
| `handleFleetRegister()` | Rate limiter exhaustion | Medium |
| `handleFleetDone()` | Invalid task ID | Low |
| `handleFleetDone()` | Auth middleware coverage (resolved — `FleetAuthMiddleware` now wraps done endpoint) | ~~Medium~~ Resolved |

#### Terminal (terminal.go)

| Area | Untested Scenario | Severity |
|---|---|---|
| `TerminalManager.Shutdown()` | Concurrent shutdown + new session creation | Medium |
| `TerminalManager.RestartSession()` | Session ID not found | Low |
| PTY lifecycle | Write after close race condition | Medium |
| Max sessions | Enforcement when limit (20) reached | Medium |

### internal/rpc

| File | Function | Untested Scenario | Severity |
|---|---|---|---|
| `client.go` | `executeWithTimeout()` | `SetDeadline` failure path | Medium |
| `client.go` | `TryConnectWithTimeout()` | Dial failure with lock held but socket exists | Medium |
| `socket_path.go` | `normalizePathForComparison()` | EvalSymlinks failure fallback | Low |
| `auth.go` | `loadAuthToken()` | Extremely large token files, non-UTF8 content | Low |

### internal/kv

| File | Function | Untested Scenario | Severity |
|---|---|---|---|
| `client.go` | `claimTask()` | Lua script returns unexpected result length | High |
| `client.go` | `toInt64()` | Integer overflow with huge string values | Medium |
| `client.go` | Circuit breaker integration | OPEN state returns `ErrCircuitOpen` | Medium |
| `scripts.go` | Lua scripts | Redis rejects script (compilation error) | Low |
| `client.go` | `validateID()` | Unicode control characters, emoji in IDs | Low |

### internal/lockfile

| Function | Untested Scenario | Severity |
|---|---|---|
| `ReadLockInfo()` | Accepts PID=0 without error | Medium |
| `TryDaemonLock()` | Partially written/truncated JSON | Medium |
| `checkPIDFile()` | PID near INT_MAX | Low |
| Platform-specific | NFS/network filesystem locking | Low |

---

## Frontend Gaps

### API Layer

| File | Function | Untested Scenario | Severity |
|---|---|---|---|
| `api/logs.ts` | All 3 functions | Entire file untested | Critical |
| `api/issues.ts` | `getBlockedIssues()` | Query parameter building | Critical |
| `api/issues.ts` | `addDependency()` | Default depType, URL encoding | Critical |
| `api/issues.ts` | `removeDependency()` | DELETE request handling | Critical |
| `api/issues.ts` | `addComment()` | Text sanitization | Critical |
| `api/agents.ts` | `checkLoomHealth()` | Timeout behavior, error swallowing | High |
| `api/agents.ts` | `fetchWithTimeout()` | Abort cleanup, signal combination | High |
| `api/client.ts` | `initAuth()` | Re-auth race conditions | Medium |
| `api/sse.ts` | Constructor | EventSource constructor throws | Medium |
| `api/sse.ts` | Mutation parsing | Invalid lastEventId format | Low |

### Hooks

| Hook | Untested Scenario | Severity |
|---|---|---|
| `useIssues` | Kanban mode fetch + blocked info enrichment | High |
| `useIssues` | Too-far-behind detection (threshold=3) | High |
| `useIssues` | Issues with empty ID (defensive check) | Medium |
| `useIssues` | Concurrent optimistic updates to different issues | Medium |
| `useSSE` | Unmount during 'connecting' state | Medium |
| `useSSE` | SSR guard (`typeof window === 'undefined'`) | Low |
| `useSSE` | Multiple rapid connect/disconnect cycles | Medium |
| `useBackendConfig` | Error handling during config update | Medium |
| `useBackendConfig` | Optimistic update rollback on failure | Medium |

### Components

| Component | Untested Scenario | Severity |
|---|---|---|
| `App.tsx` | Lazy-loaded component Suspense fallback | Medium |
| `App.tsx` | View mode switching with inflight data | Medium |
| `App.tsx` | Search debounce sync with filter state | Low |
| StatsHeader | Zero stats, very large numbers | Low |

---

## Summary

### By Severity

| Severity | Count | Category |
|---|---|---|
| **Critical** | 12 | Zero-coverage files, security paths |
| **High** | 15 | Partial coverage of core features |
| **Medium** | 40+ | Edge cases in tested code |
| **Low** | 15+ | Minor edge cases |

### Highest Priority Files to Add Tests

1. `internal/webui/handlers_comments.go` - Zero tests, user-facing
2. `internal/webui/handlers_logs.go` - Zero tests, security-critical (path traversal)
3. `internal/webui/log_streamer.go` - Mostly untested, complex concurrency
4. `internal/webui/subscription.go` - External change loop untested
5. `internal/webui/sse.go` - SSE handler reconnection untested
6. `frontend/src/api/logs.ts` - Zero tests
7. `frontend/src/api/issues.ts` - 4 functions untested
8. `internal/rpc/socket_path.go` - Security checks untested
9. `internal/webui/server.go` - Shutdown ordering untested
10. `internal/webui/routes.go` - Conditional route registration untested
