#!/usr/bin/env bash
# parallel-review.sh — set up N isolated sandboxes and run their agents CONCURRENTLY.
#
#   parallel-review.sh <plan|task> <backend> <name=worktree> [<name=worktree> ...]
#
# Example:
#   parallel-review.sh plan claude \
#     pr120=~/codebase/code-agents/loom-review-section/pr-120 \
#     pr122=~/codebase/code-agents/loom-review-section/pr-122 \
#     pr116=~/codebase/code-agents/loom-review-section/pr-116
#
# Isolation: each sandbox has its own LOOM_CONFIG_DIR (separate state, embedded fleet-db on an
# EPHEMERAL port, in-process miniredis, repo, and loom binary), so concurrent runs can't collide.
# RULE: exactly one concurrent run per sandbox — a second command in the SAME LOOM_CONFIG_DIR hits
# the embedded-fleet-db lock (ErrEmbeddedAlreadyRunning). One sandbox per PR; fan out across sandboxes.
#
# Shared ceilings (not isolated): your backend account's rate limit (the real cap on concurrency),
# /tmp disk (~55MB loom per sandbox), and CPU during the build phase (kept sequential below).
set -euo pipefail

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="${ROOT:-/tmp/loom-e2e}"
mode="${1:?usage: parallel-review.sh <plan|task> <backend> <name=worktree>...}"
backend="${2:?usage: parallel-review.sh <plan|task> <backend> <name=worktree>...}"
shift 2
[[ "$mode" == "plan" || "$mode" == "task" ]] || { echo "mode must be plan|task" >&2; exit 1; }
[[ $# -ge 1 ]] || { echo "need at least one <name=worktree>" >&2; exit 1; }
specs=("$@")

# Build fleet-db on first sandbox setup so all sandboxes share the read-only binary.
export FLEET_DB_BIN="${FLEET_DB_BIN:-$ROOT/fleet-db}"

echo "==> Phase 1: setup ${#specs[@]} sandboxes (sequential builds)"
for spec in "${specs[@]}"; do
  name="${spec%%=*}"; wt="${spec#*=}"
  [[ "$name" != "$spec" ]] || { echo "bad spec '$spec' (want name=worktree)" >&2; exit 1; }
  echo "  --- setup $name <- $wt"
  "$SELF_DIR/new-sandbox.sh" "$name" "$wt" >/dev/null
done

echo "==> Phase 2: launch ${#specs[@]} agent runs CONCURRENTLY ($mode/$backend)"
pids=(); names=()
for spec in "${specs[@]}"; do
  name="${spec%%=*}"
  out="$ROOT/$name/parallel-${mode}.out"
  "$SELF_DIR/run-agent.sh" "$name" "$mode" "$backend" >"$out" 2>&1 &
  pids+=("$!"); names+=("$name")
  echo "  launched $name (pid $!) -> $out"
done

echo "==> Phase 3: waiting for ${#pids[@]} concurrent runs to finish..."
failures=0
for i in "${!pids[@]}"; do
  if wait "${pids[$i]}"; then
    echo "  ${names[$i]}: done"
  else
    echo "  ${names[$i]}: wrapper exited nonzero"
    failures=$((failures + 1))
  fi
done

echo
echo "==================== SUMMARY ===================="
for spec in "${specs[@]}"; do
  name="${spec%%=*}"
  out="$ROOT/$name/parallel-${mode}.out"
  echo "--- $name ---"
  grep -E 'EXIT_CODE|^   HELLOWS-' "$out" 2>/dev/null | tail -6 || echo "  (no output captured: $out)"
done

if [[ "$failures" -gt 0 ]]; then
  echo
  echo "parallel review failed: $failures run(s) exited nonzero" >&2
  exit 1
fi
