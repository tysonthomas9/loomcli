#!/usr/bin/env bash
# Build static linux loom + fleet-db binaries for the SWE-Marathon Harbor agent.
#
# Output: harbor/bundle/dist/loom-bundle-linux-<arch>.tar.gz  (contains bin/loom, bin/fleet-db, VERSION)
# The Harbor adapter (loom_harbor) uploads the tarball matching the container arch,
# fail-closed on unknown arch.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
LOOMCLI="$(cd "$HERE/../.." && pwd)"
FLEETDB="${FLEETDB_DIR:-$LOOMCLI/../fleet-db}"
ARCHES="${ARCHES:-arm64 amd64}"

die() { echo "FATAL: $*" >&2; exit 1; }

[ -f "$LOOMCLI/go.mod" ] || die "$LOOMCLI is not the loomcli root"
[ -f "$FLEETDB/go.mod" ] || die "fleet-db checkout not found at $FLEETDB (set FLEETDB_DIR)"

FLEETDB_SRC="$FLEETDB"
FLEETDB_TMP_ROOT=""
cleanup_fleetdb_worktree() {
  if [ -n "$FLEETDB_TMP_ROOT" ]; then
    git -C "$FLEETDB" worktree remove --force "$FLEETDB_SRC" >/dev/null 2>&1 || true
    rmdir "$FLEETDB_TMP_ROOT" >/dev/null 2>&1 || true
  fi
}
trap cleanup_fleetdb_worktree EXIT
if [ -n "${FLEETDB_REF:-}" ]; then
  FLEETDB_TMP_ROOT="$(mktemp -d)"
  FLEETDB_SRC="$FLEETDB_TMP_ROOT/fleet-db"
  git -C "$FLEETDB" worktree add --detach "$FLEETDB_SRC" "$FLEETDB_REF"
fi

LOOM_SHA="$(git -C "$LOOMCLI" rev-parse --short HEAD)"
FLEET_SHA="$(git -C "$FLEETDB_SRC" rev-parse --short HEAD)"
if ! grep -q 'ExcludeLabels' "$FLEETDB_SRC/internal/models/role.go" 2>/dev/null \
    && ! grep -R -q --include='*.go' 'ExcludeLabels' "$FLEETDB_SRC/internal" 2>/dev/null; then
  die "fleet-db at $FLEET_SHA lacks role Labels/ExcludeLabels (fleet-db #151); rebuild with FLEETDB_REF=origin/main or newer"
fi

for arch in $ARCHES; do
  out="$HERE/dist/linux-$arch"
  rm -rf "$out"
  mkdir -p "$out/bin"
  echo "== building loom linux/$arch ($LOOM_SHA) =="
  (cd "$LOOMCLI" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=$LOOM_SHA" \
    -o "$out/bin/loom" ./cmd/loom)
  echo "== building fleet-db linux/$arch ($FLEET_SHA) =="
  (cd "$FLEETDB_SRC" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -o "$out/bin/fleet-db" ./cmd/fleet-db)
  echo "== building leadmsg linux/$arch =="
  (cd "$LOOMCLI" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out/bin/leadmsg" ./harbor/cmd/leadmsg)
  printf 'loom=%s\nfleet-db=%s\nfleetdb_caps=role-labels\nbuilt=%s\n' "$LOOM_SHA" "$FLEET_SHA" "$(date -u +%FT%TZ)" > "$out/bin/VERSION"
  tar -C "$out" -czf "$HERE/dist/loom-bundle-linux-$arch.tar.gz" bin
  echo "== wrote $HERE/dist/loom-bundle-linux-$arch.tar.gz =="
done
