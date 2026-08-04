## Rate limiting

A per-IP token bucket is applied to API requests
(`internal/webui/server/middleware/ratelimit.go`). Defaults from
`DefaultRateLimitConfig` (line 26):

| Operation | Rate | Burst |
|-----------|------|-------|
| Read (`GET`/`HEAD`/`OPTIONS`) | 100 req/s | 200 |
| Mutating (`POST`/`PUT`/`PATCH`/`DELETE`) | 20 req/s | 40 |

- Buckets are keyed on the client IP; each IP is independent.
- Stale entries are evicted after 10 minutes of inactivity, swept every 5
  minutes (`EntryTTL`, `CleanupInterval`).
- Exceeding a bucket returns `429` with a `Retry-After` header.
- `/health`, `/api/health` and `/api/client-errors` are exempt from this
  limiter (`ratelimit.go:149`).

`POST /api/client-errors` has its own limiter instead: 10 requests per minute
per IP with a burst of 10, 5-minute cleanup, 10-minute entry TTL
(`internal/webui/handlermux/handlers.go:77`).

## Error codes

The `code` field of `dto.ErrorResponse` is populated by the issue handlers.
These are the values currently emitted anywhere in `internal/webui`
(`internal/webui/handlers/issues/`):

| Code | Meaning |
|------|---------|
| `ENCODE_ERROR` | Failed to encode the HTTP response |
| `INVALID_JSON` | Malformed request body |
| `INVALID_PARAMS` | Invalid query parameters |
| `MISSING_QUERY` | Required query parameter absent |
| `REQUEST_TOO_LARGE` | Request body exceeds 1 MB |

Endpoints outside the issue handlers return `error` without a `code`.

## Common HTTP status codes

| Code | Meaning |
|------|---------|
| `200` | Success |
| `201` | Resource created |
| `202` | Accepted — async job started (workspace creation) |
| `204` | No content (e.g. no task available for a fleet claim) |
| `400` | Invalid input, or empty `{ws}` path parameter |
| `401` | Authentication required or invalid |
| `403` | Cross-origin request rejected |
| `404` | Resource not found, unknown workspace, or unregistered route |
| `409` | Conflict (circular dependency, already claimed) |
| `413` | Request body too large |
| `429` | Rate limit exceeded |
| `503` | Service unavailable (daemon down, fleet not configured) |
| `504` | Gateway timeout |

## Fleet coordination reference

Fleet endpoints let remote workers register with a pre-shared API key, receive a
JWT, atomically claim tasks, report completion, and heartbeat. They are
registered whenever a fleet Redis store initializes — `config.FleetRedis != nil`
sets the registry (`internal/webui/app/server_app.go:243`) and a non-nil registry
mounts the module (`internal/webui/app/server_modules.go:106-110`). The fleet API
key is *not* part of that gate: with Redis up and no `LOOM_FLEET_API_KEY` the
routes exist and `POST .../fleet/register` answers `503` with `"fleet
authentication not configured"` (`internal/webui/fleet/handlers.go:62-68`). The
standard JSON `404` only happens when no fleet Redis is configured at all — and
note that `loom serve` starts an in-process miniredis when no external Redis
address is set (`internal/cli/serve/serve.go:207-211`), so in the common local
case the routes are mounted.

### Two-layer auth

1. **Registration** — `POST .../fleet/register` validates the
   `X-Fleet-API-Key` header with `subtle.ConstantTimeCompare`
   (`internal/webui/fleet/handlers.go:70-80`).
2. **Everything else** — registration returns an HMAC-SHA256 JWT
   (`internal/webui/fleet/jwt.go:24-41`) carrying `worker_id`, `repos`, `iat`
   and `exp`. Claim, done, and heartbeat validate it and inject `WorkerClaims`
   into the request context.

The JWT signing key lives in Redis and supports rotation with a grace period for
the previous version (`internal/webui/fleet/signing_key.go`). When no signing
key is configured, the JWT middleware is not applied.

### Redis keys

All keys use the `fleet:` prefix. A workspace-scoped store inserts the workspace
ID, giving `fleet:{wsID}:...` (`internal/webui/fleet/store.go:41-72`).

| Key pattern | TTL | Contents |
|-------------|-----|----------|
| `fleet:workers:{workerID}` | 2 hours | Worker registration |
| `fleet:tasks:claimed:{taskID}` | 5 minutes | Claimed-task ownership |
| `fleet:worker:claim:{workerID}` | 5 minutes | Cached claim response |
| `fleet:task:result:{taskID}` | 24 hours | Task completion result |
| `fleet:ratelimit:{ip}` | sliding window | Per-IP registration rate limit |

TTL sources: `store.go:28-34`, `result.go:14`.

### Timeouts

Per-request context deadlines:

| Endpoint | Timeout | Source |
|----------|---------|--------|
| Register | 30s | `handlers.go:138` |
| Claim | 5s | `handlers_claim.go:115` |
| Done | 5s, plus 2s for cleanup | `handlers_done.go:87,162` |
| Heartbeat | 2s | `handlers_heartbeat.go:87` |

`TimeoutEnforcer` runs a background sweep that releases claims for tasks that
overrun. Defaults: 30-minute task timeout, checked once a minute
(`internal/webui/fleet/timeout.go:105-110`).

### SSE and fleet counters

`GET /api/metrics` returns the SSE hub gauges plus fleet counters
(`internal/webui/handlers/health/health.go:65-75`). The `loom_fleet_*` fields
are `omitempty` on `int64`, so they are absent both when fleet coordination is
disabled **and** when every counter happens to be zero. `connected_clients`,
`retry_queue_depth` and `uptime_seconds` are gauges; the rest are monotonic
counters.

### `fleetDone` bypasses the user JWT, not authentication

`POST /api/workspaces/{ws}/fleet/done/{id}` skips the *user* JWT middleware —
which is what `isPublicRoute` means by public — but it is not unauthenticated.
`internal/webui/fleet/module.go:64-65` wraps it in exactly the same fleet-JWT
middleware as claim and heartbeat whenever a signing key is configured
(`module.go:54-57`). The spec matches: `api/openapi.yaml:5836-5837` declares
`FleetJWT` for `fleetDone`, so the endpoint reference above renders it as
**Auth:** `FleetJWT` rather than as a public route.

## Endpoints removed from the terminal surface

The tmux-era terminal session lifecycle endpoints (`spawn`, `restart`, `kill`,
`session-status`, `sessions`, `sessions/{name}/seed`, `sessions/close-all`,
`sessions/{session}/scrollback`, `/export`, `/scrollback-info`) were deleted
during the terminal simplification — see the module comment at
`internal/webui/handlers/terminal/module.go:75` ("the surviving terminal
routes") and `internal/webui/frontend/src/api/terminal/terminal.ts:1-8`. What
remains is WebSocket auth, tab metadata, terminal UI state, and the
cross-workspace sessions-by-issue lookup.

They were dropped from `api/openapi.yaml` in the same change, so they no longer
appear in the endpoint reference above or in the generated TypeScript types
(`internal/webui/frontend/src/types/generated/openapi.ts`); the spec now
declares only the seven surviving `/terminal/*` paths. The **Documented but not
registered** table below is the authoritative check that spec and server agree.

`POST /api/csp-report`, documented in the pre-generator version of this file, no
longer exists anywhere in the Go source.
