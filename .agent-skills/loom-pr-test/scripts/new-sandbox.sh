#!/usr/bin/env bash
# new-sandbox.sh — build a loom PR binary + stand up an ISOLATED FleetDB-backed sandbox.
#
#   new-sandbox.sh <name> <pr-worktree-dir> [--no-task]
#   new-sandbox.sh --clean <name> [--yes]
#
# Creates /tmp/loom-e2e/<name>/{loom, hello/, config/, env.sh} and an UPPERCASE workspace
# with planning and implementation-ready tasks. NEVER touches the user's real ~/.loom
# (LOOM_CONFIG_DIR is always set).
#
# Env overrides:
#   FLEET_DB_REPO   path to the fleet-db source repo (default: ../fleet-db)
#   FLEET_DB_BIN    prebuilt fleet-db binary (skips building it)
#   ROOT            sandbox root (default: /tmp/loom-e2e)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOMCLI_REPO="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ROOT="${ROOT:-/tmp/loom-e2e}"
FLEET_DB_REPO="${FLEET_DB_REPO:-$LOOMCLI_REPO/../fleet-db}"

die() { echo "error: $*" >&2; exit 1; }

# ---- clean ----
if [[ "${1:-}" == "--clean" ]]; then
  name="${2:?usage: new-sandbox.sh --clean <name> [--yes]}"
  yes=0
  [[ "${3:-}" == "--yes" || "${3:-}" == "-y" || "${YES:-}" == "1" ]] && yes=1
  dir="$ROOT/$name"
  [[ -d "$dir" ]] || die "no sandbox at $dir"
  if [[ "$yes" != "1" ]]; then
    echo "About to delete: $dir"
    read -r -p "Proceed? [y/N] " ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "aborted"; exit 0; }
  fi
  rm -rf "$dir"
  echo "removed $dir"
  exit 0
fi

name="${1:?usage: new-sandbox.sh <name> <pr-worktree-dir> [--no-task]}"
worktree="${2:?usage: new-sandbox.sh <name> <pr-worktree-dir> [--no-task]}"
make_task=1; [[ "${3:-}" == "--no-task" ]] && make_task=0
[[ -d "$worktree/cmd/loom" ]] || die "worktree $worktree has no cmd/loom (not a loomcli checkout?)"

dir="$ROOT/$name"
WS_LOWER="hellows"; WS="HELLOWS"     # loom uppercases workspace names; reference WS everywhere
mkdir -p "$dir/config"

# ---- 1. fleet-db (the embedded server) ----
FLEET_DB_BIN="${FLEET_DB_BIN:-$ROOT/fleet-db}"
if [[ ! -x "$FLEET_DB_BIN" ]]; then
  [[ -f "$FLEET_DB_REPO/cmd/fleet-db/main.go" ]] || die "fleet-db repo not found at $FLEET_DB_REPO (set FLEET_DB_REPO)"
  mkdir -p "$(dirname "$FLEET_DB_BIN")"
  echo "==> building fleet-db from $FLEET_DB_REPO"
  ( cd "$FLEET_DB_REPO" && go build -o "$FLEET_DB_BIN" ./cmd/fleet-db )
fi
export FLEET_DB_BIN
echo "==> fleet-db: $FLEET_DB_BIN"

# ---- 2. build the PR's loom ----
LOOM="$dir/loom"
echo "==> building loom from $worktree"
( cd "$worktree" && go build -o "$LOOM" ./cmd/loom )

# ---- 3. target repo (with go.mod so `loom task` can build/test) ----
repo="$dir/hello"
if [[ ! -d "$repo/.git" ]]; then
  echo "==> creating hello-world repo (with go.mod) at $repo"
  mkdir -p "$repo"
  ( cd "$repo"
    git init -q -b main
    git config user.email "e2e@example.com"; git config user.name "loom-e2e"
    printf 'module hello\n\ngo 1.21\n' > go.mod
    printf 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("hello") }\n' > main.go
    git add -A && git commit -qm "init hello-world" )
fi

# ---- 4. isolated config + workspace ----
export LOOM_CONFIG_DIR="$dir/config"
[[ -n "$LOOM_CONFIG_DIR" ]] || die "refusing to run without LOOM_CONFIG_DIR"
if ! "$LOOM" workspace list 2>/dev/null | grep -qi "$WS"; then
  echo "==> creating workspace $WS"
  "$LOOM" workspace create "$WS_LOWER" --repos "$repo" >/dev/null 2>&1 || \
    "$LOOM" workspace create "$WS_LOWER" --repos "$repo" 2>&1 | grep -iv '"service":"fleet-db"' | tail -3
fi
export LOOM_WORKSPACE="$WS"

# ---- 5. a starter task ----
if [[ "$make_task" == "1" ]]; then
  echo "==> creating starter tasks"
  "$LOOM" data create --workspace "$WS" --type task --priority 1 \
    --title "Plan a Greeting(name) helper" \
    --description "Plan a minimal func Greeting(name string) string returning 'Hello, <name>!' and how main should call it." \
    2>/dev/null | grep -i created || true
  "$LOOM" data create --workspace "$WS" --type task --priority 1 \
    --title "Implement a Greeting(name) helper" \
    --description "Implement func Greeting(name string) string returning 'Hello, <name>!' and call it from main." \
    --design "Add func Greeting(name string) string to main.go, have main call Greeting(\"world\"), then run gofmt and go test ./..." \
    2>/dev/null | grep -i created || true
fi

# ---- 6. write env.sh ----
cat > "$dir/env.sh" <<EOF
# source me, then run: loom plan|task --backend <claude|codex|cursor|opencode>
export PATH="\$HOME/.local/bin:\$HOME/go/bin:\$HOME/.opencode/bin:\$PATH"
export FLEET_DB_BIN="$FLEET_DB_BIN"
export LOOM_CONFIG_DIR="$LOOM_CONFIG_DIR"
export LOOM_WORKSPACE="$WS"
alias loom="$LOOM"
EOF

echo
echo "==> sandbox ready: $dir"
echo "    source $dir/env.sh   # then: loom plan --backend claude"
"$LOOM" data list -o json 2>/dev/null | grep -iv '"service"' \
  | python3 -c "import sys,json; [print('   ',i['id'],i['status'],'design='+str(len(i.get('design') or ''))) for i in json.load(sys.stdin)]" 2>/dev/null || true
