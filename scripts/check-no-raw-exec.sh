#!/usr/bin/env bash
# check-no-raw-exec.sh — Prevent raw exec.Command in unit tests.
# Enforces DI interfaces for testability.
# Lines with //nolint:norawexec are exempt.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

violations=0
output=""

# Find all *_test.go files, excluding dirs and e2e/integration filenames.
while IFS= read -r -d '' file; do
    # Skip files with integration/e2e/container build tags in first 5 lines.
    if head -5 "$file" | grep -qE '//go:build.*(integration|e2e|container)'; then
        continue
    fi

    # Grep for exec.Command or exec.CommandContext, skip nolint lines.
    while IFS= read -r match; do
        line_num="${match%%:*}"
        line_content="${match#*:}"
        if echo "$line_content" | grep -q '//nolint:norawexec'; then
            continue
        fi
        rel_path="${file#"$REPO_ROOT/"}"
        output+="${rel_path}:${line_num}: ${line_content}"$'\n'
        violations=$((violations + 1))
    done < <(grep -n 'exec\.Command' "$file" || true)
done < <(find "$REPO_ROOT" \
    -path '*/third_party' -prune -o \
    -path '*/vendor' -prune -o \
    -path '*/worktrees' -prune -o \
    -path '*/node_modules' -prune -o \
    -path '*/.git' -prune -o \
    \( -name '*_test.go' \
       ! -name '*_e2e_test.go' \
       ! -name '*_integration_test.go' \
       -print0 \))

if [ "$violations" -gt 0 ]; then
    echo "exec.Command violations in unit tests ($violations found):" >&2
    printf '%s' "$output" >&2
    echo "" >&2
    echo "Fix: use a DI interface, or add //nolint:norawexec to exempt." >&2
    exit 1
fi

echo "No raw exec.Command violations found."
