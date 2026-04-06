# Backend Infrastructure Architecture

## Overview

The backend infrastructure encompasses the core abstractions and systems powering loom: the `IssueBackend` interface with three pluggable implementations (beads RPC, fleet REST, agent IPC), a canonical entity domain layer, entity mapping between wire and domain types, an in-process notification bus, task routing with repo affinity, the web UI server with its coordinator-based workspace lifecycle, module-based route registration, middleware stack, SSE real-time hub, agent IPC with cooperative preemption, diff viewer pipeline, session/tab persistence stores, daemon lifecycle management, auto-mode polling, and structured logging.

---

## 1. IssueBackend Interface and Implementations

### Interface Definition

**Package:** `internal/backend` -- **File:** `internal/backend/issuebackend.go`

The `IssueBackend` interface defines the standard pluggable contract for all issue storage backends. It replaces the former `IssueTracker` interface with richer query/mutation semantics. All methods return `error`; implementations return `*BackendError` (see `errors.go`) which callers extract via `errors.As` to inspect the `ErrorKind`.

```go
type IssueBackend interface {
    // Query operations
    Get(ctx context.Context, id string) (*IssueDetailData, error)
    List(ctx context.Context, opts ListOpts) ([]IssueData, error)
    Ready(ctx context.Context, opts ReadyOpts) ([]IssueData, error)
    Blocked(ctx context.Context, opts BlockedOpts) ([]IssueData, error)
    Stats(ctx context.Context) (*StatsData, error)
    Count(ctx context.Context, opts CountOpts) (int, error)

    // Mutation operations
    Create(ctx context.Context, params CreateParams) (*IssueData, error)
    Update(ctx context.Context, id string, params UpdateParams) error
    ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error
    Close(ctx context.Context, id string, params CloseParams) (*CloseResult, error)
    Reopen(ctx context.Context, id string, params ReopenParams) error
    Delete(ctx context.Context, params DeleteParams) error

    // Dependency operations
    AddDependency(ctx context.Context, params DepAddParams) error
    RemoveDependency(ctx context.Context, params DepRemoveParams) error

    // Label operations
    AddLabel(ctx context.Context, id string, label string) error
    RemoveLabel(ctx context.Context, id string, label string) error

    // Comment operations
    ListComments(ctx context.Context, id string) ([]CommentData, error)
    AddComment(ctx context.Context, params CommentAddParams) (*CommentData, error)

    // Event operations
    ListEvents(ctx context.Context, id string, limit int) ([]EventData, error)

    // Batch operations
    Batch(ctx context.Context, ops []BatchOp) ([]BatchResult, error)

    // Mutation polling
    GetMutations(ctx context.Context, sinceMs int64) ([]MutationData, error)
    WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]MutationData, error)

    // Metadata
    BackendName() string
}
```

### Wire Types

**File:** `internal/backend/types.go`

Two projection levels optimize list vs detail views:

- **`IssueData`** -- slim projection (~18 fields) for list views: ID, Title, Status, Priority, IssueType, Assignee, Owner, Labels, SourceRepo, Parent, Design (presence only), CreatedAt, UpdatedAt, DueAt, DeferUntil, DependencyCount, DependentCount.
- **`IssueDetailData`** -- full projection embedding `IssueData` plus Description, Design (full text), AcceptanceCriteria, Notes, CreatedBy, ClosedAt, CloseReason, ClosedBySession, ExternalRef, EstimatedMinutes, Dependencies[], Dependents[], Comments[].

Other wire types:
- **`DependencyData`** -- relationship with inline display fields (IssueID, DependsOnID, Type, Title, Status, Priority, IssueType, CreatedAt, CreatedBy).
- **`CommentData`** -- ID, IssueID, Author, Text, CreatedAt, ParentID, EditedAt.
- **`EventData`** -- ID, IssueID, Kind, Actor, Target, Payload, CreatedAt.
- **`StatsData`** -- 11 aggregate fields: TotalIssues, OpenIssues, InProgressIssues, ClosedIssues, BlockedIssues, DeferredIssues, ReadyIssues, TombstoneIssues, PinnedIssues, EpicsEligibleForClosure, AverageLeadTime.
- **`CloseResult`** -- Closed (issue) + Unblocked (list of newly unblocked issues).
- **`MutationData`** -- subscription events with Type, IssueID, Title, Assignee, Actor, Timestamp, OldStatus, NewStatus, ParentID, SourceRepo, StepCount.

### Option/Param Structs

All defined in `internal/backend/types.go`:

- **`ListOpts`** -- comprehensive filtering: Status, Priority, IssueType, Assignee, Labels, LabelsAny, IDs, ParentID, Limit, Query, TitleContains, DescriptionContains, NotesContains, CreatedAfter/Before, UpdatedAfter/Before, ClosedAfter/Before, EmptyDescription, NoAssignee, NoLabels, PriorityMin/Max, Pinned, Ephemeral, IncludeTemplates, MolType, ExcludeStatus, ExcludeTypes, Deferred, DeferAfter/Before, DueAfter/Before, Overdue, SourceRepos, AllowStale. Fields annotated "fleet-db: unsupported" are not handled by FleetBackend.
- **`ReadyOpts`** -- Assignee, Unassigned, Priority, Type, ParentID, Limit, SortPolicy, Labels, LabelsAny, MolType, IncludeDeferred, SourceRepos.
- **`BlockedOpts`** -- ParentID, Assignee, Priority, Type, Limit.
- **`CountOpts`** -- same filters as ListOpts for scoping, plus GroupBy for grouped counts.
- **`CreateParams`** -- full issue creation: ID (optional, backend generates if empty), Parent, Title, Description, IssueType, Priority, Design, AcceptanceCriteria, Notes, Assignee, Owner, CreatedBy, ExternalRef, EstimatedMinutes, Labels, Dependencies, DueAt, DeferUntil.
- **`UpdateParams`** -- pointer fields for "set to value" vs nil="no change" semantics. Includes Title, Description, Status, Priority, Design, AcceptanceCriteria, Notes, Assignee, Owner, IssueType, ExternalRef, EstimatedMinutes, AddLabels, RemoveLabels, SetLabels, Parent, AgentState, DueAt, DeferUntil, Claim (bool -- atomic claim takes precedence over explicit Status).
- **`CloseParams`** -- Reason, Session, SuggestNext, Force.
- **`ReopenParams`** -- Reason (recorded as comment).
- **`DeleteParams`** -- IDs (batch), Reason, Force, Cascade. Validation error if IDs empty.
- **`DepAddParams`** / **`DepRemoveParams`** -- FromID, ToID, DepType.
- **`CommentAddParams`** -- IssueID, Author, Text.
- **`BatchOp`** / **`BatchResult`** -- Operation (method name), Args (JSON-encoded), Success, Data, Error.

### Mutation Type Constants

```go
MutationCreate, MutationUpdate, MutationDelete, MutationComment,
MutationBonded, MutationSquashed, MutationBurned, MutationStatus,
MutationRefresh, MutationSessionChange
```

### BeadsBackend Implementation

**Package:** `internal/backend/beads`

Wraps `rpc.Client` via a narrow `beadsClient` interface. Maps `rpc.*` types to `backend.*` wire types via `convert.go`. NOT safe for concurrent use (single RPC connection). Uses `execAndCheck` error classification helper in `errors.go`.

**PooledBackend** (`pool.go`): Connection pool wrapper providing concurrent access to BeadsBackend instances. Circuit breaker integration via `internal/circuitbreaker` for resilience.

**MutationBridge** (`subscription.go`): Converts `rpc.MutationEvent` to `backend.MutationData` and publishes to `notify.Bus` for SSE push.

### FleetBackend Implementation

**Package:** `internal/backend/fleet`

HTTP REST client against fleet server workspace-scoped API. Thread-safe. Configured with BaseURL, WorkspaceID, AuthToken/APIKey. Supports concurrent auth token updates. 50MB max response body. Includes `entity_convert.go` for backend wire type to entity.Issue conversions.

Key files: `fleet.go` (core client), `convert.go` (rpc type conversions), `fleet_types.go` (fleet-specific wire types), `params.go` (query parameter encoding), `config.go` (client configuration), `errors.go` (error classification).

### AgentIPC Backend Implementation

**Package:** `internal/backend/agentipc`

Proxies 3 IPC-supported mutations to daemon Unix socket: `ClaimIssue`, `Update`, `Close`. All other operations return `KindNotImplemented`. Thread-safe -- each method call creates a new connection. Wire protocol: JSON lines over Unix domain socket with 5s dial timeout, 10s read timeout.

```go
type Backend struct {
    socketPath string
    agentName  string
}
```

Transport helper `sendIPC` dials, sends one JSON-line request, reads one JSON-line response, and disconnects. All transport errors are returned as `KindUnavailable`.

---

## 2. Entity Domain Types

**Package:** `internal/entity` -- Canonical domain types with zero internal-package imports.

### Issue

**File:** `internal/entity/issue.go`

Core entity with ~60+ fields organized by concern:

| Group | Fields |
|-------|--------|
| Core Identification | ID |
| Content | Title, Description, Design, AcceptanceCriteria, Notes |
| Status/Workflow | Status (IssueStatus), Priority (0-4), IssueType |
| Assignment | Assignee, Owner, EstimatedMinutes |
| Timestamps | CreatedAt, CreatedBy, UpdatedAt, ClosedAt, CloseReason, ClosedBySession |
| Scheduling | DueAt, DeferUntil |
| External Integration | ExternalRef, SourceSystem, SourceRepo |
| Relations | Labels, Dependencies ([]*Dependency), Comments ([]*Comment) |
| Tombstone | DeletedAt, DeletedBy, DeleteReason, OriginalType |
| Messaging | Sender, Ephemeral |
| Context Markers | Pinned, IsTemplate |
| Bonding | BondedFrom ([]BondRef) |
| HOP | Creator (*EntityRef), Validations, QualityScore, Crystallizes |
| Gate | AwaitType, AwaitID, Timeout, Waiters |
| Slot | Holder |
| Source Tracing | SourceFormula, SourceLocation |
| Agent Identity | HookBead, RoleBead, AgentState, LastActivity, RoleType, Rig |
| Molecule Type | MolType (swarm, patrol, work) |
| Work Type | WorkType (mutex, open_competition) |
| Event | EventKind, Actor, Target, Payload |

**IssueStatus** enum: `StatusOpen`, `StatusInProgress`, `StatusBlocked`, `StatusDeferred`, `StatusReview`, `StatusClosed`, `StatusTombstone`, `StatusPinned`, `StatusHooked`. Supports custom statuses via `IsValidWithCustom()`.

**IssueType** enum: `TypeBug`, `TypeFeature`, `TypeTask`, `TypeEpic`, `TypeChore`. Normalization maps aliases ("enhancement", "feat" -> TypeFeature). Supports custom types via `IsValidWithCustom()`.

**AgentState** enum: `StateIdle`, `StateSpawning`, `StateRunning`, `StateWorking`, `StateStuck`, `StateDone`, `StateStopped`, `StateDead`.

Tombstone constants: `DefaultTombstoneTTL` (30 days), `MinTombstoneTTL` (7 days), `ClockSkewGrace` (1 hour).

### Agent

**File:** `internal/entity/agent.go`

Agent entity: ID, Title, Description, Status (IssueStatus), State (AgentState), RoleType, Rig, HookBead, RoleBead, LastActivity, CreatedAt, UpdatedAt, Labels.

**RoleType** constants: `RolePolecat`, `RoleCrew`, `RoleWitness`, `RoleRefinery`, `RoleMayor`, `RoleDeacon`.

Helper methods: `IsAlive()` (idle/spawning/running/working), `IsDead()`, `NeedsAttention()` (stuck/dead), `IsActive()` (running/working).

### Dependency

**File:** `internal/entity/dependency.go`

Dependency: IssueID, DependsOnID, Type (DependencyType), CreatedAt, CreatedBy, Metadata, ThreadID.

**DependencyType** -- 20 type variants across categories:

| Category | Types |
|----------|-------|
| Workflow (affect ready work) | `blocks`, `parent-child`, `conditional-blocks`, `waits-for` |
| Association | `related`, `discovered-from` |
| Graph Link | `replies-to`, `relates-to`, `duplicates`, `supersedes` |
| Entity (HOP) | `authored-by`, `assigned-to`, `approved-by`, `attests` |
| Convoy Tracking | `tracks` |
| Reference | `until`, `caused-by`, `validates` |
| Delegation | `delegated-from` |

Methods: `AffectsReadyWork()` (blocks, parent-child, conditional-blocks, waits-for), `IsDirectBlocker()` (excludes parent-child).

### Comment

**File:** `internal/entity/comment.go`

Comment: ID, IssueID, Author, Text, CreatedAt, ParentID (threading), EditedAt, DeletedAt (soft-delete). Collections: Reactions ([]*Reaction), Edits ([]*CommentEdit). `MaxCommentLength`: 64KB.

CommentEdit: ID, CommentID, OldText, NewText, EditedBy, EditedAt.
Reaction: ID, CommentID, Author, Emoji, CreatedAt.

### Session

**File:** `internal/entity/session.go`

Session: SessionID, TaskID, EpicID, AgentName, Backend, Model, Phase, StartedAt, EndedAt, DurationS, Status (SessionStatus), ExitCode, ErrorClass, TokenUsage, DiffStats, AttemptNum.

**SessionStatus**: `SessionRunning`, `SessionCompleted`, `SessionFailed`, `SessionAborted`.

**TokenUsage**: InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens, EstimatedCostUSD. Methods: `IsZero()`, `Total()`.

**DiffStats**: FilesChanged, LinesAdded, LinesRemoved. Methods: `IsZero()`, `TotalLines()`.

### Workspace

**File:** `internal/entity/workspace.go`

Workspace: ID, Name, Path, Backend, Repos ([]Repo), CreatedAt. Validation: MaxWorkspaceNameLen=64, alphanumeric/hyphens/underscores only.

Repo: Name, Path, DefaultBranch, Remote, Groups, SourceRepoID. Methods: `EffectiveRemote()` (default "origin"), `EffectiveDefaultBranch()` (default "main"), `EffectiveSourceRepoID()` (default Name).

WorkspaceConfig: Backend (per-workspace configuration).

### Diff

**File:** `internal/entity/diff.go`

**DiffCommit**: Hash, ShortHash, Subject, Author, Email, Date.

**DiffFile**: Path, Status (FileStatus: M/A/D/R), OldPath (for renames), Additions, Deletions.

**DiffPatch**: Patch (unified diff text), IsBinary, IsTooLarge, Additions, Deletions. Methods: `HasContent()`, `IsViewable()`.

---

## 3. Entity Mapping Layer

**Package:** `internal/backend/mapping`

Bridges backend wire types and entity domain types with intentional lossiness:

| Function | Direction | Notes |
|----------|-----------|-------|
| `IssueFromData(IssueData)` | wire -> entity | Slim: drops ~35+ entity fields (content, HOP, gate, bonding, etc.) |
| `IssueFromDetailData(IssueDetailData)` | wire -> entity | Near-lossless for carried fields. Combines Dependencies + Dependents into single slice. ExternalRef: empty string -> nil, non-empty -> &val |
| `IssueToData(entity.Issue)` | entity -> wire | Drops content/relational/entity-only fields |
| `DependencyFromData` / `DependencyToData` | bidirectional | Drops inline display fields (Title, Status) |
| `CommentFromData` / `CommentToData` | bidirectional | Drops Reactions, Edits, DeletedAt |

Files: `issue.go`, `dependency.go`, `comment.go` (each with `_test.go`).

Round-trip: lossy at slim layer, near-lossless at detail layer.

---

## 4. Notification Bus

**Package:** `internal/notify`

**File:** `internal/notify/bus.go`

In-process pub/sub hub with workspace-scoped subscriptions. No background goroutines.

```go
type Event struct {
    Topic       string    // Dot-delimited (e.g., "issue.created")
    WorkspaceID string    // Empty = system-wide
    Payload     any       // Typed event data; consumers type-assert
    Timestamp   time.Time // Auto-set if zero on Publish
}

type Bus struct { /* thread-safe, RWMutex-protected subscriber list */ }
```

- **`Publish(Event)`** -- non-blocking fan-out; drops if subscriber buffer full (counted via `Subscription.Dropped()`).
- **`Subscribe(workspaceID, topics...)`** -- workspace-scoped with topic prefix matching. Empty workspaceID = all workspaces. No topics = all topics.
- **`SubscribeWithBuffer(bufferSize, workspaceID, topics...)`** -- custom buffer size.
- **`Subscription`**: `Events()` channel, `Close()`, `Dropped()` count.
- **`DefaultBufferSize`**: 64. Minimum buffer size clamped to 1.
- **`Publisher`** interface: `Publish(Event)`. `NopPublisher` discards all events.
- **`Bus.Close()`**: closes all subscriber channels, prevents further Publish/Subscribe.

Used by MutationBridge (`internal/backend/beads/subscription.go`) to feed SSE hub via `NotificationSubscriberHook`.

---

## 5. Task Routing and Repo Affinity

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

`MatchTask(task, constraints)` scores a task:
- **Priority check**: rejects if `task.Priority > constraints.MaxPriority`
- **Task filter check**: uses `IsAvailableForPlanning`, `IsAvailableForImplementation`, or `IsAvailableForAny`
- **Repo affinity**: bonus score when `task.SourceRepo` matches any of `constraints.SourceRepos`

`SelectBestTask(tasks, constraints)` returns the highest-scoring match.

### AgentEntryFromEnv()

Reconstructs routing context from environment variables (`LOOM_SOURCE_REPOS` -> split by comma).

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

## 6. Web UI Server Architecture

### Server Struct

**File:** `internal/webui/server.go`

The `Server` struct holds all initialized dependencies as struct fields:

| Field Group | Fields |
|-------------|--------|
| Network | listener, actualPort, corsConfig |
| HTTP Routing | mux (*http.ServeMux), wsModules ([]Module) |
| Connection Pools | pool (daemon.Pool), multiPool (*daemon.MultiPool) |
| Service Layer | issueSvc, agentSvc, workspaceSvc, termSvc, diffSvc, fileSvc, sessSvc |
| Real-time | hub (*realtime.Hub), multiSub, getMutationsSince |
| Workspace Lifecycle | registry (*coordinator.WorkspaceRegistry), initialWorkspaceID |
| Terminal | termMgr (*TerminalManager), termAuth (*realtime.TerminalAuth) |
| SSE Token Exchange | sseTokens (*realtime.TokenStore) -- external auth mode only |
| Fleet | fleetRegistry, tokenCfg, claimMetrics, fleetRegCfg |
| Redis Stores | tabMetaStore, issueTabStore, sessionHistoryStore |
| External Auth | extAuthMiddleware, jwksCleanup |
| Rate Limiters | clientErrLimiter, cspLimiter, authCfgLimiter |

### Constructor: NewServer

**File:** `internal/webui/server_app.go`

`NewServer(ctx, ServerConfig)` initializes all dependencies with a context-aware cleanup stack for failure rollback. On error, all resources allocated before the failure point are cleaned up in reverse order.

### ServerConfig

**File:** `internal/webui/server_config.go`

```go
const (
    defaultPort            = 8080
    defaultPoolSize        = 100
    defaultShutdownTimeout = 5 * time.Second
    defaultMaxPortAttempts = 10
)
```

Key config fields: Port, BindAddress ("127.0.0.1"), SocketPath, PoolSize, CORSEnabled/Origins, ShutdownTimeout, MaxPortAttempts, TerminalCmd, MaxTerminalSessions, FleetEnabled, FleetRedis, FleetJWTKey, FleetAPIKey, HSTSEnabled, ExtAuthURL/Issuer/Audience/AllowInsecure, LoomServerURL, DevMode/DevFrontendDir, GitOps, FileOps, WorkspaceConfigFn, WorkspaceCreateFn, WorkspaceListFn, InitialWorkspaceID, BackendOps, ScrollbackMaxLines, SessionsStore, NotifyTokenDir, FleetMode, FleetClientURL/Workspace/APIKey, Logger.

CORS config auto-adds auth service origin when ExtAuthURL is configured.

### Server Sub-Packages

**`server/dto/`** -- Request/response DTOs with `Validate()` methods:
- `IssueRequest` / `IssueResponse` -- issue CRUD DTOs
- `AgentStatusResponse` -- agent status projection
- `SessionResponse` -- session audit trail projection
- `WorkspaceResponse` -- workspace topology projection
- `ErrorResponse` -- standard error envelope
- `ListResponse[T]` -- generic list wrapper (not yet parameterized -- Go 1.18+ style)
- Mapper: `IssueFromEntity()` functions in `issue_mapper.go`
- Validation: `validate.go`, `issue_validate.go`
- Common: `common.go` (shared types/helpers)

**`server/handler/`** -- HTTP utilities:
- `ReadJSON` (1MB max body, trailing content check), `WriteJSON` -- in `request.go`
- `HandleServiceError`, `ParseListOpts` -- in `errors.go`
- Constants: `DefaultListLimit=100`, `MaxListLimit=1000`

**`server/middleware/`** -- Middleware chain:
| File | Middleware |
|------|-----------|
| `auth.go` | ExtAuth -- RS256 JWT verification via JWKS cache; passthrough in open mode |
| `auth_routes.go` | Public route bypass (SSE, fleet, terminal, static file routes exempt) |
| `cors.go` | CORS -- allowlist-based origin check, preflight handling |
| `security.go` | Security headers -- HSTS, CSP, X-Content-Type-Options |
| `logging.go` | Request log -- structured logging with method, path, status, duration |
| `ratelimit.go` | Rate limit -- per-IP for client error and CSP report endpoints |
| `workspace.go` | Workspace -- resolves workspace ID from URL path into context |
| `recover.go` | Panic handler with structured logging |
| `respond.go` | JSON response helpers |
| `jwks.go` | JWKS cache with periodic refresh |
| `ip.go` | Client IP extraction utilities |
| `middleware.go` | Chain(), Middleware type definition |

**`server/realtime/`** -- Real-time infrastructure:
| File | Responsibility |
|------|---------------|
| `hub.go` | SSE Hub, Client, MutationPayload |
| `handler.go` | SSE HTTP handler with reconnection catch-up |
| `writer.go` | SSE event writer (formats event: data lines) |
| `sse_token.go` | TokenStore for SSE token exchange (external auth) |
| `terminal_auth.go` | TerminalAuth -- one-time WebSocket token management |
| `terminal_relay.go` | Terminal WebSocket relay to tmux via creack/pty |

### Startup Sequence

1. Apply config defaults (port, pool size, shutdown timeout, bind address)
2. Build CORS configuration, auto-add auth service origin
3. Find available port (auto-fallback if requested port in use)
4. Initialize MultiPool for workspace-aware connection routing
5. Create initial workspace connection pool with circuit breaker
6. Initialize service layers (issue, agent, workspace, terminal, diff, file, session)
7. Create SSE hub, start `hub.Run()` goroutine
8. Create MultiWorkspaceSubscriber for per-workspace mutation polling
9. Initialize TerminalManager (tmux integration), TerminalAuth
10. Initialize external auth (JWKS cache, JWT middleware)
11. Initialize fleet store registry (if Redis configured)
12. Construct WorkspaceRegistry with lifecycle hooks (see section 7)
13. Register initial workspace and reconcile config workspaces
14. Initialize Redis-backed stores (tabmeta, issuetabs, sessionhistory)
15. Build handlers via `buildHandlers()`
16. Build modules via `buildModules()`
17. Create mux and register routes via `registerRoutes()`

### Middleware Stack

```
Request -> Recover -> RequestLog -> RateLimit -> SecurityHeaders -> ExtAuth -> CORS -> Router
```

Applied in `run()` via `middleware.Chain()`, then wrapped with `h2c.NewHandler` for HTTP/2 cleartext support.

### HTTP Server Configuration

```go
ReadTimeout:       15s
ReadHeaderTimeout: 10s
WriteTimeout:      30s
IdleTimeout:       60s
```

Graceful shutdown: cancel server-wide context, then `server.Shutdown(drainCtx)` with configurable timeout (default 5s). Components stopped in reverse-initialization order.

---

## 7. Workspace Coordinator and Lifecycle Hooks

**Package:** `internal/webui/coordinator`

### LifecycleHook Interface

**File:** `internal/webui/coordinator/coordinator.go`

```go
type LifecycleHook interface {
    Name() string
    OnRegister(ctx *RegistrationContext) error
    OnDeregister(ctx DeregistrationContext)
    Critical() bool
    OnRollback(ctx DeregistrationContext)
}
```

### RegistrationContext

Carries workspace metadata and a resource bag through the hook chain:
- **WorkspaceID**, **WorkspacePath**, **Logger**
- `Provide(key, value)` -- store named resource for downstream hooks
- `Resolve(key)` -- retrieve resource provided by earlier hook
- `Resources()` -- snapshot all resources into a WorkspaceHandle

### DeregistrationContext

WorkspaceID, Logger. No resource bag -- hooks track their own per-workspace state internally.

### WorkspaceRegistry

**File:** `internal/webui/coordinator/registry.go`

Orchestrates per-workspace lifecycle:
- `AddHook(hook)` -- append hook in registration order; fails on duplicate Name() or if registry is closed
- `Register(id, path)` -- runs hooks forward; on critical failure, rolls back previously-succeeded hooks in reverse order
- `Deregister(id)` -- runs OnDeregister in reverse order for succeeded hooks (best-effort, panic-safe)
- Double-register: auto-deregisters previous registration before re-registering
- `ForWorkspace(id)` -- returns immutable `WorkspaceHandle` (nil-receiver safe)
- `WorkspaceIDs()` -- list all registered workspace IDs
- `Close()` -- prevents new registrations; does NOT deregister existing workspaces

Sentinel errors: `ErrRegistryClosed`, `ErrEmptyWorkspaceID`, `ErrEmptyWorkspacePath`, `ErrDuplicateHookName`.

### Concrete Hooks

**Directory:** `internal/webui/`

| Hook | File | Critical | Purpose |
|------|------|----------|---------|
| BeadsPoolHook | `hook_beads_pool.go` | Yes | Creates ConnectionPool + circuit breaker, registers in MultiPool. Supports pre-built pool for initial workspace. |
| NotificationSubscriberHook | `hook_notification_subscriber.go` | No | Creates DaemonSubscriber, polls daemon mutations, broadcasts to SSE hub via MultiWorkspaceSubscriber |
| TerminalHook | `hook_terminal.go` | No | Provides TerminalManager, kills sessions on deregister |
| FleetStoreHook | `hook_fleet_store.go` | No | Registers workspace in fleet StoreRegistry |
| FleetBackendHook | `hook_fleet_backend.go` | No | Creates per-workspace FleetBackend with configured URL, workspace ID, and API key |

Hook registration order is canonical: beads-pool and notification-subscriber first (suppressed in fleet mode), then terminal, fleet-store, fleet-backend.

---

## 8. Module-Based Route Registration

### Module Interface

**File:** `internal/webui/module.go`

```go
type Module interface {
    Register(mux *http.ServeMux)
}
```

The webui server uses two muxes: an app-level mux for top-level routes and a workspace-scoped wsMux for routes under `/api/workspaces/{ws}/`. Module implementations are mux-agnostic.

### Top-Level Routes (App Mux)

**File:** `internal/webui/routes.go`

Registered directly in `registerRoutes()`:

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/health` | `healthHandler` |
| GET | `/api/health` | `apiHealthHandler` |
| POST | `/api/client-errors` | `clientErrorsHandler` (rate-limited) |
| POST | `/api/csp-report` | `cspReportHandler` (rate-limited) |
| GET | `/api/config` | `authConfigHandler` (auth mode discovery) |
| GET | `/api/stats` | `statsHandler` |
| GET | `/api/metrics` | `metricsHandler` |
| GET | `/api/daemon/status` | `daemonStatusHandler` |
| GET | `/api/config/backend` | `getBackendConfigHandler` |
| PATCH | `/api/config/backend` | `patchBackendConfigHandler` |
| GET | `/api/backends` | `getBackendsHealthHandler` (conditional) |
| GET | `/api/editors` | `listEditorsHandler` |
| POST | `/api/editors/open` | `openEditorHandler` |
| POST | `/api/sessions/notify` | `notifySessionChangeHandler` (conditional) |
| GET | `/api/monitor/status` | Monitor status (injected from cli) |
| GET | `/api/monitor/agents` | Monitor agents (injected from cli) |
| GET | `/api/monitor/tasks` | Monitor tasks (injected from cli) |
| GET | `/api/monitor/stats` | Monitor stats (injected from cli) |
| GET | `/api/monitor/sync` | Monitor sync (injected from cli) |
| GET | `/api/monitor/workspaces` | Monitor workspaces (injected from cli) |
| GET | `/api/monitor/stale-detector` | Stale detector (injected from cli) |
| GET | `/api/monitor/usage` | Usage data (injected from cli) |
| GET | `/metrics` | Prometheus metrics (injected from cli) |
| GET | `/api/observability/metrics` | Observability metrics (injected from cli) |
| GET | `/api/observability/events` | Observability events (injected from cli) |
| * | `/` | SPA catch-all (embedded FS or dev mode) |

### Workspace Management Routes (App Mux)

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `/api/workspaces/active` | `handleActiveWorkspace` |
| GET | `/api/workspaces` | `handleListWorkspaces` |
| GET | `/api/workspaces/{ws}` | `handleGetWorkspace` |
| POST | `/api/workspaces` | `handleWorkspaceCreate` |
| GET | `/api/workspaces/jobs/{id}` | `handleGetWorkspaceJob` |
| PUT | `/api/workspaces/order` | `handleWorkspaceReorder` |
| PUT | `/api/workspaces/default` | `handleSetDefaultWorkspace` |
| DELETE | `/api/workspaces/default` | `handleClearDefaultWorkspace` |
| DELETE | `/api/workspaces/{ws}` | `handleWorkspaceDelete` (with WorkspaceMiddleware) |

### Workspace-Scoped Routes (Module-Based)

All workspace-scoped routes are registered under `/api/workspaces/{ws}/` via modules. The `WorkspaceMiddleware` is applied to the entire workspace-scoped sub-mux, injecting the workspace ID into the context.

#### IssueModule (11 routes)

**File:** `internal/webui/issue_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../issues/{id}` | `handleGetIssue` |
| GET | `.../issues` | `handleListIssues` |
| POST | `.../issues` | `handleCreateIssue` |
| PATCH | `.../issues/{id}` | `handlePatchIssue` |
| POST | `.../issues/{id}/close` | `handleCloseIssue` |
| POST | `.../issues/{id}/move` | `handleMoveIssue` |
| DELETE | `.../issues/{id}` | `handleDeleteIssue` |
| POST | `.../issues/{id}/comments` | `handleAddComment` |
| GET | `.../issues/{id}/events` | `handleGetIssueEvents` |
| POST | `.../issues/{id}/dependencies` | `handleAddDependency` |
| DELETE | `.../issues/{id}/dependencies/{depId}` | `handleRemoveDependency` |

#### GitModule (13 routes)

**File:** `internal/webui/git_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| POST | `.../git/push-all` | `handleGitPushAll` |
| POST | `.../agents/{name}/git/push` | `handleGitPush` |
| POST | `.../agents/{name}/git/pull` | `handleGitPull` |
| POST | `.../agents/{name}/git/sync` | `handleGitSync` |
| POST | `.../agents/{name}/git/pr` | `handleGitPR` |
| POST | `.../agents/{name}/git/reset` | `handleGitReset` |
| GET | `.../agents/{name}/git/status` | `handleGitStatus` |
| PATCH | `.../agents/{name}/git/target` | `handleGitTargetUpdate` |
| GET | `.../issues/{id}/git/diff-stat` | `handleGetIssueDiffStat` |
| GET | `.../agents/{name}/git/diff-stat` | `handleAgentDiffStat` |
| GET | `.../agents/{name}/diff/commits` | `handleDiffCommits` |
| GET | `.../agents/{name}/diff/files` | `handleDiffFiles` |
| GET | `.../agents/{name}/diff/file` | `handleDiffFile` |

#### FileModule (3 routes)

**File:** `internal/webui/file_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../agents/{name}/files/tree` | `handleFileTree` |
| GET | `.../agents/{name}/files` | `handleFileRead` |
| PUT | `.../agents/{name}/files` | `handleFileWrite` |

#### SessionModule (6 routes)

**File:** `internal/webui/session_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../issues/{issueId}/sessions` | `handleListSessionHistory` |
| GET | `.../issues/{issueId}/sessions/{recordId}/scrollback` | `handleGetSessionScrollback` |
| GET | `.../tasks/{taskId}/sessions` | `handleListTaskSessions` |
| GET | `.../tasks/{taskId}/sessions/{sessionId}` | `handleGetSession` |
| GET | `.../tasks/{taskId}/sessions/{sessionId}/transcript` | `handleGetSessionTranscript` |
| GET | `.../tasks/{taskId}/sessions/{sessionId}/diff` | `handleGetSessionDiff` |

#### LogModule (2-3 routes)

**File:** `internal/webui/log_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../agents/{name}/logs` | `handleGetAgentLog` (conditional on agentSvc) |
| GET | `.../tasks/{id}/logs` | `handleListTaskPhases` |
| GET | `.../tasks/{id}/logs/{phase}` | `handleGetTaskLog` |

#### TerminalModule (12-16 routes)

**File:** `internal/webui/terminal_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../agents/{name}/terminal/info` | Agent terminal info (conditional) |
| GET | `.../agents/{name}/terminal/token` | Agent terminal token (conditional) |
| GET | `.../agents/{name}/terminal/ws` | Agent terminal WebSocket |
| GET | `.../terminal/sessions` | List terminal sessions |
| GET | `.../terminal/token` | General terminal token (conditional) |
| GET | `.../terminal/ws` | Terminal WebSocket |
| POST | `.../terminal/restart` | Restart terminal |
| POST | `.../terminal/kill` | Kill terminal |
| GET | `.../terminal/session-status` | Session status |
| POST | `.../terminal/spawn` | Spawn terminal |
| POST | `.../terminal/sessions/{name}/seed` | Seed session |
| POST | `.../terminal/sessions/{session}/kill` | Schedule session kill |
| POST | `.../terminal/sessions/close-all` | Close all sessions |
| GET | `.../terminal/sessions/{session}/scrollback` | Get scrollback |
| GET | `.../terminal/sessions/{session}/export` | Export session |
| GET | `.../terminal/sessions/{session}/scrollback-info` | Scrollback info |

#### TerminalTabModule (8 routes)

**File:** `internal/webui/terminal_tab_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../terminal/tabs` | List terminal tabs |
| GET | `.../terminal/tabs/{session}` | Get tab metadata |
| PUT | `.../terminal/tabs/{session}` | Set tab metadata |
| PATCH | `.../terminal/tabs/{session}` | Patch tab metadata |
| DELETE | `.../terminal/tabs/{session}` | Delete tab metadata |
| GET | `.../terminal/sessions/by-issue` | Sessions by issue |
| GET | `.../terminal/state` | Get terminal UI state |
| PATCH | `.../terminal/state` | Patch terminal UI state |

#### IssueTabModule (3 routes)

**File:** `internal/webui/issue_tab_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../issues/{issueId}/tabs` | Get issue tabs |
| PUT | `.../issues/{issueId}/tabs` | Save issue tabs |
| DELETE | `.../issues/{issueId}/tabs` | Delete issue tabs |

#### SSEModule (1-2 routes)

**File:** `internal/webui/sse_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| GET | `.../events` | SSE event stream (realtime.NewHandler) |
| GET | `.../events/token` | SSE token exchange (conditional on external auth) |

#### WorkspaceOpsModule (8 routes)

**File:** `internal/webui/workspace_ops_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| PATCH | `.../name` | Workspace rename |
| GET | `.../stats` | Stats |
| GET | `.../ready` | Ready issues |
| GET | `.../blocked` | Blocked issues |
| GET | `.../issues/graph` | Issue graph |
| GET | `.../daemon/status` | Daemon status |
| GET | `.../config/backend` | Backend config |
| PATCH | `.../config/backend` | Patch backend config |

#### FleetModule (4 routes)

**File:** `internal/webui/fleet_module.go`

| Method | Pattern | Handler |
|--------|---------|---------|
| POST | `.../fleet/register` | Fleet worker registration |
| POST | `.../fleet/claim` | Fleet task claim (conditional auth) |
| POST | `.../fleet/done/{id}` | Fleet task done (conditional auth) |
| POST | `.../fleet/heartbeat` | Fleet heartbeat (conditional auth) |

### Static Asset Handling (SPA Routing)

**File:** `internal/webui/embed.go`

`frontendHandler()` (production) and `devFrontendHandler()` (development) implement SPA routing:

- **Extensionless paths** (e.g., `/ws/abc/settings`, `/dashboard`) -- served `index.html` for React Router
- **Static assets** (`.js`, `.css`, `.map`, images, fonts, etc.) -- return 404 if not found, not SPA fallback

Cache headers: `index.html` uses `no-cache, no-store, must-revalidate`; `/assets/*` uses `public, max-age=31536000, immutable` (Vite content-hashes).

---

## 9. SSE Hub and Notification Flow

### Hub Architecture

**File:** `internal/webui/server/realtime/hub.go`

```go
type Hub struct {
    clients      map[*Client]bool
    register     chan *Client
    unregister   chan *Client
    broadcast    chan *MutationPayload  // buffered(256)
    done         chan struct{}
    retryQueue   []*MutationPayload    // overflow buffer
    droppedCount int64
}

type Client struct {
    id          int64
    send        chan *MutationPayload  // buffered(64)
    done        chan struct{}
    lastSince   int64
    sourceRepos []string              // empty = all repos
    workspaceID string                // fail-closed: empty = no mutations
}
```

### Run Loop

`hub.Run()` is a single-goroutine event loop:
- `register`: adds client to `clients` map
- `unregister`: removes client, closes `client.send`
- `broadcast`: fans out to all clients with per-client workspace and source repo filtering (non-blocking send)
- `retryTicker` (100ms): drains overflow queue
- `done`: closes all client channels and exits

### Overflow Handling

`Broadcast()` tries non-blocking send to `hub.broadcast`. On failure, appends to `retryQueue`. Beyond capacity, mutations are dropped and counted in `droppedCount`.

### SSE Handler

**File:** `internal/webui/server/realtime/handler.go`

`NewHandler(HandlerConfig)` returns an `http.Handler` that:
1. Sets SSE headers (`text/event-stream`, `no-cache`, `X-Accel-Buffering: no`)
2. Parses `Last-Event-ID` and `?since` for reconnection catch-up
3. Parses `?source_repos` for per-client filtering
4. Resolves workspace ID from context (fail-closed: no workspace = no mutations)
5. Sends catch-up mutations, then `retry: 5000` and `connected` event
6. Streams from `client.send`, sends heartbeat comment every 30s
7. Exits on client disconnect or server shutdown

### Notification Flow

```
Daemon mutations -> DaemonSubscriber polls via RPC -> MutationBridge -> notify.Bus
    -> NotificationSubscriberHook feeds MultiWorkspaceSubscriber -> SSE Hub.Broadcast()
        -> Per-client workspace/repo filtering -> Client.send channel -> SSE Writer -> Browser
```

**MultiWorkspaceSubscriber**: Creates per-workspace `DaemonSubscriber` goroutines. Each polls the daemon's mutation log and broadcasts with workspace ID tag. Provides `GetMutationsSinceForWorkspace(wsID, since)` for reconnection catch-up.

### Event ID Scheme

`eventIDCounter` is an `atomic.Int64` initialized to `time.Now().UnixMilli()`. IDs are monotonically increasing and roughly time-ordered, which is important for `Last-Event-ID` catch-up.

Constants: `RetryMs=5000`, `HeartbeatInterval=30s`, `ClientSendBuf=64`.

---

## 10. Agent IPC and Cooperative Preemption

### IPC Protocol

**File:** `internal/cli/daemon_ipc.go`

Unix domain socket server with 0600 permissions. The daemon removes stale socket files on startup (safe because `daemon.lock` prevents concurrent startup).

```go
type AgentIPCRequest struct {
    Operation string          `json:"operation"`      // "claim", "update", "complete"
    AgentName string          `json:"agent_name"`     // BD_ACTOR identity
    IssueID   string          `json:"issue_id"`
    Args      json.RawMessage `json:"args,omitempty"` // operation-specific params
}

type AgentIPCResponse struct {
    Success bool            `json:"success"`
    Error   string          `json:"error,omitempty"`
    Kind    string          `json:"kind,omitempty"`  // backend.ErrorKind for typed errors
    Data    json.RawMessage `json:"data,omitempty"`
}
```

Three operations: `claim` (atomic task acquisition with optional lock TTL), `update` (status/design changes via UpdateParams), `complete` (close with reason via CloseParams). All mutations route through daemon notification bus for SSE push.

The IPC server is started via `startIPCServer()` during daemon initialization. Agents connect using the socket path from the `LOOM_DAEMON_SOCKET` environment variable.

### AgentIPC Backend (Client Side)

**File:** `internal/backend/agentipc/backend.go`

The `Backend` struct implements `backend.IssueBackend` by proxying the three supported operations to the daemon Unix socket. All other operations return `KindNotImplemented`. Each call creates a new connection (thread-safe). See section 1 for details.

### Cooperative Preemption (Yield System)

**File:** `internal/cli/daemon_yield.go`

- Daemon writes yield file (`.agent.yield`) to agent's worktree directory when requesting agent to stop
- `YieldRequest` struct: Reason, RequestedAt, RequestedBy
- `WriteYieldFile()` uses atomic write (tmp + rename) with 0600 permissions
- Automode loop checks yield file between backend invocations
- Claude hooks stop handler checks yield file for sub-minute preemption
- Checkpoint save on yield, recovery skip on restart

### Graceful Drain (DrainWithGrace)

**File:** `internal/cli/daemon_drain.go`

Four-phase graceful shutdown sequence:
1. **Phase 1**: Write yield file via `RequestYield()`
2. **Phase 2**: Poll for voluntary exit (check PID alive every 500ms)
3. **Phase 3**: SIGTERM to process group (if yield timeout expires)
4. **Phase 4**: SIGKILL fallback (if SIGTERM timeout expires)

`DefaultYieldTimeout`: 60 seconds. Configurable via `RestartPolicy.YieldTimeout` in daemon config.

Returns `true` if agent exited from yield alone (SIGTERM was not needed).

### loom daemon stop

Full drain: yield all agents -> wait for cooperative exit -> SIGTERM -> SIGKILL for each agent.

---

## 11. Diff Viewer Pipeline

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

### Backend: Diff Handlers

All responses use `diffResponse{Success bool, Data interface{}, Error string}`.

- **`handleDiffCommits`**: resolves agent worktree, computes merge-base if no `?from`, returns commit list
- **`handleDiffFiles`**: requires `?to=<ref>`, validates against `validGitRef` regex, returns changed file list with status (M/A/D/R)
- **`handleDiffFile`**: requires `?path` (validated via `validateDiffPath` -- rejects `..` traversal) and `?to`, returns unified diff patch

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

---

## 12. Session and Tab Persistence

### sessionhistory.Store

**File:** `internal/webui/sessionhistory/store.go`

Redis key: `issue:sessions:{workspaceId}:{issueId}`. No TTL -- persists indefinitely.

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

Operations: `Add`, `List` (sorted by StartedAt desc), `Complete` (sets status, EndedAt, ScrollbackPath). Supports `MigrateLegacyKeys()` for upgrade from pre-workspace format.

### issuetabs.Store

**File:** `internal/webui/issuetabs/store.go`

Redis key: `issue:tabs:{workspaceId}:{issueId}`. TTL: 24 hours, refreshed on write.

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

Redis-backed terminal tab metadata per workspace. Key: `terminal:meta:{workspace}:{session}`. Supports `MigrateLegacyKeys()` for upgrade from pre-workspace format and `MigrateNamedKeys()` for name-to-UUID migration.

---

## 13. Daemon Lifecycle and Agent Management

### Core Types

**File:** `internal/cli/daemon.go`

```go
type Daemon struct {
    config       *DaemonConfig
    agents       []*AgentProcess    // protected by agentsMu (RWMutex)
    shutdown     chan struct{}
    shutdownOnce sync.Once
    epicAssigner *EpicAssigner
    concurrency  *ConcurrencyTracker
    eventBus     events.Emitter
    repos        []RepoConfig
    configHash   string             // SHA-256 of current config
    reconcileMu  sync.RWMutex
    ipcListener  net.Listener       // agent IPC Unix socket
    wg           sync.WaitGroup
}
```

### Startup: `Daemon.Start()`

**File:** `internal/cli/daemon_supervisor.go`

1. Sweep orphaned sessions from prior daemon runs
2. Compute initial `configHash` for reconciler no-op detection
3. Start `healthChecker()` goroutine (30s interval)
4. Start `configReconciler()` goroutine
5. If fleet mode: skip agent supervision (agents managed by fleet server)
6. For each agent: create `stopCh`/`done` channels, start `superviseAgent()` goroutine
7. IPC server started via `startIPCServer()` (separate from Start -- called by daemon command setup)

Note: `resetWorktreeBranches()` has been REMOVED. Branch management is simplified.

Environment includes `LOOM_DAEMON_SOCKET` for IPC, `BD_ACTOR`, `LOOM_WORKTREE_PATH`, `LOOM_EVENTS_DIR`, `LOOM_SOURCE_REPOS`, `LOOM_ROLE`, `LOOM_ALLOWED_TOOLS`, `LOOM_DENIED_TOOLS`.

### superviseAgent Loop

Per-agent goroutine cycle:
1. Check shutdown/stop signals
2. `concurrency.Acquire(role)` -- blocks at role limit
3. `recoverAgent(ap, 0)` -- pre-flight recovery
4. `epicAssigner.AssignWorktree()` -- atomic epic assignment
5. `EnsureWorktreeBranch()` -- checkout epic or agent branch
6. `spawnAgent(ap)` -- `buildCommand()` then `cmd.Start()`
7. `waitForAgent(ap)` -- blocks on `cmd.Wait()`
8. `classifyAgentExit()` -- sets `lastError`, `lastNoWork`
9. `handleAgentCheckpoint()` -- save/clear checkpoint
10. `recoverAgent(ap, exitCode)` -- post-mortem cleanup
11. `EnsureEpicPR()` -- non-fatal PR creation
12. Release epic and concurrency
13. `handleEpicTransition()` -- check exhaustion, reassign
14. Check shutdown, try fallback backend, check restart policy
15. `computeBackoff()` -- exponential backoff
16. Interruptible sleep

### Subprocess Construction

**File:** `internal/cli/daemon_spawn.go`

Process group: `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` -- enables group kill.

### Agent Stop

`stopAgent(ap)`:
1. `syscall.Kill(-pid, SIGTERM)` to entire process group
2. Poll for configurable timeout (default 5s)
3. `syscall.Kill(-pid, SIGKILL)` if still running

### Health Checker

**File:** `internal/cli/daemon_health.go`

Runs every 30 seconds:
- PID alive check via `lockfile.IsProcessRunning()`
- Stale lock detection
- **Output watchdog**: kills hung processes when log file unmodified beyond `OutputTimeout`
- Emits `health_check` event

---

## 14. Hot Reload: Config Reconciler

**File:** `internal/cli/daemon_reconciler.go`

- Uses `fsnotify.NewWatcher()` on project and global config directories
- 500ms debounce for `Write|Create|Rename` events on `loom.yaml` or `config.yaml`
- Fallback: 30s polling when fsnotify unavailable (NFS, containers)

`reloadAndReconcile()`:
1. Load config outside lock (file I/O)
2. Compute SHA-256 hash, compare under lock
3. `diffAgents(old, new)` using `reflect.DeepEqual`
4. `DrainWithGrace()` for removed/modified agents (cooperative preemption + SIGTERM + SIGKILL)
5. `addAgent()` for added/modified agents (starts immediately)
6. Emits `system.config_reloaded` event

---

## 15. Auto-Mode Poller

**File:** `internal/cli/automode.go`

### Per-Iteration Sequence

```
1. UpdateLockState("idle")
2. Check shutdown signal and yield file
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
13. If success, task claimed: increment TasksCompleted, emit task.completed
```

Exit conditions: `MaxTasks` reached, `ConsecutiveErrors >= 3`, `ConsecutiveNoProgress >= 3`, `IdleTimeout` exceeded, yield file detected.

---

## 16. Structured Logging and Observability

### Logging

Uses `log/slog` throughout. `middleware.RequestLog(logger)` emits per-request structured logs with method, path, status, and duration. All event emission is best-effort.

### Observability Events

**File:** `internal/cli/serve_observability.go`

JSONL event files in `daemon.events_dir`. Event types:

| Event Type | Emitted By | Data |
|------------|-----------|------|
| `agent.started` | `spawnAgent` | PID |
| `agent.stopped` | `waitForAgent` | PID, ExitCode |
| `agent.restarted` | `superviseAgent` | PID, RestartCount |
| `epic.assigned` | `superviseAgent` | EpicID |
| `task.completed` | `RunAutoModeLoop` | TaskID, Duration, DiffStats |
| `task.failed` | `RunAutoModeLoop` | TaskID, Error |
| `system.config_reloaded` | `reloadAndReconcile` | Added, Removed, Modified |
| `system.health_check` | `healthChecker` | AgentCount, HealthyCount |

Metrics replayed from disk into `MetricsStore` (30s TTL cache). `GET /api/metrics` returns SSE hub stats, fleet state, and claim metrics alongside the snapshot.

### Error Response Pattern

All handlers use:
- `respondJSON(w, status, v)` -- sets Content-Type, writes status, encodes JSON
- `respondError(w, status, message)` -- wraps in `{"error": message}`
- `handler.WriteJSON()` / `handler.HandleServiceError()` in the handler sub-package

`MaxListLimit` (1000) caps list queries. `maxRequestBody` (1MB) enforced via `http.MaxBytesReader` in `handler.ReadJSON`.

---

## 17. File Map

### Backend Layer

| File | Responsibility |
|------|---------------|
| `internal/backend/issuebackend.go` | `IssueBackend` interface definition |
| `internal/backend/types.go` | Wire types (`IssueData`, `IssueDetailData`, etc.), option/param structs, mutation constants |
| `internal/backend/beads/beads.go` | BeadsBackend -- beads RPC implementation |
| `internal/backend/beads/convert.go` | RPC type conversions (rpc.* <-> backend.*) |
| `internal/backend/beads/errors.go` | Error classification (`execAndCheck`) |
| `internal/backend/beads/pool.go` | PooledBackend -- connection pool for concurrent access |
| `internal/backend/beads/reopen.go` | Reopen support |
| `internal/backend/beads/subscription.go` | MutationBridge -- rpc events -> notify.Bus |
| `internal/backend/fleet/fleet.go` | FleetBackend -- fleet REST API client |
| `internal/backend/fleet/fleet_types.go` | Fleet-specific wire types |
| `internal/backend/fleet/convert.go` | RPC type conversions |
| `internal/backend/fleet/entity_convert.go` | Backend wire -> entity conversions |
| `internal/backend/fleet/params.go` | Query parameter encoding |
| `internal/backend/fleet/config.go` | Client configuration |
| `internal/backend/fleet/errors.go` | Error classification |
| `internal/backend/agentipc/backend.go` | AgentIPCBackend -- daemon IPC proxy (3 mutations) |
| `internal/backend/mapping/issue.go` | Issue wire <-> entity mapping |
| `internal/backend/mapping/dependency.go` | Dependency mapping |
| `internal/backend/mapping/comment.go` | Comment mapping |

### Entity Layer

| File | Responsibility |
|------|---------------|
| `internal/entity/issue.go` | Issue, IssueStatus, IssueType, AgentState, MolType, WorkType, EntityRef, Validation, BondRef |
| `internal/entity/agent.go` | Agent entity, RoleType constants (Polecat, Crew, Witness, Refinery, Mayor, Deacon) |
| `internal/entity/dependency.go` | Dependency, DependencyType (20 variants), WaitsForMeta |
| `internal/entity/comment.go` | Comment, CommentEdit, Reaction |
| `internal/entity/session.go` | Session, TokenUsage, DiffStats, SessionStatus |
| `internal/entity/workspace.go` | Workspace, Repo, WorkspaceConfig |
| `internal/entity/diff.go` | DiffCommit, DiffFile, DiffPatch, FileStatus |
| `internal/entity/doc.go` | Package documentation |

### Notification

| File | Responsibility |
|------|---------------|
| `internal/notify/bus.go` | Bus, Event, Subscription, Publisher -- in-process pub/sub |

### Server Infrastructure

| File | Responsibility |
|------|---------------|
| `internal/webui/server.go` | Server struct, StartServer, run(), Close(), buildHandlers() |
| `internal/webui/server_app.go` | NewServer constructor with cleanup stack |
| `internal/webui/server_config.go` | ServerConfig, DefaultConfig, constants |
| `internal/webui/server_workspace.go` | Workspace handlers |
| `internal/webui/module.go` | Module interface |
| `internal/webui/routes.go` | Route registration, registerWorkspaceRoutes |
| `internal/webui/issue_module.go` | IssueModule -- 11 issue CRUD/comment/event/dependency routes |
| `internal/webui/git_module.go` | GitModule -- 13 git operation and diff routes |
| `internal/webui/file_module.go` | FileModule -- 3 file operation routes |
| `internal/webui/session_module.go` | SessionModule -- 6 session history/audit trail routes |
| `internal/webui/log_module.go` | LogModule -- 2-3 log streaming routes |
| `internal/webui/terminal_module.go` | TerminalModule -- 12-16 terminal management routes |
| `internal/webui/terminal_tab_module.go` | TerminalTabModule -- 8 terminal tab metadata routes |
| `internal/webui/issue_tab_module.go` | IssueTabModule -- 3 issue tab persistence routes |
| `internal/webui/sse_module.go` | SSEModule -- 1-2 SSE event stream routes |
| `internal/webui/workspace_ops_module.go` | WorkspaceOpsModule -- 8 workspace ops/stats/config routes |
| `internal/webui/fleet_module.go` | FleetModule -- 4 fleet orchestration routes |
| `internal/webui/coordinator/coordinator.go` | LifecycleHook interface, RegistrationContext, DeregistrationContext |
| `internal/webui/coordinator/registry.go` | WorkspaceRegistry orchestration |
| `internal/webui/hook_beads_pool.go` | BeadsPoolHook -- connection pool lifecycle |
| `internal/webui/hook_notification_subscriber.go` | NotificationSubscriberHook -- SSE mutation polling |
| `internal/webui/hook_fleet_backend.go` | FleetBackendHook -- per-workspace FleetBackend |
| `internal/webui/hook_fleet_store.go` | FleetStoreHook -- fleet store registration |
| `internal/webui/hook_terminal.go` | TerminalHook -- terminal session lifecycle |
| `internal/webui/server/dto/*.go` | Request/response DTOs with validation, mappers |
| `internal/webui/server/handler/request.go` | ReadJSON, WriteJSON (1MB max, trailing check) |
| `internal/webui/server/handler/errors.go` | HandleServiceError, ParseListOpts, limit constants |
| `internal/webui/server/middleware/auth.go` | ExtAuth JWT middleware (RS256/JWKS) |
| `internal/webui/server/middleware/auth_routes.go` | Public route bypass (isPublicRoute) |
| `internal/webui/server/middleware/cors.go` | CORS middleware |
| `internal/webui/server/middleware/security.go` | Security headers (HSTS, CSP, etc.) |
| `internal/webui/server/middleware/logging.go` | Request log middleware |
| `internal/webui/server/middleware/ratelimit.go` | Per-IP rate limiting |
| `internal/webui/server/middleware/workspace.go` | Workspace ID resolution from URL path |
| `internal/webui/server/middleware/recover.go` | Panic recovery middleware |
| `internal/webui/server/middleware/respond.go` | JSON response helpers |
| `internal/webui/server/middleware/jwks.go` | JWKS cache with periodic refresh |
| `internal/webui/server/middleware/ip.go` | Client IP extraction |
| `internal/webui/server/middleware/middleware.go` | Chain(), Middleware type |
| `internal/webui/server/realtime/hub.go` | SSE Hub, Client, MutationPayload |
| `internal/webui/server/realtime/handler.go` | SSE HTTP handler with catch-up |
| `internal/webui/server/realtime/writer.go` | SSE event writer |
| `internal/webui/server/realtime/sse_token.go` | TokenStore for SSE auth token exchange |
| `internal/webui/server/realtime/terminal_auth.go` | TerminalAuth -- one-time WebSocket tokens |
| `internal/webui/server/realtime/terminal_relay.go` | Terminal WebSocket relay |

### Daemon Infrastructure

| File | Responsibility |
|------|---------------|
| `internal/cli/daemon.go` | Daemon, AgentProcess, NewDaemon |
| `internal/cli/daemon_supervisor.go` | Start(), Stop(), superviseAgent loop |
| `internal/cli/daemon_spawn.go` | buildCommand, spawnAgent, waitForAgent, process group setup |
| `internal/cli/daemon_health.go` | stopAgent, healthChecker, output watchdog |
| `internal/cli/daemon_hotreload.go` | drainAgent, addAgent |
| `internal/cli/daemon_reconciler.go` | configReconciler, reloadAndReconcile, diffAgents |
| `internal/cli/daemon_config.go` | DaemonConfig, AgentEntry, RestartPolicy |
| `internal/cli/daemon_epic.go` | EpicAssigner |
| `internal/cli/daemon_branch.go` | EnsureWorktreeBranch |
| `internal/cli/daemon_backend.go` | Backend failover |
| `internal/cli/daemon_restart.go` | shouldRestart, computeBackoff |
| `internal/cli/daemon_classify.go` | classifyAgentExit, handleEpicTransition |
| `internal/cli/daemon_repos.go` | ResolveAgentTarget, repo resolution |
| `internal/cli/daemon_ipc.go` | IPC Unix socket server (startIPCServer, AgentIPCRequest/Response) |
| `internal/cli/daemon_ipc_client.go` | IPC client for agent subprocess use |
| `internal/cli/daemon_yield.go` | Yield file management (WriteYieldFile, RequestYield, YieldRequest) |
| `internal/cli/daemon_drain.go` | DrainWithGrace -- four-phase graceful shutdown |
| `internal/cli/daemon_control.go` | Control socket commands (agent start/stop/restart) |
| `internal/cli/daemon_control_cmd.go` | CLI commands for daemon control |
| `internal/cli/checkpoint.go` | Checkpoint save/restore |
| `internal/cli/concurrency.go` | ConcurrencyTracker |

### Auto Mode

| File | Responsibility |
|------|---------------|
| `internal/cli/automode.go` | AutoModeOptions, RunAutoModeLoop, yield file checking |
| `internal/cli/automode_poller.go` | fetchReadyIssues, BuildRouterTaskCheck, adaptivePoller |
| `internal/cli/automode_tmux.go` | Tmux integration for automode |

### Task Routing

| File | Responsibility |
|------|---------------|
| `internal/cli/task_router.go` | RoleConstraints, TaskMatch, MatchTask, SelectBestTask |
| `internal/cli/taskfilter.go` | IsAvailableForPlanning, IsAvailableForImplementation, etc. |

### Diff Viewer (Frontend)

| File | Responsibility |
|------|---------------|
| `frontend/src/api/diff.ts` | fetchDiffCommits, fetchDiffFiles, fetchDiffFile |
| `frontend/src/hooks/useDiff.ts` | useDiff hook |
| `frontend/src/components/AgentDetailPanel/DiffTab.tsx` | Diff view orchestrator |
| `frontend/src/components/AgentDetailPanel/DiffFileViewer.tsx` | Hunk parser and renderer |
| `frontend/src/components/CodeMirrorEditor/CodeMirrorEditor.tsx` | CM6 editor with diff language |

### Session and Tab Persistence

| File | Responsibility |
|------|---------------|
| `internal/webui/sessionhistory/store.go` | SessionRecord, Store (Redis, no TTL) |
| `internal/webui/issuetabs/store.go` | IssueTab, Store (Redis, 24h TTL) |
| `internal/webui/tabmeta/` | Terminal tab metadata Store (Redis, per-workspace) |

### Logging and Observability

| File | Responsibility |
|------|---------------|
| `internal/cli/serve_observability.go` | Metrics cache, event replay |
| `internal/webui/handlers_logs.go` | Agent/task log endpoints |
| `internal/webui/log_streamer.go` | Log streaming/tail utilities |
| `internal/webui/log_streamer_paths.go` | Log path resolution and validation |

### Other Packages

| File / Package | Responsibility |
|---------------|---------------|
| `internal/agenterr/class.go` | Error class enum (Transient, Permanent, RateLimit, etc.) |
| `internal/agenterr/classify.go` | Generic error classification |
| `internal/agenterr/classify_claude.go` | Claude-specific error patterns |
| `internal/agenterr/classify_codex.go` | Codex-specific error patterns |
| `internal/agenterr/classify_opencode.go` | OpenCode-specific error patterns |
| `internal/agenterr/error.go` | AgentError type with class metadata |
| `internal/atomicfile/atomicfile.go` | Atomic file writes (tmp + rename) |
| `internal/authmode/authmode.go` | Auth mode constants (open, oidc) |
| `internal/circuitbreaker/breaker.go` | Circuit breaker for connection pools (failure threshold, open timeout, half-open probes) |
| `internal/configlock/configlock.go` | flock-based config file locking |
| `internal/notify/bus.go` | In-process pub/sub (see section 4) |
| `internal/sessions/` | File-based session audit trail store |
| `internal/lockfile/` | PID-based lock files, process alive checking |
| `internal/events/` | Event emitter interface for observability |
| `internal/rpc/` | RPC client/types for beads daemon communication |
| `internal/workspace/` | Workspace utilities (ShortWorkspaceID, etc.) |
