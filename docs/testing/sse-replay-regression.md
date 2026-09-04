# SSE replay-to-live regression

## Behavior

Reconnect drains every FleetDB mutation page before Loom emits `connected`.
The browser remains `connecting` until that event, rather than treating
EventSource transport open as synchronization. Hub registration completes
before replay starts; exact durable IDs suppress delayed live copies of
replayed records without decoding or comparing opaque cursors.

A later-page failure returns the successfully read prefix alongside the error.
Loom emits that prefix and closes without a completion barrier, allowing the
next reconnect to advance. For `source_repos` filters, excluded replay records
emit `checkpoint` frames containing only their durable ID and `{}` data. These
advance the browser cursor without publishing a domain mutation or marking the
stream synchronized. The contract lives in `api/openapi.yaml`, with generated
frontend documentation updated alongside producer and consumer.

## Regression boundaries

- `internal/webui/subscription/replay_test.go`: actual Fleet HTTP adapter,
  subscriber, hub and SSE handler against a controlled external HTTP transport.
  Backlogs of 101, 201 and 1,001 records assert exact ordered IDs before the
  completion barrier. The final replay record is also broadcast during the
  page read; a subsequent live marker verifies overlap cannot regress the
  checkpoint. Failed first/later pages, with and without repository filtering,
  verify failure visibility and partial progress.
- `src/api/common/__tests__/sse.test.ts` and the React event-provider tests:
  the same client reconnects, preserves its cursor, processes replay, and only
  changes the UI connection state after the server barrier. Filtered checkpoints
  do not invoke mutation callbacks.
- `tests/e2e/integration/sse-replay.integration.spec.ts`: real local product API
  writes, native EventSource transport and the persistent browser application.
  A transport fault closes the EventSource and dispatches its error notification;
  subsequent stream requests are blocked while REST remains available. The test
  commits 201 creations, restores SSE and makes 10 more creations. It asserts
  all 211 creation events in API commit order, unique nonempty event IDs, the
  exact reconnect cursor, the entire missed backlog before the first reconnect
  barrier, and no navigation. It has no REST/reload fallback for delivery.

The browser's additional writes race with recovery but are not a deterministic
proof of writes during a particular replay page. The controlled Go test owns
that scheduling proof. Browser readiness is observed through the SSE barrier;
the React provider tests verify its effect on UI state. The current UI does
not expose the old always-visible `data-state="connected"` indicator.

## Run

From the LoomCLI root:

```sh
go test -race ./internal/backend/fleet ./internal/webui/subscription \
  ./internal/webui/server/realtime ./internal/webui/app -count=1 -timeout=180s

cd internal/webui/frontend
npm run test:unit -- src/api/common/__tests__/sse.test.ts \
  src/hooks/common/__tests__/useEventProvider.test.tsx \
  src/components/ConnectionStatus/__tests__/ConnectionStatus.test.tsx
```

Use a run-owned local-mode stack following
[the runtime runbook](../../.agent-skills/loom-pr-test/SKILL.md). From the frontend
directory, with the stack URLs substituted:

```sh
RUN_INTEGRATION_TESTS=1 LOOM_LOCAL_SERVER=1 \
LOOM_BASE_URL=http://127.0.0.1:8482 LOOM_FRONTEND_BASE_URL=http://127.0.0.1:8483 \
npx playwright test --project=integration sse-replay.integration.spec.ts --reporter=line
```

Repeat with `SSE_REPLAY_DISABLE_DELIVERY=1`. **That run must fail** with an
empty delivered-creation list even though all REST creations succeeded. It is
an intentional negative control, not a second passing acceptance test. Each
run closes only issues it created and preserves the page until assertions end.

## Evidence from 2026-09-04

- Red before green: the initial Go test received only 100 events for each
  backlog size. After pagination alone was fixed, the overlap test observed a
  duplicate last replay cursor. The browser-client test observed `connected`
  at transport open. Failure/progress and filtered-checkpoint tests also failed
  their intended assertions before the corresponding changes.
- Four affected Go packages passed with `-race`; the focused replay cases also
  passed three consecutive repetitions.
- All 148 targeted frontend client/provider/connection-status tests passed.
- Frontend build/typecheck, changed TypeScript lint, generated-schema freshness,
  and `git diff --check` passed.
- The real local-stack browser regression passed twice (26.2s and 26.1s);
  the final run also checked the exact reconnect cursor. The SSE-disabled run failed
  on the intended exact-delivery assertion: 211 expected creations, zero
  delivered creations, after its 45-second observation window.

The local stack used Redis, FleetDB, Loom and Caddy on isolated ports, with the
repository's deterministic worker backend. No paid AI backend was involved.
Full repository suites and CI were not run. This is a targeted replay fix,
not closure of the entire SSE audit: token-fetch recovery, FleetDB silent hub
loss, non-durable cursor synthesis, and general hub retry-queue ordering remain
separate concerns. Replay still buffers a bounded-time prefix in memory;
it does not implement a finite storage snapshot boundary or retention reset.

Captured evidence: [Go race results](sse-replay-evidence/go-race.log.txt),
[positive browser run](sse-replay-evidence/browser-positive.log.txt),
[negative browser run](sse-replay-evidence/browser-negative.log.txt), and
[exact browser delivery proof](sse-replay-evidence/browser-proof.json).
The logs reference ephemeral local screenshot/video paths; the delivery proof
and text results are preserved here.
