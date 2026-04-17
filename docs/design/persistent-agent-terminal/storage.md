# Storage architecture

Only microVMs are stateful. Host VMs and `loom-webui` pods are stateless. Durable state splits
three ways:

1. **Snapshot + rootfs blobs** — S3-compatible object storage, with local NVMe as a pull-through
   cache on each `loom-vm-host` node.
2. **Routing / metadata** — Redis (already in use for tabmeta).
3. **Scrollback files** — inside the microVM's own filesystem (PV-backed), on the same disk as
   the agent's workspace files. Never Redis.

## 0. What lives where (summary)

| Data | Where | Why |
|---|---|---|
| microVM snapshot blobs | **S3** (durable) + NVMe (pull-through cache) | must survive host loss; large; read rarely |
| microVM rootfs base image | **S3** + NVMe cache | shared across agents, versioned, immutable |
| microVM rootfs deltas (per-agent writes) | **S3** + NVMe cache, or block storage (EBS / RBD / Longhorn) | user-editable state; fsync semantics matter — block storage optional |
| Scrollback files (per session) | **inside the microVM's rootfs** | colocated with the shell that writes them; snapshotted automatically |
| Tab metadata (labels, notes, pins) | **Redis** | small, cross-pod reads, already in use |
| Terminal routing owner keys | **Redis** (TTL) | small, cross-pod reads + writes, fleet coordination |
| Auth tokens | **Redis** (short TTL) | small, short-lived |
| Host VM image | **image registry** (Packer / cloud AMI) | built offline, immutable |
| Host VM runtime state | — | stateless; hosts are replaceable cattle |

## 1. Snapshot storage tiers

```
 ┌───────────────────────────────────────────────────────────────────────┐
 │                 loom-vm-host node  (any host that might run an agent) │
 │                                                                       │
 │   ┌──────────────────────────────────────────────────────────────┐    │
 │   │  Local NVMe cache  (~/var/lib/loom-vm-host/snapshots/)       │    │
 │   │                                                              │    │
 │   │   {agent-id}/                                                │    │
 │   │     rootfs.ext4              ← overlay or full image         │    │
 │   │     snapshot.mem             ← mmap'd by Firecracker on      │    │
 │   │     snapshot.state              restore; fast path           │    │
 │   │                                                              │    │
 │   │  Restore time: ~100–300 ms for a 1–2 GB VM                   │    │
 │   └──────────────────────┬───────────────────────────────────────┘    │
 │                          ▲                                            │
 │                          │ pull-through cache                         │
 │                          │ (control-plane-initiated fetch             │
 │                          │  on scheduling decision)                   │
 └──────────────────────────┼────────────────────────────────────────────┘
                            │
                            ▼
 ┌───────────────────────────────────────────────────────────────────────┐
 │   S3-compatible object storage  (S3 / GCS / R2 / MinIO / Ceph-RGW)    │
 │   repo glue: loom-snapshots  (thin client lib)                        │
 │                                                                       │
 │    agents/{agent-id}/                                                 │
 │      rootfs/base.ext4                ← rarely changes; shared layer   │
 │      rootfs/delta-{gen}.qcow2         ← copy-on-write deltas          │
 │      snapshots/{snapshot-id}/state    ← Firecracker device state      │
 │      snapshots/{snapshot-id}/mem      ← Firecracker guest memory      │
 │      snapshots/latest → {snapshot-id} ← pointer object                │
 │                                                                       │
 │   Durability: whatever your bucket gives you (S3: 11 nines).          │
 │   Lifecycle rules: keep last N snapshots, age others to cheaper tier. │
 │   Encryption: SSE-KMS or similar. Keys per-tenant if needed.          │
 └───────────────────────────────────────────────────────────────────────┘
```

### Save flow

```
  control-plane  →  vm-host: CreateSnapshot(agent, dest=s3://…)
                    vm-host → Firecracker API: pause + createSnapshot
                                  ↓
                        writes to local NVMe
                                  ↓
                    vm-host: upload NVMe snapshot → S3 (async, checksummed)
                                  ↓
                    vm-host: report snapshot URL + size to control-plane
                    control-plane: Redis: loom:term:snapshot:{agent} = URL
                                    lifecycle: evict older snapshots per policy
```

### Restore flow

```
  control-plane: pick a host (least-loaded, with spare RAM)
        │
        ▼
  vm-host on that host:
     NVMe cache hit?  ─ yes ─► restore from local disk         (~200 ms)
                      │
                      └─ no ──► stream snapshot from S3        (~1–5 s depending on size)
                                 + verify checksum
                                 + warm page cache while downloading
                                 → Firecracker restore
```

Hot-path reattach (same host, VM in NVMe): sub-second.
Cold reattach (different host, S3 pull): a few seconds for a 1 GB VM on a 10 Gb network.

### Snapshot retention

Pyramid; control-plane policy, configurable per-agent or per-workspace:

- `latest`: always kept, updated on every save.
- `hourly`: last 4–6.
- `daily`: last 7.
- Older: lifecycle rule moves to infrequent-access tier or deletes.

This lets a user roll back after `rm -rf`-ing their own workspace without unbounded cost.

## 2. Rootfs options

Two viable approaches. Pick per deployment, not per loom release.

### S3 + NVMe cache (default)

- `base.ext4` is an immutable image shared across agents (versioned by rootfs gen).
- Per-agent writes go to a qcow2 delta stored in S3.
- `loom-vm-host` downloads base + delta to local NVMe before boot.
- Simple, durable, but fsync inside the VM doesn't immediately hit S3; requires periodic
  delta uploads.

### Block storage (opt-in)

- `base.ext4` + per-agent delta on a remote block device (EBS, Ceph RBD, Longhorn, Portworx).
- Attached to the host VM, passed through to Firecracker as a virtio-blk.
- Fsync inside the VM is synchronous + durable per-write.
- More complex plumbing and cost; required for agents doing database-grade work.

Default recommendation: S3 for rootfs + snapshots. Upgrade to block storage later for
agents that need strict fsync durability.

## 3. Scrollback files — why files, not Redis

Reasons scrollback lives in a file inside the microVM rather than Redis (or any central store):

- **Volume pattern.** Shell output is a high-rate append-only byte stream. Every chunk via Redis
  = network RTT + serialization + RAM allocation. Redis is engineered for small-value low-latency
  lookups, not log ingestion. A chatty build log is megabytes; a `tail -f` is unbounded.
- **Access pattern.** Scrollback is read exactly once per reattach, from exactly one reader.
  Zero need for cross-process coordination.
- **Lifecycle.** Scrollback belongs with the shell. Shell runs on host X → scrollback lives on
  host X. Putting it in a separate service adds a round trip with no benefit.
- **Colocation with snapshots.** When the VM is snapshotted, the scrollback file is part of the
  rootfs that gets snapshotted. Resume → scrollback picks up exactly where it was. No separate
  sync protocol needed.
- **Replay is a file seek.** No network hop on reattach.
- **Debuggable.** `tail -f ~/.loom/sessions/lead-claude-1.log` just works. Postmortem on a
  crashed agent: read the file.

Redis **would** be right only if the webui pod wanted to read scrollback without proxying
through the shell host. We explicitly do not do that, because:
- it creates a permissions problem (webui reads every agent's scrollback directly);
- it creates a consistency problem (Redis and shell-host fd output can drift);
- it creates a capacity problem (Redis doesn't love GBs of log bytes).

All three are avoided by routing scrollback reads through the same `Attach` RPC the PTY uses.

### Layout

Per-session scrollback lives **inside the microVM**, on the agent's PV/rootfs:

```
/home/agent/sessions/{session}/
    scrollback.log           # ring-buffered, ~256 KB
    cwd                      # one-liner snapshot of shell pwd
    meta.json                # cols/rows/env hint for next respawn
```

Reasons to keep this out of Redis or S3:

- Scrollback writes are high-volume, append-only byte streams. Every write = network RTT if
  external. Local fd writes are disk-bound.
- Scrollback is colocated with the shell: when the agent is paused-snapshotted, the scrollback
  file is inside the snapshotted rootfs. When the agent is resumed, scrollback picks up exactly
  where it was. No external sync needed.
- Replay is a file seek, no network hop.
- Debuggable: `tail -f` works.
- Trivial ring buffer implementation (truncate + rewrite, or rotate `log.0`/`log.1`, or byte-cap).

**Size policy.** 256 KB per session (~2000 lines at 128 B/line). A persistent agent with 20
live sessions = ~5 MB of scrollback. Small relative to typical agent RAM footprints.

**Eviction.** Session deleted → scrollback file deleted. Never leaks.

## 4. Redis (routing + metadata only)

See [contracts.md §5](./contracts.md#5-redis-key-schema) for the full schema.

Redis is the right tool for:
- Tab metadata (`loom:tabmeta:*`) — small records, cross-pod reads, already in use.
- Routing owner keys (`loom:term:owner:*`) — small, TTL semantics useful, edge + webui both read.
- Agent records (`loom:agent:*`) — small, frequently read.

Redis is the wrong tool for:
- Scrollback byte streams.
- Snapshot blobs.
- Rootfs images.

## 5. Encryption

- Snapshots contain guest RAM, which includes user memory and credentials. Encrypt at rest
  (bucket-level SSE-KMS is fine for most deployments).
- Per-tenant crypto isolation: use per-agent data keys wrapped by a KMS master key. Firecracker
  doesn't care about bytes on disk; `loom-vm-host` does the crypt/decrypt when reading/writing
  NVMe and S3.
- Redis: use TLS to the Redis endpoint. Tokens and tabmeta are small and frequently changing;
  AOF-level encryption is usually unnecessary if the Redis host itself is in a trusted network.
- PV-backed rootfs: encrypted at the block layer (LUKS on the PV, or cloud-provider default
  encryption).

## 6. What `loom serve` touches

- Reads/writes Redis (already does for tabmeta; add routing owner reads in Part 2).
- Does **not** read or write snapshot blobs.
- Does **not** read or write rootfs files.
- Does **not** touch scrollback files (those live in the VM; replayed to loom-webui over the
  `Attach` RPC in Part 2).

Part 1 keeps scrollback in-process memory (existing `ScrollbackBuffer`) — no disk writes, no
new storage dependency. Disk/file-backed scrollback only appears in `loom-agentd` under Part 2.
