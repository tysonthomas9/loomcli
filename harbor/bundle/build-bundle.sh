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

[ -f "$LOOMCLI/go.mod" ] || { echo "FATAL: $LOOMCLI is not the loomcli root" >&2; exit 1; }
[ -f "$FLEETDB/go.mod" ] || { echo "FATAL: fleet-db checkout not found at $FLEETDB (set FLEETDB_DIR)" >&2; exit 1; }

LOOM_SHA="$(git -C "$LOOMCLI" rev-parse --short HEAD)"
FLEET_SHA="$(git -C "$FLEETDB" rev-parse --short HEAD)"

for arch in $ARCHES; do
  out="$HERE/dist/linux-$arch"
  rm -rf "$out"
  mkdir -p "$out/bin"
  echo "== building loom linux/$arch ($LOOM_SHA) =="
  (cd "$LOOMCLI" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -ldflags="-X github.com/tysonthomas9/loomcli/internal/cli.Build=$LOOM_SHA" \
    -o "$out/bin/loom" ./cmd/loom)
  echo "== building fleet-db linux/$arch ($FLEET_SHA) =="
  (cd "$FLEETDB" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -o "$out/bin/fleet-db" ./cmd/fleet-db)
  echo "== building leadmsg linux/$arch =="
  (cd "$LOOMCLI" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$out/bin/leadmsg" ./harbor/cmd/leadmsg)
  printf 'loom=%s\nfleet-db=%s\nbuilt=%s\n' "$LOOM_SHA" "$FLEET_SHA" "$(date -u +%FT%TZ)" > "$out/bin/VERSION"
  tar -C "$out" -czf "$HERE/dist/loom-bundle-linux-$arch.tar.gz" bin
  echo "== wrote $HERE/dist/loom-bundle-linux-$arch.tar.gz =="
done
