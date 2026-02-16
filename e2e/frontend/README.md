# Frontend E2E Test Suite

End-to-end test that verifies the full loom daemon pipeline (plan -> review -> implement) by having agents build a React kanban app from task descriptions.

## Files

| File | Purpose |
|------|---------|
| `issues/` | Individual YAML files -- one per epic/task (15 files + `_defaults.yaml`) |
| `designs/` | Design reference screenshots (PNG) that agents read during implementation |
| `deps.conf` | Block dependency graph for implementation ordering |
| `setup.sh` | Creates test repo, converts YAML to JSONL, imports issues, copies designs |
| `test-frontend-e2e.sh` | 3-phase daemon test + verification (build, lint, visual tests) |
| `run-all.sh` | Orchestrates setup -> test -> teardown |
| `teardown.sh` | Cleans up test directory and processes |

## Running

```bash
# Full run (~40 min with real agents)
bash e2e/frontend/run-all.sh

# Custom test dir
bash e2e/frontend/run-all.sh /tmp/my-test-dir

# Clean up after test (default: keep for inspection)
bash e2e/frontend/run-all.sh --clean

# Manual steps
bash e2e/frontend/setup.sh /tmp/loom-frontend-e2e
source /tmp/loom-frontend-e2e/test-env.sh
bash e2e/frontend/test-frontend-e2e.sh
```

## Seed Issues (`issues/`)

Each epic and task lives in its own YAML file under `issues/`. This makes individual issues easy to edit, review, and diff.

### Directory layout

```
issues/
  _defaults.yaml   # shared fields (status, owner, created_by)
  9xi.yaml         # Epic: Project Foundation & Layout
  ou9.yaml         # Epic: Kanban Board Components
  9xi.1.yaml       # Task: Initialize React+Vite+TypeScript scaffold
  9xi.2.yaml       # Task: Create mock data module
  ...              # (13 task files total)
```

### File format

**`_defaults.yaml`** -- shared fields applied to all issues during JSONL conversion:
```yaml
status: open
owner: tysonthomas9@users.noreply.github.com
created_by: Tyson Thomas
```

**Epic** (no dot in ID):
```yaml
id: 9xi
title: Project Foundation & Layout
priority: 1
```

**Task** (dot in ID -- parent epic inferred from prefix):
```yaml
id: ou9.5
title: Implement AgentList sidebar with agent cards
priority: 2
description: |-
  Multi-line description rendered cleanly.
  No \n escaping needed.

  ## Visual Design Reference
  Read $TEST_DIR/designs/screenshot.png - description of what it shows
```

### Key features

- **One file per issue** -- clean git diffs, easy to add/remove
- **Block scalar descriptions** (`|-`) -- multi-line text without `\n` escaping
- **`$TEST_DIR` placeholder** -- expanded to actual test directory path during JSONL conversion
- **Type inferred from ID format** -- dot in ID = task (e.g. `ou9.5`), no dot = epic (e.g. `ou9`)
- **Parent-child deps from ID prefix** -- `ou9.5` automatically depends on epic `ou9`

`setup.sh` globs `issues/*.yaml`, converts to JSONL via PyYAML (installed automatically if missing), and pipes to `bd import`.

## Adding a Task

1. Create a new file in `issues/` named `{id}.yaml`:
   ```yaml
   id: ou9.8
   title: Build new component
   priority: 2
   description: |-
     Full description here...

     ## Visual Design Reference
     Read $TEST_DIR/designs/relevant-screenshot.png - what it shows
   ```

2. Add dependencies to `deps.conf` (if any):
   ```
   new-component scaffold
   ```

3. Update `setup.sh` -- add the title-to-slug mapping in the python block:
   ```python
   'Build new component':     ('TASK_NEW_COMPONENT', 'new-component'),
   ```

4. Update `test-frontend-e2e.sh`:
   - Add env var validation: `: "${TASK_NEW_COMPONENT:?...}"`
   - Add to `ALL_TASKS` array
   - Add expected files to the verification section

5. Commit all changes.

## Design Screenshots (`designs/`)

8 PNG screenshots captured from the design reference at `https://designs.magicpath.ai/v1/friendly-cliff-4837`. Task descriptions reference these via `$TEST_DIR/designs/*.png` so agents can `Read` them as images.

| File | Content |
|------|---------|
| `full-page.png` | Full page at 1440x900 -- overall layout |
| `sidebar.png` | Left sidebar -- agent list + work queue |
| `agent-card.png` | Single agent card detail |
| `issue-card.png` | Issue card with priority badge + agent info |
| `column-header.png` | Kanban column header styling |
| `header-bar.png` | Top header with search + filters |
| `agent-detail-panel.png` | Agent detail modal |
| `task-detail-panel.png` | Task detail modal |

`setup.sh` copies these to `$TEST_DIR/designs/` so agents can access them.

## Dependency Graph (`deps.conf`)

Block dependencies are stored separately because adding them at setup time would prevent parallel planning. The test script adds them between Phase 2 (review) and Phase 3 (implementation).

Format: `task-slug depends-on-slug` (one per line, `#` comments).

Current ordering:
```
scaffold -> (7 components + quality) -> (detail panels) -> app -> visual
```

## Test Phases

1. **Planning** -- Daemon runs with `role: plan`. Agents pick up all 13 tasks in parallel and create designs. Waits for all tasks to reach `status=review`.

2. **Review** -- Approves all plans (`status=open`). Adds block dependencies from `deps.conf` so implementation respects ordering.

3. **Implementation** -- Daemon runs with `role: task`. Agents implement designs in dependency order. Waits for all tasks to reach `status=closed`.

4. **Verification** -- Merges worktree branches, then checks: build (`npm run build`), expected files, component exports, lint (`npm run lint`), Playwright visual tests, no Go panics in logs.
