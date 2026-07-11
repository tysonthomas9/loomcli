#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "[slack-demo] Delegating to smoke-test/smoke-test-slack-epic-runner-stack.sh"
exec "${ROOT_DIR}/smoke-test/smoke-test-slack-epic-runner-stack.sh" "$@"
