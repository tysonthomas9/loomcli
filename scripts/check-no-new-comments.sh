#!/usr/bin/env bash
# check-no-new-comments.sh — Fail when a change adds code comments.
#
# Usage: ./scripts/check-no-new-comments.sh [go|ts]
#
# Added lines come from scripts/added-lines.sh (see it for how the diff base is
# chosen). Each language plugs in a scanner that reads those ranges on stdin and
# prints "file:line: text" for every added line holding a real comment token.
# Go: scripts/commentscan (go/scanner); only //go: directives are exempt.
# TS/TSX: internal/webui/frontend/scripts/check-no-new-comments.mjs (TypeScript
# compiler API, needs frontend node_modules; no Go toolchain); nothing is exempt.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
lang="${1:-go}"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/loom-comments.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

case "$lang" in
    go)
        pathspecs=('*.go')
        (cd "$HELPER_ROOT" && go build -o "$tmpdir/commentscan" ./scripts/commentscan)
        scanner=("$tmpdir/commentscan")
        hint="Only //go: compiler directives are exempt. Lint suppressions (//nolint etc.) count too;"
        ;;
    ts)
        pathspecs=('*.ts' '*.tsx')
        frontend_dir="${LOOM_COMMENT_FRONTEND_DIR:-$HELPER_ROOT/internal/webui/frontend}"
        if [ ! -f "$frontend_dir/node_modules/typescript/package.json" ]; then
            echo "check-no-new-comments: typescript is not installed in $frontend_dir/node_modules;" >&2
            echo "  run 'make ensure-frontend-deps' (or 'npm ci' there) first" >&2
            exit 2
        fi
        scanner=(node "$frontend_dir/scripts/check-no-new-comments.mjs")
        hint="Nothing is exempt: eslint-disable, @ts-expect-error, @ts-ignore and /// <reference> count too;"
        ;;
    *)
        echo "check-no-new-comments: unknown language '$lang'" >&2
        exit 2
        ;;
esac

"$SCRIPT_DIR/added-lines.sh" "${pathspecs[@]}" >"$tmpdir/ranges"

cd "$(git rev-parse --show-toplevel)"
status=0
"${scanner[@]}" <"$tmpdir/ranges" >"$tmpdir/out" || status=$?

if [ "$status" -eq 0 ]; then
    echo "No new $lang comments found."
    exit 0
fi

if [ "$status" -eq 1 ]; then
    echo "New $lang code comments are not allowed ($(wc -l <"$tmpdir/out" | tr -d ' ') lines):" >&2
    cat "$tmpdir/out" >&2
    echo "" >&2
    echo "Fix: delete these comments; make the code self-explanatory instead." >&2
    echo "     $hint" >&2
    echo "     fix the finding or change the linter config." >&2
    exit 1
fi

cat "$tmpdir/out" >&2
exit "$status"
