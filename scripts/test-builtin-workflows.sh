#!/usr/bin/env bash
# Run the builtin workflow node unit tests (internal/infra/workflowdistribution/builtin/*.test.mjs).
#
# The workflow .ts files statically import @flue/runtime and @loom/sdk. Those
# bare specifiers only resolve
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

SRC="$ROOT/internal/infra/workflowdistribution/builtin"
STAGE="$(mktemp -d -t loom-builtin-tests.XXXXXX)"
echo "==> staging builtin workflow tests at $STAGE"
# shellcheck source=scripts/stage-builtin-modules.sh
source "$ROOT/scripts/stage-builtin-modules.sh"
stage_builtin_node_modules "$STAGE"
printf '%s\n' '{"type":"module"}' > "$STAGE/package.json"
# Copy the sources + their tests so node resolves the bare specifiers from
# STAGE/node_modules while the relative `./<runner>.ts` imports stay co-located.
cp "$SRC"/*.ts "$SRC"/*.test.mjs "$STAGE/"

echo "==> node --test (from staged dir)"
( cd "$STAGE"; node --test ./*.test.mjs )
echo "==> done. staging kept at $STAGE (in \$TMPDIR; OS-cleaned)"
