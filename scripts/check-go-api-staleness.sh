#!/usr/bin/env bash
# Verifies that internal/backend/api/gen/types.gen.go is in sync with
# api/openapi.yaml. Regenerates types into a temp file and diffs against the
# committed file. Exits 1 if they differ (spec changed but types not
# regenerated).
#
# Invoked by `make check-go` as part of the Go quality gate.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SPEC_PATH="$REPO_ROOT/api/openapi.yaml"
CONFIG_PATH="$REPO_ROOT/api/oapi-codegen.yaml"
GENERATED_PATH="$REPO_ROOT/internal/backend/api/gen/types.gen.go"
OAPI_CODEGEN_VERSION="v2.6.0"

cd "$REPO_ROOT"

if [[ ! -f "$SPEC_PATH" ]]; then
    echo "skip: api/openapi.yaml not found"
    exit 0
fi

if [[ ! -f "$GENERATED_PATH" ]]; then
    echo "FAIL: api/openapi.yaml exists but $GENERATED_PATH does not"
    echo "      Run: make gen-go-api"
    exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

spec_3_0="$tmp_dir/openapi-3.0.yaml"
fresh_gen="$tmp_dir/gen-types.gen.go"

# Preprocess 3.1 -> 3.0 (oapi-codegen v2.6 does not fully support 3.1).
go run ./scripts/openapi-to-30 "$SPEC_PATH" > "$spec_3_0"

# Generate into the temp directory, then compare against the committed file.
# Use a temporary config file that redirects output to the tmp location.
tmp_config="$tmp_dir/oapi-codegen.yaml"
sed "s|output:.*|output: $fresh_gen|" "$CONFIG_PATH" > "$tmp_config"

if ! go run "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$OAPI_CODEGEN_VERSION" --config "$tmp_config" "$spec_3_0" >/dev/null 2>"$tmp_dir/codegen.stderr"; then
    echo "FAIL: oapi-codegen failed:"
    cat "$tmp_dir/codegen.stderr"
    exit 1
fi

if ! diff -q "$GENERATED_PATH" "$fresh_gen" >/dev/null; then
    echo "FAIL: $GENERATED_PATH is stale relative to api/openapi.yaml"
    echo "      Run: make gen-go-api"
    echo ""
    diff -u "$GENERATED_PATH" "$fresh_gen" | head -40
    exit 1
fi

echo "OK: generated Go types match api/openapi.yaml"
