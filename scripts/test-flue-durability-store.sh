#!/usr/bin/env bash
# Run flue's adapter contract suite against the loom on-host durability adapter
# (internal/workflows/builtin/flue-durability-store.mjs), proving it satisfies flue's
# SessionStore + AgentSubmissionStore contract — the acceptance gate the
# FLUE-DURABILITY proposal mandates ("ride flue's own contract tests").
#
# Mirrors rebuild-builtin-bundle.sh's staging pattern: symlink the sibling flue
# runtime + its vitest into a temp workspace, copy the adapter + spec in, run vitest.
#
#   usage: scripts/test-flue-durability-store.sh
#   env:   FLUE_REPO=/path/to/flue  (default: ../flue)
#
# Note: the temp staging dir is left in place (no rm) for inspection; it lives under
# the OS temp dir and is reclaimed by the OS.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
RT="$FLUE_REPO/packages/runtime"
[ -f "$RT/dist/test-utils/define-store-contract-tests.mjs" ] || { echo "ERROR: flue runtime dist not built (need $RT/dist); build ../flue first" >&2; exit 1; }
[ -f "$RT/node_modules/vitest/vitest.mjs" ] || { echo "ERROR: vitest not found at $RT/node_modules/vitest; install ../flue deps first" >&2; exit 1; }

STAGE="$(mktemp -d -t loom-flue-store-test.XXXXXX)"
mkdir -p "$STAGE/node_modules/@flue" "$STAGE/src"
ln -s "$RT" "$STAGE/node_modules/@flue/runtime"
ln -s "$RT/node_modules/vitest" "$STAGE/node_modules/vitest"
cp "$ROOT/internal/workflows/builtin/flue-durability-store.mjs" "$STAGE/src/"
cp "$ROOT/internal/workflows/builtin/flue-durability-store.contract.spec.mjs" "$STAGE/src/"
printf '{"type":"module","name":"loom-flue-store-contract","private":true}\n' > "$STAGE/package.json"

echo "==> staging at $STAGE"
echo "==> running flue adapter contract suite against the loom durability adapter"
( cd "$STAGE" && node "$RT/node_modules/vitest/vitest.mjs" run )
