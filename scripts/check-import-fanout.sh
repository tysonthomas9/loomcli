#!/usr/bin/env bash
# check-import-fanout.sh — Prevent packages from accumulating cross-cutting deps.
# Counts module-internal imports per Go package under internal/.
# Fails with exit 1 if any package exceeds the threshold.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THRESHOLD="${1:-10}"
EXCEPTIONS_FILE="${SCRIPT_DIR}/import-fanout-exceptions.tsv"

# Validate threshold is a positive integer.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-import-fanout.sh [THRESHOLD]" >&2
    echo "  THRESHOLD must be a positive integer (default: 10)" >&2
    exit 2
fi

cd "$REPO_ROOT"

# A small number of explicit composition/delivery hubs necessarily import more
# owner-specific ports after the legacy umbrella service is removed. Keep those
# counts exact: an increase fails, and a decrease fails until this ratchet is
# tightened. Unlisted packages continue to use the global ceiling.
if [[ ! -f "$EXCEPTIONS_FILE" ]]; then
    echo "Error: missing import fanout exception ratchet: $EXCEPTIONS_FILE" >&2
    exit 2
fi
if ! awk -F '\t' '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    NF != 3 || $1 == "" || $2 !~ /^[0-9]+$/ || $3 == "" { exit 1 }
    seen[$1]++ > 0 { exit 1 }
    END { if (NR == 0) exit 1 }
' "$EXCEPTIONS_FILE"; then
    echo "Error: malformed or duplicate row in $EXCEPTIONS_FILE" >&2
    exit 2
fi

# Get module path.
MODULE=$(go list -m 2>/dev/null) || { echo "Error: go list -m failed" >&2; exit 2; }
[[ -n "$MODULE" ]] || { echo "Error: empty module path" >&2; exit 2; }

# Get imports for all internal packages.
GOLIST_OUTPUT=$(mktemp)
SEEN_EXCEPTIONS=$(mktemp)
trap 'rm -f "$GOLIST_OUTPUT" "$SEEN_EXCEPTIONS"' EXIT

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

    rel="${pkg#"${MODULE}/"}"
    expected=$(awk -F '\t' -v package="$rel" '$1 == package { print $2 }' "$EXCEPTIONS_FILE")
    if [[ -n "$expected" ]]; then
        printf '%s\n' "$rel" >> "$SEEN_EXCEPTIONS"
        if (( count != expected )); then
            direction="exceeds"
            if (( count < expected )); then
                direction="is below stale"
            fi
            violations+="$(printf '%d\t%s (%s exact exception %d)' "$count" "$rel" "$direction" "$expected")"$'\n'
        fi
    elif (( count > THRESHOLD )); then
        violations+="$(printf '%d\t%s' "$count" "$rel")"$'\n'
    fi
done < "$GOLIST_OUTPUT"

while IFS=$'\t' read -r package expected rationale; do
    [[ -z "$package" || "$package" == \#* ]] && continue
    if ! grep -Fxq "$package" "$SEEN_EXCEPTIONS"; then
        violations+="$(printf '0\t%s (exception package missing)' "$package")"$'\n'
    fi
done < "$EXCEPTIONS_FILE"

if [[ -n "$violations" ]]; then
    echo "Import fanout violations (default threshold: $THRESHOLD):" >&2
    echo "$violations" | sort -rn >&2
    exit 1
fi
