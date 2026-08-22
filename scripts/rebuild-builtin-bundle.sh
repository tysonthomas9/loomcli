#!/usr/bin/env bash
# Build a built-in workflow bundle (epic-runner or github-review-agent) from
# the embedded TS sources via the sibling Flue CLI, and write
# source-digest.txt so it matches SourceDigest(spec.Files). Generated bundle
# output is not committed; set BUILTIN_DIST_DEST to choose a specific output
# directory.
#
# Run this whenever any file in internal/workflows/builtin/ that belongs to
# the named built-in changes.
#
#   usage: scripts/rebuild-builtin-bundle.sh [name]   (default: epic-runner)
#          known names: epic-runner github-review-agent
#   env:   FLUE_REPO=/path/to/flue  (default: ../flue; must be at the pin)
#          BUILTIN_DIST_DEST=/path/to/output
#
# The packaged built-in lane consumes BUILTIN_DIST_DEST via
# `loom workflow package-builtin <name> --dist "$BUILTIN_DIST_DEST" --out <root>`
# (see scripts/test-packaged-builtin-devbox.sh and
# desktop/scripts/prepare-sidecar.sh, which loops over both names). The nested
# dist/node_modules/@loom/sdk runtime is staged by the packager (a real file
# copy), NOT here — the symlinks below are build-time inputs only.
#
# Note: DEST is replaced wholesale (rm -rf + copy) so content-hashed assets from
# prior builds never accrete.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The per-name file lists mirror builtinEpicRunnerSpec() /
# builtinGitHubReviewAgentSpec() in internal/workflows/workflows.go and MUST
# stay in sync with them: the digest below is computed over SPEC_FILES while
# the binary's is computed over spec.Files, and `package-builtin` refuses a
# dist whose source-digest.txt drifts from the embedded spec.
NAME="${1:-epic-runner}"
case "$NAME" in
  epic-runner)
    SPEC_FILES=(epic-runner.ts local-task-runner.ts daytona-task-runner.ts openshell-task-runner.ts)
    ;;
  github-review-agent)
    SPEC_FILES=(github-review-agent.ts github-review-task-runner.ts)
    ;;
  *)
    echo "unknown built-in $NAME (known: epic-runner github-review-agent)" >&2
    exit 2
    ;;
esac

FLUE_REPO="${FLUE_REPO:-$(cd "$ROOT/../flue" 2>/dev/null && pwd || true)}"
[ -n "$FLUE_REPO" ] && [ -f "$FLUE_REPO/packages/cli/bin/flue.mjs" ] || { echo "ERROR: flue CLI not found; build ../flue or set FLUE_REPO" >&2; exit 1; }
[ -d "$FLUE_REPO/packages/runtime" ] || { echo "ERROR: $FLUE_REPO/packages/runtime missing (build flue first)" >&2; exit 1; }

# Pin: the committed bundle must be reproducible from a known flue commit. Refuse
# to rebuild against a drifted ../flue — an unpinned flue HEAD is exactly what
# silently broke 3 runtime contracts (defineWorkflow default export,
# FLUE_INTERNAL_CLI_IPC gating, strict cloneJsonSerializable). To intentionally
# advance the pin, set ALLOW_FLUE_PIN_DRIFT=1 and bump FLUE_COMMIT in the same commit.
PIN_FILE="$ROOT/internal/workflows/FLUE_COMMIT"
if [ -f "$PIN_FILE" ] && [ "${ALLOW_FLUE_PIN_DRIFT:-}" != "1" ]; then
  PINNED="$(tr -d '[:space:]' < "$PIN_FILE")"
  HEAD="$(git -C "$FLUE_REPO" rev-parse HEAD 2>/dev/null || true)"
  [ "$HEAD" = "$PINNED" ] || {
    echo "ERROR: FLUE_REPO HEAD ($HEAD) != pinned $PINNED (internal/workflows/FLUE_COMMIT)." >&2
    echo "       Check out flue at the pin, or set ALLOW_FLUE_PIN_DRIFT=1 and bump FLUE_COMMIT in the same commit." >&2
    exit 1
  }
fi

SRC="$ROOT/internal/workflows/builtin"
# BUILTIN_DIST_DEST lets CI and local callers choose a scratch dir without
# recreating deleted generated bundle files in the repo.
DEST="${BUILTIN_DIST_DEST:-$(mktemp -d -t "loom-builtin-dist-${NAME}.XXXXXX")}"

STAGE="$(mktemp -d -t "loom-bundle-regen-${NAME}.XXXXXX")"
echo "==> staging $NAME workflow repo at $STAGE"
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
# file list comes from SPEC_FILES (chosen by $NAME above) — it must stay in
# sync with builtin*Spec() in internal/workflows/workflows.go.
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

echo "==> copying $NAME dist -> $DEST"
# Replace DEST wholesale: flue emits content-hashed asset names per build, so
# copying over an existing dist would let stale assets/* from prior builds
# accrete indefinitely (dead weight in the go:embed binary). The fresh
# STAGE/dist holds exactly the assets the new server.mjs references.
rm -rf "$DEST"
mkdir -p "$DEST"
cp -R "$STAGE/dist/." "$DEST/"
echo "==> done. staging kept at $STAGE (remove manually if desired)"
