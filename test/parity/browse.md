# Manual Webui Parity Checklist

Paired with `docker-compose.parity.yml` and `seed.sh`. After seeding,
open two browser tabs:

- **Reference side:** http://localhost:8081
- **Fleet side:** http://localhost:8082

Walk through the checklist below, comparing both tabs. For each step,
record pass/fail + screenshot reference in
`docs/design/parity-report-2026-04-22/webui-gaps.md`.

## ⚠ Pre-flight: backend sanity check (DO NOT SKIP)

Before running ANY visual comparison, prove that the two loom instances
are actually using different backends. Otherwise every "parity pass"
result is meaningless.

### 1. Config endpoint (most authoritative)

Each loom instance exposes its active issue backend via `/api/config`:

```bash
curl -s http://localhost:8081/api/config | jq -r '.issue_backend'
# Expected: "reference"

curl -s http://localhost:8082/api/config | jq -r '.issue_backend'
# Expected: "fleet"
```

If either returns a different value than expected, STOP. The compose is
misconfigured or a service fell back to a default.

### 2. Network-level verification (loom-fleet actually talks to fleet-db)

From inside the compose network, confirm loom-fleet's outbound issue
traffic hits fleet-db (not a local daemon):

```bash
# Tail fleet-db's access log while hitting loom-fleet's API
docker compose -f test/parity/docker-compose.parity.yml logs -f fleet-db &
LOGS_PID=$!
sleep 1

# Create an issue via loom-fleet's webui API
curl -s -X POST http://localhost:8082/api/issues \
    -H 'Content-Type: application/json' \
    -d '{"title":"sanity-check issue","issue_type":"task","priority":3}'

# Watch the fleet-db logs for an inbound request — should show a
# POST /api/v1/{ws}/issues line within a second.
sleep 2
kill $LOGS_PID
```

If fleet-db's logs show no activity, loom-fleet is NOT actually routing
through it. Likely a misconfiguration; check `LOOM_FLEET_URL` env var.

### 3. Same issue, both backends

Create an issue via each loom instance, then fetch it via the other side's
native listing. If both lists look identical, the two instances are
(wrongly) sharing state.

```bash
curl -s http://localhost:8081/api/issues | jq '.data | length'
curl -s http://localhost:8082/api/issues | jq '.data | length'
```

After seeding with identical fixtures both should show the same count —
but seeding is supposed to happen via TWO separate API calls (one per
backend). If both were populated by a single call, the stacks are bleeding
into each other. Inspect `seed.sh` to confirm dual-write.

### 4. Fleet-db workspace key

```bash
# Confirm fleet-db has the seeded workspace
curl -s http://localhost:8080/api/v1/admin/workspaces | jq '.data[] | .key'
# Expected: "PARITY"

# Confirm loom-fleet is pointing at that workspace
docker compose -f test/parity/docker-compose.parity.yml exec loom-fleet \
    printenv LOOM_WORKSPACE
# Expected: "PARITY"
```

### 5. Backend indicator in the UI

The webui Settings page (or status bar) should show the active backend
string. Both tabs' Settings should render a DIFFERENT value. If they
render the same, the frontend may not be reading from `/api/config`
correctly — file as a webui bug before walking the checklist.

**Only after all 5 sanity checks pass**, proceed.

## Kanban view

- [ ] Both tabs show 13 issues (3 epics + 10 children) in the correct
      swim lanes (open/in_progress/review/closed)
- [ ] Drag-drop on reference: move `Add login flow` → in_progress. Reload the
      tab. Status persists.
- [ ] Drag-drop on fleet: same action. Status persists.
- [ ] Ordering within a lane matches (priority then created_at desc)
- [ ] Filter by label: if labels applied via seed, both sides return the
      same set

## Table view

- [ ] Row count matches between tabs
- [ ] Sort by priority → same row order on both
- [ ] Sort by created_at desc → same row order
- [ ] Filter "type=bug" → same 3 bugs on both
- [ ] Pagination (if applicable): page 2 returns same rows

## Graph view

- [ ] Same number of nodes on both sides
- [ ] Same edges (dependencies + parent-child relations)
- [ ] Dragging layout is smooth on both (visual only)

## Monitor view

- [ ] Stats counters match within 1s (open, in_progress, blocked, closed)
- [ ] Ready-queue top 5 matches
- [ ] Blocked-by counts match

## Settings

- [ ] Backend selector on reference tab shows `reference` as active
- [ ] Backend selector on fleet tab shows `fleet` as active
- [ ] Switching projects works on both (if seeded multiple workspaces)

## SSE / realtime

- [ ] Create an issue in one reference tab; the second reference tab sees it
      within 2s
- [ ] Same test on fleet side
- [ ] Close an issue in reference; kanban reshuffles live
- [ ] Same on fleet

## Issue detail view

- [ ] Same fields visible on both sides (title, description, priority,
      type, labels, assignee, owner, created_by, created_at,
      updated_at, close_reason when closed)
- [ ] Comments display in the same order
- [ ] Dependencies list matches
- [ ] Children (for epics) match

## Record findings

For each FAIL, capture:

```
View | Step | Reference behavior | Fleet behavior | Severity | Owner
```

Severity buckets:
- **blocker** — user workflow broken on fleet side
- **annoying** — cosmetic / minor but noticeable
- **cosmetic** — invisible to most users

Owner: fleet-db (upstream feature work), loomcli (adapter), accept (waiver).

Append findings to
`docs/design/parity-report-2026-04-22/webui-gaps.md`.
