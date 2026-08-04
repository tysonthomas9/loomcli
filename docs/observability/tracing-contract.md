# Trace contract — loom + fleet-db

Canonical conventions for OpenTelemetry tracing across loomcli and fleet-db.
Every new instrumented surface MUST conform.

> **Status:** Current · *audited 2026-07-23*

Status: source-of-truth for span names and attribute keys. Mirror in
`fleet-db/docs/observability/tracing-contract.md` points here. §8 (Sampling)
is the one section that is **not** implemented — it is marked inline.

## 1. Services

Standard `service.name` values. One per process boundary.

| `service.name` | Process | Notes |
|---|---|---|
| `loom-cli` | One-shot CLI invocations (`loom plan`, `loom workspace list`, …) | Off by default; opt in via `LOOM_TRACE=1` |
| `loom-serve` | `loom serve` web/API server | On when collector configured |
| `loom-daemon` | `loom daemon` supervisor | On when daemon profile enables OTel |
| `fleet-db` | fleet-db API server | On when collector configured |
| `loom-agent` | Per-agent worker process (`loom plan --auto`, `loom task --auto`) | Inherits parent context from daemon; emits `loom.task` / `loom.agent.lifecycle` |

**Not yet done:** `otelexport` still defaults its service name to `loomcli`
(`internal/events/otelexport/config.go:36`) and hardcodes `loomcli` as the
tracer/meter instrumentation name (`exporter.go:143,145,151,153`). Only the
agent path overrides it (`internal/cli/agent_event_bus.go:69`
`ServiceName: "loom-agent"`). Renaming it per caller is still the intent; it
has been outstanding since 2026-05-07.

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
| Service method | `<package>.<Type>.<Method>` | `service.IssueService.Claim` |
| Storage method | `<backend>.<Method>` | `redis.CreateIssue`, `postgres.GetIssue` |
| Redis command | `redis.<CMD>` | `redis.HGETALL`, `redis.PIPELINE` |
| Postgres query | `pgx.<op>` | `pgx.query`, `pgx.exec` — **UNVERIFIED**: [tracing.md](./tracing.md) says `pg.query`. Both describe the fleet-db repo, which is not in this tree; one of the two is wrong and must be settled against fleet-db. |
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
  flag hardcoded to `01` —
  `internal/webui/frontend/src/api/common/client.ts:57-61`). That hardcoded
  flag is load-bearing: every sampler in the tree is `ParentBased` with
  default options (`internal/observability/tracing/tracing.go:211,220`,
  `internal/events/otelexport/exporter.go:116`), so a remote parent marked
  sampled resolves to `AlwaysSample`. Any request originating in the browser
  is therefore sampled regardless of the configured ratio.

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

**Enforcement status.** Span *name* cardinality is tested:
`internal/observability/tracing/cardinality_test.go:191` asserts every known
span name matches the allowlist, and `:368` is an in-memory exporter smoke
test. The forbidden-**attribute** list above has **no test** — a repo-wide
grep for these keys in any `*_test.go` finds no assertion. It is enforced by
review only.

## 7. Span status & errors

- Default status is **unset** (do NOT set OK explicitly — semconv guidance).
- On error: `span.RecordError(err)` then `span.SetStatus(codes.Error, "<short reason>")`.
- The status description MUST be a low-cardinality category, not the raw
  error message. Reuse the existing `categorizeError` helper in
  `otelexport/exporter.go` (`timeout`, `oom`, `permission`, `network`,
  `crash`, `unknown`).
- Cancellation (`context.Canceled`) is NOT an error — leave status unset.

## 8. Sampling

### What ships today

There are **two** span pipelines on the loomcli side and they do not share a
sampler. Always say which one you mean.

**1. The global provider** (`observability/tracing.Init`), used by every
hand-written span. `internal/cli/root.go:243` passes `AlwaysOn: true` for all
four service names, and `buildSampler` returns `ParentBased(AlwaysSample())`
whenever `AlwaysOn` is set (`internal/observability/tracing/tracing.go:209-211`).

**2. `otelexport`'s own provider**, used for the event-derived spans
(`loom.task`, `loom.agent.lifecycle`) when the caller does *not* inject a
provider. `internal/events/otelexport/exporter.go:113-116` builds its own
`sdktrace.TracerProvider` with
`ParentBased(TraceIDRatioBased(cfg.SampleRate))`.

| Process | Pipeline | Sampler | Rate |
|---|---|---|---|
| `loom-cli`, `loom-serve`, `loom-daemon`, `loom-agent` | global | `ParentBased(AlwaysSample)` | 100% when enabled |
| `loom-agent` event spans | otelexport, provider **injected** (`internal/cli/agent_event_bus.go:71` passes `WithTracerProvider`) | inherits the global sampler | 100% when enabled |
| `loom-daemon` event spans | otelexport, provider **not** injected (`internal/cli/daemon/daemon_run_helpers.go:58`) | `ParentBased(TraceIDRatioBased)` | `daemon.otel.sample_rate`, defaulted to 1.0 by `otelexport.Config.Resolved` (`internal/events/otelexport/config.go:38-40`) |

So `daemon.otel.sample_rate` is the one sampling knob that actually works
today, and it only affects daemon event spans.

`loom-cli` is not `AlwaysOff` — the global provider is simply never initialized
unless an endpoint exists. `Config.Enabled()` is false with an empty endpoint
and `Init` installs a no-op provider (`tracing.go:95,121-126`); `LOOM_TRACE=1`
supplies a default endpoint (`root.go:228-232`) and turns it on.

`OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` are **not honored**:
`tracing.go:198` always appends an explicit `WithSampler(...)`, which overrides
the SDK's env-derived default.

### Not yet implemented

The design below is the intent; **no code reads `LOOM_ENV` to pick a sampler**
(`buildSampler`, `tracing.go:209-220`, ignores `cfg.Environment`), and there is
no 5% ratio path. Do not read this table as behavior.

| Environment | Sampler | Default rate |
|---|---|---|
| `LOOM_ENV=dev` (or unset) on `loom-serve` / `fleet-db` | `AlwaysOn` | 100% |
| `LOOM_ENV=prod` on `loom-serve` / `fleet-db` | `ParentBased(TraceIDRatio)` | 5% |
| `loom-cli` | off unless `LOOM_TRACE=1` | 0% / 100% |
| `loom-daemon`, `loom-agent` | `ParentBased(AlwaysOn)` | follows parent |

Honoring `OTEL_TRACES_SAMPLER` would require `Init` to skip `WithSampler` when
the env var is present. `tracing.Config.SampleRate` already exists and feeds
`ParentBased(TraceIDRatioBased(rate))` (`tracing.go:213-220`), but nothing in
this tree sets it — `root.go:243` always sets `AlwaysOn`, which overrides it.
(Do not confuse it with the identically named `otelexport.Config.SampleRate`,
which *is* wired, from `daemon.otel.sample_rate` — see "What ships today".)

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
listed here. The span-name cardinality test
(`internal/observability/tracing/cardinality_test.go:191`) is a partial
backstop — it covers names, not attributes — so human review on this doc is
the primary gate. See §6 for what is and isn't tested.

## Related

- [README.md](./README.md) — index and precedence for these three docs
- [tracing.md](./tracing.md) — operational how-to and the current coverage
  matrix
- [events-tracing-spike.md](./events-tracing-spike.md) — closed decision record
  for event-span parenting
- `fleet-db/docs/observability/tracing-contract.md` — the fleet-db mirror
  (separate repo)
