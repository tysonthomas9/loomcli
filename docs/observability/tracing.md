# Tracing — local usage and ops

OpenTelemetry tracing is wired across loom and fleet-db. This doc covers how
to turn it on, where to look at the result, and what's tracked vs deferred.

For the canonical attribute keys, span naming rules, and sampling defaults,
see [tracing-contract.md](./tracing-contract.md).

> Refreshed 2026-05-07 to reflect post-Tier-1+2+4 audit (browser propagation,
> service-layer spans, otelpgx, projector + compactor + RPC spans, backend
> invocation sub-spans, daemon→agent traceparent inheritance).

## Run it locally in 60 seconds

```bash
# 1. Start Jaeger (UI on :16686, OTLP HTTP on :4318) and a Redis for fleet-db.
podman run -d --name otel-jaeger \
  -e COLLECTOR_OTLP_ENABLED=true \
  -p 16686:16686 -p 4318:4318 -p 4317:4317 \
  docker.io/jaegertracing/all-in-one:latest

podman run -d --name otel-redis -p 16379:6379 docker.io/library/redis:7-alpine

# 2. Build and run fleet-db pointed at both.
( cd ../fleet-db && go build -o /tmp/fleet-db ./cmd/fleet-db )
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 LOOM_ENV=dev \
  /tmp/fleet-db --redis-addr localhost:16379 --addr :18080 \
  --auth-dev-mode --authz-enabled=false &

# 3. Build loom and exercise it.
go build -o /tmp/loom ./cmd/loom
LOOM_FLEET_DB_URL=http://localhost:18080 \
  LOOM_FLEET_DB_ACTOR=tyson LOOM_WORKSPACE=DEMO \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 LOOM_TRACE=1 \
  /tmp/loom workspace list

# 4. Open the trace.
open http://localhost:16686
```

The newest trace under "loom-cli" service shows the full tree:
`loom.cli.workspace → HTTP GET → fleet-db handler → redis.<CMDS>`.

## Env knobs

| Variable | Effect |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP HTTP receiver. Empty = export disabled (propagator still installed). |
| `LOOM_TRACE=1` | Force-enable tracing on the CLI even when no endpoint is set (defaults endpoint to `localhost:4318`). |
| `LOOM_ENV` | `dev` / `staging` / `prod`. Sets `deployment.environment` resource attr and the default sampler (always-on in dev, ratio in prod). |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | Standard SDK overrides. |
| `LOOM_TRACE_PARENT` | Inherited W3C traceparent for subprocesses. Set automatically by loom when spawning embedded fleet-db. Don't set manually unless you know why. |

## What's traced today

| Layer | Component | Mechanism | Verification |
|---|---|---|---|
| Browser | `loom serve` UI fetch | `traceparent` injected by openapi-fetch middleware + legacy fetch helpers in `internal/webui/frontend/src/api/common/client.ts` | Server-side span has parent in DevTools Network → request headers |
| Process root | `loom-cli` / `loom-agent` / `loom-serve` / `loom-daemon` | service-name routed off `os.Args[1]`; root span via `tracing.Init` + `BootstrapContext` | Top-level span in Jaeger under matching service |
| Process root | `loom-cli` | flush on SIGINT/SIGTERM and `cli.ExitWithFlush` | Killed CLI still emits spans |
| Subprocess | loom → embedded fleet-db | `LOOM_TRACE_PARENT` env var injected on spawn | Single trace covers parent + child |
| Subprocess | daemon → agent | `LOOM_TRACE_PARENT` set in `internal/cli/daemon/supervisor/spawn.go`; consumed via `tracing.BootstrapContext` | Agent root span chains to daemon span |
| HTTP client | loom-cli/agent/serve/daemon → fleet-db | `otelhttp.NewTransport` on `SharedHTTPClient` + `fleetdb.Client` | Client + server spans share trace ID |
| HTTP server | fleet-db | `otelhttp` middleware + route capture in `internal/api/tracing.go` | `http.route` attr matches mux template, not raw URL |
| Service layer | fleet-db | manual spans on 8 hot read methods: `Workspace.Get/List`, `Repo.List`, `Agent.Get/List`, `Role.Get/List`, `Issue.Get/Search/List` | `service.<Type>.<Method>` spans between HTTP and storage |
| RPC layer | fleet-db | per-method spans on JSON-RPC dispatch | `rpc.<method>` spans under server handler |
| Projector | fleet-db | per-event handler spans `service.Projector.<event.action>` from `internal/projection/tracing.go` | Spans emitted as events drain |
| Compactor / archive / retention | fleet-db | per-cycle spans | Spans visible during scheduled cycles |
| Redis | fleet-db | `resilience.OTelHook` in `internal/resilience/otel_hook.go` (per-attempt, PII-redacted statements, retry storms visible) | `redis.<CMD>` children of service spans |
| Postgres | fleet-db pgxpool | `otelpgx` chained via `MultiQueryTracer` with `SlowQueryTracer` in `internal/storage/postgres/multi_tracer.go` | `pg.query` spans with redacted SQL |
| Backend invocation | loom-agent | `tracedAgentInvoker` decorator emits `loom.backend.<name>.invoke_interactive` / `invoke_non_interactive` | Sub-spans under agent root for each backend call |
| Event-derived spans | `AgentEventBus` singleton | `otelexport` subscriber → `loom.task` / `loom.agent.lifecycle` spans | Spans appear when bus emits |
| Logs | loomcli + fleet-db | slog handler injects `trace_id` / `span_id` | Log lines contain both fields when in-span |
| Sampling / SDK | all services | sync processor for CLI/agent, batch for serve/daemon; no-op TracerProvider when disabled | "tracing enabled" log absent in disabled mode |

### Known gaps (intentionally not instrumented yet)

| Gap | Reason |
|---|---|
| Archive Postgres backend (`database/sql`, not pgxpool) | `otelpgx` doesn't apply; would need `otelsql` migration. |
| WebSocket / SSE handlers (`internal/webui/log/streamer.go`, `internal/webui/server/realtime/handler.go`) | Long-lived connections; no span model for streamed frames yet. |
| Daemon supervisor goroutines | Lifecycle decisions infrequent; log search is sufficient. |
| Recovery service (`fleet-db/internal/recovery/`) | Admin-only, manual operation. |
| RPC trace-context propagation | JSON-RPC envelope doesn't carry `traceparent`; RPC spans are unrooted on the producer side. |

## Verifying traces from a script

```bash
python3 <<'EOF'
import json, subprocess
raw = subprocess.check_output(
    ["curl", "-s", "http://localhost:16686/api/traces?service=loom-cli&limit=1"]
).decode()
t = json.loads(raw)["data"][0]
print(f"Trace {t['traceID']}: {len(t['spans'])} spans")
for s in t["spans"]:
    proc = t["processes"][s["processID"]]["serviceName"]
    print(f"  [{proc}] {s['operationName']}")
EOF
```

A working trace includes spans from BOTH `loom-cli` and `fleet-db`. If only
one service appears, the propagator isn't installed on the missing side
(check `tracing.Init` was called and `OTEL_EXPORTER_OTLP_ENDPOINT` is set).

## Disabling tracing

Don't set `OTEL_EXPORTER_OTLP_ENDPOINT` and don't set `LOOM_TRACE`. The
TracerProvider becomes a no-op, the OTLP exporter is never constructed, and
overhead is one map lookup per request (the propagator is still installed
so inbound `traceparent` re-emits). Verify with:

```bash
unset OTEL_EXPORTER_OTLP_ENDPOINT LOOM_TRACE
loom workspace list
# logs do NOT contain "tracing enabled"; no trace appears in Jaeger.
```

## Cleanup

```bash
podman rm -f otel-jaeger otel-redis
```

## Known caveats

- **CLI trace is opt-in.** Short-lived `loom <cmd>` runs are off by default to
  keep CI logs clean. Set `LOOM_TRACE=1`.
- **Event spans land in their own traces.** `loom.task` and
  `loom.agent.lifecycle` will fragment until Phase 9 lands the events
  schema change. See [events-tracing-spike.md](./events-tracing-spike.md).
- **`loom workspace list` makes 2 fleet-db round-trips per workspace.** Visible
  in the trace tree. Real N+1 — flagged in Phase 6 backlog.
- **RPC spans are unrooted on the producer.** JSON-RPC envelope has no
  `traceparent`; server-side `rpc.<method>` spans start a fresh trace.
- **Service-layer coverage is partial.** Hot reads on Workspace, Repo, Agent,
  Role, and Issue have spans; writes and remaining services still skip from
  `http.server` to storage.
