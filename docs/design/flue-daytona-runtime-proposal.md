# Flue and Daytona Runtime Proposal — pointer

> **Status:** Not on `main` — the full document lives only on the unmerged
> branch `origin/flue-runtime`. This file exists so the references to it
> from the V2 docs resolve. *audited 2026-07-23*

## Why this file is a stub

`docs/design/fleetdb-agent-platform-v2-proposal.md`,
`docs/design/fleetdb-agent-platform-v2-phased-delivery.md`, and
`docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md`
list `docs/design/flue-daytona-runtime-proposal.md` under "Related". It was
written 2026-06-02 (1060 lines as of `d20ed3abd`) on a branch that was
never merged, so the path has never existed on `main`.

Read the original with:

```bash
git show origin/flue-runtime:docs/design/flue-daytona-runtime-proposal.md
```

Its own framing: how Loom can use Flue as an agent backend and Daytona as
an isolated remote sandbox without losing FleetDB tasks, daemon
supervision, sessions, logs, diffs, and user-visible run status — and the
insistence that **Flue backend integration** and **Flue sandbox selection**
are separate decisions.

It has not been re-audited against shipped code. Treat its claims as
2026-06-02 intent, not as fact.

## Why this matters to the older architecture docs

`docs/design/distributed-control-plane.md` proposed a `RuntimeProvider` Go
interface and an "E2B Sandbox Provider" section. Neither shipped. The
runtime-provider question was reopened by this proposal, and what landed is
Daytona plus a much narrower launch-only seam.

## Where the runtime layer lives today

See `docs/design/2026-07-23-control-plane-as-built.md` for the full map. In
short:

- The runtime seam is `sandbox.SandboxLauncher`,
  `internal/driver/sandbox/launcher.go:83` — one method,
  `Launch(ctx, LaunchSpec) (SandboxProcess, error)`. There is no
  `RuntimeProvider` interface anywhere; `domain.RuntimeProvider`
  (`internal/domain/control_plane.go:21-29`) is a string enum.
- Launcher selection: `ResolveSandboxLauncher()`,
  `internal/driver/executor.go:69`, wired from
  `internal/cli/serve/serve.go:358`. `LOOM_DRIVER_SANDBOX=container`
  selects rootless containers (`internal/driver/sandbox/container.go`);
  an invalid sandbox config disables the executor rather than degrading
  isolation.
- Daytona: `internal/driver/bundled_runner.go:16-20`,
  `internal/driver/task_bridge.go`,
  `internal/runtimepreflight/preflight.go`,
  `internal/workflows/builtin/daytona-task-runner.ts`.
- E2B: never implemented. `RuntimeProviderE2B`
  (`internal/domain/control_plane.go:25`) is an unused enum value.

## Related

- `docs/design/flue-daytona-fleetdb-v1-proposal.md` — the control-plane half
  of the same V1 pair (also a stub, same reason).
- `docs/design/2026-07-23-control-plane-as-built.md` — what shipped.
- `docs/design/distributed-control-plane.md` — the "Runtime Providers" and
  ephemeral-sandbox sections this proposal superseded.
- `docs/product/container-runner-mvp-spec.md` — the container-runner
  product spec.
