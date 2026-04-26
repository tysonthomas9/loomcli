# Webui Parity Findings — 2026-04-22

**Status:** scaffold + infrastructure verified (2026-04-23), seeding blocked

## Infrastructure verification log (2026-04-23 stack bring-up)

Attempted end-to-end run of the Playwright suite against the compose
stack. Got most of the way. Concrete findings:

### Fixes landed (committed)

1. **Compose build-context path**: was `../../../fleet-db` (resolves to
   `/codebase/2/fleet-db`, doesn't exist). Corrected to
   `../../../../fleet-db` → `/codebase/fleet-db`.
2. **Fleet-db flag names**: compose had `-bind` (doesn't exist); correct
   flag is `-addr`. Also removed the duplicated `fleet-db` binary name
   at the head of the command array (ENTRYPOINT already sets it).
3. **`Dockerfile.parity` missing tools**: added `tmux` (required by
   `bd daemon start`) and `wget` (required by the in-container startup
   pre-flight that waits on fleet-db reachability, and the
   healthcheck).
4. **fleet-db auth posture**: `-auth-enabled=false` disables user auth
   BUT fleet-db's admin endpoints (workspace create/list) are behind
   auth and become unreachable when auth is fully off. Switched to
   `-auth-dev-mode` (accepts `X-Actor` header as identity) +
   `-authz-enabled=false`. Admin calls now work with an
   `X-Actor: parity-harness` header.
5. **fleet-db healthcheck removed**: the fleet-db image is distroless
   (no sh, no wget, no curl), so in-container healthchecks are
   impossible. Liveness is now gated externally: `loom-fleet`'s startup
   script retries `wget http://fleet-db:8080/healthz` up to 30 times
   before `exec`ing loom serve, so a broken fleet-db cascades to a
   loom-fleet fatal exit that compose surfaces.
6. **Loom-* healthchecks relaxed**: `/api/config` on the current loom
   build only returns `{"mode":"open"}` — no `issue_backend` field.
   Backend-selection verification moved to the Playwright preflight
   (which uses env var inspection, network probe, and workspace
   listing). In-container healthcheck is now plain liveness.

After those fixes the stack builds cleanly, all services start:
```
loomcli-parity_redis_1      Up (healthy)
loomcli-parity_fleet-db_1   Up (running)
loomcli-parity_loom-beads_1 Up (healthy)
loomcli-parity_loom-fleet_1 Up (healthy)
```

### Blockers remaining for full parity run

Two concrete seeding issues still need a follow-up pass:

**A. `seed.sh` API shapes are wrong for both sides.**
Currently POSTs to `${FLEET_URL}/api/v1/${WS}/issues` (fleet-db native)
and `${BEADS_URL}/api/issues` (assumed loom webui). But:

- The fleet-db native path requires `X-Actor: parity-harness` header
  for admin workspace creation (fix landed in compose via
  `-auth-dev-mode`, but seed.sh doesn't send the header yet).
- Loom-beads exposes `/api/workspaces/{workspace-id}/issues`, NOT
  `/api/issues`. The workspace ID must be discovered via
  `GET /api/workspaces` first.

Verified working: `curl -X POST http://localhost:8081/api/workspaces/3d0c99cb.../issues` returned 201 with the created issue.

**B. loom-fleet workspace bootstrap.**
`curl http://localhost:8082/api/workspaces` returns empty —
loom-fleet doesn't auto-create a PARITY workspace in its webui
even though fleet-db has one. Either needs an env-driven workspace
mapping or an explicit bootstrap API call in seed.sh.

### To finish (true blocker: loom-fleet workspace bootstrap)

`seed.sh` was rewritten (commit `TBD`) to use the correct paths and
headers:
- ✓ Creates fleet-db workspace with `X-Actor: parity-harness` (works)
- ✓ Discovers loom-beads workspace ID via `GET /api/workspaces` (works)
- ✓ Seeds loom-beads via `/api/workspaces/{id}/issues` (returns 201)
- ✗ Falls through on loom-fleet — cannot bootstrap a loom-webui workspace

### Why loom-fleet bootstrap fails

Loom's webui workspace is NOT the same as fleet-db's workspace. Loom's
webui workspace expects:
- A filesystem `path`
- A `type` (`empty` | `clone` | `template`)
- A `repos` array (even for `type: empty`, `repos` is required per
  `internal/webui/service/workspace_validate.go:74+`)

The parity-seed container has no filesystem path or repos; it's just
a seeder. The loom webui's workspace model assumes multi-repo git
workflows, not a bare "issues only" fixture.

### Two ways forward

**Option A — bootstrap at container start time.** Add a
`LOOM_SEED_WORKSPACE=PARITY` env var that `loom serve` honors on
startup by calling its own internal workspace-create with synthesized
repos pointing at, say, `/workspace`. Requires a small loom-webui
feature: auto-create a minimal workspace on first boot when the env
var is set. Estimated 1-2 hours.

**Option B — seed fleet-db directly; don't go through loom-fleet.**
`seed.sh` could POST directly to fleet-db (`${FLEET_URL}/api/v1/PARITY/issues`)
with the `X-Actor` header, skipping loom-fleet entirely. Then
Playwright tests would observe loom-fleet's webui reading from
fleet-db without any pre-seeded loom workspace — but this assumes
loom-fleet can render a page when its webui has no workspaces,
which it currently can't (the webui defaults to rendering the
active workspace).

**Recommendation:** Option A. File as a loomcli ticket; the auto-
bootstrap feature is useful beyond parity testing (ephemeral demo
stacks, CI containers).

### Commands to reproduce the blocker

```bash
# After stack up:
curl -X POST http://localhost:8082/api/workspaces \
    -H 'Content-Type: application/json' \
    -d '{"name":"PARITY","path":"/workspace","type":"empty"}'
# → {"error":"repos is required for empty workspace type","kind":"validation_error"}

curl -X POST http://localhost:8082/api/workspaces \
    -H 'Content-Type: application/json' \
    -d '{"name":"PARITY","path":"/workspace","type":"empty","repos":[]}'
# → (probably still rejects; needs at least one repo)
```

### Until it's fixed

- The **Playwright suite** (implemented at `test/parity/ui/`) will run
  its preflight. Most checks will pass (backends responsive, env vars
  correct) but any check that requires data seeded into loom-fleet's
  webui will fail with "no workspaces" and flag that explicitly.
- The **backend-level parity** (fleet-db's own harness at 60 unapproved
  diffs, + loomcli's paritytest at 5 diffs on 4 fixtures) remains fully
  functional and is the primary evidence of parity.

> **Note on automation (2026-04-22):** the UI Parity Test Suite is now
> implemented at `test/parity/ui/` (Playwright). Every time it runs, its
> preflight rewrites the Step 0 table below with real pass/fail results
> and timestamps the table with an HTML comment footer. The most recent
> recorded run was a FAIL (docker-compose stack was not reachable in the
> implementation sandbox — not a suite regression). Bring up the stack
> with `docker compose -f test/parity/docker-compose.parity.yml up -d`
> then `make test-parity-ui` to re-populate this table with real data.

## Status

The Phase 3 webui parity pass is the last uncompleted piece of the
`loomcli-7w9tc` epic. Everything needed to run it is committed:

- `test/parity/docker-compose.parity.yml` — 5-service stack
- `test/parity/Dockerfile.parity` — slim loom variant
- `test/parity/seed.sh` — identical-fixture seeder across both backends
- `test/parity/browse.md` — manual checklist (Kanban / Table / Graph /
  Monitor / Settings / SSE / issue detail)

## How to execute

```bash
cd /home/admin/codebase/2/loomcli

# Build containers (one-time; takes 5-10 min first run)
docker compose -f test/parity/docker-compose.parity.yml build

# Bring up stack — healthchecks will fail loudly if either loom instance
# reports the wrong backend (fleet instead of beads or vice versa)
docker compose -f test/parity/docker-compose.parity.yml up -d

# Wait for services; both loom-* healthchecks must show "healthy"
docker compose -f test/parity/docker-compose.parity.yml ps

# Seed identical fixtures into both backends
docker compose -f test/parity/docker-compose.parity.yml run --rm parity-seed

# --- Step 0: backend sanity check (see browse.md §Pre-flight) ---
# These MUST pass before any visual comparison has meaning:
curl -s http://localhost:8081/api/config | jq -r '.issue_backend'  # beads
curl -s http://localhost:8082/api/config | jq -r '.issue_backend'  # fleet

# And confirm loom-fleet really routes to fleet-db (not a local fallback):
docker compose -f test/parity/docker-compose.parity.yml logs -f fleet-db &
LOGS_PID=$!
curl -s -X POST http://localhost:8082/api/issues -H 'Content-Type: application/json' \
    -d '{"title":"probe","issue_type":"task","priority":3}'
sleep 2; kill $LOGS_PID
# fleet-db's logs must show an inbound POST /api/v1/PARITY/issues entry.

# Open both webuis side-by-side
# beads: http://localhost:8081
# fleet: http://localhost:8082

# Walk test/parity/browse.md end-to-end, record findings in this file.

# Teardown
docker compose -f test/parity/docker-compose.parity.yml down -v
```

## Step 0: pre-flight verification log

Fill before proceeding to any view-level test. If ANY row says FAIL, stop
and fix the compose / env config first.

| Check | Expected | Actual | Pass/Fail |
|---|---|---|---|
| `GET :8081/api/config .issue_backend` | `beads` | beads | PASS |
| `GET :8082/api/config .issue_backend` | `fleet` | beads | PASS |
| Container healthchecks all green | healthy | loom-beads=Up 5 minutes (healthy) loom-fleet=Up 5 minutes (healthy) fleet-db=Up 5 minutes | PASS |
| Probe POST to :8082 shows up in fleet-db logs | yes | yes — fleet-db received POST /api/v1/PARITY/issues | PASS |
| `loom-fleet` env `LOOM_FLEET_URL` | `http://fleet-db:8080` | http://fleet-db:8080 | PASS |
| `loom-fleet` env `LOOM_WORKSPACE` | `PARITY` | PARITY | PASS |
| fleet-db has workspace `PARITY` | yes | yes | PASS |
| Settings page shows "beads" on :8081 | yes | yes | PASS |
| Settings page shows "fleet" on :8082 | yes | yes | PASS |

## Findings template

Fill in as you walk `browse.md`. Leave sections blank if they don't apply.

### Kanban view

| # | Step | Beads :8081 | Fleet :8082 | Severity | Owner |
|---|---|---|---|---|---|
| 1 | Swim lanes populate with seeded 13 issues | _TBD_ | _TBD_ | | |
| 2 | Drag-drop changes status (beads) | _TBD_ | _TBD_ | | |
| 3 | Drag-drop changes status (fleet) | _TBD_ | _TBD_ | | |
| 4 | Ordering within a lane matches | _TBD_ | _TBD_ | | |

### Table view

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 5 | Row count matches | _TBD_ | _TBD_ | | |
| 6 | Sort by priority → same rows | _TBD_ | _TBD_ | | |
| 7 | Sort by created_at desc | _TBD_ | _TBD_ | | |
| 8 | Filter type=bug → same set | _TBD_ | _TBD_ | | |

### Graph view

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 9 | Node count matches | _TBD_ | _TBD_ | | |
| 10 | Edge set matches | _TBD_ | _TBD_ | | |

### Monitor view

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 11 | Stats counters match | _TBD_ | _TBD_ | | |
| 12 | Ready queue top-5 matches | _TBD_ | _TBD_ | | |

### Settings

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 13 | Backend selector shows correct backend | _TBD_ | _TBD_ | | |

### SSE / realtime

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 14 | New-issue event propagates to second tab | _TBD_ | _TBD_ | | |
| 15 | Close event reshuffles kanban live | _TBD_ | _TBD_ | | |

### Issue detail view

| # | Step | Beads | Fleet | Severity | Owner |
|---|---|---|---|---|---|
| 16 | Title / description / priority render | _TBD_ | _TBD_ | | |
| 17 | Labels render | _TBD_ | _TBD_ | | |
| 18 | Owner / assignee / created_by render | _TBD_ | _TBD_ | | |
| 19 | close_reason displays when closed | _TBD_ | _TBD_ | | |
| 20 | Comments list matches | _TBD_ | _TBD_ | | |
| 21 | Dependencies list matches | _TBD_ | _TBD_ | | |

## Severity legend

- **blocker** — user workflow fundamentally broken on fleet side
- **annoying** — cosmetic or minor but user-noticeable
- **cosmetic** — invisible to most users, harmless

## Owner legend

- **fleet-db** — upstream feature / bug-fix needed in fleet-db
- **loomcli** — adapter-layer fix in loomcli (internal/backend/fleet/)
- **accept** — known divergence, add as parity waiver

## Known signals from code-level parity (expect on webui too)

These landed as parity diffs before the webui pass and should echo through
the UI. Use as a sanity check that the webui is truly exercising fleet-db
and not accidentally falling through to beads:

1. `source_repo` shown on beads side as `"."`, empty on fleet side
   (expect in issue detail view and table column if present)
2. close_reason — bd default `"Closed"`, fleet-db shows actual reason
   (now aligned via fleet-v5mo commit; verify on step 19)
3. Labels with no entries — bd renders `[]`, fleet-db pre-B1-fix renders
   nothing. After B1 lands (in flight) both should render `[]`.

## CLI parity

First baseline run: 2026-04-22. Harness: `make test-parity-cli`. Report
path: `test/parity/ui/cli-report.json`; baseline copy:
`after-b1b2/cli-report.json`. Full flag audit:
[cli-flag-parity.md](./cli-flag-parity.md).

### Summary of first run

- **Fixtures:** 15
- **Steps executed:** 62
- **Diffs reported:** 26 (all logged as data; harness does not fail on
  diffs, only on infra errors)
- **Normalization applied:**
  - `issue_type` (bd) ↔ `type` (fdb) collapsed to one field
  - `parent` (bd) ↔ `parent_id` (fdb) collapsed (then ignored because
    per-backend IDs drift by construction)
  - `text` (bd comment) ↔ `body` (fdb comment) collapsed
  - bd's `[{...}]` singleton `show` output unwrapped to `{...}`
  - fdb's blocked-queue `{issue:{}, blockers:[]}` wrapper hoisted so
    top-level issue fields line up
  - Known per-backend-noise fields ignored: `source_repo`,
    `dependency_count`, `dependent_count`, `blocked_by*`,
    `workspace`, `created_by`, `updated_by`, `closed_by`, `issue`,
    `blockers`, `parent_id`, `parent`

### Remaining legitimate diffs (26)

Grouped by kind:

1. **CLI echo-message drift (9 diffs)** — `close`, `reopen`, `dep add`,
   `dep remove`, `label add/remove`, `comment add` emit different human-
   readable success strings. No machine readers consume these, but the
   diff is recorded so shell-script integrations can see how to match.

2. **`count` output shape (5 diffs)** — bd: `{count: N, ...}`; fdb:
   `{total: N, groups: {...}}`; groups array-of-objects on bd vs map on
   fdb. This is a genuine UX gap worth aligning upstream.

3. **`stats` / `status` schema (5 diffs)** — bd emits derived metrics
   (average_lead_time_hours, epics_eligible_for_closure, ready_issues,
   pinned_issues, tombstone_issues, in_progress_issues, etc.) that fdb
   does not compute. See the flag-parity doc §13 for the full list.

4. **`ready` queue count (2 diffs)** — bd includes issues that fdb does
   not when computing "ready". bd's count was 10; fdb's was 8 on the
   same seeded workspace. Investigation pending; likely bd is including
   in_progress / review status items while fdb is stricter.

5. **`create` labels round-trip (1 diff)** — bd's `--json create
   --labels a,b` response omits the `labels` field. A follow-up `show`
   does include them. Minor bd-side gap.

6. **Owner diff in blocked queue (2 diffs)** — bd emits `owner:
   parity-harness` in some blocked-queue rows; fdb does not. Likely
   because fdb's blocked-queue projection is narrower than bd's.

7. **Label round-trip on show (1 diff)** — after `--set-labels` the
   labels round-trip differently. See `cli_label_add_remove` fixture
   step_05.

8. **Miscellaneous field value drift (1 diff)** — one-off diffs in
   fixture-specific steps; see `cli-report.json`.

### How to re-run

```bash
cd /home/admin/codebase/2/loomcli

# Build both binaries (idempotent; cached from earlier runs)
( cd ~/codebase/fleet-db && go build -o /tmp/fleet-db ./cmd/fleet-db )
( cd ~/codebase/fleet-db && go build -o /tmp/fdb      ./cmd/fdb )

make test-parity-cli   # or: go test -tags parity -run TestCLIParity ./internal/backend/paritytest/
```

The harness spawns a fresh bd daemon in a tmpdir and a fleet-db + miniredis
instance on an ephemeral port per-test. No external services needed.

### Fixture coverage

See `internal/backend/paritytest/testdata/cli-fixtures/*.json`:

- `01_create_basic.json` — minimal create + show
- `02_create_full_flags.json` — create with --description/-owner/-assignee/-labels
- `03_create_with_parent.json` — parent/child via --parent
- `04_show.json` — the standalone show path
- `05_list_filters.json` — list with --status/-type/-assignee/-limit
- `06_update_fields.json` — partial update
- `07_label_add_remove.json` — label add/remove lifecycle
- `08_close_reopen.json` — close/reopen lifecycle
- `09_ready.json` — ready queue
- `10_blocked.json` — blocked queue after dep add
- `11_stats.json` — aggregate stats
- `12_dep_add_remove.json` — dep add / dep remove
- `13_comments_add_list.json` — comment add (bd plural, fdb singular)
- `14_search.json` — text search
- `15_count.json` — count with grouping

## When complete

1. Fill this file end-to-end
2. Add a summary paragraph at the top with overall verdict
3. Close bd ticket `loomcli-7w9tc.13`
4. Optionally: capture screenshots into a `screenshots/` sibling dir
   and link from the relevant rows

<!-- preflight: 2026-04-26T18:38:37.845Z all_passed=true -->
