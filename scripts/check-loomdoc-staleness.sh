#!/usr/bin/env bash
# Verifies that the committed loomdoc reference docs under docs/reference/ are in
# sync with their inputs: the git-tracked Go source each is derived from and the
# hand-written preambles (docs/reference/<name>.preamble.md). For each doc it
# regenerates the body into a temp file (via `loomdoc -stdout <subcommand>`, which
# never touches the working tree) and diffs against the committed one. Exits 1 if
# any differ.
#
# Invoked by `make check-go` as part of the Go quality gate. Mirrors
# scripts/check-api-docs-staleness.sh, which gates docs/api.md the same way.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

if [[ ! -d "scripts/loomdoc" ]]; then
    echo "skip: scripts/loomdoc not found"
    exit 0
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

# Each loomdoc subcommand writes docs/reference/<doc>.md; the pairs below map the
# subcommand to that committed basename. Keep in sync with the loomdoc registry.
subcommands=(envvars cli layers)
docs=(env-vars cli architecture)

stale=0
for i in "${!subcommands[@]}"; do
    sub="${subcommands[$i]}"
    doc="docs/reference/${docs[$i]}.md"
    fresh="$tmp_dir/${docs[$i]}.md"

    if [[ ! -f "$doc" ]]; then
        echo "FAIL: $doc does not exist"
        echo "      Run: make docs-gen"
        exit 1
    fi

    if ! go run ./scripts/loomdoc -stdout "$sub" > "$fresh" 2>"$tmp_dir/gen.stderr"; then
        echo "FAIL: loomdoc $sub failed:"
        cat "$tmp_dir/gen.stderr"
        exit 1
    fi

    if ! diff -q "$doc" "$fresh" >/dev/null; then
        echo "FAIL: $doc is stale relative to the git-tracked Go source it is"
        echo "      generated from or its docs/reference/${docs[$i]}.preamble.md."
        echo "      Run: make docs-gen"
        echo ""
        # `|| true`: diff exits 1 on difference (that is why we are here) and
        # `set -e`/pipefail would otherwise abort before the accumulator below,
        # hiding drift in the remaining docs.
        diff -u "$doc" "$fresh" | head -40 || true
        stale=1
    fi
done

if [[ "$stale" -ne 0 ]]; then
    exit 1
fi

echo "OK: docs/reference/{env-vars,cli,architecture}.md match their generated source"
