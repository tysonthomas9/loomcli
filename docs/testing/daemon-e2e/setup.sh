#!/usr/bin/env bash
# setup.sh — Create an isolated test repo for daemon E2E testing.
#
# Creates a small Python project with 2 epics, 4 tasks, and 2 worktrees.
# The plan agent creates designs, then the task agent implements them.
#
# Usage: ./setup.sh [test_dir]
#   test_dir defaults to /tmp/loom-daemon-e2e

set -euo pipefail

TEST_DIR="${1:-/tmp/loom-daemon-e2e}"

if [ -d "$TEST_DIR" ]; then
  echo "!! $TEST_DIR already exists. Remove it first or choose another path."
  echo "   Run: rm -rf $TEST_DIR"
  exit 1
fi

echo "==> Creating test repo at $TEST_DIR"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

# Initialize git repo
git init -q
git commit --allow-empty -q -m "initial commit"

# Initialize beads
bd init -q

# Create project structure
mkdir -p src tests worktrees .loom/logs

# --- Python source files ---

cat > src/__init__.py <<'PYEOF'
PYEOF

cat > src/calculator.py <<'PYEOF'
"""Simple calculator module."""


def add(a: float, b: float) -> float:
    """Return the sum of a and b."""
    return a + b


def subtract(a: float, b: float) -> float:
    """Return the difference of a and b."""
    return a - b


def multiply(a: float, b: float) -> float:
    """Return the product of a and b."""
    return a * b


def divide(a: float, b: float) -> float:
    """Return the quotient of a divided by b.

    Raises:
        ValueError: If b is zero.
    """
    if b == 0:
        raise ValueError("Cannot divide by zero")
    return a / b
PYEOF

cat > src/utils.py <<'PYEOF'
"""String utility functions."""


def reverse(text: str) -> str:
    """Return the reversed string."""
    return text[::-1]


def capitalize_words(text: str) -> str:
    """Capitalize the first letter of each word."""
    return text.title()


def truncate(text: str, max_length: int, suffix: str = "...") -> str:
    """Truncate text to max_length, appending suffix if truncated."""
    if len(text) <= max_length:
        return text
    return text[: max_length - len(suffix)] + suffix
PYEOF

cat > tests/__init__.py <<'PYEOF'
PYEOF

# Commit source files
git add -A
git commit -q -m "Add Python project and loom config"

# Create worktrees
git worktree add -q ./worktrees/falcon -b falcon
git worktree add -q ./worktrees/nova   -b nova

# --- loom.yaml ---
# falcon=plan creates designs, nova=task implements them

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
    role: plan
    auto: true
  - worktree: nova
    role: task
    auto: true
EOF

# Also create a task-only config for Phase 2 (after designs exist)
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

# --- Seed beads with two epics and tasks ---

echo "==> Creating test epics and tasks"

EPIC_A=$(bd create --title="Calculator improvements" --type=epic --priority=1 --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_A" ] || { echo "FAIL: Could not create Epic A"; exit 1; }

TASK_A1=$(bd create --title="Add a power(base, exp) function to src/calculator.py" --type=task --priority=1 --parent="$EPIC_A" --json 2>/dev/null | jq -r '.id')
TASK_A2=$(bd create --title="Write unit tests for all functions in src/calculator.py" --type=task --priority=2 --parent="$EPIC_A" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_A1" ] && [ -n "$TASK_A2" ] || { echo "FAIL: Could not create Epic A tasks"; exit 1; }

EPIC_B=$(bd create --title="Utils improvements" --type=epic --priority=2 --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_B" ] || { echo "FAIL: Could not create Epic B"; exit 1; }

TASK_B1=$(bd create --title="Add a snake_case(text) function to src/utils.py" --type=task --priority=2 --parent="$EPIC_B" --json 2>/dev/null | jq -r '.id')
TASK_B2=$(bd create --title="Write unit tests for all functions in src/utils.py" --type=task --priority=3 --parent="$EPIC_B" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_B1" ] && [ -n "$TASK_B2" ] || { echo "FAIL: Could not create Epic B tasks"; exit 1; }

bd sync 2>/dev/null || true

# Write env file for other scripts to source
ENV_FILE="$TEST_DIR/test-env.sh"
cat > "$ENV_FILE" <<EOF
export TEST_DIR="$TEST_DIR"
export EPIC_A="$EPIC_A"
export EPIC_B="$EPIC_B"
export TASK_A1="$TASK_A1"
export TASK_A2="$TASK_A2"
export TASK_B1="$TASK_B1"
export TASK_B2="$TASK_B2"
EOF

echo ""
echo "==> Test environment ready at $TEST_DIR"
echo ""
echo "  Epics:"
echo "    EPIC_A=$EPIC_A  (2 tasks: $TASK_A1, $TASK_A2)"
echo "    EPIC_B=$EPIC_B  (2 tasks: $TASK_B1, $TASK_B2)"
echo ""
echo "  Worktrees: falcon (plan), nova (task)"
echo ""
echo "  Source the env file for individual tests:"
echo "    source $ENV_FILE"
echo ""
echo "  Verify with: cd $TEST_DIR && loom daemon --dry-run"
