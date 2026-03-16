## WORKFLOW: Compute Parallel Execution Layers for an Epic

You are a build scheduler. Given an epic with tasks and dependencies, produce an optimal
parallel execution plan. The epic ID is in the EPIC_ID environment variable.

### Step 1: Gather Task Data

```bash
EPIC_ID="${EPIC_ID:?}"
for id in $(bd show "$EPIC_ID" --children 2>/dev/null | grep -oE "${EPIC_ID}\.[0-9]+"); do
  st=$(bd show "$id" --json 2>/dev/null | jq -r '.[0].status')
  deps=$(bd show "$id" --json 2>/dev/null | jq -r '[.[0].dependencies[]? | select(.dependency_type == "blocks") | .id] | join(",")')
  title=$(bd show "$id" --json 2>/dev/null | jq -r '.[0].title')
  echo "$id|$st|$deps|$title"
done
```

### Step 2: Compute Dependency Layers

1. Tasks whose dependencies are ALL closed go in Layer 0
2. Tasks whose dependencies are all in Layer 0 or closed go in Layer 1
3. Continue until all tasks are assigned

### Step 3: Assign Workers (4 available)

Within each layer, assign tasks to workers 1-4 to minimize file overlap.

### Step 4: Output layers.json

Write to `layers.json`:
```json
{
  "epic_id": "EPIC-ID",
  "base_branch": "branch-name",
  "num_workers": 4,
  "layers": [
    {
      "layer": 0,
      "conflict_risk": "LOW",
      "tasks": [
        {"id": "TASK-1", "worker": 1, "title": "...", "files_touched": ["path/a.go"]}
      ]
    }
  ],
  "skipped": [{"id": "TASK-X", "reason": "already closed"}]
}
```

Every open task must appear in exactly one layer. Respect ALL dependencies.
