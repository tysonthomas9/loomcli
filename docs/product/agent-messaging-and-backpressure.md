# Agent Messaging and Backpressure

> **Status:** Current · *audited 2026-07-23*

**Last updated:** 2026-07-23
**Related:** [`error-class-reference.md`](error-class-reference.md),
[`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md),
[`session-stores.md`](session-stores.md)

## Purpose

Three mechanisms sit between "an agent wants to do work" and "the work
happens", and none of them was documented. This file names them and says which
is which, because two are commonly confused with a third package that does
something entirely different.

- **`internal/agentinbox`** — durable agent-to-agent message delivery.
- **`internal/notify`** — in-process pub/sub event bus.
- **`internal/cli/automode/ratelimit_breaker.go`** — the rate-limit cooldown.

And the trap: **`internal/circuitbreaker` is not the agent rate-limit
mechanism.** See "What `internal/circuitbreaker` is" below.

## Agent inbox

`agentinbox.Enqueue` (`internal/agentinbox/message.go:29`) writes a
`domain.AgentInboxMessage` (`internal/domain/control_plane.go:235-259`)
addressed to a target agent. Statuses are `queued` | `delivered` | `failed`
(`control_plane.go:229-233`).

The message carries provenance, so a delivered instruction can be traced back
to whatever caused it (`MessageOptions`, `message.go:18-27`; persisted fields
`control_plane.go:243-249`):

| Field | Provenance |
|---|---|
| `source_kind` / `source_ref` | Free-form origin descriptor. |
| `driver_run_id` | The driver run that produced the message. |
| `task_run_id` | The task run within it. |
| `trigger_event_id` / `trigger_delivery_id` | The inbound trigger and its delivery attempt. |
| `session_id` | The sending session. |

`driver_run_id` and `task_run_id` are the only `*_run` identifiers in the
codebase. They belong to the driver subsystem and are **not** an identity for
an agent execution attempt — that is `session_id`
(see [`session-stores.md`](session-stores.md)).

Redelivery control is `dedupe_key`. `agentinbox.ContentDedupeKey(prefix,
parts...)` (`message.go:60-67`) hashes the parts with SHA-256 so a caller can
mint a stable key from message content without storing the content itself.
Delivery bookkeeping lives on the record: `attempt`, `claimed_by`,
`claim_expires_at`, `last_error`, `error_class`, `delivered_thread_id`,
`delivered_at` (`control_plane.go:250-256`).

## Event bus

`internal/notify` is an in-process pub/sub bus with workspace-scoped
subscriptions and zero internal-package imports (`internal/notify/doc.go:1-9`).

`Bus.Publish` is **non-blocking and lossy by design**: if a subscriber's
buffer is full the event is dropped for that subscriber and a per-subscriber
`dropped` counter increments (`internal/notify/bus.go:113-157`). Read
`Subscription.Dropped()` (`bus.go:80`) or `Bus.TotalDropped()` (`bus.go:213`)
before treating the bus as a delivery guarantee — it is not one. There is no
persistence and no replay.

Subscribers pick a workspace and a topic list (`Bus.Subscribe`, `bus.go:163`;
`SubscribeWithBuffer`, `bus.go:168`).

In the current tree the bus is constructed in exactly one place: the daemon,
via `wireDaemonNotifyBus` (`internal/cli/daemon/daemon_mutations.go:262-270`),
feeding a `MutationBuffer` subscribed to the `issue` topic
(`daemon_mutations.go:58`). The package doc's wider ambition — SSE hub, audit
log, metrics exporters — describes intended consumers, not currently wired
ones.

## Rate-limit backpressure

When an agent invocation comes back `RateLimited`
(see [`error-class-reference.md`](error-class-reference.md)),
`handleRateLimitError` (`internal/cli/automode/automode_task.go:265-283`) does
three things:

1. Increments `ConsecutiveRateLimits`; at `maxConsecutiveRateLimits = 5`
   (`automode_task.go:75`) the process exits rather than spending more retry
   budget (`automode_task.go:274-276`).
2. Records the event on the sliding-window breaker
   (`recordRateLimitOnBreaker`, `automode_task.go:288-304`).
3. Sleeps for `ae.RetryAfter` when the backend supplied one, otherwise 60s
   (`automode_task.go:277-282`).

The breaker itself is `rateLimitBreaker`
(`internal/cli/automode/ratelimit_breaker.go:43-54`), a sliding-window circuit
breaker specific to auto mode:

```text
Closed   → Open      when rate-limit events in the window reach the threshold
Open     → HalfOpen  when the cooldown elapses (observed via ShouldBlock)
HalfOpen → Closed    on RecordSuccess (window cleared)
HalfOpen → Open      on RecordRateLimit (fresh cooldown)
```

(`ratelimit_breaker.go:33-38`.) Two behaviors matter and are easy to get
wrong:

- In `Closed`, `RecordSuccess` does **not** clear the window. Alternating
  rate-limit/success patterns still accumulate toward a trip — that is the
  case the consecutive counter alone misses
  (`ratelimit_breaker.go:104-117`, `:27-31`).
- The breaker is disabled outright when threshold or window is `<= 0`
  (`ratelimit_breaker.go:71-73`).

On a trip, auto mode prints the window count and cooldown and emits a
`circuit.opened` event (`automode_task.go:298-304`).

## What `internal/circuitbreaker` is

A generic circuit breaker for protecting calls to unreliable services
(`internal/circuitbreaker/breaker.go:1-3`). It has nothing to do with model
rate limits or agent retry policy. Its callers are infrastructure only:

- Redis KV client (`internal/kv/client.go`), including the KV stale detector
  (`internal/cli/serve/daemonwire/stale.go:24-29`).
- The web UI's daemon IPC client (`internal/webui/daemon/breaker.go`).
- Web UI health/doctor reporting (`internal/webui/health_doctor.go`,
  `internal/webui/handlers/health/health.go`).

The KV stale detector only runs when a Redis address is configured; without
one, `InitStaleDetectorHandler` returns a handler that reports the detector
disabled (`internal/cli/serve/daemonwire/stale.go:17-20`).

## Related

- [`error-class-reference.md`](error-class-reference.md) — `RateLimited` and the rest of the vocabulary
- [`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) — user-facing recovery
- [`session-stores.md`](session-stores.md) — `session_id` vs `driver_run_id`/`task_run_id`
