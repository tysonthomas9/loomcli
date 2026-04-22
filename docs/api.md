# Loom WebUI API Reference

The loom WebUI server exposes a REST API for managing issues, dependencies, fleet coordination, real-time events, log streaming, and terminal access.

## Overview

- **Base URL:** `http://localhost:8080` (configurable via `--port` and `--bind`)
- **Content-Type:** `application/json` for all request and response bodies
- **Request body limit:** 1 MB

## Authentication

### Bearer Token

All protected endpoints require a bearer token via the `Authorization` header:

```
Authorization: Bearer <token>
```

For WebSocket and SSE connections (which cannot set custom headers from browsers), the token can be passed as a query parameter: `?token=<token>`.

### Public Routes

These routes do not require bearer token authentication:

- `GET /health`
- `GET /api/health`
- `GET /api/workspaces/{ws}/terminal/ws` (uses its own one-time token)
- `POST /api/client-errors` (errors may occur before auth bootstrap)
- `POST /api/csp-report` (browsers send CSP reports automatically without auth)
- `POST/GET /api/workspaces/{ws}/fleet/*` (use fleet-specific auth)
- Frontend static files (non-`/api/` paths)

## Response Format

Most endpoints return a standard envelope:

```json
{
  "success": true,
  "data": { ... },
  "error": "message",
  "code": "ERROR_CODE"
}
```

The `error` and `code` fields are only present on failure. Some endpoints use slightly different response shapes (documented per-endpoint).

## Health & Status

### `GET /health`

Simple health check for load balancers. Does not check daemon connectivity.

- **Auth:** None
- **Response:** `200 OK`

```json
{"status": "ok"}
```

### `GET /api/health`

Detailed health check including daemon connection, pool stats, and circuit breaker state.

- **Auth:** None
- **Response:** `200 OK` or `503 Service Unavailable`

```json
{
  "status": "ok|degraded|unhealthy",
  "daemon": {
    "connected": true,
    "status": "ok",
    "uptime": 3600.5,
    "version": "1.0.0",
    "error": ""
  },
  "pool": {
    "active": 2,
    "idle": 8,
    "total": 10
  },
  "circuit_breaker": {
    "state": "Closed|Open|HalfOpen",
    "failure_count": 0,
    "last_state_change": "2024-01-15T00:00:00Z"
  }
}
```

### `GET /api/daemon/status`

Daemon runtime configuration.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "version": "1.0.0",
    "workspace_path": "/path/to/workspace",
    "database_path": "/path/to/db",
    "socket_path": "/path/to/socket",
    "pid": 12345,
    "uptime_seconds": 3600.5,
    "last_activity_time": "2024-01-15T12:00:00Z",
    "exclusive_lock_active": false,
    "exclusive_lock_holder": "",
    "auto_commit": true,
    "auto_push": false,
    "auto_pull": false,
    "local_mode": false,
    "sync_interval": "5s"
  }
}
```

### `GET /api/stats`

Project-level statistics.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "total_issues": 42,
    "open_issues": 15,
    "in_progress_issues": 3,
    "closed_issues": 20,
    "blocked_issues": 2,
    "deferred_issues": 1,
    "ready_issues": 10,
    "tombstone_issues": 1,
    "pinned_issues": 2,
    "epics_eligible_for_closure": 0,
    "average_lead_time_hours": 12.5
  }
}
```

### `GET /api/metrics`

SSE hub runtime metrics and fleet coordination counters. This endpoint is not workspace-scoped — it returns server-wide metrics aggregated across all workspaces. Always registered regardless of fleet or Redis configuration.

- **Auth:** Required (standard bearer token, applied at server middleware level)
- **Query Parameters:** None
- **Request Body:** None

All counters are monotonically increasing except `connected_clients` and `retry_queue_depth`, which are gauges.

Fleet metric fields (prefixed with `loom_fleet_`) use JSON `omitempty` — they are entirely absent from the response when fleet coordination is not configured. Because `omitempty` on `int64` treats `0` as empty, a fleet-enabled deployment where all claim counters are zero will also omit those fields.

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "connected_clients": 3,
    "dropped_mutations": 0,
    "retry_queue_depth": 0,
    "uptime_seconds": 3600.5,
    "loom_fleet_timeouts_total": 0,
    "loom_fleet_claims_success": 5,
    "loom_fleet_claims_collision": 1,
    "loom_fleet_claims_timeout": 0,
    "loom_fleet_claims_total": 6
  }
}
```

- **Response (fleet disabled — no Redis):** `200 OK`

```json
{
  "success": true,
  "data": {
    "connected_clients": 1,
    "dropped_mutations": 0,
    "retry_queue_depth": 0,
    "uptime_seconds": 120.3
  }
}
```

- **Field Descriptions:**

| Field | Type | Gauge/Counter | Description |
|-------|------|---------------|-------------|
| `connected_clients` | int | gauge | Number of active SSE connections (browsers with `/api/events` open) |
| `dropped_mutations` | int64 | counter | Cumulative mutations dropped because a client's send channel was full |
| `retry_queue_depth` | int | gauge | Mutations currently queued for retry delivery to clients |
| `uptime_seconds` | float64 | gauge | Seconds since the SSE hub was created (server start) |
| `loom_fleet_timeouts_total` | int64 | counter | Fleet workers forcibly timed out by the TimeoutEnforcer. Omitted when fleet disabled |
| `loom_fleet_claims_success` | int64 | counter | Successful fleet task claims. Omitted when fleet disabled |
| `loom_fleet_claims_collision` | int64 | counter | Fleet claims that failed due to optimistic-lock collision. Omitted when fleet disabled |
| `loom_fleet_claims_timeout` | int64 | counter | Fleet claims that timed out waiting for a task. Omitted when fleet disabled |
| `loom_fleet_claims_total` | int64 | counter | Total fleet claim attempts (success + collision + timeout). Omitted when fleet disabled |

- **Errors:**
  - `401` — missing or invalid bearer token
  - `503` — SSE hub not initialized (`{"success": false, "error": "SSE hub not initialized"}`)

- **Concurrency:** All underlying data sources (SSEHub counters, ClaimMetrics, TimeoutEnforcer) use atomic operations or RWMutex — safe for concurrent calls.
- **No rate limiting** beyond the global rate limiter. The endpoint is lightweight (no I/O, no RPC calls).

## Backends

Endpoint for backend health discovery. Allows the frontend to query which AI backends (Claude, Codex, OpenCode, Gemini, Cursor, etc.) are registered, installed, configured, and healthy. Used by the UI to display backend availability status and inform backend selection.

**Conditional Registration:** This endpoint is only registered when the `BackendOps` implementation is provided at server startup (`backendOps != nil`). If not registered, requests to `/api/backends` fall through to the SPA catch-all handler and return HTML, not a JSON 404.

### Data Model: BackendHealth

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | always present | Machine-readable backend identifier (e.g. `"claude"`, `"codex"`, `"gemini"`). Used to reference the backend in config endpoints. |
| `display_name` | string | always present | Human-readable label (e.g. `"Claude"`, `"Codex"`). May be empty string if backend lacks Meta capability. |
| `available` | bool | always present | Composite availability flag — `true` only when the backend is considered healthy. |
| `installed` | bool | always present | Whether the backend binary/tool exists on the system PATH. |
| `api_key_set` | bool | always present | Whether required credentials (API keys, tokens) are configured. |
| `version` | string | optional (`omitempty`) | Backend version when detectable. Omitted from JSON when empty. |
| `message` | string | optional (`omitempty`) | Diagnostic message explaining unavailability (e.g. `"codex not found on PATH"`). Omitted from JSON when empty. |

**Health check resolution order:**

1. If the backend is registered but `GetBackendByName` returns false → all boolean flags are false, no version or message
2. Otherwise, defaults to `available=true`, `installed=true`, `api_key_set=true`
3. If backend has `HasHealthCheck` capability → overrides `installed`/`api_key_set`/`available` from `HealthCheck()` result; gets `version` and `message` from health check; gets `display_name` from `Meta().DisplayName` if `HasMeta`
4. Else if backend only has `HasMeta` capability → uses `Meta()` for `display_name` and `version`; keeps all-true defaults
5. If backend has neither capability → keeps all-true defaults; no `display_name` or `version`

### `GET /api/backends`

List all registered AI backends with their health and availability status.

- **Auth:** Required (Bearer token)
- **Query Parameters:** None
- **Conditional Availability:** Only registered when `BackendOps` is provided at server startup. If not registered, requests fall through to the SPA catch-all handler (returns HTML, not JSON).
- **Response `200 OK`:**

```json
{
  "success": true,
  "data": [
    {
      "name": "claude",
      "display_name": "Claude",
      "available": true,
      "installed": true,
      "api_key_set": true,
      "version": "1.0.0"
    },
    {
      "name": "codex",
      "display_name": "Codex",
      "available": false,
      "installed": false,
      "api_key_set": false,
      "message": "codex not found on PATH"
    }
  ]
}
```

Notes:
- `data` is always a JSON array, never `null` — nil results from `ListBackendsHealth` are normalized to `[]`
- `version` and `message` fields are omitted when empty (Go `omitempty` tag)
- Order matches backend registry iteration order
- Empty array `[]` is returned when no backends are registered: `{"success": true, "data": []}`

- **Response `401 Unauthorized`:**

```json
{
  "success": false,
  "error": "authentication required"
}
```

Returned when auth is enabled and no valid Bearer token is provided.

- **Response `500 Internal Server Error`:**

```json
{
  "success": false,
  "error": "failed to list backends"
}
```

Returned when the `BackendOps` implementation encounters an internal error. Note: the current implementation always returns `(result, nil)` — individual backend inspection errors are absorbed, not propagated. A 500 would only occur if `ListBackendsHealth` is modified to return errors in the future.

## Backend Configuration

Endpoints for reading and updating the AI backend configuration. Configuration exists at two levels:

- **Project-level** — stored in `loom.yaml` in the workspace directory (discovered via daemon status RPC)
- **Per-workspace override** — stored in `~/.loom/config.yaml` (global config, addressed by workspace UUID)

### Data Model: BackendConfigData

| Field | Type | Description |
|-------|------|-------------|
| `backend` | string | Current backend name (default: `"claude"`) |
| `source` | string | `"project"` if explicitly set in loom.yaml, `"default"` if using fallback |
| `available` | []string | All backends available for tab creation (includes `"shell"`) |
| `agents` | []AgentBackendOverride | Per-agent backend overrides from loom.yaml |

#### AgentBackendOverride

| Field | Type | Description |
|-------|------|-------------|
| `worktree` | string | Agent worktree name |
| `role` | string | Agent role |
| `backend` | string | Per-agent backend override (empty if using project default) |

**Valid backends for PATCH:** `"claude"`, `"codex"`, `"opencode"`, `"gemini"`, `"cursor"`. The `"shell"` backend appears in the `available` list but cannot be set via PATCH endpoints.

### `GET /api/config/backend`

Read the project-level backend configuration from `loom.yaml`.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "backend": "claude",
    "source": "default",
    "available": ["claude", "codex", "opencode", "gemini", "cursor", "shell"],
    "agents": []
  }
}
```

- `available` always contains the 5 AI backends plus `"shell"` (6 total)
- `agents` contains per-agent overrides from loom.yaml; empty array if no agents defined
- `source` is `"project"` when the backend field is explicitly set, `"default"` when using fallback

- **Errors:**
  - `500` — failed to parse config (loom.yaml YAML parse error)
  - `503` — connection pool not initialized or daemon not available
  - `504` — daemon not available (2s context timeout exceeded)

### `PATCH /api/config/backend`

Update the project-level backend in `loom.yaml`.

- **Auth:** Required
- **Request Body:**

```json
{
  "backend": "codex"
}
```

`backend` must be one of: `"claude"`, `"codex"`, `"opencode"`, `"gemini"`, `"cursor"`. The `"shell"` backend is rejected with `400`.

- **Response:** `200 OK`

Returns the full updated config in the same format as GET. `source` is always `"project"` after a successful PATCH.

```json
{
  "success": true,
  "data": {
    "backend": "codex",
    "source": "project",
    "available": ["claude", "codex", "opencode", "gemini", "cursor", "shell"],
    "agents": [
      {"worktree": "falcon", "role": "plan", "backend": "codex"}
    ]
  }
}
```

- **Errors:**
  - `400` — invalid request body (malformed JSON) or invalid backend name
  - `413` — request body too large (>1 MB)
  - `500` — `"failed to parse config: ..."` (covers both read and YAML parse errors from loom.yaml) or `"failed to save config"` (write error)
  - `503` — connection pool not initialized or daemon not available
  - `504` — daemon not available (2s context timeout exceeded)

- **Behavior Notes:**
  - Reads existing `loom.yaml` (or creates empty if absent), updates the `backend` field while preserving all other fields (`agents`, `daemon`, `roles`), and writes back to disk
  - Uses `yaml.Node` types for round-trip preservation of uninterpreted YAML fields

### `GET /api/workspaces/{ws}/config/backend`

Read backend configuration scoped to a specific workspace in multi-workspace mode. Behaves identically to `GET /api/config/backend` but routes the daemon connection through the workspace-specific pool via `WorkspaceMiddleware`.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID |

- **Response:** Same format and error codes as `GET /api/config/backend`
- Only available when MultiPool is configured (multi-workspace mode)

### `PATCH /api/workspaces/{ws}/config/backend`

Update backend configuration scoped to a specific workspace in multi-workspace mode. Behaves identically to `PATCH /api/config/backend` but routes through the workspace-specific daemon pool.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID |

- **Request Body, Response, and Errors:** Same as `PATCH /api/config/backend`
- Only available when MultiPool is configured (multi-workspace mode)

### `PATCH /api/workspace/{name}/config/backend`

Update a workspace's backend override in the global config (`~/.loom/config.yaml`). This is separate from the project-level endpoints — it sets a per-workspace backend preference in the multi-workspace configuration.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Workspace UUID (resolved to name via config lookup) |

- **Request Body:**

```json
{
  "backend": "codex"
}
```

`backend` must be non-empty and one of: `"claude"`, `"codex"`, `"opencode"`, `"gemini"`, `"cursor"`.

- **Response:** `200 OK` (with workspace config function)

```json
{
  "success": true,
  "data": {
    "name": "my-workspace",
    "path": "/home/user/projects/my-workspace",
    "repos": [],
    "groups": [],
    "agents": [],
    "workspaces": [
      {
        "name": "my-workspace",
        "path": "/home/user/projects/my-workspace",
        "active": true,
        "repo_count": 3,
        "is_default": false,
        "backend": "codex"
      }
    ],
    "default_workspace": ""
  }
}
```

The `data` field contains refreshed `WorkspaceData` (all slices normalized to non-nil). If no workspace config function is available, `data` is omitted:

```json
{
  "success": true
}
```

- **Errors:**
  - `400` — workspace ID required (empty path param), invalid request body (malformed JSON), backend is required (empty field), invalid backend name
  - `404` — no config found (`~/.loom/config.yaml` doesn't exist) or `"workspace with ID \"<id>\" not found"` (UUID not in workspaces map)
  - `413` — request body too large (>1 MB)
  - `500` — failed to load config (read/parse error) or failed to save config (write error)

- **Behavior Notes:**
  - Loads `~/.loom/config.yaml` using YAML round-trip preservation (updating one workspace's backend doesn't affect other workspaces, `workspace_order`, `default_workspace`, global `backend`, or `daemon` fields)
  - Saves atomically via temp file + rename (cleaned up on error)
  - Resolves the workspace by UUID from the path parameter, not by name

## Issues

> **Note:** All `/api/issues/...` routes documented below have been superseded by workspace-scoped equivalents at `/api/workspaces/{ws}/issues/...`. See [Multi-Workspace Endpoints](#multi-workspace-endpoints) for the workspace-scoped versions, which use identical request/response shapes but route through `WorkspaceMiddleware` for per-workspace isolation.

### `GET /api/issues`

List issues with filtering, pagination, and optional Kanban enrichment.

- **Auth:** Required
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `status` | string | Filter by status |
  | `type` | string | Filter by issue type |
  | `assignee` | string | Filter by assignee |
  | `q` | string | Search query |
  | `priority` | int (0-4) | Filter by priority |
  | `labels` | string | Comma-separated labels (all must match) |
  | `limit` | int | Max results (max 1000) |
  | `title_contains` | string | Substring match on title |
  | `description_contains` | string | Substring match on description |
  | `notes_contains` | string | Substring match on notes |
  | `created_after` | string | RFC3339 or YYYY-MM-DD |
  | `created_before` | string | RFC3339 or YYYY-MM-DD |
  | `updated_after` | string | RFC3339 or YYYY-MM-DD |
  | `updated_before` | string | RFC3339 or YYYY-MM-DD |
  | `empty_description` | bool | Only issues with empty description |
  | `no_assignee` | bool | Only unassigned issues |
  | `no_labels` | bool | Only issues without labels |
  | `pinned` | bool | Filter by pinned status |
  | `exclude_status` | string | Comma-separated statuses to exclude |
  | `include_blocked` | bool | Include blocked dependency info |

- **Response:** `200 OK`

Without `include_blocked=true`:
```json
{
  "success": true,
  "data": [
    {
      "issue": { "id": "abc", "title": "...", ... },
      "comment_count": 2,
      "dependency_count": 1,
      "parent": "parent-id",
      "parent_title": "Parent Issue Title"
    }
  ]
}
```

With `include_blocked=true`:
```json
{
  "success": true,
  "data": [
    {
      "issue": { ... },
      "comment_count": 2,
      "dependency_count": 1,
      "parent": "parent-id",
      "parent_title": "Parent Issue Title",
      "is_blocked": true,
      "blocked_by_count": 2,
      "blocked_by": ["dep-1", "dep-2"]
    }
  ]
}
```

### `GET /api/issues/{id}`

Get a single issue by ID.

- **Auth:** Required
- **Response:** `200 OK`, `404 Not Found`

```json
{
  "success": true,
  "data": { "id": "abc", "title": "...", ... }
}
```

### `POST /api/issues`

Create a new issue.

- **Auth:** Required
- **Request Body:**

```json
{
  "title": "string (required)",
  "issue_type": "bug|feature|task|epic|chore (required)",
  "priority": 0,
  "id": "custom-id (optional)",
  "parent": "parent-issue-id (optional)",
  "description": "string",
  "design": "string",
  "acceptance_criteria": "string",
  "notes": "string",
  "assignee": "string",
  "owner": "string",
  "created_by": "string",
  "external_ref": "string",
  "estimated_minutes": 60,
  "labels": ["label1", "label2"],
  "dependencies": ["issue-id-1"],
  "due_at": "2024-01-15T00:00:00Z",
  "defer_until": "2024-01-15T00:00:00Z"
}
```

- **Validation:** Max 50 labels, max 100 dependencies, priority 0-4
- **Response:** `201 Created`, `400 Bad Request`

### `PATCH /api/issues/{id}`

Partial update of an issue. All fields are optional.

- **Auth:** Required
- **Request Body:**

```json
{
  "title": "string",
  "description": "string",
  "status": "string",
  "priority": 0,
  "assignee": "string",
  "design": "string",
  "acceptance_criteria": "string",
  "notes": "string",
  "external_ref": "string",
  "estimated_minutes": 60,
  "issue_type": "bug|feature|task|epic|chore",
  "add_labels": ["new-label"],
  "remove_labels": ["old-label"],
  "set_labels": ["exact-labels"],
  "pinned": true,
  "parent": "parent-id",
  "due_at": "2024-01-15T00:00:00Z",
  "defer_until": "2024-01-15T00:00:00Z"
}
```

- **Response:** `200 OK`, `404 Not Found`, `409 Conflict`

```json
{
  "success": true,
  "data": {"id": "abc", "status": "updated"}
}
```

### `POST /api/issues/{id}/close`

Close an issue.

- **Auth:** Required
- **Request Body:** (optional)

```json
{
  "reason": "string",
  "session": "string",
  "suggest_next": false,
  "force": false
}
```

- **Response:** `200 OK`, `404 Not Found`, `409 Conflict` (has open blockers when `force=false`)

### `POST /api/issues/{id}/comments`

Add a comment to an issue.

- **Auth:** Required
- **Request Body:**

```json
{"text": "Comment text (required, max 64KB)"}
```

- **Response:** `201 Created`, `400 Bad Request`, `404 Not Found`

```json
{
  "success": true,
  "data": {
    "id": "comment-id",
    "text": "Comment text",
    "author": "web-ui",
    "created_at": "2024-01-15T12:00:00Z"
  }
}
```

### `GET /api/ready`

Issues ready to work on (open/in_progress with no blockers).

- **Auth:** Required
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `assignee` | string | Filter by assignee |
  | `type` | string | Filter by issue type |
  | `parent_id` | string | Filter by parent epic |
  | `mol_type` | string | swarm, patrol, or work |
  | `sort` | string | hybrid, priority, or oldest |
  | `unassigned` | bool | Only unassigned issues |
  | `include_deferred` | bool | Include deferred issues |
  | `priority` | int (0-4) | Filter by priority |
  | `limit` | int | Max results |
  | `labels` | string | Comma-separated labels (all must match) |
  | `labels_any` | string | Comma-separated labels (any must match) |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": [
    {
      "id": "abc",
      "title": "...",
      "status": "open",
      "parent": "parent-id",
      "parent_title": "Parent Title"
    }
  ]
}
```

### `GET /api/blocked`

Issues that are blocked by other issues.

- **Auth:** Required
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `parent_id` | string | Filter by parent |
  | `assignee` | string | Filter by assignee |
  | `type` | string | Filter by issue type |
  | `priority` | int (0-4) | Filter by priority |
  | `limit` | int | Max results (max 1000) |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": [
    {
      "id": "abc",
      "title": "...",
      "blocked_by_count": 2,
      "blocked_by": ["dep-1", "dep-2"]
    }
  ]
}
```

### `GET /api/issues/graph`

Dependency graph data for visualization.

- **Auth:** Required
- **Query Parameters:**
  | Parameter | Type | Default | Description |
  |-----------|------|---------|-------------|
  | `status` | string | `all` | all, open, or closed |
  | `include_closed` | bool | `true` | Include closed issues; only applies when status=all, ignored otherwise |

- **Response:** `200 OK`

```json
{
  "success": true,
  "issues": [
    {
      "id": "abc",
      "title": "...",
      "status": "open",
      "priority": 2,
      "issue_type": "task",
      "labels": ["frontend"],
      "dependencies": [
        {"depends_on_id": "def", "type": "blocks"}
      ],
      "defer_until": "",
      "due_at": ""
    }
  ]
}
```

## Dependencies

### `POST /api/issues/{id}/dependencies`

Add a dependency (make `{id}` depend on another issue).

- **Auth:** Required
- **Request Body:**

```json
{
  "depends_on_id": "other-issue-id (required)",
  "dep_type": "blocks (optional, defaults to 'blocks')"
}
```

- **Validation:** No self-dependencies, circular dependency detection
- **Response:** `200 OK`, `400 Bad Request`, `404 Not Found`, `409 Conflict` (cycle or already exists)

### `DELETE /api/issues/{id}/dependencies/{depId}`

Remove a dependency.

- **Auth:** Required
- **Response:** `200 OK`, `404 Not Found`

```json
{"success": true, "data": null}
```

## Workspace

Workspace endpoints manage workspace lifecycle: creating (empty or clone), reading topology, renaming, deleting, reordering, setting defaults, and updating per-workspace backend configuration. All workspace config is YAML-backed (`~/.loom/config.yaml`) with atomic temp-file + rename writes.

### Response Envelope

All workspace CRUD endpoints use this envelope:

```json
{
  "success": true,
  "data": { /* WorkspaceData object */ },
  "error": "message (only on failure)"
}
```

Mutation endpoints (create, rename, delete, reorder, set/clear default) return refreshed workspace topology in the `data` field on success.

### Data Models

#### WorkspaceData

Full workspace topology returned by `GET /api/workspaces/active` and `GET /api/workspaces/{ws}`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Workspace UUID |
| `name` | string | Workspace display name |
| `path` | string | Absolute filesystem path |
| `repos` | []WorkspaceRepo | Repositories in this workspace |
| `groups` | []string | Defined repo groups |
| `agents` | []WorkspaceAgentInfo | Agent assignments |
| `workspaces` | []WorkspaceSummary | All configured workspaces |
| `workspace_order` | []string | Custom display ordering (omitted if empty) |
| `default_workspace` | string | Name of the default workspace |

All array fields marshal as `[]` (never `null`).

#### WorkspaceSummary

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Workspace UUID |
| `name` | string | Workspace name |
| `path` | string | Absolute filesystem path |
| `active` | bool | Whether this is the currently selected workspace |
| `repo_count` | int | Number of repositories |
| `is_default` | bool | Whether this is the default workspace |
| `backend` | string | AI backend override (omitted if not set) |

#### WorkspaceRepo

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Repository name |
| `path` | string | Absolute filesystem path |
| `default_branch` | string | Default git branch |
| `remote` | string | Git remote URL |
| `source_repo_id` | string | Source repo identifier (omitted if empty) |
| `groups` | []string | Group memberships |

#### WorkspaceAgentInfo

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Agent name |
| `repos` | []string | Assigned repository names |
| `repo_groups` | []string | Assigned repo group names |
| `cross_repo` | bool | Whether agent works across repos |

### Validation Rules

- **Workspace names:** alphanumeric, hyphens, underscores only; max 64 characters
- **Clone URLs:** must start with `https://` or `git@`; no control characters (`\x00`, `\n`, `\r`); no path segments starting with `-` (flag injection prevention)
- **Request body limit:** 1 MB (all endpoints)

### `GET /api/workspaces/active`

Returns the full workspace topology for the active (default) workspace.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "id": "abc-123",
    "name": "my-workspace",
    "path": "/home/user/workspace",
    "repos": [
      {
        "name": "myrepo",
        "path": "/home/user/workspace/myrepo",
        "default_branch": "main",
        "remote": "https://github.com/org/myrepo",
        "groups": ["backend"]
      }
    ],
    "groups": ["backend", "frontend"],
    "agents": [
      {
        "name": "nova",
        "repos": ["myrepo"],
        "repo_groups": ["backend"],
        "cross_repo": false
      }
    ],
    "workspaces": [
      {
        "id": "abc-123",
        "name": "my-workspace",
        "path": "/home/user/workspace",
        "active": true,
        "repo_count": 1,
        "is_default": true,
        "backend": "claude"
      }
    ],
    "workspace_order": ["my-workspace"],
    "default_workspace": "my-workspace"
  }
}
```

- **Response (single-repo mode):** When `configFn` is nil, returns empty workspace with all arrays initialized to `[]`:
  ```json
  {"success": true, "data": {"repos": [], "groups": [], "agents": [], "workspaces": []}}
  ```
- **Errors:**
  - `500` — failed to load workspace config

### `POST /api/workspaces`

Create a new workspace.

- **Auth:** Required
- **Request Body:**

```json
{
  "name": "string (required)",
  "type": "empty|clone|template (required)",
  "repos": ["path/to/repo"],
  "clone_url": "https://github.com/org/repo",
  "clone_urls": ["https://github.com/org/repo1", "git@github.com:org/repo2"],
  "branch": "string (optional)",
  "path": "string (optional workspace directory)"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Workspace name (alphanumeric, hyphens, underscores; max 64 chars) |
| `type` | string | yes | `"empty"`, `"clone"`, or `"template"` |
| `repos` | []string | for empty | Repository paths (required when type is `"empty"`) |
| `clone_url` | string | no | Single git URL (backward compat; merged into `clone_urls` if empty) |
| `clone_urls` | []string | for clone | Git URLs to clone (at least one required when type is `"clone"`) |
| `branch` | string | no | Branch name to check out after clone |
| `path` | string | no | Custom workspace directory path |

- **Timeout:** 60 seconds
- **Response:** `201 Created`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `400` — invalid name, missing required fields, invalid clone URL
  - `413` — request body too large (>1 MB)
  - `501` — template type not supported, or workspace creation not available
  - `504` — creation timed out (>60 seconds)
  - `500` — creation failed

### `PATCH /api/workspaces/{ws}/name`

Rename a workspace. The workspace is identified by UUID via WorkspaceMiddleware.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID |

- **Request Body:**

```json
{
  "new_name": "string (required)"
}
```

- **Behavior:** Updates the workspace map key, `default_workspace` (if the renamed workspace was default), and `workspace_order`. No-op if `new_name` equals the current name (returns `200` with no `data` field). Atomic config write.
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `400` — workspace ID required, empty name, name too long (>64 chars), invalid characters
  - `404` — workspace not found, no config found
  - `409` — workspace name already exists
  - `413` — request body too large
  - `500` — failed to load/save config

### `DELETE /api/workspaces/{ws}`

Remove a workspace from config. Does NOT delete worktrees or files from disk. Deregisters connection pool and fleet store.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `400` — workspace ID required
  - `404` — workspace not found
  - `409` — workspace has running agents
  - `501` — workspace deletion not available
  - `500` — failed to resolve or delete workspace

### `PUT /api/workspaces/order`

Persist custom workspace display order.

- **Auth:** Required
- **Request Body:**

```json
{
  "order": ["workspace-a", "workspace-b", "uuid-c"]
}
```

Accepts both workspace names and UUIDs. UUIDs are resolved to names internally. Unknown entries are silently filtered. Duplicates are deduplicated.

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `400` — invalid JSON
  - `404` — no config found
  - `413` — request body too large
  - `500` — failed to load/save config

### `PUT /api/workspaces/default`

Set the default workspace.

- **Auth:** Required
- **Request Body:**

```json
{
  "name": "workspace-name (required)"
}
```

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `400` — name is required
  - `404` — workspace not found
  - `501` — set default not available
  - `500` — failed to save config

### `DELETE /api/workspaces/default`

Clear the default workspace, reverting to first-in-order behavior.

- **Auth:** Required
- **No request body**
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData */ }
}
```

- **Errors:**
  - `501` — clear default not available
  - `500` — failed to save config

### `PATCH /api/workspaces/{ws}/config/backend`

Update a workspace's AI backend. Routes through the workspace's daemon connection pool to update the project-level backend in `loom.yaml`.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID |

- **Request Body:**

```json
{
  "backend": "claude"
}
```

Valid backends: `"claude"`, `"codex"`, `"opencode"`, `"gemini"`, `"cursor"`.

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "backend": "claude",
    "source": "project",
    "available": ["claude", "codex", "opencode", "gemini", "cursor", "shell"],
    "agents": [
      {"worktree": "nova", "role": "coder", "backend": "claude"}
    ]
  }
}
```

- **Errors:**
  - `400` — invalid backend value
  - `413` — request body too large
  - `500` — failed to parse/save config
  - `503` — connection pool not initialized
  - `504` — daemon not available (timeout)

## Multi-Workspace

These endpoints are only available when MultiPool is configured (multi-workspace mode). They provide workspace listing with per-workspace connection pool statistics.

### `GET /api/workspaces`

List all registered workspaces with pool stats.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "workspaces": [
    {
      "id": "abc-123",
      "name": "my-workspace",
      "path": "/home/user/workspace",
      "active": true,
      "pool": {
        "size": 5,
        "created": 5,
        "active": 2,
        "available": 3,
        "closed": false
      }
    }
  ]
}
```

Note: This endpoint uses a `"workspaces"` key (not `"data"`). The `pool` field is omitted when pool stats are unavailable.

#### PoolStats

| Field | Type | Description |
|-------|------|-------------|
| `size` | int | Configured pool size |
| `created` | int | Total connections created |
| `active` | int | Connections currently in use |
| `available` | int | Connections available in pool |
| `closed` | bool | Whether the pool is shut down |

### `GET /api/workspaces/{ws}`

Get a single workspace's full topology by ID. Returns the same `WorkspaceData` shape as `GET /api/workspaces/active`.

- **Auth:** Required
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace UUID or name |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": { /* WorkspaceData — same shape as /api/workspaces/active */ }
}
```

The `active` flag in each `WorkspaceSummary` is set to `true` for the workspace matching the `ws` path parameter.

- **Errors:**
  - `400` — workspace ID is required (empty path param)
  - `404` — workspace not found
  - `500` — failed to load workspace config

## Issue Session History

Workspace-scoped endpoints for querying session history records linked to issues. These are backed by Redis via `sessionhistory.Store` and track terminal sessions associated with specific issues (started by users or `start-work`).

### `GET /api/workspaces/{ws}/issues/{issueId}/sessions`

List all session history records for an issue.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID, validated by WorkspaceMiddleware) |
| issueId | string | yes | Issue ID (validated: `^[a-zA-Z0-9._-]+$`) |

- **Behavior:** Returns all session history records for the specified issue in the given workspace, sorted by `started_at` descending (most recent first). Returns empty array (not null) for unknown issues.
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": [
    {
      "id": "issue-proj-1:1700000000",
      "session_name": "issue-proj-1",
      "issue_id": "proj.1",
      "backend": "claude",
      "status": "active",
      "launcher": "user",
      "started_at": "2025-01-15T10:00:00Z"
    }
  ]
}
```

- Completed sessions include `ended_at` and optionally `scrollback_path`
- **Errors:**
  - `400` — invalid workspace ID (empty) or invalid issue ID (empty or fails regex)
  - `404` — workspace not found (from middleware)
  - `500` — Redis list failure
  - `503` — session history not available (no Redis)
- **Conditional registration:** Only registered when `sessionHistoryStore != nil`

### `GET /api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback`

Retrieve terminal scrollback content for a completed session.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID) |
| issueId | string | yes | Issue ID (validated: `^[a-zA-Z0-9._-]+$`) |
| recordId | string | yes | Session record ID (must be non-empty) |

- **Behavior:** Finds the record by ID within the issue's session history, reads the scrollback file from disk. Path-traversal protection: scrollback file must be under `~/.loom/session-scrollback/` after `filepath.Clean`.
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "content": "terminal output text...",
    "lines": 42
  }
}
```

- `content`: full scrollback text as a single string
- `lines`: line count (newline-delimited, +1 for non-empty content)
- **Errors:**
  - `400` — invalid issue ID, empty record ID, or invalid scrollback path (path traversal attempt)
  - `404` — workspace not found (middleware), session record not found, no scrollback available (`scrollback_path` empty), or scrollback file not found on disk
  - `500` — Redis get failure or file read failure
  - `503` — session history not available (no Redis)
- **Security:** Scrollback path cleaned via `filepath.Clean` and validated to start with `~/.loom/session-scrollback/` prefix. Paths outside this directory are rejected with `400`.

## Real-time Events (SSE)

### `GET /api/events`

Server-Sent Events stream for real-time mutation notifications.

- **Auth:** Required (via bearer token header or `?token=` query param)
- **Headers:**
  - `Last-Event-ID`: Resume from last seen event ID for catch-up
- **Query Parameters:**
  - `since`: Catch-up from specific timestamp (Unix milliseconds)

**Protocol details:**

- Content-Type: `text/event-stream`
- Retry interval: 5000 ms
- Heartbeat: SSE comment every 30 seconds

**Initial connection event:**

```
event: connected
data: {"clientId":1}
```

**Mutation events:**

```
id: 1705312800001
event: mutation
data: {"type":"update","issue_id":"abc","title":"...","assignee":"user","actor":"web-ui","timestamp":"2024-01-15T12:00:00Z","old_status":"open","new_status":"in_progress","parent_id":"","step_count":0}
```

**Mutation types:** `create`, `update`, `delete`, `comment`, `status`, `bonded`, `squashed`, `burned`

## Fleet Coordination

Fleet endpoints enable multi-server coordination where remote workers register with a pre-shared API key, receive a JWT for subsequent authenticated requests, atomically claim tasks, report task completion, and send periodic heartbeats. Fleet endpoints use their own authentication flow separate from the standard bearer token auth.

Workspace-scoped variants of all fleet endpoints are also available at `/api/workspaces/{ws}/fleet/...` when multi-workspace mode is configured. These use `WorkspaceMiddleware` for routing and `StoreRegistry` for per-workspace fleet store resolution.

### Prerequisites

Fleet endpoints are only registered when `fleetEnabled` is true — this requires Redis to be configured (`--fleet-redis`) and a fleet API key (`--fleet-api-key`). When these flags are not set, all fleet routes return `404` (not registered).

### Authentication Model

Fleet endpoints use a two-layer authentication model:

1. **Registration auth (`X-Fleet-API-Key`):** The register endpoint validates a pre-shared API key via the `X-Fleet-API-Key` header using constant-time comparison (`crypto/subtle.ConstantTimeCompare`). This is the bootstrap mechanism.
2. **JWT bearer auth (`Authorization: Bearer`):** After registration, the worker receives a JWT (HMAC-SHA256, default 1-hour expiry). The claim, heartbeat, and done endpoints are protected by `FleetAuthMiddleware` which validates this JWT and injects `WorkerClaims` into the request context.

The JWT signing key is managed in Redis via `SigningKeyManager` (supports key rotation with previous-version grace period). When no signing key is configured, the JWT middleware is not applied to claim, heartbeat, or done routes.

### Data Models

#### Worker

| Field | Type | Description |
|-------|------|-------------|
| `worker_id` | string | Unique worker identifier (max 256 chars) |
| `repos` | []string | Repository names the worker handles |
| `registered_at` | int64 | Unix timestamp of registration |

Redis key: `fleet:workers:{workerID}`, TTL: 2 hours.

#### ClaimResponse

| Field | Type | Description |
|-------|------|-------------|
| `task_id` | string | Claimed task identifier |
| `success` | bool | Whether the claim succeeded |
| `payload` | object | Task payload (raw JSON) |

Redis key: `fleet:worker:claim:{workerID}`, TTL: 5 minutes.

#### TaskResult

| Field | Type | Description |
|-------|------|-------------|
| `worker_id` | string | Worker that completed the task |
| `task_id` | string | Task identifier |
| `success` | bool | Whether the task succeeded |
| `commit_sha` | string | Git commit SHA (optional, for successful tasks) |
| `error` | string | Error message (optional, for failed tasks) |
| `completed_at` | string | RFC 3339 timestamp |

Redis key: `fleet:task:result:{taskID}`, TTL: 24 hours.

#### JWT Claims

```json
{
  "worker_id": "string",
  "repos": ["string"],
  "iat": 1705312800,
  "exp": 1705316400
}
```

Algorithm: HMAC-SHA256. Default expiry: 1 hour.

### `POST /api/workspaces/{ws}/fleet/register`

Register a fleet worker and obtain a JWT for subsequent authenticated requests.

- **Auth:** `X-Fleet-API-Key` header (constant-time validated against `--fleet-api-key`)
- **Rate Limit:** Per-IP sliding window rate limiting (when Redis-based `FleetRateLimiter` is configured). Uses `RemoteAddr` only — `X-Forwarded-For` is not trusted. Fails open on Redis errors (availability over strictness).
- **Request Body:**

```json
{
  "worker_id": "string (required, max 256 chars)",
  "repos": ["repo-name (optional)"]
}
```

Worker IDs containing colons, newlines, tabs, or spaces are rejected by the store layer during registration (returns `500`, not `400`).

- **Response:** `201 Created`

```json
{"success": true, "token": "<JWT>"}
```

Re-registration is idempotent — it updates the timestamp and repos, and issues a new JWT.

- **Errors:**
  - `400` — `worker_id` missing, empty, or exceeds 256 characters; invalid/malformed request body
  - `401` — missing `X-Fleet-API-Key` header, or invalid API key
  - `413` — request body too large (>1 MB)
  - `429` — rate limit exceeded (per-IP)
  - `500` — Redis registration failure, or JWT generation failure
  - `503` — fleet store or token config is nil (`"fleet API not available"`), or API key not configured (`"fleet authentication not configured"`)

- **Timeout:** 30-second context timeout on Redis registration

### `POST /api/workspaces/{ws}/fleet/claim`

Atomically claim a task to work on.

- **Auth:** JWT bearer token (from registration) — validated by `FleetAuthMiddleware` when signing key is configured; no auth when signing key is not configured
- **Request Body:** (optional — can be empty or omitted entirely)

```json
{
  "issue_id": "string (optional — claim a specific issue)",
  "status": "string (accepted but not currently forwarded to the RPC layer — has no effect)",
  "issue_type": "string (optional — filter by issue type)",
  "max_priority": 2
}
```

If the body is empty or `Content-Length` is 0, the endpoint finds the highest-priority ready task automatically.

- **Behavior:**
  - **Specific issue (`issue_id` provided):** Attempts to atomically claim the specified issue via RPC Update with `Claim=true`, setting status to `"in_progress"`. Returns `409` if already claimed by another worker.
  - **Auto-assignment (no `issue_id`):** Calls RPC Ready to fetch up to 10 candidate tasks (filtered by optional `issue_type`, `max_priority`), then iterates through them attempting to claim each one. Returns the first successfully claimed task. Returns `204` if no tasks are available or all candidates are already claimed.

- **Response:** `200 OK` (task claimed)

```json
{
  "success": true,
  "payload": {
    "issue": {
      "id": "string",
      "title": "string",
      "status": "in_progress",
      "labels": ["string"]
    },
    "labels": ["string"],
    "dependencies": [],
    "reason": "",
    "deadline": null
  }
}
```

- **Errors:**
  - `204` — no tasks available (no body returned)
  - `400` — invalid/malformed request body
  - `401` — missing/invalid JWT (when `FleetAuthMiddleware` is active)
  - `404` — specific `issue_id` not found (response body `error` field reads `"internal server error"`)
  - `409` — specific issue already claimed by another worker
  - `413` — request body too large (>1 MB)
  - `500` — RPC error, daemon error, or response parse failure
  - `503` — connection pool not initialized
  - `504` — timeout acquiring daemon connection (5-second deadline)

- **Metrics:** Records claim outcomes (`success`, `collision`, `timeout`) via `ClaimMetrics`.

### `POST /api/workspaces/{ws}/fleet/done/{id}`

Mark a task as complete. The `{id}` path parameter is the worker ID.

- **Auth:** JWT bearer token (from registration) — validated by `FleetAuthMiddleware` when signing key is configured; no auth when signing key is not configured
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Worker ID (from `r.PathValue("id")`) |

- **Request Body:**

```json
{
  "success": true,
  "commit_sha": "abc123 (optional, for successful tasks)",
  "error": "failure reason (optional, for failed tasks)"
}
```

- **Behavior:**
  1. Validates worker exists in Redis (`GetWorker`)
  2. Looks up worker's current claim (`GetWorkerClaim`)
  3. If no active claim, returns success idempotently (no `task_id` in response)
  4. Records task result in Redis (24-hour TTL) via `RecordTaskResult`
  5. Releases the claim key via `ReleaseClaim`
  6. Clears worker claim cache (best-effort, uses fresh 2-second context)

- **Response:** `200 OK` (task completed)

```json
{
  "success": true,
  "task_id": "claimed-task-id",
  "worker_id": "worker-id"
}
```

- **Response:** `200 OK` (idempotent — no active claim)

```json
{
  "success": true,
  "worker_id": "worker-id"
}
```

- **Errors:**
  - `401` — missing/invalid JWT (when `FleetAuthMiddleware` is active)
  - `400` — missing worker ID in path, invalid/malformed request body
  - `404` — worker not found in Redis
  - `413` — request body too large (>1 MB)
  - `500` — Redis lookup/write failure (get worker, get claim, record result, release claim)
  - `503` — fleet store is nil (`"fleet API not available"`)

- **Timeout:** 5-second context timeout for main operations; 2-second fresh context for claim cache cleanup

### `POST /api/workspaces/{ws}/fleet/heartbeat`

Refresh a worker's registration TTL to keep it alive.

- **Auth:** JWT bearer token (from registration) — validated by `FleetAuthMiddleware` when signing key is configured
- **Request Body:**

```json
{
  "worker_id": "string (required, max 256 chars)"
}
```

- **Behavior:** Refreshes the worker's registration TTL in Redis back to 2 hours. Uses `EXPIRE` (not `SET`), so it only succeeds if the worker registration key still exists. Without heartbeats, the registration expires after the initial 2-hour TTL.

- **Response:** `200 OK`

```json
{
  "success": true,
  "last_heartbeat": "2024-01-15T12:00:00Z"
}
```

- **Errors:**
  - `400` — `worker_id` missing, empty, exceeds 256 characters, or invalid/malformed request body
  - `404` — worker not found (registration expired or never registered)
  - `413` — request body too large (>1 MB)
  - `500` — Redis heartbeat failure
  - `503` — fleet store is nil (`"fleet store not initialized"`)

- **Timeout:** 2-second context timeout on Redis heartbeat update

### Redis Key Reference

All keys use the `fleet:` prefix. When workspace-scoped (`Store.workspaceID` is set), keys are prefixed with `fleet:{wsID}:` instead.

| Key Pattern | TTL | Description |
|-------------|-----|-------------|
| `fleet:workers:{workerID}` | 2 hours | Worker registration data |
| `fleet:tasks:claimed:{taskID}` | 5 minutes | Claimed task ownership |
| `fleet:worker:claim:{workerID}` | 5 minutes | Cached claim response for worker |
| `fleet:task:result:{taskID}` | 24 hours | Task completion result |
| `fleet:ratelimit:{ip}` | sliding window | Per-IP rate limit tracking |

### Timeout Enforcement

`TimeoutEnforcer` runs a background loop (default: check every 1 minute, timeout after 30 minutes) that releases claims and invokes a callback for timed-out tasks. Claim TTL can be extended via `ExtendClaimTTL` after beads confirmation.

| Endpoint | Timeout | Purpose |
|----------|---------|---------|
| Register | 30s | Worker registration to Redis |
| Claim | 5s | Acquire daemon connection |
| Done | 5s main, 2s cleanup | Record result and release claim |
| Heartbeat | 2s | Update worker TTL in Redis |

## Log Streaming

### `GET /api/agents/{name}/logs`

Get recent log lines for an agent.

- **Auth:** Required
- **Path Parameters:** `name` — agent name (alphanumeric, hyphens, underscores)
- **Query Parameters:** `lines` — number of lines (default 200, max 10000)
- **Response:** `200 OK`, `400` (invalid name), `404` (log not found)

```json
{
  "success": true,
  "data": {
    "lines": ["line 1", "line 2"],
    "line_count": 200
  }
}
```

### `GET /api/agents/{name}/logs/stream`

Real-time agent log streaming via SSE.

- **Auth:** Required
- **Query Parameters:** `since` — start from specific line number
- **Event Format:**

```
event: log-line
data: {"line":"log content","line_number":42,"timestamp":"2024-01-15T12:00:00Z"}
```

### `GET /api/tasks/{id}/logs`

List available log phases for a task.

- **Auth:** Required
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "phases": ["planning", "implementation"]
  }
}
```

### `GET /api/tasks/{id}/logs/{phase}`

Get task log content for a specific phase.

- **Auth:** Required
- **Path Parameters:** `phase` — `planning` or `implementation`
- **Query Parameters:** `lines` — number of lines (default 200, max 10000)
- **Response:** Same shape as agent logs

### `GET /api/tasks/{id}/logs/{phase}/stream`

Real-time task log streaming via SSE.

- **Auth:** Required
- **Query Parameters:** `since` — start from specific line number
- **Event Format:** Same as agent log stream

## Session Audit Trail

Workspace-scoped endpoints for the file-backed session audit trail. These track per-task session metadata (tokens, cost, files changed) via `sessions.Store` and provide transcript and diff retrieval. The underlying store is global (not workspace-isolated), but routes are workspace-scoped for access control via WorkspaceMiddleware.

### `GET /api/workspaces/{ws}/tasks/{taskId}/sessions`

List all sessions for a task.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID, validated by WorkspaceMiddleware) |
| taskId | string | yes | Task ID (validated: `^[a-zA-Z0-9._-]+$`, must be non-empty) |

- **Behavior:** Lists all sessions for a task from the file-backed session index. For each session, computes `is_active`, `has_transcript`, and `has_diff` by checking status and probing file existence. Deduplicates by `session_id` (last-seen wins). Auto-heals stale running sessions (>2h old) by marking them as "aborted".
- **Note:** The handler does not use workspace context internally — the file-based store is global. Workspace validation is handled by the middleware for URL consistency and access control.
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "task_id": "bd-xyz789",
    "sessions": [
      {
        "session_id": "20260322-100000-spark-xyz78-a1b2c3d4",
        "task_id": "bd-xyz789",
        "agent_name": "spark",
        "backend": "claude",
        "model": "claude-sonnet-4-6",
        "phase": "implementation",
        "started_at": "2026-03-22T10:00:00Z",
        "ended_at": "2026-03-22T10:15:00Z",
        "duration_s": 900.0,
        "status": "completed",
        "exit_code": 0,
        "input_tokens": 50000,
        "output_tokens": 12000,
        "estimated_cost_usd": 0.15,
        "files_changed": 3,
        "lines_added": 45,
        "lines_removed": 12,
        "is_active": false,
        "has_transcript": true,
        "has_diff": true
      }
    ]
  }
}
```

- `sessions` is always an empty array `[]` (never null)
- `is_active` is `true` only when `status == "running"`
- `has_transcript` is `true` when `transcript.jsonl` exists and has entries
- `has_diff` is `true` when `diff.patch` exists and is non-empty
- **Errors:**
  - `400` — missing or invalid task ID, or empty workspace ID (middleware)
  - `404` — workspace not found (middleware)
  - `500` — failed to list sessions (index file read error)
  - `503` — session store not available (`sessStore` is nil)
- **Registration:** Unconditional on wsMux; handler returns 503 when `sessStore` is nil

### `GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}`

Get metadata for a single session.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID) |
| taskId | string | yes | Task ID (validated: `^[a-zA-Z0-9._-]+$`) |
| sessionId | string | yes | Session ID (validated: `^[a-zA-Z0-9._-]+$`, must be non-empty) |

- **Behavior:** Returns full metadata for a single session. Enforces task ownership — `session.TaskID` must match `taskId`, otherwise returns 404 (prevents cross-task access).
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "session_id": "20260322-100000-spark-xyz78-a1b2c3d4",
    "task_id": "bd-xyz789",
    "agent_name": "spark",
    "backend": "claude",
    "status": "completed",
    "exit_code": 0,
    "is_active": false,
    "last_error": ""
  }
}
```

- Includes all SessionRecord fields plus `is_active` (computed) and `last_error`
- **Errors:**
  - `400` — invalid task ID or invalid session ID
  - `404` — workspace not found (middleware), session not found (`metadata.json` missing), or task ownership mismatch
  - `500` — failed to load session (metadata read/parse error)
  - `503` — session store not available

### `GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript`

Get all transcript entries for a session.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID) |
| taskId | string | yes | Task ID (validated) |
| sessionId | string | yes | Session ID (validated) |

- **Behavior:** Returns all transcript entries sorted by `seq` ascending. Enforces task ownership before returning data. Corrupt JSONL lines are silently skipped (logged server-side).
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "session_id": "20260322-100000-spark-xyz78-a1b2c3d4",
    "entries": [
      {
        "seq": 1,
        "ts": "2026-03-22T10:00:01Z",
        "role": "user",
        "type": "text",
        "content": "Implement the feature..."
      },
      {
        "seq": 2,
        "ts": "2026-03-22T10:00:05Z",
        "role": "assistant",
        "type": "tool_use",
        "tool_name": "Read",
        "tool_input": "{\"file_path\": \"/path/to/file.go\"}"
      }
    ]
  }
}
```

- `entries` is always empty array `[]` (never null) when no transcript exists
- Entries sorted by `seq` ascending
- **Errors:**
  - `400` — invalid task ID or invalid session ID
  - `404` — workspace not found (middleware), session not found, or task ownership mismatch
  - `500` — failed to load session or transcript
  - `503` — session store not available

### `GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff`

Get the raw diff patch content for a session.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (UUID) |
| taskId | string | yes | Task ID (validated) |
| sessionId | string | yes | Session ID (validated) |

- **Behavior:** Returns raw `diff.patch` content as plain text (not JSON). Enforces task ownership.
- **Response:** `200 OK`, Content-Type: `text/plain`, body: raw unified diff text
- **Errors:**
  - `400` — invalid task ID or invalid session ID (JSON error)
  - `404` — workspace/session not found, task mismatch, or `diff.patch` not found (JSON error)
  - `500` — failed to load session or read diff (JSON error)
  - `503` — session store not available (JSON)
- **Note:** Only endpoint returning plain text on success; all errors still use JSON envelope.

### `POST /api/sessions/notify`

Push a session status change as an SSE event. Loopback-only — **not workspace-scoped** (stays on the main mux).

- **Auth:** Loopback-only — restricted to `127.0.0.1`/`::1` via `RemoteAddr` check. Returns `403` for non-loopback. No bearer token required.
- **Request Body:**

```json
{
  "task_id": "string (required)",
  "session_id": "string (required)",
  "status": "string (optional)",
  "workspace_id": "string (optional, used for SSE broadcast scoping)"
}
```

- **Behavior:** Broadcasts a `session_change` SSE event to connected web UI clients. Fire-and-forget. The `workspace_id` in the body is forwarded to the SSE broadcast payload for client-side filtering. If `workspace_id` is empty, a warning is logged but the broadcast still fires.
- **SSE broadcast payload:**

```json
{
  "type": "session_change",
  "issue_id": "<task_id from request>",
  "new_status": "<status from request>",
  "timestamp": "RFC3339",
  "workspace_id": "<workspace_id from request>"
}
```

- **Response:** `204 No Content` (success)
- **Errors:**
  - `400` — malformed JSON or missing `task_id`/`session_id` (plain text error)
  - `403` — non-loopback caller (plain text error)
- **Registration:** Only when SSE hub is non-nil. Errors use plain text `http.Error`, not JSON envelope.

## Terminal Session Lifecycle

REST + WebSocket API for terminal session lifecycle management, using workspace-scoped routes. These endpoints govern creating, connecting to, monitoring, restarting, killing, and seeding terminal sessions backed by tmux.

### Workspace Scoping

All session lifecycle endpoints use the `/api/workspaces/{ws}/terminal/...` prefix. The `{ws}` path parameter is the workspace identifier (UUID). WorkspaceMiddleware validates the workspace exists and injects the ID into the request context.

| Status | Condition | Body |
|--------|-----------|------|
| `400` | `{ws}` is empty or whitespace | `{"error": "workspace ID is required"}` |
| `404` | `{ws}` does not match any registered workspace | `{"error": "workspace not found: {ws}"}` |

### Cross-Cutting Validation

- **Session names**: regex `^[a-zA-Z0-9_-]+$` — enforced on all endpoints that accept session names. Dots in session names (from issue IDs) are sanitized to dashes before validation.
- **Request body limit**: 1 MB (standard `maxRequestBody`)

### Authentication: One-Time Tokens

- **Standard endpoints**: Bearer token via Authorization header, plus WorkspaceMiddleware validation
- **Token endpoint** (`GET .../terminal/token`): requires standard bearer auth, returns a one-time terminal token
- **WebSocket endpoint** (`GET .../terminal/ws`): uses one-time token via `?token=` query param. Standard bearer auth is not required for the WebSocket upgrade — the one-time token carries session identity and authorization.
- **Restart/Kill/Session-status**: require both bearer auth AND one-time terminal token via `?token=` query param
- **Token format**: `base64url(JSON{session, exp, nonce}).base64url(HMAC-SHA256-signature)`
- **Token lifetime**: 60 seconds, single-use (nonce tracked server-side in memory)
- **Server restart**: generates new HMAC secret, all old tokens become invalid (by design)

### `GET /api/workspaces/{ws}/terminal/token`

Generate a one-time HMAC-SHA256 terminal authentication token.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (validated by WorkspaceMiddleware) |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| session | string | yes | Session name (validated against session name regex) |

- **Response Headers:** `Cache-Control: no-store`
- **Response:** `200 OK`

```json
{"token": "<one-time-use-token>"}
```

- **Errors:**
  - `400` — missing or invalid session name
  - `404` — workspace not found
  - `500` — HMAC generation failure

### `GET /api/workspaces/{ws}/terminal/ws` (WebSocket)

WebSocket endpoint for live terminal relay (tmux-backed). Supports bidirectional terminal I/O.

- **Auth:** One-time token via `?token=` query param (not bearer auth — token carries session identity and authorization)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (validated by WorkspaceMiddleware) |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| session | string | yes | tmux session name |
| token | string | yes* | One-time terminal token (*when auth enabled) |

- **Pre-Upgrade Validation (HTTP JSON before WebSocket upgrade):**
  - `400` — missing session, invalid session name
  - `401` — terminal authentication failed (invalid/expired/replayed token)
  - `404` — workspace not found
  - `503` — terminal manager not initialized, or maximum terminal sessions reached (default 20)

- **WebSocket Binary Protocol:**
  - All frames are binary (`MessageBinary`)
  - **Server → Client:** raw PTY output bytes (read buffer 4096 bytes)
  - **Client → Server:** raw terminal input bytes OR resize message
  - **Resize message format (in-band): exactly 5 bytes**
    - Byte 0: `0x01` (resize marker)
    - Bytes 1-2: cols as uint16 big-endian
    - Bytes 3-4: rows as uint16 big-endian
    - Example: 80×24 = `[0x01, 0x00, 0x50, 0x00, 0x18]`
  - Max terminal size: 500 cols × 200 rows (values exceeding these are silently ignored)
  - Zero values for cols or rows: silently ignored (no resize performed)
  - Read limit: 32 KB per WebSocket message
  - Default terminal size: 80×24 (frontend sends resize immediately after connect)
  - Non-matching binary messages (wrong length or missing `0x01` marker): treated as regular terminal input, written to PTY

- **Close Codes:**

| Code | Meaning | Frontend behavior |
|------|---------|-------------------|
| 1000 | Normal closure / session detached | Allow reconnect |
| 4001 | Backend process exited (crash) | Show CrashOverlay, no auto-reconnect |

- **Close reason on crash:** last 10 lines of PTY output (truncated to 123 bytes, UTF-8-safe)

- **Session Creation:** if tmux session does not exist, `Attach()` creates it with the default command. Session is workspace-scoped (prefixed by workspace ID).
- **Deferred Kill Cancellation:** if a pending scheduled kill exists for this session, `CancelPendingKill()` is called on attach.
- **SSE Broadcast:** if session is linked to an issue (via tab metadata), broadcasts `terminal_session_change` event on connect.
- **Concurrent Connections:** each WebSocket connection to the same session gets its own PTY attach with a unique connection ID.

### `POST /api/workspaces/{ws}/terminal/spawn`

Pre-create a tmux session for a specific backend.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Request Body:**

```json
{
  "session_name": "string (required)",
  "backend": "string (required — claude|codex|shell|gemini|etc.)"
}
```

- **Session name sanitization:** dots replaced with dashes before validation.
- **Shell backend:** uses default shell command instead of loom lead.
- **Default terminal size:** 120×40.

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "session_name": "sanitized-name",
    "backend": "claude",
    "command": "claude",
    "created": true
  }
}
```

`created` is `false` if the tmux session already existed (idempotent).

- **Errors:**
  - `400` — missing session_name, missing backend, invalid session name, invalid backend
  - `404` — workspace not found
  - `413` — request body too large (>1 MB)
  - `500` — tmux spawn failure
  - `503` — terminal manager not initialized

- **Side Effect:** for issue-linked sessions (matching pattern `issue-{project}-{number}`), records session in session history store with workspace ID.

### `POST /api/workspaces/{ws}/terminal/restart`

Kill existing tmux session and prepare for re-creation with current backend.

- **Auth:** Required (standard bearer + one-time terminal token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| session | string | yes | Session name |
| token | string | yes* | One-time terminal token (*when auth enabled) |

- **Behavior:** kills existing tmux session, reads current backend from loom.yaml via daemon, updates default command, returns new backend name.
- **Shell tab special case:** sessions starting with `lead-shell-` are killed without changing `defaultCommand`; returns `{"success": true, "backend": "shell"}`.

- **Response:** `200 OK`

```json
{"success": true, "backend": "claude"}
```

- **Errors:**
  - `400` — missing session, invalid session name, invalid backend in loom.yaml
  - `401` — terminal authentication failed
  - `404` — workspace not found
  - `405` — method not allowed (non-POST)
  - `503` — terminal manager not initialized

- **Note:** does NOT create the new session — the frontend reconnects via WebSocket which triggers `Attach()`.

### `POST /api/workspaces/{ws}/terminal/kill`

Forcibly kill a terminal session (immediate, no grace period).

- **Auth:** Required (standard bearer + one-time terminal token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| session | string | yes | Session name |
| token | string | yes* | One-time terminal token (*when auth enabled) |

- **Response:** `200 OK`

```json
{"success": true}
```

- **Errors:**
  - `400` — invalid session name
  - `401` — terminal authentication failed
  - `404` — workspace not found
  - `405` — method not allowed (non-POST)
  - `503` — terminal manager not initialized

### `GET /api/workspaces/{ws}/terminal/session-status`

Check whether a terminal session is alive.

- **Auth:** Required (standard bearer + one-time terminal token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| session | string | yes | Session name |
| token | string | yes* | One-time terminal token (*when auth enabled) |

- **Response:** `200 OK`

Alive:
```json
{"alive": true}
```

Dead (session gone or pane process exited):
```json
{
  "alive": false,
  "exit_reason": "last 10 lines of terminal output"
}
```

`exit_reason` is only present if capture succeeds. Checks both tmux session existence AND pane liveness (process may have exited but tmux session lingers).

- **Errors:**
  - `400` — invalid session name
  - `401` — terminal authentication failed
  - `404` — workspace not found
  - `503` — terminal manager not initialized

### `GET /api/workspaces/{ws}/terminal/sessions`

List all active terminal sessions in a workspace.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "sessions": [
      {
        "name": "talk-to-lead",
        "label": "talk-to-lead",
        "created": 1711108800
      }
    ]
  }
}
```

- `"talk-to-lead"` is always included (`created` = 0 if not yet spawned)
- Sessions are filtered by server's `sessionPrefix` (workspace-scoped isolation)
- `created`: Unix timestamp, 0 if session not yet created in tmux
- **Note:** The `TerminalSessionInfo` struct declares `issue_id` with `json:"issue_id,omitempty"` but the handler never populates it — it is always omitted from the JSON response. Issue-to-session mapping is served by `GET /api/workspaces/{ws}/terminal/sessions/by-issue` (documented in the tab metadata spec, loomcli-wit5o).

- **Errors:**
  - `404` — workspace not found
  - `500` — tmux list-sessions failure
  - `503` — terminal manager not initialized

### `POST /api/workspaces/{ws}/terminal/sessions/{name}/seed`

Inject issue context into a running terminal session via tmux send-keys.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |
| name | string | yes | Session name |

- **Request Body:**

```json
{
  "issue_id": "string (required)",
  "title": "string (required)",
  "description": "string (optional, truncated to 800 chars)",
  "design": "string (optional, truncated to 500 chars)",
  "blockers": [
    {"id": "string", "title": "string"}
  ]
}
```

`blockers`: max 5 included in the injected prompt.

- **Behavior:** formats a context prompt from the fields and injects it into the running tmux session via tmux send-keys.

- **Response:** `200 OK`

```json
{"success": true}
```

- **Errors:**
  - `400` — missing session name, invalid JSON, missing issue_id or title
  - `404` — workspace not found; or session not found (tmux session does not exist): `{"success": false, "error": "session not found: {name}"}`
  - `500` — tmux send-keys failure
  - `503` — terminal manager not initialized

### `POST /api/workspaces/{ws}/terminal/sessions/{session}/kill` (Deferred)

Schedule a terminal session kill after a 30-second grace period. Cancelled if the user re-attaches via WebSocket before the timeout fires.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |
| session | string | yes | Session name (validated via `tabmeta.ValidateSessionName`) |

- **Response:** `200 OK`

```json
{"success": true}
```

- **Errors:**
  - `400` — invalid session name
  - `404` — workspace not found
  - `503` — terminal manager not initialized

- **Note:** The race between scheduled kill and re-attach is safe — `CancelPendingKill()` is called in `Attach()`, so re-attach always wins.

### `POST /api/workspaces/{ws}/terminal/sessions/close-all`

Kill all terminal sessions in a workspace and clean up associated metadata.

- **Auth:** Required (standard bearer token)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Behavior:**
  1. Kill all tmux sessions (by workspace-scoped prefix)
  2. Delete all tab metadata for this workspace (Redis cleanup)
  3. Broadcast SSE `terminal_session_change` event

- **Response:** `200 OK`

Clean:
```json
{"success": true}
```

Partial (tmux killed but metadata cleanup failed):
```json
{"success": true, "warning": "sessions killed but metadata cleanup incomplete"}
```

- **Errors:**
  - `404` — workspace not found
  - `500` — tmux kill failure
  - `503` — terminal manager not initialized

### Edge Cases

- **Session name with dots** (from issue IDs): sanitized to dashes by spawn handler before validation
- **Session limit reached** (default 20): rejected pre-WebSocket-upgrade with 503 and message "maximum terminal sessions reached"
- **Token replay**: nonce tracked in memory map, rejected with 401 on second use
- **Token expired** (>60s): rejected with 401
- **Race between scheduled kill and re-attach**: `CancelPendingKill()` called in `Attach()`, so re-attach always wins
- **Server restart**: generates new HMAC secret, all old tokens become invalid (by design)
- **tmux binary not found**: `TerminalManager` creation fails at startup, all terminal endpoints return 503
- **close-all with Redis down**: tmux sessions killed but metadata cleanup fails; returns 200 with warning string, not 500
- **Concurrent WebSocket connections to same session**: each gets its own PTY attach with unique connection ID
- **Non-matching binary WebSocket messages** (wrong length or missing `0x01` marker): treated as terminal input bytes, written to PTY — no error returned
- **Cross-workspace session isolation**: tmux sessions prefixed by workspace ID, so identically-named sessions in different workspaces never collide

### Scrollback Sources

The terminal system has two independent scrollback mechanisms:

1. **tmux capture-pane** (used by scrollback and export endpoints): runs `tmux capture-pane -p -S` on the live tmux session. Returns raw terminal output including ANSI escape codes.
2. **In-memory ring buffer** (used by scrollback-info endpoint): a per-session `ScrollbackBuffer` that captures PTY relay output in real-time. Default capacity: 10,000 lines. Lost on server restart.

### `GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback`

Capture live scrollback from a tmux terminal session (up to 5,000 lines). Returns raw content with ANSI escape codes preserved.

- **Auth:** Required (standard bearer token + WorkspaceMiddleware validation)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |
| session | string | yes | Session name (`^[a-zA-Z0-9_-]+$`) |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "content": "raw terminal output with ANSI codes...",
    "lines": 42
  }
}
```

- **Errors:**
  - `400` — empty workspace ID or empty session name
  - `404` — workspace not found or session not found (tmux session does not exist)
  - `500` — tmux capture-pane command failed

### `GET /api/workspaces/{ws}/terminal/sessions/{session}/export`

Download session transcript as a file. Captures full tmux history (not limited to 5,000 lines) and strips ANSI escape codes.

- **Auth:** Required (standard bearer token + WorkspaceMiddleware validation)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |
| session | string | yes | Session name (`^[a-zA-Z0-9_-]+$`) |

- **Query Parameters:**

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| format | string | no | "txt" | Export format: "txt" or "md" |

- **Response:** `200 OK` — file download (not JSON)

```
Content-Type: text/plain; charset=utf-8
Content-Disposition: attachment; filename="{session}-{YYYYMMDD-HHMMSS}.{format}"
```

- **Errors:**
  - `400` — invalid session name or unsupported format value
  - `404` — workspace not found or session not alive
  - `500` — tmux capture-pane command failed

### `GET /api/workspaces/{ws}/terminal/sessions/{session}/scrollback-info`

Get in-memory scrollback buffer statistics for a terminal session. Returns zeroed stats if no buffer exists (session never had a WebSocket connection).

- **Auth:** Required (standard bearer token + WorkspaceMiddleware validation)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |
| session | string | yes | Session name (`^[a-zA-Z0-9_-]+$`) |

- **Response:** `200 OK` (always succeeds for valid session name)

```json
{
  "line_count": 42,
  "max_lines": 10000,
  "truncated_count": 0
}
```

- **Errors:**
  - `400` — invalid session name
  - `404` — workspace not found

### `GET /api/workspaces/{ws}/terminal/state`

Get persisted terminal UI state (active tab selection). Redis-backed, survives server restarts. Returns empty state on Redis failure (never errors to client).

- **Auth:** Required (standard bearer token + WorkspaceMiddleware validation)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Response:** `200 OK` (always)

```json
{
  "active_tab": "talk-to-lead"
}
```

Returns `{"active_tab": ""}` if no state has been set or if Redis fails.

- **Errors:**
  - `400` — empty workspace ID
  - `404` — workspace not found

- **Note:** The Redis key is `terminal:ui-state:{ws}` — workspace-scoped, so each workspace has an independent active-tab record.

### `PATCH /api/workspaces/{ws}/terminal/state`

Update persisted terminal UI state (active tab selection).

- **Auth:** Required (standard bearer token + WorkspaceMiddleware validation)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID |

- **Request Body:**

```json
{
  "active_tab": "session-name"
}
```

- **Response:** `200 OK`

```json
{
  "active_tab": "session-name"
}
```

- **Errors:**
  - `400` — empty workspace ID or invalid request body (malformed JSON / body exceeds 1 MB)
  - `404` — workspace not found
  - `500` — Redis write failure

- **Note:** Setting `active_tab` to `""` clears the selection. Does not broadcast SSE events.

## Agent Terminal

Workspace-scoped endpoints for agent terminal access: transport mode discovery, one-time token generation, and live WebSocket relay. These endpoints allow the frontend to determine whether an agent has a live tmux session or should fall back to archive logs, authenticate for WebSocket access, and stream the agent's terminal output in real time.

### Workspace Scoping

All three agent terminal endpoints are registered on the workspace-scoped mux under `/api/workspaces/{ws}/agents/{name}/terminal/...`. WorkspaceMiddleware validates the `{ws}` path parameter (400 if empty, 404 if workspace does not exist) and injects the workspace ID into the request context. Handlers retrieve it via `WorkspaceFromContext(r.Context())`.

### Agent Name Validation

All three endpoints validate the `{name}` path parameter against regex `^[a-zA-Z0-9_-]+$`. Empty names and names with special characters (path traversal, dots, spaces) are rejected with 400.

### Session Resolution

Agent terminal endpoints use `FindLatestAgentSession(workspaceID, agentName)` which scans all tmux sessions for the newest one matching the pattern `loom-<wsPrefix>-<role>-<agent>-<pid>` where `wsPrefix` is derived from the workspace ID. When `workspaceID` is empty, the function returns no match (fail-closed). Multiple sessions for the same agent are tie-broken by created timestamp (newest wins), then lexicographic name order.

### Authentication Model

- **`/terminal/info`** — standard bearer token auth (server-level middleware) + WorkspaceMiddleware validation
- **`/terminal/token`** — standard bearer token auth + WorkspaceMiddleware; only registered when `termAuth` is initialized
- **`/terminal/ws`** — public route (bypasses bearer auth via `isPublicRoute` which matches paths with prefix `/api/agents/` and suffix `/terminal/ws` after stripping the workspace prefix); uses one-time token via `?token=` query param. WorkspaceMiddleware still validates workspace existence.

### Token Scoping

Agent terminal tokens use a distinct scope format: `agent:<name>:logs`. This prevents token reuse across agents or between agent and main terminal endpoints.

### `GET /api/workspaces/{ws}/agents/{name}/terminal/info`

Check whether an agent has a live tmux session suitable for terminal streaming or should fall back to archive logs.

- **Auth:** Required (standard bearer token + WorkspaceMiddleware)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (validated by WorkspaceMiddleware: 400 if empty, 404 if not found) |
| name | string | yes | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "agent": "ember",
    "mode": "tmux"
  }
}
```

The `mode` field is `"tmux"` if a matching live tmux session exists for the agent in the specified workspace, `"archive"` otherwise.

- **Errors:**
  - `400` — empty workspace ID (`"workspace ID is required"`), missing agent name, or invalid agent name
  - `404` — workspace not found
  - `500` — failed to inspect terminal sessions (tmux list-sessions failure)
  - `503` — terminal manager not initialized (defensive; endpoint is not registered when manager is absent)

### `GET /api/workspaces/{ws}/agents/{name}/terminal/token`

Generate a one-time HMAC-SHA256 token scoped to an agent's terminal stream.

- **Auth:** Required (standard bearer token + WorkspaceMiddleware)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (validated by WorkspaceMiddleware) |
| name | string | yes | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Response Headers:** `Cache-Control: no-store`
- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "token": "<base64url-encoded-one-time-token>"
  }
}
```

The token is single-use, expires in 60 seconds, and is scoped to `agent:<name>:logs`.

- **Errors:**
  - `400` — empty workspace ID, missing agent name, or invalid agent name
  - `404` — workspace not found
  - `500` — failed to generate token (HMAC generation failure)
  - `503` — terminal authentication not initialized

- **Note:** This endpoint is only registered when both `termManager` and `termAuth` are initialized at startup. If `termAuth` is nil, the route does not exist (404 from SPA catch-all).

### `GET /api/workspaces/{ws}/agents/{name}/terminal/ws` (WebSocket)

WebSocket endpoint for live agent terminal relay (tmux-backed). Supports bidirectional terminal I/O.

- **Auth:** One-time token via `?token=` query param (public route — bypasses bearer auth middleware)
- **Path Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| ws | string | yes | Workspace ID (validated by WorkspaceMiddleware) |
| name | string | yes | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| token | string | yes | One-time terminal token (scoped to `agent:<name>:logs`) |

- **Pre-Upgrade Validation (HTTP JSON before WebSocket upgrade):**
  - `400` — empty workspace ID, missing agent name, or invalid agent name
  - `401` — terminal authentication failed (invalid/expired/replayed token, or scope mismatch)
  - `404` — workspace not found, or no active terminal session for agent
  - `500` — failed to inspect terminal sessions
  - `503` — terminal manager not initialized, terminal authentication not initialized, or maximum terminal sessions reached

- **WebSocket Binary Protocol (identical to main terminal):**
  - All frames are binary (`MessageBinary`)
  - **Server → Client:** raw PTY output bytes (read buffer 4096 bytes)
  - **Client → Server:** raw terminal input bytes OR resize message
  - **Resize message format (in-band):** exactly 5 bytes: `[0x01, cols_hi, cols_lo, rows_hi, rows_lo]`
    - Byte 0: `0x01` (resize marker)
    - Bytes 1-2: cols as uint16 big-endian
    - Bytes 3-4: rows as uint16 big-endian
  - Max terminal size: 500 cols × 200 rows (values exceeding these are silently ignored)
  - Zero values for cols or rows are silently ignored
  - Read limit: 32 KB per WebSocket message
  - Default terminal size: 80×24 (set on attach; frontend sends resize immediately after connect)
  - Non-matching binary messages (wrong length or missing `0x01` marker) are treated as regular terminal input

- **Close Codes:**

| Code | Meaning | Frontend behavior |
|------|---------|-------------------|
| 1000 | Normal closure / session detached | Allow reconnect |
| 4001 | Backend process exited (crash) | Show crash overlay, no auto-reconnect |

- **Close reason on crash:** last 10 lines of PTY output (truncated to 123 bytes, UTF-8-safe)

- **Origin Validation:** Uses `allowedOrigins` config for WebSocket upgrade acceptance.

- **Session Attachment:** Uses `AttachExistingRaw(sessionName, 80, 24)` — attaches to an already-running tmux session without session creation. The session must already exist (discovered via `FindLatestAgentSession`).

- **Differences from Main Terminal WebSocket (`GET /api/workspaces/{ws}/terminal/ws`):**
  - No scrollback buffer capture (nil scrollback)
  - No deferred kill cancellation on attach
  - No SSE `terminal_session_change` broadcast
  - No context banner injection
  - Session is discovered by agent name within workspace (not provided directly by the client)
  - Token scope is agent-specific (`agent:<name>:logs`), not session-based

- **Note:** This endpoint is registered whenever `termManager` is initialized (regardless of `termAuth`). When `termAuth` is nil, the handler itself returns 503 rather than the route being absent.

### Edge Cases

- **Agent name with path traversal** (e.g., `../../../etc/passwd`): rejected by `validAgentName` regex with 400
- **Empty workspace ID in context**: `FindLatestAgentSession` returns no match (fail-closed); info returns `archive` mode, ws returns 404
- **No tmux server running**: `FindLatestAgentSession` returns no match; info returns `archive` mode, ws returns 404
- **Multiple tmux sessions for same agent**: newest by created timestamp wins (tie-broken by lexicographic name order)
- **Cross-workspace isolation**: sessions from workspace "alpha" are not visible when querying workspace "beta" (different `wsPrefix`)
- **Token scope mismatch** (using ember's token for spark's ws): `ValidateToken` fails with 401
- **Token replay** (reusing a consumed token): nonce already used, returns 401
- **Token expired** (>60s): returns 401
- **Session limit reached** (default 20): pre-upgrade check returns 503; race with `AttachExistingRaw` also handles gracefully
- **Agent session disappears between discovery and attach**: `AttachExistingRaw` returns error, WS closes with reason
- **Server restart**: new HMAC secret generated, all previously-issued tokens become invalid
- **Concurrent connections to same agent session**: each gets its own PTY attach with unique connection ID

## Git Operations

Workspace-scoped REST API for git workflow operations on agent worktrees. These endpoints allow the frontend to push changes, pull updates, synchronize branches, create GitHub PRs, hard-reset worktrees, query git status, update target branches, push all worktrees, and retrieve issue-level diff statistics.

All endpoints are registered on `wsMux` under `/api/workspaces/{ws}/...` behind `WorkspaceMiddleware`. They are **conditionally available** — when `gitOps` is nil (no worktree configuration), none of these endpoints exist and requests fall through to the SPA catch-all.

**Note:** Diff/code-review endpoints (`/agents/{name}/diff/commits`, `/diff/files`, `/diff/file`) are covered separately by a dedicated spec.

### Cross-Cutting: Workspace Routing

All 9 endpoints are wrapped by `WorkspaceMiddleware`, which:

1. Extracts `{ws}` from the URL path via `r.PathValue("ws")`
2. Validates non-empty (returns `400` if empty)
3. Validates workspace exists via `wsExists(wsID)` (returns `404` if not found)
4. Injects workspace ID into request context via `WithWorkspace(ctx, wsID)`

| Status | Condition | Body |
|--------|-----------|------|
| `400` | `{ws}` is empty or whitespace | `{"error": "workspace ID is required"}` |
| `404` | `{ws}` does not match any registered workspace | `{"error": "workspace not found: {ws}"}` |

Authentication (`401`) is enforced by a separate auth middleware applied at the server level, not by `WorkspaceMiddleware`.

### Cross-Cutting: Agent Resolution

7 of 9 endpoints use a shared `resolveAgent()` helper that:

1. Extracts `{name}` from the URL path
2. Validates against regex `^[a-zA-Z0-9_-]+$`
3. Extracts workspace ID from context via `WorkspaceFromContext(r.Context())`
4. Resolves via `GitOps.ResolveAgentWorktree(workspaceID, name)`

| Status | Condition | Body |
|--------|-----------|------|
| `400` | Missing agent name | `{"error": "missing agent name"}` |
| `400` | Invalid agent name format | `{"error": "invalid agent name: must match [a-zA-Z0-9_-]+"}` |
| `404` | Agent worktree not found in this workspace | `{"error": "agent worktree \"name\" not found"}` |

### Cross-Cutting: Git Ref Validation

Branch and ref parameters are validated against regex `^[a-zA-Z0-9][a-zA-Z0-9_./-]*$` and must not contain `..` (path traversal prevention). Names starting with `-` are rejected (command injection prevention).

### `POST /api/workspaces/{ws}/git/push-all`

Push all agent worktree branches in this workspace to their target branches.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |

- **Request Body:** None
- **Response:** `200 OK`

```json
{
  "results": [
    {
      "name": "drift",
      "success": true,
      "message": "pushed"
    },
    {
      "name": "spark",
      "success": true,
      "message": "already up to date"
    },
    {
      "name": "blaze",
      "success": false,
      "error": "merge conflict in file.go"
    }
  ],
  "pushed": 1,
  "failed": 1
}
```

- Each result has `name` + (`success` + `message`) or (`error`). The `success` field defaults to false when `error` is present.
- `"already up to date"` entries have `success: true` but do NOT increment the `pushed` counter.
- `pushed`/`failed` counts reflect actual push successes and failures only.
- Uses `wt.Remote` if set, falls back to `"origin"`.

- **Errors:**
  - `500` — listing worktrees failed (`{"error": "listing worktrees: ..."}`)

- **Note:** Non-atomic — partial failures are reported per-worktree. Returns `200` with `{"results": [], "pushed": 0, "failed": 0}` if no worktrees exist.

### `POST /api/workspaces/{ws}/agents/{name}/git/push`

Merge the agent's worktree branch INTO the target branch (loom push semantics — not `git push`).

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body** (optional):

```json
{
  "target": "branch-name"
}
```

`target` defaults to the worktree's `DefaultBranch` if empty or omitted. Validated against git ref regex.

- **Response:** `200 OK`

```json
{
  "success": true,
  "message": "merged agent/drift into v2",
  "already_up_to_date": false
}
```

- **Response:** `409 Conflict` (merge conflicts)

```json
{
  "success": false,
  "message": "merge conflicts detected",
  "already_up_to_date": false,
  "conflicted_files": ["path/to/file.go"]
}
```

- **Errors:**
  - `400` — missing/invalid agent name, invalid target branch name
  - `404` — agent worktree not found in this workspace
  - `502` — git operation failed

### `POST /api/workspaces/{ws}/agents/{name}/git/pull`

Merge the source branch INTO the agent's current worktree branch.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body** (optional):

```json
{
  "source": "branch-name"
}
```

`source` defaults to the worktree's `DefaultBranch` if empty or omitted. Validated against git ref regex.

- **Response:** `200 OK`

```json
{
  "success": true,
  "message": "merged v2 into agent/drift",
  "already_up_to_date": false
}
```

- **Response:** `409 Conflict` (merge conflicts)

```json
{
  "success": false,
  "message": "merge conflicts detected",
  "already_up_to_date": false,
  "conflicted_files": ["path/to/file.go"]
}
```

- **Errors:**
  - `400` — missing/invalid agent name, invalid source branch name
  - `404` — agent worktree not found in this workspace
  - `500` — getting current branch failed (`{"error": "getting current branch: ..."}`)
  - `502` — git operation failed

### `POST /api/workspaces/{ws}/agents/{name}/git/sync`

Two-phase operation: first pushes worktree branch to default target, then pulls from target back into worktree. If push fails with conflicts, pull is NOT attempted.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body:** None
- **Response:** `200 OK` (both succeed)

```json
{
  "push_result": {
    "success": true,
    "message": "merged agent/drift into v2",
    "already_up_to_date": false
  },
  "pull_result": {
    "success": true,
    "message": "merged v2 into agent/drift",
    "already_up_to_date": false
  }
}
```

- **Response:** `409 Conflict` (push conflicts — pull not attempted)

```json
{
  "push_result": {
    "success": false,
    "message": "merge conflicts detected",
    "already_up_to_date": false,
    "conflicted_files": ["file.go"]
  },
  "pull_result": null
}
```

`pull_result` is null when push has conflicts.

- **Response:** `409 Conflict` (push succeeds, pull conflicts)

```json
{
  "push_result": {
    "success": true,
    "message": "merged agent/drift into v2",
    "already_up_to_date": false
  },
  "pull_result": {
    "success": false,
    "message": "merge conflicts detected",
    "already_up_to_date": false,
    "conflicted_files": ["file.go"]
  }
}
```

- **Errors:**
  - `400` — missing/invalid agent name
  - `404` — agent worktree not found in this workspace
  - `500` — getting current branch failed (`{"error": "getting current branch: ..."}`)
  - `502` — push or pull git operation failed

### `POST /api/workspaces/{ws}/agents/{name}/git/pr`

Create a GitHub PR from the agent's worktree branch to the target branch using the `gh` CLI.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body** (optional):

```json
{
  "target": "branch-name"
}
```

`target` defaults to the worktree's `DefaultBranch` if empty or omitted. Validated against git ref regex.

- **Pre-check:** Verifies `gh` CLI is installed via `CheckGhInstalled()`. Returns `503` immediately if not installed.

- **Response:** `201 Created` (PR created)

```json
{
  "url": "https://github.com/org/repo/pull/42",
  "created": true,
  "already_exists": false,
  "no_commits": false
}
```

- **Response:** `200 OK` (PR already exists)

```json
{
  "url": "https://github.com/org/repo/pull/42",
  "created": false,
  "already_exists": true,
  "no_commits": false
}
```

- **Response:** `200 OK` (no commits to merge)

```json
{
  "created": false,
  "already_exists": false,
  "no_commits": true
}
```

- **Errors:**
  - `400` — missing/invalid agent name, invalid target branch
  - `404` — agent worktree not found in this workspace
  - `502` — `gh` CLI PR creation failed
  - `503` — `gh` CLI not installed (`{"error": "gh CLI not installed: install from https://cli.github.com/ and run 'gh auth login'"}`)

### `POST /api/workspaces/{ws}/agents/{name}/git/reset`

Hard reset the worktree to a specified branch.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body** (optional):

```json
{
  "branch": "target-branch",
  "force": false,
  "push": false
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `branch` | string | worktree's `DefaultBranch` | Branch to reset to. Validated against git ref regex. |
| `force` | bool | `false` | If true, bypasses agent lock check |
| `push` | bool | `false` | If true, force-pushes the branch to origin after resetting |

- **Response:** `200 OK`

```json
{
  "success": true,
  "message": "reset to v2",
  "previous_branch": "agent/drift",
  "pushed": false
}
```

`previous_branch` is omitted when empty.

- **Response:** `423 Locked` (agent locked — cannot reset without `force`)

```json
{
  "error": "agent locked",
  "lock_info": {
    "agent": "drift",
    "pid": 12345,
    "duration": "2m30s",
    "task_id": "loomcli-abc12"
  }
}
```

`task_id` is omitted when empty. Only returned when `force=false` and agent has an active lock.

- **Errors:**
  - `400` — missing/invalid agent name, invalid branch name
  - `404` — agent worktree not found in this workspace
  - `502` — git operation failed

### `GET /api/workspaces/{ws}/agents/{name}/git/status`

Detailed git status for the agent's worktree.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Response:** `200 OK`

```json
{
  "branch": "agent/drift",
  "target_branch": "v2",
  "is_clean": true,
  "ahead": 3,
  "behind": 0,
  "changed_files": [],
  "conflicted_files": [],
  "has_conflicts": false,
  "stash_count": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `branch` | string | Current worktree branch |
| `target_branch` | string | Target/integration branch |
| `is_clean` | bool | True if no uncommitted changes |
| `ahead` | int | Commits ahead of target branch (after fetch) |
| `behind` | int | Commits behind target branch (after fetch) |
| `changed_files` | []string | File paths with uncommitted changes |
| `conflicted_files` | []string | File paths with merge conflicts |
| `has_conflicts` | bool | True if merge conflicts exist |
| `stash_count` | int | Number of stash entries |

- **Errors:**
  - `400` — missing/invalid agent name
  - `404` — agent worktree not found in this workspace
  - `500` — git status operation failed (`{"error": "getting git status: ..."}`)

### `PATCH /api/workspaces/{ws}/agents/{name}/git/target`

Change the target/integration branch for the repo associated with this worktree.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Request Body** (required):

```json
{
  "branch": "new-target-branch"
}
```

`branch` is required and non-empty. Validated against git ref regex.

- **Response:** `200 OK`

```json
{
  "success": true,
  "branch": "new-target-branch"
}
```

- **Errors:**
  - `400` — missing/invalid agent name
  - `400` — `"branch is required"` (empty branch)
  - `400` — `"invalid branch name"` (fails git ref regex or contains `..`)
  - `400` — `"target branch update only supported in workspace mode"` (non-workspace worktree)
  - `400` — `"invalid request body"` (malformed JSON)
  - `404` — agent worktree not found in this workspace
  - `500` — updating target branch failed (`{"error": "updating target branch: ..."}`)

### `GET /api/workspaces/{ws}/issues/{id}/git/diff-stat`

Diff statistics (added/removed lines) for an issue's assigned agent worktree.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `id` | string | Issue ID (e.g., `"loomcli-abc12"`) |

- **Response:** `200 OK`

```json
{
  "branch": "agent/drift",
  "added": 142,
  "removed": 37
}
```

| Field | Type | Description |
|-------|------|-------------|
| `branch` | string | Agent's worktree branch |
| `added` | int | Lines added vs target branch |
| `removed` | int | Lines removed vs target branch |

- **Errors:**
  - `400` — missing issue ID
  - `404` — `"issue not found: {id}"` (daemon RPC lookup failed)
  - `404` — `"issue has no assignee (no agent worktree)"` (no assignee field)
  - `404` — `"agent worktree not found for {assignee}"` (worktree resolution failed in this workspace)
  - `500` — `"internal server error"` (non-404 daemon RPC error)
  - `500` — `"failed to parse issue data"` (issue JSON unmarshal failure)
  - `503` — daemon not available (RPC pool connection failure, 5s timeout)

- **Note:** Uses daemon RPC via `multiPool` (routed to workspace-specific pool) with a 5-second context timeout.

## Agent Diff / Code Review

Workspace-scoped REST API for agent worktree diff and code-review operations. These 3 endpoints allow the frontend to list commits in a diff range, list changed files between two refs, and retrieve a unified diff patch for a single file.

All endpoints are registered on `wsMux` under `/api/workspaces/{ws}/...` behind `WorkspaceMiddleware`. They are **conditionally available** — when `gitOps` is nil (no worktree configuration), none of these endpoints exist and requests fall through to the SPA catch-all.

**Note on legacy flat routes:** Legacy flat routes (`/api/agents/{name}/diff/...`) still exist on the main mux for backward compatibility but are slated for removal by loomcli-n28bt.41. This spec documents only the workspace-scoped routes, which are the authoritative API surface.

**Note on response envelope:** Unlike the Git Operations endpoints above (which respond with raw structs), all diff endpoints use the `diffResponse` envelope: `{success: true, data: T}` on success or `{success: false, error: "msg"}` on error. This matches the frontend's `ApiResult<T>` pattern. The one exception is `resolveAgent()` errors, which use the raw `{"error": "msg"}` format (no `success` field) since they go through `respondError`, not `respondDiffError`.

### Cross-Cutting: Workspace Routing

All 3 endpoints are wrapped by `WorkspaceMiddleware`, which:

1. Extracts `{ws}` from the URL path via `r.PathValue("ws")`
2. Validates non-empty (returns `400` if empty)
3. Validates workspace exists via `wsExists(wsID)` (returns `404` if not found)
4. Injects workspace ID into request context via `WithWorkspace(ctx, wsID)`

| Status | Condition | Body |
|--------|-----------|------|
| `400` | `{ws}` is empty or whitespace | `{"error": "workspace ID is required"}` |
| `404` | `{ws}` does not match any registered workspace | `{"error": "workspace not found: {ws}"}` |

Authentication (`401`) is enforced by a separate auth middleware applied at the server level, not by `WorkspaceMiddleware`.

### Cross-Cutting: Merge-Base Resolution

All 3 endpoints use `resolveMergeBaseDefault()` for the `from` ref:

1. If `?from=` query param is provided and non-empty, validates against git ref regex `^[a-zA-Z0-9][a-zA-Z0-9_./-]*$` and rejects if it contains `..`
2. If `?from=` is empty or omitted, calls `ops.ResolveMergeBase(wt.Path, wt.DefaultBranch)` to compute the merge-base automatically
3. Returns `400` with `"invalid from ref"` if validation fails
4. Returns `500` with `"failed to resolve merge-base: ..."` if merge-base computation fails

### Data Models

**DiffCommitResult:**

```json
{
  "hash": "full SHA string",
  "short_hash": "abbreviated SHA",
  "subject": "commit message first line",
  "author": "author name",
  "email": "author email",
  "date": "commit date string"
}
```

**DiffFileResult:**

```json
{
  "path": "relative file path",
  "status": "M",
  "old_path": "original path (only present for R or C status)",
  "additions": 10,
  "deletions": 5
}
```

- `status` values: `M` (modified), `A` (added), `D` (deleted), `R` (renamed), `C` (copied)
- `old_path` uses `omitempty` — only present for renamed or copied files

**DiffFilePatchResult:**

```json
{
  "patch": "unified diff string",
  "is_binary": false,
  "is_too_large": false,
  "additions": 1,
  "deletions": 0
}
```

- When `is_binary` is true, `patch` is empty
- When `is_too_large` is true, `patch` is empty (file exceeds diff size limit)

### `GET /api/workspaces/{ws}/agents/{name}/diff/commits`

List commits between the merge-base (or explicit `from` ref) and HEAD in the agent's worktree.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Query params:**

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `from` | string | no | merge-base of worktree's `DefaultBranch` | Start ref for commit range |
| `limit` | int | no | `0` (unlimited) | Maximum number of commits to return |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "commits": [
      {
        "hash": "aaa111222333...",
        "short_hash": "aaa111",
        "subject": "first commit",
        "author": "Alice",
        "email": "alice@example.com",
        "date": "2026-01-01"
      }
    ]
  }
}
```

`commits` is always an array (never null) — nil slices are normalized to `[]`.

- **Errors:**
  - `400` — missing agent name: `{"error": "missing agent name"}` (no `success` field)
  - `400` — invalid agent name: `{"error": "invalid agent name: must match [a-zA-Z0-9_-]+"}` (no `success` field)
  - `404` — agent worktree not found: `{"error": "agent worktree \"name\" not found"}` (no `success` field)
  - `400` — invalid `limit` value: `{"success": false, "error": "invalid limit value: abc (must be an integer)"}`
  - `400` — invalid `from` ref (fails regex or contains `..`): `{"success": false, "error": "invalid from ref"}`
  - `500` — merge-base resolution failed: `{"success": false, "error": "failed to resolve merge-base: ..."}`
  - `500` — git log operation failed: `{"success": false, "error": "failed to get diff commits: ..."}`

### `GET /api/workspaces/{ws}/agents/{name}/diff/files`

List changed files between two refs in the agent's worktree, with per-file status and addition/deletion line counts.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Query params:**

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `to` | string | yes | — | End ref for diff (typically `"HEAD"` or a commit SHA) |
| `from` | string | no | merge-base of worktree's `DefaultBranch` | Start ref for diff |

- **Response:** `200 OK`

```json
{
  "success": true,
  "data": {
    "files": [
      {
        "path": "main.go",
        "status": "M",
        "additions": 10,
        "deletions": 5
      },
      {
        "path": "new.go",
        "status": "A",
        "additions": 20,
        "deletions": 0
      },
      {
        "path": "renamed.go",
        "status": "R",
        "old_path": "old_name.go",
        "additions": 2,
        "deletions": 1
      }
    ]
  }
}
```

`files` is always an array (never null) — nil slices are normalized to `[]`. `old_path` is only present for renamed (`R`) or copied (`C`) files.

- **Errors:**
  - `400` — missing agent name: `{"error": "missing agent name"}` (no `success` field)
  - `400` — invalid agent name: `{"error": "invalid agent name: must match [a-zA-Z0-9_-]+"}` (no `success` field)
  - `404` — agent worktree not found: `{"error": "agent worktree \"name\" not found"}` (no `success` field)
  - `400` — missing `to` parameter: `{"success": false, "error": "missing required query parameter: to"}`
  - `400` — invalid `to` ref (fails regex or contains `..`): `{"success": false, "error": "invalid to ref"}`
  - `400` — invalid `from` ref (fails regex or contains `..`): `{"success": false, "error": "invalid from ref"}`
  - `500` — merge-base resolution failed: `{"success": false, "error": "failed to resolve merge-base: ..."}`
  - `500` — git diff operation failed: `{"success": false, "error": "failed to get diff files: ..."}`

### `GET /api/workspaces/{ws}/agents/{name}/diff/file`

Get the unified diff patch for a single file between two refs.

- **Auth:** Required
- **Path params:**

| Param | Type | Description |
|-------|------|-------------|
| `ws` | string | Workspace ID (UUID) |
| `name` | string | Agent name (validated: `^[a-zA-Z0-9_-]+$`) |

- **Query params:**

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `path` | string | yes | — | Relative file path within the worktree |
| `to` | string | yes | — | End ref for diff (typically `"HEAD"` or a commit SHA) |
| `from` | string | no | merge-base of worktree's `DefaultBranch` | Start ref for diff |

- **Path validation:** The `path` parameter must be a relative path. Rejected if empty, starts with `/`, or resolves (after `path.Clean()`) to `.`, `..`, or any `../` prefix.

- **Response:** `200 OK` (normal diff)

```json
{
  "success": true,
  "data": {
    "patch": "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n+new line\n",
    "is_binary": false,
    "is_too_large": false,
    "additions": 1,
    "deletions": 0
  }
}
```

- **Response:** `200 OK` (binary file)

```json
{
  "success": true,
  "data": {
    "patch": "",
    "is_binary": true,
    "is_too_large": false,
    "additions": 0,
    "deletions": 0
  }
}
```

- **Response:** `200 OK` (file too large)

```json
{
  "success": true,
  "data": {
    "patch": "",
    "is_binary": false,
    "is_too_large": true,
    "additions": 0,
    "deletions": 0
  }
}
```

- **Errors:**
  - `400` — missing agent name: `{"error": "missing agent name"}` (no `success` field)
  - `400` — invalid agent name: `{"error": "invalid agent name: must match [a-zA-Z0-9_-]+"}` (no `success` field)
  - `404` — agent worktree not found: `{"error": "agent worktree \"name\" not found"}` (no `success` field)
  - `400` — missing `path` parameter: `{"success": false, "error": "missing required query parameter: path"}`
  - `400` — invalid path (absolute, empty, traversal): `{"success": false, "error": "invalid path: must be relative with no '..' traversal"}`
  - `400` — missing `to` parameter: `{"success": false, "error": "missing required query parameter: to"}`
  - `400` — invalid `to` ref (fails regex or contains `..`): `{"success": false, "error": "invalid to ref"}`
  - `400` — invalid `from` ref (fails regex or contains `..`): `{"success": false, "error": "invalid from ref"}`
  - `500` — merge-base resolution failed: `{"success": false, "error": "failed to resolve merge-base: ..."}`
  - `500` — git diff operation failed: `{"success": false, "error": "failed to get diff patch: ..."}`

## Issue Tabs

Issue tab state persistence endpoints. These endpoints manage the tab layout (which tabs are open, their order, and which is active) for an issue's detail panel. Tab state is Redis-backed with a 24-hour TTL, includes terminal session liveness validation on read, and broadcasts SSE events on mutation.

All 3 endpoints require Redis. When the issue tab store is not configured (no Redis), the routes are not registered and requests return `404`.

### Data Model: IssueTabState

```json
{
  "issue_id": "loomcli-abc",
  "tabs": [
    {
      "id": "details",
      "type": "details",
      "label": "Details",
      "sort_order": 0
    },
    {
      "id": "terminal-s1",
      "type": "terminal",
      "label": "Terminal 1",
      "session_name": "s1",
      "sort_order": 1
    }
  ],
  "active_tab_id": "details",
  "updated_at": "2026-03-22T10:00:00Z"
}
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `issue_id` | string | Issue ID (from path parameter) |
| `tabs` | array | Array of tab objects |
| `tabs[].id` | string | Unique tab identifier (e.g., `"details"`, `"logs"`, `"terminal-session1"`) |
| `tabs[].type` | string | Tab type: `"details"`, `"logs"`, or `"terminal"` |
| `tabs[].label` | string | Display name |
| `tabs[].session_name` | string | Tmux session name (only for `type="terminal"`, omitted otherwise) |
| `tabs[].sort_order` | int | Tab ordering (lower = first) |
| `active_tab_id` | string | ID of the currently selected tab |
| `updated_at` | string | RFC 3339 timestamp (set automatically on save) |

**Redis key:** `ws:{workspaceID}:issue:tabs:{issueId}` — stores a single JSON blob. Atomic read/write of the entire state.

**TTL:** 24 hours, refreshed on every PUT.

**Issue ID validation:** All endpoints validate `issueId` against regex `^[a-zA-Z0-9._-]+$` (allows dots for sub-issue IDs like `"loomcli-abc.1"`). Empty IDs are rejected with `400`.

### `GET /api/issues/{issueId}/tabs`

Retrieve the persisted tab state for an issue.

- **Auth:** Required
- **Path Parameters:**

  | Param | Type | Required | Description |
  |-------|------|----------|-------------|
  | `issueId` | string | yes | Issue ID (alphanumeric, hyphens, underscores, dots) |

- **Terminal Session Filtering:** If a TerminalManager is available, terminal-type tabs are cross-referenced against active tmux sessions. Tabs whose tmux session no longer exists are filtered out. If the active tab was removed during filtering, `active_tab_id` falls back to `"details"`. The filtered state is transparently saved back to Redis if any tabs were removed or the active tab changed. If the TerminalManager is nil or listing sessions fails, no filtering occurs — all tabs are returned as-is.

- **Response `200 OK`** (no saved state):

```json
{"success": true}
```

  No `data` field is present when no state has been saved for this issue.

- **Response `200 OK`** (with saved state):

```json
{
  "success": true,
  "data": {
    "issue_id": "loomcli-abc",
    "tabs": [
      {"id": "details", "type": "details", "label": "Details", "sort_order": 0},
      {"id": "terminal-s1", "type": "terminal", "label": "Terminal 1", "session_name": "s1", "sort_order": 1}
    ],
    "active_tab_id": "details",
    "updated_at": "2026-03-22T10:00:00Z"
  }
}
```

- **Response `400`:** Invalid issue ID (empty or contains disallowed characters)
- **Response `500`:** Redis get failure

### `PUT /api/issues/{issueId}/tabs`

Save or replace the full tab state for an issue.

- **Auth:** Required
- **Path Parameters:**

  | Param | Type | Required | Description |
  |-------|------|----------|-------------|
  | `issueId` | string | yes | Issue ID (alphanumeric, hyphens, underscores, dots) |

- **Request Body:**

```json
{
  "tabs": [
    {
      "id": "details",
      "type": "details",
      "label": "Details",
      "sort_order": 0
    },
    {
      "id": "terminal-s1",
      "type": "terminal",
      "label": "Terminal 1",
      "session_name": "s1",
      "sort_order": 1
    }
  ],
  "active_tab_id": "details"
}
```

  - `tabs`: array of tab objects (can be empty)
  - `active_tab_id`: which tab is currently selected
  - `issue_id` is taken from the path parameter, not the request body
  - `updated_at` is set automatically to current UTC time
  - Request body limit: 1 MB

- **Behavior:** Saves the full tab state atomically (replaces any existing state). Sets a 24-hour TTL on the Redis key. Broadcasts an `issue_tabs` SSE event with the issue ID, workspace ID, and current timestamp.

- **Response `200 OK`:**

```json
{
  "success": true,
  "data": {
    "issue_id": "loomcli-abc",
    "tabs": [...],
    "active_tab_id": "details",
    "updated_at": "2026-03-22T10:00:00Z"
  }
}
```

- **Response `400`:** Invalid issue ID, or invalid/malformed request body (including body exceeding 1 MB limit)
- **Response `500`:** Redis save failure

### `DELETE /api/issues/{issueId}/tabs`

Remove the tab state for an issue.

- **Auth:** Required
- **Path Parameters:**

  | Param | Type | Required | Description |
  |-------|------|----------|-------------|
  | `issueId` | string | yes | Issue ID (alphanumeric, hyphens, underscores, dots) |

- **Behavior:** Removes the tab state for the given issue from Redis. Deleting a non-existent key is a no-op (returns `200` success, matching Redis DEL semantics). No SSE broadcast.

- **Response `200 OK`:**

```json
{"success": true}
```

- **Response `400`:** Invalid issue ID (empty or contains disallowed characters)
- **Response `500`:** Redis delete failure

## Monitor Endpoints

Monitor endpoints serve daemon-collected data (agent status, task distribution, metrics). They are injected from the cli package via `ServerConfig.MonitorHandlers`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/monitor/status` | Full monitor dashboard data |
| GET | `/api/monitor/agents` | Agent list with workspace grouping |
| GET | `/api/monitor/tasks` | Task distribution by status |
| GET | `/api/monitor/stats` | Monitor statistics |
| GET | `/api/monitor/sync` | Git sync status |
| GET | `/api/monitor/workspaces` | Workspace topology metadata |
| GET | `/api/monitor/stale-detector` | Stale detector status |
| GET | `/api/monitor/usage` | Token usage aggregates |
| GET | `/metrics` | Prometheus metrics (public, no auth required) |
| GET | `/api/observability/metrics` | Event metrics snapshot |
| GET | `/api/observability/events` | Paginated event log |

## Multi-Workspace Endpoints

All endpoints in this section are registered on the workspace sub-mux (`wsMux`) behind `WorkspaceMiddleware`. The middleware:

1. Extracts the `{ws}` path parameter from the URL
2. Trims whitespace and validates it is non-empty (returns `400` if empty)
3. Validates the workspace exists via `wsExists(wsID)` (returns `404` if not found)
4. Injects the workspace ID into the request context
5. Routes to the correct per-workspace daemon connection pool via `MultiPool.Get(ctx)`

**Common error responses (all 16 endpoints):**

| Status | Condition |
|--------|-----------|
| `400` | Workspace ID is empty: `{"success": false, "error": "workspace ID is required"}` |
| `404` | Workspace not found: `{"success": false, "error": "workspace not found: {ws}"}` |
| `503` | Per-workspace daemon pool unavailable |
| `504` | Handler timeout (context deadline exceeded) |

### Issue Management

These 11 endpoints handle issue lifecycle under `/api/workspaces/{ws}/issues/...`. Request and response shapes are identical to the non-workspace-scoped equivalents documented in the [Issues](#issues) section, with the addition of workspace routing.

#### `GET /api/workspaces/{ws}/issues`

List issues with filtering, pagination, and optional Kanban enrichment.

- **Auth:** Required
- **Timeout:** 2 seconds
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `status` | string | Filter by status |
  | `type` | string | Filter by issue type |
  | `assignee` | string | Filter by assignee |
  | `q` | string | Search query |
  | `priority` | int (0-4) | Filter by priority |
  | `labels` | string | Comma-separated labels (all must match) |
  | `limit` | int | Max results (max 1000) |
  | `title_contains` | string | Substring match on title |
  | `description_contains` | string | Substring match on description |
  | `notes_contains` | string | Substring match on notes |
  | `created_after` | string | RFC3339 or YYYY-MM-DD |
  | `created_before` | string | RFC3339 or YYYY-MM-DD |
  | `updated_after` | string | RFC3339 or YYYY-MM-DD |
  | `updated_before` | string | RFC3339 or YYYY-MM-DD |
  | `empty_description` | bool | Only issues with empty description |
  | `no_assignee` | bool | Only unassigned issues |
  | `no_labels` | bool | Only issues without labels |
  | `pinned` | bool | Filter by pinned status |
  | `exclude_status` | string | Comma-separated statuses to exclude |
  | `include_blocked` | bool | Include blocked dependency info |
  | `source_repos` | string | Comma-separated repo names to filter by |
  | `parent_id` | string | Filter by parent issue ID |

- **Response `200`:**

```json
{
  "success": true,
  "data": [
    {
      "issue": { "id": "abc", "title": "...", ... },
      "comment_count": 2,
      "dependency_count": 1,
      "parent": "parent-id",
      "parent_title": "Parent Issue Title"
    }
  ]
}
```

With `include_blocked=true`, each entry also includes `blocked_by_ids` and `blocked_by_titles` arrays.

- **Errors:** `400` (invalid params), `503` (pool unavailable), `504` (timeout)

#### `GET /api/workspaces/{ws}/issues/{id}`

Get a single issue by ID.

- **Auth:** Required
- **Timeout:** 2 seconds
- **Path params:** `id` — issue ID
- **Response `200`:**

```json
{
  "success": true,
  "data": { "id": "abc", "title": "...", ... }
}
```

- **Errors:** `400` (missing ID), `404` (not found), `503` (pool unavailable), `504` (timeout)

#### `POST /api/workspaces/{ws}/issues`

Create a new issue.

- **Auth:** Required
- **Timeout:** 30 seconds
- **Request Body:**

```json
{
  "title": "string (required)",
  "issue_type": "bug|feature|task|epic|chore (required)",
  "priority": 0,
  "id": "string (optional, auto-generated if empty)",
  "parent": "parent-issue-id (optional)",
  "description": "string (optional)",
  "design": "string (optional)",
  "acceptance_criteria": "string (optional)",
  "notes": "string (optional)",
  "assignee": "string (optional)",
  "owner": "string (optional)",
  "created_by": "string (optional)",
  "external_ref": "string (optional)",
  "estimated_minutes": 480,
  "labels": ["label1", "label2"],
  "dependencies": ["dep-id-1"],
  "due_at": "2024-12-31T23:59:59Z",
  "defer_until": "2024-12-25T00:00:00Z"
}
```

- **Validation:** Title required, issue_type required and must be valid, priority 0-4, max 50 labels, max 100 dependencies
- **Response `201`:** `{"success": true, "data": {...created issue...}}`
- **Errors:** `400` (validation), `413` (body too large), `503` (pool unavailable), `504` (timeout)

#### `PATCH /api/workspaces/{ws}/issues/{id}`

Partially update an issue. All fields are optional.

- **Auth:** Required
- **Timeout:** 2 seconds
- **Path params:** `id` — issue ID
- **Request Body:**

```json
{
  "title": "string",
  "status": "open|in_progress|review|closed",
  "priority": 0,
  "assignee": "string",
  "description": "string",
  "add_labels": ["new-label"],
  "remove_labels": ["old-label"],
  "set_labels": ["label1", "label2"]
}
```

- **Response `200`:** `{"success": true, "data": {...updated issue...}}`
- **Errors:** `400` (missing ID, invalid body), `404` (not found), `413` (body too large), `503` (pool unavailable), `504` (timeout)

#### `POST /api/workspaces/{ws}/issues/{id}/close`

Close an issue.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID
- **Request Body:** (optional)

```json
{
  "reason": "string (optional)",
  "session": "session-id (optional)",
  "suggest_next": false,
  "force": false
}
```

When `force=false` and the issue has open blockers, returns `409 Conflict`.

- **Response `200`:** `{"success": true, "data": {...closed issue...}}`
- **Errors:** `400` (missing ID, invalid body), `404` (not found), `409` (open blockers when `force=false`), `413` (body too large), `503` (pool unavailable), `504` (timeout)

#### `POST /api/workspaces/{ws}/issues/{id}/move`

Move an issue to a different workspace.

- **Auth:** Required
- **Timeout:** 30 seconds
- **Path params:** `id` — issue ID
- **Request Body:**

```json
{
  "target_workspace": "workspace-name (required)"
}
```

**Behavior:**

1. Validates source issue exists and is not closed
2. Validates target workspace exists and differs from source
3. Resolves workspace name to stable UUID (backward compat with pre-UUID names)
4. Creates copy in target workspace (title, description, priority, type, labels, design, acceptance_criteria, notes, assignee, owner, external_ref, estimated_minutes, due_at, defer_until) with "(Moved from {source-id})" appended to description
5. Adds comment on source: "Moved to {target-id} in workspace '{target-name}'"
6. Closes source issue with `force=true`
7. Returns warnings if comment/close fails or if source has an active assignee

Dependencies are NOT moved.

- **Response `200`:**

```json
{
  "success": true,
  "data": {
    "source_id": "original-id",
    "target_id": "new-id",
    "warnings": ["Active agent \"spark\" assigned to this issue. Moving will not stop the agent."]
  }
}
```

- **Errors:**
  - `400` — missing ID, missing target_workspace, target workspace not found, same workspace, closed issue, multi-workspace mode required, workspace config unavailable, target workspace not registered
  - `404` — source issue not found
  - `413` — body too large
  - `502` — target workspace daemon unavailable
  - `503` — source pool unavailable
  - `504` — timeout

#### `DELETE /api/workspaces/{ws}/issues/{id}`

Permanently delete an issue. Internally uses `force=true` — no confirmation prompt.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID
- **No request body**
- **Response `200`:** `{"success": true, "data": ...}`
- **Errors:** `400` (missing ID), `404` (not found), `503` (pool unavailable), `504` (timeout)

#### `POST /api/workspaces/{ws}/issues/{id}/comments`

Add a comment to an issue.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID
- **Request Body:**

```json
{
  "text": "Comment text (required, max 64 KB)"
}
```

Text is trimmed; empty text after trimming returns `400`.

- **Response `201`:**

```json
{
  "success": true,
  "data": {
    "id": "comment-id",
    "text": "Comment text",
    "author": "web-ui",
    "created_at": "2024-01-15T12:00:00Z"
  }
}
```

- **Errors:** `400` (missing ID, empty text, text exceeds 64 KB), `404` (not found), `413` (body too large), `503` (pool unavailable), `504` (timeout)

#### `GET /api/workspaces/{ws}/issues/{id}/events`

List events for an issue.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID
- **Query Parameters:**
  | Parameter | Type | Default | Description |
  |-----------|------|---------|-------------|
  | `limit` | int | 100 | Max events to return (max 500) |

Invalid or negative `limit` silently defaults to 100; values above 500 are clamped to 500.

- **Response `200`:**

```json
{
  "success": true,
  "data": [
    {
      "id": "event-id",
      "issue_id": "abc",
      "type": "status_change",
      "actor": "web-ui",
      "data": {},
      "created_at": "2024-01-15T12:00:00Z"
    }
  ]
}
```

Empty events list is returned as `[]` (not null).

- **Errors:** `400` (missing ID), `404` (not found), `503` (pool unavailable), `504` (timeout)

#### `POST /api/workspaces/{ws}/issues/{id}/dependencies`

Add a dependency (make `{id}` depend on another issue).

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID
- **Request Body:**

```json
{
  "depends_on_id": "other-issue-id (required)",
  "dep_type": "blocks (optional, defaults to \"blocks\")"
}
```

- **Response `200`:** `{"success": true, "data": ...}`
- **Errors:** `400` (self-dependency, missing depends_on_id, duplicate dependency), `404` (issue or target not found), `409` (circular dependency), `413` (body too large), `503` (pool unavailable), `504` (timeout)

#### `DELETE /api/workspaces/{ws}/issues/{id}/dependencies/{depId}`

Remove a dependency from an issue.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Path params:** `id` — issue ID, `depId` — dependency issue ID to remove
- **No request body**
- **Response `200`:** `{"success": true, "data": ...}`
- **Errors:** `400` (missing IDs), `404` (issue or dependency not found), `503` (pool unavailable), `504` (timeout)

### Workspace Views

These 4 endpoints provide aggregate workspace-wide data. They are NOT issue-specific CRUD operations — they compute dashboard/overview state across all issues in the workspace.

#### `GET /api/workspaces/{ws}/stats`

Get workspace issue statistics.

- **Auth:** Required
- **Timeout:** 2 seconds
- **No query params**
- Same response shape as `GET /api/stats` documented in [Health & Status](#health--status)
- **Errors:** `503` (pool unavailable), `504` (timeout)

#### `GET /api/workspaces/{ws}/ready`

Get issues ready to work on (unblocked, open).

- **Auth:** Required
- **Timeout:** 5 seconds
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `assignee` | string | Filter by assignee |
  | `type` | string | Filter by issue type |
  | `parent_id` | string | Filter by parent issue ID |
  | `priority` | int (0-4) | Filter by priority |
  | `labels` | string | Comma-separated labels (all must match) |
  | `limit` | int | Max results |
  | `unassigned` | bool | Only unassigned issues |
  | `include_deferred` | bool | Include deferred issues |
  | `source_repos` | string | Comma-separated repo names to filter by |

- Same response shape as `GET /api/ready` documented in [Issues](#issues)
- **Errors:** `400` (invalid params), `503` (pool unavailable), `504` (timeout)

#### `GET /api/workspaces/{ws}/blocked`

Get blocked issues.

- **Auth:** Required
- **Timeout:** 5 seconds
- **Query Parameters:**
  | Parameter | Type | Description |
  |-----------|------|-------------|
  | `parent_id` | string | Filter by parent issue ID |
  | `assignee` | string | Filter by assignee |
  | `type` | string | Filter by issue type |
  | `priority` | int (0-4) | Filter by priority |
  | `limit` | int | Max results |

- Same response shape as `GET /api/blocked` documented in [Issues](#issues)
- **Errors:** `400` (invalid params), `503` (pool unavailable), `504` (timeout)

#### `GET /api/workspaces/{ws}/issues/graph`

Get the dependency graph for visualization.

- **Auth:** Required
- **Timeout:** 10 seconds
- **Query Parameters:**
  | Parameter | Type | Default | Description |
  |-----------|------|---------|-------------|
  | `status` | string | `all` | Filter: all, open, closed |
  | `include_closed` | bool | `true` | Include closed issues (only applies when status=all) |
  | `source_repos` | string | | Comma-separated repo names to filter by |

> **Note:** This endpoint lives under `/issues/graph` despite being a view endpoint, following the existing route pattern.

- Same response shape as `GET /api/issues/graph` documented in [Issues](#issues)
- **Errors:** `400` (invalid params), `503` (pool unavailable), `504` (timeout)

### Daemon Status

#### `GET /api/workspaces/{ws}/daemon/status`

Get daemon runtime status for the workspace.

- **Auth:** Required
- **Timeout:** 2 seconds
- **No query params**
- Same response shape as `GET /api/daemon/status` documented in [Health & Status](#health--status)
- **Errors:** `503` (pool unavailable), `504` (timeout)

---

## Client Error & CSP Reporting

Two public POST endpoints for frontend observability — they accept error payloads from the browser, validate and truncate fields, log the reports via `slog.Warn`, and return `204 No Content`. Both are unauthenticated (errors may occur before auth bootstrap) and excluded from the global rate limiter (each has its own dedicated per-IP limiter).

### `POST /api/client-errors`

Accept and log client-side JavaScript errors.

- **Auth:** None (public — errors may occur before auth bootstrap)
- **Content-Type:** `application/json`
- **Rate Limit:** Dedicated per-IP — 10 requests/minute, burst 10 (excluded from global rate limiter)
- **Max Body Size:** 16 KB (enforced via `http.MaxBytesReader`)

**Request body:**

```json
{
  "type": "global-error",
  "message": "Uncaught TypeError: Cannot read properties of null",
  "stack": "at foo (app.js:1:5)\nat bar (app.js:10:3)",
  "url": "http://localhost:8080/",
  "line": 1,
  "col": 5,
  "userAgent": "Mozilla/5.0 ...",
  "timestamp": "2026-03-22T10:00:00.000Z"
}
```

| Field | Type | Required | Max Length | Description |
|-------|------|----------|-----------|-------------|
| `type` | string | yes | 50 chars | Error category (e.g. `"global-error"`, `"unhandled-rejection"`, `"react-error"`, `"api-error"`) |
| `message` | string | yes | 4096 chars | Error message text |
| `stack` | string | no | 8192 chars (truncated) | Stack trace |
| `url` | string | no | 2048 chars (truncated) | Page URL where the error occurred |
| `line` | int | no | — | Line number of the error |
| `col` | int | no | — | Column number of the error |
| `userAgent` | string | no | 512 chars (truncated) | Browser user agent string |
| `timestamp` | string | no | — | ISO 8601 timestamp from the client |

**Validation:**

- `type` is required and must not exceed 50 characters → 400
- `message` is required and must not exceed 4096 characters → 400
- Optional string fields (`stack`, `url`, `userAgent`) are silently truncated to their max lengths (not rejected)

**Response 204:** No Content — error logged successfully (empty body)

**Response 400:** Invalid JSON body, missing/empty `type`, missing/empty `message`, `type` too long, `message` too long

```json
{"error": "type is required"}
```

**Response 400 (oversized body):** Request body exceeds 16 KB — `http.MaxBytesReader` causes the JSON decode to fail, returning 400 `"invalid JSON body"` (same as other parse errors)

**Response 429:** Per-IP rate limit exceeded

```json
{"error": "rate limit exceeded", "retry_after": 6}
```

Includes `Retry-After` header with seconds until next allowed request.

**Frontend integration:**

The frontend `errorReporter.ts` module automatically sends errors to this endpoint. It implements:

- **Circuit breaker:** after 3 consecutive failures, stops reporting for 60 seconds
- **Deduplication:** same type+message suppressed within 5-second window
- **Timeout:** 5-second abort signal on each report
- **Fire-and-forget:** errors are sent asynchronously, never blocking the UI
- **Error types reported:** `global-error` (window.onerror), `unhandled-rejection` (unhandledrejection event), `react-error` (React error boundaries), `api-error` (fetchApi failures)

---

### `POST /api/csp-report`

Accept and log browser Content-Security-Policy violation reports.

- **Auth:** None (public — browsers send CSP reports automatically without auth headers)
- **Content-Type:** `application/csp-report` or `application/json` (both accepted)
- **Rate Limit:** Dedicated per-IP — 60 requests/minute (1/sec), burst 20 (excluded from global rate limiter)
- **Max Body Size:** 10 KB (enforced via `io.LimitReader`)

**Request body:** Standard browser CSP report format (envelope with `"csp-report"` key):

```json
{
  "csp-report": {
    "document-uri": "http://localhost:8080/",
    "violated-directive": "script-src",
    "effective-directive": "script-src",
    "original-policy": "default-src 'self'; script-src 'self' 'sha256-...'",
    "blocked-uri": "inline",
    "status-code": 200,
    "source-file": "http://localhost:8080/app.js",
    "line-number": 42,
    "column-number": 10
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `document-uri` | string | URI of the document where the violation occurred |
| `violated-directive` | string | The policy directive that was violated |
| `effective-directive` | string | The effective directive that caused the violation |
| `original-policy` | string | The full CSP policy string |
| `blocked-uri` | string | URI of the resource that was blocked |
| `status-code` | int | HTTP status code of the document |
| `source-file` | string | Source file where the violation occurred |
| `line-number` | int | Line number in the source file |
| `column-number` | int | Column number in the source file |

All fields are optional — browsers may send partial reports. The server logs whichever fields are present. String fields (`document-uri`, `violated-directive`, `blocked-uri`, `source-file`) are silently truncated to prevent oversized log entries (URIs to 2048 chars, directives to 512 chars).

**Response 204:** No Content — report logged successfully (empty body)

**Response 400:** Failed to read body, invalid JSON body

```json
{"error": "invalid JSON body"}
```

**Response 415:** Unsupported Media Type — Content-Type is neither `application/csp-report` nor `application/json`

```json
{"error": "unsupported content type"}
```

**Response 429:** Per-IP rate limit exceeded

```json
{"error": "rate limit exceeded", "retry_after": 1}
```

Includes `Retry-After` header with seconds until next allowed request.

**CSP header integration:**

The server's `SecurityHeadersMiddleware` sets a `Content-Security-Policy` response header on all responses that includes `report-uri /api/csp-report`. This causes browsers to automatically POST violation reports to this endpoint when a CSP rule is violated.

---

### Notes

- Both endpoints are public (no auth required) — this is intentional so errors during auth bootstrap and browser-initiated CSP reports are captured
- Body size limits differ: client-errors uses 16 KB (via `MaxBytesReader` which causes decode errors), CSP reports use 10 KB (via `io.LimitReader` which silently truncates)
- Client error validation rejects empty/missing required fields (`type`, `message`) with 400; CSP reports have no required fields (all optional per browser behavior)
- CSP report Content-Type validation is strict: only `application/csp-report` and `application/json` are accepted (415 for anything else); client errors rely on JSON decode failing for non-JSON
- Rate limiters are per-IP using `RemoteAddr` only (`X-Forwarded-For` not trusted to prevent spoofing)
- Rate limiter entries are cleaned up in a background goroutine: stale entries (no requests for 10 minutes) evicted every 5 minutes
- Different IPs get independent rate-limit buckets — one client being rate-limited does not affect others

## Rate Limiting

Per-IP token bucket rate limiting applied to all API endpoints (except `/health`, `/api/health`, `/api/client-errors`, and `/api/csp-report`).

| Operation Type | Rate | Burst |
|---------------|------|-------|
| Read (GET/HEAD/OPTIONS) | 100 req/sec | 200 |
| Mutating (POST/PUT/PATCH/DELETE) | 20 req/sec | 40 |

- Stale entries evicted after 10 minutes of inactivity (cleanup every 5 minutes)
- Returns `429 Too Many Requests` with `Retry-After` header
- `/api/client-errors` and `/api/csp-report` are excluded from this global limiter — they use dedicated per-endpoint rate limiters (see [Client Error & CSP Reporting](#client-error--csp-reporting))

## Error Codes

Error codes appear in the `code` field of error responses on issue-related endpoints:

| Code | Description |
|------|-------------|
| `POOL_NOT_INITIALIZED` | Daemon connection pool not available |
| `DAEMON_UNAVAILABLE` | Cannot connect to daemon |
| `CONNECTION_TIMEOUT` | Timeout connecting to daemon |
| `RPC_ERROR` | Error communicating with daemon |
| `DAEMON_ERROR` | Daemon returned an error |
| `PARSE_ERROR` | Failed to parse daemon response |
| `ENCODE_ERROR` | Failed to encode HTTP response |
| `INVALID_PARAMS` | Invalid query parameters |
| `INVALID_JSON` | Malformed request body |
| `REQUEST_TOO_LARGE` | Request body exceeds 1 MB |
| `VALIDATION_ERROR` | Request validation failed |

## Common HTTP Status Codes

| Code | Meaning |
|------|---------|
| `200` | Success |
| `201` | Resource created |
| `204` | No content (e.g., no tasks available for fleet claim) |
| `400` | Invalid input |
| `401` | Authentication required or invalid |
| `403` | Cross-origin request rejected |
| `404` | Resource not found |
| `409` | Conflict (circular dependency, already claimed, etc.) |
| `413` | Request body too large |
| `429` | Rate limit exceeded |
| `503` | Service unavailable (daemon down, fleet not configured) |
| `504` | Gateway timeout (daemon connection timeout) |
