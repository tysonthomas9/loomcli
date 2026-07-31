#!/usr/bin/env bash
# Build one built-in workflow bundle from the embedded TS sources via the
# pinned Flue CLI, and refresh source-digest.txt so it matches
# SourceDigest(spec.Files). Generated bundle output is not committed; set
# BUILTIN_DIST_DEST to choose a specific output directory. epic-runner remains
# the default for existing CI callers; desktop packaging builds every built-in.
#
#   usage: scripts/rebuild-builtin-bundle.sh        (requires pinned Flue built)
#   env:   FLUE_REPO=/path/to/flue  (default: ../flue)
#          BUILTIN_DIST_DEST=/path/to/output
#          BUILTIN_WORKFLOW=<built-in workflow name> (default: epic-runner)
#          KEEP_BUILTIN_STAGE=1  (retain the temporary build tree for debugging)
#
# Note: DEST is replaced wholesale (rm -rf + copy) so content-hashed assets from
# prior builds never accrete.
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
PIN_FILE="$ROOT/internal/infra/workflowdistribution/FLUE_COMMIT"
if [ -f "$PIN_FILE" ] && [ "${ALLOW_FLUE_PIN_DRIFT:-}" != "1" ]; then
  PINNED="$(tr -d '[:space:]' < "$PIN_FILE")"
  HEAD="$(git -C "$FLUE_REPO" rev-parse HEAD 2>/dev/null || true)"
  [ "$HEAD" = "$PINNED" ] || {
    echo "ERROR: FLUE_REPO HEAD ($HEAD) != pinned $PINNED (internal/infra/workflowdistribution/FLUE_COMMIT)." >&2
    echo "       Check out flue at the pin, or set ALLOW_FLUE_PIN_DRIFT=1 and bump FLUE_COMMIT in the same commit." >&2
    exit 1
  }
fi

SRC="$ROOT/internal/infra/workflowdistribution/builtin"
BUILTIN_WORKFLOW="${BUILTIN_WORKFLOW:-epic-runner}"
# BUILTIN_DIST_DEST lets CI and local callers choose a scratch dir without
# recreating deleted generated bundle files in the repo.
DEST="${BUILTIN_DIST_DEST:-$(mktemp -d -t loom-builtin-dist.XXXXXX)}"
case "$BUILTIN_WORKFLOW" in
  epic-runner)
    # builtinEpicRunnerSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(epic-runner.ts local-task-runner.ts daytona-task-runner.ts openshell-task-runner.ts)
    ;;
  github-review-agent)
    # builtinGitHubReviewAgentSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(github-review-agent.ts github-review-task-runner.ts)
    ;;
  bug-fix-agent)
    # builtinBugFixAgentSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(bug-fix-agent.ts local-task-runner.ts daytona-task-runner.ts)
    ;;
  review-loop-agent)
    # builtinReviewLoopAgentSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(review-loop-agent.ts github-review-task-runner.ts)
    ;;
  local-review-agent)
    # builtinLocalReviewAgentSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(local-review-agent.ts github-review-task-runner.ts)
    ;;
  prompt-agent)
    # builtinPromptAgentSpec in internal/infra/workflowdistribution/catalog_build.go.
    SPEC_FILES=(prompt-agent.ts local-task-runner.ts)
    ;;
  *)
    echo "ERROR: unsupported BUILTIN_WORKFLOW=$BUILTIN_WORKFLOW" >&2
    exit 2
    ;;
esac

STAGE="$(mktemp -d -t loom-bundle-regen.XXXXXX)"
cleanup_stage() {
  if [ "${KEEP_BUILTIN_STAGE:-0}" = "1" ]; then
    echo "==> staging kept at $STAGE"
    return
  fi
  if ! rm -rf -- "$STAGE"; then
    echo "WARNING: failed to remove temporary staging tree $STAGE" >&2
  fi
}
trap cleanup_stage EXIT

echo "==> staging $BUILTIN_WORKFLOW workflow repo at $STAGE"
mkdir -p "$STAGE/workflows"
# shellcheck source=scripts/stage-builtin-modules.sh
source "$ROOT/scripts/stage-builtin-modules.sh"
stage_builtin_node_modules "$STAGE"
printf '%s\n' '{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}' > "$STAGE/package.json"
for f in "${SPEC_FILES[@]}"; do cp "$SRC/$f" "$STAGE/workflows/$f"; done

echo "==> flue build --target node --root $STAGE --output $STAGE/dist"
( cd "$STAGE"; node "$FLUE_REPO/packages/cli/bin/flue.mjs" build --target node --root "$STAGE" --output "$STAGE/dist" )
[ -f "$STAGE/dist/server.mjs" ] || { echo "ERROR: flue build produced no dist/server.mjs" >&2; exit 1; }

# Rolldown emits source-region and source-map comments into executable output.
# Those comments retain the absolute Flue checkout/build paths even after the
# .map files are removed for Desktop. Strip only those non-runtime annotations,
# then fail if a known builder root remains in executable JavaScript.
# shellcheck disable=SC2016 # JavaScript template expressions are intentionally single-quoted from the shell.
node -e '
const fs = require("fs"), path = require("path");
const root = process.argv[1], forbidden = process.argv.slice(2).filter(Boolean);
function files(dir) {
  return fs.readdirSync(dir, { withFileTypes: true })
    .sort((a, b) => a.name.localeCompare(b.name))
    .flatMap((entry) => {
      const item = path.join(dir, entry.name);
      return entry.isDirectory() ? files(item) : [item];
    });
}
const annotation = /^\s*\/\/#(?:(?:end)?region(?:\s.*)?|\s*sourceMappingURL=.*)$/;
const leaks = [];
for (const file of files(root)) {
  if (!/\.(?:cjs|mjs|js)$/.test(file)) continue;
  const original = fs.readFileSync(file, "utf8");
  const cleaned = original.split("\n").filter((line) => !annotation.test(line)).join("\n");
  if (cleaned !== original) fs.writeFileSync(file, cleaned);
  for (const builderRoot of forbidden) {
    if (cleaned.includes(builderRoot)) leaks.push(`${file}: ${builderRoot}`);
  }
}
if (leaks.length > 0) {
  process.stderr.write(`ERROR: generated workflow bundle retained builder paths:\n${leaks.join("\n")}\n`);
  process.exit(1);
}
' "$STAGE/dist" "$STAGE" "$FLUE_REPO" "$ROOT"

# Digest = byte-exact mirror of workflowcatalog.SourceDigest over the spec files. The
# file list comes from SPEC_FILES (single source of truth in this script) — it
# must stay in sync with the matching builtin*Spec() in
# internal/infra/workflowdistribution/catalog_build.go.
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
echo "==> done"
