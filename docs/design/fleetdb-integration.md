# Fleet-DB Integration Design

**Status:** Approved
**Date:** 2026-03-13
**Approach:** Pragmatic Hybrid (3-phase)
**Related:** See `docs/design/distributed-control-plane.md` for the
target local/global/observed state boundary and distributed runtime
architecture.

## Overview

Integrate fleet-db as a dual issue-tracking backend alongside beads. Users choose
`beads` (local SQLite/git, via `bd` CLI) or `fleetdb` (Redis-backed, event-sourced,
via in-process Go client) per project. Loomcli can auto-start an embedded fleet-db
server using miniredis (dev) or connect to a real Redis (production).

## Current State

All issue operations go through two paths:

1. **`defaultDeps.BD.Run(dir, args...)`** — used by `automode_poller.go`, `deps.go`
2. **`execCommand(GetBeadsDir(), "bd", args...)`** — direct bypass in 16 call sites

### Complete bd Command Inventory

| Command Pattern | Files | Category |
|---|---|---|
| `ready --json --limit N [--parent ID]` | `automode_poller.go`, `monitor.go`, `daemon_epic.go` | Data query |
| `list --json [--status=X] [--type=X] [--assignee=X] [--limit N]` | `automode_poller.go`, `monitor.go`, `daemon_epic.go`, `recover.go` | Data query |
| `blocked --json` | `monitor.go` | Data query |
| `stats --json` | `monitor.go` | Data query |
| `show ID --json` | `claim.go`, `lock.go`, `recover.go`, `daemon_pr.go` | Data query |
| `show ID` (text) | `recover.go` | Data query |
| `update ID --status X [--assignee ""]` | `recover.go` | Data mutation |
| `update ID --external-ref URL` | `daemon_pr.go` | Data mutation |
| `close ID --reason TEXT` | `recover.go` | Data mutation |
| `sync --status` | `monitor.go` | Infrastructure |
| `daemon start/stop/status` | `daemon_ensure.go`, `serve.go`, `doctor.go` | Infrastructure |
| `init` | `init.go`, `workspace_cmd.go` | Infrastructure |
| `--version` | `doctor.go`, `init.go` | Infrastructure |

**Data operations** (first 9 rows) are abstractable. **Infrastructure operations**
(last 4 rows) are bd-specific and stay as direct `execCommand` calls.

---

## Phase 1: RunCommand Adapter

**Goal:** Working dual backend with zero caller changes.

### New Interfaces

```go
// internal/cli/issue_backend.go

// IssueBackend provides raw command-style access to the issue tracker.
type IssueBackend interface {
    // RunCommand executes a bd-style command and returns stdout.
    // Args follow bd CLI conventions: "ready", "--json", "--limit", "100".
    RunCommand(dir string, args ...string) (string, error)
}

// IssueTracker extends IssueBackend with typed methods for hot paths.
// Phase 1: only RunCommand is used. Phase 2 adds typed methods.
type IssueTracker interface {
    IssueBackend

    // Typed methods (Phase 2+)
    Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)
    List(ctx context.Context, opts ListOpts) ([]BdIssue, error)
    Blocked(ctx context.Context) ([]BdIssue, error)
    Stats(ctx context.Context) (BdStats, error)
    GetIssue(ctx context.Context, id string) (*BdIssue, error)
    GetIssueText(ctx context.Context, id string) (string, error)
    UpdateStatus(ctx context.Context, id, status, assignee string) error
    UpdateExternalRef(ctx context.Context, id, ref string) error
    CloseIssue(ctx context.Context, id, reason string) error
    SyncStatus(ctx context.Context) (string, error)
    BackendName() string
}

type ReadyOpts struct {
    Limit    int
    ParentID string
}

type ListOpts struct {
    Status    string
    IssueType string
    Assignee  string
    Limit     int
}
```

Package-level state (matches existing `beadsDirCache`/`beadsDirOnce` pattern):

```go
var (
    trackerMu      sync.RWMutex
    trackerInstance IssueTracker
)

func defaultTracker() IssueTracker {
    trackerMu.RLock()
    t := trackerInstance
    trackerMu.RUnlock()
    if t != nil {
        return t
    }
    // Lazy init: default to bdBackend
    trackerMu.Lock()
    defer trackerMu.Unlock()
    if trackerInstance == nil {
        trackerInstance = newBDBackend(defaultBDRunner{}, GetBeadsDir())
    }
    return trackerInstance
}

func setDefaultTracker(t IssueTracker) {
    trackerMu.Lock()
    defer trackerMu.Unlock()
    trackerInstance = t
}
```

### New Files

#### `internal/cli/issue_backend.go`
- `IssueBackend` and `IssueTracker` interfaces
- `ReadyOpts`, `ListOpts` types
- `defaultTracker()` / `setDefaultTracker()` package-level functions

#### `internal/cli/bd_backend.go`
- `bdBackend` struct wrapping `BDRunner`
- `RunCommand(dir, args...)` → delegates to `bd.Run(dir, args...)`
- Phase 1 typed methods delegate to `RunCommand` + JSON unmarshal
- Reuses existing `BdIssue`, `BdStats` types from `monitor.go`

```go
type bdBackend struct {
    runner BDRunner
    dir    string
}

func newBDBackend(runner BDRunner, dir string) *bdBackend

// RunCommand passes through to the bd CLI subprocess.
func (b *bdBackend) RunCommand(dir string, args ...string) (string, error) {
    result := b.runner.Run(dir, args...)
    if result.Err != nil {
        return "", result.Err
    }
    return result.Stdout, nil
}

// Ready delegates to RunCommand in Phase 1.
func (b *bdBackend) Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error) {
    args := []string{"ready", "--json", "--limit", strconv.Itoa(opts.Limit)}
    if opts.ParentID != "" {
        args = append(args, "--parent", opts.ParentID)
    }
    out, err := b.RunCommand(b.dir, args...)
    if err != nil {
        return nil, err
    }
    var issues []BdIssue
    return issues, json.Unmarshal([]byte(out), &issues)
}
// ... similar for List, Stats, etc.
```

#### `internal/cli/fleetdb_backend.go`
- `fleetDBBackend` struct wrapping fleet-db `service.IssueService`
- `RunCommand` parses bd-style args and dispatches to service methods
- Returns JSON matching bd's output format exactly

```go
type fleetDBBackend struct {
    svc       service.IssueService  // fleet-db service layer
    workspace string                 // fleet-db workspace key
    logger    *slog.Logger
}

func (f *fleetDBBackend) RunCommand(dir string, args ...string) (string, error) {
    // Parse bd-style args and dispatch
    if len(args) == 0 {
        return "", fmt.Errorf("no command specified")
    }
    ctx := context.Background()
    switch args[0] {
    case "ready":
        return f.handleReady(ctx, args[1:])
    case "list":
        return f.handleList(ctx, args[1:])
    case "blocked":
        return f.handleBlocked(ctx, args[1:])
    case "stats":
        return f.handleStats(ctx, args[1:])
    case "show":
        return f.handleShow(ctx, args[1:])
    case "update":
        return f.handleUpdate(ctx, args[1:])
    case "close":
        return f.handleClose(ctx, args[1:])
    case "sync":
        return "synced\n", nil
    case "daemon":
        return f.handleDaemon(args[1:])
    default:
        return "", fmt.Errorf("unknown command: %s", args[0])
    }
}
```

**Arg parsing:** Simple linear scan for `--flag=value` and `--flag value` patterns.
The call sites are internal and the argument patterns are fixed (enumerated above).

**JSON output contract:** The `fleetDBBackend` must produce JSON identical to bd's
output for each command. Key field mapping:

| fleet-db field | bd JSON field | Notes |
|---|---|---|
| `Issue.Type` (IssueType) | `issue_type` | Different key name |
| `Issue.Status` (Status) | `status` | Same |
| `Issue.Priority` (int) | `priority` | Same |
| `Issue.Labels` ([]string) | `labels` | Same |
| Dependencies | `dependencies` | fleet-db requires separate `ListDependencies` call |

**Dependency hydration strategy:**
- `ready` and `list`: Only `fetchReadyIssues` uses dependencies (for blocker checking).
  Batch-fetch dependencies concurrently (max 10 goroutines) for ready results.
  For `list --limit 500` (fetchUnclosedIssueIDs), caller only checks status, so
  return empty dependencies array.
- `show`: Always include dependencies (single issue, one extra call).

#### `internal/cli/fleetdb_server.go`
- Manages embedded fleet-db server lifecycle
- Two modes: miniredis (auto_start, dev) or real Redis (production)

```go
type FleetDBServer struct {
    backend   *fleetDBBackend
    rdb       *redis.Client
    miniRedis *miniredis.Miniredis  // nil when using real Redis
    projector *projection.RedisProjector
    logger    *slog.Logger
}

type FleetDBServerConfig struct {
    RedisURL    string // empty = use miniredis
    Workspace   string // fleet-db workspace key (default: "default")
    AutoStart   bool   // start embedded miniredis
}

func NewFleetDBServer(cfg FleetDBServerConfig, logger *slog.Logger) (*FleetDBServer, error)
func (s *FleetDBServer) Backend() IssueTracker
func (s *FleetDBServer) Stop() error
```

**Startup sequence:**
1. If `RedisURL` is empty and `AutoStart` is true → start miniredis
2. Connect redis client
3. Create `storage.NewRedisStorage(rdb, workspace)`
4. Create `storage.NewRedisEventStore(rdb)`
5. Create `projection.NewRedisProjector(rdb, eventStore, store, logger)`
6. Start projector
7. Create `service.NewIssueService(store, store, store, store, eventStore, proj, logger)`
8. Ensure workspace exists: `store.CreateWorkspace(ctx, &models.Workspace{Key: workspace})`
9. Return `FleetDBServer` with `fleetDBBackend` wrapping the service

#### `internal/cli/config_fleetdb.go`
- Config resolution from all 3 layers

```go
type FleetDBSettings struct {
    Enabled   bool   `yaml:"enabled,omitempty"`
    RedisURL  string `yaml:"redis_url,omitempty"`
    Workspace string `yaml:"workspace,omitempty"`
    AutoStart bool   `yaml:"auto_start,omitempty"`
}

// resolveFleetDBConfig merges config from env vars, loom.yaml, ~/.loom/config.yaml.
// Priority: env vars > loom.yaml > ~/.loom/config.yaml > defaults
func resolveFleetDBConfig(daemon *DaemonSettings, global *LoomConfig) FleetDBServerConfig
```

**Environment variables:**
- `LOOM_FLEETDB_ENABLED=true` — enable fleet-db backend
- `LOOM_FLEETDB_REDIS_URL=redis://localhost:6379` — Redis connection
- `LOOM_FLEETDB_WORKSPACE=MY-PROJECT` — workspace key

### Modified Files

#### `internal/cli/deps.go`
Add `Tracker IssueTracker` to `Deps`. Wire in `DefaultDeps()`:

```go
type Deps struct {
    Git     GitRunner
    Exec    ExecRunner
    FS      FileSystem
    Logger  *slog.Logger
    Clock   func() time.Time
    BD      BDRunner
    Tracker IssueTracker  // NEW
}

func DefaultDeps() *Deps {
    bd := defaultBDRunner{}
    return &Deps{
        Git:     defaultGitRunner{},
        Exec:    defaultExecRunner{},
        FS:      defaultFileSystem{},
        Logger:  slog.Default(),
        Clock:   time.Now,
        BD:      bd,
        Tracker: newBDBackend(bd, GetBeadsDir()),  // default: beads
    }
}
```

#### `internal/cli/daemon_config.go`
Add `FleetDB *FleetDBSettings` to `DaemonSettings` (after line 23):

```go
type DaemonSettings struct {
    // ... existing fields ...
    FleetDB *FleetDBSettings `yaml:"fleetdb,omitempty"`  // NEW
}
```

Add to `ProjectFile` as well (via `DaemonSettings` already being embedded).
Extend `overlayDaemonSettings` to merge `FleetDB` sub-struct.

#### `internal/cli/config.go`
Add `FleetDB *FleetDBSettings` to `LoomConfig.Daemon` (already via `DaemonSettings`).
No additional changes needed since `LoomConfig.Daemon *DaemonSettings` already exists.

#### `internal/cli/monitor.go`
Single change at line 919-925:

```go
// Before:
func runBdCommand(args ...string) (string, error) {
    result := execCommand(GetBeadsDir(), "bd", args...)
    if result.Err != nil {
        return "", result.Err
    }
    return result.Stdout, nil
}

// After:
func runBdCommand(args ...string) (string, error) {
    return defaultTracker().RunCommand(GetBeadsDir(), args...)
}
```

This single change routes all 7 monitor bd calls through the active backend.

#### `internal/cli/daemon_cmd.go`
Insert fleet-db startup between steps 9.6 and 10 (after OTel init, before NewDaemon):

```go
// 9.7. Start fleet-db embedded backend if configured
var fleetDBSrv *FleetDBServer
if config.Daemon.FleetDB != nil && config.Daemon.FleetDB.Enabled {
    fleetCfg := resolveFleetDBConfig(&config.Daemon, loadedGlobalConfig)
    fleetDBSrv, err = NewFleetDBServer(fleetCfg, slog.Default())
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: starting fleet-db backend: %v\n", err)
        os.Exit(1)
    }
    setDefaultTracker(fleetDBSrv.Backend())
    defer func() { _ = fleetDBSrv.Stop() }()
    log.Printf("[daemon] fleet-db backend active (workspace: %s)", fleetCfg.Workspace)
}

// 10. Create and start daemon (existing)
daemon, err := NewDaemon(config, projectDir, eventBus)
```

#### `go.mod`
```
require (
    github.com/tysonthomas9/fleet-db v0.0.0
)

replace github.com/tysonthomas9/fleet-db => ../fleet-db
```

### What Stays Unchanged

All existing callers work without modification:
- `automode_poller.go` — uses `defaultDeps.BD.Run()`, unchanged
- `daemon_epic.go` — uses `execCommand("bd", ...)`, unchanged (Phase 2 migrates these)
- `claim.go` — uses `execCommand("bd", ...)`, unchanged
- `lock.go` — uses `execCommand("bd", ...)`, unchanged
- `recover.go` — uses `execCommand("bd", ...)`, unchanged
- `daemon_pr.go` — uses `execCommand("bd", ...)`, unchanged
- `daemon_ensure.go` — bd infrastructure, stays as-is permanently
- `doctor.go` — bd infrastructure, stays as-is permanently
- `init.go` — bd infrastructure, stays as-is permanently
- All test files — `MockBDRunner` unchanged, interface unchanged

### Config Example

```yaml
# loom.yaml
daemon:
  fleetdb:
    enabled: true
    workspace: "LOOM"
    auto_start: true  # embedded miniredis for dev

# Production:
# daemon:
#   fleetdb:
#     enabled: true
#     workspace: "LOOM"
#     redis_url: "redis://localhost:6379"
```

### Testing Strategy

- `bd_backend_test.go` — Mock `BDRunner` returning fixture JSON, assert `RunCommand`
  pass-through and typed method JSON parsing
- `fleetdb_backend_test.go` — Use miniredis + real fleet-db service layer, seed issues,
  assert `RunCommand("ready", "--json")` returns valid `[]BdIssue`-shaped JSON
- All existing tests pass unchanged (default backend is `bdBackend`)

---

## Phase 2: Typed Hot-Path Methods

**Goal:** Eliminate JSON serialize/deserialize overhead on hot paths. Migrate the
highest-frequency callers to typed `IssueTracker` methods.

### Hot Paths (called every 100ms–5s in daemon loops)

| Caller | Current Code | New Code |
|---|---|---|
| `automode_poller.go:53` fetchReadyIssues | `defaultDeps.BD.Run(dir, "ready", "--json", ...)` → JSON unmarshal | `defaultDeps.Tracker.Ready(ctx, opts)` |
| `automode_poller.go:74` fetchUnclosedIssueIDs | `defaultDeps.BD.Run(dir, "list", "--json", ...)` → JSON unmarshal | `defaultDeps.Tracker.List(ctx, opts)` |
| `monitor.go:604-624` collectTaskStatus (5 calls) | `runBdCommand(...)` → JSON unmarshal | `defaultDeps.Tracker.Ready/List/Blocked(ctx, ...)` |
| `monitor.go:811` collectStatistics | `runBdCommand("stats", "--json")` → JSON unmarshal | `defaultDeps.Tracker.Stats(ctx)` |
| `monitor.go:854` collectReadyTasksByPriority | `runBdCommand("ready", "--json", ...)` → JSON unmarshal | `defaultDeps.Tracker.Ready(ctx, opts)` |
| `daemon_epic.go:134` queryOpenEpics | `execCommand("bd", "list", "--type=epic", ...)` → JSON unmarshal | `defaultDeps.Tracker.List(ctx, ListOpts{IssueType:"epic", Status:"open"})` |
| `daemon_epic.go:159` epicHasReadyTasks | `execCommand("bd", "ready", "--parent", ...)` → JSON unmarshal | `defaultDeps.Tracker.Ready(ctx, ReadyOpts{ParentID:id, Limit:1})` |

### Changes

#### `internal/cli/automode_poller.go`

```go
// Before:
func fetchReadyIssues(parentID string) ([]BdIssue, error) {
    args := []string{"ready", "--json", "--limit", "100"}
    if parentID != "" {
        args = append(args, "--parent", parentID)
    }
    result := defaultDeps.BD.Run(GetBeadsDir(), args...)
    // ... JSON unmarshal ...
}

// After:
func fetchReadyIssues(parentID string) ([]BdIssue, error) {
    return defaultDeps.Tracker.Ready(context.Background(), ReadyOpts{
        Limit:    100,
        ParentID: parentID,
    })
}
```

```go
// Before:
func fetchUnclosedIssueIDs() (map[string]bool, error) {
    result := defaultDeps.BD.Run(GetBeadsDir(), "list", "--json", "--limit", "500")
    // ... JSON unmarshal, filter ...
}

// After:
func fetchUnclosedIssueIDs() (map[string]bool, error) {
    issues, err := defaultDeps.Tracker.List(context.Background(), ListOpts{Limit: 500})
    if err != nil {
        return nil, err
    }
    ids := make(map[string]bool, len(issues))
    for _, issue := range issues {
        if issue.Status != "closed" {
            ids[issue.ID] = true
        }
    }
    return ids, nil
}
```

#### `internal/cli/monitor.go`

Replace `collectTaskStatus` internals. The 5 parallel `runBdCommand` calls become
5 parallel typed method calls:

```go
func collectTaskStatus(tracker IssueTracker, readyLimit int) (...) {
    ctx := context.Background()
    var wg sync.WaitGroup

    var readyIssues, ipIssues, revIssues, blkIssues, closedIssues []BdIssue
    var readyErr, ipErr, revErr, blkErr, clErr error

    wg.Add(5)
    go func() { defer wg.Done(); readyIssues, readyErr = tracker.Ready(ctx, ReadyOpts{Limit: readyLimit}) }()
    go func() { defer wg.Done(); ipIssues, ipErr = tracker.List(ctx, ListOpts{Status: "in_progress"}) }()
    go func() { defer wg.Done(); revIssues, revErr = tracker.List(ctx, ListOpts{Status: "review"}) }()
    go func() { defer wg.Done(); blkIssues, blkErr = tracker.Blocked(ctx) }()
    go func() { defer wg.Done(); closedIssues, clErr = tracker.List(ctx, ListOpts{Status: "closed", Limit: 50}) }()
    wg.Wait()
    // ... rest unchanged, but operates on []BdIssue directly (no JSON unmarshal) ...
}
```

Replace `collectStatistics`:
```go
func collectStatistics(tracker IssueTracker) MonitorStats {
    stats, err := tracker.Stats(context.Background())
    if err != nil { return MonitorStats{} }
    return MonitorStats{
        Open:       stats.Summary.OpenIssues,
        Closed:     stats.Summary.ClosedIssues,
        Total:      stats.Summary.TotalIssues,
        InProgress: stats.Summary.InProgressIssues,
        Blocked:    stats.Summary.BlockedIssues,
        // ... etc
    }
}
```

Replace `collectSyncBdStatus`:
```go
func collectSyncBdStatus(tracker IssueTracker) SyncInfo {
    status, err := tracker.SyncStatus(context.Background())
    // ... same error checking logic ...
}
```

#### `internal/cli/daemon_epic.go`

```go
// Before:
func defaultQueryOpenEpics() ([]EpicInfo, error) {
    result := execCommand(GetBeadsDir(), "bd", "list", "--type=epic", "--status=open", "--json", "--limit", "0")
    // ... JSON unmarshal ...
}

// After:
func defaultQueryOpenEpics() ([]EpicInfo, error) {
    issues, err := defaultDeps.Tracker.List(context.Background(), ListOpts{
        IssueType: "epic",
        Status:    "open",
        Limit:     0,
    })
    if err != nil {
        return nil, err
    }
    // ... convert []BdIssue to []EpicInfo (same logic, no JSON step) ...
}
```

#### `internal/cli/monitor.go` — Delete `runBdCommand`

After all callers are migrated, `runBdCommand` has no remaining callers and is deleted.

### Performance Impact

For beads backend: identical (typed methods call `RunCommand` internally, same as Phase 1).
For fleet-db backend: eliminates per-call JSON marshal → unmarshal round trip. The
`fleetDBBackend` typed methods call `svc.GetReady()` etc. directly and return `[]BdIssue`
without serialization.

### Testing

- Update `automode_poller_test.go` to set `defaultDeps.Tracker` (a `MockIssueTracker`)
  instead of `defaultDeps.BD`
- Update `monitor_test.go` similarly
- Add `MockIssueTracker` to test helpers
- Existing `MockBDRunner` tests remain for bd_backend_test.go

---

## Phase 3: Full Migration

**Goal:** All issue data operations go through `IssueTracker`. Remove the `RunCommand`
adapter. `BDRunner` remains only for bd infrastructure commands.

### Cold-Path Callers to Migrate

| File | Line | Current | New |
|---|---|---|---|
| `claim.go` | 109 | `execCommand(dir, "bd", "show", taskID, "--json")` | `defaultDeps.Tracker.GetIssue(ctx, taskID)` |
| `lock.go` | 381 | `execCommand(GetBeadsDir(), "bd", "show", taskID, "--json")` | `defaultDeps.Tracker.GetIssue(ctx, taskID)` |
| `recover.go` | 215 | `execCommand(GetBeadsDir(), "bd", "show", taskID)` (text) | `defaultDeps.Tracker.GetIssueText(ctx, taskID)` |
| `recover.go` | 310 | `execCommand(GetBeadsDir(), "bd", "close", taskID, "--reason", reason)` | `defaultDeps.Tracker.CloseIssue(ctx, taskID, reason)` |
| `recover.go` | 327 | `execCommand(GetBeadsDir(), "bd", "show", taskID, "--json")` | `defaultDeps.Tracker.GetIssue(ctx, taskID)` |
| `recover.go` | 341 | `execCommand(GetBeadsDir(), "bd", "update", taskID, "--status", "open", "--assignee", "")` | `defaultDeps.Tracker.UpdateStatus(ctx, taskID, "open", "")` |
| `recover.go` | 472 | `execCommand(GetBeadsDir(), "bd", "list", "--assignee", name, "--status", "in_progress", "--json")` | `defaultDeps.Tracker.List(ctx, ListOpts{Assignee:name, Status:"in_progress"})` |
| `daemon_pr.go` | 83 | `execCommand(GetBeadsDir(), "bd", "show", epicID, "--json")` | `defaultDeps.Tracker.GetIssue(ctx, epicID)` + `defaultDeps.Tracker.List(ctx, ListOpts{ParentID:epicID})` for children |
| `daemon_pr.go` | 169 | `execCommand(GetBeadsDir(), "bd", "update", epicID, "--external-ref", prURL)` | `defaultDeps.Tracker.UpdateExternalRef(ctx, epicID, prURL)` |

### Interface Additions

Add `GetEpicChildren` to `IssueTracker` (needed by `daemon_pr.go:getEpicInfo` which
queries children via `bd show --json` dependents field):

```go
type IssueTracker interface {
    // ... existing methods ...
    GetEpicChildren(ctx context.Context, epicID string) ([]BdIssue, error)
}
```

### Cleanup

1. Remove `RunCommand` from `IssueBackend` interface (or remove `IssueBackend` entirely)
2. Remove `bdBackend.RunCommand` and `fleetDBBackend.RunCommand` implementations
3. Remove the `RunCommand` dispatch table from `fleetdb_backend.go`
4. Remove `BDRunner` from `Deps` struct
5. Remove `defaultBDRunner` type
6. Keep `BDRunner` interface for `daemon_ensure.go`, `doctor.go`, `init.go` which use
   bd infrastructure commands — these use `execCommand("bd", ...)` directly

**Files that permanently keep `execCommand("bd", ...)`:**
- `daemon_ensure.go:23,46` — `bd daemon start/status` (no fleet-db equivalent)
- `serve.go:189` — `bd daemon stop`
- `doctor.go:304,331` — `bd --version`, `bd daemon status`
- `init.go:169,225,254` — `bd init`, `bd --version`
- `workspace_cmd.go:257` — `bd init`

These are bd-specific infrastructure operations. When fleet-db is active, these either:
- Become no-ops (daemon start/stop — fleet-db is embedded)
- Are skipped (doctor checks are backend-conditional)
- Are replaced (init creates fleet-db workspace instead)

### Doctor Integration

```go
func checkIssueBackend() checkResult {
    if isFleetDBActive() {
        return checkFleetDB()  // verify Redis connectivity
    }
    return checkBdCLI()  // existing check
}
```

### Init Integration

```go
func initIssueBackend(dir string) error {
    if isFleetDBActive() {
        // Ensure workspace exists in fleet-db
        return ensureFleetDBWorkspace(dir)
    }
    // Existing: bd init
    result := execCommand(dir, "bd", "init")
    ...
}
```

### Testing

- All tests use `MockIssueTracker` for business logic
- `MockBDRunner` only remains in infrastructure tests (doctor, init, daemon_ensure)
- Add parity test: run same operations through `bdBackend` and `fleetDBBackend`,
  compare results (follow fleet-db's own `compat/` pattern)

---

## Data Flow Diagrams

### Phase 1 — Beads path (unchanged behavior)

```
monitor.go:runBdCommand("ready", "--json", "--limit", "100")
  → defaultTracker().RunCommand(dir, "ready", "--json", "--limit", "100")
    → bdBackend.RunCommand(dir, args)
      → defaultBDRunner.Run(dir, "ready", "--json", "--limit", "100")
        → execCommand(dir, "bd", "ready", "--json", "--limit", "100")
          → subprocess: bd ready --json --limit 100
            → stdout JSON
```

### Phase 1 — Fleet-db path

```
monitor.go:runBdCommand("ready", "--json", "--limit", "100")
  → defaultTracker().RunCommand(dir, "ready", "--json", "--limit", "100")
    → fleetDBBackend.RunCommand(dir, args)
      → parseReadyArgs(args) → limit=100, parentID=""
        → svc.GetReady(ctx, workspace, ReadyFilter{Limit:100})
          → Redis ZRANGEBYSCORE (in-process, no subprocess)
        → convertToJSON([]models.Issue) → []BdIssue JSON string
```

### Phase 2 — Fleet-db typed path

```
automode_poller.go:fetchReadyIssues("")
  → defaultDeps.Tracker.Ready(ctx, ReadyOpts{Limit:100})
    → fleetDBBackend.Ready(ctx, opts)
      → svc.GetReady(ctx, workspace, ReadyFilter{Limit:100})
        → Redis ZRANGEBYSCORE (in-process)
      → convertToBdIssues([]models.Issue) → []BdIssue
        [no JSON serialization at all]
```

### Phase 3 — All paths typed

```
recover.go:closeTask(taskID, reason)
  → defaultDeps.Tracker.CloseIssue(ctx, taskID, reason)
    → fleetDBBackend.CloseIssue(ctx, id, reason)
      → svc.CloseIssue(ctx, workspace, id, reason)
        → Redis pipeline (in-process)
```

---

## Config Hierarchy

```
Priority (highest to lowest):
  1. Environment variables: LOOM_FLEETDB_ENABLED, LOOM_FLEETDB_REDIS_URL, LOOM_FLEETDB_WORKSPACE
  2. Project file: loom.yaml → daemon.fleetdb.*
  3. Global config: ~/.loom/config.yaml → daemon.fleetdb.*
  4. Defaults: enabled=false, workspace="default", auto_start=false
```

### loom.yaml example (dev)

```yaml
version: 1
backend: claude
daemon:
  fleetdb:
    enabled: true
    workspace: "LOOM"
    auto_start: true  # starts embedded miniredis
  restart_policy:
    max_retries: 3
agents:
  - worktree: falcon
    role: task
    auto: true
```

### loom.yaml example (production)

```yaml
version: 1
backend: claude
daemon:
  fleetdb:
    enabled: true
    workspace: "LOOM"
    redis_url: "redis://redis.internal:6379"
  restart_policy:
    max_retries: 5
agents:
  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: plan
    auto: true
```

### ~/.loom/config.yaml example

```yaml
version: 1
default_workspace: main
daemon:
  fleetdb:
    redis_url: "redis://localhost:6379"  # shared across all projects
```

---

## Dependency Impact

### go.mod additions

```
require github.com/tysonthomas9/fleet-db v0.0.0

replace github.com/tysonthomas9/fleet-db => ../fleet-db
```

Fleet-db's transitive dependencies already shared with loomcli:
- `github.com/redis/go-redis/v9`
- `github.com/alicebob/miniredis/v2`
- `github.com/google/uuid`

New transitive dependencies from fleet-db (only needed if importing `internal/` packages):
- `github.com/jackc/pgx/v5` — PostgreSQL driver (can be excluded via build tags if only Redis path is used)
- `github.com/mattn/go-sqlite3` — SQLite archive backend (same)

**Note:** Since fleet-db uses `internal/` packages, both repos must be in the same
module or fleet-db must export the service layer via `pkg/`. If `internal/` is not
importable, the alternative is to use fleet-db's `pkg/client` with an HTTP or RPC
transport to a fleet-db subprocess (instead of in-process). This changes Phase 1's
`fleetDBBackend` to use `client.Client` instead of `service.IssueService`.

---

## Risk Mitigation

| Risk | Mitigation |
|---|---|
| fleet-db `internal/` not importable | Use `pkg/client` with RPC transport to subprocess. Or move service interfaces to `pkg/`. |
| JSON output mismatch between bd and fleet-db backend | Parity tests comparing output for identical operations |
| miniredis data loss on restart | Expected for dev. Document that production requires real Redis. |
| Concurrent access to `defaultTracker()` | `sync.RWMutex` protection (same pattern as `beadsDirCache`) |
| `bd daemon start` called when fleet-db active | Phase 1: bd daemon may fail gracefully. Phase 3: conditional skip. |
| Go version mismatch (loomcli vs fleet-db) | Align `go` directives in both go.mod files |

---

## File Summary

### Phase 1 — New Files (5)
- `internal/cli/issue_backend.go` — interfaces + package-level state (~80 lines)
- `internal/cli/bd_backend.go` — beads adapter (~150 lines)
- `internal/cli/fleetdb_backend.go` — fleet-db adapter + RunCommand dispatch (~350 lines)
- `internal/cli/fleetdb_server.go` — embedded server lifecycle (~120 lines)
- `internal/cli/config_fleetdb.go` — config resolution (~80 lines)

### Phase 1 — Modified Files (4 + go.mod)
- `internal/cli/deps.go` — add `Tracker` field
- `internal/cli/daemon_config.go` — add `FleetDBSettings`
- `internal/cli/monitor.go` — 1-line change to `runBdCommand`
- `internal/cli/daemon_cmd.go` — insert server startup block
- `go.mod` — add fleet-db dependency

### Phase 2 — Modified Files (~5)
- `internal/cli/automode_poller.go` — migrate to typed methods
- `internal/cli/monitor.go` — migrate collect* functions
- `internal/cli/daemon_epic.go` — migrate epic queries
- `internal/cli/monitor.go` — delete `runBdCommand`
- Test files — add `MockIssueTracker`

### Phase 3 — Modified Files (~7)
- `internal/cli/claim.go` — migrate to `GetIssue`
- `internal/cli/lock.go` — migrate to `GetIssue`
- `internal/cli/recover.go` — migrate all 5 bd calls
- `internal/cli/daemon_pr.go` — migrate to typed methods
- `internal/cli/doctor.go` — backend-conditional checks
- `internal/cli/init.go` — backend-conditional init
- `internal/cli/deps.go` — remove `BD` field, cleanup
