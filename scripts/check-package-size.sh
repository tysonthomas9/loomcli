#!/usr/bin/env bash
# Check that Go packages under internal/ remain within their architectural
# size class. Deep owner roots may hold more implementation behind their
# public interface; every other package keeps the stricter default ceiling.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THRESHOLD="${1:-25}"
OWNER_ROOT_THRESHOLD="${2:-40}"

# Validate thresholds are positive integers.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]] || ! [[ "$OWNER_ROOT_THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-package-size.sh [THRESHOLD] [OWNER_ROOT_THRESHOLD]" >&2
    echo "  thresholds must be positive integers (defaults: 25 and 40)" >&2
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

# Count files per directory (package), then apply the strict default ceiling
# except to exact owner roots shaped internal/modules/<owner>. Nested adapter
# packages remain subject to the strict ceiling; this is a class rule, not an
# allowlist of today's large packages.
violations=$(while IFS= read -r f; do dirname "$f"; done < "$FILTERED_LIST" \
    | sort | uniq -c | sort -rn \
    | awk -v threshold="$THRESHOLD" -v owner_threshold="$OWNER_ROOT_THRESHOLD" '
        {
            limit = threshold
            segment_count = split($2, segments, "/")
            if (segment_count == 3 && segments[1] == "internal" && segments[2] == "modules") {
                limit = owner_threshold
            }
            if ($1 > limit) {
                print $1 "\t" $2 "\t(limit " limit ")"
            }
        }
    ')

if [[ -n "$violations" ]]; then
    echo "Package size violations (default: $THRESHOLD, owner roots: $OWNER_ROOT_THRESHOLD):" >&2
    echo "$violations" >&2
    exit 1
fi
