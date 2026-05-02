# Go Backend Tests

This page tracks the current Go test surfaces after FleetDB became the
canonical issue store. For the deletion phase, current backend tests should
prove behavior through FleetDB-backed stores or explicit migration-only code.

## Primary Commands

| Scope | Command | Notes |
|---|---|---|
| Full gate | `make gate` | Runs Go and frontend quality checks used before push. |
| Go packages | `go test ./internal/cli/... ./internal/webui/...` | Broad CLI/WebUI package coverage. |
| FleetDB resolver | `go test ./internal/cli/... -run 'Test.*(IssueBackend|Fleet|Config|Deps)'` | Verifies fail-closed backend selection. |
| FleetDB runtime lint | `./scripts/check-no-beads-prod.sh` | Rejects active production Beads/bd references outside the explicit allowlist. |
| Clean checkout smoke | `./scripts/test-fleetdb-clean-checkout.sh` | Verifies local setup does not require a `bd` binary or `.beads` artifacts. |

## Current Test Areas

| Area | Representative Packages | What To Cover |
|---|---|---|
| Backend contract | `internal/backend`, `internal/backend/fleet`, `internal/infra/fleetdb` | CRUD, list/ready/blocked/search, stats/count, metadata, dependencies, comments/events, batch, and claim behavior. |
| CLI backend selection | `internal/cli`, `internal/cli/config` | FleetDB defaults, explicit `fleet`/`fleetdb`, invalid legacy backend values, fail-closed errors. |
| Agent and supervisor flows | `internal/cli/agent`, `internal/cli/automode`, `internal/cli/daemon/...` | Claim flow, task lifecycle, local supervisor state, session metadata, control commands. |
| Workspace lifecycle | `internal/cli/workspace`, `internal/webui/service`, `internal/webui/app` | Workspace list/create/delete/default, repo groups, FleetDB-backed state agreement between CLI and UI. |
| WebUI handlers | `internal/webui/handlers/...`, `internal/webui/handlermux` | API behavior through IssueBackend and store-backed workspace services. |
| Realtime | `internal/webui/server/realtime`, `internal/webui/subscription` | SSE cursor normalization, reconnect catch-up, mutation translation, workspace filtering. |
| Observability and monitor | `internal/cli/monitor`, `internal/cli/serve/...` | Queue summaries, git sync state, metrics, and no local issue-daemon startup. |

## Migration-Only Tests

Tests for importing old data may intentionally invoke the legacy CLI from
`internal/cli/migrate`. Keep those references isolated to migration packages and
do not use them as parity or deletion-gate evidence.

## Updating This Inventory

When adding new FleetDB parity coverage:

- Add or update the focused package test.
- Name the relevant acceptance gate from
  [Fleet-DB acceptance gates](fleetdb-acceptance-gates.md).
- Include the command output in the issue handoff.
- Keep new production code free of active Beads/bd fallbacks; use
  `./scripts/check-no-beads-prod.sh` before closing deletion work.
