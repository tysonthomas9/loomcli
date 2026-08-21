#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OVERRIDES="$ROOT/harbor/prompts-generic"
STOCK="$ROOT/internal/cli/agent/prompts"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

check_marker() {
  local file="$1" expected="$2" first
  IFS= read -r first < "$file"
  [ "$first" = "<!-- ROLE-MARKER: $expected -->" ] \
    || fail "$(basename "$file") first line is not ROLE-MARKER: $expected"
  printf 'PASS: %s marker\n' "$(basename "$file")"
}

check_fields() {
  local override="$1" stock="$2" token field
  while IFS= read -r token; do
    [ -n "$token" ] || continue
    field="${token#.}"
    grep -Eq "\{\{[^}]*\.${field}([^A-Za-z0-9]|$)" "$stock" \
      || fail "$(basename "$override") uses unknown template field .$field"
  done < <(grep -E '\{\{' "$override" | grep -Eo '\.[A-Z][A-Za-z0-9]*' | sort -u || true)
  printf 'PASS: %s fields are supported by %s\n' "$(basename "$override")" "$(basename "$stock")"
}

frontend="$OVERRIDES/team-frontend-dev-override.md"
backend="$OVERRIDES/team-backend-dev-override.md"
qa="$OVERRIDES/team-qa-override.md"
architect="$OVERRIDES/team-architect-override.md"

check_marker "$frontend" team-dev
check_marker "$backend" team-dev
check_marker "$qa" team-qa
check_marker "$architect" team-architect

for dev in "$frontend" "$backend"; do
  grep -q 'IMPL-DONE attempt=' "$dev" \
    || fail "$(basename "$dev") lacks the IMPL-DONE contract"
  if grep -q 'loom data close' "$dev"; then
    fail "$(basename "$dev") tells an implementation worker to close a task"
  fi
  printf 'PASS: %s uses harness-owned completion\n' "$(basename "$dev")"
done

grep -q 'IMPL-DONE attempt=' "$qa" || fail "$(basename "$qa") lacks the IMPL-DONE contract"
qa_close_lines=$(grep -n 'loom data close' "$qa" || true)
[ -n "$qa_close_lines" ] || fail "$(basename "$qa") lacks the commit-free verification close path"
while IFS=: read -r line _; do
  [ -n "$line" ] || continue
  start=$((line > 12 ? line - 12 : 1))
  sed -n "${start},${line}p" "$qa" | grep -Eq 'No commits|QA RESULTS' \
    || fail "$(basename "$qa") close command is not under the no-commits QA RESULTS branch"
done <<< "$qa_close_lines"
printf 'PASS: %s separates commit and no-commit completion\n' "$(basename "$qa")"

check_fields "$frontend" "$STOCK/team-frontend-dev.md"
check_fields "$backend" "$STOCK/team-backend-dev.md"
check_fields "$qa" "$STOCK/team-qa.md"
check_fields "$architect" "$STOCK/team-architect.md"

bash -n "$ROOT/harbor/stub/codex"
bash -n "$ROOT/harbor/test/run-stub-trial.sh"
printf 'PASS: stub and dry-run scripts are bash -n clean\n'
printf 'TEAM PROMPT CHECKS PASSED\n'
