#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "[slack-demo] Delegating to scripts/run-slack-codex-epic-runner-stack.sh"
exec "${ROOT_DIR}/scripts/run-slack-codex-epic-runner-stack.sh" "$@"
