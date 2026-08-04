#!/usr/bin/env bash
# Cheap integration check run against a DISPOSABLE detached checkout of the
# candidate commit — /app itself is never touched here (check-before-FF).
# Exit 0 = candidate may be fast-forwarded into /app.
#
# v1 scope: syntax-level sanity + optional app-owned hook. This is deliberately
# NOT the verifier (the benchmark scores /app itself); it exists to keep
# obviously-broken commits out of /app while staying cheap per task.
set -uo pipefail

CO="${1:?usage: integration-check.sh <checkout-dir>}"
cd "$CO" || exit 1

fail=0
note() { printf '[check] %s\n' "$*"; }

# 1) shell syntax
while IFS= read -r -d '' f; do
  if ! bash -n "$f" 2>&1; then
    note "bash syntax FAIL: $f"
    fail=1
  fi
done < <(find . \( -name node_modules -o -name .venv -o -name dist -o -name .git \) -prune -o -name '*.sh' -type f -print0)

# 2) node syntax
if command -v node >/dev/null 2>&1; then
  while IFS= read -r -d '' f; do
    if ! node --check "$f" 2>&1; then
      note "node syntax FAIL: $f"
      fail=1
    fi
  done < <(find . \( -name node_modules -o -name .venv -o -name dist -o -name .git \) -prune -o -name '*.js' -type f -print0)
fi

# 3) python syntax
if command -v python3 >/dev/null 2>&1; then
  while IFS= read -r -d '' f; do
    if ! python3 -m py_compile "$f" 2>&1; then
      note "python syntax FAIL: $f"
      fail=1
    fi
  done < <(find . \( -name node_modules -o -name .venv -o -name dist -o -name .git \) -prune -o -name '*.py' -type f -print0)
fi

# 4) app-owned hook: if the tree ships its own quick check, honor it (bounded).
if [ -x ./marathon-check.sh ]; then
  note "running app-owned marathon-check.sh"
  if ! timeout 120 ./marathon-check.sh; then
    note "marathon-check.sh FAIL"
    fail=1
  fi
fi

if [ "$fail" = "0" ]; then
  note "PASS"
fi
exit "$fail"
