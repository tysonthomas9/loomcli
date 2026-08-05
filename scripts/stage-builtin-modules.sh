#!/usr/bin/env bash
# Shared staging for builtin workflow sources. Builtins import only @loom/sdk
# and @flue/runtime; provider SDKs and plaintext credentials must stay outside
# workflow/task-runner resolution.
#
# SOURCED, not executed. Callers set ROOT and FLUE_REPO and create STAGE.

stage_builtin_node_modules() {
  local stage="$1"
  mkdir -p "$stage/node_modules/@loom" "$stage/node_modules/@flue"
  ln -s "$ROOT/sdk" "$stage/node_modules/@loom/sdk"
  ln -s "$FLUE_REPO/packages/runtime" "$stage/node_modules/@flue/runtime"
}
