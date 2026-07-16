# Generic SSE Envelope Notes

Date: 2026-05-15

## Goal

The workspace SSE stream should carry all fleet-db-backed state changes, not
only issue-shaped mutations. Consumers should decide whether to invalidate local
state from generic envelope fields:

- `entity_type`: changed entity family, such as `issue`, `dependency`,
  `comment`, `label`, `agent`, `terminal`, `session`, or `workspace`
- `entity_id`: changed entity identifier
- `action`: source action, usually the fleet-db action, such as `issue.update`
  or `dep.add`

The legacy `type` and `issue_id` fields remain for backward compatibility.

## Implementation Notes

- Fleet mutation conversion preserves `entity_type`, `entity_id`, and `action`
  from fleet-db and still folds actions into the older coarse `type` value.
- RPC catch-up and live hub broadcasts project the same envelope fields so
  reconnect replay and live events share one payload shape.
- Terminal, issue-tab, agent-refresh, and session-change broadcasts now include
  entity envelope fields instead of relying on issue-specific fields.
- The frontend `EventProvider` can filter subscriptions by coarse type, entity
  type, and action.
- The issue store treats `entity_type` as authoritative when it is present. It
  invalidates issue projections for `issue`, `dependency`, `dep`, `comment`, and
  `label`; it ignores terminal/session/agent events even if a legacy `issue_id`
  is present.

## Gotchas

- Do not let `issue_id` override `entity_type`. Terminal session events can
  reference an issue for compatibility, but they should not mutate or refetch
  the issue list.
- There is one product browser SSE stream per workspace
  (`/api/workspaces/{ws}/events`). The server also keeps a backend mutation
  long-poll open to FleetDB (`/api/v2/{ws}/events/mutations`) so it can feed
  that browser stream. These are two different hops in one pipeline, not two
  browser SSE subscriptions.
- Backend mutation long-polls are activated only after an authorized SSE token
  or stream request (`/events/token` or `/events`). Ordinary workspace REST
  requests must validate workspace existence without starting a FleetDB mutation
  subscriber. This keeps REST polling, page bootstrap calls, and background
  monitor refresh from opening idle backend long-polls for workspaces with no
  browser event stream.
- Keep old events working: events without `entity_type` still fall back to
  legacy `type` and `issue_id` behavior.
- Keep backend and RPC projection in sync. `BackendMutationToPayload` and
  `RPCMutationToPayload` must produce equivalent JSON for the same logical
  mutation.
- OpenAPI generated types need to match the SSE schema even though the stream is
  consumed directly by the browser client.
- Embedded FleetDB needs a larger local Redis pool than FleetDB's production
  default. The UI opens one browser SSE client but also performs ordinary
  workspace, monitor, and issue requests while the backend mutation long-poll is
  active. Leaving FleetDB at its default pool of 10 caused `redis: connection
  pool timeout` and 503 responses during agent-browser checks. Embedded startup
  now defaults `FLEET_REDIS_POOL_SIZE=100` and
  `FLEET_REDIS_MIN_IDLE_CONNS=10`; the E2E harness uses pool size 200.
- When validating with `agent-browser`, use a named session and prefer UI-driven
  observations. Adding a second direct `EventSource` inside the same browser
  session can create extra long polls against the embedded FleetDB stack; in the
  2026-05-15 check this produced Redis pool timeouts and the UI correctly showed
  its stale/reconnecting banner.

## Validation

- Frontend focused SSE/store tests:
  `npm run test:unit -- src/api/common/__tests__/sse.test.ts src/hooks/common/__tests__/useEventProvider.test.tsx src/stores/__tests__/issueStore.test.ts`
  - `WorkspaceSSEClient` accepts generic non-issue agent payloads and tracks
    opaque cursors for them.
  - `EventProvider` entity/action filters deliver `agent.status` events and
    skip malformed mutation JSON without poisoning later generic events.
  - The issue store ignores `agent.status` and `agent.refresh` for issue-list
    mutation/refetch purposes, even if a legacy `issue_id` is present.
- Frontend contract checks:
  `npm run typecheck`
- Frontend lint on touched files:
  `npx eslint ...`
- Backend focused packages:
  `GOCACHE=/tmp/go-build-cache go test ./internal/rpc ./internal/backend ./internal/backend/fleet ./internal/webui/server/realtime`
  - Generic agent payload projection is covered in
    `internal/webui/server/realtime`.
  - Agent refresh broadcasting is covered in `internal/webui/handlers/agents`
    and asserts the event has `entity_type: "agent"` with no `issue_id`.
  - In sandboxed Codex runs, the full `./internal/rpc` package can fail on
    Unix socket bind permissions; use
    `GOCACHE=/tmp/go-build-cache go test ./internal/rpc -run 'TestMutationEvent'`
    for the mutation envelope checks when that happens.
- Agent-browser smoke:
  - Started an isolated E2E stack on API `:19191` and frontend `:3191`.
  - Opened `agent-browser --session sse-envelope` to the isolated frontend.
  - Created issue `E2E-WS-1` via the API and confirmed the Kanban UI received
    the SSE create event and rendered it in Open with Blocked still at 0.
  - Captured a browser-side `session_change` SSE payload with
    `entity_type: "session"`, `entity_id: "sse-envelope-agent-browser"`,
    `action: "session.change"`, and legacy `issue_id: "E2E-WS-1"`.
  - Confirmed the issue stayed in Open and did not appear as Blocked after that
    session event.
- Long-term activation check:
  - Started an isolated E2E stack on API `:19196` and frontend `:3196`.
  - Called `GET /api/workspaces/E2E-WS/issues` before opening the browser and
    confirmed it did not start `backend mutation subscription started` or
    `/api/v2/E2E-WS/events/mutations`.
  - Opened `agent-browser --session sse-longterm` to the frontend and confirmed
    `/api/workspaces/E2E-WS/events/token` activated the backend subscriber and
    started the FleetDB mutation long-poll.
  - Created issue `E2E-WS-1` through the UI. The Kanban and work queue showed
    `Open 1`, `Blocked 0`, and no stale/reconnect banner.
- Multi-workspace and multi-client follow-up:
  - Added route-level tests that run the subscription module behind the real
    workspace middleware and verify token activation resolves separate
    workspaces (`ws-alpha`, `ws-beta`).
  - Added stream-route coverage for multiple authorized clients across two
    workspaces.
  - Re-ran app-level hub tests for duplicate issue IDs across workspaces and
    two-tab SSE independence.
