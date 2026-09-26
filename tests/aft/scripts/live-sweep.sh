#!/usr/bin/env bash
# Authoritative post-run cleanup for the live tier. Runs from run-aft.sh AFTER aft
# exits, pass or fail, and its verdict changes the run's exit code.
#
# Why this exists rather than trusting the suite:
#   * aft skips every remaining step once one fails (runner.ts), so a test's own
#     final "close the tab" step is skipped on exactly the runs that leak.
#   * aft treats suite teardown failure as report-only, so a teardown that cannot
#     close a tab still reports success.
#   * DeleteTab removes metadata first and only logs a PTY-kill failure
#     (terminal/service_tabs.go), so "no tabs" is not by itself proof the child died.
# Hence: delete tabs, then verify against the PROCESS table, and fail loudly.
#
# Usage: live-sweep.sh <real-binary-path> <pid-baseline-file>
set -uo pipefail   # no -e: every branch here must be reachable and reported

base="${AFT_BASE_URL:-}"
real_bin="${1:-}"
baseline_file="${2:-}"
curl_opts=(-sf --max-time 10)
rc=0

if [[ -z "$base" ]]; then
    echo "[sweep] AFT_BASE_URL unset; cannot sweep terminal tabs" >&2
    exit 1
fi

workspaces() {
    # /api/workspaces returns {"success":..,"workspaces":[..]} — NOT the {"data":..}
    # envelope most endpoints use. Reading the wrong key yields an empty list and a
    # confident, false "nothing left running".
    curl "${curl_opts[@]}" "$base/api/workspaces" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
items = payload.get("workspaces")
if not isinstance(items, list):
    raise SystemExit("unexpected /api/workspaces shape: expected a list under \"workspaces\"")
for ws in items:
    if isinstance(ws, dict):
        key = ws.get("key") or ws.get("id") or ws.get("workspace_key")
        if key:
            print(key)
'
}

tabs_in() {
    curl "${curl_opts[@]}" "$base/api/workspaces/$1/terminal/tabs" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
items = payload.get("data") if isinstance(payload, dict) else payload
if not isinstance(items, list):
    raise SystemExit("unexpected terminal tabs shape")
for tab in items:
    if isinstance(tab, dict) and tab.get("session_name"):
        print(tab["session_name"])
'
}

ws_list="$(workspaces)" || {
    echo "[sweep] FAILED to enumerate workspaces — cannot prove cleanup" >&2
    exit 1
}

closed=0
for ws in $ws_list; do
    session_list="$(tabs_in "$ws")" || {
        echo "[sweep] FAILED to read terminal tabs for $ws" >&2
        rc=1
        continue
    }
    for session in $session_list; do
        if curl "${curl_opts[@]}" -X DELETE "$base/api/workspaces/$ws/terminal/tabs/$session" >/dev/null; then
            closed=$((closed + 1))
        else
            echo "[sweep] FAILED to delete tab $session in $ws" >&2
            rc=1
        fi
    done
done

remaining=0
for ws in $ws_list; do
    # Checked assignment, NOT `$(tabs_in … | grep -c . || true)`: that swallows an API
    # error or malformed body and counts zero, i.e. the authoritative cleanup check
    # reports "clean" precisely when it failed to look. Same class of bug as the
    # earlier grep-no-match abort — silence must never read as safety here.
    if ! ws_tabs="$(tabs_in "$ws")"; then
        echo "[sweep] FAILED to re-read terminal tabs for $ws during verification" >&2
        rc=1
        continue
    fi
    n="$(printf '%s' "$ws_tabs" | grep -c . || true)"
    remaining=$((remaining + n))
done

# Process-level proof. Tab metadata can be gone while the child survives, so the
# process table is the only honest oracle for "did the paid CLI actually die".
leaked=""
if [[ -z "$real_bin" || ! -f "${baseline_file:-}" ]]; then
    # Fail closed: without both inputs there is no process proof, and "we did not
    # look" must not render as "nothing leaked".
    echo "[sweep] FAILED: no process baseline available, cannot prove the paid CLI died" >&2
    rc=1
else
    # Match the BASENAME, not the resolved path. Loom execs the configured binary by
    # name (defaultCodexBinary = "codex", codex_runtime.go), so the child's argv is
    # `codex app-server …` and a pgrep for /abs/path/codex never matches the very
    # process this check exists to catch. Basename matching can also flag an unrelated
    # CLI the operator started mid-run; the baseline diff makes that rare and the
    # failure direction is the safe one.
    # Anchor to argv[0]. A bare `pgrep -f codex` matches any command line CONTAINING
    # the string: `loom … --backend codex`, and ChatGPT.app's updater living under
    # ~/Library/Caches/com.openai.codex. That reported four "leaks" of which two were
    # the user's desktop app — a detector that cries wolf is as useless as one that
    # stays silent. This matches only `codex …` or `/any/path/codex …`.
    bin_base="$(basename "$real_bin")"
    sleep 2   # let a killed PTY child finish exiting before we accuse it
    # Distinguish pgrep's exit codes. `|| true` used to swallow all of them, so a
    # genuine lookup FAILURE (rc>1: bad pattern, pgrep missing, permission problem)
    # produced an empty list and this check reported "no leak" — the authoritative
    # cleanup proof silently passing precisely when it could not look. rc=1 is the
    # normal "nothing matched"; anything above that fails the run.
    pgrep_out="$(pgrep -f "^([^ ]*/)?${bin_base}([[:space:]]|$)" 2>/dev/null)"
    pgrep_rc=$?
    if [[ "$pgrep_rc" -gt 1 ]]; then
        echo "[sweep] FAILED: pgrep for '$bin_base' errored (rc=$pgrep_rc); cannot prove the paid CLI died" >&2
        rc=1
        pgrep_out=""
    fi
    for pid in $pgrep_out; do
        [[ "$pid" == "$$" ]] && continue
        if ! grep -qx "$pid" "$baseline_file" 2>/dev/null; then
            leaked="$leaked $pid($(ps -o command= -p "$pid" 2>/dev/null | cut -c1-60))"
        fi
    done
fi

echo "[sweep] closed=$closed tabs_remaining=$remaining leaked_pids=${leaked:-none}"
if [[ "$remaining" != "0" ]]; then
    echo "[sweep] LIVE CLEANUP FAILED: $remaining terminal tab(s) still registered" >&2
    rc=1
fi
if [[ -n "$leaked" ]]; then
    echo "[sweep] LIVE CLEANUP FAILED: paid backend process(es) still running:$leaked" >&2
    echo "[sweep] kill them before rerunning:$leaked" >&2
    rc=1
fi
exit "$rc"
