# Agent-browser SSE delivery and reconnect proof — 2026-09-09

Passed against the running PostgreSQL/Redis/FleetDB/Loom stack at localhost:8583 using agent-browser 0.37.1 and two independent browser profiles. Loom revision bfa55e093; FleetDB revision 730f47af1931c55eee95d092a5d3772c9716e930. Provisioning: existing owned local-mode stack `loomcli-pg-browser-manual-0909`; real service transport and storage, deterministic localdogfood agent backend, no paid inference. Workspace SSE-MANUAL-0909 has no automatic agents.

## Procedure and observed result

1. Created SSE-MANUAL-0909-1, “SSE two-browser delivery 0909”, through the writer browser. Opened the same Kanban in the observer browser. Attached a passive Chromium network observer and reloaded once to establish the capture baseline.
2. Changed the issue to In Progress through the writer UI. The observer's actual SSE connection delivered `issue.claim`; the observer card moved to In Progress.
3. Changed the issue to Review. Browser offline emulation alone did not sever the existing SSE connection: the trace still received `issue.update`. This attempt was excluded as disconnect proof.
4. Kept the observer offline and stopped/started only this stack's UI proxy. The existing stream failed with `net::ERR_INCOMPLETE_CHUNKED_ENCODING`. Saved the trace before reconnecting. Changed the issue to Closed through the writer UI while the observer remained disconnected.
5. Set the observer online. Its new SSE request returned HTTP 200 with Last-Event-ID exactly matching the last received cursor (110-0), and no `since` query parameter. The server replayed `issue.assign` (111-0) and `issue.close` (112-0), then sent `connected`.
6. The observer showed exactly one matching card in Done. `performance.timeOrigin` matched the baseline, proving no document reload during recovery. Captured mutation IDs were unique within this test window. All eleven saved assertions passed.

All issue mutations used product UI through agent-browser. The passive CDP observer inspected the application's existing response; it did not create another SSE subscription or mock network responses. An earlier JavaScript fetch wrapper captured nothing and was excluded as evidence.

## Evidence and remaining finding

[Sanitized trace, outage text, final DOM assertions and checks](evidence/agent-browser-sse-0909/). Local recording: `/private/tmp/sse-agent-browser-proof-0909/observer.mp4`. Recorder reported 60 fps encoding, 36,091 output frames and 133 captured frames; repeated frames mean this is not proof of continuous 60 fps sampling or absence of brief flicker. The accepted transport evidence is the actual request/frame trace plus browser DOM assertions.

During the outage the page displayed “Connection lost — showing last known state” and “Reconnecting — data may be stale”, but the main content displayed “Failed to load data”. This is a remaining stale-state presentation inconsistency, captured in `disconnected.txt`; this run does not establish its root cause or fix it. Follow-up: determine whether ordinary disconnect should retain the mounted board while reporting retry failure, and separately test explicit resync behavior.

This proves one live cross-browser delivery and one genuine interrupted-stream catch-up. It does not cover expired cursors, resync recovery, server/database restarts, every screen, or arbitrary duplicate delivery. No production code changed in this proof. The application stack was left running and the observer returned online.
