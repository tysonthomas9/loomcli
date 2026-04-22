# Manual Webui Parity Checklist

Paired with `docker-compose.parity.yml` and `seed.sh`. After seeding,
open two browser tabs:

- **Beads side:** http://localhost:8081
- **Fleet side:** http://localhost:8082

Walk through the checklist below, comparing both tabs. For each step,
record pass/fail + screenshot reference in
`docs/design/parity-report-2026-04-22/webui-gaps.md`.

## Kanban view

- [ ] Both tabs show 13 issues (3 epics + 10 children) in the correct
      swim lanes (open/in_progress/review/closed)
- [ ] Drag-drop on beads: move `Add login flow` → in_progress. Reload the
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

- [ ] Backend selector on beads tab shows `beads` as active
- [ ] Backend selector on fleet tab shows `fleet` as active
- [ ] Switching projects works on both (if seeded multiple workspaces)

## SSE / realtime

- [ ] Create an issue in one beads tab; the second beads tab sees it
      within 2s
- [ ] Same test on fleet side
- [ ] Close an issue in beads; kanban reshuffles live
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
View | Step | Beads behavior | Fleet behavior | Severity | Owner
```

Severity buckets:
- **blocker** — user workflow broken on fleet side
- **annoying** — cosmetic / minor but noticeable
- **cosmetic** — invisible to most users

Owner: fleet-db (upstream feature work), loomcli (adapter), accept (waiver).

Append findings to
`docs/design/parity-report-2026-04-22/webui-gaps.md`.
