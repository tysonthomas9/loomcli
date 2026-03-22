#!/usr/bin/env bash
# check-no-log-printf.sh — Prevent stdlib log.Printf in new Go files.
# New code should use log/slog for structured logging.
# Existing files are grandfathered in via log-printf-baseline.txt.
# Lines with //nolint:nologprintf are exempt.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BASELINE="$SCRIPT_DIR/log-printf-baseline.txt"

cd "$REPO_ROOT"

# Load baseline into associative array.
declare -A allowed
if [ -f "$BASELINE" ]; then
    while IFS= read -r line; do
        # Skip comments and blank lines.
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "${line// /}" ]] && continue
        allowed["$line"]=1
    done < "$BASELINE"
fi

violations=0
output=""

# Find all non-test Go files, excluding third_party/vendor/worktrees.
while IFS= read -r -d '' file; do
    rel_path="${file#"$REPO_ROOT/"}"

    # Skip if file is in the baseline.
    if [[ -n "${allowed[$rel_path]+x}" ]]; then
        continue
    fi

    # Grep for log.Print/Printf/Println/Fatal/Fatalf/Fatalln, skip nolint lines.
    while IFS= read -r match; do
        line_num="${match%%:*}"
        line_content="${match#*:}"
        if echo "$line_content" | grep -q '//nolint:nologprintf'; then
            continue
        fi
        output+="${rel_path}:${line_num}: ${line_content}"$'\n'
        violations=$((violations + 1))
    done < <(grep -nE '\blog\.(Print|Printf|Println|Fatal|Fatalf|Fatalln)\b' "$file" || true)
done < <(find "$REPO_ROOT" \
    -path '*/third_party' -prune -o \
    -path '*/vendor' -prune -o \
    -path '*/worktrees' -prune -o \
    -path '*/node_modules' -prune -o \
    -path '*/.git' -prune -o \
    \( -name '*.go' \
       ! -name '*_test.go' \
       -print0 \))

if [ "$violations" -gt 0 ]; then
    echo "log.Printf violations ($violations found):" >&2
    printf '%s' "$output" >&2
    echo "" >&2
    echo "Fix: use log/slog instead (e.g. slog.Info, slog.Warn, slog.Error)." >&2
    echo "     Or add //nolint:nologprintf to exempt, or add to scripts/log-printf-baseline.txt." >&2
    exit 1
fi

echo "No new log.Printf violations found."
