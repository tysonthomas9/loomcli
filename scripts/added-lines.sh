#!/usr/bin/env bash
# added-lines.sh — Print the line ranges a change adds, one "path<TAB>start<TAB>end" per range.
#
# Usage: ./scripts/added-lines.sh [pathspec...]     (e.g. '*.go', or '*.ts' '*.tsx')
#
# Run from inside the target git repo. Compares the working tree (committed,
# staged, and unstaged edits, plus untracked files) against a base commit:
#   1. LOOM_COMMENT_BASE, if set
#   2. CI pull_request: merge-base of HEAD and origin/$GITHUB_BASE_REF
#   3. CI push: LOOM_COMMENT_PUSH_BEFORE (github.event.before); skipped when it
#      is empty, all zeros, or not present in the clone
#   4. otherwise: merge-base of HEAD and whichever of LOOM_COMMENT_BASE_REFS
#      (default "origin/main origin/v5") HEAD is fewest commits ahead of
# Paths are relative to the repo root. Status goes to stderr; exit 2 on error.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

die() {
    echo "added-lines: $*" >&2
    exit 2
}

is_commit() {
    git rev-parse --verify --quiet "$1^{commit}" >/dev/null 2>&1
}

resolve_base() {
    if [ -n "${LOOM_COMMENT_BASE:-}" ]; then
        is_commit "$LOOM_COMMENT_BASE" || die "LOOM_COMMENT_BASE=$LOOM_COMMENT_BASE is not a commit"
        git rev-parse "$LOOM_COMMENT_BASE^{commit}"
        return
    fi
    if [ -n "${GITHUB_BASE_REF:-}" ]; then
        is_commit "origin/$GITHUB_BASE_REF" || die "origin/$GITHUB_BASE_REF not found; fetch with enough history (fetch-depth: 0)"
        git merge-base HEAD "origin/$GITHUB_BASE_REF" || die "no merge-base with origin/$GITHUB_BASE_REF"
        return
    fi
    if [ "${GITHUB_EVENT_NAME:-}" = "push" ]; then
        local before="${LOOM_COMMENT_PUSH_BEFORE:-}"
        if [ -z "$before" ] || [[ "$before" =~ ^0+$ ]] || ! is_commit "$before"; then
            echo "added-lines: push has no usable before-SHA (${before:-unset}); skipping" >&2
            return
        fi
        git rev-parse "$before^{commit}"
        return
    fi
    local ref best="" best_count=""
    for ref in ${LOOM_COMMENT_BASE_REFS:-origin/main origin/v5}; do
        is_commit "$ref" || continue
        local count
        count="$(git rev-list --count "$ref..HEAD")"
        if [ -z "$best_count" ] || [ "$count" -lt "$best_count" ]; then
            best="$ref"
            best_count="$count"
        fi
    done
    [ -n "$best" ] || die "none of ${LOOM_COMMENT_BASE_REFS:-origin/main origin/v5} found; fetch one or set LOOM_COMMENT_BASE"
    echo "added-lines: comparing against $best" >&2
    git merge-base HEAD "$best" || die "no merge-base with $best"
}

base="$(resolve_base)"
if [ -z "$base" ]; then
    exit 0
fi
echo "added-lines: base $base" >&2

git -c core.quotePath=false diff --no-ext-diff --no-color --no-textconv -U0 -M \
    --diff-filter=d --src-prefix=a/ --dst-prefix=b/ "$base" -- "$@" |
    awk '
        /^diff --git / { header = 1; next }
        header && /^\+\+\+ / { path = substr($0, 7); next }
        /^@@ / {
            header = 0
            n = split($3, a, ",")
            start = substr(a[1], 2) + 0
            count = (n > 1) ? a[2] + 0 : 1
            if (count > 0) printf "%s\t%d\t%d\n", path, start, start + count - 1
        }
    '

git -c core.quotePath=false ls-files --others --exclude-standard -z -- "$@" |
    while IFS= read -r -d '' file; do
        lines=$(awk 'END { print NR }' "$file")
        if [ "$lines" -gt 0 ]; then
            printf '%s\t1\t%d\n' "$file" "$lines"
        fi
    done
