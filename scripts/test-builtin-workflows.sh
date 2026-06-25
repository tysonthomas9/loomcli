#!/usr/bin/env bash
# Run the builtin workflow node unit tests (internal/workflows/builtin/*.test.mjs).
#
# The workflow .ts files statically import @flue/runtime (and @loom/sdk, plus
# @daytona/sdk for the daytona runner) — those bare specifiers only resolve
# through a staged node_modules, exactly like the bundle build. Mirror the
# rebuild-builtin-bundle staging so `node --test` can load the modules.
#
#   usage: scripts/test-builtin-workflows.sh
#   env:   FLUE_REPO=/path/to/flue   (default: ../flue)
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
[ -n "$FLUE_REPO" ] && [ -d "$FLUE_REPO/packages/runtime" ] || {
  echo "ERROR: @flue/runtime not found; build ../flue or set FLUE_REPO" >&2
  exit 1
}

SRC="$ROOT/internal/workflows/builtin"
STAGE="$(mktemp -d -t loom-builtin-tests.XXXXXX)"
echo "==> staging builtin workflow tests at $STAGE"
mkdir -p "$STAGE/node_modules/@loom" "$STAGE/node_modules/@flue" "$STAGE/node_modules/@daytona"
ln -s "$ROOT/sdk" "$STAGE/node_modules/@loom/sdk"
ln -s "$FLUE_REPO/packages/runtime" "$STAGE/node_modules/@flue/runtime"
DAYTONA_SDK="$FLUE_REPO/node_modules/.pnpm/node_modules/@daytona/sdk"
[ -d "$DAYTONA_SDK" ] && ln -s "$DAYTONA_SDK" "$STAGE/node_modules/@daytona/sdk" \
  || echo "WARN: @daytona/sdk not found at $DAYTONA_SDK; daytona tests may not load" >&2
printf '%s\n' '{"type":"module"}' > "$STAGE/package.json"
# Copy the sources + their tests so node resolves the bare specifiers from
# STAGE/node_modules while the relative `./<runner>.ts` imports stay co-located.
cp "$SRC"/*.ts "$SRC"/*.test.mjs "$STAGE/"

echo "==> node --test (from staged dir)"
( cd "$STAGE"; node --test ./*.test.mjs )
echo "==> done. staging kept at $STAGE (in \$TMPDIR; OS-cleaned)"
