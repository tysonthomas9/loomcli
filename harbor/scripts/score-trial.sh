#!/usr/bin/env bash
# B2h L1: attach a quality scorecard to a finished trial.
#
#   harbor/scripts/score-trial.sh <job-dir> [--with-sonar] [--with-mutation]
#
# Runs AFTER the trial and its official verification are complete, off the
# preserved /app artifact — never inside the live trial path, never before
# official scoring (codex B2h-vet finding 9). Writes scorecard.json beside the
# trial's metrics.json, stamped with the scorer commit hash so numbers stay
# attributable to the code that produced them.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
JOB="${1:?usage: score-trial.sh <job-dir> [--with-sonar] [--with-mutation]}"; shift || true
[ -d "$JOB" ] || { echo "FATAL: no such job dir: $JOB" >&2; exit 1; }

TRIAL=$(find "$JOB" -maxdepth 1 -type d -name '*__*' | head -1)
[ -n "$TRIAL" ] || { echo "FATAL: no trial dir under $JOB" >&2; exit 1; }
APP="$TRIAL/artifacts/app"
[ -d "$APP" ] || { echo "FATAL: no preserved artifact at $APP (run with --artifact /app)" >&2; exit 1; }
[ -f "$TRIAL/verifier/metrics.json" ] || echo "WARN: no verifier metrics.json — scoring an unverified trial"

ID=$(basename "$JOB")
SCORER=$(git -C "$HERE/.." rev-parse --short HEAD 2>/dev/null || echo unknown)
export MAINT_ARTIFACTS="$ID=$APP"
# language inference decides which coupling resolver runs for this artifact
PY_N=$(find "$APP" -name '*.py' -not -path '*/.git/*' | wc -l | tr -d ' ')
JS_N=$(find "$APP" \( -name '*.js' -o -name '*.mjs' \) -not -path '*/.git/*' -not -path '*/node_modules/*' | wc -l | tr -d ' ')
if [ "$PY_N" -ge "$JS_N" ]; then export MAINT_PRIMARY="$ID=py"; export MAINT_JS_IDS=""
else export MAINT_PRIMARY="$ID=js"; export MAINT_JS_IDS="$ID"; fi
echo "[score-trial] $ID  py=$PY_N js=$JS_N  primary=${MAINT_PRIMARY#*=}  scorer=$SCORER"

bash "$HERE/maint-all.sh" "$@"

python3 - "$HOME/.mx-stage/scorecard.json" "$TRIAL/scorecard.json" "$ID" "$SCORER" "$APP" <<'PY'
import json, sys, datetime
src, dst, aid, scorer, app = sys.argv[1:6]
card = json.load(open(src))
out = {'trial': aid, 'artifact': app, 'scorer_commit': scorer,
       'scored_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
       'scorecard': card.get(aid, card)}
json.dump(out, open(dst, 'w'), indent=1)
print(f'scorecard -> {dst}')
PY
