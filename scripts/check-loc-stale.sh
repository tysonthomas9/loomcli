#!/usr/bin/env bash
# Check for stale entries in .loc-allowlist.
# An entry is "stale" when the file's current LOC is ≤ threshold (default 500)
# with a configurable margin (STALE_MARGIN env var, default 20%).
#
# Without --check-stale: prints warnings, exits 0 (informational).
# With --check-stale: prints warnings, exits 1 if any stale entries found.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ALLOWLIST_FILE="$REPO_ROOT/.loc-allowlist"

CHECK_STALE=false
THRESHOLD=500

for arg in "$@"; do
    case "$arg" in
        --check-stale) CHECK_STALE=true ;;
        --*) echo "Unknown flag: $arg" >&2; exit 2 ;;
        *) THRESHOLD="$arg" ;;
    esac
done

# Validate threshold is a positive integer.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-loc-stale.sh [--check-stale] [THRESHOLD]" >&2
    echo "  THRESHOLD must be a positive integer (default: 500)" >&2
    exit 2
fi

if [[ ! -f "$ALLOWLIST_FILE" ]]; then
    echo "No .loc-allowlist file found." >&2
    exit 0
fi

STALE_MARGIN="${STALE_MARGIN:-20}"

# Parse allowlist entries (comments/blanks stripped).
stale=()
while IFS= read -r line; do
    # Strip comments and leading/trailing whitespace.
    line="${line%%#*}"
    line="$(echo "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [[ -z "$line" ]] && continue

    ceiling=$(echo "$line" | awk '{print $1}')
    filepath=$(echo "$line" | awk '{print $2}')

    [[ -z "$ceiling" || -z "$filepath" ]] && continue

    full_path="$REPO_ROOT/$filepath"
    if [[ ! -f "$full_path" ]]; then
        stale+=("$(printf 'MISSING\t%s\t(file no longer exists, ceiling was %s)' "$filepath" "$ceiling")")
        continue
    fi

    loc=$(wc -l < "$full_path")
    loc=$((loc + 0))  # trim whitespace

    # Stale if current LOC <= threshold * (1 - margin/100)
    # Using integer math: stale if loc * 100 <= threshold * (100 - margin)
    if (( loc * 100 <= THRESHOLD * (100 - STALE_MARGIN) )); then
        stale+=("$(printf '%d\t%s\t(ceiling: %d, threshold: %d, can be removed)' "$loc" "$filepath" "$ceiling" "$THRESHOLD")")
    fi
done < "$ALLOWLIST_FILE"

if [[ ${#stale[@]} -gt 0 ]]; then
    echo "Stale .loc-allowlist entries (margin: ${STALE_MARGIN}%):" >&2
    printf '%s\n' "${stale[@]}" >&2
    if [[ "$CHECK_STALE" == "true" ]]; then
        exit 1
    fi
fi

exit 0
