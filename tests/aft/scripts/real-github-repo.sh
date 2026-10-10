#!/usr/bin/env bash
# PX.7 real-GitHub tier: the run's public, fixture-only sandbox repository
# (README + check workflow, never a token). Public because branch protection
# on a private repo needs GitHub Pro (Tyson, 2026-10-10).
#
#   real-github-repo.sh create            creates tysonthomas9/loom-aft-git-<yyyymmdd-hhmm>,
#                                         seeds main with a README and the required
#                                         Actions check "check" (red when a file
#                                         contains FAIL), protects main with that
#                                         check, and prints the repo slug
#   real-github-repo.sh close-prs <repo>  closes the repo's open loom/* PRs
#   real-github-repo.sh ledger <repo> <event> [detail]
#                                         appends one line to reports/live-ledger.log
#
# Only repos named tysonthomas9/loom-aft-git-<yyyymmdd-hhmm> are ever touched, and
# create refuses a name that already exists. The operator's gh login does the
# work; the token is passed to git only through an environment-backed credential
# helper and is never printed. The repo is kept for inspection: the token has no
# delete_repo scope, so the operator deletes old sandboxes.
set -euo pipefail

owner=tysonthomas9
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
report_dir="${AFT_REPORT_DIR:-$script_dir/../reports}"

valid() { [[ "$1" =~ ^tysonthomas9/loom-aft-git-[0-9]{8}-[0-9]{4}$ ]] || { echo "refusing repo '$1': not a harness sandbox name" >&2; exit 2; }; }
gh_api() { env -u GITHUB_TOKEN -u GH_TOKEN gh api "$@"; }

case "${1:-}" in
create)
  gh auth status > /dev/null 2>&1 || { echo "gh is not logged in; run gh auth login" >&2; exit 1; }
  [[ "$(gh_api user --jq .login)" == "$owner" ]] || { echo "gh is not logged in as $owner" >&2; exit 1; }
  repo="$owner/loom-aft-git-$(date +%Y%m%d-%H%M)"
  valid "$repo"
  if gh_api "repos/$repo" > /dev/null 2>&1; then
    echo "refusing: $repo already exists (one new sandbox per run; retry in a minute)" >&2
    exit 1
  fi
  env -u GITHUB_TOKEN -u GH_TOKEN gh repo create "$repo" --public \
    --description "Loom AFT real-GitHub sandbox (PX.7). Created by the test harness; safe to delete." > /dev/null
  seed="$(mktemp -d /tmp/loom-aft-git-seed.XXXXXX)"
  git init -q -b main "$seed"
  printf '# Loom AFT sandbox\n\nCreated by tests/aft/scripts/real-github-repo.sh for one real-GitHub AFT run.\n' > "$seed/README.md"
  mkdir -p "$seed/.github/workflows"
  cat > "$seed/.github/workflows/check.yml" <<'YAML'
name: check
on:
  push:
  pull_request:
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: No file may contain the failure marker
        run: |
          if grep -rIl --exclude-dir=.git --exclude-dir=.github 'FAIL' .; then
            echo "a file contains the failure marker"
            exit 1
          fi
YAML
  git -C "$seed" add README.md .github/workflows/check.yml
  env -u GIT_AUTHOR_NAME -u GIT_AUTHOR_EMAIL -u GIT_COMMITTER_NAME -u GIT_COMMITTER_EMAIL \
    git -C "$seed" -c user.name="Tyson Thomas" -c user.email=11642062+tysonthomas9@users.noreply.github.com \
    commit -q -m "Seed the Loom AFT sandbox"
  # shellcheck disable=SC2016 # the helper expands the variable when git runs it
  AFT_GH_PUSH_TOKEN="$(gh auth token)" git -C "$seed" -c credential.helper= \
    -c 'credential.helper=!f() { echo username=x-access-token; echo "password=$AFT_GH_PUSH_TOKEN"; }; f' \
    push -q "https://github.com/$repo.git" main
  # Require the check on main, for admins too, so a red PR cannot be merged by
  # the same account Loom acts as.
  python3 -c 'import json; print(json.dumps({"required_status_checks":{"strict":False,"contexts":["check"]},"enforce_admins":True,"required_pull_request_reviews":None,"restrictions":None}))' |
    gh_api -X PUT "repos/$repo/branches/main/protection" --input - > /dev/null
  printf '%s\n' "$repo"
  ;;

close-prs)
  repo="${2:-}"
  valid "$repo"
  for number in $(gh_api "repos/$repo/pulls?state=open&per_page=100" --jq '.[] | select(.head.ref | startswith("loom/")) | .number'); do
    gh_api -X PATCH "repos/$repo/pulls/$number" -f state=closed > /dev/null && echo "[real-github] closed PR #$number"
  done
  ;;

ledger)
  repo="${2:-}" event="${3:-}" detail="${4:-}"
  valid "$repo"
  mkdir -p "$report_dir"
  printf '%s real-github repo=https://github.com/%s event=%s run=%s %s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$repo" "$event" "${RUN_ID:-}" "$detail" >> "$report_dir/live-ledger.log"
  ;;

*)
  echo "usage: real-github-repo.sh create | close-prs <repo> | ledger <repo> <event> [detail]" >&2
  exit 2
  ;;
esac
