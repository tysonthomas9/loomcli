# Frontend E2E Test Suite

End-to-end test that verifies the full loom daemon pipeline (plan → review → implement) by having agents build a React kanban app from task descriptions.

## Files

| File | Purpose |
|------|---------|
| `seed-issues.jsonl` | Beads export with 2 epics + 13 tasks (descriptions, priorities, parent-child deps) |
| `designs/` | Design reference screenshots (PNG) that agents read during implementation |
| `deps.conf` | Block dependency graph for implementation ordering |
| `setup.sh` | Creates test repo, imports seed issues, copies designs, generates env file |
| `test-frontend-e2e.sh` | 3-phase daemon test + verification (build, lint, visual tests) |
| `run-all.sh` | Orchestrates setup → test → teardown |
| `teardown.sh` | Cleans up test directory and processes |

## Running

```bash
# Full run (~40 min with real agents)
bash e2e/frontend/run-all.sh

# Keep test dir for inspection
bash e2e/frontend/run-all.sh --keep

# Manual steps
bash e2e/frontend/setup.sh /tmp/loom-frontend-e2e
source /tmp/loom-frontend-e2e/test-env.sh
bash e2e/frontend/test-frontend-e2e.sh
```

## Adding a Ticket

1. Create the task in any beads-initialized directory:
   ```bash
   bd create --title="Build new component" --type=task --priority=2 --description="Full description..."
   ```

2. Export the updated seed:
   ```bash
   bd export | python3 -c "
   import sys, json
   for line in sys.stdin:
       obj = json.loads(line)
       for field in ['design', 'assignee', 'close_reason', 'closed_at']:
           obj.pop(field, None)
       obj['status'] = 'open'
       if 'dependencies' in obj:
           obj['dependencies'] = [d for d in obj['dependencies'] if d['type'] == 'parent-child']
       print(json.dumps(obj, ensure_ascii=False))
   " > e2e/frontend/seed-issues.jsonl
   ```

3. Add dependencies to `deps.conf` (if any):
   ```
   new-component scaffold
   ```

4. Update `setup.sh` — add the title-to-slug mapping in the python block:
   ```python
   'Build new component':     ('TASK_NEW_COMPONENT', 'new-component'),
   ```

5. Update `test-frontend-e2e.sh`:
   - Add env var validation: `: "${TASK_NEW_COMPONENT:?...}"`
   - Add to `ALL_TASKS` array
   - Add expected files to the verification section

6. Commit all changes.

## How It Works

### Design Screenshots (`designs/`)

8 PNG screenshots captured from the design reference at `https://designs.magicpath.ai/v1/friendly-cliff-4837`. Visual task descriptions reference these files so agents can `Read` them as images to match the design:

| File | Content |
|------|---------|
| `full-page.png` | Full page at 1440x900 — overall layout |
| `sidebar.png` | Left sidebar — agent list + work queue |
| `agent-card.png` | Single agent card detail |
| `issue-card.png` | Issue card with priority badge + agent info |
| `column-header.png` | Kanban column header styling |
| `header-bar.png` | Top header with search + filters |
| `agent-detail-panel.png` | Agent detail modal |
| `task-detail-panel.png` | Task detail modal |

`setup.sh` copies these to the test directory so agents can access them at `/tmp/loom-frontend-e2e/designs/*.png`.

### Seed Issues (`seed-issues.jsonl`)

JSONL file exported from beads via `bd export`. Contains epics and tasks with full descriptions but no designs, assignments, or block dependencies. Visual component tasks reference design screenshots instead of hardcoded CSS values — agents derive styling by reading the PNGs. On import, beads renames IDs to match the test repo's prefix (`--rename-on-import`).

### Dependency Graph (`deps.conf`)

Block dependencies are stored separately because adding them at setup time would prevent parallel planning. The test script adds them between Phase 2 (review) and Phase 3 (implementation).

Format: `task-slug depends-on-slug` (one per line, `#` comments).

Current ordering:
```
scaffold → (7 components + quality) → (detail panels) → app → visual
```

### Test Phases

1. **Planning** — Daemon runs with `role: plan`. Agents pick up all 13 tasks in parallel and create designs. Waits for all tasks to reach `status=review`.

2. **Review** — Approves all plans (`status=open`). Adds block dependencies from `deps.conf` so implementation respects ordering.

3. **Implementation** — Daemon runs with `role: task`. Agents implement designs in dependency order. Waits for all tasks to reach `status=closed`.

4. **Verification** — Merges worktree branches, then checks: build (`npm run build`), expected files, component exports, lint (`npm run lint`), Playwright visual tests, no Go panics in logs.
