# Fleet-Db Agent Control Plane

**Status:** Contract v1 for implementation
**Date:** 2026-05-01
**Related:** `loomcli-wpltp`, `distributed-control-plane.md`,
`distributed-control-plane-data-model.md`

## Purpose

This document defines the fleet-db architecture needed before loomcli can
remove beads and keep local plus distributed agent execution coherent.

The user-facing model should be agent-centric. Avoid exposing "runs" as a
primary concept. Internally, though, execution still needs separate identities
for durable agent configuration, individual execution attempts, leases, nodes,
terminals, and artifacts.

## Current Fleet-Db Fit

Fleet-db already has several useful primitives:

- Issue CRUD, ready/blocked queries, search, labels, dependencies, comments,
  history, and mutation streams.
- Issue claim/release with TTL-backed locks and same-actor refresh.
- Worker storage with `idle` and `working` states plus heartbeat timestamp.
- Repos, roles, agents, and daemon profiles as first-class Redis-backed
  entities.
- V2 opaque event cursors for mutation polling and SSE resume.
- Auth permissions for issues, workers, events, repos, agents, roles, and
  daemon profiles.

These are not enough for distributed loom execution. The current worker/claim
model is issue-centric and uses `issue_id -> worker_id` locks. It does not have
node identity, session history, lease fencing tokens, terminal metadata,
artifacts, or control commands.

## Target Model

### AgentProfile

Durable configuration for a named agent. This is the user-facing "agent".

```text
AgentProfile
  workspace_key
  agent_id
  mode: ephemeral | service
  role_name
  backend
  fallback_backends
  repos
  repo_groups
  task_filter
  max_concurrency
  budget_policy
  desired_state: stopped | idle | running | draining
  created_at
  updated_at
```

`ephemeral` agents normally claim one task, execute one session, and exit.
`service` agents are long-running and may claim many tasks over time or run
orchestration/control workflows without a task.

### AgentSession

One execution instance for an agent. This replaces the proposed `TaskRun`
name. It gives the system a stable history and fencing target without making
the UI center on "runs".

```text
AgentSession
  session_id
  workspace_key
  agent_id
  node_id
  kind: task | orchestration | terminal | maintenance | ad_hoc
  task_id: optional
  terminal_id: optional
  parent_session_id: optional
  status: queued | leased | starting | running | idle | yielded |
          completed | failed | cancelled | expired
  phase
  attempt
  started_at
  last_heartbeat
  finished_at
  summary
  error_class
  exit_code
  metadata
```

For an ephemeral worker, one session usually equals one task attempt. For a
long-running service agent, one service session may stay open and create child
task sessions or serially claim tasks.

### AgentLease

Authority to mutate an active session. Completion, heartbeat, progress, yield,
and artifact attachment must require a valid lease token.

```text
AgentLease
  lease_id
  workspace_key
  session_id
  agent_id
  node_id
  holder_worker_id
  token_hash
  version
  acquired_at
  expires_at
```

The raw token is returned only to the claimant. Fleet-db stores only a hash.
This prevents a stale process from reporting success after another node has
reclaimed the same task/session.

### Node

Machine or runtime capacity that owns local side effects.

```text
Node
  workspace_key
  node_id
  owner_actor
  runtime_provider: local | e2b | kubernetes | ci | other
  labels
  capabilities
  tool_inventory
  version
  capacity
  drain_state: active | draining | drained
  last_heartbeat
  expires_at
```

Nodes are separate from users and agents. Users mutate intent, nodes execute
local effects, and agent sessions are the execution records.

### TerminalSession

Terminals are outside agent sessions by default. A terminal may attach to an
agent session, but it can also be used for ad-hoc workspace/repo inspection,
"talk to lead", debugging, or service-agent control.

```text
TerminalSession
  terminal_id
  workspace_key
  node_id
  repo_name: optional
  worktree_ref: optional
  owner_actor
  agent_id: optional
  session_id: optional
  task_id: optional
  purpose: run | profile | workspace | repo | ad_hoc
  status: opening | open | detached | closed | failed
  pty_backend: tmux | pty | container | e2b
  created_at
  last_activity_at
  closed_at
  metadata
```

Fleet-db stores terminal metadata and attach/control intent. Live PTY bytes
stay node-local or flow through a stream service. Do not persist terminal
output as fleet-db events.

### Artifact

Durable output metadata. Blob contents live in local storage, object storage,
or a log backend; fleet-db stores pointers and summaries.

```text
Artifact
  artifact_id
  workspace_key
  agent_id: optional
  session_id: optional
  terminal_id: optional
  task_id: optional
  kind: transcript | log | patch | diff | test_result | file_snapshot |
        commit | note
  uri
  content_type
  size_bytes
  digest
  summary
  created_at
```

## API Shape

This is the upstream fleet-db API contract loomcli expects before beads
fallbacks can be removed. Endpoint names are workspace-scoped and should be
reflected in fleet-db's OpenAPI definition, generated clients, Redis store,
Postgres store, and integration tests.

```text
POST  /api/v1/{ws}/nodes
GET   /api/v1/{ws}/nodes
GET   /api/v1/{ws}/nodes/{node_id}
POST  /api/v1/{ws}/nodes/{node_id}/heartbeat
PATCH /api/v1/{ws}/nodes/{node_id}

POST  /api/v1/{ws}/agents
GET   /api/v1/{ws}/agents
GET   /api/v1/{ws}/agents/{agent_id}
PATCH /api/v1/{ws}/agents/{agent_id}
DELETE /api/v1/{ws}/agents/{agent_id}

POST  /api/v1/{ws}/agents/{agent_id}/claim
GET   /api/v1/{ws}/agents/{agent_id}/sessions
GET   /api/v1/{ws}/agent-sessions
GET   /api/v1/{ws}/agent-sessions/{session_id}
POST  /api/v1/{ws}/agent-sessions/{session_id}/heartbeat
PATCH /api/v1/{ws}/agent-sessions/{session_id}
POST  /api/v1/{ws}/agent-sessions/{session_id}/complete
POST  /api/v1/{ws}/agent-sessions/{session_id}/release

POST  /api/v1/{ws}/terminals
GET   /api/v1/{ws}/terminals
GET   /api/v1/{ws}/terminals/{terminal_id}
PATCH /api/v1/{ws}/terminals/{terminal_id}
POST  /api/v1/{ws}/terminals/{terminal_id}/commands
POST  /api/v1/{ws}/terminals/{terminal_id}/attach-token

POST  /api/v1/{ws}/artifacts
GET   /api/v1/{ws}/artifacts
GET   /api/v1/{ws}/artifacts/{artifact_id}

POST  /api/v1/{ws}/agents/{agent_id}/commands
GET   /api/v1/{ws}/agents/{agent_id}/commands?since=...
POST  /api/v1/{ws}/commands/{command_id}/ack
```

The fleet-db OpenAPI spec must add schema components for:

- `AgentProfile` and profile create/update requests.
- `Node` plus node heartbeat and drain requests.
- `AgentSession` plus claim, heartbeat, update, complete, and release
  requests/responses.
- `AgentLease` plus lease token response and lease-token request headers or
  body fields.
- `TerminalSession` plus attach-token request/response.
- `Artifact` plus artifact create/list filters.
- `Command` plus command ack/result/cancel requests.
- `FleetEvent` or equivalent mutation payload with `entity_type`, `entity_id`,
  `action`, `cursor`, `actor`, and `occurred_at`.

Do not reuse loomcli WebUI's current local terminal schemas as the upstream
fleet-db contract. Those schemas describe local tmux process management; this
contract describes distributed metadata and control intent.

### API Invariants

- All mutating endpoints must require an actor identity. In local embedded
  mode this is `X-Actor`; in remote mode it is the authenticated principal plus
  any service/node identity granted by auth.
- `workspace_key` always comes from the path, not the request body.
- `node_id`, `agent_id`, `session_id`, `terminal_id`, `artifact_id`, and
  `command_id` are opaque IDs generated or validated by fleet-db.
- List endpoints must support pagination with an opaque cursor. Offset-only
  pagination is not acceptable for `AgentSession`, `Artifact`, or `Command`
  history.
- State transitions must be idempotent where clients naturally retry
  (`heartbeat`, `ack`, `release`) and conflict-protected where stale writers
  are dangerous (`complete`, `cancel`, lease renewals).
- Fleet-db should return typed errors that loomcli can map consistently:
  `validation`, `not_found`, `conflict`, `unauthorized`, `forbidden`,
  `lease_expired`, `lease_mismatch`, `stale_version`, and `unavailable`.

### Entity Contract

`AgentProfile` is the existing user-facing agent concept. Fleet-db may keep
the route name `/agents`, but the model must be treated as durable desired
configuration, not as an execution record.

`AgentSession` is the only accepted execution record. It replaces task-run
terminology for loomcli. A task attempt, long-lived orchestrator slice,
maintenance job, ad-hoc terminal-backed action, or cron invocation all create
or attach to an agent session.

`AgentLease` is the only write authority for active execution. Heartbeat,
progress, completion, failure, yield, release, command result, and artifact
attachment require the current lease token unless the operation is explicitly
user/admin control-plane intent.

`Node` is runtime capacity. It may represent a laptop, a local supervisor, an
E2B sandbox, a CI runner, or a Kubernetes pod. Node rows are observed/runtime
records, not durable agent configuration.

`TerminalSession` stores metadata and attachment intent only. PTY bytes,
scrollback, sockets, process IDs, and file descriptors remain node-local or in
a stream/log backend and are referenced through artifacts or attach tokens.

`Artifact` stores durable metadata and pointers only. Blob contents, large
logs, transcripts, screenshots, patches, and test outputs live in object
storage, local node storage, git, or a log backend.

`Command` is a control-plane request from user/service to agent/node/session.
Commands are not terminal bytes. They must have delivery state, ack state,
dedupe keys, and optional lease binding.

### Command Contract

```text
Command
  command_id
  workspace_key
  target_type: agent | session | node | terminal
  target_id
  type: start | stop | drain | resume | cancel | send_input |
        rotate_log | collect_artifact | open_terminal | close_terminal |
        custom
  payload
  idempotency_key
  status: queued | delivered | acked | running | succeeded | failed |
          cancelled | expired
  created_by
  created_at
  delivered_at
  acked_at
  completed_at
  expires_at
  result_summary
  error_class
  lease_id: optional
```

Commands are pull-friendly: workers can poll `GET
/api/v1/{ws}/agents/{agent_id}/commands?since=...` or subscribe through SSE.
Push transports may be added later, but the durable command log is the source
of truth.

The claim endpoint should atomically:

1. Find an eligible task for the agent profile and node capabilities.
2. Create an `AgentSession`.
3. Acquire an `AgentLease` with a fresh fencing token.
4. Transition the issue/task to the correct in-progress/claimed state.
5. Emit session and issue events.

## Event Actions

Add fleet-db event actions for the control-plane surface:

```text
node.register
node.heartbeat
node.update
node.drain

agent.create
agent.update
agent.delete

agent_session.create
agent_session.heartbeat
agent_session.update
agent_session.complete
agent_session.release
agent_session.expire

agent_lease.acquire
agent_lease.renew
agent_lease.release
agent_lease.expire

terminal.create
terminal.update
terminal.close

artifact.create

command.create
command.ack
command.cancel
```

High-frequency heartbeats should update projected state cheaply. Durable
events should be emitted for state transitions, lease acquisition/release, and
meaningful lifecycle milestones. Do not emit a durable event for every routine
heartbeat unless an audit mode explicitly asks for that volume.

## Storage And Indexes

Required hot indexes:

- profiles by workspace/name: `workspace_key, agent_id`
- active sessions by workspace: `workspace_key, status`
- sessions by agent: `workspace_key, agent_id, created_at`
- sessions by task: `workspace_key, task_id, created_at`
- sessions by node: `workspace_key, node_id, status`
- leases by expiry: `workspace_key, expires_at`
- leases by resource/session: `workspace_key, session_id, status`
- active nodes by heartbeat/expiry: `workspace_key, expires_at`
- terminals by workspace/node/status
- artifacts by session/terminal/task
- commands by target/status/cursor: `workspace_key, target_type, target_id,
  status, created_at`

Redis mode can use hashes, sets, sorted sets, and TTL keys. Postgres mode needs
tables and indexes for the same entities. Fleet-db should not ship this as
Redis-only if remote distributed mode is expected to be production-grade.

### Redis Storage Contract

- Store latest entity state in hashes keyed by workspace and entity ID.
- Maintain sorted-set indexes for session history, lease expiry, node expiry,
  command delivery, terminal status, and artifact history.
- Store active lease tokens as hashes only. Raw tokens must never be persisted;
  only a hash and lease version are stored.
- Lease acquire/renew/release/complete must be atomic. Redis mode should use a
  Lua script or equivalent transaction so session state, issue claim state,
  lease state, and event emission cannot diverge.
- TTL keys may drive expiry detection, but expiry must also be recoverable by
  scanning the `expires_at` index after process restart.

### Postgres Storage Contract

Fleet-db Postgres mode must expose the same behavior as Redis mode. Minimum
tables:

```text
agent_profiles(workspace_key, agent_id, desired_state, config_json, timestamps)
nodes(workspace_key, node_id, runtime_provider, labels_json, capabilities_json,
      drain_state, last_heartbeat, expires_at, timestamps)
agent_sessions(workspace_key, session_id, agent_id, node_id, kind, task_id,
      terminal_id, parent_session_id, status, phase, attempt,
      started_at, last_heartbeat, finished_at, metadata_json)
agent_leases(workspace_key, lease_id, session_id, agent_id, node_id,
      token_hash, version, acquired_at, expires_at, released_at, status)
terminal_sessions(workspace_key, terminal_id, node_id, agent_id, session_id,
      task_id, purpose, status, pty_backend, metadata_json, timestamps)
artifacts(workspace_key, artifact_id, agent_id, session_id, terminal_id,
      task_id, kind, uri, content_type, size_bytes, digest, summary, created_at)
commands(workspace_key, command_id, target_type, target_id, type, payload_json,
      idempotency_key, status, timestamps, result_json, lease_id)
```

Postgres lease mutation must use row locks or compare-and-swap predicates on
`version`, `token_hash`, and `expires_at`. It must not rely on app-side
read-then-write checks.

## Scalability Rules

- `AgentProfile` is low-cardinality config and can be queried often.
- `AgentSession` is append-heavy history and must be paginated and archivable.
- `AgentLease` is hot coordination state and must be small, TTL-backed, and
  updated atomically.
- Terminal byte streams and long logs must stay out of fleet-db. Store only
  metadata, cursors, attach tokens, and artifact pointers.
- Heartbeats update latest-state records; they should not become unbounded
  event streams.
- SSE should support workspace, entity type, agent, task, and repo filters so
  high-volume workspaces do not fan every event to every client.

### SSE And Cursor Contract

Fleet-db must expose one durable cursor model for long-poll and SSE. Cursors
are opaque to loomcli and may be Redis stream IDs, Postgres WAL/event IDs, or a
versioned encoded form. Clients must be able to reconnect to any fleet-db
process with the last cursor and receive missed events.

Required filters:

```text
workspace_key
entity_type: issue | node | agent | agent_session | agent_lease |
             terminal | artifact | command
agent_id
session_id
task_id
repo_name
node_id
```

Server rules:

- Filters apply to catch-up and live delivery.
- Slow clients are disconnected before unbounded buffering.
- Heartbeat-only updates may be coalesced into projected state; lifecycle
  transitions must be durable events.
- Event payloads include `workspace_key`, `entity_type`, `entity_id`, `action`,
  `cursor`, `actor`, and `occurred_at`.

## Local And Distributed Modes

Local single-user embedded mode and remote multi-user mode use the same
control-plane contract. The difference is deployment, not data shape:

| Concern | Local embedded | Remote distributed |
|---|---|---|
| Fleet-db process | Spawned by loomcli | Shared service |
| Redis/Postgres | Embedded miniredis for local dev | Managed Redis or Postgres |
| Node | Local supervisor registers one local node | Each laptop/sandbox/runner registers a node |
| Actor | `X-Actor` from local user/env | Authenticated user/service |
| Lease | Still required | Required |
| Terminal bytes | Local tmux/PTY | Node-local or stream service |
| Artifacts | Local URI or git/object pointer | Object/log storage pointer |

Local mode must not bypass node registration or leases. It is the cheap path
that keeps distributed correctness exercised during normal development.

## Upstream Work Tracking

The fleet-db implementation work is intentionally tracked as concrete tickets
before loomcli supervisor migration:

| Ticket | Upstream fleet-db scope | Blocks |
|---|---|---|
| `loomcli-wpltp.11.1` | `Node`, `AgentProfile`, and `AgentSession` domain/store/API/OpenAPI/client coverage | `.8`, `.11.7` |
| `loomcli-wpltp.11.2` | `TerminalSession` and `Artifact` metadata models, attach-token intent, artifact pointer APIs | `.11.7` |
| `loomcli-wpltp.11.3` | `AgentLease` fencing tokens, atomic acquire/renew/release/complete, stale-token errors | `.8`, `.11.7` |
| `loomcli-wpltp.11.4` | Durable `Command` log, polling, ack/result/cancel semantics | `.11.7` |
| `loomcli-wpltp.11.5` | Shared event fanout, opaque cursor catch-up, SSE filters, slow-client bounds | `.6`, `.11.7` |
| `loomcli-wpltp.11.6` | Postgres parity for existing repo/role/agent/daemon entities and all new control-plane entities | `.8`, `.11.7` |
| `loomcli-wpltp.11.7` | Loomcli supervisor migration to node/session/lease/artifact/command APIs | `.5`, `.9` |

`loomcli-wpltp.5`, `.6`, and `.8` must remain blocked until the relevant
fleet-db tickets above are implemented and accepted. The supervisor should not
invent loomcli-local placeholders for these concepts.

## Migration Plan

1. Add fleet-db models, storage, service, HTTP/RPC, OpenAPI, and client types
   for `Node`, `AgentSession`, `AgentLease`, `TerminalSession`, `Artifact`,
   and `Command`.
2. Keep existing issue claim/release endpoints as compatibility shims. New loom
   supervisor code should use agent claim/session endpoints.
3. Rework worker heartbeat around session lease renewal. Keep
   `/workers/{id}/heartbeat` as a compatibility endpoint until loomcli no
   longer needs it.
4. Route SSE through the existing EventHub for SSE as well as mutation
   long-poll, then add event filters.
5. Implement Postgres parity for repo/agent/role/daemon plus the new
   agent-control-plane entities.
6. Update loomcli's local supervisor to register a node, claim as an agent,
   heartbeat the session lease, report artifacts, and complete/release through
   the fencing token.
7. Only after fleet-db-only embedded and remote acceptance passes should
   loomcli switch fleet-db to the default and delete beads fallbacks.

## Impact On `loomcli-wpltp`

The following tasks depend on this fleet-db work:

- `loomcli-wpltp.5`: local supervisor as a fleet-db-backed worker
- `loomcli-wpltp.6`: fleet-db-only SSE and realtime acceptance
- `loomcli-wpltp.8`: remote distributed mode hardening
- `loomcli-wpltp.9`: fleet-db default and fail-closed behavior
- `loomcli-wpltp.10`: beads deletion

The design should be treated as a blocker for `.5`, `.6`, and `.8`, not as an
implementation detail inside loomcli.
