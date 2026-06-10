# Loom Dynamic Workflows

TypeScript workspace for Loom's dynamic workflow runner (see
`docs/design/dynamic-workflow-runner.md`). Workflow projects are normal
[Flue](https://flueframework.com) projects; Loom is the control plane,
fleet-db the data plane, and the Flue server the execution plane.

## Layout

- `packages/workflow-sdk` — `@loom/workflow-sdk`: run-scoped FleetDB
  client (`fleetClientForWake`), idempotent task starts (`startTask`,
  one TaskRun per task), and effectively-once side effects
  (`recordAction` via the ActionLedger).
- `examples/epic-runner` — the Phase 1 epic runner: a level-triggered
  reconciler agent woken once per `DriverRun`. Re-derives the epic
  frontier from FleetDB on every wake; conversational memory lives in
  the per-epic Flue instance (`epic-runner/<epic-id>`).
- `template/app.ts` — the sanctioned `app.ts` seam for new projects:
  `/healthz` (required by Loom's supervisor) + Flue routes + the place
  for auth middleware.

## Local development

```sh
pnpm install
pnpm build                      # builds the SDK + flue bundles

# Terminal 1: the loom daemon (control plane; watches fleet-db)
loom daemon

# Terminal 2: the execution plane for the example project
loom workflow dev --project ./workflows/examples/epic-runner

# Run an epic
loom workflow run epic-runner --epic EPIC-123
loom workflow logs <run-id> --follow
```

`loom workflow dev` injects `LOOM_FLEET_BASE_URL` and `LOOM_WORKSPACE`
into the Flue child; the agent needs `ANTHROPIC_API_KEY` (inherited
from your shell) for its model calls.
