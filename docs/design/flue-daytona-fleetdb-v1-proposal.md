# Flue-Daytona FleetDB TaskRun V1 Proposal — pointer

> **Status:** Not on `main` — the full document lives only on the unmerged
> branch `origin/flue-runtime`. This file exists so the references to it
> from the V2 docs resolve. *audited 2026-07-23*

## Why this file is a stub

`docs/design/fleetdb-agent-platform-v2-proposal.md`,
`docs/design/fleetdb-agent-platform-v2-phased-delivery.md`, and
`docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md`
all list `docs/design/flue-daytona-fleetdb-v1-proposal.md` under "Related".
It was written on 2026-06-03 (1557 lines as of `f18827e57`, "docs: add flue
daytona edge case review") but that branch was never merged, so the path
has never existed on `main`.

Read the original with:

```bash
git show origin/flue-runtime:docs/design/flue-daytona-fleetdb-v1-proposal.md
```

Its own summary of scope: the V1 control-plane contract for running Loom
task agents with FleetDB as the source of truth for epics, task
dependencies, claims, sessions, leases, and artifacts; Flue as the agent
backend/harness; and Daytona as a remote runtime provider for isolated
filesystem and shell execution.

It has not been re-audited against shipped code. Treat its claims as
2026-06-03 intent, not as fact.

## What V1 meant, per the V2 proposal

`docs/design/fleetdb-agent-platform-v2-proposal.md-42` describes V1 as
the finite-TaskRun path:

```text
FleetDB ready task -> TaskRun -> lease -> runner -> sandbox -> artifacts -> CompleteRun
```

V2 says that path "is still required, but it is not enough" — V2 adds
long-lived services, triggers, and the broader platform model on top.

## Where that path lives today

That pipeline shipped. For the as-built map — claim, lease, fencing,
heartbeat, sandbox launch, artifacts, completion — see
`docs/design/2026-07-23-control-plane-as-built.md`. In short:

- `domain.TaskRun`, `internal/domain/platform.go:498`.
- `store.TaskRunStore`, `internal/store/platform_store.go:762-773`
  (`Create`, `ClaimQueued`, `Heartbeat`, `Complete`, `Finish`, `Requeue`).
- `driver.TaskWorker.RunOnce`, `internal/driver/task_worker.go:48`.
- Daytona runner: `internal/driver/bundled_runner.go:16-20`,
  `internal/workflows/builtin/daytona-task-runner.ts`.
- Runner-facing HTTP: `POST /api/workspaces/{ws}/task-run/{op}`,
  `internal/webui/handlers/taskrunapi/module.go:145`.

## Related

- `docs/design/flue-daytona-runtime-proposal.md` — the runtime-provider half
  of the same V1 pair (also a stub, same reason).
- `docs/design/fleetdb-agent-platform-v2-proposal.md` — the V2 correction.
- `docs/design/2026-07-23-control-plane-as-built.md` — what shipped.
- `docs/design/distributed-control-plane.md` — the conceptual architecture
  V1 was written against.
