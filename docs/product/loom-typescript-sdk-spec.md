# Loom TypeScript SDK — spec pointer

> **Status:** Current, but this file is a pointer. The authoritative
> reference is the package itself: `sdk/README.md` and the published types
> in `sdk/*.d.ts`. The original PRD is not on `main`. *audited 2026-08-03*

## Why this file is a pointer

`docs/design/fleetdb-agent-platform-v2-proposal.md`,
`docs/design/fleetdb-agent-platform-v2-phased-delivery.md`, and
`docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md`
list `docs/product/loom-typescript-sdk-spec.md` under "Related". The
document they point at ("Loom TypeScript SDK & Flue-as-Control-Plane-Client
— PRD", 2026-06-03) lives only on the unmerged branch
`origin/flue-runtime`:

```bash
git show origin/flue-runtime:docs/product/loom-typescript-sdk-spec.md
```

The SDK it proposed **shipped** and is checked into `main` under `sdk/`, so
duplicating a PRD here would be worse than pointing at the code. What
follows is the one-hop map; everything in it is verified against `sdk/`.

## What shipped

`@loom/sdk` (`sdk/package.json`) — one package, two clients, all wire
fields camelCase.

| Entry point | Who uses it | Fronts |
|---|---|---|
| `@loom/sdk/driver` (`sdk/driver.js`, `sdk/driver.d.ts`) | Workflow code running *inside* a driver run, with a run-scoped token. | `POST /api/workspaces/{ws}/driver/{op}` and the driver watch/await/workflow routes, `internal/webui/handlers/driverapi/module.go:191-198`. |
| `@loom/sdk/runner` (`sdk/runner.js`, `sdk/runner.d.ts`) | Runner harnesses — claiming env, logs, artifacts, completion. | `POST /api/workspaces/{ws}/task-run/{op}` and `PUT /api/workspaces/{ws}/task-run/artifacts/{artifactId}/content`, `internal/webui/handlers/taskrunapi/module.go:145,148`. |
| `@loom/sdk/runtime-adapters` | Flue↔Loom conversions (e.g. usage accounting). | — |

Driver-client namespaces (`sdk/README.md:166-175`): `epics`, `agents`,
`tasks`, `taskRuns`, `connectors`, `events`, `workflows`, plus terminal
result helpers. Runner entry exports `TaskRunClient`, `ArtifactHandle`,
`RunnerEnv`, `LoomAPIError` (`sdk/runner.d.ts`).

The v1 surface is frozen and shipped with the package as
`sdk/api-surface.v1.json`, enforced by contract tests on both sides of the
wire (`sdk/contract.test.mjs`). Errors are a frozen envelope
`{ code, message, retryable, details? }`; the SDK ships **no automatic
retry** — `retryable` is server guidance only (`sdk/README.md:187`, in
"Errors and retries", `sdk/README.md:181-189`).

In-repo consumers: `internal/workflows/builtin/epic-runner.ts`,
`github-review-agent.ts`, `daytona-task-runner.ts`,
`local-task-runner.ts`.

## Naming caveat

`sdk/README.md:27-31` records an unresolved decision: the intended npm name
is `@loom/sdk`, with `@browseroperator/loom-sdk` as the decided fallback if
the `@loom` scope is not ours. Scope ownership must be verified from an
authenticated machine before the first publish.

## Related

- `sdk/README.md` — the authoritative SDK reference (install, quickstart,
  runners, auth, operation reference, versioning, publishing).
- `sdk/api-surface.v1.json` — the frozen v1 wire surface.
- `docs/design/2026-07-23-control-plane-as-built.md` — the HTTP routes the
  SDK fronts, and the lease-token auth model behind them.
- `docs/design/fleetdb-agent-platform-v2-proposal.md` — the platform
  direction that treats the SDK as the control-plane client.
