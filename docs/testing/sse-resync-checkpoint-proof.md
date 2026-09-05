# Accept checkpoints only after complete frames

The fetch-event-source parser updates its retry header as soon as an `id:` line
is parsed, before the frame's terminating blank line. If the stream ends in
between, the application never receives the mutation but retry could skip it.
New real-library tests reproduced this: an unterminated frame changed the public
checkpoint from the accepted value (or no value) to `skipped` before dispatch.

The client now owns the accepted checkpoint separately from the parser header.
Only complete frame callbacks can accept parser progress. Getters and manual
disconnect/rebind use accepted state; each network attempt restores the parser
header from that state before it can be reused for retry. A truncated empty-ID
reset likewise cannot erase the accepted checkpoint.

The modern authoritative server sends resync frames without an SSE ID, but the
browser still accepted ID-bearing resync frames as cursor advancement or reset.
The fetch-event-source parser changes its retry header before invoking the
message callback. Query recovery was only scheduled after that advancement,
so a failed refresh could leave reconnect skipping unobserved records.

The client now restores its previous checkpoint and the parser's actual mutable
header before invoking resync listeners. This applies to every resync reason,
malformed payloads, explicit empty IDs, and a missing previous checkpoint.
Resync remains a notification; it never authorizes cursor progress. Listeners
observe `from` and `to` as the same retained checkpoint (or no checkpoint).

The stream continues after the notification. A subsequent valid mutation or
checkpoint can still advance normally, including in the same received chunk.
Standalone ID-only frames and explicit empty-ID resets outside resync retain
their existing transport semantics. No speculative recovery candidate is
acknowledged by this change.

## Evidence class

Tests use the actual pinned fetch-event-source library with deterministic
ReadableStream HTTP fixtures. They check the outgoing Last-Event-ID header on
retries, not just the client field. Coverage includes omitted, empty, changed,
and malformed resync frames, callback-driven disconnect/rebind, same-chunk
valid frames, unterminated IDs/mutations, and existing transport checkpoints.
The truncated-frame tests failed against the earlier implementation before the
accepted-checkpoint fix; the red log is `resync-truncated-red.log`. This is library/client
integration, not paired browser or actual FleetDB storage proof.

Run from `internal/webui/frontend`:

```sh
npx vitest run src/api/common/__tests__/sse.test.ts
npx vitest run --maxWorkers=2
npm run typecheck
npm run lint
npm run check:arch
npm run build
```

Final local validation: all 74 focused SSE tests and all 9,032 frontend tests
across 416 files pass. TypeScript, scoped ESLint, formatting, architecture checks,
and the production frontend build pass. Full frontend ESLint reports no errors
and 26 existing warnings outside this change; the build retains its bundle-size
warning. The provider integration assertion now checks that handshake resync
schedules one refresh and one epoch without accepting the supplied floor.
Independent implementation and integration review found no blocker.

Logs are under `/private/tmp/sse-stack-review/resync-checkpoint*`. Dependencies
are installed in this isolated worktree; no shared node_modules are modified.

## Recovery remains incomplete

Rejecting premature acknowledgment does not implement a successful expired
cursor reset. The current coordinator proves that registered refresh callbacks
completed, not that their results came from the captured source or observed
its committed prefix. Redis and unenrolled raw heads may precede projected
effects, and REST source selection is independent from the SSE connection.

The [recovery read-proof plan](../design/sse-recovery-read-proof-plan.md) records
the required source binding, read certificates, query scope, attempt identity,
and acknowledgment protocol. Re-expiry and scope changes must invalidate an
attempt. Durable source incarnation and paired browser evidence remain required.
The full streaming goal remains active; no merge or deployment is included.
