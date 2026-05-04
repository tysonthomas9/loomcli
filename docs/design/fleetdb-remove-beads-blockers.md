# FleetDB Parity / Remove-Beads Blockers

Snapshot date: 2026-05-04

This document captures the open epic and task set that blocks treating FleetDB as the complete replacement for beads in loomcli.

## Summary

- Open actionable blocker tickets: 11
- Blocked child tickets still attached to `loomcli-26v50`: 2
- Open blocker epics: 3
- Total unresolved blocker items including epics: 16
- Closed child tickets captured for future reference: 57
- Total child tickets captured across `loomcli-7w9tc` and `loomcli-26v50`: 69

## Recommended Order

1. `loomcli-7w9tc.17` - Unify production FleetBackend URL dialects.
2. `loomcli-7w9tc.18` - Expand parity coverage across the full IssueBackend surface.
3. `loomcli-7w9tc.19` - File upstream fleet-db tickets for server-side capability gaps.
4. `loomcli-37h1h` - Move daemon config out of YAML and into FleetDB.
5. `loomcli-d924z` - Make clean FleetDB-only onboarding/e2e stack rebuildable without beads artifacts.
6. Close out the lower-priority parity harness/process tasks.
7. Remove fallback/legacy beads and YAML code only after the above has evidence from tests.

## Blocker Epics

### `loomcli-7w9tc` - Beads vs Fleet-DB Parity Validation

Priority: P1
Status: open

Validate fleet-db feature parity with beads across three surfaces:

- IssueBackend interface
- bd CLI
- webui

This epic is the evidence gate before deleting beads paths. The key risk is not just whether the UI appears to work, but whether the production FleetBackend covers the same behavioral surface that loom currently expects from beads.

Open child tickets: 10
Closed child tickets: 13

### `loomcli-26v50` - Move loom state to fleet-db (clean architecture, no yaml)

Priority: P1
Status: open

Goal: eliminate YAML config and move workspaces, repos, agents, roles, and daemon settings to FleetDB as first-class entities. Embedded FleetDB should auto-start for local single-user mode, while the same architecture should support cloud/multi-user mode.

Acceptance criteria still relevant:

- All workspace/repo/agent/role/daemon-setting state lives in FleetDB.
- Zero YAML files are read or written by loom CLI/webui/daemon.
- `loom serve` auto-starts embedded FleetDB when `LOOM_FLEET_DB_URL` is unset.
- Active workspace resolves from `LOOM_WORKSPACE` env, then `~/.loom/state.json`.
- New CLI surface: `loom workspace/repo/agent/role/daemon` noun-verb commands.
- `internal/cli/config/` and `internal/cli/serve/workspacemgr/` are deleted.
- `gopkg.in/yaml.v3` is removed from `go.mod`.

Open child tickets: 0
Blocked child tickets: 2
Closed child tickets: 44

Note: this epic has no open children, but the epic itself remains open because the acceptance criteria are not fully met.

### `loomcli-37h1h` - Migrate daemon config (loom.yaml DaemonSettings) to fleet-db

Priority: P2
Status: open

Follow-up to `loomcli-26v50`. Workspace state migration is mostly complete, but daemon-side `loom.yaml` settings still exist.

Known YAML/daemon consumers called out in the ticket:

- `daemon_cmd.go`
- `daemon.go`
- `daemon_reconciler.go`
- `fleet_mode.go`
- `doctor_checks.go`
- `workspace/setup.go`
- `migrate_http.go`
- `workspacemgr.CreateWorkspace`

Open child tickets: 0

Note: this should probably be broken down into implementation tasks before work continues.

## Open Actionable Tickets

### `loomcli-7w9tc.17` - Unify FleetBackend URL dialects (webui envelope + native)

Priority: P1
Type: task
Parent: `loomcli-7w9tc`

Problem: production `internal/backend/fleet/FleetBackend` speaks loom-webui paths with `{success,data}` envelopes, while real fleet-db serves `/api/v1/{workspace}/...` with bare JSON. The parity harness has a duplicate native fleet adapter, which means production FleetBackend has not been proven against real fleet-db.

Acceptance:

- FleetBackend handles both URL prefixes.
- Table-driven tests cover both response shapes.
- `internal/backend/paritytest/fleetadapter.go` is deleted.
- `make test-parity` passes using the production adapter end to end.

### `loomcli-7w9tc.18` - Paritytest coverage expansion: 19 unimplemented IssueBackend methods

Priority: P1
Type: task
Parent: `loomcli-7w9tc`

Problem: paritytest dispatch covers only part of the IssueBackend contract. Several methods either have no end-to-end coverage or rely on adapter shims.

Methods called out for coverage:

- `SearchIssues`
- `Batch`
- `Count`
- full `Stats`
- `GetMutations`
- `WaitForMutations`
- `AddDependency`
- `RemoveDependency`
- `AddLabel`
- `RemoveLabel`
- `ListComments`
- `AddComment`
- `ListEvents`
- `GetChildren`
- `DeferIssue`
- `UndeferIssue`
- `ClaimIssue`
- `Delete`
- `Blocked`

Acceptance:

- 15+ new fixtures.
- `DualRunner.executeStep` handles all methods.
- parity duplicate adapter is deleted after `loomcli-7w9tc.17`.
- report shows comparison coverage for every IssueBackend method.

### `loomcli-7w9tc.23` - Loom webui: auto-bootstrap workspace on `LOOM_SEED_WORKSPACE`

Priority: P2
Type: task
Parent: `loomcli-7w9tc`

Problem: the parity docker stack can start, but loom-fleet has no webui workspace at startup. The API cannot bootstrap the expected empty workspace without a repos array.

Acceptance:

- Setting `LOOM_SEED_WORKSPACE=PARITY` creates a workspace at startup if one does not exist.
- `test/parity/seed.sh` can post issues to loom-fleet without extra setup.
- `make test-parity-ui` runs all specs to completion.

### `loomcli-7w9tc.19` - File upstream fleet-db tickets: Batch atomicity, full Stats, cursor API

Priority: P2
Type: task
Parent: `loomcli-7w9tc`

Upstream tickets needed in fleet-db:

- Polymorphic batch endpoint with atomicity.
- Full stats aggregation: ready issues, epics eligible for closure, average lead time.
- Cursor API for mutations using Redis stream IDs without losing sequence precision.

This matters because loomcli currently has client-side shims that are not equivalent to server-side FleetDB behavior.

### `loomcli-d924z` - Make empty fleetdb stack rebuildable without beads copy bloat

Priority: P2
Type: bug

Problem: clean empty-stack validation hit Podman disk pressure because the parity Dockerfile copied `third_party/beads` even for a FleetDB-only loom image.

Acceptance direction:

- Add a fleet-only runtime Dockerfile or build target.
- Clean onboarding/e2e should not require beads artifacts.
- This should support fresh-user validation without seed data.

### `loomcli-7w9tc.22` - Pin fleet-db binary provenance

Priority: P3
Type: task
Parent: `loomcli-7w9tc`

Problem: Go subprocess parity and docker-compose parity can use different fleet-db builds without clear signal.

Acceptance direction:

- Single pinned fleet-db SHA/source.
- Go spawn and docker compose both use the same provenance.

### `loomcli-7w9tc.21` - Waiver lifecycle: permanent vs pending_fix

Priority: P3
Type: task
Parent: `loomcli-7w9tc`

Problem: parity waivers do not distinguish permanent architectural differences from temporary bug-covering waivers.

Acceptance direction:

- Add `waiver_class: permanent | pending_fix`.
- Require target fix ticket and expiry for pending fixes.
- Harness should fail when pending-fix waivers expire or their target closes.

### `loomcli-7w9tc.20` - Merge CLI + RPC paritytest harnesses

Priority: P3
Type: task
Parent: `loomcli-7w9tc`

Problem: paritytest has separate CLI and RPC harness paths with duplicated spawn, fixture loading, substitution, and diff logic.

Acceptance direction:

- Extract shared `Caller` abstraction.
- Keep backend and exec callers as implementations.
- Use one fixture bank, differ, and report format.

### `loomcli-7w9tc.16` - Decide `source_repo` drift fix side

Priority: P3
Type: bug
Parent: `loomcli-7w9tc`

Problem: beads returns `source_repo="."` from git workdir context, while FleetDB returns empty/null.

Options captured in ticket:

- FleetDB persists source repo from create request context.
- beads stops auto-populating source repo.
- waive the drift.
- loom adapter strips source repo from beads responses.

Recommendation captured in ticket: FleetDB should persist and echo repo context from create requests.

### `loomcli-7w9tc.13` - Run webui parity pass and write `webui-gaps.md`

Priority: P3
Type: task
Parent: `loomcli-7w9tc`

Goal: run manual parity checklist against both stacks and document gaps by view, step, backend behavior, severity, and owner.

Output target:

- `docs/design/parity-report-2026-04-22/webui-gaps.md`

### `loomcli-7w9tc.12` - Optional agent-browser script for automated webui parity

Priority: P4
Type: task
Parent: `loomcli-7w9tc`

Goal: convert manual parity checklist into an agent-browser/Playwright script that drives both webuis and asserts equivalence.

Targets:

- beads stack: `localhost:8081`
- fleet stack: `localhost:8082`

## Follow-Up Breakdown Needed

The following epics are blockers but do not currently have granular child tickets:

- `loomcli-26v50`
- `loomcli-37h1h`

Before implementation continues, split them into concrete tasks for:

- daemon settings schema/API in FleetDB
- loom FleetDB client methods for daemon settings
- daemon/reconciler/doctor/setup migration from YAML loaders to store interfaces
- local state cache cleanup
- deletion of `internal/cli/config/`
- deletion of `internal/cli/serve/workspacemgr/`
- removal of `gopkg.in/yaml.v3`
- quality gate proving no YAML reads/writes remain
- final no-beads/no-fallback validation path

## Closed Ticket Ledger

These closed tickets are included so future cleanup can check whether anything was prematurely closed, partially implemented, or superseded by later architecture decisions.

### Closed `loomcli-7w9tc` Children

- `loomcli-7w9tc.1` P1 task closed - P1.1 Run fleet-db parity harness with bd available
- `loomcli-7w9tc.2` P2 task closed - P1.2 Commit parity report artifacts to loomcli docs
- `loomcli-7w9tc.3` P2 task closed - P2.1 Scaffold internal/backend/paritytest/ with parity build tag
- `loomcli-7w9tc.4` P2 task closed - P2.2 Write fixtures for loomcli-extended IssueBackend methods
- `loomcli-7w9tc.5` P2 task closed - P2.3 Implement DualRunner: instantiate beads + fleet, execute fixtures
- `loomcli-7w9tc.6` P2 task closed - P2.4 Emit diff report in fleet-db-compatible format
- `loomcli-7w9tc.7` P3 task closed - P2.5 Add 'make test-parity' target to loomcli Makefile
- `loomcli-7w9tc.8` P3 task closed - P2.6 Generate loomcli-gaps.md from harness output
- `loomcli-7w9tc.9` P2 task closed - P3.1 Write test/parity/docker-compose.parity.yml
- `loomcli-7w9tc.10` P2 task closed - P3.2 Write test/parity/seed.sh fixture loader
- `loomcli-7w9tc.11` P3 task closed - P3.3 Write test/parity/browse.md webui parity checklist
- `loomcli-7w9tc.14` P2 bug closed - Loomcli beads adapter: closeResultToData returns null for closed-issue fields
- `loomcli-7w9tc.15` P2 bug closed - Loomcli beads adapter: classifies 'issue not found' as KindUnavailable instead of KindNotFound

### `loomcli-26v50` Children Not Closed

- `loomcli-26v50.29` P3 feature blocked - loomcli: PID-file + Unix-socket reuse for embedded fleet-db
- `loomcli-26v50.30` P3 chore blocked - Unify 'loom agent' (runner) with 'loom agentdef' (CRUD)

### Closed `loomcli-26v50` Children

- `loomcli-26v50.1` P1 feature closed - fleet-db: Add Agent as first-class entity
- `loomcli-26v50.2` P1 feature closed - fleet-db: Add Role as first-class entity
- `loomcli-26v50.3` P1 feature closed - fleet-db: Add DaemonProfile as first-class entity
- `loomcli-26v50.4` P1 feature closed - loomcli: Create internal/domain package (pure types)
- `loomcli-26v50.5` P1 feature closed - loomcli: Create internal/store package (repository interfaces)
- `loomcli-26v50.6` P1 feature closed - fleet-db: Add Repo as first-class entity
- `loomcli-26v50.7` P1 feature closed - loomcli: Create internal/bootstrap/mode.go (active workspace resolver)
- `loomcli-26v50.8` P1 feature closed - loomcli: Create internal/bootstrap/embedded.go (subprocess fleet-db lifecycle)
- `loomcli-26v50.9` P1 feature closed - loomcli: Create internal/bootstrap/statecache.go
- `loomcli-26v50.10` P1 feature closed - loomcli: Create internal/infra/fleetdb HTTP client (implements all stores)
- `loomcli-26v50.11` P1 task closed - loomcli: Rewrite workspacemgr to use store
- `loomcli-26v50.12` P1 task closed - loomcli: Update automode + agent runner to use store
- `loomcli-26v50.13` P1 task closed - loomcli: webui - rewrite svcimpl/workspace_service to use store
- `loomcli-26v50.14` P1 task closed - loomcli: Rewrite daemon agent reconciler to use store
- `loomcli-26v50.15` P1 task closed - loomcli: webui - rewrite svcimpl/agent_service to use store
- `loomcli-26v50.16` P1 task closed - loomcli: webui - replace ServerConfig closure bag with Store field
- `loomcli-26v50.17` P1 feature closed - loomcli: New CLI 'loom workspace' commands (add/list/use/remove/show/status)
- `loomcli-26v50.18` P1 feature closed - loomcli: New CLI 'loom daemon config' commands (per-workspace)
- `loomcli-26v50.19` P1 feature closed - loomcli: New CLI 'loom agent' commands (add/list/remove/start/stop/show)
- `loomcli-26v50.20` P1 feature closed - loomcli: New CLI 'loom role' commands (add/list/remove/show/set)
- `loomcli-26v50.21` P1 feature closed - loomcli: New CLI 'loom repo' commands (add/list/remove/show)
- `loomcli-26v50.22` P2 chore closed - loomcli: Update docs + READMEs (no more loom.yaml references)
- `loomcli-26v50.23` P2 chore closed - loomcli: Delete internal/cli/serve/workspacemgr/
- `loomcli-26v50.24` P2 chore closed - loomcli: Remove gopkg.in/yaml.v3 dependency from go.mod
- `loomcli-26v50.25` P2 chore closed - loomcli: Delete internal/cli/config/ (config.go, project.go, daemon.go)
- `loomcli-26v50.26` P2 feature closed - loomcli: Daemon auto-start with ownership filtering (cloud-ready)
- `loomcli-26v50.27` P3 feature closed - loom role set <key> <value>: per-field updater
- `loomcli-26v50.28` P3 feature closed - loom agentdef start/stop: integrate with daemon signal
- `loomcli-26v50.31` P3 feature closed - loom daemon profile: --unset / clear-field support
- `loomcli-26v50.32` P3 chore closed - loomcli: convert new CLI commands from Run to RunE
- `loomcli-26v50.33` P3 chore closed - localredis: bound stream snapshot size with XRevRangeN cap
- `loomcli-26v50.34` P3 chore closed - localredis: skip snapshot rewrite when keyspace unchanged
- `loomcli-26v50.35` P4 chore closed - Rebuild parity-test fleet-db container with current schema
- `loomcli-26v50.36` P4 chore closed - internal/netutil: promote pickFreePort + waitForHealthz
- `loomcli-26v50.37` P3 feature closed - fleet-db: PATCH/PUT endpoints should return canonical entity body
- `loomcli-26v50.38` P3 chore closed - Share fleet HTTP plumbing between internal/backend/fleet and internal/infra/fleetdb
- `loomcli-26v50.39` P4 bug closed - embedded fleet-db: fix 'waitid: no child processes' race on shutdown
- `loomcli-26v50.40` P2 bug closed - fleet-db: handleDaemonUpdate projector loses fields on PUT
- `loomcli-26v50.41` P3 bug closed - localredis: dirty-flag skip is no-op for embedded-CLI use
- `loomcli-26v50.42` P1 bug closed - loom serve /api/workspaces still reads yaml, not fleet-db store (Phase 4 incomplete)
- `loomcli-26v50.43` P2 task closed - Migrate remaining webui endpoints off multiPool/yaml (issues, agents detail, kanban)
- `loomcli-26v50.44` P2 task closed - Document fleet-db distributed control-plane architecture
- `loomcli-26v50.45` P2 task closed - Document distributed control-plane data model for review
- `loomcli-26v50.46` P2 task closed - Fix fleet-db workspace browser E2E parity

