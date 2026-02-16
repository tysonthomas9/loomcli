#!/usr/bin/env bash
# setup.sh — Create an isolated test environment for the frontend E2E test.
#
# Initializes git+beads, creates worktrees, writes loom configs, and imports
# seed issues from issues/ directory (2 epics + 13 tasks).
#
# Usage: ./setup.sh [test_dir]
#   test_dir defaults to /tmp/loom-frontend-e2e

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="${1:-/tmp/loom-frontend-e2e}"

if [ -d "$TEST_DIR" ]; then
  echo "!! $TEST_DIR already exists. Remove it first or choose another path."
  echo "   Run: rm -rf $TEST_DIR"
  exit 1
fi

# Validate issue files exist
ISSUES_DIR="$SCRIPT_DIR/issues"
if [ ! -d "$ISSUES_DIR" ]; then
  echo "FATAL: $ISSUES_DIR not found"
  exit 1
fi
if [ ! -f "$ISSUES_DIR/_defaults.yaml" ]; then
  echo "FATAL: $ISSUES_DIR/_defaults.yaml not found"
  exit 1
fi

# Check PyYAML is available
if ! python3 -c "import yaml" 2>/dev/null; then
  echo "Installing PyYAML..."
  pip3 install --quiet pyyaml
fi

# Convert individual YAML issue files → JSONL, expanding $TEST_DIR and filling defaults
SEED_FILE=$(mktemp)
if ! python3 -c "
import yaml, json, sys, datetime, glob, os

issues_dir = sys.argv[1]
test_dir = sys.argv[2]

with open(os.path.join(issues_dir, '_defaults.yaml')) as f:
    defaults = yaml.safe_load(f)

now = datetime.datetime.now().astimezone().isoformat()
prefix = 'loom-seed-gen-'

for fpath in sorted(glob.glob(os.path.join(issues_dir, '*.yaml'))):
    if os.path.basename(fpath).startswith('_'):
        continue
    with open(fpath) as f:
        item = yaml.safe_load(f)

    item_id = str(item['id'])
    is_task = '.' in item_id
    full_id = prefix + item_id

    obj = {
        'id': full_id,
        'title': item['title'],
        'status': defaults['status'],
        'priority': item['priority'],
        'issue_type': 'task' if is_task else 'epic',
        'owner': defaults['owner'],
        'created_at': now,
        'created_by': defaults['created_by'],
        'updated_at': now,
    }

    if is_task:
        desc = item.get('description', '').replace('\$TEST_DIR', test_dir)
        parent_id = prefix + item_id.rsplit('.', 1)[0]
        obj['description'] = desc
        obj['dependencies'] = [{
            'issue_id': full_id,
            'depends_on_id': parent_id,
            'type': 'parent-child',
            'created_at': now,
        }]

    print(json.dumps(obj, ensure_ascii=False))
" "$ISSUES_DIR" "$TEST_DIR" > "$SEED_FILE"; then
  echo "FATAL: Failed to convert issue files to JSONL"
  exit 1
fi

echo "==> Creating test repo at $TEST_DIR"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

# ============================================================
# Section 1: Initialize Git + Beads
# ============================================================

git init -q
git commit --allow-empty -q -m "initial commit"
bd init -q

# ============================================================
# Section 2: Create Worktrees
# ============================================================

git worktree add -q ./worktrees/falcon -b falcon
git worktree add -q ./worktrees/nova -b nova

# ============================================================
# Section 3: Loom Configs
# ============================================================

cat > loom.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: task
    auto: true
EOF

cat > loom-plan.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: plan
    auto: true
  - worktree: nova
    role: plan
    auto: true
EOF

cat > loom-task.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: task
    auto: true
EOF

mkdir -p .loom/logs

# ============================================================
# Section 4: Import Seed Issues
# ============================================================

echo "==> Importing seed issues from $SEED_FILE"
IMPORT_LOG=".loom/logs/import.log"
if ! bd import -i "$SEED_FILE" --rename-on-import 2>&1 | tee "$IMPORT_LOG"; then
  echo "FATAL: bd import failed. Log:"
  cat "$IMPORT_LOG"
  exit 1
fi

ISSUE_COUNT=$(bd list --json 2>/dev/null | jq 'length' 2>/dev/null || echo 0)
TASK_COUNT=$(bd list --json 2>/dev/null | jq '[.[] | select(.issue_type != "epic")] | length' 2>/dev/null || echo 0)
EPIC_COUNT=$((ISSUE_COUNT - TASK_COUNT))
echo "  Imported $EPIC_COUNT epics + $TASK_COUNT tasks"

EXPECTED_LINES=$(wc -l < "$SEED_FILE" | tr -d ' ')
if [ "$ISSUE_COUNT" -lt "$EXPECTED_LINES" ]; then
  echo "WARN: Imported $ISSUE_COUNT issues but JSONL has $EXPECTED_LINES lines"
fi
if [ "$ISSUE_COUNT" -eq 0 ]; then
  echo "FATAL: No issues imported"
  exit 1
fi

# ============================================================
# Section 4.5: Copy Design Assets
# ============================================================

DESIGNS_SRC="$SCRIPT_DIR/designs"
DESIGNS_DST="$TEST_DIR/designs"
if [ -d "$DESIGNS_SRC" ]; then
  echo "==> Copying design screenshots to $DESIGNS_DST"
  cp -r "$DESIGNS_SRC" "$DESIGNS_DST"
  echo "  Copied $(ls "$DESIGNS_DST"/*.png 2>/dev/null | wc -l | tr -d ' ') design images"
else
  echo "WARN: Design screenshots not found at $DESIGNS_SRC"
fi

# ============================================================
# Section 5: Generate Env File + Slug Map
# ============================================================

ENV_FILE="$TEST_DIR/test-env.sh"
SLUG_MAP="$TEST_DIR/slug-to-id.map"

echo "export TEST_DIR=\"$TEST_DIR\"" > "$ENV_FILE"

# IDs are renamed on import, so read from bd list (not the seed file) to get actual IDs
bd list --json 2>/dev/null | python3 -c "
import sys, json

# Title prefix → (env var name, slug)
mapping = {
    'Project Foundation':        ('EPIC_1',             None),
    'Kanban Board Components':   ('EPIC_2',             None),
    'Initialize React':          ('TASK_SCAFFOLD',      'scaffold'),
    'Create mock data':          ('TASK_DATA',          'data'),
    'Implement Header':          ('TASK_HEADER',        'header'),
    'Create App shell':          ('TASK_APP',           'app'),
    'Create KanbanBoard':        ('TASK_BOARD',         'board'),
    'Implement KanbanColumn':    ('TASK_COLUMN',        'column'),
    'Create IssueCard':          ('TASK_CARD',          'card'),
    'Implement AgentList':       ('TASK_AGENTLIST',     'agentlist'),
    'Create WorkQueue':          ('TASK_WORKQUEUE',     'workqueue'),
    'Create Agent Detail':       ('TASK_AGENT_DETAIL',  'agent-detail'),
    'Create Task Detail':        ('TASK_TASK_DETAIL',   'task-detail'),
    'Set up ESLint':             ('TASK_QUALITY',       'quality'),
    'Create Playwright':         ('TASK_VISUAL',        'visual'),
}

env_lines = []
slug_lines = []

for obj in json.loads(sys.stdin.read()):
    title = obj['title']
    issue_id = obj['id']
    for prefix, (var_name, slug) in mapping.items():
        if title.startswith(prefix):
            env_lines.append(f'export {var_name}=\"{issue_id}\"')
            if slug:
                slug_lines.append(f'{slug}={issue_id}')
            break

# Write env file (append to existing TEST_DIR line)
with open(sys.argv[1], 'a') as f:
    for line in sorted(env_lines):
        f.write(line + '\n')

# Write slug map
with open(sys.argv[2], 'w') as f:
    for line in sorted(slug_lines):
        f.write(line + '\n')
" "$ENV_FILE" "$SLUG_MAP"

# Validate python output
if [ ! -s "$ENV_FILE" ]; then
  echo "FATAL: python3 failed to generate env file ($ENV_FILE is empty)"
  exit 1
fi
if [ ! -s "$SLUG_MAP" ]; then
  echo "FATAL: python3 failed to generate slug map ($SLUG_MAP is empty)"
  exit 1
fi

# Verify slug map has expected number of entries (should match task count)
SLUG_COUNT=$(wc -l < "$SLUG_MAP" | tr -d ' ')
if [ "$SLUG_COUNT" -ne "$TASK_COUNT" ]; then
  echo "WARN: slug-to-id.map has $SLUG_COUNT entries but expected $TASK_COUNT tasks"
  echo "  This may indicate a title prefix mismatch in the python mapping"
fi

# ============================================================
# Section 6: Dependency Note
# ============================================================

# Dependencies are NOT added here. The daemon uses bd ready which respects
# dependencies, blocking both planning AND implementation. Adding deps in setup
# serializes the planning phase unnecessarily.
#
# Instead, deps are read from deps.conf by test-frontend-e2e.sh between
# Phase 2 (review) and Phase 3 (implementation) so that:
#   - Phase 1 (planning): all 13 tasks plannable in parallel
#   - Phase 3 (implementation): scaffold → components+quality → detail panels → app → visual
echo "==> Skipping dependencies (added by test script before implementation)"

# ============================================================
# Section 7: Sync & Summary
# ============================================================

bd sync 2>/dev/null || true

echo ""
echo "==> Test environment ready at $TEST_DIR"
echo ""
echo "  Issues: $EPIC_COUNT epics + $TASK_COUNT tasks (from issues/)"
echo ""
echo "  Slug map:"
while IFS='=' read -r slug id; do
  var="TASK_$(echo "$slug" | tr '[:lower:]-' '[:upper:]_')"
  printf "    %-20s = %s\n" "$var" "$id"
done < "$SLUG_MAP"
echo ""
echo "  Worktrees: falcon, nova"
echo ""
echo "  Source the env file for individual tests:"
echo "    source $ENV_FILE"
echo ""
echo "  Verify with: cd $TEST_DIR && loom daemon --dry-run"
