#!/usr/bin/env bash
# check-import-fanout.sh — Prevent packages from accumulating cross-cutting deps.
# Counts module-internal imports per Go package under internal/.
# Fails with exit 1 if any package exceeds the threshold.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THRESHOLD="${1:-10}"

# Validate threshold is a positive integer.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-import-fanout.sh [THRESHOLD]" >&2
    echo "  THRESHOLD must be a positive integer (default: 10)" >&2
    exit 2
fi

cd "$REPO_ROOT"

# Get module path.
MODULE=$(go list -m 2>/dev/null) || { echo "Error: go list -m failed" >&2; exit 2; }
[[ -n "$MODULE" ]] || { echo "Error: empty module path" >&2; exit 2; }

# Get imports for all internal packages.
GOLIST_OUTPUT=$(mktemp)
trap 'rm -f "$GOLIST_OUTPUT"' EXIT

if ! go list -f '{{.ImportPath}} {{join .Imports ","}}' ./internal/... > "$GOLIST_OUTPUT" 2>/dev/null; then
    echo "Error: go list failed — fix build errors first" >&2
    exit 2
fi

# Count module-internal imports per package.
violations=""
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    pkg="${line%% *}"
    imports="${line#* }"

    count=0
    if [[ "$imports" != "$pkg" ]] && [[ -n "$imports" ]]; then
        # Count imports matching module prefix, excluding self.
        count=$(echo "$imports" | tr ',' '\n' | grep "^${MODULE}/" | grep -cv "^${pkg}$" || true)
    fi

    if (( count > THRESHOLD )); then
        rel="${pkg#"${MODULE}/"}"
        violations+="$(printf '%d\t%s' "$count" "$rel")"$'\n'
    fi
done < "$GOLIST_OUTPUT"

if [[ -n "$violations" ]]; then
    echo "Import fanout violations (threshold: $THRESHOLD):" >&2
    echo "$violations" | sort -rn >&2
    exit 1
fi
