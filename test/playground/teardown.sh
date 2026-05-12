#!/bin/sh
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
RUNTIME="$HERE/.runtime"

loom workspace remove PLAYGROUND --force 2>/dev/null || true
rm -rf "$RUNTIME"
echo "Playground torn down."
