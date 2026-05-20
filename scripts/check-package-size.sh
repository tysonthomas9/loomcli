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

# Count build-applicable non-test files per package. `go list` respects GOOS,
# GOARCH, and build tags, so platform-specific alternatives are not double
# counted as if they compiled into one package together.
violations=$(go list -f '{{.Dir}}\t{{len .GoFiles}}' ./internal/... \
    | while IFS=$'\t' read -r dir count; do
        rel="${dir#$REPO_ROOT/}"
        if [[ "$rel" == */third_party/* || "$rel" == */vendor/* || "$rel" == */worktrees/* || "$rel" == */node_modules/* ]]; then
            continue
        fi
        if (( count > THRESHOLD )); then
            printf '%d\t%s\n' "$count" "$rel"
        fi
    done | sort -rn)

if [[ -n "$violations" ]]; then
    echo "Package size violations (threshold: $THRESHOLD):" >&2
    echo "$violations" >&2
    exit 1
fi
