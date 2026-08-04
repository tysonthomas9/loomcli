## About this document

This is the reference for the **Loom WebUI HTTP API** — the server started by
`loom serve` (`internal/cli/serve/serve.go`), which the web UI, the CLI, and
remote workers all talk to. "Loom" here is the agent-orchestration platform, not
any similarly named product; see `docs/loom-glossary.md` for the other
overloaded words used below (workspace, agent, lead, fleet, flue, driver).

The endpoint reference and schema tables are generated from `api/openapi.yaml`.
The sections in this preamble and in the appendix are hand-written and are the
only parts of this file you may edit — they live in `docs/api.preamble.md` and
`docs/api.appendix.md`.

`api/openapi.yaml` is not a complete description of what the server registers.
The **Spec Coverage vs Registered Routes** appendix at the end of this document
is regenerated on every run and lists the exact gaps in both directions. Read it
before trusting the absence of an endpoint here as evidence it does not exist.

## Overview

- **Base URL:** `http://127.0.0.1:8080`. Port and bind address come from
  `--port`/`LOOM_SERVER_PORT` and `--bind`/`LOOM_BIND_ADDR`
  (`internal/cli/serve/serve.go:151-164`).
- **Content-Type:** `application/json` for request and response bodies, except
  the WebSocket and Server-Sent Events endpoints and `GET /metrics`
  (Prometheus text format).
- **Request body limit:** 1 MB — `handler.MaxRequestBody`
  (`internal/webui/server/handler/request.go:15`), applied via
  `http.MaxBytesReader`. `POST /api/client-errors` uses its own smaller 16 KB
  limit (`internal/webui/handlers/misc/client_errors.go:34`).
- **Unregistered `/api/` paths** return `404` with the JSON body
  `{"error":"not found"}` (`internal/webui/app/routes.go:32-36`). Non-`/api`
  paths reach the SPA handler only when the server was started with a frontend
  directory; `registerFrontendRoutes` returns without registering `/` when
  `FrontendDir` is empty (`internal/webui/app/frontend.go:11-16`), and the
  frontend is normally served externally, so those paths get Go's default text
  `404`.

## Authentication

This document owns **per-endpoint** auth requirements. Auth-mode *policy* —
which modes exist, when each applies, credential storage, and the SSRF and
subprocess-env analysis — is owned by [`security.md`](security.md). When the two
disagree about policy, `security.md` wins.

### Bearer token

Protected endpoints require a bearer JWT:

```
Authorization: Bearer <token>
```

`extractBearerToken` reads the `Authorization` header **only**
(`internal/webui/server/middleware/auth.go:181-194`); there is no `?token=`
fallback on the generic auth middleware. Browser transports that cannot set
headers use a dedicated token exchange instead:

- **SSE:** `GET /api/workspaces/{ws}/events/token` mints a short-lived token the
  `EventSource` connection presents (`internal/webui/subscription/module.go:55`).
- **Terminal / agent-terminal WebSockets:** `GET .../terminal/token` mints a
  one-time HMAC token passed as `?token=` on the upgrade request (see
  *One-time terminal tokens* below).

### Public routes

`isPublicRoute` (`internal/webui/server/middleware/auth_routes.go:11`) decides
what bypasses the JWT middleware. Workspace-scoped paths are normalised by
stripping `/api/workspaces/{ws}` first, so the rules below apply to both the
global and the workspace-scoped form.

Public regardless of method:

- Any path under `/api/fleet/`, `/api/internal/workers/`, `/api/webhooks/`,
  `/api/driver/`, `/api/task-run/`, `/api/auth/` — these authenticate inside
  their own handlers (fleet API key / fleet JWT, `LOOM_WORKER_TOKEN`, per-binding
  webhook signature, `X-Loom-Driver-*` run credentials, task-run lease token,
  and the proxied BetterAuth service respectively).
- `POST /api/client-errors` — must work before auth bootstrap completes.
- `POST` to the session-notify path (`sessions.NotifyPath`) — uses its own
  mechanism.

Public for `GET` only:

- `/health`, `/api/health`, `/metrics`
- `/api/config` — auth discovery, needed before a token exists
- `/api/events` (i.e. `/api/workspaces/{ws}/events`) — SSE token exchange
- `/api/terminal/ws` (i.e. `/api/workspaces/{ws}/terminal/ws`) — one-time token
- `/api/agents/{name}/terminal/ws` — one-time token
- Any non-`/api/` path — frontend static files and SPA routes

### One-time terminal tokens

`realtime.TerminalAuth` (`internal/webui/server/realtime/terminal_auth.go`)
issues the tokens used by the terminal and agent-terminal WebSockets.

- Format: `base64url(JSON{session, workspace, uid, exp, nonce}) + "." +
  base64url(HMAC-SHA256)`.
- Lifetime: 60 seconds (`TerminalTokenExpiry`), single use — the nonce is
  recorded in memory and replays are rejected.
- The HMAC secret is 32 random bytes generated per process, so every server
  restart invalidates all outstanding tokens. This is deliberate.
- Agent-terminal tokens are scoped `agent:<name>:logs`
  (`internal/webui/handlers/terminal/agent.go:29`), which stops a token minted
  for one agent from being replayed against another or against the main
  terminal.

## Response envelopes

There is no single envelope. The three shapes in use are:

1. **Error envelope** — `dto.ErrorResponse`
   (`internal/webui/server/dto/common.go:5`):
   `{"success": false, "error": "...", "code": "...", "details": {...}}`.
   `code` and `details` are `omitempty`.
2. **List envelope** — `dto.ListResponse[T]`:
   `{"success": true, "data": [...], "total": N}`. `data` is always an array,
   never `null`.
3. **Bare JSON** — some handlers write the payload directly with no `success`
   wrapper, and middleware-level failures use `{"error": "..."}` with no
   `success` field (`internal/webui/app/respond.go:20`).

The generated per-endpoint tables below record what `api/openapi.yaml` declares
for each operation; where the spec says only `type: object`, the shape is not
pinned down and the handler is the authority.

## Workspace scoping

Most endpoints live under `/api/workspaces/{ws}/...` and are served by a nested
mux mounted behind workspace middleware
(`internal/webui/app/routes.go:140-199`). `middleware.WorkspaceResolved`
(`internal/webui/server/middleware/workspace.go:76`):

1. reads `{ws}` via `r.PathValue("ws")` and trims whitespace,
2. returns `400 {"error": "workspace ID is required"}` when it is empty,
3. resolves it to a canonical workspace ID, returning
   `404 {"error": "workspace not found: {ws}"}` when resolution fails,
4. injects the resolved `WorkspaceRef` into the request context, where handlers
   read it with `middleware.WorkspaceFromContext`.

So every workspace-scoped operation below can also return `400` or `404` from
the middleware, before its own handler runs. Authentication (`401`) is enforced
earlier still, by the server-level auth middleware.

Two deliberate exceptions to the mount:

- `GET /api/workspaces/{ws}/readyz` bypasses workspace middleware so it can
  report readiness for a workspace that is not registered yet
  (`internal/webui/app/routes.go:188-197`).
- `PATCH` routes are registered on the outer mux rather than the nested one,
  working around a `net/http` bug where reading the body of a `PATCH` routed
  through a nested mux via a wildcard subtree pattern hangs
  (`internal/webui/app/routes.go:151-153`).

## Shared validation

- **Agent names** must match `^[a-zA-Z0-9_-]+$`
  (`internal/webui/handlers/terminal/agent.go:25`,
  `internal/webui/handlers/misc/logs.go:26`). Git handlers reject a missing name
  with `400 "missing agent name"` and a malformed one with
  `400 "invalid agent name"`; an unknown worktree is
  `404 agent worktree "<name>" not found`
  (`internal/webui/handlers/git/diff_service.go:32-42`).
- **Git refs and branches** must match `^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`
  (`internal/webui/handlers/git/git.go:25`,
  `internal/webui/svcimpl/validators.go:18`). The leading-character rule rejects
  refs starting with `-` (argument injection) and the character class excludes
  everything needed for `..` traversal.

## Conditional registration

A route existing in the source does not mean it is mounted in every process.
Handler modules skip registration when their dependency is nil — for example
`internal/webui/handlers/terminal/module.go:76-98` registers the agent-terminal
routes only when the agent service, tmux manager, or terminal auth is
configured, and `internal/webui/app/routes.go:23-25` registers the whole
workspace subtree only when a multi-workspace pool exists. A request to an
unmounted `/api/` route gets the JSON `404` described above, not a `501`.
