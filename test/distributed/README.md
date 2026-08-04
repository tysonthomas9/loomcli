# Distributed Fleet-Db Smoke

> **Status:** Current · *audited 2026-08-03*. Target, ports, and the
> `FLEET_DB_REPO` resolution checked against `Makefile:235-260,475-477` and
> `test/distributed/docker-compose.smoke.yml`.

Run from the loomcli repo root:

```bash
make test-distributed-smoke
```

This stack runs without a sidecar issue backend. It starts shared Redis and fleet-db, two
`loom serve` processes in fleet mode, two lightweight local supervisor heartbeat
loops, two WebUI proxy sidecars, and a one-shot smoke runner.

The Make target builds static `loom` and `fleet-db` binaries into
`tmp/distributed-smoke/bin` and mounts them into small runtime containers
(`Makefile:237-243`).

**A sibling `fleet-db` checkout is required.** `Makefile:475-477` probes
`../fleet-db` first, then `../../fleet-db`, and falls back to `../../fleet-db`
when neither exists — at which point the `go build ./cmd/fleet-db` step fails.
Other harnesses in this repo (`scripts/start-e2e-server.sh:14`,
`deploy/podman-stack/build.sh:28`) hard-code different depths, so set the
variable explicitly:

```bash
FLEET_DB_REPO=/path/to/fleet-db make test-distributed-smoke
```

The smoke runner reports:

- auth/audit: issue creation through fleet-db records the authenticated actor
- claims: two supervisor actors contend for one issue and exactly one wins
- SSE reconnect: a cursor from fleet-db is reused against the second loom server
  and catches up the gap mutation
- WebUI health: both UI proxy paths and loom health endpoints respond

Host ports are `8090` for fleet-db, `8091`/`8092` for raw loom servers, and
`8093`/`8094` for the WebUI proxy sidecars
(`test/distributed/docker-compose.smoke.yml:54,97,140,185,199`). These collide
with `test/fleetdb/`'s defaults — do not run both stacks at once.

## Related

- [`../../docs/testing/README.md`](../../docs/testing/README.md) — index of all
  test surfaces
- [`../../deploy/podman-stack/README.md`](../../deploy/podman-stack/README.md) —
  the fuller distributed-topology stack (auth, connectors, sandbox, workers)
- [`../local-mode/README.md`](../local-mode/README.md) — single-machine dogfood
  stack
- [`../../docs/loom-glossary.md`](../../docs/loom-glossary.md) — **fleet mode**
  (`--fleet-mode`) is independent of whether fleet-db is embedded or remote
