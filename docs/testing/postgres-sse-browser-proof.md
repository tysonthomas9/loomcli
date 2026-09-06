# PostgreSQL bootstrap and browser SSE proof

Run on 2026-09-05 Pacific (2026-09-06 UTC), using Loom branch `test/pg-sse-browser` based on #679 `12bba19d748d20ddb379a86a81ae80d49afcdf1f` and Fleet branch `feat/pg-workspace-genesis` based on #285 `99b4b027b453db31a08c2f5c1fb86679134ffc15`. Both services were built from the final production source in these paired branches; only documentation and captured evidence changed afterward.

Evidence coordinates: browser/product integration; real local Loom/Fleet/PostgreSQL services; isolated Podman project; positive normal-workflow checks plus a real proxy disconnect. The standard deterministic localdogfood backend is orchestration, not paid AI execution. No manual database enrollment, seeded SQL, fake browser responses or transport mocks were used. The unchanged product entrypoint creates the workspace, roles, repository, agents, epic and tasks through supported APIs/CLI.

## Reproduction

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-genesis-0905 \
LOCAL_MODE_COMPOSE='podman compose' \
LOCAL_MODE_FLEETDB_BUILD_CONTEXT=/path/to/paired/fleet-db \
LOCAL_MODE_FLEETDB_PORT=8580 LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' make local-mode-postgres-up
```

Browser URL `http://127.0.0.1:8583/ws/LOCALMODE/kanban`; agent-browser session `pg-genesis-0905`, dedicated profile `/private/tmp/loom-agent-browser/pg-genesis-0905`. The [passive observer](evidence/pg-sse-genesis-0905/fetch-observer.js) records request paths, status, timings and resume headers. It forwards the real fetch unchanged and does not capture credentials or substitute responses.

## Product failures fixed before the final run

1. Foreground projection reported a pending role after the background worker had already committed it. Fleet now reconfirms the exact target receipt after no progress or a later apply failure; a deterministic negative control fails on the old implementation.
2. Loom attempted to place a bare workspace-local Repo name into Fleet's global `org/repo` catalog. Create/delete now operate only on first-class Repo records; global catalog validation remains strict.
3. Fleet issue creation checked only that global catalog. Explicit `HasIssueRepo` membership now admits a registered same-workspace Repo's effective source identity or a deliberate global binding, with strict malformed-record checks in both backends.

Fresh workspace genesis is opt-in and atomic, covering the source anchor, workspace projection, fresh provenance, receipt and prefix. It does not promote historical lanes. The fourth fresh product startup completed without hand-seeding state. See the paired Fleet `docs/postgres-workspace-genesis-proof.md` for storage fault and race evidence.

## Browser observations

| Scenario | Observed result and limit |
| --- | --- |
| Startup | Product-created board, repository and agents rendered. Token and fetch-SSE routes returned 200. Development auth was enabled; this is not an authentication-security proof. |
| Mounted status update | PATCH through Loom API changed LOCALMODE-5 from open to review (200); the card moved into Review without page reload. A collection GET followed, so this establishes stream-triggered refresh rather than cache-only mutation application. |
| Browser offline toggle | Existing SSE continued while subsequent collection fetches failed. The board showed Failed to load data/Retrying. This did not establish a stream disconnect. |
| Actual socket interruption | Stopped only `loomcli-pg-genesis-0905-ui-local-1`, restored browser online mode, PATCHed the issue title through the still-running API (200), then started the same proxy. No browser reload occurred in this phase. |
| Resume request | The browser retried token acquisition, then fetched events with HTTP200, no `since` query and a c2 Last-Event-ID representing source position 24-0. The real Fleet committed page after that exact cursor contains the disconnected issue.update at 25-0 and the same source identity. |
| UI convergence | The title “Exercise epic swimlane UI — disconnected replay” appeared in Review. Two collection GETs followed reconnect, so the DOM cannot establish replay-only or exactly-once delivery. The immediate reconnect snapshot still exposed Retry now; do not infer a healthy status indicator from HTTP200 alone. |
| Selected history and comment | Ordinary issue/history requests returned 200. Keyboard activation of Add Comment sent POST201; COMMENTS(1) and Added comment rendered. After reload and reopening the issue, the same one comment and journey entry remained. The initial pointer action issued no request; it was not counted as a submission. |
| Browser tooling | Page-error query reported no exceptions. Screenshot commands hung and were stopped/bounded; no screenshot artifact or visual-layout verification is claimed. The dedicated session was restarted before the measured mutation/reconnect sequence. |

[Fetch trace](evidence/pg-sse-genesis-0905/final-fetch.json), [committed source page](evidence/pg-sse-genesis-0905/replay-source.json), [reconnected DOM](evidence/pg-sse-genesis-0905/reconnected-dom.txt), [comment DOM](evidence/pg-sse-genesis-0905/final-dom.txt), [after-reload DOM](evidence/pg-sse-genesis-0905/persisted-dom.txt), and [after-reload fetches](evidence/pg-sse-genesis-0905/persisted-fetch.json) are checked in. DOM evidence is the browser accessibility snapshot, not reconstructed HTML. Fetch timings share one navigation's performance clock; the after-reload trace starts a new clock.

## Validation and remaining work

Loom Fleet adapter race suite passed (1.510s), workspace manager/config race suites passed (2.924s/1.637s), scoped adapter lint reported zero issues, and vet passed. The real Make target built the frontend and paired service images. Fleet affected race suites, build, vet, lint, PostgreSQL fault/producer proofs and strict harness gate passed; detailed commands/results are in its proof. Independent agents reviewed implementation and browser evidence. No full browser integration suite or hosted CI success is asserted for these heads.

This closes the basic product-bootstrap/connectivity obstacle from Fleet #286, but that issue remains open for multiclient, exact event-level duplicate/gap assertions and remaining scenarios. Source replacement, retention expiry, workspace A→B→A, complete cache publication, checkpoint reset acknowledgment and public autonomous claim/release routing remain unverified or unfinished. The browser's ordinary history is not publication of a v6 recovery manifest. Existing fetch-SSE integration tests that require zero collection refetches need reconciliation against the current observed invalidation path; do not weaken those assertions without deciding the intended contract.

The active goal remains browser verification and delivery, with broader recovery architecture explicitly deferred. This patch does not claim all SSE bugs fixed or whole-client recovery complete.

## Cleanup

Only this run's browser session and Compose project are in scope. Logs were preserved under `/private/tmp/sse-stack-review/genesis-*`; final `make local-mode-postgres-down` completed with the same project name, removing its containers, volumes and network. The older proof PostgreSQL server and shared lifecycle services on ports 8380/8382/8383 are outside this cleanup scope.
