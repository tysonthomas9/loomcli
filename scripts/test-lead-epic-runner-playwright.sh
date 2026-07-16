#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$ROOT/internal/webui/frontend"
FLEET_DB_REPO="${FLEET_DB_REPO:-$ROOT/../fleet-db}"
FLUE_REPO="${FLUE_REPO:-$ROOT/../flue}"
E2E_PORT="${E2E_PORT:-19171}"
E2E_FRONTEND_PORT="${E2E_FRONTEND_PORT:-19172}"
RUN_ROOT="$ROOT/tmp/lead-epic-runner"
TASK_RUNNER_LOG="${LEAD_EPIC_RUNNER_TASK_LOG:-$RUN_ROOT/task-runner.log}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

json_array() {
  node -e 'process.stdout.write(JSON.stringify(process.argv.slice(1)))' "$@"
}

require_cmd git
require_cmd node
require_cmd npx

if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
  echo "FleetDB repo not found at $FLEET_DB_REPO" >&2
  echo "Set FLEET_DB_REPO to the sibling fleet-db checkout." >&2
  exit 2
fi

fleet_head="$(git -C "$FLEET_DB_REPO" rev-parse --short HEAD)"
mkdir -p "$RUN_ROOT"
rm -f "$TASK_RUNNER_LOG"

export FLEET_DB_REPO
export FLEET_DB_BIN="${FLEET_DB_BIN:-$RUN_ROOT/fleet-db-$fleet_head}"
export E2E_PORT
export E2E_FRONTEND_PORT
export LEAD_EPIC_RUNNER_TASK_LOG="$TASK_RUNNER_LOG"
export LOOM_DRIVER_EXECUTOR=1
export LOOM_DRIVER_EXECUTOR_NODE_ID="${LOOM_DRIVER_EXECUTOR_NODE_ID:-lead-epic-runner-e2e-node}"
export LOOM_DRIVER_TASK_RUNNER_CMD_JSON="${LOOM_DRIVER_TASK_RUNNER_CMD_JSON:-$(json_array "$(command -v node)" "$ROOT/scripts/lead-epic-runner-task-runner.mjs")}"
export RUN_INTEGRATION_TESTS=1
export RUN_LEAD_EPIC_RUNNER_E2E=1

if [[ -d "$ROOT/sdk" ]]; then
  export LOOM_SDK_ROOT="${LOOM_SDK_ROOT:-$ROOT/sdk}"
fi

if [[ -z "${LOOM_REAL_FLUE_CMD_JSON:-}" && -z "${LOOM_REAL_FLUE_CMD:-}" ]]; then
  if [[ -f "$FLUE_REPO/packages/cli/bin/flue.mjs" && -f "$FLUE_REPO/packages/cli/dist/flue.js" ]]; then
    export LOOM_REAL_FLUE_CMD_JSON="$(json_array "$(command -v node)" "$FLUE_REPO/packages/cli/bin/flue.mjs")"
  else
    echo "Flue CLI dist not found at $FLUE_REPO/packages/cli/dist/flue.js" >&2
    echo "Build the sibling flue checkout first, or set LOOM_REAL_FLUE_CMD_JSON." >&2
    exit 2
  fi
fi

echo "[lead-epic-runner-e2e] loomcli: $ROOT"
echo "[lead-epic-runner-e2e] fleet-db: $FLEET_DB_REPO ($fleet_head)"
echo "[lead-epic-runner-e2e] api/frontend: $E2E_PORT/$E2E_FRONTEND_PORT"
echo "[lead-epic-runner-e2e] task log: $TASK_RUNNER_LOG"

cd "$FRONTEND_DIR"
exec npx playwright test --project=integration tests/e2e/integration/lead-epic-runner.integration.spec.ts "$@"
