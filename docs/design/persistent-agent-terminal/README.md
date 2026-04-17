# Persistent-agent terminal architecture

Design for the terminal stack in loomcli across three deployment tiers:

1. **Today / ephemeral agents** — `loom serve` runs on one node; terminals die with the process.
2. **Part 1 (near-term)** — detach PTY lifetime from WebSocket lifetime so refresh / network blips don't kill the shell. Single-binary, no new services.
3. **Part 2 (persistent agents)** — persistent agents run inside Firecracker microVMs with a small `loom-agentd` service; `loom serve` dispatches between local (ephemeral) and remote (persistent) via the same interface.

## Documents

| File | Scope |
|---|---|
| [`architecture.md`](./architecture.md) | Current state, Part 1 delta, Part 2 end state, diagrams |
| [`part1-detach-pty-from-ws.md`](./part1-detach-pty-from-ws.md) | Part 1: three architecture options, recommendation, nine design knobs |
| [`contracts.md`](./contracts.md) | RPC contracts between `loom serve`, `loom-control-plane`, `loom-vm-host`, `loom-agentd` |
| [`storage.md`](./storage.md) | Snapshot + rootfs tiering: local NVMe + S3-compatible object storage |

## TL;DR for reviewers

- **Part 1** is a ~150-line change to `handlers/terminal/ws.go` plus a new `SessionKey` type and a `PTYSource` interface satisfied by the existing `TerminalManager`. Zero new services. Lands first.
- **Part 2** introduces three new repos (`loom-control-plane`, `loom-vm-host`, `loom-agentd`) and keeps `loomcli`'s footprint tiny — only adds `AgentdClient` as a second `PTYSource` implementation. `loom serve` never sees Firecracker.
- **Snapshots** live in S3-compatible storage with local NVMe as a pull-through cache. Host VMs are stateless and never snapshotted.
- **Scrollback** is file-backed, in the shell's own pod/VM — never Redis.
- **Routing** (which pod owns which session) is a Redis entry keyed by `(workspace, agent, session)`, written by the control plane, read by the edge.
