# Container Runner MVP Spec

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/session-artifact-contract.md`,
`docs/design/distributed-control-plane.md`

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

- runtime provider: `podman`
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

- Should the server launch containers directly, or should a local runner
  daemon do it?
- Should credentials be copied, mounted, or brokered through a secret
  provider?
- Should artifact upload be streaming or final-only for MVP?
