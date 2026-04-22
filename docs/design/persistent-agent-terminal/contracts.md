# Service contracts

Interface boundaries between the services in the Part 2 architecture. These are **contracts**, not
implementations — the goal is that each repo can be built and tested against a mock of every other
without sharing code.

## Service map

```
  loom-webui ────────── gRPC ───────────► loom-agentd          (dataplane: terminal byte stream)
  loom-webui ────────── gRPC ───────────► loom-control-plane   (routing + ensure alive)
  loom-control-plane ── gRPC ───────────► loom-vm-host         (VM lifecycle)
  loom-vm-host ──────── Firecracker API ► Firecracker          (local UNIX socket)
  loom-agentd ───────── heartbeat ──────► loom-control-plane   (liveness)
  all ─────────────────── Redis ─────────► state                (routing, metadata, owner keys)
```

All gRPC is mTLS. Redis is shared between `loom-webui` and `loom-control-plane` only; `loom-vm-host`
and `loom-agentd` never talk to Redis directly.

## 1. `loom-webui` → `loom-agentd` (dataplane)

Primary path for persistent-agent terminal I/O. Lives over vsock (or TCP, see
[architecture.md §3](./architecture.md#3-part-2--persistent-agents-in-firecracker)).

### `Attach(stream)`  — bidirectional

```proto
service Terminal {
  rpc Attach(stream AttachClientMsg) returns (stream AttachServerMsg);
  rpc Kill(KillRequest) returns (KillResponse);
  rpc List(ListRequest) returns (ListResponse);
  rpc Quiesce(QuiesceRequest) returns (QuiesceResponse);
  rpc Resume(ResumeRequest) returns (ResumeResponse);
}

message AttachClientMsg {
  oneof msg {
    AttachOpen  open   = 1;  // first frame only
    AttachInput input  = 2;  // user keystrokes, raw bytes
    AttachResize resize = 3;
  }
}

message AttachOpen {
  string session     = 1;  // e.g. "lead-claude-1"
  uint32 cols        = 2;
  uint32 rows        = 3;
  string command_hint = 4; // optional: "loom lead --backend codex" — agentd may ignore
  bool   expect_replay = 5; // true = client expects scrollback replay from this reattach
}

message AttachInput   { bytes  data   = 1; }
message AttachResize  { uint32 cols = 1; uint32 rows = 2; }

message AttachServerMsg {
  oneof msg {
    AttachReady   ready   = 1;   // first server frame
    AttachOutput  output  = 2;   // raw PTY bytes
    AttachReplay  replay  = 3;   // scrollback reset+bytes in one frame
    AttachKilled  killed  = 4;   // session killed on agentd side
  }
}

message AttachReady  { string conn_id = 1; uint32 cols = 2; uint32 rows = 3; bool reattached = 4; }
message AttachOutput { bytes  data    = 1; }
message AttachReplay { bytes  data    = 1; }  // starts with \x1b[2J\x1b[H
message AttachKilled { string reason  = 1; }  // "idle_reap", "explicit_kill", "shutdown"
```

**Semantics.**
- First client frame MUST be `AttachOpen`. First server frame MUST be `AttachReady`.
- If `reattached == true` in `AttachReady`, a single `AttachReplay` frame follows before any
  `AttachOutput`.
- After replay, server streams `AttachOutput` frames until either side closes the stream.
- Resize is purely client → server. The server never changes `cols`/`rows` on its own.
- When the stream closes, `loom-agentd` starts its 60 s grace timer for that session
  (same semantics as Part 1's `ScheduleKill`).

**Error codes (stream status on close).**
- `NOT_FOUND` — session doesn't exist and `command_hint` wasn't enough to synthesize one.
- `RESOURCE_EXHAUSTED` — per-VM session cap reached.
- `UNAVAILABLE` — agent is shutting down or mid-snapshot.
- `PERMISSION_DENIED` — mTLS cert doesn't scope to this workspace/agent.

### `Kill(KillRequest)` — unary

```proto
message KillRequest  { string session = 1; bool force = 2; }
message KillResponse { bool   killed  = 1; }
```

- `force = false` (default): hard-kill if no WS attached, otherwise mark for kill on next detach.
- `force = true`: immediate hard-kill regardless of attached WS.

### `List(ListRequest)` — unary

```proto
message ListRequest  {}
message ListResponse { repeated SessionInfo sessions = 1; }

message SessionInfo {
  string session        = 1;
  bool   attached       = 2;
  uint32 attached_count = 3;
  google.protobuf.Timestamp last_output = 4;
  uint32 cols           = 5;
  uint32 rows           = 6;
}
```

Read-only; used by the control plane for diagnostics and idle-snapshot decisions.

### `Quiesce(QuiesceRequest)` — unary

```proto
message QuiesceRequest  { string session = 1; }
message QuiesceResponse { bool   quiesced = 1; }  // true if state changed; false if already quiesced

message ResumeRequest   { string session = 1; }
message ResumeResponse  { bool   resumed  = 1; }  // true if state changed; false if not quiesced
```

**Semantics.**
- `Quiesce(session)` flushes the session's scrollback ring to disk and halts further ring writes
  for that session. Called by `loom-control-plane` before `vm-host.SaveSnapshot` so the snapshot
  captures a consistent on-disk scrollback. The PTY continues to accept output from the child;
  those bytes are buffered in kernel PTY buffers and may be read after `Resume`, or lost if the
  shell generates more than kernel buffer size (bounded — typical limit ~4KB). For long-quiesce
  scenarios the caller is expected to bound quiesce windows accordingly.
- `Quiesce` returns only after the flush is durable; the response is the synchronous signal that
  the session is snapshot-safe.
- `Resume(session)` re-enables ring writes for a session previously quiesced. Called by
  `loom-control-plane` after a successful `vm-host.ResumeFromSnapshot`. Any PTY output produced
  during the quiesce window that is still in kernel buffers will drain into the ring on the next
  read cycle.
- Both RPCs are idempotent: `Quiesce` on an already-quiesced session returns OK with
  `quiesced = false`; `Resume` on a non-quiesced session returns OK with `resumed = false`.
- These RPCs are session-scoped and share the same mTLS-authenticated channel as `Attach` / `Kill`
  / `List`. The `loom-control-plane` is the expected caller; `loom-webui` has no use for them and
  should not invoke them in normal operation.

**Error codes.**
- `NOT_FOUND` — session does not exist on this agent.
- `FAILED_PRECONDITION` — scrollback flush target is unwritable (disk full, state dir missing).
  `Resume` does not currently raise this code, but callers should be prepared for it should a
  future implementation fail to re-arm ring writes.
- `UNAVAILABLE` — agent is shutting down.
- `PERMISSION_DENIED` — mTLS cert doesn't scope to this workspace/agent.

### `Resume(ResumeRequest)` — unary

See the `Quiesce` section above for the request/response protos and semantics; `Resume` is the
counterpart RPC and is documented together with `Quiesce` for sequence clarity.

## 2. `loom-webui` → `loom-control-plane`

For routing lookups and "wake this agent up."

```proto
service ControlPlane {
  rpc ResolveAgent(ResolveRequest) returns (ResolveResponse);
  rpc EnsureAlive(EnsureRequest)  returns (EnsureResponse);
  rpc ReleaseAttach(ReleaseRequest) returns (ReleaseResponse);
}

message ResolveRequest  { string workspace = 1; string agent = 2; }
message ResolveResponse {
  AgentKind kind       = 1;                      // ephemeral | persistent
  string    vm_host    = 2;                      // set for persistent agents
  string    vsock_cid  = 3;                      // or tcp addr for the agentd endpoint
  int32     agentd_port = 4;
  string    mtls_cert_pem = 5;                   // short-lived cert for this webui ↔ agentd
}

enum AgentKind { AGENT_UNKNOWN = 0; AGENT_EPHEMERAL = 1; AGENT_PERSISTENT = 2; }

message EnsureRequest  { string workspace = 1; string agent = 2; }
message EnsureResponse {
  AgentStatus status = 1;
  ResolveResponse routing = 2;  // populated once status == READY
}

enum AgentStatus { STATUS_UNKNOWN = 0; PROVISIONING = 1; WARMING = 2; READY = 3; FAILED = 4; }

message ReleaseRequest  { string workspace = 1; string agent = 2; }
message ReleaseResponse {}
```

**Semantics.**
- `ResolveAgent` is cheap — backed by Redis cache; returns `NOT_FOUND` if the agent has no routing
  entry.
- `EnsureAlive` is the only RPC that may block for seconds. It triggers VM scheduling and snapshot
  restore if needed. The control plane streams progress via status-poll if desired.
- `ReleaseAttach` informs the control plane that `loom-webui` is done with its attach; used for
  idle-snapshot bookkeeping (not strictly required for correctness).

**Caching on `loom-webui`.** `ResolveResponse` is cacheable for `cache_ttl` seconds (control plane
chooses; default 30 s). On `UNAVAILABLE` from `loom-agentd` the webui pod should evict the cache
entry and call `EnsureAlive` again.

## 3. `loom-control-plane` → `loom-vm-host`

Lifecycle control.

```proto
service VMHost {
  rpc StartVM(StartRequest) returns (StartResponse);
  rpc ResumeFromSnapshot(ResumeRequest) returns (ResumeResponse);
  rpc PauseVM(PauseRequest) returns (PauseResponse);
  rpc SaveSnapshot(SaveRequest) returns (SaveResponse);
  rpc KillVM(KillVMRequest) returns (KillVMResponse);
  rpc NodeStatus(NodeStatusRequest) returns (NodeStatusResponse);
}

message StartRequest {
  string agent       = 1;
  string rootfs_url  = 2;   // s3://bucket/rootfs/{agent}/base.ext4
  uint32 vcpus       = 3;
  uint32 mem_mb      = 4;
  map<string, string> labels = 5;
}
message StartResponse {
  string vm_id      = 1;    // host-local VM id
  string vsock_cid  = 2;    // or tcp addr
  int32  agentd_port = 3;
  AgentStatus status = 4;   // PROVISIONING / READY
}

message ResumeRequest {
  string agent        = 1;
  string snapshot_url = 2;  // s3://bucket/snapshots/{agent}/{snapshot_id}/
  string rootfs_url   = 3;
}
message ResumeResponse {
  string vm_id = 1;
  string vsock_cid = 2;
  int32  agentd_port = 3;
}

message PauseRequest  { string vm_id = 1; }
message PauseResponse {}

message SaveRequest  { string vm_id = 1; string dest_url = 2; /* s3:// */ }
message SaveResponse { string snapshot_id = 1; int64 size_bytes = 2; }

message KillVMRequest  { string vm_id = 1; }
message KillVMResponse {}

message NodeStatusRequest  {}
message NodeStatusResponse {
  uint32 free_mem_mb = 1;
  uint32 free_vcpus  = 2;
  uint32 running_vms = 3;
}
```

**Semantics.**
- `StartVM` pulls rootfs to local NVMe (cache-aware), boots Firecracker, waits for `loom-agentd`
  heartbeat, returns.
- `ResumeFromSnapshot` downloads snapshot (NVMe cache hit = fast path), restores VM, returns when
  agentd reachable.
- `SaveSnapshot` pauses → createSnapshot → uploads to `dest_url` → checksum → response. VM stays
  paused; caller decides whether to resume or kill.
- `KillVM` hard-stops Firecracker, discards local NVMe caches for that VM.
- `NodeStatus` reports capacity; used by the control plane's scheduler.

## 4. `loom-agentd` → `loom-control-plane` (heartbeat)

```proto
service AgentHeartbeat {
  rpc Heartbeat(stream HeartbeatMsg) returns (stream HeartbeatAck);
}

message HeartbeatMsg {
  string agent          = 1;
  google.protobuf.Timestamp ts = 2;
  uint32 session_count  = 3;
  uint32 attached_count = 4;
  google.protobuf.Timestamp last_user_activity = 5;
  float  load_avg_1m    = 6;
}

message HeartbeatAck {
  enum Directive { NOOP = 0; REQUEST_SNAPSHOT = 1; REQUEST_SHUTDOWN = 2; }
  Directive directive = 1;
}
```

**Semantics.**
- Long-lived stream, keep-alive every 15 s.
- Control plane uses `last_user_activity` and `attached_count == 0` to decide when to send
  `REQUEST_SNAPSHOT` for idle suspension.
- `REQUEST_SHUTDOWN` gives the agent N seconds to quiesce before the vm-host sends `KillVM`.

**Relationship to `Terminal.Quiesce` (§1).** When the control plane decides to snapshot, it sends
`HeartbeatAck{directive: REQUEST_SNAPSHOT}` as an early-warning signal. Agentd is not required to
act on this directive; the authoritative trigger is the subsequent `Terminal.Quiesce(session)`
call per session. The `REQUEST_SNAPSHOT` directive exists so agentd can log, or in future
prepare, before the blocking `Quiesce` call arrives. The full snapshot sequence from the control
plane's perspective is therefore:

1. Control plane sends `HeartbeatAck{REQUEST_SNAPSHOT}` on the heartbeat stream (advisory hint).
2. Control plane calls `Terminal.Quiesce(session)` for each session on the agent (authoritative;
   blocks until the scrollback flush is durable).
3. Control plane calls `vm-host.SaveSnapshot`.
4. Control plane calls `vm-host.ResumeFromSnapshot` (on restore).
5. Control plane calls `Terminal.Resume(session)` for each session (re-enables ring writes).

Agents MUST NOT treat `REQUEST_SNAPSHOT` as a command to quiesce themselves — quiescing is
always driven by the explicit `Quiesce` RPC so that the control plane owns the ordering between
flush completion and `SaveSnapshot`.

## 5. Redis key schema

All keys namespaced with `loom:`.

| Key | Type | TTL | Written by | Read by | Notes |
|---|---|---|---|---|---|
| `loom:agent:{agent_id}` | hash | none | control-plane | control-plane | canonical agent record: kind (ephemeral/persistent), rootfs url, size, labels |
| `loom:term:owner:{ws}:{agent}` | string | 60 s, refreshed on heartbeat | control-plane | loom-webui | `vm_host:port` of the agentd owning this agent; absence ⇒ agent cold |
| `loom:term:session:{ws}:{agent}:{session}` | hash | 60 s, refreshed on attach | loom-webui | loom-webui | metadata: last attach time, attach count, cols/rows; not load-bearing for correctness |
| `loom:term:snapshot:{agent}` | string | none | control-plane | control-plane | latest snapshot id/url |
| `loom:vm:host:{host_id}` | hash | 30 s, refreshed by vm-host | vm-host | control-plane | node capacity and health |
| `loom:tabmeta:{ws}:{session}` | hash | none | loom-webui | loom-webui | existing — labels, notes, pinned, issue link |

TTL on `term:owner` intentionally exceeds the PTY detach grace (60 s) so a reconnecting client
always lands on the same owner. Longer if snapshot policies are relaxed.

## 6. Protocols and auth across boundaries

| Boundary | Protocol | Auth |
|---|---|---|
| browser ↔ loom-webui | HTTPS + WSS | user session (existing cookie / JWT) |
| loom-webui ↔ loom-control-plane | gRPC | service mTLS, cluster root CA |
| loom-webui ↔ loom-agentd | gRPC over vsock (tunnelled through loom-vm-host) or TCP | short-lived mTLS cert issued by control-plane, scoped to `(workspace, agent)` |
| loom-control-plane ↔ loom-vm-host | gRPC | service mTLS, cluster-scoped |
| loom-vm-host ↔ Firecracker | Firecracker API (local UNIX socket) | local file perms only |
| loom-agentd ↔ loom-control-plane (heartbeat) | gRPC (long-lived stream) | agent mTLS cert baked into rootfs or injected at boot |

Short-lived certs for `webui ↔ agentd` prevent a compromised webui pod from talking to agents
outside its scope — the cert's Common Name includes `(workspace, agent)` and `loom-agentd` rejects
requests whose `AttachOpen.session` falls outside the cert's scope.

## 7. Versioning & compatibility

- Protobuf packages versioned `v1`, e.g. `loom.terminal.v1`. Breaking changes require a new
  package.
- `loom-agentd` baked into rootfs images — control plane tracks rootfs version and refuses to
  resume snapshots taken against incompatible rootfs versions.
- `loom-webui` may run mixed with `loom-agentd` versions that are N-1 through N+1; outside that
  band the webui returns a protocol-mismatch error to the browser and shows a "please reload"
  banner.

## 8. What `loomcli` does *not* need in Part 1

None of the above contracts exist yet. In Part 1, `loom serve`:
- Does not import protobuf.
- Does not talk to any new service.
- Does not know about Firecracker, snapshots, or vsock.

The only Part-1 surface that becomes relevant later is the `PTYSource` Go interface
(see [part1-detach-pty-from-ws.md](./part1-detach-pty-from-ws.md) §Option C). Part 2 adds a second
implementation of that interface backed by the `Attach` RPC above.
