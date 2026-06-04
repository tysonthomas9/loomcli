#!/usr/bin/env bash
# test-epic-branch-git.sh — prove the epic-branch sync MECHANIC without Daytona
# or GitHub. Runs the exact git sequence the flue runner's commitToEpicBranch
# uses (hydrate the shared branch → commit → push with fetch→rebase→retry)
# against a LOCAL bare remote, for N sequential "tasks" plus an injected race,
# then asserts the epic branch accumulated one commit per task while base stayed
# untouched (a clean, PR-able diff). The live Daytona/GitHub e2e is
# scripts/e2e-epic-branch-pr.sh; this is its offline core.
set -euo pipefail

EPIC="loom/epic-test"
BASE="master"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BARE="$TMP/remote.git"
ID='-c user.name=loom -c user.email=loom@localhost'

ok()  { printf '  \033[1;32m✓ %s\033[0m\n' "$*"; }
die() { printf '  \033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ── seed a bare remote with a base branch + an epic branch off it ──
git init -q --bare "$BARE"
git clone -q "$BARE" "$TMP/seed"
( cd "$TMP/seed"
  git checkout -q -b "$BASE"
  printf 'hello\n' > README.md
  git $ID add -A && git $ID commit -q -m base
  git push -q origin "$BASE"
  git push -q origin "$BASE:refs/heads/$EPIC" )      # epic branch = base, as the e2e's GitHub-API step does
ok "seeded remote: $BASE + $EPIC"

# ── what commitToEpicBranch does in the sandbox, per task ──
run_task() {
  local n=$1
  local wd="$TMP/task$n"
  git clone -q --no-single-branch "$BARE" "$wd"
  ( cd "$wd"
    git checkout -q "$EPIC"                          # hydrate from the shared epic tip
    printf 'epic task %s\n' "$n" > "EPIC_${n}.md"    # the agent's work (distinct file)
    git $ID add -A
    git $ID commit -q -m "loom: task$n"
    for attempt in 1 2 3 4; do                       # push with fetch→rebase→retry
      if git push -q origin "HEAD:refs/heads/$EPIC" 2>/dev/null; then return 0; fi
      git fetch -q origin "$EPIC" && git $ID rebase -q FETCH_HEAD || { git rebase --abort 2>/dev/null||true; return 1; }
    done
    return 1 )
}

# Tasks 1 + 2 run sequentially (each push is a clean fast-forward).
run_task 1 && ok "task 1 committed onto $EPIC"
run_task 2 && ok "task 2 committed onto $EPIC"

# Task 3 with an INJECTED RACE: another writer advances the epic branch after
# task 3 has cloned + committed but before it pushes → its first push is rejected
# (non-fast-forward) → fetch + rebase + retry must recover.
wd="$TMP/task3"
git clone -q --no-single-branch "$BARE" "$wd"
( cd "$wd"; git checkout -q "$EPIC"; printf 'epic task 3\n' > EPIC_3.md; git $ID add -A; git $ID commit -q -m 'loom: task3' )
# the racer lands a commit first
( cd "$TMP/seed"; git checkout -q "$EPIC"; git pull -q origin "$EPIC"; printf 'racer\n' > RACER.md; git $ID add -A; git $ID commit -q -m 'loom: racer'; git push -q origin "$EPIC" )
( cd "$wd"
  if git push -q origin "HEAD:refs/heads/$EPIC" 2>/dev/null; then die "task 3 push should have been rejected (race not exercised)"; fi
  git fetch -q origin "$EPIC" && git $ID rebase -q FETCH_HEAD
  git push -q origin "HEAD:refs/heads/$EPIC" ) && ok "task 3 recovered from a push race via rebase-retry"

# ── assert: epic accumulated every commit; base is untouched (PR-able) ──
VERIFY="$TMP/verify"
git clone -q --no-single-branch "$BARE" "$VERIFY"
cd "$VERIFY"
ahead="$(git rev-list --count "origin/$BASE..origin/$EPIC")"
[ "$ahead" = "4" ] && ok "epic is $ahead commits ahead of $BASE (3 tasks + 1 racer)" || die "ahead_by=$ahead, want 4"
for f in EPIC_1.md EPIC_2.md EPIC_3.md RACER.md; do
  git cat-file -e "origin/$EPIC:$f" 2>/dev/null && ok "$f present on $EPIC" || die "$f missing on $EPIC"
done
git diff --quiet "origin/$BASE" "origin/$EPIC" -- README.md && ok "base content untouched on the epic branch" || die "base files changed unexpectedly"
[ "$(git rev-list --count "origin/$EPIC..origin/$BASE")" = "0" ] && ok "$BASE has no commits the epic lacks (clean PR base)" || die "$BASE diverged from the epic"

printf '\n\033[1;32mPASS\033[0m epic-branch mechanic: sequential commits accumulate on one shared branch, races rebase-recover, base stays clean.\n'
