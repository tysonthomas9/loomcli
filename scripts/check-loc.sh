#!/usr/bin/env bash
# Check that Go source files do not exceed a maximum line count.
# Files listed in .loc-allowlist are permitted up to their recorded ceiling (ratchet).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THRESHOLD="${1:-1000}"
TEST_THRESHOLD="${2:-2000}"
ALLOWLIST_FILE="$REPO_ROOT/.loc-allowlist"

# Validate thresholds are positive integers.
if ! [[ "$THRESHOLD" =~ ^[1-9][0-9]*$ ]]; then
    echo "Usage: check-loc.sh [THRESHOLD] [TEST_THRESHOLD]" >&2
    echo "  THRESHOLD      max lines for source files (default: 1000)" >&2
    echo "  TEST_THRESHOLD max lines for *_test.go files (default: 2000)" >&2
    exit 2
fi

cd "$REPO_ROOT"

# Parse allowlist into a temp file of "ceiling path" lines (comments/blanks stripped).
ALLOWLIST_PARSED=$(mktemp)
trap 'rm -f "$ALLOWLIST_PARSED"' EXIT
if [[ -f "$ALLOWLIST_FILE" ]]; then
    sed 's/#.*//; s/^[[:space:]]*//; s/[[:space:]]*$//' "$ALLOWLIST_FILE" \
        | awk 'NF==2 {print $1, $2}' > "$ALLOWLIST_PARSED"
fi

# Look up a file's ceiling from the parsed allowlist. Prints ceiling or empty string.
get_ceiling() {
    awk -v path="$1" '$2 == path {print $1; exit}' "$ALLOWLIST_PARSED"
}

# Collect all .go files: tracked + untracked.
files=$(
    {
        git ls-files -z '*.go'
        git ls-files -z --others --exclude-standard '*.go'
    } | sort -zu | tr '\0' '\n' | sed 's|^\./||'
)

# Filter out excluded directories.
EXCLUDE_DIRS='third_party/|vendor/|worktrees/|node_modules/'
files=$(echo "$files" | grep -Ev "^($EXCLUDE_DIRS)" || true)

[[ -z "$files" ]] && exit 0

# Filter out generated code (// Code generated in first 3 lines).
filtered=()
while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    # Skip files tracked in the index but missing on disk (pending deletions).
    [[ -f "$f" ]] || continue
    if head -3 "$f" | grep -q '// Code generated'; then
        continue
    fi
    filtered+=("$f")
done <<< "$files"

[[ ${#filtered[@]} -eq 0 ]] && exit 0

# Count lines and check against threshold/ceiling.
violations=()
for f in "${filtered[@]}"; do
    loc=$(wc -l < "$f")
    loc=$((loc + 0))  # trim whitespace

    ceiling=$(get_ceiling "$f")
    # Use TEST_THRESHOLD for test files, THRESHOLD for source files.
    if [[ "$f" == *_test.go ]]; then
        effective_threshold=$TEST_THRESHOLD
    else
        effective_threshold=$THRESHOLD
    fi

    if [[ -n "$ceiling" ]]; then
        if (( loc > ceiling )); then
            violations+=("$(printf '%d\t%s\t(ceiling: %d)' "$loc" "$f" "$ceiling")")
        fi
    elif (( loc > effective_threshold )); then
        violations+=("$(printf '%d\t%s' "$loc" "$f")")
    fi
done

if [[ ${#violations[@]} -gt 0 ]]; then
    echo "LOC violations (threshold: $THRESHOLD):" >&2
    # Sort by line count descending.
    printf '%s\n' "${violations[@]}" | sort -rn >&2
    exit 1
fi
