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

# OpenCode config (committed so daemon recovery doesn't delete it as untracked)
cat > opencode.json <<'EOF'
{
  "$schema": "https://opencode.ai/config.json",
  "model": "openai/gpt-5.2"
}
EOF

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

EPIC_A=$(bd create --title="Calculator improvements" --type=epic --priority=1 \
  --description="Add exponentiation to the calculator module and ensure all functions have comprehensive test coverage." \
  --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_A" ] || { echo "FAIL: Could not create Epic A"; exit 1; }

TASK_A1=$(bd create --title="Add a power(base, exp) function to src/calculator.py" \
  --type=task --priority=1 --parent="$EPIC_A" \
  --description="$(cat <<'DESC'
Add a power(base, exp) function to src/calculator.py that returns base**exp.

Requirements:
- Function signature: power(base: float, exp: float) -> float
- Follow existing conventions: type hints, docstring with Raises section
- Raise ValueError when base is 0 and exp is negative (consistent with divide's zero handling)
- Use Python's built-in ** operator

Acceptance criteria:
- power(2, 3) returns 8
- power(5, 0) returns 1
- power(0, -1) raises ValueError
- Function has a docstring
DESC
)" --json 2>/dev/null | jq -r '.id')

TASK_A2=$(bd create --title="Write unit tests for all functions in src/calculator.py" \
  --type=task --priority=2 --parent="$EPIC_A" \
  --description="$(cat <<'DESC'
Create tests/test_calculator.py with pytest unit tests for all functions in src/calculator.py (add, subtract, multiply, divide, and power).

Requirements:
- Use pytest framework
- Test each function with: positive numbers, negative numbers, zero, floats
- Test error cases: divide(x, 0) raises ValueError, power(0, negative) raises ValueError
- Minimum 20 test cases total

Acceptance criteria:
- tests/test_calculator.py exists
- All tests pass when run with: python3 -m pytest tests/test_calculator.py
- At least 20 test functions defined
DESC
)" --json 2>/dev/null | jq -r '.id')

[ -n "$TASK_A1" ] && [ -n "$TASK_A2" ] || { echo "FAIL: Could not create Epic A tasks"; exit 1; }

EPIC_B=$(bd create --title="Utils improvements" --type=epic --priority=2 \
  --description="Add snake_case conversion to the utils module and ensure all functions have comprehensive test coverage." \
  --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_B" ] || { echo "FAIL: Could not create Epic B"; exit 1; }

TASK_B1=$(bd create --title="Add a snake_case(text) function to src/utils.py" \
  --type=task --priority=2 --parent="$EPIC_B" \
  --description="$(cat <<'DESC'
Add a snake_case(text) function to src/utils.py that converts strings to snake_case format.

Requirements:
- Function signature: snake_case(text: str) -> str
- Handle camelCase, PascalCase, spaces, hyphens, and mixed separators
- Handle acronyms (e.g. HTMLParser -> html_parser)
- Use only Python standard library (re module)
- Follow existing conventions: type hints, docstring

Acceptance criteria:
- snake_case('camelCase') returns 'camel_case'
- snake_case('PascalCase') returns 'pascal_case'
- snake_case('HTMLParser') returns 'html_parser'
- snake_case('hello world') returns 'hello_world'
- snake_case('') returns ''
- Function has a docstring
DESC
)" --json 2>/dev/null | jq -r '.id')

TASK_B2=$(bd create --title="Write unit tests for all functions in src/utils.py" \
  --type=task --priority=3 --parent="$EPIC_B" \
  --description="$(cat <<'DESC'
Create tests/test_utils.py with pytest unit tests for all functions in src/utils.py (reverse, capitalize_words, truncate, and snake_case).

Requirements:
- Use pytest framework
- Test each function with normal inputs, empty strings, and edge cases
- Test truncate with custom suffix
- Test snake_case with camelCase, PascalCase, acronyms, spaces, hyphens
- Minimum 15 test cases total

Acceptance criteria:
- tests/test_utils.py exists
- All tests pass when run with: python3 -m pytest tests/test_utils.py
- At least 15 test functions defined
DESC
)" --json 2>/dev/null | jq -r '.id')

[ -n "$TASK_B1" ] && [ -n "$TASK_B2" ] || { echo "FAIL: Could not create Epic B tasks"; exit 1; }

# Pre-populate design fields so task agents can work without a plan phase.
# The design just restates the description — its presence signals "planned".
bd update "$TASK_A1" --design="$(cat <<'DESIGN'
## Implementation
Add `power(base: float, exp: float) -> float` to `src/calculator.py` after `divide`.
Use `base ** exp`. Raise `ValueError("Cannot raise zero to a negative power")` when `base == 0 and exp < 0`.
Include docstring with Raises section, matching existing style.
DESIGN
)" 2>/dev/null
bd update "$TASK_A2" --design="$(cat <<'DESIGN'
## Implementation
Create `tests/test_calculator.py`. Import all functions from `src.calculator`.
Write pytest tests for add, subtract, multiply, divide, and power.
Cover: positive, negative, zero, float inputs. Test ValueError for divide(x,0) and power(0,neg).
Minimum 20 test functions.
DESIGN
)" 2>/dev/null
bd update "$TASK_B1" --design="$(cat <<'DESIGN'
## Implementation
Add `snake_case(text: str) -> str` to `src/utils.py`. Use `re` module.
Insert underscores at camelCase boundaries, handle acronyms, replace non-alnum with underscores, lowercase.
Include docstring matching existing style.
DESIGN
)" 2>/dev/null
bd update "$TASK_B2" --design="$(cat <<'DESIGN'
## Implementation
Create `tests/test_utils.py`. Import all functions from `src.utils`.
Write pytest tests for reverse, capitalize_words, truncate, and snake_case.
Cover: normal inputs, empty strings, edge cases, custom suffix for truncate, various naming conventions for snake_case.
Minimum 15 test functions.
DESIGN
)" 2>/dev/null

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
