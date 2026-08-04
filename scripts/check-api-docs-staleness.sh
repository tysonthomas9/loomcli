#!/usr/bin/env bash
# Verifies that docs/api.md is in sync with its inputs: api/openapi.yaml, the
# hand-written partials (docs/api.preamble.md, docs/api.appendix.md), and the
# route registrations under internal/webui. Regenerates the document into a
# temp file and diffs against the committed one. Exits 1 if they differ.
#
# Invoked by `make check-go` as part of the Go quality gate.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SPEC_PATH="$REPO_ROOT/api/openapi.yaml"
DOC_PATH="$REPO_ROOT/docs/api.md"
PREAMBLE_PATH="$REPO_ROOT/docs/api.preamble.md"
APPENDIX_PATH="$REPO_ROOT/docs/api.appendix.md"

cd "$REPO_ROOT"

if [[ ! -f "$SPEC_PATH" ]]; then
    echo "skip: api/openapi.yaml not found"
    exit 0
fi

for required in "$PREAMBLE_PATH" "$APPENDIX_PATH"; do
    if [[ ! -f "$required" ]]; then
        echo "FAIL: $required is missing — docs/api.md cannot be regenerated"
        echo "      Restore it, then run: make gen-api-docs"
        exit 1
    fi
done

if [[ ! -f "$DOC_PATH" ]]; then
    echo "FAIL: api/openapi.yaml exists but $DOC_PATH does not"
    echo "      Run: make gen-api-docs"
    exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

fresh_doc="$tmp_dir/api.md"

if ! go run ./scripts/openapi-to-md \
    -preamble "docs/api.preamble.md" \
    -appendix "docs/api.appendix.md" \
    -routes "internal/webui" \
    "$SPEC_PATH" > "$fresh_doc" 2>"$tmp_dir/gen.stderr"; then
    echo "FAIL: openapi-to-md failed:"
    cat "$tmp_dir/gen.stderr"
    exit 1
fi

if ! diff -q "$DOC_PATH" "$fresh_doc" >/dev/null; then
    echo "FAIL: $DOC_PATH is stale relative to api/openapi.yaml, the docs/api.*.md"
    echo "      partials, or the routes registered under internal/webui."
    echo "      Run: make gen-api-docs"
    echo ""
    diff -u "$DOC_PATH" "$fresh_doc" | head -40
    exit 1
fi

echo "OK: docs/api.md matches api/openapi.yaml + partials + registered routes"
