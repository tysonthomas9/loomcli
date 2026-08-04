#!/usr/bin/env bash
# Check that no Go package under internal/ exceeds a maximum number of non-test source files.
# No allowlist — if a package is too big, split it.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THRESHOLD="${1:-25}"

# Validate threshold is a positive integer.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-package-size.sh [THRESHOLD]" >&2
    echo "  THRESHOLD must be a positive integer (default: 25)" >&2
    exit 2
fi

cd "$REPO_ROOT"

# Find all non-test .go files under internal/, excluding vendor-like dirs.
files=$(find internal/ -name '*.go' ! -name '*_test.go' \
    ! -path '*/third_party/*' \
    ! -path '*/vendor/*' \
    ! -path '*/worktrees/*' \
    ! -path '*/node_modules/*' \
    2>/dev/null || true)

[[ -z "$files" ]] && exit 0

# Filter out generated code (// Code generated in first 3 lines).
# Write filtered file paths to a temp file for portability (no bash 4 arrays needed).
FILTERED_LIST=$(mktemp)
trap 'rm -f "$FILTERED_LIST"' EXIT

while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    if head -3 "$f" | grep -q '// Code generated'; then
        continue
    fi
    echo "$f"
done <<< "$files" > "$FILTERED_LIST"

[[ ! -s "$FILTERED_LIST" ]] && exit 0

# Count files per directory (package), then check against threshold.
# dirname each file, sort, count unique dirs, filter by threshold.
violations=$(while IFS= read -r f; do dirname "$f"; done < "$FILTERED_LIST" \
    | sort | uniq -c | sort -rn \
    | awk -v threshold="$THRESHOLD" '$1 > threshold {print $1 "\t" $2}')

if [[ -n "$violations" ]]; then
    echo "Package size violations (threshold: $THRESHOLD):" >&2
    echo "$violations" >&2
    exit 1
fi
