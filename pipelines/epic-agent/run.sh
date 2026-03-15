#!/usr/bin/env bash
# Usage: EPIC_ID=beads-xxx ./pipelines/epic-agent/run.sh [--resume] [--dry-run]
#
# Runs the epic-agent pipeline against the current loomcli repo.
# Requires: agentflow binary on PATH, bd (beads) configured, claude CLI.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIPELINE_YAML="$SCRIPT_DIR/pipeline.yaml"

: "${EPIC_ID:?EPIC_ID is required (e.g. EPIC_ID=beads-xxx ./run.sh)}"

echo "Pipeline:     epic-agent"
echo "Epic:         $EPIC_ID"
echo "Work dir:     $REPO_ROOT"
echo ""

exec agentflow run "$PIPELINE_YAML" \
  --work-dir="$REPO_ROOT" \
  --pipeline-root="$SCRIPT_DIR" \
  "$@"
