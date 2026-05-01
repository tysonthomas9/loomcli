# Fleet-Db Agent Control Plane

**Status:** Draft for implementation planning
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

- active sessions by workspace: `workspace_key, status`
- sessions by agent: `workspace_key, agent_id, created_at`
- sessions by task: `workspace_key, task_id, created_at`
- leases by expiry: `workspace_key, expires_at`
- active nodes by heartbeat/expiry: `workspace_key, expires_at`
- terminals by workspace/node/status
- artifacts by session/terminal/task

Redis mode can use hashes, sets, sorted sets, and TTL keys. Postgres mode needs
tables and indexes for the same entities. Fleet-db should not ship this as
Redis-only if remote distributed mode is expected to be production-grade.

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
