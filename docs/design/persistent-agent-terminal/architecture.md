# Architecture: current, Part 1, Part 2

## 1. Today — one `loom serve` process

Everything is in-tree in `loomcli`. One binary, one process:

```
 ┌────────────────────────────────────────────────────────────────────────────┐
 │                           Browser (wterm)                                  │
 └────────────────────────────────────┬───────────────────────────────────────┘
                                      │ HTTPS / WSS
                                      ▼
 ┌────────────────────────────────────────────────────────────────────────────┐
 │                            loom serve  (one process)                       │
 │                         cmd: internal/cli/serve/serve.go                   │
 │                                                                            │
 │   HTTP / WS routes                                                         │
 │     internal/webui/handlers/terminal/ws.go         ← WS upgrade            │
 │     internal/webui/handlers/terminal/agent.go      ← agent WS              │
 │     internal/webui/handlers/terminal/sessions.go   ← kill + list           │
 │     internal/webui/handlers/misc/config.go         ← config API            │
 │                                                                            │
 │   TerminalManager (in-process, tmux-backed)                                │
 │     internal/webui/terminal/manager.go                                     │
 │     internal/webui/terminal/lifecycle.go  (has ScheduleKill already)       │
 │     internal/webui/terminal/scrollback.go                                  │
 │     internal/webui/terminal/tmux.go / spawn.go                             │
 │                                                                            │
 │   Redis clients: tabmeta, sessionhistory, fleet (task-claim only)          │
 │                                                                            │
 └────────────────────────────────────┬───────────────────────────────────────┘
                                      ▼
                                  ┌───────┐
                                  │ Redis │
                                  └───────┘
```

Key coupling to break in Part 1: `handlers/terminal/ws.go:runTerminalRelay` calls
`p.manager.Detach(connID)` the instant `WSToPTY` returns. `Detach` closes the tmux
session's PTY and kills the child. There is no grace period.

## 2. Part 1 — detach PTY lifetime from WS lifetime

Everything stays inside `loom serve`. The delta is localized.

### Code changes

```
loom serve  (still one process)                                      ← unchanged
│
├─ HTTP / WS routes
│    handlers/terminal/ws.go                                         ← rewritten
│       runTerminalRelay:
│         - two contexts: ptyCtx (long-lived) vs wsCtx (per-WS)      ← NEW
│         - Detach no longer kills; it calls ScheduleKill(60s)       ← NEW
│         - replays scrollback on each attach (reset + ring)         ← NEW
│
├─ TerminalManager                                                   ← small diffs
│    terminal/manager.go       (+ PTYSource interface, SessionKey)
│    terminal/scrollback.go    (byte cap instead of line cap, ~256 KB)
│    terminal/lifecycle.go     (existing ScheduleKill; + idle-reap check)
│
└─ New file
     terminal/key.go           (SessionKey value type)
```

### Diagram (Part 1 end state)

Same as today, with annotations showing which functions changed:

```
 ┌────────────────────────────────────────────────────────────────────────────┐
 │                           Browser (wterm)                                  │
 │    On first attach OR reattach: expects an optional                        │
 │    \x1b[2J\x1b[H + scrollback replay before live stream.                   │
 └────────────────────────────────────┬───────────────────────────────────────┘
                                      │ WSS
                                      ▼
 ┌────────────────────────────────────────────────────────────────────────────┐
 │                            loom serve                                      │
 │                                                                            │
 │  runTerminalRelay(ws, session, workspace):                                 │
 │    1. key := SessionKey{workspace, session}                                │
 │    2. sess := ptySource.Attach(key, ...)                                   │
 │       └─ if existing session: CancelPendingKill, reuse tmux session        │
 │    3. replay := reset + scrollback.Bytes()                                 │
 │       conn.Write(replay)                                                   │
 │    4. ptyCtx, ptyCancel := context.WithCancel(Background())  ← KEY        │
 │       wsCtx, wsCancel   := context.WithCancel(reqCtx)                      │
 │    5. go PtyToWS(ptyCtx, ...)   ← drains PTY to ring + WS                 │
 │       WSToPTY(wsCtx, ...)       ← blocks until WS closes                  │
 │    6. ptySource.Detach(connID)  ← ONLY closes THIS WS's PTY attachment     │
 │       ptySource.ScheduleKill(key, 60s)  ← kill in 60s unless reconnect     │
 │                                                                            │
 │  Idle-reap tick (every 60s):                                               │
 │    for each session with no WS AND no output in 30 min:                    │
 │      killByInternal(key)                                                   │
 └────────────────────────────────────┬───────────────────────────────────────┘
                                      ▼
                                  ┌───────┐
                                  │ Redis │
                                  └───────┘
```

### State machine (one `TerminalSession`)

```
              Attach(key)
                  │
                  ▼
          ┌────────────────┐
   ┌──────►    ATTACHED    │    (one or more WS connections)
   │      │  relay running │
   │      └────────┬───────┘
   │               │  last WS disconnects → Detach + ScheduleKill(60s)
   │               ▼
   │      ┌────────────────┐
   │      │    DETACHED    │    drain PTY → ring, no WS out
   │      │  grace armed   │
   │      └──┬──────────┬──┘
   │         │          │
   │   new WS Attach    │  grace expires, or idle reaper (30 min),
   │   CancelKill       │  or explicit DELETE
   │         │          │
   └─────────┘          ▼
                ┌────────────────┐
                │     DEAD       │   tmux killed, ring discarded
                └────────────────┘
```

### Nothing else changes

- Web UI, auth, tab metadata, agent-tmux path, fleet task-claim, session listing API — all unchanged.
- Test layout unchanged; new tests added under `terminal/` and `handlers/terminal/`.

## 3. Part 2 — persistent agents in Firecracker

### Persistent vs ephemeral agents

Two classes of agent with different lifecycles and different terminal guarantees:

| | **Persistent agent** | **Ephemeral agent** |
|---|---|---|
| Lifecycle | long-lived; owns its own state dir / repo / env | spun up for a task, torn down when done |
| Terminal expectation | survives web-pod restart, network blips, page refresh | exists only while the agent pod is alive |
| State survival | shell process + scrollback + cwd + running jobs | ephemeral, same as today |
| Who owns the shell | `loom-agentd` inside a Firecracker VM (separate lifecycle from the webui pod) | the webui pod (ephemeral) |
| Snapshot-able | yes — Firecracker memory + device snapshot | no |
| `PTYSource` impl | `AgentdClient` (gRPC-over-vsock) | `LocalPTYHost` (in-process `TerminalManager`) |

The Part 1 work covers the **ephemeral** column end-to-end (WS-reconnect durability within pod
lifetime). Part 2 adds the **persistent** column — a different code path that points *out* of
`loom serve`.

### Topology

```
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                                  BROWSER                                     │
 │                          wterm + React client                                │
 │                            ( repo: loomcli )                                 │
 └────────────────────────────────────┬─────────────────────────────────────────┘
                                      │  HTTPS + WSS
                                      ▼
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                        Edge / LB (NGINX • Envoy)                             │
 │                            (not a loom repo)                                 │
 │                   Routes by workspace → owning loom-webui pod.               │
 └────────────────────────────────────┬─────────────────────────────────────────┘
                                      │
                                      ▼
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                     loom-webui pods  (N replicas)                            │
 │                            repo: loomcli                                     │
 │  • HTTP/REST + WS upgrade + auth                                             │
 │  • Tab state (Redis)                                                         │
 │  • PTYSource interface dispatch:                                             │
 │        ephemeral agent → LocalPTYHost (wraps existing TerminalManager)       │
 │        persistent agent → AgentdClient (gRPC → loom-agentd in the VM)        │
 └──────────────┬─────────────────────────────┬──────────────────────────┬──────┘
                │ Redis                       │ gRPC (dataplane via vm-host)
                │                             │                          │
       ┌────────▼────────┐           ┌────────▼─────────────┐            │
       │     Redis       │           │   loom-control-      │            │
       │  • tabmeta      │◄──────────┤   plane pods         │            │
       │  • term routing │           │  repo: loom-control  │            │
       │    (ws, session)│           │                      │            │
       │    → vm-host    │           │  • agent lifecycle   │            │
       │  • agent kinds  │           │  • VM scheduling     │            │
       │    & addresses  │           │  • snapshot policy   │            │
       └─────────────────┘           └────────┬─────────────┘            │
                                              │ gRPC (control plane)     │
                                              ▼                          │
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                          loom-vm-host  node agent                            │
 │                          repo: loom-vm-host                                  │
 │   • One DaemonSet per VM-capable node. Wraps Firecracker + jailer.           │
 │   • RPCs: StartVM • ResumeVM • PauseVM • SaveSnapshot • KillVM               │
 │   • vsock / virtio-net bridge; webui pods reach agentd via vm-host proxy.    │
 └─────────────────────────────────────┬────────────────────────────────────────┘
                                       │  spawns / resumes Firecracker VMs
                                       ▼
 ┌──────────────────────────────────────────────────────────────────────────────┐
 │                Firecracker microVM  (one per persistent agent)               │
 │                                                                              │
 │   ┌───────────────────────────────────────────────────────────────────┐      │
 │   │                     loom-agentd  (pid 1-ish)                      │      │
 │   │                     repo: loom-agentd                             │      │
 │   │                                                                   │      │
 │   │   • gRPC server on vsock  (webui pods attach here for terminals)  │      │
 │   │   • Same detach-from-WS semantics as LocalPTYHost                 │      │
 │   │   • Scrollback ring files per session (local PV)                  │      │
 │   │   • Heartbeat → control plane                                     │      │
 │   │   • Honours SaveSnapshot signal  (flush fds, quiesce)             │      │
 │   └──────────────────────────────┬────────────────────────────────────┘      │
 │                                  │                                           │
 │                                  ▼                                           │
 │        /workspace        (PV-backed — survives snapshot)                     │
 │        /home/agent/sessions/{session}/scrollback.log                         │
 │        /home/agent/sessions/{session}/cwd                                    │
 │        user processes: shells, build watchers, language servers…             │
 └──────────────────────────────────────────────────────────────────────────────┘
                                       ▲   ▲
                                       │   │  Firecracker state (RAM, vCPU,
                                       │   │  device) ↔ snapshot blobs
                                       │   ▼
                         ┌──────────────────────────────────┐
                         │  Snapshot + rootfs object store  │
                         │  repo: loom-snapshots (thin lib) │
                         │  • rootfs.ext4 (PV or S3-backed) │
                         │  • snapshot.mem, snapshot.state  │
                         │    on S3 / MinIO / GCS           │
                         └──────────────────────────────────┘
```

### Ephemeral path (for contrast — unchanged from Part 1)

```
 ┌──────────────────┐  WS   ┌──────────────────────────────────┐
 │  loom-webui pod  │──────►│   LocalPTYHost (wraps today's    │
 │   repo: loomcli  │       │   TerminalManager, in-process)   │
 └──────────────────┘       │   Dies with pod — that's OK.     │
                            └──────────────────────────────────┘
```

### Repos and responsibilities

| Repo | Service | Key code | Deploys as |
|---|---|---|---|
| `loomcli` | loom-webui | HTTP, WS upgrade, auth, React UI, `PTYSource` with `LocalPTYHost` and (Part 2) `AgentdClient` | Deployment |
| `loom-control-plane` | control plane | agent registry, VM scheduler, snapshot policy, Redis owner writes | Deployment |
| `loom-vm-host` | per-node VM supervisor | Firecracker + jailer wrapper, vsock bridge, PV mounts, snapshot upload | DaemonSet on VM-capable nodes |
| `loom-agentd` | per-VM agent daemon | `PTYManager`-equivalent with detach grace, scrollback ring files, gRPC on vsock | baked into the agent rootfs image |
| `loom-snapshots` | tiny client lib | snapshot blob layout, upload/download, retention policy | Go library, not a service |

### What `loom serve` does (vs doesn't) in Part 2

|   | Part 1 | Part 2 |
|---|---|---|
| HTTP/WS API | yes | yes |
| Tab metadata | yes | yes |
| Auth | yes | yes |
| Ephemeral PTYs | yes (LocalPTYHost) | yes (LocalPTYHost) |
| Persistent PTYs | — | no — delegates via AgentdClient |
| Scrollback files | local disk | for ephemeral only; persistent scrollback is in the VM |
| VM lifecycle | — | no — delegates to control plane |
| Firecracker calls | — | never |
| Snapshot I/O | — | never |

The persistent-agent code path **never enters `loom serve`'s address space**. PTYs for persistent agents are spawned by `loom-agentd` inside the VM, on the VM's CPU, using the VM's filesystem. `loom serve` only sees gRPC frames.

## 4. Attach sequence (Part 2 happy path)

```
 Browser         loom-webui        Redis         control-plane        vm-host         loom-agentd
   │                │                │                │                 │                 │
   │ WSS attach     │                │                │                 │                 │
   │  ws=W          │                │                │                 │                 │
   │  agent=A       │                │                │                 │                 │
   │  session=S     │                │                │                 │                 │
   ├───────────────►│                │                │                 │                 │
   │                │ GET owner(A)   │                │                 │                 │
   │                ├───────────────►│                │                 │                 │
   │                │  miss          │                │                 │                 │
   │                │◄───────────────┤                │                 │                 │
   │                │ EnsureAlive(A) │                │                 │                 │
   │                ├──────────────────────────────► │                 │                 │
   │                │                │                │ ResumeSnapshot  │                 │
   │                │                │                ├────────────────►│                 │
   │                │                │                │                 │ firecracker     │
   │                │                │                │                 │   restore       │
   │                │                │                │                 ├────────────────►│
   │                │                │                │                 │     (wake)      │
   │                │                │                │ VM ready, addr  │                 │
   │                │                │                │◄────────────────┤                 │
   │                │ route = addr   │                │                 │                 │
   │                │◄───────────────┤────────────────┤                 │                 │
   │                │  write route   │                │                 │                 │
   │                ├───────────────►│                │                 │                 │
   │                │                │                │                 │                 │
   │                │                gRPC Attach(S) over vsock (mTLS)   │                 │
   │                ├────────────────────────────────────────────────── │────────────────►│
   │                │                │                │                 │   open/reattach │
   │                │                │                │                 │   PTY, replay   │
   │                │                │                │                 │   scrollback    │
   │                │ ◄-- stream of output frames --- │                 │                 │
   │ ◄─ WSS frames  │                │                │                 │                 │
   │                │                │                │                 │                 │
   │ -- input --►   │                │                │                 │                 │
   │                │ -- input ---------------------- │---------------- │--- input ---►   │
   │                │                │                │                 │                 │
```

## 5. Detach / snapshot sequence

```
   browser disconnect
        │
        ▼
   loom-webui closes gRPC attach              loom-agentd:
        │                                       • keeps PTY alive (grace)
        │                                       • appends to scrollback file
        ▼
   control-plane sees lastAttach > idleTTL
        │
        ▼
   control-plane → vm-host: SaveSnapshot(A)
        │
        ▼
   vm-host → agentd: quiesce fds
   vm-host → firecracker: CreateSnapshot
        │
        ▼
   snapshot.mem + .state → object storage
   VM halted. Redis routing entry TTL's out.
        │
        ▼
   Next browser attach → EnsureAlive triggers restore (Attach sequence above).
```

Snapshot restore time on NVMe: ~100–300 ms for a 1–2 GB VM. Cold S3 pull adds 1–5 s on a 10 Gb network for a 1 GB snapshot.

## 6. Host VMs are never snapshotted

`loom-vm-host` nodes are stateless replaceables. They run a KVM-enabled kernel, the vm-host daemon, and whatever Firecracker processes are scheduled on them — no user-meaningful state. If one dies:

- Provision a new host from an image (Packer / cloud-native).
- Control plane reschedules any affected microVMs onto other hosts by pulling snapshots from object storage.
- Route traffic to the new host once its daemon heartbeats.

The stateful unit is the microVM. Only microVMs are snapshotted.

## 7. Alternatives considered

### Why Firecracker (and not the other options)

The only way terminal state survives a host / pod restart is if the process tree lives outside the
failure domain you're trying to survive. Ranked options:

| Option | Survives WS blip | Survives pod restart | Complexity | Notes |
|---|---|---|---|---|
| Detach PTY from WS (Part 1) | yes | no | low | what Part 1 is |
| tmux inside the same pod | yes | **no** — pod dies → tmux dies | low | **does not help** for pod restart |
| tmux in a separate pod | yes | only if the tmux pod doesn't restart too | medium | still dies on that pod's eviction |
| CRIU checkpoint of a single container | yes | yes-ish | **high** | painful with TTYs, external sockets, seccomp; fails loudly |
| Kubernetes native checkpoint | yes | yes-ish | high | still alpha (1.30+); no transport story |
| **Firecracker microVM snapshots** | yes | **yes** | medium | snapshot = full VM RAM + CPU + device state; ~100–300 ms restore on NVMe; used at scale by AWS Lambda SnapStart, Fly.io |
| Dedicated VM per agent (Codespaces-style) | yes | yes | high (infra) | strictly more expensive than Firecracker for the same effect |

**Key correction we worked through in design discussion:** tmux does *not* solve pod-restart
durability when it lives inside the pod. The tmux server is a process; the socket is its address.
Killing the pod kills the process. This is why Part 2 uses a separate lifecycle bucket
(Firecracker microVMs) rather than tmux-in-pod.

### Why not CRIU directly in `loom serve`

- CRIU doesn't know which processes matter. Every transient subprocess gets snapshotted.
  Snapshots become enormous and restore becomes flaky.
- Network-socket handling is painful: hand-classifying every external connection is required.
- PTY + seccomp + cgroup edge cases.

Firecracker sidesteps all of these by treating the VM as one atomic unit with a well-defined
outside world (virtio devices). `loom serve` only has to care whether the VM is paused or running.

