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
| CLI backend selection | `internal/cli`, `internal/cli/config` | FleetDB defaults, explicit `fleet`/`fleetdb`, invalid backend values, fail-closed errors. |
| Agent and supervisor flows | `internal/cli/agent`, `internal/cli/automode`, `internal/cli/daemon/...` | Claim flow, task lifecycle, local supervisor state, session metadata, control commands. |
| Workspace lifecycle | `internal/cli/workspace`, `internal/webui/service`, `internal/webui/app` | Workspace list/create/delete/default, repo groups, FleetDB-backed state agreement between CLI and UI. |
| WebUI handlers | `internal/webui/handlers/...`, `internal/webui/handlermux` | API behavior through IssueBackend and store-backed workspace services. |
| Realtime | `internal/webui/server/realtime`, `internal/webui/subscription` | SSE cursor normalization, reconnect catch-up, mutation translation, workspace filtering. |
| Observability and monitor | `internal/cli/monitor`, `internal/cli/serve/...` | Queue summaries, git sync state, metrics, and no local issue-daemon startup. |

## Tests Must Never Write the Workspace Ledger

`<workspace>/sessions/index.jsonl` and `<workspace>/usage.jsonl` are production
data: the running fleet appends to them, and the dashboards, usage reports and
session transcripts read them back. A test that writes a row there is not a
harmless side effect — it corrupts a record nobody can distinguish from a real
one after the fact. Measured on 2026-09-01: 2391 of 11215 ledger rows and 1196
session directories in the live PUPPET workspace were test fixtures.

The leak was environmental, not a bug in any one test. Every fleet agent shell
exports `LOOM_WORKSPACE_RUNTIME_DIR` pointing at the live workspace, and
`cli.GetWorkspaceRuntimeDir` honored it — so `go test` run from an agent shell
resolved the real workspace and `internal/cli/automode` built its session and
usage stores on top of it. Two mechanisms now prevent that:

- **In-process.** `cli.GetWorkspaceRuntimeDir` ignores `LOOM_WORKSPACE_RUNTIME_DIR`
  under `testing.Testing()` when the value was *inherited* — identical to the
  one captured at process start. A value a test sets itself is different, and is
  still honored, so ordinary tests are unaffected.
- **Process boundary.** `scripts/test.sh` re-execs itself through
  `scripts/with-clean-loom-env.sh`, which unsets the whole `LOOM_*` runtime
  block. This covers `make test`, `make test-integration`, `make test-all` and a
  direct `./scripts/test.sh`; `make check` step 12 already ran `go test` through
  the same scrubber.

The in-process guard does not cross an exec boundary, which is why the second
mechanism exists. A test that runs the built `loom` binary must still hand the
child an explicit runtime dir — pass its command's environment through
`testutil.SandboxLoomRuntimeDir`, or set `LOOM_WORKSPACE_RUNTIME_DIR` to a
`t.TempDir()` yourself.

## Updating This Inventory

When adding new FleetDB regression coverage:

- Add or update the focused package test.
- Name the relevant acceptance gate from
  [Fleet-DB acceptance gates](fleetdb-acceptance-gates.md).
- Include the command output in the issue handoff.
- Keep new production code free of active Beads/bd fallbacks; use
  `./scripts/check-no-beads-prod.sh` before closing deletion work.
