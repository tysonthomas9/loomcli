# Disposable test backend

A dockerized fleet-db + Redis for tests that need a real control plane, isolated
from anything that matters.

```bash
make test-env-up                      # or: scripts/test-env.sh up
eval "$(scripts/test-env.sh env)"     # LOOM_FLEET_DB_URL / LOOM_WORKSPACE / actor
go test ./... -run TestThatNeedsFleetDB
make test-env-down
```

## Why

The deployed stack on `:3011` holds the live workspace: real issues, real
agent definitions, the board people look at. A test that creates a probe issue,
flips an agentdef's `task_filter`, or closes a task there is writing to
production data, and cleaning up afterwards is best-effort — `loom data` has no
issue delete, so probes are permanent.

This stack removes the temptation. It is disposable by construction: Redis runs
with no persistence, and `down` takes the volumes with it.

## What keeps it separate

| | |
|---|---|
| workspace | `LOOMTEST` — distinct from the live workspace, `FLEETDB` (`test/fleetdb`), `LOCALMODE` (`test/local-mode`) |
| port | `127.0.0.1:53351` — clear of `3009/3011/3012/3013`, `8080/8082/8083`, `8280/8282/8283` |
| state | ephemeral Redis; every `up` is a clean slate |
| binding | localhost only |

The distinct workspace key is the important one: a client that picks up the URL
but not `LOOM_WORKSPACE` falls back to ambient config, which is how a test ends
up writing to the live workspace. `scripts/test-env.sh env` always emits both,
and unsets `LOOM_SERVER_URL` so a stale value cannot split the backends.

## Commands

| | |
|---|---|
| `up` | build, start, wait for `/healthz`, create the workspace (idempotent) |
| `env` | print the exports — `eval` it |
| `status` | container state, health, issue count |
| `logs` | follow fleet-db logs |
| `reset` | wipe the data, keep the stack |
| `down` | stop and drop volumes |

`reset` recreates the stack rather than deleting the workspace, because there is
no API path to an empty workspace: fleet-db returns `409 workspace contains
issues`, and issues have no delete endpoint to empty it with first. It asserts
the result is empty rather than assuming it.

## Relation to the other stacks

`test/fleetdb/` (regression, browser suite) and `test/local-mode/` (dogfood demo
with agents and a seeded epic) are demo/verification stacks — they seed data and
re-seed agents on rebuild. Use them for what they are for. This one is the plain
backend to point a test at.

fleet-db is built from the sibling checkout (`../../../fleet-db`), matching the
other stacks, so a local fleet-db change is testable before it is pushed.

Modelled on orche's `fleet-db.docker-compose.yml` + `scripts/fleet-db-docker.sh`,
which pair a dockerized backend on a dedicated port with a dedicated
`ORCHE-TEST` workspace.
