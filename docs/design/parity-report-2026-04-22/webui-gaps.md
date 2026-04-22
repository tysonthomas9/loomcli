# Webui Parity Findings — 2026-04-22

**Status:** scaffold ready, execution pending operator

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
| `GET :8081/api/config .issue_backend` | `beads` | UNREACHABLE | FAIL |
| `GET :8082/api/config .issue_backend` | `fleet` | UNREACHABLE | FAIL |
| Container healthchecks all green | healthy | loom-beads=(not found) loom-fleet=(not found) fleet-db=(not found) | FAIL |
| Probe POST to :8082 shows up in fleet-db logs | yes | probe failed | FAIL |
| `loom-fleet` env `LOOM_FLEET_URL` | `http://fleet-db:8080` | docker exec failed | FAIL |
| `loom-fleet` env `LOOM_WORKSPACE` | `PARITY` | docker exec failed | FAIL |
| fleet-db has workspace `PARITY` | yes | admin/workspaces unreachable | FAIL |
| Settings page shows "beads" on :8081 | yes | UNREACHABLE | FAIL |
| Settings page shows "fleet" on :8082 | yes | UNREACHABLE | FAIL |

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

## When complete

1. Fill this file end-to-end
2. Add a summary paragraph at the top with overall verdict
3. Close bd ticket `loomcli-7w9tc.13`
4. Optionally: capture screenshots into a `screenshots/` sibling dir
   and link from the relevant rows

<!-- preflight: 2026-04-22T19:31:15.401Z all_passed=false -->
