#!/usr/bin/env bash
# Usage: EPIC_ID=beads-xxx ./pipelines/epic-agent/run-parallel.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIPELINE_YAML="$SCRIPT_DIR/pipeline.yaml"
REVIEW_YAML="$SCRIPT_DIR/review-merge.yaml"

: "${EPIC_ID:?EPIC_ID is required}"

NUM_WORKERS="${NUM_WORKERS:-4}"
MAX_RETRIES="${MAX_RETRIES:-3}"
INTEGRATION_BRANCH=$(git -C "$REPO_ROOT" branch --show-current)
RUNS_DIR="$REPO_ROOT/.agentflow-runs/$EPIC_ID-parallel"
WORKTREES_DIR="$REPO_ROOT/.agentflow-worktrees"
LAYERS_FILE="$RUNS_DIR/layers.json"

mkdir -p "$RUNS_DIR"

log()  { echo "$(date '+%H:%M:%S') [parallel] $*"; }
logw() { echo "$(date '+%H:%M:%S') [worker-$1] $2"; }

echo ""
echo "════════════════════════════════════════════════"
echo "  Parallel Epic Agent"
echo "════════════════════════════════════════════════"
echo "  Epic:          $EPIC_ID"
echo "  Workers:       $NUM_WORKERS"
echo "  Branch:        $INTEGRATION_BRANCH"
echo "  Runs dir:      $RUNS_DIR"
echo "════════════════════════════════════════════════"
echo ""

# ── Step 1: Layer planning ──
log "Step 1: Computing execution layers..."
if [ -f "$LAYERS_FILE" ]; then
  log "Using existing layers.json"
else
  EPIC_ID="$EPIC_ID" claude -p "$(cat "$SCRIPT_DIR/prompts/plan-layers.md")

IMPORTANT: Write layers.json to the current working directory." \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose \
    2>"$RUNS_DIR/plan-layers-stderr.log" | \
    tee "$RUNS_DIR/plan-layers-transcript.jsonl" | \
    jq -r 'select(.type == "result") | .result // empty' > "$RUNS_DIR/plan-layers-output.txt" 2>/dev/null || true
  if [ -f "$REPO_ROOT/layers.json" ] && [ ! -f "$LAYERS_FILE" ]; then
    mv "$REPO_ROOT/layers.json" "$LAYERS_FILE"
  fi
  if [ ! -f "$LAYERS_FILE" ]; then
    log "ERROR: AI agent did not produce layers.json"
    exit 1
  fi
fi

NUM_LAYERS=$(jq '.layers | length' "$LAYERS_FILE")
log "$NUM_LAYERS layers, $(jq '[.layers[].tasks[]] | length' "$LAYERS_FILE") tasks"
jq -r '.layers[] | "  Layer \(.layer) (\(.conflict_risk) risk): \([.tasks[].id] | join(", "))"' "$LAYERS_FILE"
echo ""

# ── Step 2: Create worktrees ──
log "Step 2: Creating $NUM_WORKERS worktrees..."
BASE_COMMIT=$(git -C "$REPO_ROOT" rev-parse HEAD)
log "Base commit: $(echo $BASE_COMMIT | head -c 8)"
mkdir -p "$WORKTREES_DIR"
for i in $(seq 1 "$NUM_WORKERS"); do
  wt="$WORKTREES_DIR/worker-$i"
  branch="epic-worker-$i"
  git -C "$REPO_ROOT" worktree remove --force "$wt" 2>/dev/null || true
  git -C "$REPO_ROOT" branch -D "$branch" 2>/dev/null || true
  git -C "$REPO_ROOT" worktree add "$wt" -b "$branch" "$BASE_COMMIT"
  logw "$i" "Created (branch: $branch)"
done
echo ""

# ── Step 3: Execute layers ──
TOTAL_COMPLETED=0
TOTAL_FAILED=0

for layer_idx in $(seq 0 $((NUM_LAYERS - 1))); do
  LAYER_TASKS=$(jq -r ".layers[$layer_idx].tasks" "$LAYERS_FILE")
  LAYER_RISK=$(jq -r ".layers[$layer_idx].conflict_risk" "$LAYERS_FILE")
  TASK_COUNT=$(echo "$LAYER_TASKS" | jq 'length')

  echo ""
  echo "════════════════════════════════════════════════"
  echo "  Layer $layer_idx / $((NUM_LAYERS - 1)) — $TASK_COUNT tasks ($LAYER_RISK risk)"
  echo "════════════════════════════════════════════════"

  # State dir — dot-prefixed to avoid cleanup glob matches
  STATE_DIR="$RUNS_DIR/.state-layer-${layer_idx}"
  mkdir -p "$STATE_DIR"
  LAYER_BASE=$(git -C "$REPO_ROOT" rev-parse HEAD)

  # ── 3a. Launch workers ──
  PIDS=""
  ACTIVE_WORKERS=""

  for task_idx in $(seq 0 $((TASK_COUNT - 1))); do
    tid=$(echo "$LAYER_TASKS" | jq -r ".[$task_idx].id")
    wnum=$(echo "$LAYER_TASKS" | jq -r ".[$task_idx].worker")
    ttitle=$(echo "$LAYER_TASKS" | jq -r ".[$task_idx].title")
    wt="$WORKTREES_DIR/worker-$wnum"
    pass_dir="$RUNS_DIR/layer-${layer_idx}-task-${tid}"
    mkdir -p "$pass_dir"

    echo "$tid" > "$STATE_DIR/w${wnum}-task"
    logw "$wnum" "Starting: $tid — $ttitle"

    EPIC_ID="$EPIC_ID" TASK_ID="$tid" BD_ROOT="$REPO_ROOT" \
      agentflow run "$PIPELINE_YAML" \
      --work-dir="$wt" \
      --results-dir="$pass_dir" \
      --pipeline-root="$SCRIPT_DIR" \
      > "$pass_dir/agentflow-stdout.log" 2>&1 &

    echo "$!" > "$STATE_DIR/w${wnum}-pid"
    PIDS="$PIDS $!"
    ACTIVE_WORKERS="$ACTIVE_WORKERS $wnum"
  done

  # ── 3b. Wait for workers ──
  log "Waiting for $(echo $ACTIVE_WORKERS | wc -w | tr -d ' ') workers..."

  for wnum in $ACTIVE_WORKERS; do
    pid=$(cat "$STATE_DIR/w${wnum}-pid")
    tid=$(cat "$STATE_DIR/w${wnum}-task")
    wt="$WORKTREES_DIR/worker-$wnum"

    set +e; wait "$pid"; ec=$?; set -e

    if [ $ec -eq 0 ]; then
      logw "$wnum" "SUCCESS: $tid"
      TOTAL_COMPLETED=$((TOTAL_COMPLETED + 1))
    else
      logw "$wnum" "FAILED: $tid (exit $ec)"
      result="failed"
      for retry in $(seq 1 "$MAX_RETRIES"); do
        logw "$wnum" "Retry $retry/$MAX_RETRIES for $tid"
        (cd "$REPO_ROOT" && bd update "$tid" --status=open) 2>/dev/null || true
        retry_dir="$RUNS_DIR/layer-${layer_idx}-task-${tid}-retry-${retry}"
        mkdir -p "$retry_dir"
        set +e
        EPIC_ID="$EPIC_ID" TASK_ID="$tid" BD_ROOT="$REPO_ROOT" \
          agentflow run "$PIPELINE_YAML" \
          --work-dir="$wt" --results-dir="$retry_dir" --pipeline-root="$SCRIPT_DIR" \
          > "$retry_dir/agentflow-stdout.log" 2>&1
        ec=$?; set -e
        if [ $ec -eq 0 ]; then
          logw "$wnum" "SUCCESS on retry $retry: $tid"
          result="passed"
          TOTAL_COMPLETED=$((TOTAL_COMPLETED + 1))
          break
        fi
      done
      if [ "$result" = "failed" ]; then
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        log "FATAL: $tid failed after $MAX_RETRIES retries. Halting."
        (cd "$REPO_ROOT" && bd update "$tid" --status=open) 2>/dev/null || true
        exit 1
      fi
    fi
  done

  # ── 3c. Merge worker branches (always from REPO_ROOT) ──
  log "Merging worker branches..."

  STASHED=false
  if ! git -C "$REPO_ROOT" diff --quiet 2>/dev/null || ! git -C "$REPO_ROOT" diff --cached --quiet 2>/dev/null; then
    git -C "$REPO_ROOT" stash push -m "parallel-merge-layer-$layer_idx" 2>/dev/null || true
    STASHED=true
    log "Stashed uncommitted changes"
  fi

  git -C "$REPO_ROOT" checkout "$INTEGRATION_BRANCH" 2>/dev/null

  for wnum in $(echo "$ACTIVE_WORKERS" | tr ' ' '\n' | sort -n); do
    branch="epic-worker-$wnum"
    tid=$(cat "$STATE_DIR/w${wnum}-task")

    if [ "$(git -C "$REPO_ROOT" rev-parse "$branch")" = "$(git -C "$REPO_ROOT" rev-parse HEAD)" ]; then
      logw "$wnum" "No new commits"
      continue
    fi

    log "Merging $branch ($tid)..."
    set +e
    git -C "$REPO_ROOT" merge --no-edit --no-verify \
      -m "Merge $tid from $branch

Co-Authored-By: Claude <noreply@anthropic.com>" "$branch" 2>"$RUNS_DIR/merge-${tid}-stderr.log"
    merge_ec=$?
    set -e

    if [ $merge_ec -ne 0 ]; then
      conflicted=$(git -C "$REPO_ROOT" diff --name-only --diff-filter=U 2>/dev/null)
      if [ -n "$conflicted" ]; then
        log "Conflict: $conflicted — invoking AI resolution..."
        set +e
        claude -p "$(cat "$SCRIPT_DIR/prompts/merge-conflicts.md")

Conflicted files:
$conflicted

Source: $branch (task: $tid)
Target: $INTEGRATION_BRANCH

Working directory: $REPO_ROOT" \
          --dangerously-skip-permissions --verbose \
          > "$RUNS_DIR/merge-${tid}-resolution.log" 2>&1
        set -e

        if [ -f "$REPO_ROOT/.git/MERGE_HEAD" ]; then
          log "AI didn't commit merge. Force committing..."
          git -C "$REPO_ROOT" add -A
          git -C "$REPO_ROOT" commit --no-verify --no-edit \
            -m "Resolve conflicts: $branch ($tid)

Co-Authored-By: Claude <noreply@anthropic.com>" 2>/dev/null || true
        fi
        log "Conflict resolved: $tid"
      else
        log "ERROR: Merge failed (not conflict): $(cat "$RUNS_DIR/merge-${tid}-stderr.log" 2>/dev/null)"
        git -C "$REPO_ROOT" merge --abort 2>/dev/null || true
        exit 1
      fi
    else
      log "Clean merge: $tid"
    fi
  done

  # ── 3d. Layer context + review ──
  log "Writing layer context..."
  REVIEW_DIR="$RUNS_DIR/layer-${layer_idx}-review"
  mkdir -p "$REVIEW_DIR"
  {
    echo "# Layer $layer_idx Integration Review"
    echo "Epic: $EPIC_ID"
    echo ""
    echo "## Tasks Merged"
    for wnum in $(echo "$ACTIVE_WORKERS" | tr ' ' '\n' | sort -n); do
      tid=$(cat "$STATE_DIR/w${wnum}-task")
      echo "### $tid (worker-$wnum)"
      (cd "$REPO_ROOT" && bd show "$tid" --json 2>/dev/null | jq -r '.[0].design // "No design"' 2>/dev/null) || echo "No design"
      echo ""
    done
    echo "## Diff Summary"
    git -C "$REPO_ROOT" diff --stat "$LAYER_BASE..HEAD" 2>/dev/null
    echo ""
    echo "## Changed Files"
    git -C "$REPO_ROOT" diff --name-only "$LAYER_BASE..HEAD" 2>/dev/null
  } > "$REVIEW_DIR/layer-context.txt"

  if [ -f "$REVIEW_YAML" ]; then
    log "Running post-merge review..."
    set +e
    agentflow run "$REVIEW_YAML" \
      --work-dir="$REPO_ROOT" --results-dir="$REVIEW_DIR" --pipeline-root="$SCRIPT_DIR" \
      > "$REVIEW_DIR/agentflow-stdout.log" 2>&1
    review_ec=$?
    set -e
    if [ $review_ec -eq 0 ]; then
      log "Post-merge review PASSED"
    else
      log "WARNING: Post-merge review failed (exit $review_ec) — continuing"
    fi
  else
    log "No review-merge.yaml found, skipping review"
  fi

  # ── 3e. Restore stash ──
  if [ "$STASHED" = "true" ]; then
    git -C "$REPO_ROOT" stash pop 2>/dev/null || log "Stash pop conflict (non-fatal)"
  fi

  # ── 3f. Sync worktrees ──
  log "Syncing worktrees..."
  MERGED=$(git -C "$REPO_ROOT" rev-parse HEAD)
  for i in $(seq 1 "$NUM_WORKERS"); do
    wt="$WORKTREES_DIR/worker-$i"
    if [ -d "$wt" ]; then
      git -C "$wt" checkout "epic-worker-$i" 2>/dev/null || true
      git -C "$wt" reset --hard "$MERGED" 2>/dev/null || true
    fi
  done

  log "Layer $layer_idx complete."
done

echo ""
echo "════════════════════════════════════════════════"
echo "  Parallel Epic Agent — Complete"
echo "════════════════════════════════════════════════"
echo "  Epic:        $EPIC_ID"
echo "  Layers:      $NUM_LAYERS"
echo "  Completed:   $TOTAL_COMPLETED"
echo "  Failed:      $TOTAL_FAILED"
echo "  Branch:      $INTEGRATION_BRANCH"
echo "════════════════════════════════════════════════"
(cd "$REPO_ROOT" && bd sync)
