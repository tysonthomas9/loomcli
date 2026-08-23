#!/usr/bin/env bash
# Export the durable evidence of one harbor job into harbor/runs/<job>/ so it can be committed.
# harbor/test/jobs/ is gitignored (hundreds of MB per run of daemon noise, git mirrors, redis
# snapshots); this keeps the ~5-30 MB that later analysis actually needs.
#   harbor/scripts/export-run.sh <job-name> [--no-raw]     (--no-raw: digests only, skip raw JSONL)
set -euo pipefail
HARBOR_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
job="${1:?job name}"; raw=1; [ "${2:-}" = "--no-raw" ] && raw=0
J="$HARBOR_DIR/test/jobs/$job"; [ -d "$J" ] || { echo "no such job: $J" >&2; exit 1; }
T="$(ls -d "$J"/*__*/ | head -1)"; A="$T/agent"; R="$HARBOR_DIR/runs/$job"
mkdir -p "$R"/{transcripts,digests,critic,verifier,judge,analysis}
cp "$J.launcher.log" "$R/launcher.log" 2>/dev/null || true
for f in orchestrate.log integration.log lead-passes.log usage-summary.json team-agents.tsv app-git-log.txt \
         final-issues.json final-issues-post-quiesce.json template-apply.json app-snapshot.tar.gz; do
  [ -e "$A/$f" ] && cp "$A/$f" "$R/"; done
grep -v 'source=fleet-db' "$A/daemon.out" | grep -v heartbeat > "$R/daemon-filtered.log" || true
cp "$A"/critic-*.log "$A"/check-*.log "$R/critic/" 2>/dev/null || true
if [ -d "$T/verifier" ]; then rsync -a --exclude artifacts --exclude '*.png' "$T/verifier/" "$R/verifier/"; fi
[ -e "$T/result.json" ] && cp "$T/result.json" "$R/verifier/result.json"
for f in "$A"/loom-state-final/daemon-logs/MARATHON/*.log; do
  [ -e "$f" ] || continue; b="$(basename "$f")"; name="${b#*-*-}"
  [ "$raw" = 1 ] && cp "$f" "$R/transcripts/$name"
  python3 "$HARBOR_DIR/scripts/digest-transcript.py" "$f" > "$R/digests/${name%.log}.md"
done
[ -d "$J/judge/out" ] && for d in "$J"/judge/out/*/; do cp "$d"/{driver-report.txt,verdicts.json,ux.json} "$R/judge/" 2>/dev/null || true; done
[ -d "$J/analysis" ] && cp "$J"/analysis/README.md "$J"/analysis/digests/task-ledger.md "$R/analysis/" 2>/dev/null && cp "$J"/analysis/reports/*.md "$R/analysis/" 2>/dev/null || true
du -sh "$R" | awk '{print "exported", $2, "(" $1 ")"}'
