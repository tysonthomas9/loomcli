#!/usr/bin/env bash
# Upload SWE-Marathon trial evidence to the private GitHub repo
# (tysonthomas9/loom-marathon-trials). Run after each trial completes.
#
# Safety design (codex vet-A hardened):
#   - trials/.gitignore ALLOWLISTS codex-home content (sessions only)
#   - the secret gate scans exactly the STAGED file set (so ignored junk can't
#     false-positive and nothing unstaged can sneak through), gz-aware
#   - broadened credential patterns; ANY hit refuses the upload (fail-closed)
#   - files >50MB gzipped before staging (GitHub hard-rejects 100MB)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TRIALS="$(cd "$HERE/../trials" && pwd)"
REMOTE="https://github.com/tysonthomas9/loom-marathon-trials.git"
LABEL="${1:-sync}"
SCOPE="${2:-}"   # optional: restrict the commit to one job dir (skips in-flight trials)
# Value-shaped credential patterns ONLY. Key-name patterns ("access_token":)
# were removed: the benchmark apps implement auth, so their source legitimately
# contains those keys — a key name is not a secret, a value shape is.
PATTERNS='sk-ant-|sk-proj-|api03-|eyJ[A-Za-z0-9_-]{40,}|Bearer [A-Za-z0-9._~+/-]{30,}'

cd "$TRIALS"

echo "== compressing oversized files =="
find . -type f -size +50M -not -path './.git/*' -not -name '*.gz' -exec gzip -9 -v {} \;

if [ ! -d .git ]; then
  git init -q -b main
  git remote add origin "$REMOTE"
fi
if [ -n "$SCOPE" ]; then git add -A "$SCOPE" .gitignore README.md 2>/dev/null || git add -A "$SCOPE"; else git add -A; fi

echo "== secret gate (staged set, gz-aware) =="
fail=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  case "$f" in
    *.gz) hit=$(zgrep -lE "$PATTERNS" "$f" 2>/dev/null || true) ;;
    *)    hit=$(grep -lIE "$PATTERNS" "$f" 2>/dev/null || true) ;;
  esac
  if [ -n "$hit" ]; then
    echo "FATAL: credential-shaped content in staged file: $f" >&2
    fail=1
  fi
  if [ "$(basename "$f")" = "auth.json" ] && [ ! -L "$f" ]; then
    echo "FATAL: real auth.json staged: $f" >&2
    fail=1
  fi
done < <(git diff --cached --name-only --diff-filter=ACM)
if [ "$fail" != 0 ]; then
  git reset -q
  echo "upload REFUSED — nothing pushed" >&2
  exit 1
fi
echo "clean"

if git diff --cached --quiet; then
  echo "nothing new to upload"
  exit 0
fi
git commit -q -m "trial evidence: $LABEL ($(date -u +%FT%TZ))

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01S53S26U1h2M6bn18RAdd1J"
git push -q -u origin main
echo "uploaded: $(git rev-parse --short HEAD) -> $REMOTE"
