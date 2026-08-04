# Driver-Op HTTP API

> **Status:** Current · *audited 2026-07-23*
> **Date:** 2026-07-23 (documents work landed in commit `8020b2bcf`, "serve: add
> driver-op HTTP API module (SDK v2 transport)")

A running workflow bundle's entire control surface is HTTP against `loom serve`.
`sdk/api-surface.v1.json` is the machine-readable record; this is the prose one.

## Two Surfaces, Two Credentials

| Surface | Route | Caller | Credential |
|---|---|---|---|
| Driver ops | `POST /api/workspaces/{ws}/driver/{op}` | the workflow bundle (`sdk/driver.js`) | run-scoped bearer token |
| Task-run ops | `POST /api/workspaces/{ws}/task-run/{op}` | the task runner (`sdk/runner.js`) | per-TaskRun lease token |

Both are registered by their own module: `internal/webui/handlers/driverapi/module.go:191`
and `internal/webui/handlers/taskrunapi/module.go:145`.

The package doc states the design intent: the driver-op API exposes "the same
operations the hidden `loom driver` CLI subcommands expose, but over loom serve
so workflow bundles talk HTTP instead of spawning CLI subprocesses"
(`internal/webui/handlers/driverapi/module.go:1-5`). Wire format is camelCase
JSON with structured errors `{code, message, retryable}`.

## Driver Ops

Dispatched from a map, so the op name is the last path segment
(`internal/webui/handlers/driverapi/module.go:141-158`):

```text
claim-ready                 epic-get                epic-snapshot
list-agents                 agent-orchestration-session
update-agent-parent         deliver-lead-assignment deliver-agent-message
exec-task                   task-run-get            active-task-runs
recover-stale-tasks         complete-task           release-task
connector-dispatch          emit-event
```

`exec-task` is the one whose meaning changed after the docs were written: the
SDK sends `enqueueOnly: true` and the handler enqueues a `queued` TaskRun rather
than executing it (`internal/webui/handlers/driverapi/module.go:530`, `:592`).
See [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md).

`recover-stale-tasks` exists but workflows must not call it: stale recovery is
server policy, run unconditionally by serve
(`internal/cli/serve/serve_loops.go:25-30`).

## Routes The `{op}` Pattern Cannot Match

Four routes have their own handlers because their paths have two segments after
`/driver/`, plus one SSE stream
(`internal/webui/handlers/driverapi/module.go:192-198`):

| Route | Purpose |
|---|---|
| `GET  /api/workspaces/{ws}/driver/watch/epic` | SSE journal of TaskRun events for an epic |
| `POST /api/workspaces/{ws}/driver/events/await` | register an await on a trigger event (AW9) |
| `GET  /api/workspaces/{ws}/driver/events/awaits` | list the run's awaits (re-entry context) |
| `POST /api/workspaces/{ws}/driver/workflows/start` | start a child workflow (AW10) |
| `POST /api/workspaces/{ws}/driver/workflows/await` | await a child workflow (AW10) |

### Epic watch stream contract

From `internal/webui/handlers/driverapi/watch.go:43-55`:

- handshake: `event: snapshot` carrying the watch snapshot, `id` = cursor;
- journal: `event: taskRun` per event, `id` = the event's `Seq`;
- resume: `Last-Event-ID` header (or `afterSeq` query) as an **exclusive**
  int64 `Seq` cursor — already-seen events are skipped;
- liveness: every reconciliation tick re-verifies the parent run and re-sends a
  snapshot; when the parent is no longer running the stream ends with
  `event: closed` `{code: "parent_not_running"}`.

Errors raised before streaming starts use the same `{code, message, retryable}`
envelope as the op routes.

## Auth

Run-scoped bearer tokens are the preferred credential. The server derives the
parent `DriverRun` identity (run / node / lease / fencing token) from the token
claims, so a workflow never handles fencing headers or ambient credentials
(`internal/webui/handlers/driverapi/module.go:7-15`).

- Tokens are HS256 JWTs minted at claim: `internal/driver/run.go:253`
  (`MintRunToken`), parsed at `internal/webui/handlers/driverapi/token_auth.go:37`.
- Signing key: `LOOM_RUN_TOKEN_SIGNING_KEY` (hex, 32 bytes)
  (`internal/driver/run.go:208`, resolution at `:327`). Unset, serve falls back
  to an ephemeral per-process key — single-instance only, in-flight runs die
  with the process (`AGENTS.md` § Driver Runtime Auth).
- TTL: `LOOM_RUN_TOKEN_TTL` (`internal/driver/run.go:215`).
- Revocation is not a token list. The resolved identity is verified through the
  same fenced-heartbeat path the CLI uses, so terminal runs and superseded
  leases are rejected regardless of token expiry
  (`internal/webui/handlers/driverapi/module.go:12-15`).
- Legacy transport: the `X-Loom-Driver-*` header quad plus the optional shared
  static `LOOM_DRIVER_API_TOKEN` still works for CLI subcommands and ops
  tooling, and is deprecated for workflow runtimes behind
  `LOOM_DRIVER_LEGACY_AUTH_ENV` (`AGENTS.md` § Driver Runtime Auth).

Task runners authenticate separately with their per-TaskRun lease token —
the `{taskRunId, nodeId, leaseId, leaseToken, fencingToken}` tuple fleet-db's
fenced checks verify (`internal/webui/handlers/taskrunapi/module.go:151-154`).
Serve exports `LOOM_TASK_RUN_API_URL` to bridge-spawned runners so they do not
need fleet-db credentials at all (`AGENTS.md` § Driver Runtime Auth).

## Task-Run Ops

`internal/webui/handlers/taskrunapi/module.go:105-116`:

```text
get   task-get   heartbeat   log-append   complete   runtime-credential
artifact-declare   artifact-get   artifact-list   artifact-finalize
```

Raw artifact content is a separate route because the body is the content itself:
`PUT /api/workspaces/{ws}/task-run/artifacts/{artifactId}/content`
(`internal/webui/handlers/taskrunapi/module.go:148`).

## Related

- [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md) — what
  `exec-task` actually does now.
- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the current platform contract.
- [`generic-sse-envelope.md`](generic-sse-envelope.md) — the repo's other SSE
  stream shape.
