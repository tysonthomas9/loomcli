#!/usr/bin/env bash
# FREE full-mechanics ensemble dry-run under real Harbor + the real slack-clone
# task image: fake codex drives lead-seed -> planner design -> lead approve ->
# coder impl -> critic verdict -> atomic integration gate -> harness close,
# through real loom + fleet-db, including the MANDATORY failing-integration
# proof (T2 attempt 1 must fail the gate, leave /app untouched, and reopen).
#
# Usage: harbor/test/run-stub-trial.sh [jobs-dir]
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
HARBOR_DIR="$(cd "$HERE/.." && pwd)"          # loomcli/harbor
SWE="${SWE_MARATHON_DIR:-$HARBOR_DIR/../../swe-marathon}"
JOBS="${1:-$HARBOR_DIR/test/jobs}"
JOB_NAME="stub-ensemble-$(date +%H%M%S)"

[ -d "$SWE/tasks/slack-clone" ] || { echo "FATAL: swe-marathon checkout not found at $SWE (set SWE_MARATHON_DIR)" >&2; exit 1; }
arch="$(uname -m)"
case "$arch" in
  arm64|aarch64) tarball="$HARBOR_DIR/bundle/dist/loom-bundle-linux-arm64.tar.gz" ;;
  x86_64|amd64)  tarball="$HARBOR_DIR/bundle/dist/loom-bundle-linux-amd64.tar.gz" ;;
  *) echo "FATAL: unsupported host arch $arch" >&2; exit 1 ;;
esac
[ -f "$tarball" ] || { echo "FATAL: $tarball missing — run harbor/bundle/build-bundle.sh" >&2; exit 1; }

# Verification is DISABLED for the dry-run: test.sh reaches the CUA stage
# whenever /app serves health (gate results only affect reward, not the flow),
# so running the verifier would either spend real CUA money (real key) or
# hard-fail the trial (dummy key). The NOP gate already proves verifier
# plumbing; this trial proves ensemble mechanics. ALWAYS force the dummy key so
# an exported real key can never leak into a "free" run.
export ANTHROPIC_API_KEY="dummy-key-stub-dry-run-only"
# Podman's docker-compose delegation banner otherwise pollutes every
# environment.exec stdout (Harbor merges stderr into stdout).
export PODMAN_COMPOSE_WARNING_LOGS=false

echo "== launching stub ensemble trial (job=$JOB_NAME) =="
( cd "$SWE" && PYTHONPATH="$HARBOR_DIR" harbor run \
    -p tasks/slack-clone \
    -a loom_harbor:LoomAgent \
    -e docker \
    --ak stub=true \
    --ak budget_secs=1800 \
    --ak reserve_secs=240 \
    --ak cadence_secs=20 \
    --ak spend_cap_usd=1 \
    ${STUB_EXTRA_AKS:-} \
    -o "$JOBS" --job-name "$JOB_NAME" -n 1 -q -y \
    --disable-verification )

TRIAL_DIR="$(find "$JOBS/$JOB_NAME" -maxdepth 1 -type d -name 'slack-clone__*' | head -1)"
[ -n "$TRIAL_DIR" ] || { echo "FATAL: no trial dir under $JOBS/$JOB_NAME" >&2; exit 1; }
echo "== trial dir: $TRIAL_DIR =="

AGENT_LOG_DIR="$TRIAL_DIR/agent"
INTEG="$AGENT_LOG_DIR/integration.log"

fail=0
check() {
  local desc="$1"; shift
  if "$@"; then echo "PASS: $desc"; else echo "FAIL: $desc"; fail=1; fi
}

echo
echo "== dry-run invariant checks =="
check "integration.log exists" test -f "$INTEG"
check "T1 integrated" grep -q ' INTEGRATED task=.* attempt=1 ' "$INTEG"
check "failing-integration proof: an INTEGRATION-FAILED record exists" \
  grep -q ' INTEGRATION-FAILED ' "$INTEG"
check "no invariant violations (/app never moved on a failed check)" \
  bash -c "! grep -q INVARIANT-VIOLATION '$INTEG'"
check "revision loop closed: attempt=2 integrated after the failure" \
  grep -q ' INTEGRATED task=.* attempt=2 ' "$INTEG"
check "harness closed all three tasks" python3 - "$AGENT_LOG_DIR/final-issues.json" <<'PY'
import json, sys
issues = json.load(open(sys.argv[1]))
tasks = [i for i in issues if i.get("issue_type") != "epic"]
closed = [i for i in tasks if i.get("status") == "closed"]
sys.exit(0 if len(tasks) >= 3 and len(closed) == len(tasks) else 1)
PY
# 5 commits: baseline + T1 + T2-attempt1 + T2-attempt2 + T3. The rejected
# T2-attempt1 commit rides along as an ANCESTOR when the gate fast-forwards to
# attempt 2 (single coder branch) — it was never /app HEAD on its own, which is
# what check-before-FF guarantees.
check "/app advanced exactly with integrations (5 commits incl. baseline)" \
  python3 - "$AGENT_LOG_DIR/app-git-log.txt" <<'PY'
import sys
lines = [l for l in open(sys.argv[1]).read().splitlines() if l.strip()]
sys.exit(0 if len(lines) == 5 else 1)
PY
check "usage summary written with nonzero stub spend" python3 - "$AGENT_LOG_DIR/usage-summary.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
sys.exit(0 if d.get("input_tokens", 0) > 0 and d.get("stub") else 1)
PY

echo
if [ "$fail" = 0 ]; then
  echo "== STUB DRY-RUN: ALL INVARIANTS PROVEN =="
else
  echo "== STUB DRY-RUN: INVARIANT FAILURES (see above; logs in $AGENT_LOG_DIR) =="
fi
exit "$fail"
