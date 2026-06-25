#!/usr/bin/env bash
# Rebuild the prebuilt epic-runner workflow bundle
# (internal/workflows/builtin-dist/epic-runner/dist) from the embedded TS sources
# via the sibling Flue CLI, and refresh source-digest.txt so the Go digest gate
# (registerPrebuiltBuiltinWorkflow / embeddedPrebuiltDigestMatches) matches
# SourceDigest(spec.Files).
#
# Run this whenever any of internal/workflows/builtin/{epic-runner,local-task-runner,
# daytona-task-runner,openshell-task-runner}.ts changes.
#
#   usage: scripts/rebuild-builtin-bundle.sh        (requires ../flue built)
#   env:   FLUE_REPO=/path/to/flue  (default: ../flue)
#
# Note: DEST is replaced wholesale (rm -rf + copy) so content-hashed assets from
# prior builds never accrete — the committed dist holds only the assets the
# current server.mjs references (it is go:embed'd into the loom binary).
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
[ -n "$FLUE_REPO" ] && [ -f "$FLUE_REPO/packages/cli/bin/flue.mjs" ] || { echo "ERROR: flue CLI not found; build ../flue or set FLUE_REPO" >&2; exit 1; }
[ -d "$FLUE_REPO/packages/runtime" ] || { echo "ERROR: $FLUE_REPO/packages/runtime missing (build flue first)" >&2; exit 1; }

# Pin: the committed bundle must be reproducible from a known flue commit. Refuse
# to rebuild against a drifted ../flue — an unpinned flue HEAD is exactly what
# silently broke 3 runtime contracts (defineWorkflow default export,
# FLUE_INTERNAL_CLI_IPC gating, strict cloneJsonSerializable). To intentionally
# advance the pin, set ALLOW_FLUE_PIN_DRIFT=1 and bump FLUE_COMMIT in the same commit.
PIN_FILE="$ROOT/internal/workflows/builtin-dist/FLUE_COMMIT"
if [ -f "$PIN_FILE" ] && [ "${ALLOW_FLUE_PIN_DRIFT:-}" != "1" ]; then
  PINNED="$(tr -d '[:space:]' < "$PIN_FILE")"
  HEAD="$(git -C "$FLUE_REPO" rev-parse HEAD 2>/dev/null || true)"
  [ "$HEAD" = "$PINNED" ] || {
    echo "ERROR: FLUE_REPO HEAD ($HEAD) != pinned $PINNED (internal/workflows/builtin-dist/FLUE_COMMIT)." >&2
    echo "       Check out flue at the pin, or set ALLOW_FLUE_PIN_DRIFT=1 and bump FLUE_COMMIT in the same commit." >&2
    exit 1
  }
fi

SRC="$ROOT/internal/workflows/builtin"
# BUILTIN_DIST_DEST lets CI build into a scratch dir for a non-destructive
# digest/reproducibility diff against the committed dist.
DEST="${BUILTIN_DIST_DEST:-$ROOT/internal/workflows/builtin-dist/epic-runner/dist}"
# The 4 files that make up the epic-runner spec (builtinEpicRunnerSpec in workflows.go).
SPEC_FILES=(epic-runner.ts local-task-runner.ts daytona-task-runner.ts openshell-task-runner.ts)

STAGE="$(mktemp -d -t loom-bundle-regen.XXXXXX)"
echo "==> staging epic-runner workflow repo at $STAGE"
mkdir -p "$STAGE/workflows"
# shellcheck source=scripts/stage-builtin-modules.sh
source "$ROOT/scripts/stage-builtin-modules.sh"
stage_builtin_node_modules "$STAGE"
printf '%s\n' '{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}' > "$STAGE/package.json"
for f in "${SPEC_FILES[@]}"; do cp "$SRC/$f" "$STAGE/workflows/$f"; done

echo "==> flue build --target node --root $STAGE --output $STAGE/dist"
( cd "$STAGE"; node "$FLUE_REPO/packages/cli/bin/flue.mjs" build --target node --root "$STAGE" --output "$STAGE/dist" )
[ -f "$STAGE/dist/server.mjs" ] || { echo "ERROR: flue build produced no dist/server.mjs" >&2; exit 1; }

# Digest = byte-exact mirror of workflows.SourceDigest over the spec files. The
# file list comes from SPEC_FILES (single source of truth in this script) — it
# must stay in sync with builtinEpicRunnerSpec() in internal/workflows/workflows.go.
DIGEST="$(node -e '
const fs=require("fs"),crypto=require("crypto");
const dir=process.argv[1], names=process.argv.slice(2);
const files=names.map(n=>["workflows/"+n, fs.readFileSync(dir+"/"+n)]).sort((a,b)=>a[0]<b[0]?-1:1);
const h=crypto.createHash("sha256"),NUL=Buffer.from([0]);
for(const [k,c] of files){h.update(Buffer.from(k,"utf8"));h.update(NUL);h.update(c);h.update(NUL);}
process.stdout.write("sha256:"+h.digest("hex"));
' "$SRC" "${SPEC_FILES[@]}")"
printf '%s\n' "$DIGEST" > "$STAGE/dist/source-digest.txt"
echo "==> source-digest.txt = $DIGEST"

echo "==> copying dist -> $DEST"
# Replace DEST wholesale: flue emits content-hashed asset names per build, so
# copying over an existing dist would let stale assets/* from prior builds
# accrete indefinitely (dead weight in the go:embed binary). The fresh
# STAGE/dist holds exactly the assets the new server.mjs references.
rm -rf "$DEST"
mkdir -p "$DEST"
cp -R "$STAGE/dist/." "$DEST/"
echo "==> done. staging kept at $STAGE (remove manually if desired)"