#!/usr/bin/env bash
# Prepare a trial dir for the secret-gated upload: strip tool-internal junk
# from archives, slim oversized snapshots (worktrees live in app-mirror.git),
# separate session files, and redact app-issued tokens. Idempotent.
# Usage: prep-trial-for-upload.sh <trial-job-dir under harbor/trials>
set -euo pipefail
D="${1:?usage: prep-trial-for-upload.sh <job-dir>}"
cd "$(dirname "$0")/../trials"
[ -d "$D" ] || { echo "no such job dir: $D" >&2; exit 1; }

# 1. inside every tarball: drop codex-internal state + plugin trees, redact tokens
for tgz in $(find "$D" -name '*.tar.gz'); do
  W=$(mktemp -d); tar -xzf "$tgz" -C "$W"
  find "$W" -type d -path '*/.codex/.tmp' -prune -exec rm -rf {} + 2>/dev/null || true
  find "$W" -path '*/.codex/*' \( -name '*.sqlite*' -o -name '*.db' \) -delete 2>/dev/null || true
  python3 - "$W" <<'PY'
import pathlib, re, sys
pat = re.compile(r"(Bearer )[A-Za-z0-9._~+/-]{30,}|eyJ[A-Za-z0-9_-]{40,}(\.[A-Za-z0-9_-]+)*")
n = 0
for f in pathlib.Path(sys.argv[1]).rglob("*"):
    if not f.is_file(): continue
    try: s = f.read_text(errors="strict")
    except (UnicodeDecodeError, OSError): continue
    s2, c = pat.subn(lambda m: (m.group(1) + "[REDACTED-APP-TOKEN]") if m.group(1) else "eyJ[REDACTED-APP-JWT]", s)
    if c: f.write_text(s2); n += c
print(f"  {n} in-tar redactions")
PY
  tar -C "$W" -czf "$tgz" $(ls "$W") && rm -rf "$W"
done

# 2. slim any agent app-snapshot >50MB: extract sessions, drop .worktrees
for snap in $(find "$D" -path '*/agent/app-snapshot.tar.gz' -size +50M); do
  A=$(cd "$(dirname "$snap")" && pwd)   # ABSOLUTE: tar runs after a cd
  W=$(mktemp -d); tar -xzf "$snap" -C "$W"
  mkdir -p "$A/codex-sessions"
  find "$W" -path '*/sessions/*.jsonl' -exec cp {} "$A/codex-sessions/" \; 2>/dev/null || true
  rm -rf "$W"/*/.worktrees
  ( cd "$W"/* && tar -czf "$A/app-snapshot-slim.tar.gz" . )
  [ -s "$A/app-snapshot-slim.tar.gz" ] || { echo "slim tar failed for $snap" >&2; exit 1; }
  rm -f "$snap"; rm -rf "$W"
  for f in "$A"/codex-sessions/*.jsonl; do
    [ -f "$f" ] && [ "$(stat -f%z "$f" 2>/dev/null || stat -c%s "$f")" -gt 52428800 ] && gzip -9 "$f"
  done
  echo "  slimmed $snap"
done

# 3. flat-file redaction sweep
python3 - "$D" <<'PY'
import pathlib, re, sys
pat = re.compile(r"(Bearer )[A-Za-z0-9._~+/-]{30,}|eyJ[A-Za-z0-9_-]{40,}(\.[A-Za-z0-9_-]+)*")
n = 0
for ext in ("*.json", "*.jsonl", "*.txt", "*.html", "*.log"):
    for f in pathlib.Path(sys.argv[1]).rglob(ext):
        try: s = f.read_text(errors="strict")
        except (UnicodeDecodeError, OSError): continue
        s2, c = pat.subn(lambda m: (m.group(1) + "[REDACTED-APP-TOKEN]") if m.group(1) else "eyJ[REDACTED-APP-JWT]", s)
        if c: f.write_text(s2); n += c
print(f"flat redactions: {n}")
PY
echo "prep complete: $D"
