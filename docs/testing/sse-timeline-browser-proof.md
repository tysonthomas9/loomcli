# Current fetch-SSE browser verification

The current paired build renders ordinary issue/history/comment views, but the real local-mode stack cannot establish its strict mutation feed. The browser displays `Reconnecting — data may be stale`; it does not silently use the legacy raw feed. This is a failed live-update acceptance scenario, not an end-to-end recovery pass.

## Reproduction and scope

Run on 2026-09-05 Pacific time using `feat/sse-recovery-timeline` based on Loom `b61da7d67e9ed2c1b637c5e5a74f8ec04c4b1728` and `feat/pg-recovery-timeline` based on Fleet `8e79247621e3b9a28c46c062a67bcb0fc625900f`, including the v6 patch in these branches. Both images were built from the current worktrees. Documentation was added afterward; production source was frozen during the run.

Evidence coordinates: browser/product integration; real local Loom and Fleet/Redis services; isolated Podman Compose project; positive ordinary-view checks and negative unsupported-feed check. The repo's deterministic localdogfood agent backend created and changed issues through its real workflow. No paid AI backend was launched. This does not prove AI execution or PostgreSQL browser recovery. No API interception, fake responses, direct storage writes or manual enrollment were used.

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-sse-timeline-0905 \
LOCAL_MODE_COMPOSE='podman compose' \
LOCAL_MODE_COMPOSE_FILES=/private/tmp/sse-stack-review/timeline-browser-compose.yml \
LOCAL_MODE_FLEETDB_PORT=8480 LOCAL_MODE_API_PORT=8482 LOCAL_MODE_UI_PORT=8483 \
LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' make local-mode-up
```

The override only points Fleet's build context at the paired `fleet-db` worktree and its `deploy/docker/Dockerfile`. Browser URL: `http://127.0.0.1:8483/ws/LOCALMODE/kanban`. Agent-browser session: `sse-timeline-0905`; dedicated profile: `/private/tmp/loom-agent-browser/sse-timeline-0905`. The browser exercised the current production frontend bundle and fetch transport.

## Observations

| Scenario | Actual result |
| --- | --- |
| Initial board | Product-created workspace, agents, epic and cards render. Visible reconnect/stale-data warning. |
| Stream setup | Browser network records show `/api/workspaces/LOCALMODE/events/token` returning 200 and `/events` returning 503 as Fetch requests. No successful SSE connection was established. Local-mode auth is disabled; token-route availability is not an authentication-security proof. |
| Source diagnosis | Fleet logs show `/api/v2/LOCALMODE/events/mutations` returning 503. A correctly framed request with the documented development `X-Actor` header returns `mutation_source_unsupported`. Legacy daemon v1 polling still occurs; it does not provide the browser's strict committed feed. |
| Stale board | `LOCALMODE-5`, “Exercise epic swimlane UI,” remained in Open on the mounted board after the real deterministic planner changed it. Opening its details showed Review. A reload moved the card into Review. This demonstrates the consequence of the unavailable stream. |
| Selected history | Ordinary `.../issues/LOCALMODE-5/events?limit=200` returns 200. The rendered journey includes creation, claim/release, design and status changes. This is ordinary history, not v6 recovery publication. |
| Comment round trip | Added “Browser verification: current timeline render and comment round trip.” through the UI. The comment persisted after reload and reopening the issue; the journey included Added comment and reported seven returned events. |
| Browser exceptions | `agent-browser errors` returned no page exceptions at the final check. HTTP 503 stream failures remained present. |

Screenshots:

- [Mounted board with stale-data warning](evidence/sse-timeline-0905/kanban-stale.png).
- [UI-created persisted comment](evidence/sse-timeline-0905/comment.png).
- [Board corrected after reload](evidence/sse-timeline-0905/kanban-reloaded.png).

Local logs and DOM/network evidence are under `/private/tmp/sse-stack-review/timeline-browser-*`. Screenshots above are checked in; temporary logs are not permanent CI artifacts.

## Remaining acceptance and next step

The standard local-mode Compose stack uses Redis, which does not implement the strict committed v2 source. PostgreSQL selection alone is insufficient: `InitializeProjectionLane` has no production caller, and existing bootstrap creates workspace data before any clean-genesis enrollment. Hand-inserting an enrollment would not prove the product workflow. Public issue claim routing also still uses the legacy lock API, which is incompatible with enrolled lanes.

The next implementation should expose supported clean-genesis enrollment in the real workspace/bootstrap workflow and provide a PostgreSQL local-mode configuration. Then rerun the existing real SSE update and multiclient integration scenarios through that workflow. Autonomous claim/release evidence additionally requires its enrolled public routing.

Successful reconnect, exact Last-Event-ID continuity, post-boundary delivery without duplicates, source replacement, workspace A→B→A races, retention-expiry recovery and v6 cache publication were not proved in this browser run. Their focused tests remain separate evidence. Prepared recovery data still cannot publish or acknowledge a reset; complete view coverage and publication ownership remain prerequisites.

The browser was closed and `make local-mode-down` completed with the same Compose project and override, removing the run-owned containers, volumes and network. Shared lifecycle services on ports 8380/8382/8383 and the earlier PostgreSQL proof container are outside this run's cleanup scope.
