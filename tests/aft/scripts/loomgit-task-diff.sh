#!/usr/bin/env bash
# P2.18 task diff: real TaskRuns record revisions, product verdicts apply them,
# and the task diff route is checked against the PR diff Git itself computes.
set -euo pipefail

phase="$1"
workspace="$2"
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo="$AFT_WORK_DIR/$(printf '%s' "$workspace" | tr '[:upper:]' '[:lower:]')-repo"
lead_ref="refs/heads/loom/ws/$workspace/interactive/lead"

json() { python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }

case "$phase" in
setup)
  git init -q -b main "$repo"
  printf 'base\n' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$(printf '%s' "$workspace" | tr '[:upper:]' '[:lower:]')\",\"type\":\"empty\",\"repos\":[\"$repo\"]}" >/dev/null
  if [[ "${3:-stack}" == trunk ]]; then
    LOOM_WORKSPACE="$workspace" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" git-settings --delivery pr-per-task --workspace "$workspace" >/dev/null
  fi
  git -C "$repo" rev-parse main > "$AFT_WORK_DIR/$workspace-trunk"
  ;;
teardown)
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh"
  curl -s -X DELETE "$api" >/dev/null || true
  ;;
run)
  # run <ws> <name> <file>: one epic with one task whose deterministic agent
  # writes <file>; waits until its TaskRun records a revision.
  name="$3" file="$4"
  epic="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"$name epic\",\"issue_type\":\"epic\",\"priority\":2}" | json '["data"]["id"]')"
  task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"$name task\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"STUB_CODEX_PATCH=$file\"}" | json '["data"]["id"]')"
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" >/dev/null
  printf '%s\n' "$task" > "$AFT_WORK_DIR/$workspace-$name-task"
  for _ in $(seq 1 60); do
    curl -fsS "$api/issues/$task/revisions" | grep -q '"head_sha"' && exit 0
    sleep 2
  done
  echo "TaskRun for $name recorded no revision" >&2
  exit 1
  ;;
approve)
  # approve <ws> <name>: approve the task's newest revision; it is applied as
  # the lead's new top layer, whose tip is recorded.
  name="$3"
  task="$(cat "$AFT_WORK_DIR/$workspace-$name-task")"
  read -r change number sha < <(curl -fsS "$api/issues/$task/revisions" |
    python3 -c 'import json,sys; r=json.load(sys.stdin)["data"][0]; print(r["change_id"], r["number"], r["head_sha"])')
  curl -fsS -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" \
    > "$AFT_WORK_DIR/$workspace-$name-verdict.json"
  grep -q '"status":"applied"' "$AFT_WORK_DIR/$workspace-$name-verdict.json"
  git -C "$repo" rev-parse "$lead_ref" > "$AFT_WORK_DIR/$workspace-$name-tip"
  ;;
diff)
  # diff <ws> <name> <compare> <file> [<from-tip> <to-tip>]: the task diff
  # compares as expected, names only <file>, and (when given) equals Git's
  # diff between the two recorded tips, i.e. what the task's PR contains.
  name="$3" compare="$4" file="$5"
  task="$(cat "$AFT_WORK_DIR/$workspace-$name-task")"
  out="$AFT_WORK_DIR/$workspace-$name-diff.json"
  curl -fsS "$api/issues/$task/diff" > "$out"
  expected=""
  if [[ $# -ge 7 ]]; then
    from="$(cat "$AFT_WORK_DIR/$workspace-$6")" to="$(cat "$AFT_WORK_DIR/$workspace-$7")"
    expected="$AFT_WORK_DIR/$workspace-$name-pr.diff"
    git -C "$repo" diff --no-ext-diff --no-textconv --no-renames --no-color --patch "$from" "$to" > "$expected"
  fi
  python3 - "$out" "$compare" "$file" "$expected" <<'PY'
import json, sys
diffs = json.load(open(sys.argv[1]))["data"]
# One entry per repo the task changed; these tasks change one repo.
assert len(diffs) == 1, diffs
data = diffs[0]
assert data["compare"] == sys.argv[2], (data["compare"], sys.argv[2])
paths = [f["path"] for f in data["files"]]
assert paths == [sys.argv[3]], paths
if sys.argv[4]:
    patch = "".join(f["patch"] for f in data["files"])
    assert patch == open(sys.argv[4]).read(), (patch, open(sys.argv[4]).read())
PY
  ;;
revisions)
  # revisions <ws> <name>: across several background reconcile passes, the
  # applied task's newest revision stays the applied, reviewed one; no pass
  # re-derives an empty "awaiting review" revision at the same head (P4.1c).
  name="$3"
  task="$(cat "$AFT_WORK_DIR/$workspace-$name-task")"
  out="$AFT_WORK_DIR/$workspace-$name-revisions.json"
  for _ in $(seq 1 5); do
    curl -fsS "$api/issues/$task/revisions" > "$out"
    python3 - "$out" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))["data"]
heads = [r["head_sha"] for r in d]
assert len(heads) == len(set(heads)), ("a revision repeats an earlier head", d)
assert d[0]["applied"] and d[0].get("verdict"), ("newest revision is not the applied, reviewed one", d)
PY
    sleep 2
  done
  ;;
*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac
