#!/bin/sh
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
RUNTIME="$HERE/.runtime"
REPO="$RUNTIME/repo"
BIN="$RUNTIME/bin"

mkdir -p "$BIN"
ln -sf "$HERE/loom-backend-playground" "$BIN/loom-backend-playground"

if [ ! -d "$REPO/.git" ]; then
  rm -rf "$REPO"
  mkdir -p "$REPO"
  cp -R "$HERE/repo-template/." "$REPO/"
  git -C "$REPO" init -q -b main
  git -C "$REPO" add .
  git -C "$REPO" -c user.email=playground@loom.local -c user.name=Playground \
    commit -q -m "Initial playground commit"
fi

export PATH="$BIN:$PATH"
{
  printf 'export PATH="%s:$PATH"\n' "$BIN"
  printf 'export LOOM_WORKSPACE=PLAYGROUND\n'
} > "$RUNTIME/env"

# Try to create the workspace. If a previous run left orphan fleet-db keys
# (e.g. interrupted setup, `kill -9`'d daemon), `loom workspace create`
# fails with HTTP 409 on role seeding. Recover by running teardown.sh
# (which surgically purges fleet-db:PLAYGROUND:*) and retrying once.
if ! loom workspace create playground --repos "$REPO" 2>"$RUNTIME/create.err"; then
  if grep -q "already exists" "$RUNTIME/create.err"; then
    echo "[setup] orphan fleet-db state detected; running teardown then retrying..." >&2
    "$HERE/teardown.sh" >/dev/null 2>&1 || true
    # teardown nukes .runtime/, so re-materialize before the retry.
    mkdir -p "$BIN"
    ln -sf "$HERE/loom-backend-playground" "$BIN/loom-backend-playground"
    rm -rf "$REPO"
    mkdir -p "$REPO"
    cp -R "$HERE/repo-template/." "$REPO/"
    git -C "$REPO" init -q -b main
    git -C "$REPO" add .
    git -C "$REPO" -c user.email=playground@loom.local -c user.name=Playground \
      commit -q -m "Initial playground commit"
    {
      printf 'export PATH="%s:$PATH"\n' "$BIN"
      printf 'export LOOM_WORKSPACE=PLAYGROUND\n'
    } > "$RUNTIME/env"
    loom workspace create playground --repos "$REPO"
  else
    cat "$RUNTIME/create.err" >&2
    exit 1
  fi
fi
rm -f "$RUNTIME/create.err"
export LOOM_WORKSPACE=PLAYGROUND
loom workspace use PLAYGROUND

loom agentdef add playground-planner --role plan --backend playground --repos "$(basename "$REPO")" --auto --task-filter needs_plan
loom agentdef add playground-coder   --role task --backend playground --repos "$(basename "$REPO")" --auto --task-filter has_design

loom data create --title "Seed task 1 (playground)" --type task --priority 2
loom data create --title "Seed task 2 (playground)" --type task --priority 2
loom data create --title "Seed task 3 (playground)" --type task --priority 3

cat <<EOF

Playground ready. Next steps:

  # 1. In a new terminal, source the env so loom finds the workspace + harness
  source $RUNTIME/env

  # 2. Start the agent supervisor (foreground; Ctrl+C to stop)
  loom daemon

  # 3. In a third terminal (after sourcing env too), watch progress
  loom monitor              # or:  loom data list

After ~30s the coder closes the seeded tasks. Inspect:
  git -C $REPO log --oneline
  cat $REPO/playground.txt

To tear down: $HERE/teardown.sh
EOF
