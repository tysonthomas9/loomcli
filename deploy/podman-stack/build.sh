#!/usr/bin/env bash
# Build the podman-stack images.
#
#   1. Cross-compile loom + fleet-db/fdb on the HOST for linux/<podman machine
#      arch> (CGO_ENABLED=0 — both binaries are pure Go; no Go toolchain ships
#      in any image).
#   2. Stage a minimal build context in a private mktemp dir (binaries, the
#      @loom/sdk package root, the flue workspace source + host-built dist
#      with node_modules excluded — host node_modules are darwin-native).
#   3. podman build the four images:
#        localhost/loom-stack/fleet-db
#        localhost/loom-stack/loom-serve
#        localhost/loom-stack/loom-worker
#        localhost/loom-stack/stub-upstream
#
# Fails loudly on any step (set -Eeuo pipefail + explicit checks). The only
# rm in this script targets the script's own mktemp dir.
#
# Env overrides:
#   LOOMCLI_REPO / FLEET_DB_REPO / FLUE_REPO   repo locations (default: siblings)
#   LOOM_STACK_GOARCH                          target arch (default: podman machine arch)
#   LOOM_STACK_IMAGE_PREFIX                    image name prefix (default: localhost/loom-stack)

set -Eeuo pipefail

STACK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOMCLI_REPO="${LOOMCLI_REPO:-$(cd "$STACK_DIR/../.." && pwd)}"
FLEET_DB_REPO="${FLEET_DB_REPO:-$(cd "$LOOMCLI_REPO/../fleet-db" 2>/dev/null && pwd || true)}"
FLUE_REPO="${FLUE_REPO:-$(cd "$LOOMCLI_REPO/../flue" 2>/dev/null && pwd || true)}"
IMAGE_PREFIX="${LOOM_STACK_IMAGE_PREFIX:-localhost/loom-stack}"

die() { echo "ERROR: $*" >&2; exit 1; }
log() { printf '\n==> %s\n' "$*"; }

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
require_cmd go
require_cmd podman
require_cmd rsync

[[ -d "$LOOMCLI_REPO/cmd/loom" ]] || die "loomcli repo not found at $LOOMCLI_REPO (set LOOMCLI_REPO)"
[[ -n "$FLEET_DB_REPO" && -d "$FLEET_DB_REPO/cmd/fleet-db" ]] || die "fleet-db repo not found (set FLEET_DB_REPO)"
[[ -n "$FLUE_REPO" && -d "$FLUE_REPO/packages/cli" ]] || die "flue repo not found (set FLUE_REPO)"
[[ -d "$LOOMCLI_REPO/sdk" ]] || die "@loom/sdk package root not found at $LOOMCLI_REPO/sdk"

# Flue dist is built on the HOST (the image only does a prod install). Refuse
# to bake a stale-free image without it.
if [[ ! -f "$FLUE_REPO/packages/cli/dist/flue.js" ]]; then
  die "flue CLI dist missing ($FLUE_REPO/packages/cli/dist/flue.js).
Build it on the host first:  (cd \"$FLUE_REPO\" && pnpm install && pnpm build)"
fi

# Flue pin enforcement. The image bakes ../flue (rsync'd to flue-src below) AND the
# prebuilt epic-runner bundle, which the runner resolves @flue/runtime against at
# runtime — so the two MUST agree. The bundle is reproducible only from the flue
# commit recorded in internal/workflows/builtin-dist/FLUE_COMMIT (enforced by
# scripts/rebuild-builtin-bundle.sh). rebuild-builtin-bundle.sh guarded its own path;
# this guards the image build. Without it, a `git pull` in ../flue silently bakes a
# drifted flue whose renamed APIs break the bundled runner at runtime (this is exactly
# how configureProvider->registerProvider and ctx.init->initializeRootHarness slipped
# through on 2026-06-25). To advance intentionally: run scripts/rebuild-builtin-bundle.sh,
# commit the new bundle + bumped FLUE_COMMIT, then build with ALLOW_FLUE_PIN_DRIFT=1.
FLUE_PIN_FILE="$LOOMCLI_REPO/internal/workflows/builtin-dist/FLUE_COMMIT"
if [[ -f "$FLUE_PIN_FILE" && "${ALLOW_FLUE_PIN_DRIFT:-}" != "1" ]]; then
  FLUE_PINNED="$(tr -d '[:space:]' < "$FLUE_PIN_FILE")"
  FLUE_HEAD="$(git -C "$FLUE_REPO" rev-parse HEAD 2>/dev/null || true)"
  if [[ -n "$FLUE_PINNED" && "$FLUE_HEAD" != "$FLUE_PINNED" ]]; then
    die "flue pin drift: ../flue HEAD ($FLUE_HEAD) != pinned $FLUE_PINNED ($FLUE_PIN_FILE).
The prebuilt epic-runner bundle was built against the pinned commit; baking a drifted flue
silently breaks the bundled runner. Either pin flue back:
    git -C \"$FLUE_REPO\" checkout $FLUE_PINNED
or advance intentionally: run scripts/rebuild-builtin-bundle.sh, commit the new bundle +
FLUE_COMMIT, then re-run this build with ALLOW_FLUE_PIN_DRIFT=1."
  fi
fi

podman info >/dev/null 2>&1 || die "podman is not reachable — is the podman machine running? (podman machine start)"

# Target arch = the podman machine's Linux arch, not the macOS host's.
if [[ -n "${LOOM_STACK_GOARCH:-}" ]]; then
  GOARCH_TARGET="$LOOM_STACK_GOARCH"
else
  PODMAN_ARCH="$(podman info --format '{{.Host.Arch}}')"
  case "$PODMAN_ARCH" in
    arm64|aarch64) GOARCH_TARGET=arm64 ;;
    amd64|x86_64)  GOARCH_TARGET=amd64 ;;
    *) die "unsupported podman machine arch: $PODMAN_ARCH (set LOOM_STACK_GOARCH)" ;;
  esac
fi
log "target platform: linux/$GOARCH_TARGET"

TMP_ROOT="$(mktemp -d -t loom-podman-stack-build.XXXXXX)"
cleanup() { rm -rf "$TMP_ROOT"; }
trap cleanup EXIT

CTX="$TMP_ROOT/ctx"
mkdir -p "$CTX/bin"

log "cross-compiling Go binaries (CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH_TARGET)"
(
  cd "$LOOMCLI_REPO"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_TARGET" \
    go build -ldflags "-s -w" -o "$CTX/bin/loom" ./cmd/loom
)
(
  cd "$FLEET_DB_REPO"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_TARGET" \
    go build -ldflags "-s -w" -o "$CTX/bin/fleet-db" ./cmd/fleet-db
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_TARGET" \
    go build -ldflags "-s -w" -o "$CTX/bin/fdb" ./cmd/fdb
)
for bin in loom fleet-db fdb; do
  [[ -s "$CTX/bin/$bin" ]] || die "build produced empty binary: $bin"
done

log "staging @loom/sdk and flue workspace source"
rsync -a --exclude='node_modules/' "$LOOMCLI_REPO/sdk/" "$CTX/loom-sdk/"
# Keep dist/ (host-built, platform-independent JS); drop node_modules
# (darwin-native), VCS and build caches. Workspace package.jsons must all
# stay so the in-image `pnpm install --frozen-lockfile --prod` matches the
# lockfile importers.
rsync -a \
  --exclude='.git/' \
  --exclude='node_modules/' \
  --exclude='.turbo/' \
  --exclude='.cache/' \
  "$FLUE_REPO/" "$CTX/flue-src/"

cp "$STACK_DIR/worker-entrypoint.sh" "$CTX/worker-entrypoint.sh"
cp "$STACK_DIR/serve-entrypoint.sh" "$CTX/serve-entrypoint.sh"
mkdir -p "$CTX/stub-upstream"
cp "$STACK_DIR/stub-upstream/server.mjs" "$CTX/stub-upstream/server.mjs"

# A1 github-review-agent provisioner plus the generic task-runner invoker
# baked into the loom-serve image where the embedded driver executor runs
# codex-backed review TaskRuns.
A1_DIR="${A1_REVIEW_DIR:-$LOOMCLI_REPO/deploy/agents/a1-github-review}"
[[ -f "$A1_DIR/setup.sh" ]] || die "A1 setup script not found at $A1_DIR/setup.sh"
[[ -f "$LOOMCLI_REPO/scripts/loom-task-runner-invoker.mjs" ]] || die "task runner invoker not found at $LOOMCLI_REPO/scripts/loom-task-runner-invoker.mjs"
mkdir -p "$CTX/a1-github-review"
cp "$A1_DIR/setup.sh" "$CTX/a1-github-review/setup.sh"
cp "$LOOMCLI_REPO/scripts/loom-task-runner-invoker.mjs" "$CTX/loom-task-runner-invoker.mjs"

# Normalize staged permissions. The caller may run under a restrictive umask
# (the acceptance driver sets umask 077 for its secrets), which would bake
# root-owned, world-unreadable files into the images — the non-root container
# users (fleet, node) then fail with EACCES / "Permission denied" at exec.
chmod -R u+rwX,go+rX "$CTX"
chmod 0755 "$CTX/bin/"* "$CTX/worker-entrypoint.sh" "$CTX/serve-entrypoint.sh" "$CTX/loom-task-runner-invoker.mjs"

build_image() {
  local name="$1" containerfile="$2"
  log "podman build $IMAGE_PREFIX/$name:latest"
  podman build \
    --platform "linux/$GOARCH_TARGET" \
    -f "$STACK_DIR/$containerfile" \
    -t "$IMAGE_PREFIX/$name:latest" \
    "$CTX"
}

build_image stub-upstream Containerfile.stub-upstream
build_image fleet-db      Containerfile.fleet-db
build_image loom-worker   Containerfile.worker
build_image loom-serve    Containerfile.loom-serve

log "images built:"
podman images --filter "reference=$IMAGE_PREFIX/*" \
  --format '  {{.Repository}}:{{.Tag}}  {{.Size}}'
