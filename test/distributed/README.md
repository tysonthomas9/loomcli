# Distributed Fleet-Db Smoke

Run from the loomcli repo root:

```bash
make test-distributed-smoke
```

This stack runs without beads. It starts shared Redis and fleet-db, two
`loom serve` processes in fleet mode, two lightweight local supervisor heartbeat
loops, two WebUI proxy sidecars, and a one-shot smoke runner.

The Make target builds static `loom` and `fleet-db` binaries into
`tmp/distributed-smoke/bin` and mounts them into small runtime containers. Set
`FLEET_DB_REPO=/path/to/fleet-db` if the fleet-db repo is not available at
`../../fleet-db` from the loomcli repo root.

The smoke runner reports:

- auth/audit: issue creation through fleet-db records the authenticated actor
- claims: two supervisor actors contend for one issue and exactly one wins
- SSE reconnect: a cursor from fleet-db is reused against the second loom server
  and catches up the gap mutation
- WebUI health: both UI proxy paths and loom health endpoints respond

Host ports are `8090` for fleet-db, `8091`/`8092` for raw loom servers, and
`8093`/`8094` for the WebUI proxy sidecars.
