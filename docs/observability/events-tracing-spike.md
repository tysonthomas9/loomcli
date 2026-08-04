# Spike: connecting agent event spans to originating requests

> **Status:** Decided and shipped (`fdb709d61`, 2026-05-07) · *audited 2026-07-23*
>
> Historical decision record. **Do not implement from the outline below** — it
> describes the plan, not what was built. Option A shipped with two deviations:
>
> - `Bus.Emit(e Event)` kept its signature (`internal/events/emitter.go:46`).
>   A separate `Bus.EmitCtx(ctx, e)` was added
>   (`internal/events/trace_context.go:89`) plus a process-wide ambient context
>   provider, `events.SetContextProvider` (`trace_context.go:23-31`), installed
>   by the CLI at `internal/cli/root.go:192` pointing at `cmdstore.RootContext`.
>   Emit sites were therefore **not** all audited — the ambient provider covers
>   them.
> - Baggage serialization (open question, below) was **rejected**, deliberately:
>   `internal/events/trace_context.go:47-51` records that only TraceContext
>   propagator output is captured, "so we don't accidentally bake
>   high-cardinality values into the JSONL log". Baggage crosses process
>   boundaries via env vars instead.
>
> Current behavior lives in [tracing.md](./tracing.md); the normative contract
> is [tracing-contract.md](./tracing-contract.md).

## Status
Decided; implemented in Phase 9. This document captures three options, scores
them, and records why Option A won. Kept for the rationale — the trade-offs
against Options B and C are the expensive part to reconstruct.

## The problem

`internal/events/otelexport` already emits two span types:

- `loom.task` (one per task claim/complete/fail)
- `loom.agent.lifecycle` (one per agent start/stop/restart)

Every one of these starts with `tracer.Start(context.Background(), ...)`. The
`ev` argument has no trace context. So:

- Tasks claimed in response to a `loom serve` HTTP request show up in their
  own trace, disconnected from the request span that triggered them.
- Agents spawned by the daemon show up in their own trace, disconnected from
  the daemon's supervisor loop.
- A single user action ("kick off this plan") fragments into 3+ disjoint
  traces.

For Phase 9 to deliver the value the trace contract promises (§1: "agent
event spans connect to originating requests"), this gap has to close.

## Constraints

1. Events are written to a JSONL log on disk (`internal/events/jsonl_writer.go`)
   and then read by `otelexport` via the in-process bus subscription. The
   bus and the JSONL log are two parallel sinks for the same `events.Event`.
2. Agents run in *separate processes* spawned by the daemon. There is no
   in-process channel between the request span and the agent process.
3. The bus subscription is asynchronous — the `HandleEvent` callback runs
   on a background goroutine, divorced from any HTTP request goroutine.
4. The contract requires `LOOM_TRACE_PARENT` env-var propagation to spawned
   subprocesses (Phase 7 — already shipped).

## Three options

### Option A — Add `TraceContext` field to `events.Event`

Extend the struct:

```go
type Event struct {
    Type   EventType
    Agent  string
    // ...existing fields
    TraceParent string // W3C traceparent at emit time
    TraceState  string // W3C tracestate at emit time
}
```

Every emit site (`Bus.Emit(...)`) is changed to capture
`otel.GetTextMapPropagator().Inject(ctx, ...)` from the *call site's*
context. The `otelexport` consumer uses
`propagation.Extract(...)` to rebuild the context and starts the span as
its child.

**Pros:** Direct. Every event carries the parent inline. Works for
in-process and cross-process events (agents already write JSONL with
this field; the daemon reads it back).

**Cons:** Schema change. JSONL log readers downstream of us (analytics,
ad-hoc scripts) see a new field. Every emit site must be audited to pass
a real ctx (some pass `context.Background()` today — those are bugs to
fix anyway).

### Option B — Reroute event emission through a context-aware sink

Instead of changing `Event`, change the bus interface from
`Emit(Event)` to `Emit(ctx, Event)`. The bus internally captures the
trace context per-event into a side map keyed by event ID. The JSONL
writer ignores it; `otelexport` reads from the side map when handling.

**Pros:** No schema change. Backward-compatible JSONL.

**Cons:** Side map = cross-cutting state. Memory leak risk (map entry
never cleaned up if the consumer crashes between emit and handle). Two
sources of truth for "what's the parent of this event?" depending on
whether the consumer is in-process or replaying from JSONL.

### Option C — Replay JSONL with synthetic context, drop in-process bus

Make JSONL the canonical event sink. `otelexport` becomes a *tail
reader* of the JSONL file rather than a bus subscriber. The trace
parent comes from a `traceparent` field on the JSONL line (so this
needs the schema change anyway — it ends up looking like Option A).

**Pros:** One pipeline, not two. Replay from disk → traces work even
on restart (catch up after a crash).

**Cons:** Big-bang refactor. The bus has other consumers (live UI
updates via SSE). Removing it in this phase is too much scope.

## Scoring

| Criterion | A | B | C |
|---|---|---|---|
| Connects in-process event spans to request | ✓ | ✓ | ✓ |
| Works across daemon → agent process boundary | ✓ | ✗ (side map is per-process) | ✓ |
| Schema change required | yes | no | yes |
| Memory-leak risk | none | medium | none |
| Refactor scope | medium | small | large |
| Replay-from-disk traces | ✓ | ✗ | ✓ |

## Decision

**Option A.** It's the only option that solves the cross-process case
cleanly, which matters because daemon-spawned agents are the most
common source of disconnected spans. The schema change is a one-line
addition to `events.Event`; readers that don't know about
`TraceParent` will silently ignore it. The audit of emit sites for
real-ctx propagation is work we'd have to do under any option.

## Implementation outline (Phase 9)

1. Add `TraceParent string` and `TraceState string` to `events.Event`.
2. Change `Bus.Emit` to `Bus.Emit(ctx context.Context, ev Event)`.
   At the entry, inject the active span context into `ev.TraceParent`.
3. Update every emit site to pass a real ctx. Most are already in
   methods that take ctx; the few that don't (timer-driven background
   emitters) get a top-level supervisor span as parent.
4. In `otelexport.HandleEvent`, extract via the propagator before the
   `tracer.Start` call and use the extracted context as parent.
5. JSONL writer: serializes `TraceParent` as a top-level field.
6. JSONL reader (replay path): rebuilds context from `TraceParent`
   before re-emitting.
7. Cross-process: agents inherit `LOOM_TRACE_PARENT` (Phase 7) at
   process start. Each event the agent emits picks up that context as
   the default when none is in-flight.

## Open questions for the implementation PR

- Should `tracestate` (vendor-specific) ride alongside `traceparent`?
  Recommend yes — costs nothing and matters when we add baggage.
- Should we also add `BaggageHeader` for `loom.workspace`, `loom.actor`?
  Recommend yes; same justification.
- Do we replay-from-JSONL? Recommend NO in Phase 9 — keep the
  existing bus path. Replay is a separate, larger initiative.

## How the open questions resolved

- **`tracestate` alongside `traceparent`** — yes. Both are fields on
  `events.Event` (`internal/events/event.go:77-78`) and both are captured by
  `InjectTraceContext` (`internal/events/trace_context.go:52-60`).
- **`BaggageHeader` for `loom.workspace` / `loom.actor`** — **no**, rejected.
  See `internal/events/trace_context.go:47-51` for the reasoning
  (high-cardinality values would be baked into the on-disk JSONL log).
- **Replay-from-JSONL** — not adopted, as recommended.
  `MetricsStore.ReplayFromFile` (`internal/events/replay.go:14`) feeds the metrics store, not the span exporter.

## Related

- [README.md](./README.md) — index and precedence for these three docs
- [tracing.md](./tracing.md) — what is traced today
- [tracing-contract.md](./tracing-contract.md) — normative span names and keys
