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
- `GET /api/terminal/ws` (uses its own one-time token)
- `POST/GET /api/fleet/*` (use fleet-specific auth)
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

SSE hub and fleet coordination metrics.

- **Auth:** Required
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

Fleet metrics fields are omitted when fleet coordination is not enabled.

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

Fleet endpoints are only available when Redis is configured (`--fleet-redis`) and `--fleet-api-key` is set. Fleet endpoints use their own authentication (not the standard bearer token).

### `POST /api/fleet/register`

Register a fleet worker and obtain a JWT.

- **Auth:** `X-Fleet-API-Key` header (constant-time validated)
- **Rate Limit:** Per-IP rate limiting (when configured)
- **Request Body:**

```json
{
  "worker_id": "string (required, max 256 chars)",
  "repos": ["repo-name"]
}
```

- **Response:** `201 Created`

```json
{"success": true, "token": "<JWT>"}
```

- **Status Codes:** `400` (missing/invalid worker_id), `401` (invalid API key), `429` (rate limited), `503` (fleet not configured)

### JWT Claims

The JWT issued by registration contains:

```json
{
  "worker_id": "string",
  "repos": ["string"],
  "iat": 1705312800,
  "exp": 1705316400
}
```

Algorithm: HMAC-SHA256. Default expiry: 1 hour.

### `POST /api/fleet/claim`

Atomically claim a task to work on.

- **Auth:** JWT bearer token (from register)
- **Request Body:** (optional)

```json
{
  "issue_id": "specific-issue-id (optional)",
  "status": "open (optional, default)",
  "issue_type": "task (optional)",
  "max_priority": 2
}
```

- **Response:**
  - `200 OK` (task claimed):
    ```json
    {
      "success": true,
      "payload": {
        "issue": { ... },
        "labels": ["label"],
        "dependencies": [],
        "reason": "",
        "deadline": null
      }
    }
    ```
  - `204 No Content` (no tasks available)
  - `409 Conflict` (specific issue already claimed)

### `POST /api/fleet/done/{id}`

Mark a task as complete. The `{id}` is the worker ID.

- **Auth:** None (no JWT validation)
- **Request Body:**

```json
{
  "success": true,
  "commit_sha": "abc123 (optional)",
  "error": "failure reason (optional)"
}
```

- **Response:** `200 OK`

```json
{
  "success": true,
  "task_id": "claimed-task-id",
  "worker_id": "worker-id"
}
```

Idempotent: if the worker has no active claim, returns success without a task_id.

### `POST /api/fleet/heartbeat`

Keep a worker alive and update its status.

- **Auth:** JWT bearer token
- **Request Body:**

```json
{"worker_id": "string (required)"}
```

- **Response:** `200 OK`

```json
{
  "success": true,
  "last_heartbeat": "2024-01-15T12:00:00Z"
}
```

- **Status Codes:** `404` (worker not found)

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

## Terminal

### `GET /api/terminal/token`

Generate a one-time terminal authentication token.

- **Auth:** Required (standard bearer token)
- **Query Parameters:** `session` — session name (required)
- **Response:** `200 OK`

```json
{"token": "<one-time-use-token>"}
```

### `GET /api/terminal/ws`

WebSocket endpoint for terminal relay (tmux-backed).

- **Auth:** One-time token via `?token=` query param
- **Query Parameters:**
  - `session` — session name (required, alphanumeric + hyphens/underscores)
  - `token` — one-time terminal token (required when auth enabled)
- **Protocol:**
  - Binary frames for terminal I/O
  - In-band resize: marker byte `0x01` + 4 bytes big-endian uint32 (`rows << 16 | cols`)
  - Max terminal size: 500 cols x 200 rows
  - Read limit: 32 KB per message
  - Default size: 80x24

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

## Loom Proxy

### `/api/loom/**`

Proxies requests to the loom agent status server (same-origin to avoid CORS/CSP issues). Only available when a loom server URL is configured.

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

## Rate Limiting

Per-IP token bucket rate limiting applied to all API endpoints (except `/health` and `/api/health`).

| Operation Type | Rate | Burst |
|---------------|------|-------|
| Read (GET/HEAD/OPTIONS) | 100 req/sec | 200 |
| Mutating (POST/PUT/PATCH/DELETE) | 20 req/sec | 40 |

- Stale entries evicted after 10 minutes of inactivity (cleanup every 5 minutes)
- Returns `429 Too Many Requests` with `Retry-After` header

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
