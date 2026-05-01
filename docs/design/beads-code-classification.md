# Beads Code Classification

**Status:** Removal classification for `loomcli-wpltp`
**Date:** 2026-05-01
**Related:** `docs/design/fleetdb-parity-inventory.md`,
`docs/testing/fleetdb-acceptance-gates.md`

## Classification Rules

| Class | Meaning | May remain before fleet-db default? | May remain after beads deletion? |
|---|---|---:|---:|
| Active fallback | Production path can still route to beads or bd daemon | No | No |
| Parity-only | Used only to compare beads behavior against fleet-db | Yes, until fleet-db-only gates replace it | No |
| Migration/import-only | Used only to import existing beads data into fleet-db | Yes, if explicitly scoped | No, unless migration is still a supported product feature |
| Retained, repoint to fleet-db | Product functionality that currently uses beads-shaped plumbing | Yes | Yes, after repointing |
| Test fixture/naming | Tests use `bd-*` IDs or beads labels as inert sample data | Yes | Yes only if not user-facing and not runtime-coupled |
| Dead/removable | Legacy code with no required parity, migration, or product role | No | No |

## Runtime Fallbacks To Remove Before Fleet-DB Default

These paths must be removed or made fail-closed before `loomcli-wpltp.9` can
make fleet-db the default.

| Surface | Current code | Class | Required action | Blocking ticket |
|---|---|---|---|---|
| Backend resolver default | `internal/cli/issue_backend_resolve.go` | Active fallback | Stop defaulting to `beads`; invalid or missing fleet-db config must produce an explicit error or embedded fleet-db startup | `loomcli-wpltp.9.1`, `loomcli-wpltp.9.4` |
| Valid backend set | `IssueBackendBeads`, `validIssueBackends` | Active fallback | Remove `beads` as an accepted runtime backend once fleet-db gates are green | `loomcli-wpltp.9.1`, `loomcli-wpltp.10.5` |
| Default dependency construction | `internal/cli/deps.go` | Active fallback | Remove `BDRunner`, `defaultBDRunnerImpl`, and `newCliBeadsAdapter` fallback; fleet-db construction errors must surface | `loomcli-wpltp.9.2`, `loomcli-wpltp.10.1` |
| Workspace-aware backend fallback | `internal/cli/issue_backend_workspace.go` | Active fallback | Do not fall back to `DefaultIssueBackend` when workspace fleet backend construction fails | `loomcli-wpltp.9.2` |
| Serve issue backend bootstrap | `internal/cli/serve/serve.go` | Active fallback | Stop ensuring and stopping `bd daemon`; serve should use fleet-db store/backend lifecycle | `loomcli-wpltp.9.3` |
| Workspace lifecycle side effects | `internal/cli/serve/workspacemgr`, `internal/cli/workspace` | Active fallback | Remove `bd init`, `bd repo sync`, and daemon start/stop side effects from create/clone/delete | `loomcli-wpltp.4.2`, `loomcli-wpltp.4.3`, `loomcli-wpltp.4.4`, `loomcli-wpltp.4.6` |
| Web daemon pool hooks | `internal/webui/hooks/beads_pool.go`, `notification_subscriber.go` | Active fallback | Replace with fleet-db backend/subscriber hooks for all runtime modes, then delete beads hooks | `loomcli-wpltp.6.*`, `loomcli-wpltp.10.2` |
| Daemon RPC subscriber | `internal/webui/subscription/subscriber.go`, `internal/webui/daemon` | Active fallback | Remove bd daemon polling once backend subscriber covers reconnect, filtering, and backpressure | `loomcli-wpltp.6.*`, `loomcli-wpltp.10.2` |
| Web issue move RPC path | `internal/webui/service/issue_move.go` | Active fallback | Route through `IssueBackend`/fleet-db instead of daemon RPC clients | `loomcli-wpltp.2.2`, `loomcli-wpltp.4.*` |
| Agent prompts | `internal/cli/agent/prompts.go` | Active fallback/user-facing | Replace `bd ready`, `bd update`, `bd show`, and `bd sync` instructions with loom/fleet-db-backed commands | `loomcli-wpltp.5.2`, `loomcli-wpltp.5.6`, `loomcli-wpltp.10.6` |
| Agent recovery hints | `internal/cli/agent/recover_helpers.go` | Active fallback/user-facing | Stop printing `bd update ...` recovery instructions | `loomcli-wpltp.5.3`, `loomcli-wpltp.10.6` |
| Make install/sync beads | `Makefile`, `scripts/sync-beads.sh` | Active fallback/tooling | Remove release/user install dependency on `bd`; keep no sync/update targets after deletion | `loomcli-wpltp.10.4`, `loomcli-wpltp.10.5` |

## Parity-Only Code That May Temporarily Remain

These surfaces are allowed while G1/G2/G3 still compare beads and fleet-db, but
must be deleted when fleet-db-only gates replace side-by-side comparison.

| Surface | Current code | Class | Removal condition | Blocking ticket |
|---|---|---|---|---|
| Loom CLI backend parity harness | `internal/backend/paritytest` | Parity-only | Fleet-db-only backend/CLI regression gate exists and passes | `loomcli-wpltp.2.1`, `loomcli-wpltp.10.3` |
| Side-by-side browser parity stack | `test/parity/docker-compose.parity.yml`, `test/parity/ui`, `test/parity/seed.sh` | Parity-only | Fleet-db-only browser regression suite exists and passes | `loomcli-wpltp.3.1`, `loomcli-wpltp.10.3` |
| SSE parity comparison specs | `test/parity/ui/09-sse-realtime.spec.ts`, `10-sse-reconnect-parity.spec.ts` | Parity-only | Fleet-db-only SSE tests cover reconnect, replay, filtering, backpressure, and scale | `loomcli-wpltp.6.*`, `loomcli-wpltp.10.3` |
| Parity docs and reports | `docs/design/parity-report-*`, `docs/design/sse-reconnect-parity-spec.md`, `test/parity/browse.md` | Parity-only | Final fleet-db acceptance docs replace side-by-side instructions | `loomcli-wpltp.10.5` |
| Vendored bd binary for parity | `third_party/beads` as used by parity setup | Parity-only | No comparison gate needs a beads oracle | `loomcli-wpltp.10.4` |

## Migration/Import-Only Code

This repo currently contains a beads-to-fleet migration package. The product
decision for this epic is to get fleet-db parity first and skip legacy beads
functionality for the long-term runtime. If migration is not explicitly kept as
a supported feature, this class should be deleted with beads.

| Surface | Current code | Class | Required action | Blocking ticket |
|---|---|---|---|---|
| Migration command package | `internal/cli/migrate` | Migration/import-only | Decide whether import remains supported; otherwise delete package and command registration | `loomcli-wpltp.1.3`, `loomcli-wpltp.10.5` |
| Migration tests/fixtures | `internal/cli/migrate/*_test.go` | Migration/import-only | Keep only if migration is a supported compatibility feature | `loomcli-wpltp.10.5` |
| Beads docs describing migration | `docs/design/remove-beads.md`, old integration docs | Migration/import-only/docs | Update or supersede with fleet-db-first migration policy | `loomcli-wpltp.10.5` |

## Product Functionality To Retain And Repoint

These are not "beads legacy" even if their current implementation uses daemon
or beads-shaped plumbing.

| Surface | Current code | Class | Fleet-db target | Blocking ticket |
|---|---|---|---|---|
| Local agent supervisor | `internal/cli/daemon/supervisor` | Retained, repoint to fleet-db | Durable supervisor identity, fleet-db claim loop, lifecycle/session persistence, control channel | `loomcli-wpltp.5.*` |
| Agent IPC/control socket | `internal/cli/daemon_ipc_client.go`, daemon control wiring | Retained, repoint to fleet-db | Use for local control if useful, but operations mutate fleet-db state | `loomcli-wpltp.5.5` |
| Agent queue | `internal/cli/task_router.go`, web daemon queue callbacks | Retained, repoint to fleet-db | Fetch and claim ready work through fleet-db only | `loomcli-wpltp.5.2`, `loomcli-wpltp.3.5` |
| Terminal sessions | Web terminal handlers/managers | Retained, repoint to fleet-db metadata | Keep PTY/tmux mechanics; persist session/log metadata through fleet-db | `loomcli-wpltp.3.3`, `loomcli-wpltp.5.4` |
| File explorer and git diff | Web file/diff modules and agent services | Retained, mostly backend-independent | Keep local filesystem/git behavior; scope workspace/repo metadata through fleet-db | `loomcli-wpltp.3.4`, `loomcli-wpltp.4.*` |
| Workspace management | `internal/cli/workspace`, `workspacemgr`, fleet store | Retained, repoint to fleet-db | Fleet-db-backed source of truth for list/default/create/clone/delete/repo groups/roles | `loomcli-wpltp.4.*`, `loomcli-37h1h` |
| Realtime hub | `internal/webui/server/realtime`, `internal/webui/subscription/backend_subscriber.go` | Retained, repoint to fleet-db | Keep SSE hub; use fleet-db mutation source everywhere | `loomcli-wpltp.6.*` |

## Dead Or Removable In Deletion Phase

These should have no runtime role after fleet-db default/fail-closed behavior is
complete.

| Surface | Current code | Class | Delete ticket |
|---|---|---|---|
| Beads backend implementation | `internal/backend/beads` | Dead/removable after parity | `loomcli-wpltp.10.1` |
| CLI beads adapter | `internal/cli/cli_beads_adapter.go` | Dead/removable after G1 | `loomcli-wpltp.10.1` |
| Web daemon RPC client/pools | `internal/webui/daemon` | Dead/removable after G3 | `loomcli-wpltp.10.2` |
| Beads web hooks | `internal/webui/hooks/beads_pool.go`, `notification_subscriber.go` | Dead/removable after G3 | `loomcli-wpltp.10.2` |
| Daemon polling subscriber | `internal/webui/subscription/subscriber.go`, beads-only parts of `multi.go` | Dead/removable after G3 | `loomcli-wpltp.10.2` |
| Vendored beads subtree | `third_party/beads` | Dead/removable after comparison/migration removal | `loomcli-wpltp.10.4` |
| Sync/update tooling | `scripts/sync-beads.sh`, `scripts/sync-beads_test.sh`, Makefile sync/update targets | Dead/removable after vendored subtree removal | `loomcli-wpltp.10.4` |
| User-facing bd instructions | `AGENTS.md`, agent prompts, docs copy, frontend fixtures where visible | Dead/removable after replacement commands exist | `loomcli-wpltp.3.6`, `loomcli-wpltp.10.5`, `loomcli-wpltp.10.6` |

## Test Fixture Naming Allowed To Remain

Not every string containing `bd` is a runtime dependency. These references are
allowed after deletion only if G8 excludes them deliberately and they do not
drive production behavior:

- Unit-test issue IDs like `bd-1`, `bd-fleet-42`, or `bd-abc.1`.
- Historical design docs kept for archived context.
- Parity reports kept as historical artifacts after the active parity harness
  is removed.

Any active prompt, command help, UI copy, startup log, config option, runtime
import, subprocess call, or production hook that mentions beads/bd is not test
fixture naming and must be removed or renamed.

## Before Fleet-DB Default Mode

Fleet-db default mode is blocked until all active fallback rows above are fixed
and G0 through G7 are green. Parity-only and migration/import-only code may
still exist at that point if it is isolated from runtime selection and cannot
be reached by normal user workflows.

## Before Beads Deletion

Beads deletion is blocked until:

1. G0 through G8 are green.
2. Parity-only comparison gates have fleet-db-only replacements.
3. Migration/import support has an explicit keep/delete decision.
4. Product functionality listed under "Retained, repoint to fleet-db" has been
   repointed and verified.
5. `third_party/beads`, `internal/backend/beads`, web daemon RPC pools, CLI
   beads adapter, and user-facing bd instructions are gone.
