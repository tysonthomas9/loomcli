# Redis SSE compatibility test — 2026-09-09 PDT

Baseline result: **failed before SSE activation**. The same seven-test browser suite that passed on PostgreSQL failed its two initial connection tests against Redis; the remaining five did not run because each spec is serial. This does not establish Redis catch-up, stale-content recovery or duplicate handling.

## Runtime evidence

A fresh standard local-mode stack used project `loomcli-pg-browser-redis-0910`, ports 8680/8682/8683, and exactly the paired FleetDB/Loom images used for the PostgreSQL proof, with current frontend revision 8c0be5f97. The project prefix and verifier target are historical safety guards; no PostgreSQL compose override or PostgreSQL service was used. FleetDB startup explicitly logged `backend: redis`. The stack used deterministic localdogfood and made no paid inference calls.

Command:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-redis-0910 \
LOCAL_MODE_API_PORT=8682 LOCAL_MODE_UI_PORT=8683 \
PLAYWRIGHT_JSON_OUTPUT_FILE=/private/tmp/redis-sse-report.json \
make local-mode-postgres-sse-verify
```

The real browser obtained its events token and issue snapshot with HTTP 200, but every events subscription returned HTTP 503. agent-browser 0.37.1 independently observed the same token-200/events-503 sequence. Direct product endpoint reads confirmed:

- Loom `/api/workspaces/{workspace}/events`: `subscription_unavailable`.
- FleetDB `/api/v2/{workspace}/events/mutations?since=$`: `mutation_source_unsupported`, “scoped committed mutation source unsupported”.

Sanitized [trace and result](evidence/redis-sse-0910/). The original PostgreSQL stack remained running. The Redis test containers were stopped after evidence collection.

## Cause and required follow-up

At FleetDB revision 730f47af1931c55eee95d092a5d3772c9716e930, `cmd/fleet-db/mutations_scoped.go` delegates ReadScopedMutationPage to the committed reader. The only production storage implementation is `internal/storage/postgres/mutation_source.go`; Redis cannot satisfy this interface. `internal/api/mutations_scoped.go` deliberately converts unsupported source capability to 503. Loom's subscriber activation fails and `internal/webui/server/realtime/handler.go` returns subscription_unavailable before sending an SSE connected frame.

Redis needs an implementation of the same scoped committed-mutation contract: durable source identity, source-bound head and replay cursors, ordered committed mutation pages with a fixed replay boundary, and correct source-change/retention errors. Certified projection snapshot/recovery support must also be audited for Redis before claiming full recovery parity. A raw Redis-stream cursor fallback would bypass the new guarantees and is not a fix.

After implementing that contract, run this exact suite against both storage backends as a required matrix gate, with zero skipped tests. The baseline run changed no production code and did not weaken the assertions or replace failed SSE with polling.


## Fix and successful Redis proof

FleetDB commit `39ba7510` now admits fresh workspaces into a scoped committed
Redis mutation feed. A compare-and-apply Lua boundary checks source identity,
incarnation, the previously observed head, and the next raw event before applying
projection commands. Required blocked-cache rebuild and committed publication run
in that same script. Other projectors recompute their commands after a head conflict.
The mutation hub subscribes to committed publication rather than raw append.

The paired Redis stack `loomcli-pg-browser-redis-fixed-0910`, ports
8780/8782/8783, passed **all seven tests, zero skips, zero retries, 33.5 seconds on the final rebuild (initial fix run: 33.8 seconds)**.
This used a real Redis service, FleetDB, Loom, the UI proxy, and Chromium.
No SSE frames were mocked. The suite proves connected frames, create/status/close
updates, rapid creates, two-client delivery, actual proxy disconnect and accepted
cursor replay, no duplicates in the observed intervals, retained content and stale
warning recovery. [Machine-readable result](evidence/redis-sse-0910/final-result.json).

agent-browser 0.37.1 separately observed HTTP 200 on the actual Fetch SSE request,
created `SSE-TEST-1789011230611-Y1YOUEKBU-9` through New Issue, and changed its status
to Closed through the details panel. The final UI status was `closed` and the browser
reported no page errors. [Sanitized network and UI evidence](evidence/redis-sse-0910/fixed-agent-browser-network.json).
The attempted 60 fps recording failed because ffmpeg received no image stream;
there is no successful video artifact for this run.

Rerun the same verifier with the patched FleetDB image:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-redis-fixed-0910 \
LOCAL_MODE_API_PORT=8782 LOCAL_MODE_UI_PORT=8783 \
PLAYWRIGHT_JSON_OUTPUT_FILE=/private/tmp/redis-sse-fixed-report.json \
make local-mode-postgres-sse-verify
```

## Coverage limits

Existing historical Redis namespaces are not silently enrolled. They need an
explicit certified rebuild/migration. Certified snapshot recovery is unsupported;
legacy import and replay reject enrolled histories before deleting state. Legacy
recovery requires a quiesced workspace; its preflight is not an online mutation fence.
This proof does not cover Redis restart durability (the local Redis service disables
AOF), Redis failover, certified stream retention/scale, or paid agent execution.
The browser verifier still needs to become a required Redis/PostgreSQL CI matrix;
a local passing run does not establish that CI gate.
