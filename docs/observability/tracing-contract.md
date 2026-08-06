# Trace contract — loom + fleet-db

Canonical conventions for OpenTelemetry tracing across loomcli and fleet-db.
Every new instrumented surface MUST conform.

Status: source-of-truth. Mirror in `fleet-db/docs/observability/tracing-contract.md`
points here.

## 1. Services

Standard `service.name` values. One per process boundary.

| `service.name` | Process | Notes |
|---|---|---|
| `loom-cli` | One-shot CLI invocations (`loom plan`, `loom workspace list`, …) | Off by default; opt in via `LOOM_TRACE=1` |
| `loom-serve` | `loom serve` web/API server | On when collector configured |
| `loom-daemon` | `loom daemon` supervisor | On when daemon profile enables OTel |
| `fleet-db` | fleet-db API server | On when collector configured |
| `loom-agent` | Per-agent worker process (`loom plan --auto`, `loom task --auto`) | Inherits parent context from daemon; emits `loom.task` / `loom.agent.lifecycle` |

The existing `otelexport` default `loomcli` is being **renamed** to one of the
above based on caller.

## 2. Resource attributes

Every span carries these on its emitting process. Set once at TracerProvider init.

| Key | Source | Example |
|---|---|---|
| `service.name` | hardcoded per binary, see §1 | `fleet-db` |
| `service.version` | git SHA via existing `-X main.commit` ldflag | `d76c559e` |
| `service.namespace` | static | `loom` |
| `deployment.environment` | env `LOOM_ENV` (`dev`, `staging`, `prod`) | `prod` |
| `host.name` | semconv host detector | `worker-3.example.com` |

Use `semconv "go.opentelemetry.io/otel/semconv/v1.26.0"` (matches existing
otelexport pin). Bump in lockstep across both repos.

## 3. Span naming

Low cardinality. Span names go in dashboards — they MUST NOT contain IDs,
workspace keys, or any unbounded value.

| Span kind | Name format | Example |
|---|---|---|
| HTTP server | `<METHOD> <route-template>` | `GET /api/v1/{workspace}/issues/{id}` |
| HTTP client | `<METHOD>` only; route on attribute | `POST` |
| Service/coordinator method | `<package>.<Type>.<Method>` with a fixed package name | `workspacecoord.WorkspaceService.List` |
| Storage method | `<backend>.<Method>` | `redis.CreateIssue`, `postgres.GetIssue` |
| Redis command | `redis.<CMD>` | `redis.HGETALL`, `redis.PIPELINE` |
| Postgres query | `pgx.<op>` | `pgx.query`, `pgx.exec` |
| CLI command | `loom.cli.<verb>.<noun>` | `loom.cli.workspace.list` |
| Event-derived | `loom.task`, `loom.agent.lifecycle` | (existing — preserved) |
| Git subprocess | `git.<subcommand>` | `git.push`, `git.pull`, `git.rebase`, `git.rev-parse` |
| WebSocket lifetime | `ws.<phase>` (`upgrade`, `disconnect`) | `ws.upgrade`, `ws.disconnect` |
| SSE lifetime | `sse.<phase>` (`handshake`, `disconnect`) | `sse.handshake`, `sse.disconnect` |

HTTP server route templates MUST come from the same route-capture used by the
existing Prometheus middleware so traces and metrics share labels.

**Long-lived connection rule (WS/SSE).** Connection-level spans cover ONLY
the upgrade handshake and the disconnect teardown — never the lifetime of
the relay. A 30-minute terminal WebSocket emitting a 30-minute span is
useless in Jaeger and inflates retention. Per-message spans flood the
collector and put PII at risk. The two short spans bracket the connection
so duration is recoverable from the gap between them. The route lives on
the parent otelhttp span (`http.route`), not the lifetime span name. The
disconnect reason goes on the `disconnect.reason` attribute (bounded enum:
`client_close`, `server_close`, `session_killed`, `backend_exited`,
`error`). See `internal/webui/server/realtime/handler.go` and
`internal/webui/handlers/terminal/{ws,agent}.go`.

## 4. Span attributes

### 4.1 Loom domain (preserved from `internal/events/otelexport`)

| Key | Type | Notes |
|---|---|---|
| `loom.agent` | string | Agent name (e.g. `falcon`). Already in use — DO NOT rename. |
| `loom.role` | string | Role name (e.g. `reviewer`). |
| `loom.epic_id` | string | Epic issue ID. |
| `loom.task_id` | string | Task issue ID. |
| `loom.error_type` | string | One of: `timeout`, `oom`, `permission`, `network`, `crash`, `unknown`. |
| `loom.pid` | int | OS PID for agent processes. |
| `loom.exit_code` | int | Exit code of an agent process. |

### 4.2 Loom domain (new)

| Key | Type | Notes |
|---|---|---|
| `loom.workspace` | string | Workspace key (e.g. `ACME`). Use this — NOT `workspace.id`, NOT `ws`. |
| `loom.repo` | string | Repo name within workspace. |
| `loom.session_id` | string | Terminal/session id for `loom serve`. |
| `loom.backend` | string | AI backend (`claude`, `codex`, `opencode`). |
| `loom.actor` | string | Authenticated identity (X-Actor or JWT subject). |
| `loom.command` | string | CLI verb on root span only (`workspace.list`, `plan`, `task`). |
| `git.command` | string | Git subcommand verb (e.g. `push`, `pull`, `fetch`). Bounded set. |
| `git.arg_count` | int | Number of args passed to git. Numeric, not the args themselves. |
| `git.exit_code` | int | Process exit code. `0` on success; `-1` if no exit code (signal/spawn failure). |
| `git.duration_ms` | int | Wall-clock duration of the git invocation, in milliseconds. |
| `disconnect.reason` | string | Bounded enum on `ws.disconnect` / `sse.disconnect`: `client_close`, `server_close`, `session_killed`, `backend_exited`, `error`. |
| `network.peer.address` | string | semconv-style peer address from `r.RemoteAddr` on connection upgrade. |

### 4.3 OTel semconv (use as-is, do not re-prefix)

| Key | When | Source |
|---|---|---|
| `http.request.method`, `http.response.status_code`, `http.route`, `url.path`, `server.address` | HTTP server/client | semconv |
| `db.system` (`redis` / `postgresql`), `db.statement` (truncated, see §6), `db.operation`, `db.namespace` | Storage | semconv |
| `code.function`, `code.namespace`, `code.filepath`, `code.lineno` | Optional, only on errors | semconv |
| `error.type`, `exception.type`, `exception.message` | On Span.RecordError | semconv |

### 4.4 Result/cardinality attributes (encouraged)

| Key | Type | Notes |
|---|---|---|
| `result.count` | int | Items returned by a list endpoint. |
| `result.bytes` | int | Response payload size. |
| `cache.hit` | bool | When applicable. |
| `pipeline.size` | int | Redis pipeline commands count. |

## 5. Propagation

- **Wire format**: W3C `traceparent` + `tracestate` (default OTel propagator
  composition). Set globally:
  ```go
  otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
      propagation.TraceContext{},
      propagation.Baggage{},
  ))
  ```
- **Baggage** carries `loom.workspace`, `loom.actor`, `loom.command` only.
  NEVER put PII or secrets in baggage — it propagates unencrypted.
- **Subprocess**: when loom auto-spawns embedded fleet-db or an agent
  worker, serialize the active `traceparent` into env var
  `LOOM_TRACE_PARENT` (kept distinct from `TRACEPARENT` to avoid clashing
  with other tools). The spawned process reads it once at boot as the
  bootstrap span parent.
- **Frontend → loom serve**: `client.ts` generates a fresh `traceparent`
  per request (random 16-byte trace id, random 8-byte span id, sampled
  flag from a feature flag).

## 6. PII / redaction policy

A span attribute MUST be one of:

1. **Allowlisted** in §4 above, OR
2. A semconv key from §4.3, OR
3. A boolean / numeric / enum from a fixed set.

Free-form user-supplied strings are forbidden as attributes unless redacted.
This rules out, by name:

| Forbidden as attribute | Why |
|---|---|
| `issue.title`, `comment.body`, `issue.description` | User-supplied, may contain secrets |
| `prompt`, `prompt_template`, `prompt_file` | May contain credentials |
| `git.diff`, `git.args` (raw), `command.args` (raw) | High cardinality + PII risk (branch names, URLs, refspecs) |
| Auth tokens, JWTs, API keys | Obvious |
| Email addresses, full names | PII |

For HTTP/SQL/Redis statement attributes (`db.statement`, `url.full`):

- Truncate to **256 bytes** (semconv guidance is 1024; we go tighter).
- Strip query string `?...` from `url.full`; keep route template only.
- For Redis, record `db.statement = "<CMD> <key-glob>"` where key-glob
  replaces variable parts (e.g. `HGETALL workspace:*` not
  `HGETALL workspace:ACME`).

A unit test (Phase 13) asserts no forbidden keys appear in any emitted
span across the test suite.

## 7. Span status & errors

- Default status is **unset** (do NOT set OK explicitly — semconv guidance).
- On error: `span.RecordError(err)` then `span.SetStatus(codes.Error, "<short reason>")`.
- The status description MUST be a low-cardinality category, not the raw
  error message. Reuse the existing `categorizeError` helper in
  `otelexport/exporter.go` (`timeout`, `oom`, `permission`, `network`,
  `crash`, `unknown`).
- Cancellation (`context.Canceled`) is NOT an error — leave status unset.

## 8. Sampling

| Environment | Sampler | Default rate |
|---|---|---|
| `LOOM_ENV=dev` (or unset) on `loom-serve` / `fleet-db` | `AlwaysOn` | 100% |
| `LOOM_ENV=prod` on `loom-serve` / `fleet-db` | `ParentBased(TraceIDRatio)` | 5% |
| `loom-cli` | `AlwaysOff` unless `LOOM_TRACE=1` | 0% / 100% |
| `loom-daemon`, `loom-agent` | `ParentBased(AlwaysOn)` | follows parent |

Override per-process with `OTEL_TRACES_SAMPLER` + `OTEL_TRACES_SAMPLER_ARG`
(standard env vars).

## 9. Disabled-mode semantics

If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset OR `daemon.otel.enabled=false`:

- TracerProvider init returns a no-op provider.
- `otel.SetTracerProvider` is still called (no-op TP) so library code
  that does `otel.GetTracerProvider().Tracer(...)` works without panics.
- Propagator is still installed so inbound `traceparent` is parsed and
  re-emitted (cheap; lets upstream traces stay connected even when this
  hop doesn't sample).
- Zero overhead beyond the propagator. No batcher, no exporter, no
  background goroutines.

## 10. Versioning this contract

When adding a new attribute key or span name, update this file in the
same PR. Reviewers reject instrumentation PRs that introduce keys not
listed here. The Phase 13 cardinality + redaction tests act as a
backstop, but the human review on this doc is the primary gate.
