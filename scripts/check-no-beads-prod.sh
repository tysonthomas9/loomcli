#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
allowlist="$repo_root/scripts/no-beads-prod-allowlist.txt"

if [[ ! -f "$allowlist" ]]; then
  echo "missing allowlist: $allowlist" >&2
  exit 1
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

cd "$repo_root"

rg -n \
  -e 'github\.com/tysonthomas9/loomcli/internal/backend/beads' \
  -e 'third_party/beads' \
  -e 'exec\.Command(Context)?\([^\\n]*"bd"' \
  -e 'CommandContext\([^\\n]*"bd"' \
  -e 'issue_backend[^\\n]*beads' \
  -e 'fallback[^\\n]*beads' \
  -e 'beads[^\\n]*fallback' \
  -e 'NewBeads|beads\.' \
  cmd internal \
  --glob '*.go' \
  --glob '!**/*_test.go' \
  --glob '!internal/backend/beads/**' \
  --glob '!internal/backend/paritytest/**' \
  >"$tmp" || true

bad=0
while IFS=: read -r file line text; do
  [[ -n "${file:-}" ]] || continue
  if ! grep -Fxq "$file" "$allowlist"; then
    if [[ $bad -eq 0 ]]; then
      echo "production beads/bd references found outside scripts/no-beads-prod-allowlist.txt:" >&2
    fi
    printf '%s:%s:%s\n' "$file" "$line" "$text" >&2
    bad=1
  fi
done <"$tmp"

if [[ $bad -ne 0 ]]; then
  echo "" >&2
  echo "FleetDB is canonical; add new production beads/bd references only with an explicit migration-ticket allowlist update." >&2
  exit 1
fi

echo "No new production beads/bd references outside allowlist."
