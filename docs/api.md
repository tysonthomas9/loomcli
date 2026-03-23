# Loom WebUI API Reference

The loom WebUI server exposes a REST API for managing issues, dependencies, fleet coordination, real-time events, log streaming, and terminal access.

## Overview

- **Base URL:** `http://localhost:8080` (configurable via `--port` and `--bind`)
- **Content-Type:** `application/json` for all request and response bodies
- **Request body limit:** 1 MB

## Authentication

### Overview

The Loom WebUI uses a pre-shared API key for authentication. The key is auto-generated on first start and stored locally. The frontend bootstraps authentication by fetching the key from a same-origin-only endpoint, then includes it as a bearer token on all subsequent requests. WebSocket endpoints use a separate one-time token mechanism. Fleet endpoints use their own API key + JWT flow.

### Bearer Token Authentication

All protected endpoints require a bearer token via the `Authorization` header:

```
Authorization: Bearer <token>
```

For WebSocket and SSE connections (which cannot set custom headers from browsers), the token can be passed as a query parameter: `?token=<token>`.

**Token Extraction Priority:**
1. `Authorization: Bearer <token>` header (checked first)
2. `?token=<token>` query parameter (fallback for WebSocket/SSE browser connections)
3. If neither present: `401 Unauthorized`

When both header and query parameter are present, the header takes precedence.

Token comparison uses constant-time comparison (`subtle.ConstantTimeCompare`) to prevent timing attacks.

**Error Responses:**

401 Unauthorized (no token):
```json
{"error": "authentication required"}
```

401 Unauthorized (wrong token):
```json
{"error": "invalid token"}
```

### Token Bootstrap Endpoint: `GET /api/auth/token`

Returns the pre-shared API key so the frontend can bootstrap authentication without requiring users to manually copy tokens.

- **Auth:** None (public endpoint -- bypasses auth middleware)
- **Method:** GET
- **Path:** /api/auth/token

**Request Headers:**

| Header | Required | Description |
|--------|----------|-------------|
| Sec-Fetch-Site | No | Browser-sent header. If present, must be "same-origin". Non-browser clients (curl, etc.) that don't send this header are allowed through. |

#### When Auth Is Enabled

**Response 200 OK:**
```json
{"token": "a1b2c3d4e5f6..."}
```
- Token is a 64-character hex string (32 random bytes)
- `Content-Type: application/json`
- `Cache-Control: no-store` (prevents caching of the token)

**Response 403 Forbidden:**
```json
{"error": "cross-origin requests not allowed"}
```
- Returned when `Sec-Fetch-Site` header is present but not "same-origin"
- Prevents cross-origin browser requests from exfiltrating the API key
- Token value is NOT present in the 403 response body (no leak)

#### When Auth Is Disabled

**Response 404 Not Found:**
```json
{"error": "authentication not enabled"}
```
- `Content-Type: application/json`
- Explicit JSON 404 prevents SPA catch-all from returning 200 HTML

### Public Routes (No Auth Required)

These routes bypass the auth middleware entirely:

| Method | Path | Reason |
|--------|------|--------|
| GET | /health | Load balancer health check |
| GET | /api/health | Detailed health check |
| GET | /api/auth/token | Token bootstrap (same-origin protected) |
| GET | /api/terminal/ws | Uses its own one-time token auth |
| GET | /api/agents/{name}/terminal/ws | Uses its own one-time token auth |
| POST | /api/client-errors | Error reporting during auth bootstrap |
| POST | /api/csp-report | Browser-sent CSP violations (no auth headers) |
| ANY | /api/fleet/* | Fleet-specific auth (API key + JWT) |
| GET | non-/api/ paths | Frontend static files and SPA routes |
| OPTIONS | any path | CORS preflight (passes through before public route check) |

Non-GET methods on otherwise-public GET-only paths (e.g., POST /health) are NOT public.

### Auth-Disabled Mode

When `ServerConfig.AuthEnabled` is false, the auth middleware is not applied. The `GET /api/auth/token` endpoint is still registered but returns 404 JSON (`{"error": "authentication not enabled"}`) instead of serving a token. This explicit 404 prevents the SPA catch-all from returning 200 HTML that the frontend would misparse.

### API Key Lifecycle

**Generation:**
- `GenerateAPIKey()` produces 32 cryptographically random bytes via `crypto/rand`
- Encoded as 64-character hex string

**Storage:**
- Default path: `~/.loom/webui-api-key`
- File permissions: `0600` (owner read/write only)
- Parent directory created with `0700` if absent

**Loading (LoadOrCreateAPIKey):**
1. Attempt to read existing file
2. If file exists and contains non-whitespace content: return trimmed key
3. If file missing, empty, or whitespace-only: generate new key, write to file, return it
4. Idempotent: subsequent calls return the same key

**Configuration Override:**
- `ServerConfig.APIKey` can provide a pre-set key (skips file load)
- `ServerConfig.AuthEnabled` controls whether auth is active (default: true)

### Middleware Chain

Request processing order: `RequestLog -> RateLimit -> SecurityHeaders -> Auth -> CORS -> Router`

### Workspace-Scoped Path Normalization

The auth middleware normalizes workspace-prefixed paths before public route checks:
- `/api/workspaces/{ws}/fleet/claim` is normalized to `/api/fleet/claim`
- `/api/workspaces/{ws}/health` is normalized to `/api/health`
- Paths without the prefix are unchanged

This ensures workspace-scoped requests get the same public/protected classification as their global equivalents.

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

SSE hub runtime metrics with optional fleet coordination counters. This is the primary observability surface for the web UI server's push infrastructure and fleet coordination layer.

- **Auth:** Required
- **Query Parameters:** None
- **Path Parameters:** None
- **Request Body:** None

Returns a snapshot of SSE hub runtime metrics and, when fleet coordination is enabled, fleet claim operation counters. All counters are monotonically increasing (except `connected_clients` and `retry_queue_depth` which are gauges). The hub dependency is required -- if the SSE hub was not initialized at server startup, the endpoint returns 503.

Fleet metric fields (prefixed with `loom_fleet_`) use JSON `omitempty` -- they are entirely absent from the response when fleet coordination is not configured (`timeoutEnforcer` is nil and `claimMetrics` is nil). This means a non-fleet deployment returns only the 4 core SSE metrics. Note: because the fleet fields use `omitempty` on `int64`, a fleet-enabled deployment where all claim counters are 0 will also not show those fields.

**Response 200 OK (fleet enabled):**

```json
{
  "success": true,
  "data": {
    "connected_clients": 3,
    "dropped_mutations": 0,
    "retry_queue_depth": 0,
    "uptime_seconds": 3600.5,
    "loom_fleet_timeouts_total": 2,
    "loom_fleet_claims_success": 5,
    "loom_fleet_claims_collision": 1,
    "loom_fleet_claims_timeout": 0,
    "loom_fleet_claims_total": 6
  }
}
```

**Response 200 OK (fleet disabled -- no Redis):**

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

**Response 503 Service Unavailable (SSE hub not initialized):**

```json
{
  "success": false,
  "error": "SSE hub not initialized"
}
```

**Field Descriptions:**

| Field | Type | Gauge/Counter | Description |
|-------|------|---------------|-------------|
| connected_clients | int | gauge | Number of active SSE connections (browsers with /api/events open) |
| dropped_mutations | int64 | counter | Cumulative mutations dropped because a client's send channel was full |
| retry_queue_depth | int | gauge | Mutations currently queued for retry delivery to clients |
| uptime_seconds | float64 | gauge | Seconds since the SSE hub was created (server start) |
| loom_fleet_timeouts_total | int64 | counter | Fleet workers forcibly timed out by the TimeoutEnforcer. Omitted when fleet disabled |
| loom_fleet_claims_success | int64 | counter | Successful fleet task claims. Omitted when fleet disabled |
| loom_fleet_claims_collision | int64 | counter | Fleet claims that failed due to optimistic-lock collision. Omitted when fleet disabled |
| loom_fleet_claims_timeout | int64 | counter | Fleet claims that timed out waiting for a task. Omitted when fleet disabled |
| loom_fleet_claims_total | int64 | counter | Total fleet claim attempts (success + collision + timeout). Omitted when fleet disabled |

## Backend Configuration

### Data Model: BackendConfigData

```json
{
  "backend": "string (current backend name, default: \"claude\")",
  "source": "string (\"project\" if explicitly set in loom.yaml, \"default\" if using fallback)",
  "available": ["claude", "codex", "opencode", "gemini", "cursor", "shell"],
  "agents": [
    {
      "worktree": "string (agent worktree name)",
      "role": "string (agent role)",
      "backend": "string (per-agent backend override, empty if using project default)"
    }
  ]
}
```

Valid backends for PATCH operations: `claude`, `codex`, `opencode`, `gemini`, `cursor`. The `shell` backend is included in the `available` list for reading but CANNOT be set via PATCH endpoints.

### `GET /api/config/backend`

Read the project-level backend configuration from loom.yaml.

- **Auth:** Required
- **Query Parameters:** None

**Behavior:** Acquires daemon connection (2s timeout) to get workspace path, reads loom.yaml, extracts backend field and agent overrides. If backend field is empty or loom.yaml doesn't exist, defaults to "claude" with `source="default"`.

**Response 200 OK:**

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

- `available` always contains the 5 AI backends + "shell" (6 total)
- `agents` array contains per-agent overrides from loom.yaml; empty array if no agents defined
- `source` is "project" when backend field is explicitly set, "default" when using fallback

**Error Responses:**
- `500`: Failed to parse config (loom.yaml YAML parse error)
- `503`: Connection pool not initialized (pool is nil) or daemon not available (connection failed)
- `504`: Daemon not available (context deadline exceeded -- 2s timeout)

### `PATCH /api/config/backend`

Update the project-level backend in loom.yaml.

- **Auth:** Required
- **Request Body:**

```json
{
  "backend": "string (required, must be one of: claude, codex, opencode, gemini, cursor)"
}
```

"shell" is NOT accepted -- returns 400.

**Behavior:** Validates backend name, acquires daemon connection (2s timeout), reads existing loom.yaml (or creates empty if absent), updates backend field preserving all other fields (agents, daemon, roles), writes back to disk.

**Response 200 OK:**

```json
{
  "success": true,
  "data": {
    "backend": "codex",
    "source": "project",
    "available": ["claude", "codex", "opencode", "gemini", "cursor", "shell"],
    "agents": [
      {
        "worktree": "falcon",
        "role": "plan",
        "backend": "codex"
      }
    ]
  }
}
```

- `source` is always "project" after a successful PATCH
- Returns the full updated config in the same format as GET

**Error Responses:**
- `400`: Invalid request body (malformed JSON), invalid backend name
- `413`: Request body too large (>1 MB)
- `500`: Failed to parse config (read error), failed to save config (write error)
- `503`: Connection pool not initialized (pool is nil) or daemon not available
- `504`: Daemon not available (context deadline exceeded)

### `GET /api/workspaces/{ws}/config/backend`

Read backend configuration scoped to a specific workspace in multi-workspace mode. Behaves identically to `GET /api/config/backend` but uses the multiPool to route the daemon connection to the workspace-specific daemon.

- **Auth:** Required
- **Path Parameters:** `ws` -- workspace identifier (used by WorkspaceMiddleware to select the correct daemon pool)
- Same response format and error codes as `GET /api/config/backend`
- Only available when multiPool is configured (multi-workspace mode)

### `PATCH /api/workspaces/{ws}/config/backend`

Update backend configuration scoped to a specific workspace in multi-workspace mode. Behaves identically to `PATCH /api/config/backend` but uses the multiPool for workspace-specific daemon routing.

- **Auth:** Required
- **Path Parameters:** `ws` -- workspace identifier
- Same request body, response format, and error codes as `PATCH /api/config/backend`
- Only available when multiPool is configured (multi-workspace mode)

### `PATCH /api/workspace/{name}/config/backend`

Update a workspace's backend override in the global config (`~/.loom/config.yaml`). This is separate from the project-level endpoint -- it sets a per-workspace backend preference in the multi-workspace configuration.

- **Auth:** Required
- **Path Parameters:** `name` -- workspace name (matched against workspaces map in config.yaml)
- **Request Body:**

```json
{
  "backend": "string (required, non-empty, must be one of: claude, codex, opencode, gemini, cursor)"
}
```

**Behavior:** Validates input, loads `~/.loom/config.yaml` (YAML round-trip preserving all fields), looks up workspace by name, updates its backend field, saves atomically via temp file + rename, returns refreshed workspace data.

**Response 200 OK (with workspace config function):**

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

**Response 200 OK (without workspace config function):**

```json
{
  "success": true
}
```

**Error Responses:**
- `400`: Workspace name required (empty path param), backend is required (empty field), invalid backend name, invalid request body (malformed JSON)
- `404`: No config found (`~/.loom/config.yaml` doesn't exist), workspace not found (name not in workspaces map)
- `413`: Request body too large (>1 MB)
- `500`: Failed to load config (read/parse error), failed to save config (write error)

## Issues

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

Fleet endpoints are only available when Redis is configured (`--fleet-redis`) and `--fleet-api-key` is set. Fleet endpoints use their own authentication flow separate from the standard bearer token auth.

### Prerequisites

Fleet endpoints are only registered when `fleetEnabled` is true in routes.go -- this requires:
- Redis configured via `--fleet-redis`
- Fleet API key set via `--fleet-api-key`

When not configured, none of these endpoints exist and requests will 404 via the SPA catch-all.

### Authentication Model

Fleet uses a two-layer authentication model:

1. **Registration auth (X-Fleet-API-Key):** The register endpoint validates a pre-shared API key via the `X-Fleet-API-Key` header using constant-time comparison (`crypto/subtle.ConstantTimeCompare`). This is the bootstrap mechanism.
2. **JWT bearer auth (Authorization: Bearer):** After registration, the worker receives a JWT (HMAC-SHA256, default 1-hour expiry). The claim and heartbeat endpoints are protected by `FleetAuthMiddleware` which validates this JWT and injects `WorkerClaims` into the request context. The done endpoint currently has no JWT validation.

### JWT Claims

```json
{
  "worker_id": "string",
  "repos": ["string"],
  "iat": 1705312800,
  "exp": 1705316400
}
```

Algorithm: HMAC-SHA256. Signing key is managed in Redis via `SigningKeyManager` (supports key rotation with previous-version grace period). Default expiry: 1 hour.

### Data Models

**Worker:**
```json
{
  "worker_id": "string",
  "repos": ["string"],
  "registered_at": 1705312800
}
```
Redis key: `fleet:workers:{workerID}`, TTL: 2 hours.

**ClaimResponse:**
```json
{
  "task_id": "string",
  "success": true,
  "payload": {}
}
```
Redis key: `fleet:worker:claim:{workerID}`, TTL: 5 minutes.

**TaskResult:**
```json
{
  "worker_id": "string",
  "task_id": "string",
  "success": true,
  "commit_sha": "string",
  "error": "string",
  "completed_at": "RFC3339"
}
```
Redis key: `fleet:task:result:{taskID}`, TTL: 24 hours.

### `POST /api/fleet/register`

Register a fleet worker and obtain a JWT.

- **Auth:** `X-Fleet-API-Key` header (constant-time validated against `--fleet-api-key`)
- **Rate Limit:** Per-IP sliding window rate limiting (when Redis-based FleetRateLimiter is configured)
- **Max Body Size:** 1 MB
- **Request Body:**

```json
{
  "worker_id": "string (required, max 256 chars, no colons/newlines/tabs/spaces)",
  "repos": ["repo-name (optional)"]
}
```

**Behavior:** Registers (or re-registers) the worker in Redis with a 2-hour TTL. Re-registration is idempotent -- it updates the timestamp and repos. After successful registration, generates and returns a JWT token signed with the shared HMAC-SHA256 key.

**Response 201 Created:**
```json
{"success": true, "token": "<JWT>"}
```

**Error Responses:**
- `400`: worker_id missing, empty, or exceeds 256 characters; invalid/malformed request body
- `401`: Missing X-Fleet-API-Key header, or invalid API key
- `413`: Request body too large (>1 MB)
- `429`: Rate limit exceeded (per-IP)
- `500`: Redis registration failure, or JWT generation failure
- `503`: Fleet store or token config is nil ("fleet API not available"), or API key not configured ("fleet authentication not configured")

**Timeout:** 30-second context timeout on Redis registration.

### `POST /api/fleet/claim`

Atomically claim a task to work on.

- **Auth:** JWT bearer token (from registration) -- validated by FleetAuthMiddleware when signing key is configured; no auth when signing key is not configured
- **Max Body Size:** 1 MB
- **Request Body:** (optional -- can be empty or omitted entirely)

```json
{
  "issue_id": "string (optional -- claim a specific issue)",
  "status": "string (optional, default: 'open')",
  "issue_type": "string (optional -- filter by issue type)",
  "max_priority": 2
}
```

If the body is empty or Content-Length is 0, the endpoint finds the highest-priority ready task automatically.

**Behavior:**
- **Specific issue (issue_id provided):** Attempts to atomically claim the specified issue via RPC Update with Claim=true, setting status to "in_progress". Returns 409 if already claimed by another worker.
- **Auto-assignment (no issue_id):** Calls RPC Ready to fetch up to 10 candidate tasks (filtered by optional status, issue_type, max_priority), then iterates through them attempting to claim each one. Returns the first successfully claimed task. Returns 204 if no tasks are available or all candidates are already claimed.

**Response 200 OK (task claimed):**
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

**Response 204 No Content:** No tasks available (no body returned).

**Error Responses:**
- `400`: Invalid/malformed request body
- `401`: Missing/invalid JWT (when FleetAuthMiddleware is active)
- `404`: Specific issue_id not found
- `409`: Specific issue already claimed by another worker
- `413`: Request body too large (>1 MB)
- `500`: RPC error, daemon error, or response parse failure
- `503`: Connection pool not initialized
- `504`: Timeout acquiring daemon connection (5-second deadline)

**Metrics:** Records claim outcomes (success/collision/timeout) via `fleet.ClaimMetrics`.

### `POST /api/fleet/done/{id}`

Mark a task as complete. The `{id}` is the worker ID.

- **Auth:** None (no JWT validation)
- **Path Parameters:**
  | Param | Type | Required | Description |
  |-------|------|----------|-------------|
  | id | string | yes | Worker ID (from `r.PathValue("id")`) |
- **Max Body Size:** 1 MB
- **Request Body:**

```json
{
  "success": true,
  "commit_sha": "abc123 (optional, for successful tasks)",
  "error": "failure reason (optional, for failed tasks)"
}
```

**Behavior:**
1. Validates worker exists in Redis (GetWorker)
2. Looks up worker's current claim (GetWorkerClaim)
3. If no active claim, returns success idempotently (no task_id in response)
4. Records task result in Redis (24-hour TTL) via RecordTaskResult
5. Releases the claim key via ReleaseClaim (also clears claim time tracking)
6. Clears worker claim cache (best-effort, uses fresh 2-second context)

**Response 200 OK (task completed):**
```json
{
  "success": true,
  "task_id": "claimed-task-id",
  "worker_id": "worker-id"
}
```

**Response 200 OK (idempotent -- no active claim):**
```json
{
  "success": true,
  "worker_id": "worker-id"
}
```

**Error Responses:**
- `400`: Missing worker ID in path, invalid/malformed request body
- `404`: Worker not found in Redis
- `413`: Request body too large (>1 MB)
- `500`: Redis lookup/write failure (get worker, get claim, record result, release claim)
- `503`: Fleet store is nil ("fleet API not available")

**Timeout:** 5-second context timeout for main operations; 2-second fresh context for claim cache cleanup.

### `POST /api/fleet/heartbeat`

Keep a worker alive by refreshing its registration TTL.

- **Auth:** JWT bearer token (from registration) -- validated by FleetAuthMiddleware when signing key is configured
- **Max Body Size:** 1 MB
- **Request Body:**

```json
{
  "worker_id": "string (required, max 256 chars)"
}
```

**Behavior:** Refreshes the worker's registration TTL in Redis back to 2 hours. This keeps the worker "alive" -- without heartbeats, the registration expires after the initial 2-hour TTL. The heartbeat uses EXPIRE (not SET) so it only succeeds if the worker registration key still exists.

**Response 200 OK:**
```json
{
  "success": true,
  "last_heartbeat": "2024-01-15T12:00:00Z"
}
```

**Error Responses:**
- `400`: worker_id missing, empty, exceeds 256 characters, or invalid/malformed request body
- `404`: Worker not found (registration expired or never registered)
- `413`: Request body too large (>1 MB)
- `500`: Redis heartbeat failure
- `503`: Fleet store is nil ("fleet store not initialized")

**Timeout:** 2-second context timeout on Redis heartbeat update.

### Redis Key Reference

When workspace-scoped (`Store.workspaceID` is set), all Redis keys are prefixed with `fleet:{wsID}:` instead of the global `fleet:` prefix.

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `fleet:workers:{workerID}` | 2 hours | Worker registration |
| `fleet:worker:claim:{workerID}` | 5 minutes | Active claim |
| `fleet:task:result:{taskID}` | 24 hours | Task result |

### Timeout Enforcement

A background `TimeoutEnforcer` runs periodically (default: check every 1 minute, timeout after 30 minutes) that releases claims and invokes a callback for timed-out tasks.

## Log Streaming

### `GET /api/agents/{name}/logs`

Get recent log lines for an agent.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name (alphanumeric, hyphens, underscores)
- **Query Parameters:** `lines` -- number of lines (default 200, max 10000)
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
- **Query Parameters:** `since` -- start from specific line number
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
- **Path Parameters:** `phase` -- `planning` or `implementation`
- **Query Parameters:** `lines` -- number of lines (default 200, max 10000)
- **Response:** Same shape as agent logs

### `GET /api/tasks/{id}/logs/{phase}/stream`

Real-time task log streaming via SSE.

- **Auth:** Required
- **Query Parameters:** `since` -- start from specific line number
- **Event Format:** Same as agent log stream

## Terminal

### `GET /api/terminal/token`

Generate a one-time terminal authentication token.

- **Auth:** Required (standard bearer token)
- **Query Parameters:** `session` -- session name (required)
- **Response:** `200 OK`

```json
{"token": "<one-time-use-token>"}
```

### `GET /api/terminal/ws`

WebSocket endpoint for terminal relay (tmux-backed).

- **Auth:** One-time token via `?token=` query param
- **Query Parameters:**
  - `session` -- session name (required, alphanumeric + hyphens/underscores)
  - `token` -- one-time terminal token (required when auth enabled)
- **Protocol:**
  - Binary frames for terminal I/O
  - In-band resize: marker byte `0x01` + 4 bytes big-endian uint32 (`rows << 16 | cols`)
  - Max terminal size: 500 cols x 200 rows
  - Read limit: 32 KB per message
  - Default size: 80x24

## Agent Terminal

Agent terminal endpoints provide terminal access to individual agent tmux sessions. They mirror the main terminal WebSocket protocol (same binary relay, same resize protocol, same close codes) but differ in session resolution and capabilities.

**Key differences from the main terminal WebSocket:**
- No scrollback buffer (nil passed to ptyToWS)
- No deferred kill cancellation on attach
- No SSE terminal_session_change broadcast on connect
- No context banner injection
- Session is auto-discovered by agent name (not provided directly by the client)
- Token scope is agent-specific ("agent:\<name\>:logs"), not session-based

### Session Resolution

Agent terminal endpoints do NOT use a session name provided by the client. Instead, `FindLatestAgentSession` scans all tmux sessions for the newest one matching the pattern `loom-<role>-<agent>-<pid>` (e.g., `loom-lead-ember-12345`). When multiple sessions match (e.g., `loom-lead-ember-123` and `loom-lead-ember-456`), the newest by created timestamp is returned (tie-broken by name lexicographic order).

### `GET /api/agents/{name}/terminal/info`

Determine whether an agent has a live tmux session or should fall back to archive logs.

- **Auth:** Required (standard bearer token)
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)

**Response 200 OK (tmux mode):**
```json
{
  "success": true,
  "data": {
    "agent": "ember",
    "mode": "tmux"
  }
}
```

**Response 200 OK (archive mode -- no live session found):**
```json
{
  "success": true,
  "data": {
    "agent": "ember",
    "mode": "archive"
  }
}
```

**Error Responses:**
- `400`: Missing agent name (`{"success": false, "error": "missing agent name"}`), invalid agent name (`{"success": false, "error": "invalid agent name: must match [a-zA-Z0-9_-]+"}`)
- `500`: `{"success": false, "error": "failed to inspect terminal sessions"}` -- tmux list-sessions failure
- `503`: `{"success": false, "error": "terminal manager not initialized"}` -- TerminalManager is nil

### `GET /api/agents/{name}/terminal/token`

Generate a one-time HMAC-SHA256 token scoped to a specific agent's terminal.

- **Auth:** Required (standard bearer token)
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Cache-Control:** `no-store` header set on response (prevents caching of one-time tokens)

The token is single-use, expires in 60 seconds, and is tied to a random nonce tracked server-side. The scope is "agent:\<name\>:logs".

**Response 200 OK:**
```json
{
  "success": true,
  "data": {
    "token": "<base64url-encoded-one-time-token>"
  }
}
```

**Error Responses:**
- `400`: Missing agent name, invalid agent name
- `500`: `{"success": false, "error": "failed to generate token"}` -- HMAC generation failure
- `503`: `{"success": false, "error": "terminal authentication not initialized"}` -- terminalAuth is nil

This endpoint is only registered when both TerminalManager and terminalAuth are initialized. If termAuth is nil at startup, the route does not exist (404 via SPA catch-all).

### `GET /api/agents/{name}/terminal/ws` (WebSocket)

WebSocket endpoint for live agent terminal relay.

- **Auth:** One-time token via `?token=` query param (public route -- bypasses bearer auth middleware)
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Query Parameters:**
  | Param | Type | Required | Description |
  |-------|------|----------|-------------|
  | token | string | yes | One-time terminal token (scoped to agent:\<name\>:logs) |

**Pre-upgrade validation (returns HTTP JSON before WebSocket upgrade):**
- `400`: Missing agent name, invalid agent name
- `401`: Terminal authentication failed (invalid/expired/replayed token, or scope mismatch)
- `404`: No active terminal session for agent (FindLatestAgentSession found no matching tmux session)
- `500`: Failed to inspect terminal sessions (tmux list-sessions error)
- `503`: Terminal manager not initialized, terminal authentication not initialized, maximum terminal sessions reached

**WebSocket binary protocol (identical to main terminal):**
- All frames are binary (MessageBinary)
- Server to Client: raw PTY output bytes (read buffer 4096 bytes)
- Client to Server: raw terminal input bytes OR resize message
- **Resize message format (in-band): exactly 5 bytes: `[0x01, cols_hi, cols_lo, rows_hi, rows_lo]`**
  - Byte 0: `0x01` (resizeMsgMarker)
  - Bytes 1-2: cols as uint16 big-endian
  - Bytes 3-4: rows as uint16 big-endian
- Max terminal size: 500 cols x 200 rows (values exceeding these are silently ignored)
- Zero values for cols or rows are silently ignored
- Read limit: 32 KB per WebSocket message (wsReadLimit = 32768)
- Default terminal size: 80x24 (set on attach; frontend sends resize immediately after connect)
- Non-matching binary messages (wrong length or missing 0x01 marker) are treated as regular terminal input

**Close codes:**

| Code | Meaning | Frontend behavior |
|------|---------|-------------------|
| 1000 | Normal closure / session detached | Allow reconnect |
| 4001 | Backend process exited (crash) | Show CrashOverlay, NO auto-reconnect |

Close reason on crash: last 10 lines of PTY output (truncated to 123 bytes UTF-8-safe).

Session attachment uses `AttachExistingRaw` -- attaches to an already-running tmux session without prefix rewriting or session creation. The session must already exist.

## Git Operations

All git endpoints are conditionally registered -- they only exist when `gitOps` is non-nil (worktree configuration present). When gitOps is nil, requests fall through to the SPA catch-all (returns HTML, not 404 JSON). All endpoints require standard bearer token authentication.

### Cross-Cutting: Agent Resolution

All agent-scoped git endpoints use a shared `resolveAgent()` helper:
1. Extracts `{name}` from the URL path
2. Validates against regex `^[a-zA-Z0-9_-]+$`
3. Resolves via `GitOps.ResolveAgentWorktree(name)`
4. Returns `400` for missing/invalid name, `404` for unresolved worktree

### Cross-Cutting: Git Ref Validation

Branch/ref parameters are validated against regex `^[a-zA-Z0-9][a-zA-Z0-9_./-]*$` and must not contain `..` (path traversal prevention). Refs starting with `-` are rejected (command injection prevention).

### `POST /api/git/push-all`

Push all agent worktree branches to their target branches.

- **Auth:** Required
- **Request Body:** None

**Behavior:** Iterates all agent worktrees, pushes each worktree branch to its default target branch. Uses remote from worktree config (falls back to "origin"). Non-atomic -- partial failures are reported per-worktree.

**Response 200 OK:**
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
      "error": "merge conflict in file.go"
    }
  ],
  "pushed": 1,
  "failed": 1
}
```

- Each result has `name` + (`success`+`message`) OR (`error`). The `success` field is absent (false) when `error` is present.
- "already up to date" entries are `success=true` but do NOT increment the `pushed` counter.
- `pushed`/`failed` counts only reflect actual push successes and failures.

**Error Responses:**
- `500`: Listing worktrees failed (`{"error": "listing worktrees: ..."}`)

### `POST /api/agents/{name}/git/push`

Merge agent branch into target branch (loom push semantics -- not git push).

- **Auth:** Required
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Request Body:** (optional)

```json
{
  "target": "branch-name"
}
```

- `target`: optional, defaults to worktree's DefaultBranch if empty/omitted
- Validated against git ref regex; must not contain ".."

**Behavior:** Fetches remote, checks out target, merges worktree branch, pushes result.

**Response 200 OK (success):**
```json
{
  "success": true,
  "message": "merged agent/drift into v2",
  "already_up_to_date": false
}
```

**Response 200 OK (already up to date):**
```json
{
  "success": true,
  "message": "already up to date",
  "already_up_to_date": true
}
```

**Response 409 Conflict (merge conflicts):**
```json
{
  "success": false,
  "message": "merge conflicts detected",
  "already_up_to_date": false,
  "conflicted_files": ["path/to/file.go"]
}
```

**Error Responses:**
- `400`: Missing agent name, invalid agent name, invalid target branch name
- `404`: Agent worktree not found
- `502`: Git operation failed (`{"error": "..."}`)

### `POST /api/agents/{name}/git/pull`

Merge source branch into agent's current branch.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Request Body:** (optional)

```json
{
  "source": "branch-name"
}
```

- `source`: optional, defaults to worktree's DefaultBranch if empty/omitted
- Validated against git ref regex; must not contain ".."

**Behavior:** Resolves the worktree's current branch (via GetCurrentBranch), then merges the source branch INTO that current branch.

**Response 200 OK (success):**
```json
{
  "success": true,
  "message": "merged v2 into agent/drift",
  "already_up_to_date": false
}
```

**Response 409 Conflict (merge conflicts):**
```json
{
  "success": false,
  "message": "merge conflicts detected",
  "already_up_to_date": false,
  "conflicted_files": ["path/to/file.go"]
}
```

**Error Responses:**
- `400`: Missing agent name, invalid agent name, invalid source branch name
- `404`: Agent worktree not found
- `500`: Failed to get current branch (`{"error": "getting current branch: ..."}`)
- `502`: Git operation failed

### `POST /api/agents/{name}/git/sync`

Full push+pull cycle using worktree's DefaultBranch.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Request Body:** None

**Behavior:** Two-phase operation -- first pushes worktree branch to default target, then pulls from target back into worktree. Stops and returns conflict on push failure.

**Response 200 OK (both succeed):**
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

**Response 409 Conflict (push conflicts -- pull not attempted):**
```json
{
  "push_result": {
    "success": false,
    "message": "merge conflicts detected",
    "already_up_to_date": false,
    "conflicted_files": ["file.go"]
  }
}
```

`pull_result` is null/absent when push has conflicts.

**Response 409 Conflict (push succeeds, pull conflicts):**
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
    "conflicted_files": ["file.go"]
  }
}
```

**Error Responses:**
- `400`: Missing/invalid agent name
- `404`: Agent worktree not found
- `500`: Failed to get current branch
- `502`: Push or pull git operation failed

### `POST /api/agents/{name}/git/pr`

Create a GitHub PR from agent branch to target branch.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Request Body:** (optional)

```json
{
  "target": "branch-name"
}
```

- `target`: optional, defaults to worktree's DefaultBranch
- Validated against git ref regex; must not contain ".."

**Pre-check:** Verifies gh CLI is installed via `CheckGhInstalled()`.

**Response 201 Created (PR created):**
```json
{
  "url": "https://github.com/org/repo/pull/42",
  "created": true,
  "already_exists": false,
  "no_commits": false
}
```

**Response 200 OK (PR already exists):**
```json
{
  "url": "https://github.com/org/repo/pull/42",
  "created": false,
  "already_exists": true,
  "no_commits": false
}
```

**Response 200 OK (no commits to merge):**
```json
{
  "created": false,
  "already_exists": false,
  "no_commits": true
}
```

**Error Responses:**
- `400`: Missing/invalid agent name, invalid target branch
- `404`: Agent worktree not found
- `502`: gh CLI PR creation failed
- `503`: gh CLI not installed (`{"error": "gh CLI not installed: install from https://cli.github.com/ and run 'gh auth login'"}`)

### `POST /api/agents/{name}/git/reset`

Hard reset worktree to a specified branch.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Request Body:** (optional)

```json
{
  "branch": "target-branch",
  "force": false,
  "push": false
}
```

- `branch`: optional, defaults to worktree's DefaultBranch
- `force`: if true, bypasses agent lock check
- `push`: if true, force-pushes the branch to origin after resetting
- Validated against git ref regex; must not contain ".."

**Response 200 OK (success):**
```json
{
  "success": true,
  "message": "reset to v2",
  "previous_branch": "agent/drift",
  "pushed": false
}
```

`previous_branch` is omitted when empty (omitempty).

**Response 423 Locked (agent locked -- cannot reset):**
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

- `task_id` omitted when empty (omitempty)
- Only returned when `force=false` and agent has active lock

**Error Responses:**
- `400`: Missing/invalid agent name, invalid branch name
- `404`: Agent worktree not found
- `502`: Git operation failed

### `GET /api/agents/{name}/git/status`

Detailed git status for an agent worktree.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Query Parameters:** None

**Response 200 OK:**
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

- `ahead`/`behind` are relative to the target branch (after fetch)
- `changed_files`: list of file paths with uncommitted changes
- `conflicted_files`: list of file paths with merge conflicts
- `stash_count`: number of stash entries

**Error Responses:**
- `400`: Missing/invalid agent name
- `404`: Agent worktree not found
- `500`: Git status operation failed (`{"error": "getting git status: ..."}`)

### `PATCH /api/agents/{name}/git/target`

Change target/integration branch for an agent worktree.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name
- **Request Body:** (required)

```json
{
  "branch": "new-target-branch"
}
```

- `branch`: required, non-empty
- Validated against git ref regex; must not contain ".."

**Pre-check:** Only supported in workspace mode (`wt.IsWorkspace` must be true).

**Response 200 OK:**
```json
{
  "success": true,
  "branch": "new-target-branch"
}
```

**Error Responses:**
- `400`: Missing/invalid agent name, "branch is required" (empty branch), "invalid branch name", "target branch update only supported in workspace mode" (non-workspace worktree), "invalid request body" (malformed JSON)
- `404`: Agent worktree not found
- `500`: Updating target branch failed

### `GET /api/issues/{id}/git/diff-stat`

Diff statistics for an issue's assigned agent worktree.

- **Auth:** Required
- **Path Parameters:** `id` -- issue ID (e.g., "loomcli-abc12")

**Behavior:** Looks up issue via daemon RPC to get assignee, resolves assignee to agent worktree, computes diff statistics (added/removed lines) against the worktree's default branch.

**Response 200 OK:**
```json
{
  "branch": "agent/drift",
  "added": 142,
  "removed": 37
}
```

**Error Responses:**
- `400`: Missing issue ID
- `404`: "issue not found: {id}" (daemon RPC lookup failed), "issue has no assignee (no agent worktree)" (no assignee field), "agent worktree not found for {assignee}" (worktree resolution failed)
- `500`: Internal server error (daemon RPC parse failure)
- `503`: Daemon not available (RPC pool connection failure, 5s timeout)

## Agent Diff / Code Review

These endpoints provide diff and code-review capabilities for agent worktrees. They are conditionally registered inside the same `if gitOps != nil` block as the core git endpoints.

Unlike the core git endpoints (which use raw structs), diff endpoints use the `diffResponse` envelope:
```json
{"success": true, "data": { ... }}
```
or on error:
```json
{"success": false, "error": "message"}
```

Note: `resolveAgent()` errors use `respondError` (not diffResponse), which is a subtly different envelope from the diff-specific errors.

### Cross-Cutting: Merge-Base Resolution

All three diff endpoints use `resolveMergeBaseDefault()` for the "from" ref:
1. If `?from=` query param is provided and valid, uses it directly
2. If `?from=` contains ".." or fails validGitRef, returns 400
3. If `?from=` is empty/omitted, calls `ops.ResolveMergeBase(wt.Path, wt.DefaultBranch)` to compute merge-base
4. If merge-base resolution fails, returns 500

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
  "status": "M|A|D|R|C (modified/added/deleted/renamed/copied)",
  "old_path": "original path (omitted unless renamed/copied)",
  "additions": 10,
  "deletions": 5
}
```

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

### `GET /api/agents/{name}/diff/commits`

List commits between merge-base and HEAD.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Query Parameters:**
  | Param | Type | Required | Default | Description |
  |-------|------|----------|---------|-------------|
  | from | string | no | merge-base of worktree's DefaultBranch | Start ref for commit range |
  | limit | int | no | 0 (unlimited) | Maximum number of commits to return |

**Response 200 OK:**
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

`commits` is always an array (never null) -- nil slices are normalized to `[]`.

**Error Responses:**
- `400`: Missing agent name, invalid agent name, invalid from ref (fails regex or contains ".."), invalid limit value (non-integer string)
- `404`: Agent worktree not found
- `500`: "failed to resolve merge-base: ..." (merge-base computation failed, when from is omitted), "failed to get diff commits: ..." (git log operation failed)

### `GET /api/agents/{name}/diff/files`

List changed files between two refs.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Query Parameters:**
  | Param | Type | Required | Default | Description |
  |-------|------|----------|---------|-------------|
  | to | string | yes | -- | End ref for diff (typically "HEAD" or a commit SHA) |
  | from | string | no | merge-base of worktree's DefaultBranch | Start ref for diff |

**Response 200 OK:**
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

`files` is always an array (never null) -- nil slices are normalized to `[]`. `old_path` is only present for renamed (R) or copied (C) files. Status values: M (modified), A (added), D (deleted), R (renamed), C (copied).

**Error Responses:**
- `400`: Missing agent name, invalid agent name, "missing required query parameter: to" (to param is mandatory), "invalid to ref" (fails regex or contains ".."), "invalid from ref" (explicit from fails regex or contains "..")
- `404`: Agent worktree not found
- `500`: "failed to resolve merge-base: ..." (merge-base computation failed, when from is omitted), "failed to get diff files: ..." (git diff operation failed)

### `GET /api/agents/{name}/diff/file`

Get unified diff patch for a single file.

- **Auth:** Required
- **Path Parameters:** `name` -- agent name (validated: `^[a-zA-Z0-9_-]+$`)
- **Query Parameters:**
  | Param | Type | Required | Default | Description |
  |-------|------|----------|---------|-------------|
  | path | string | yes | -- | Relative file path within the worktree |
  | to | string | yes | -- | End ref for diff (typically "HEAD" or a commit SHA) |
  | from | string | no | merge-base of worktree's DefaultBranch | Start ref for diff |

**Path validation:** The path parameter must be a relative path with no ".." traversal, must not start with "/", and must not resolve to "." or ".." after cleaning.

**Response 200 OK (normal diff):**
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

**Response 200 OK (binary file):**
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

**Response 200 OK (file too large):**
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

**Error Responses:**
- `400`: Missing agent name, invalid agent name, "missing required query parameter: path", "invalid path: must be relative with no '..' traversal" (absolute path, empty, ".", "..", or contains "../"), "missing required query parameter: to", "invalid to ref" (fails regex or contains ".."), "invalid from ref" (explicit from fails regex or contains "..")
- `404`: Agent worktree not found
- `500`: "failed to resolve merge-base: ..." (merge-base computation failed, when from is omitted), "failed to get diff patch: ..." (git diff operation failed)

## Loom Proxy

### `/api/loom/**`

Proxies requests to the loom agent status server (same-origin to avoid CORS/CSP issues). Only available when a loom server URL is configured.

## Client Error & CSP Reporting

These two observability endpoints allow the frontend and browsers to report errors back to the server. Both are unauthenticated (public), excluded from the global rate limiter, and protected by their own dedicated per-IP token-bucket rate limiters.

### `POST /api/client-errors`

Accept and log client-side JavaScript errors.

- **Auth:** None (public endpoint -- errors may occur before auth bootstrap)
- **Content-Type:** application/json
- **Rate Limit:** Dedicated per-IP limiter -- 10 requests/minute, burst 10 (excluded from global rate limiter)
- **Max Body Size:** 16 KB (enforced via `http.MaxBytesReader`)
- **Request Body:**

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
| type | string | yes | 50 chars | Error category (e.g. "global-error", "unhandled-rejection", "react-error", "api-error") |
| message | string | yes | 4096 chars | Error message text |
| stack | string | no | 8192 chars (truncated) | Stack trace |
| url | string | no | 2048 chars (truncated) | Page URL where the error occurred |
| line | int | no | -- | Line number of the error |
| col | int | no | -- | Column number of the error |
| userAgent | string | no | 512 chars (truncated) | Browser user agent string |
| timestamp | string | no | -- | ISO 8601 timestamp from the client |

**Validation:**
- `type` is required and must not exceed 50 characters (400 if violated)
- `message` is required and must not exceed 4096 characters (400 if violated)
- Optional string fields (`stack`, `url`, `userAgent`) are silently truncated to their max lengths (not rejected)

**Response 204 No Content:** Error logged successfully (empty body).

**Error Responses:**
- `400`: Invalid JSON body, missing/empty type, missing/empty message, type too long, message too long
  ```json
  {"error": "type is required"}
  ```
- `413`: Request body exceeds 16 KB (surfaced as 400 "invalid JSON body" since MaxBytesReader truncates mid-parse)
- `429`: Per-IP rate limit exceeded
  ```json
  {"error": "rate limit exceeded", "retry_after": 6}
  ```
  Includes `Retry-After` header with seconds until next allowed request.

**Frontend Integration:**
The frontend `errorReporter.ts` module automatically sends errors to this endpoint with:
- Circuit breaker: after 3 consecutive failures, stops reporting for 60 seconds
- Deduplication: same type+message suppressed within 5-second window
- Timeout: 5-second abort signal on each report
- Fire-and-forget: errors are sent asynchronously, never blocking the UI
- Error types: global-error (window.onerror), unhandled-rejection, react-error (React error boundaries), api-error (fetchApi failures)

### `POST /api/csp-report`

Accept and log browser Content-Security-Policy violation reports.

- **Auth:** None (public endpoint -- browsers send CSP reports automatically without auth headers)
- **Content-Type:** `application/csp-report` OR `application/json` (both accepted)
- **Rate Limit:** Dedicated per-IP limiter -- 60 requests/minute (1/sec), burst 20 (excluded from global rate limiter)
- **Max Body Size:** 10 KB (enforced via `io.LimitReader`)
- **Request Body:** Standard browser CSP report format (envelope with "csp-report" key):

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
| document-uri | string | URI of the document where the violation occurred |
| violated-directive | string | The policy directive that was violated |
| effective-directive | string | The effective directive that caused the violation |
| original-policy | string | The full CSP policy string |
| blocked-uri | string | URI of the resource that was blocked |
| status-code | int | HTTP status code of the document |
| source-file | string | Source file where the violation occurred |
| line-number | int | Line number in the source file |
| column-number | int | Column number in the source file |

All fields are optional -- browsers may send partial reports. The server logs whichever fields are present.

**Response 204 No Content:** Report logged successfully (empty body).

**Error Responses:**
- `400`: Failed to read body, invalid JSON body
  ```json
  {"error": "invalid JSON body"}
  ```
- `415`: Unsupported Media Type -- Content-Type is neither `application/csp-report` nor `application/json`
  ```json
  {"error": "unsupported content type"}
  ```
- `429`: Per-IP rate limit exceeded
  ```json
  {"error": "rate limit exceeded", "retry_after": 1}
  ```
  Includes `Retry-After` header with seconds until next allowed request.

**CSP Header Integration:**
The server's SecurityHeadersMiddleware sets a Content-Security-Policy response header on all responses that includes `report-uri /api/csp-report`. This causes browsers to automatically POST violation reports to this endpoint when a CSP rule is violated.

## Rate Limiting

Per-IP token bucket rate limiting applied to all API endpoints (except `/health`, `/api/health`, `/api/client-errors`, and `/api/csp-report`).

| Operation Type | Rate | Burst |
|---------------|------|-------|
| Read (GET/HEAD/OPTIONS) | 100 req/sec | 200 |
| Mutating (POST/PUT/PATCH/DELETE) | 20 req/sec | 40 |

- Stale entries evicted after 10 minutes of inactivity (cleanup every 5 minutes)
- Returns `429 Too Many Requests` with `Retry-After` header

`/api/client-errors` and `/api/csp-report` are excluded from the global rate limiter and use their own dedicated per-endpoint rate limiters (see Client Error & CSP Reporting section).

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
| `415` | Unsupported media type (CSP report with wrong content-type) |
| `423` | Locked (agent has active lock, cannot reset) |
| `429` | Rate limit exceeded |
| `502` | Bad gateway (git operation failed) |
| `503` | Service unavailable (daemon down, fleet not configured) |
| `504` | Gateway timeout (daemon connection timeout) |
