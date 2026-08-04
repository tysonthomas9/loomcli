# Container Runner MVP Spec

> **Status:** Aspirational — proposed 2026-05-04, not implemented as of
> 2026-07-24. No `loom-runner` binary, image, or entrypoint exists anywhere in
> the tree; `cmd/` contains only `loom`, and `domain.RuntimeProvider` has no
> `podman` value (`internal/domain/control_plane.go:23-29`). Loom *does* run
> containers today, but through two different mechanisms — see
> [What Shipped Instead](#what-shipped-instead). Read this document as the
> recorded plan, not a description.

**Date:** 2026-05-04
**Related:** see [Related](#related) at the bottom.

## What Shipped Instead

Nothing in this spec was built. Two unrelated container paths did ship, and
neither is "a Loom agent in a Podman container" as described below.

| Shipped shape | What actually runs in the container | Where |
|---|---|---|
| **`loom worker`** — a remote agent worker | The same `loom` binary, different subcommand. It registers with a `loom serve` control plane over HTTP and runs the auto-mode loop with HTTP-backed lock/event/log bridges. Deliberately *not* a separate runner binary. | `internal/cli/serve/worker/worker_cmd.go:40-50`; container image `deploy/podman-stack/Containerfile.worker`; entrypoint `deploy/podman-stack/worker-entrypoint.sh` |
| **Driver sandbox** — `LOOM_DRIVER_SANDBOX=container` | A workflow **driver / task run**, not an agent. Rootless, podman-first with docker fallback, read-only rootfs, `no-new-privileges`, mandatory memory/cpu/pids caps, four egress modes. | `internal/driver/sandbox/container.go:145-170`, `internal/driver/sandbox/egress.go` |

The codified multi-container reference deployment is `deploy/podman-stack/`
(serve + fleet-db + redis + workers + stub upstream on a podman machine). Read
`deploy/podman-stack/README.md` for how to run it, and
`docs/testing/local-mode-podman-e2e.md` for the test shape.

Several ideas below did survive into `loom worker`, in adapted form: the runner
registers with the control plane before doing work, heartbeats while running,
and streams logs over HTTP rather than writing them into the container. What
did **not** survive is the `loom-runner` binary, the golden image, the
`podman` runtime-provider value, and the container-metadata record.

## Purpose

Define the MVP behavior for running Loom agents inside Podman containers
while preserving UI visibility, logs, sessions, and artifacts after the
container exits.

## Product Promise

A containerized planner or coder should feel like a first-class Loom
agent, not an invisible side process.

The user should see:

- container started
- agent registered
- session running
- task claimed
- logs streaming
- artifacts preserved
- container exited with final status

## MVP Runtime

The first runner implementation targets Podman on the local developer
machine.

Future runtime providers can reuse the same contract:

- Docker
- Kubernetes
- E2B
- remote VM
- CI runner

## Runner Lifecycle

```text
create container -> register runner -> create run/session
-> preflight -> claim task -> invoke backend -> stream artifacts
-> finalize run -> exit container
```

## Required Container Metadata

Each run should record:

- runtime provider: `podman` — note this value was never added; the enum is
  `local` | `e2b` | `kubernetes` | `ci` | `other`
  (`internal/domain/control_plane.go:23-29`)
- image name and digest/tag
- container ID
- container name
- node ID or host identity
- command
- environment profile name, not secret values
- workspace ID
- agent name
- role/backend/model

## Required Runner Protocol

The runner must call server APIs to:

1. Register node/runner.
2. Create run.
3. Create session.
4. Send preflight result.
5. Claim task.
6. Append logs/transcript.
7. Attach artifacts.
8. Heartbeat while running.
9. Finalize run.

If the server is unavailable, the runner should fail preflight unless the
user explicitly asks for detached execution.

## Storage Rules

- Durable session data must live outside the container.
- `podman run --rm` must not lose transcripts, logs, diffs, or run
  metadata.
- Container-local files are allowed only as temporary buffers.
- Secrets must be mounted read-only and never copied into artifacts.

## Golden Runner Image

The MVP runner image should include:

- Loom CLI
- Codex CLI
- `git`
- `jq`
- `make`
- shell utilities expected by prompts
- CA certificates
- health/preflight script

The image should expose:

- `loom-runner plan`
- `loom-runner task`
- `loom-runner preflight`

## Workspace Mounting

The runner needs an explicit workspace strategy:

| Strategy | Use |
|---|---|
| Bind mount host workspace | Local Podman dogfood and fast iteration. |
| Git clone into container volume | Cleaner isolated run. |
| Shared named volume | Repeatable local distributed tests. |

The MVP should support bind mount and named volume. It should warn when
UI diff/session paths are not server-visible.

## UI Behavior

The UI should show:

- container state: starting, running, exited, failed, stale
- container ID/name
- image
- logs
- heartbeat age
- task claim
- final artifacts

If the container exits before task claim:

```text
Container exited before claiming a task.
Open preflight/logs for details.
```

If the container stops heartbeating:

```text
Runner stale. Last heartbeat 2m ago.
Actions: inspect logs, mark failed, release claim.
```

## MVP User Flow

1. User clicks "Start container coder" or runs a CLI command.
2. Loom creates container with configured image and mounts.
3. Runner registers and creates run/session.
4. UI shows agent card immediately.
5. Runner preflights tools and credentials.
6. Runner claims eligible task.
7. UI task card shows claimed container agent.
8. Logs and transcript stream.
9. Runner finalizes session and exits.
10. UI shows completed/failed run with artifacts.

## Failure Handling

| Failure | Expected result |
|---|---|
| Image missing | Launch fails before run starts; UI shows image pull/build action. |
| Credentials missing | Preflight failed; no model invocation. |
| Workspace mount missing | Preflight failed with mount details. |
| Server unreachable | Runner exits unless detached mode was requested. |
| Container killed | Run becomes stale, then failed/aborted after timeout. |
| Artifact upload fails | Run warning with retry action. |

## Acceptance Criteria

- Starting a Podman planner creates a UI-visible run before model
  invocation.
- Task Sessions tab shows the run while the container is active.
- Logs survive after `podman run --rm`.
- Container exit code is recorded.
- A killed container becomes stale, then failed or aborted.
- The dogfood stack can run planner and coder containers without manual
  session or agent repair.

## Open Questions

All three were left unanswered — the spec was never implemented. The first is
partly answered by what shipped: for `loom worker`, containers are launched by
the operator's compose file, not by `loom serve`
(`deploy/podman-stack/compose.yaml`).

- Should the server launch containers directly, or should a local runner
  daemon do it?
- Should credentials be copied, mounted, or brokered through a secret
  provider?
- Should artifact upload be streaming or final-only for MVP?

## Related

- [`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md)
  — its "Container And Remote Placement" section is the current, verified map
  of where agents and task runs can actually be placed. **Read that instead of
  this document if you need facts.**
- [`local-mode-product-spec.md`](local-mode-product-spec.md) — the
  single-machine mode this was meant to follow.
- [`session-artifact-contract.md`](session-artifact-contract.md) — what a
  session must persist regardless of where it runs.
- [`agent-execution-prd.md`](agent-execution-prd.md) — the parent PRD.
- [`dogfood-agent-execution-test-plan.md`](dogfood-agent-execution-test-plan.md)
  — the test plan that referenced this spec.
- `deploy/podman-stack/README.md` — the codified multi-container deployment.
- `docs/testing/local-mode-podman-e2e.md` — the podman e2e test shape.
- `docs/design/flue-daytona-runtime-proposal.md` — the remote-sandbox
  alternative that links here.
