#!/usr/bin/env bash
# terminal.sh: read the open agent terminal from a suite's run: step.
# The terminal is xterm.js with its DOM renderer: one div per row under .xterm-rows.
#
#   terminal.sh mounted [secs]               wait until the terminal grid mounts
#   terminal.sh wait <ERE> [secs] [fail-ERE] wait until the rows match <ERE>; stop at once on <fail-ERE>
#
# Waits are bounded (default 100s, under the 120s run: cap). On timeout or a fail
# match it prints the last rows and exits 1.
set -euo pipefail

cmd="${1:?usage: terminal.sh mounted|wait ...}"
browser() { agent-browser --session "${AFT_SESSION:?}" "$@"; }
rows() {
  browser eval "Array.from(document.querySelectorAll('[data-testid=terminal-wrapper] .xterm-rows > div')).map(e => e.textContent).join('\\n')" 2> /dev/null || true
}

case "$cmd" in
mounted)
  for _ in $(seq 1 "${2:-100}"); do
    browser eval "!!document.querySelector('[data-testid=terminal-wrapper] .xterm-rows')" 2> /dev/null | grep -q true && exit 0
    sleep 1
  done
  echo "agent terminal never mounted" >&2
  exit 1
  ;;
wait)
  want="${2:?usage: terminal.sh wait <ERE> [secs] [fail-ERE]}" text=""
  for _ in $(seq 1 "${3:-100}"); do
    text="$(rows)"
    if [ -n "${4:-}" ] && grep -qE "$4" <<< "$text"; then
      echo "agent terminal shows a failure: ${text: -600}" >&2
      exit 1
    fi
    grep -qE "$want" <<< "$text" && exit 0
    sleep 1
  done
  echo "agent terminal never showed /$want/; last rows: ${text: -600}" >&2
  exit 1
  ;;
*)
  echo "unknown command $cmd" >&2
  exit 2
  ;;
esac
