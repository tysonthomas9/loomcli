#!/usr/bin/env bash
# Deterministic repro for LOOM-2:
# ensure-runtime hides spawn errors behind a generic health-check timeout.
#
# Before the fix: the script exits non-zero — the user-facing error mentions
# only "connection refused" / "context deadline exceeded".
# After the fix: the script exits 0 — the wrapped error or runtime.json.error
# contains the real cause from loom-serve.log ("fleet-db binary not found").
#
# Run from the repo root:
#   bash scripts/repro/loom-serve-fleetdb-missing.sh

set -uo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$repo_root/scripts/lib/sandbox.sh"
cd "$repo_root"

if [ -z "${LOOM_BIN:-}" ]; then
    echo "[repro] building ./bin/loom"
    if ! go build -o ./bin/loom ./cmd/loom; then
        echo "[repro] go build failed" >&2
        exit 2
    fi
    LOOM_BIN="$repo_root/bin/loom"
fi

if [ ! -x "$LOOM_BIN" ]; then
    echo "[repro] LOOM_BIN ($LOOM_BIN) is not executable" >&2
    exit 2
fi

if ! loom_mktemp_dir loom-serve-fleetdb-missing; then
    echo "[repro] mktemp failed" >&2
    exit 2
fi
tmp="$LOOM_SANDBOX_DIR"
trap 'rm -rf "$tmp"' EXIT INT TERM

# Scrub PATH so the spawned `loom serve` cannot find fleet-db on disk, and
# force FLEET_DB_BIN to a path that does not exist. The bundled-binary
# lookup in localEnv() also requires that fleet-db NOT live next to
# $LOOM_BIN — running this from ./bin/loom (no sibling fleet-db) satisfies
# that. If you symlinked fleet-db next to loom, this assertion will not fire.
export PATH=/usr/bin:/bin
export FLEET_DB_BIN=/nonexistent/fleet-db

out_file="$tmp/out.log"

echo "[repro] running loom local service against $tmp (35s timeout)"
# shellcheck disable=SC2086
LOOM_CONFIG_DIR="$tmp" LOOM_DESKTOP_DATA_DIR="$tmp" \
    "$LOOM_BIN" local --data-dir "$tmp" service --port 0 \
    >"$out_file" 2>&1 &
loom_pid=$!

# Wait up to 35s for the loom process to exit on its own.
waited=0
while kill -0 "$loom_pid" 2>/dev/null; do
    if [ "$waited" -ge 35 ]; then
        kill "$loom_pid" 2>/dev/null || true
        sleep 1
        kill -9 "$loom_pid" 2>/dev/null || true
        break
    fi
    sleep 1
    waited=$((waited + 1))
done
wait "$loom_pid" 2>/dev/null
exit_code=$?

echo "[repro] loom exited (status=$exit_code)"
echo "----- captured output -----"
cat "$out_file"
echo "---------------------------"

runtime_err=""
if [ -f "$tmp/runtime.json" ]; then
    if command -v jq >/dev/null 2>&1; then
        runtime_err="$(jq -r '.error // ""' <"$tmp/runtime.json")"
    else
        runtime_err="$(cat "$tmp/runtime.json")"
    fi
    echo "----- runtime.json.error -----"
    printf '%s\n' "$runtime_err"
    echo "------------------------------"
fi

# Match either the precise wording from the original report
# ("fleet-db binary not found") OR the current code's equivalent surface
# ("failed to open fleet-db store" + a hint to install/set FLEET_DB_BIN).
# Both confirm the real cause is now visible.
fleetdb_signature='failed to open fleet-db store|fleet-db binary not found'

if grep -Eq "$fleetdb_signature" "$out_file" 2>/dev/null; then
    echo "[repro] OK — fleet-db spawn failure surfaced in CLI output"
    exit 0
fi
if printf '%s' "$runtime_err" | grep -Eq "$fleetdb_signature"; then
    echo "[repro] OK — fleet-db spawn failure surfaced in runtime.json.error"
    exit 0
fi

echo "[repro] FAIL — expected fleet-db spawn failure in CLI output or runtime.json.error" >&2
exit 1
