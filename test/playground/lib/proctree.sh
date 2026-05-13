#!/usr/bin/env bash
# Process-tree helpers for daemon-lifecycle scenarios.
#
# Uses the same `ps -A -o pid=,ppid=,pgid=` and `lsof -p <pid> -d cwd`
# invocations the daemon's process inspector uses, so OS-level assertions
# match what the daemon itself sees. If you update the daemon's process
# inspector flags, mirror them in assert_no_orphans_under below.
#
# Depends on lib/common.sh (red, green, log, fails).

# read_pid_file <path> [<timeout-sec>] — bounded poll for a PID file written
# by a backend. Emits the integer PID on stdout when the file appears with
# non-empty content; returns 1 if the deadline elapses.
#
# PID-file polling that also returns the file content. Use lib/common.sh's
# wait_for_file for marker files where the content is empty.
read_pid_file() {
  local path="$1" timeout="${2:-25}"
  local deadline=$(( $(date +%s) + timeout ))
  while [ ! -s "$path" ]; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      return 1
    fi
    sleep 0.5
  done
  tr -d '[:space:]' < "$path"
}

# assert_pid_alive <pid> [<label>] — increments `fails` (from common.sh)
# and prints a red line if the process is gone; prints green otherwise.
assert_pid_alive() {
  local pid="$1" label="${2:-pid $1}"
  if kill -0 "$pid" 2>/dev/null; then
    green "  ✓ $label: PID $pid alive"
  else
    red "  ✗ $label: PID $pid is gone (expected alive)"
    fails=$((fails+1))
  fi
}

# assert_pid_dead <pid> [<label>] — inverse of assert_pid_alive.
assert_pid_dead() {
  local pid="$1" label="${2:-pid $1}"
  if kill -0 "$pid" 2>/dev/null; then
    red "  ✗ $label: PID $pid still alive (expected dead)"
    fails=$((fails+1))
  else
    green "  ✓ $label: PID $pid is gone"
  fi
}

# assert_no_orphans_under <worktree-path> — fail if any process has cwd
# under the given worktree. Test-time check (no PPID==1 filter), so any
# survivor under our worktree — orphaned to init or not — is a failure.
#
# ps -A -o pid=,ppid=,pgid=        — headerless process listing
# lsof -p <pid> -d cwd -F n -a     — cwd of pid, in field-tagged output
assert_no_orphans_under() {
  local worktree="$1"
  if [ -z "$worktree" ]; then
    red "  ✗ assert_no_orphans_under: empty worktree path"
    fails=$((fails+1))
    return
  fi
  # Resolve symlinks (macOS /var → /private/var) so prefix match catches
  # both forms.
  local resolved
  resolved="$(cd "$worktree" 2>/dev/null && pwd -P || printf '%s' "$worktree")"
  local survivors=""
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    local pid cwd
    pid="$(printf '%s' "$line" | awk '{print $1}')"
    [ -z "$pid" ] && continue
    cwd="$(lsof -p "$pid" -d cwd -F n -a 2>/dev/null | awk '/^n/{print substr($0,2); exit}')"
    [ -z "$cwd" ] && continue
    case "$cwd" in
      "$worktree"|"$worktree"/*|"$resolved"|"$resolved"/*)
        survivors="$survivors $pid"
        ;;
    esac
  done < <(ps -A -o pid=,ppid=,pgid= 2>/dev/null)

  if [ -n "$survivors" ]; then
    red "  ✗ orphans under $worktree:$survivors"
    fails=$((fails+1))
  else
    green "  ✓ no orphans under $worktree"
  fi
}
