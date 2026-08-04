# Go Backend Tests

> **Status:** Current · *audited 2026-08-03*

This page tracks the current Go test surfaces after FleetDB became the
canonical issue store. For the deletion phase, current backend tests should
prove behavior through FleetDB-backed stores or explicit migration-only code.

## Primary Commands

| Scope | Command | Notes |
|---|---|---|
| Full gate | `make gate` | Runs Go and frontend quality checks used before push. |
| Go packages | `go test ./internal/cli/... ./internal/webui/...` | Broad CLI/WebUI package coverage. |
| FleetDB resolver | `go test ./internal/cli/... -run 'Test.*(IssueBackend|Fleet|Config|Deps)'` | Verifies fail-closed backend selection. |
| FleetDB runtime lint | `./scripts/check-no-beads-prod.sh` | Rejects active production Beads/bd references outside the explicit allowlist. Also runs as step 10 of `check-go` (`Makefile:523-524`). |
| Clean checkout smoke | `./scripts/test-fleetdb-clean-checkout.sh` | Verifies local setup does not require a `bd` binary or `.beads` artifacts. Wrapped by `make test-fleetdb-embedded` (`Makefile:90`). |
| Supervisor control plane | `make test-fleetdb-supervisor` (`Makefile:94`) | Pins an exact `-run` regex across `./internal/cli`, `./internal/cli/data`, `./internal/cli/agentdef`, `./internal/cli/daemon`, `./internal/cli/daemon/supervisor`: `Test(AgentIPCClient\|IPCServer_\|Data(Ready\|ShowClaimClose)_NoServer\|ClaimTask_\|TaskIDForLifecycle_\|Supervisor(Register\|Heartbeats\|Mirrors)ControlPlane\|BuildCommand_SessionEnvVars)`. Extend the regex, don't fork the target. |

## Current Test Areas

| Area | Representative Packages | What To Cover |
|---|---|---|
| Backend contract | `internal/backend`, `internal/backend/fleet`, `internal/infra/fleetdb` | CRUD, list/ready/blocked/search, stats/count, metadata, dependencies, comments/events, batch, and claim behavior. |
| CLI backend selection | `internal/cli`, `internal/cli/config` | FleetDB defaults, explicit `fleet`/`fleetdb`, invalid backend values, fail-closed errors. |
| Agent and supervisor flows | `internal/cli/agent`, `internal/cli/automode`, `internal/cli/daemon/...` | Claim flow, task lifecycle, local supervisor state, session metadata, control commands. |
| Workspace lifecycle | `internal/cli/workspace`, `internal/webui/service`, `internal/webui/app` | Workspace list/create/delete/default, repo groups, FleetDB-backed state agreement between CLI and UI. |
| WebUI handlers | `internal/webui/handlers/...`, `internal/webui/handlermux` | API behavior through IssueBackend and store-backed workspace services. |
| Realtime | `internal/webui/server/realtime`, `internal/webui/subscription` | SSE cursor normalization, reconnect catch-up, mutation translation, workspace filtering. |
| Observability and monitor | `internal/cli/monitor`, `internal/cli/serve/...` | Queue summaries, git sync state, metrics, and no local issue-daemon startup. |

## Updating This Inventory

When adding new FleetDB regression coverage:

- Add or update the focused package test.
- Name the relevant acceptance gate from
  [Fleet-DB acceptance gates](fleetdb-acceptance-gates.md).
- Include the command output in the issue handoff.
- Keep new production code free of active Beads/bd fallbacks; use
  `./scripts/check-no-beads-prod.sh` before closing deletion work.

## Related

- [fleetdb-acceptance-gates.md](fleetdb-acceptance-gates.md) — the named gates G0-G8 and today's command for each
- [test-infrastructure.md](test-infrastructure.md) — what `make gate` actually runs, and the coverage thresholds
- [test-patterns.md](test-patterns.md) — Go table-driven and handler test conventions
- [../testing-terminology.md](../testing-terminology.md) — depth / realness / provisioning / polarity
- [README.md](README.md) — testing docs index
