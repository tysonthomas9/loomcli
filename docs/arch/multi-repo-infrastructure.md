# Multi-Repo Infrastructure Architecture (Epic y5vpk)

> **Note (loomcli-26v50 migration):** sections describing `~/.loom/config.yaml` and `loom.yaml` as the source of workspace/repo config describe the legacy persistence path. As of the fleet-db migration, workspace + repo + agent + role + daemon-profile state is stored in fleet-db (see `internal/store/`, `internal/infra/fleetdb/`); the noun-verb CLI (`loom workspace/repo/role/agentdef/daemon profile`) is the user-facing interface. The yaml-based legacy code is being retired in tickets `.23` (workspacemgr), `.24` (yaml.v3), `.25` (config/).

## Overview

The multi-repo infrastructure enables a single beads daemon to aggregate issues from multiple repositories and route agent work items based on repository affinity. The central concept is `source_repo`: a stable string identifier attached to every issue at creation time, propagated through the entire stack from database to frontend, and used for SQL filtering, SSE fan-out, and agent task routing.

The system operates in two tiers:

- **beads tier** (`third_party/beads/`): the issue storage daemon. It owns the SQLite schema, RPC protocol, and in-process SSE hub.
- **loomcli tier** (`internal/`): the orchestration layer. It owns workspace/repo config, daemon process management, agent spawning, and the web UI server.

These tiers share only the wire-protocol types via `internal/rpc/protocol.go` and `internal/rpc/mutation.go`.

---

## 1. RPC Protocol Layer

### Key Interfaces

All filtering arguments flow through shared structs in `internal/rpc/protocol.go` (loomcli) and `third_party/beads/internal/rpc/protocol.go` (beads). Both files are kept in sync; they differ only in import paths and minor comment wording.

```go
// ReadyArgs — filter ready work
type ReadyArgs struct {
    SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
    // ...other fields omitted
}

// ListArgs — general issue list
type ListArgs struct {
    SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
    // ...other fields omitted
}

// CountArgs — aggregate count
type CountArgs struct {
    SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
    // ...other fields omitted
}

// CreateArgs — issue creation
type CreateArgs struct {
    SourceRepo string `json:"source_repo,omitempty"` // Source repository for multi-repo workspaces
    // ...other fields omitted
}

// GetGraphDataArgs — dependency graph fetch
type GetGraphDataArgs struct {
    SourceRepos []string `json:"source_repos,omitempty"` // Filter by source repository
    // ...other fields omitted
}
```

Note the distinction: `CreateArgs` carries a singular `SourceRepo` (the issuing repo), while all query args carry a plural `SourceRepos` (the IN-filter list).

The mutation event wire type lives in `internal/rpc/mutation.go`:

```go
type MutationEvent struct {
    SourceRepo string `json:"source_repo,omitempty"` // Source repository for multi-repo workspaces
    // ...other fields omitted
}
```

### File Map

| File | Role |
|------|------|
| `internal/rpc/protocol.go` | Loomcli-side RPC structs (ReadyArgs, ListArgs, CountArgs, CreateArgs, GetGraphDataArgs) |
| `internal/rpc/mutation.go` | MutationEvent with SourceRepo field |
| `third_party/beads/internal/rpc/protocol.go` | Beads-side mirror of the same structs |
| `third_party/beads/internal/rpc/server_issues_epics.go` | Server handlers: maps RPC args to storage filter structs |

---

## 2. SQL Filtering

### Schema

The `source_repo` column was added in migration 012 and indexed:

```go
// third_party/beads/internal/storage/sqlite/migrations/012_source_repo_column.go
ALTER TABLE issues ADD COLUMN source_repo TEXT DEFAULT '.'
CREATE INDEX IF NOT EXISTS idx_issues_source_repo ON issues(source_repo)
```

Migration 041 added a composite partial index specifically for multi-repo filtered queries:

```go
// third_party/beads/internal/storage/sqlite/migrations/041_status_source_repo_index.go
CREATE INDEX IF NOT EXISTS idx_issues_status_source_repo
    ON issues(status, source_repo)
    WHERE deleted_at IS NULL
```

The partial index (`WHERE deleted_at IS NULL`) reduces index size by excluding tombstoned rows. The existing single-column `idx_issues_source_repo` is kept for tombstone-aware queries (e.g., deletion tracking). The composite index targets the dominant query shape: `WHERE status IN (...) AND source_repo IN (...)`.

### SQL IN Filter Construction

In `third_party/beads/internal/storage/sqlite/ready.go`, the `GetReadyWork` function appends a parameterized IN clause when `filter.SourceRepos` is non-empty:

```go
if len(filter.SourceRepos) > 0 {
    if len(filter.SourceRepos) > 100 {
        return nil, fmt.Errorf("source_repos filter supports at most 100 values, got %d", len(filter.SourceRepos))
    }
    placeholders := make([]string, len(filter.SourceRepos))
    for i, repo := range filter.SourceRepos {
        placeholders[i] = "?"
        args = append(args, repo)
    }
    whereClauses = append(whereClauses,
        fmt.Sprintf("i.source_repo IN (%s)", strings.Join(placeholders, ",")))
}
```

The same pattern is applied in `SearchIssues` (for `handleList` and `handleCount`) and in `GetGraphData`. The 100-value cap prevents unbounded SQL IN clause expansion.

### File Map

| File | Role |
|------|------|
| `third_party/beads/internal/storage/sqlite/migrations/012_source_repo_column.go` | Adds `source_repo` column and single-column index |
| `third_party/beads/internal/storage/sqlite/migrations/041_status_source_repo_index.go` | Adds composite partial index `(status, source_repo) WHERE deleted_at IS NULL` |
| `third_party/beads/internal/storage/sqlite/ready.go` | SQL IN filter in `GetReadyWork` |
| `third_party/beads/internal/storage/sqlite/issues.go` | SQL IN filter in `SearchIssues` (list/count) |
| `third_party/beads/internal/rpc/server_issues_epics.go` | Maps RPC args to storage filter structs |
| `third_party/beads/internal/types/types.go` | `Issue.SourceRepo`, `IssueFilter.SourceRepos`, `WorkFilter.SourceRepos` |

---

## 3. SSE Integration

### Go SSE Hub

The SSE hub lives in `internal/webui/sse.go`. Each connected client stores its per-connection repo filter:

```go
type SSEClient struct {
    id          int64
    send        chan *MutationPayload
    done        chan struct{}
    lastSince   int64
    sourceRepos []string // repos this client wants; empty = all
}
```

The hub's broadcast loop applies per-client filtering before enqueuing:

```go
case mutation := <-h.broadcast:
    h.mu.RLock()
    for client := range h.clients {
        if !matchesSourceRepoFilter(client.sourceRepos, mutation.SourceRepo) {
            continue
        }
        select {
        case client.send <- mutation:
        default:
            // buffer full, skip
        }
    }
    h.mu.RUnlock()
```

The filter logic treats an empty client filter or empty mutation repo as "match all":

```go
func matchesSourceRepoFilter(sourceRepos []string, sourceRepo string) bool {
    if len(sourceRepos) == 0 || sourceRepo == "" {
        return true
    }
    return containsRepo(sourceRepos, sourceRepo)
}
```

### TypeScript SSE Client

`internal/webui/frontend/src/api/sse.ts` builds the SSE URL with a `source_repos` parameter and preserves it across reconnects:

```typescript
export function getSSEUrl(since?: number, sourceRepos?: string[]): string {
  const params = new URLSearchParams();
  if (sourceRepos && sourceRepos.length > 0) {
    params.set("source_repos", sourceRepos.join(","));
  }
  // ...
}

async connect(since?: number, sourceRepos?: string[]): Promise<void> {
    if (sourceRepos !== undefined) {
        this.currentSourceRepos = sourceRepos;
    }
    const url = getSSEUrl(sinceParam, sourceRepos);
    this.eventSource = new EventSource(url);
}
```

### File Map

| File | Role |
|------|------|
| `internal/webui/sse.go` | SSEHub, SSEClient (with sourceRepos), matchesSourceRepoFilter, MutationPayload |
| `internal/webui/frontend/src/api/sse.ts` | BeadsSSEClient, getSSEUrl, MutationPayload interface |
| `internal/webui/frontend/src/hooks/useSSE.ts` | UseSSEOptions.sourceRepos, lifecycle management |
| `internal/webui/frontend/src/hooks/useIssues.ts` | UseIssuesOptions.sourceRepos, propagation to SSE and fetch |
| `internal/webui/frontend/src/types/filter.ts` | WorkFilter.source_repos |

---

## 4. Config System

### RepoConfig

`internal/cli/config.go` defines the workspace-level repo config:

```go
type RepoConfig struct {
    Name          string   `yaml:"name"`
    Path          string   `yaml:"path"`
    DefaultBranch string   `yaml:"default_branch,omitempty"`
    Remote        string   `yaml:"remote,omitempty"`
    Groups        []string `yaml:"groups,omitempty"`
    SourceRepoID  string   `yaml:"source_repo_id,omitempty"`
}
```

`SourceRepoID` defaults to `Name` when left blank (applied in `LoadConfig()`). It is the value written into the `source_repo` column at issue hydration time and matched against `SourceRepos` filters at query time.

### AgentEntry

`internal/cli/daemon_config.go` defines the per-agent config:

```go
type AgentEntry struct {
    Worktree    string   `yaml:"worktree"`
    Role        string   `yaml:"role"`
    Repos       []string `yaml:"repos,omitempty"`
    RepoGroups  []string `yaml:"repo_groups,omitempty"`
    CrossRepo   bool     `yaml:"cross_repo,omitempty"`
    SourceRepos []string `yaml:"-" json:"-"` // Resolved at runtime, not persisted
    // ...
}
```

### configReconciler with fsnotify Watcher

`internal/cli/daemon_reconciler.go` watches config files using `fsnotify`. It watches directories (not files) because editors perform atomic write-rename cycles. A 500ms debounce handles burst writes. When `fsnotify` is unavailable (NFS, containers), a 30-second polling fallback is used. Both paths call `reloadAndReconcile()` which computes a SHA-256 hash to detect actual changes and uses `diffAgents()` to compute added/removed/modified sets.

### File Map

| File | Role |
|------|------|
| `internal/cli/config.go` | LoomConfig, WorkspaceConfig, RepoConfig |
| `internal/cli/daemon_config.go` | AgentEntry, DaemonConfig, LoadDaemonConfig |
| `internal/cli/daemon_reconciler.go` | configReconciler, reloadAndReconcile, diffAgents |
| `internal/cli/daemon_repos.go` | resolveAgentRepos(), expandRepoGroup() |

---

## 5. Daemon Plumbing

### resolveAgentRepos()

`internal/cli/daemon_repos.go` computes the `SourceRepos` slice for a given `AgentEntry`:

```go
func resolveAgentRepos(agent AgentEntry, repos []RepoConfig) []string {
    if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
        return nil // nil = "all repos" (backward compatible)
    }
    // Resolve explicit Repos: map name to SourceRepoID
    // Expand RepoGroups: find all RepoConfigs in matching groups
    // Deduplicate via seen map
}
```

### LOOM_SOURCE_REPOS Environment Variable

`internal/cli/daemon_spawn.go` injects the resolved list into the agent subprocess:

```go
if sourceRepos := resolveAgentRepos(ap.entry, d.repos); len(sourceRepos) > 0 {
    cmd.Env = append(cmd.Env,
        fmt.Sprintf("LOOM_SOURCE_REPOS=%s", strings.Join(sourceRepos, ",")))
}
```

### AgentEntryFromEnv() Round-Trip

`internal/cli/task_router.go` reconstructs the agent's routing context:

```go
func AgentEntryFromEnv() AgentEntry {
    var ae AgentEntry
    if v := os.Getenv("LOOM_SOURCE_REPOS"); v != "" {
        ae.SourceRepos = strings.Split(v, ",")
    }
    return ae
}
```

### Per-Agent stopCh Lifecycle

Each `AgentProcess` owns a `stopCh chan struct{}` and `done chan struct{}`. The `stopCh` is created in `addAgent()` and closed by `drainAgent()` to signal the agent's goroutine to stop. The `done` channel closes when the goroutine exits. This allows surgical hot-reload of individual agents.

### File Map

| File | Role |
|------|------|
| `internal/cli/daemon.go` | Daemon struct (repos field), NewDaemon, AgentProcess lifecycle |
| `internal/cli/daemon_spawn.go` | LOOM_SOURCE_REPOS injection |
| `internal/cli/daemon_hotreload.go` | addAgent, drainAgent (stopCh/done lifecycle) |
| `internal/cli/daemon_repos.go` | resolveAgentRepos, expandRepoGroup |
| `internal/cli/task_router.go` | AgentEntryFromEnv, MatchTask (repo affinity scoring) |
| `internal/cli/automode_poller.go` | fetchReadyIssues (LOOM_SOURCE_REPOS in ready fetch) |

---

## 6. API Surface

### /api/workspace

Returns per-repo `SourceRepoID` and agent repo bindings:

```go
type WorkspaceRepo struct {
    Name         string   `json:"name"`
    Path         string   `json:"path"`
    SourceRepoID string   `json:"source_repo_id,omitempty"`
    Groups       []string `json:"groups"`
}

type WorkspaceAgentInfo struct {
    Name       string   `json:"name"`
    Repos      []string `json:"repos"`
    RepoGroups []string `json:"repo_groups"`
    CrossRepo  bool     `json:"cross_repo"`
}
```

### /api/ready, /api/issues, /api/issues/graph

All parse `source_repos` as a comma-separated query parameter via `parseArrayParam` and pass to the corresponding RPC args.

### File Map

| File | Role |
|------|------|
| `internal/webui/server_workspace.go` | WorkspaceData, WorkspaceRepo, WorkspaceAgentInfo |
| `internal/webui/handlers_ready.go` | source_repos parsing for ready endpoint |
| `internal/webui/handlers_issues_parse.go` | source_repos parsing for issues endpoint |
| `internal/webui/handlers_graph.go` | source_repos parsing for graph endpoint |
| `internal/cli/serve_workspace_info.go` | buildWorkspaceInfo, SourceRepoID propagation |

---

## 7. Complete Data Flow

```
~/.loom/config.yaml
  workspaces:
    myws:
      repos:
        - name: api-server
          source_repo_id: api-server     <- stable filter token
          groups: [backend]

loom.yaml (project-local)
  agents:
    - worktree: worktrees/falcon
      repos: [api-server]               <- explicit repo binding

Daemon startup (NewDaemon)
  +-- ResolveActiveWorkspace()           <- loads d.repos from config
  +-- resolveAgentRepos(entry, d.repos)  <- expands repos -> SourceRepoIDs
       = ["api-server"]

Agent subprocess spawn (buildCommand)
  +-- LOOM_SOURCE_REPOS=api-server       <- injected into env

Agent subprocess (loom task <worktree> --auto)
  +-- AgentEntryFromEnv()                <- reads LOOM_SOURCE_REPOS
  +-- fetchReadyIssues()
       opts.SourceRepos = ["api-server"]
  +-- bd ready --source-repos api-server  (RPC call)
       ReadyArgs.SourceRepos = ["api-server"]

beads RPC server (handleReady)
  +-- wf.SourceRepos = ["api-server"]    <- maps to storage filter

SQLite (GetReadyWork)
  +-- WHERE ... AND i.source_repo IN ('api-server')
       uses idx_issues_status_source_repo composite index

SSE mutation broadcast
  +-- MutationEvent.SourceRepo = "api-server"
  +-- matchesSourceRepoFilter(client.sourceRepos, "api-server")
       -> only clients subscribed to api-server receive the event

Web frontend
  +-- GET /api/events?source_repos=api-server
  +-- GET /api/ready?source_repos=api-server
  +-- WorkspaceRepo.SourceRepoID -> builds per-repo filter UI
```

---

## 8. Critical Details

- **source_repo default**: Issues in single-repo mode have `source_repo = '.'`. An empty `SourceRepos` filter returns all issues regardless.
- **Empty SourceRepos = all repos**: At every layer, maintaining strict backward compatibility.
- **SourceRepoID defaults to Name**: Explicit `source_repo_id` only needed when repo names may change.
- **100-value cap**: SQL filter rejects >100 source repos to prevent unbounded IN clauses.
- **SSE filtering is server-side only**: The TypeScript client does not apply client-side filtering.
- **fsnotify fallback**: 500ms debounce for burst writes; 30-second polling when fsnotify unavailable.
- **Agent stopCh isolation**: Per-agent channels enable surgical hot-reload without affecting other agents.
- **d.repos not refreshed on reconcile**: Workspace topology changes require full daemon restart.
