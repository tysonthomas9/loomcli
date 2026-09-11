# shellcheck shell=sh
# scripts/lib/sandbox.sh — shared temp-sandbox helpers for loom test/build scripts.
#
# Why this exists: scripts that build/test with `go` create throwaway sandbox
# dirs (via mktemp -d) and let `go` write build/link scratch into the shared
# $TMPDIR. When a run is killed (Ctrl-C, test timeout SIGTERM, or SIGKILL from
# an OOM / `podman machine` teardown / laptop sleep), those dirs orphan and the
# temp volume fills up over time. These helpers address that by:
#   (1) rooting every sandbox under $TMPDIR/loom so orphans are discoverable and
#       sweepable by scripts/clean-stale-tmp.sh,
#   (2) pointing GOTMPDIR at the sandbox so `go`'s go-build*/go-link* scratch is
#       contained and removed together with the sandbox,
#   (3) offering cleanup that fires on INT and TERM, not just EXIT.
#
# POSIX sh — safe to source from both #!/bin/sh and bash scripts. Sourcing only
# defines functions; it installs no traps and has no other side effects.
#
# IMPORTANT: call loom_mktemp_dir directly (NOT in a `$(...)` command
# substitution) and read the result from $LOOM_SANDBOX_DIR. A command
# substitution runs the function in a subshell, so its `export GOTMPDIR` would
# not reach your script and `go` scratch would not be contained.
#
# Usage (simple script — helper owns teardown):
#     . ".../sandbox.sh"
#     loom_mktemp_dir mytool; tmp="$LOOM_SANDBOX_DIR"
#     loom_autoclean "$tmp"                     # rm -rf on EXIT/INT/TERM
#
# Usage (rich script that already has its own cleanup for containers/PIDs/logs):
#     . ".../sandbox.sh"
#     loom_mktemp_dir loom-podman-stack; TMP_ROOT="$LOOM_SANDBOX_DIR"
#     ...
#     trap cleanup EXIT INT TERM                # keep your cleanup; just add INT TERM
#   Do NOT call loom_autoclean here — your cleanup already removes TMP_ROOT.

# loom_tmp_root: predictable parent dir for all loom sandboxes.
# Override with LOOM_TMP_ROOT if you need sandboxes on a different volume.
loom_tmp_root() {
    _loom_base="${LOOM_TMP_ROOT:-${TMPDIR:-/tmp}}"
    printf '%s' "${_loom_base%/}/loom"          # strip trailing slash → no // in path
}

# loom_mktemp_dir [name]: create a fresh sandbox dir under the loom root, set and
# export GOTMPDIR to it (contains `go` build/link scratch), and publish the path
# in $LOOM_SANDBOX_DIR. Must run in the current shell (see note above), so it
# assigns a variable rather than echoing. Does NOT install a trap — the caller
# owns cleanup (loom_autoclean, or its own teardown). If a script makes several
# sandboxes, GOTMPDIR tracks the most recent; all live under the sweepable root.
loom_mktemp_dir() {
    _loom_root="$(loom_tmp_root)"
    mkdir -p "$_loom_root" || return 1
    LOOM_SANDBOX_DIR="$(mktemp -d "$_loom_root/${1:-sb}.XXXXXX")" || return 1
    GOTMPDIR="$LOOM_SANDBOX_DIR"
    export GOTMPDIR
}

# loom_autoclean DIR [DIR...]: install a trap that removes the given dirs on
# EXIT, INT, and TERM, preserving the original exit status. If the script
# defines a `loom_extra_cleanup` function, it is invoked first (for any teardown
# beyond removing temp dirs). For simple scripts only — rich scripts should keep
# their own cleanup and merely widen its trap to `EXIT INT TERM`.
loom_autoclean() {
    _LOOM_CLEAN_DIRS="$*"
    trap '_loom_run_cleanup' EXIT INT TERM
}

_loom_run_cleanup() {
    _loom_status=$?
    trap - EXIT INT TERM
    if command -v loom_extra_cleanup >/dev/null 2>&1; then
        loom_extra_cleanup || true
    fi
    for _loom_cd in $_LOOM_CLEAN_DIRS; do
        [ -n "$_loom_cd" ] && rm -rf "$_loom_cd"
    done
    exit "$_loom_status"
}

# loom_source_sandbox is intentionally omitted: each caller sources this file by
# resolving the repo root, e.g.
#     . "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)/scripts/lib/sandbox.sh"
