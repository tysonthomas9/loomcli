#!/usr/bin/env bash
# loom-slot.sh — run a heavy job (check-go, race runs, AFT, local-mode stacks)
# inside one of N machine-wide slots, so several agents can work in parallel
# without clashing on ports, compose project names, image tags or temp dirs.
#
# Usage:
#   scripts/loom-slot.sh run [--slot N] -- <command...>   # take a slot, run, release
#   scripts/loom-slot.sh env <N>                          # print the slot's exports
#   scripts/loom-slot.sh status                           # who holds which slot
#
# `run` takes the first free slot (or waits for slot N with --slot) and runs the
# command with these exported (slot N, base B = LOOM_SLOT_PORT_BASE + N*100):
#   LOOM_SLOT=N  LOOM_SLOT_DIR=<state>/slot-N  TMPDIR=<slot dir>/tmp
#   LOCAL_MODE_COMPOSE_PROJECT=loomcli-slot-N  (make derives image tags from it)
#   LOCAL_MODE_FLEETDB_PORT=B+80  LOCAL_MODE_API_PORT=B+82  LOCAL_MODE_UI_PORT=B+83
#   LOCAL_MODE_API_URL=http://127.0.0.1:B+82
#   E2E_PORT=B+90  E2E_FRONTEND_PORT=B+91  (tests/aft/run-aft.sh)
#   FLEET_DB_REPO / LOCAL_MODE_FLEETDB_CONTEXT, AFT_DIR (durable AFT inputs)
# Values already in the environment win, except the slot-owned ones above.
#
# Env:
#   LOOM_SLOTS            number of slots (default 4)
#   LOOM_SLOT_PORT_BASE   port block base (default 18000 → slot 1 = 18100-18199)
#   LOOM_SLOT_STATE_DIR   lock/temp root (default ~/.cache/loom/slots)
#   LOOM_SLOT_WAIT_SECS   give up waiting for a slot after this many seconds
#                         (default 0 = wait forever)
#   LOOM_SLOT_STOP_SECS   on INT/TERM, KILL the command's process group if it
#                         has not exited after this many seconds (default 60)
#   LOOM_AFT_INPUTS       durable AFT inputs (default ~/.cache/loom/aft-inputs,
#                         holding fleet-db/ and testing-app/)
#
# Locks are directories (mkdir is atomic on Linux and macOS, no flock needed)
# holding the owner's pid; a lock whose owner is gone is reclaimed.
set -euo pipefail

SLOTS="${LOOM_SLOTS:-4}"
PORT_BASE="${LOOM_SLOT_PORT_BASE:-18000}"
STATE_DIR="${LOOM_SLOT_STATE_DIR:-$HOME/.cache/loom/slots}"
WAIT_SECS="${LOOM_SLOT_WAIT_SECS:-0}"
STOP_SECS="${LOOM_SLOT_STOP_SECS:-60}"
AFT_INPUTS="${LOOM_AFT_INPUTS:-$HOME/.cache/loom/aft-inputs}"

die() { echo "loom-slot: $*" >&2; exit 2; }

[[ "$SLOTS" =~ ^[1-9][0-9]*$ ]] || die "LOOM_SLOTS must be a positive integer"
[[ "$PORT_BASE" =~ ^[1-9][0-9]*$ ]] || die "LOOM_SLOT_PORT_BASE must be a positive integer"
[[ "$STOP_SECS" =~ ^[1-9][0-9]*$ ]] || die "LOOM_SLOT_STOP_SECS must be a positive integer"
(( PORT_BASE + SLOTS * 100 + 99 < 65536 )) || die "port block past 65535"

check_slot() {
	[[ "$1" =~ ^[1-9][0-9]*$ ]] && (( $1 <= SLOTS )) || die "slot must be 1..$SLOTS (got '$1')"
}

slot_env() {
	local n="$1" base=$((PORT_BASE + $1 * 100)) dir="$STATE_DIR/slot-$1"
	local fdb="${FLEET_DB_REPO:-$AFT_INPUTS/fleet-db}"
	# No GOTMPDIR: go test puts t.TempDir() under it, bypassing the TMPDIR that
	# tests such as internal/loomharness/claude set for their own isolation.
	cat <<EOF
export LOOM_SLOT=$n
export LOOM_SLOT_DIR='$dir'
export TMPDIR='$dir/tmp'
export LOCAL_MODE_COMPOSE_PROJECT=loomcli-slot-$n
export LOCAL_MODE_FLEETDB_PORT=$((base + 80))
export LOCAL_MODE_API_PORT=$((base + 82))
export LOCAL_MODE_UI_PORT=$((base + 83))
export LOCAL_MODE_API_URL=http://127.0.0.1:$((base + 82))
export E2E_PORT=$((base + 90))
export E2E_FRONTEND_PORT=$((base + 91))
export FLEET_DB_REPO='$fdb'
export LOCAL_MODE_FLEETDB_CONTEXT='$fdb'
export AFT_DIR='${AFT_DIR:-$AFT_INPUTS/testing-app}'
EOF
}

lock_dir() { echo "$STATE_DIR/slot-$1.lock"; }

owner_alive() {
	local pid
	pid="$(cat "$1/pid" 2>/dev/null || true)"
	[[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null
}

take_lock() {
	mkdir "$1" 2>/dev/null || return 1
	echo "$$" >"$1/pid"
	printf '%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$PWD" "$CMD_TEXT" >"$1/info"
}

# try_lock N → 0 if this process now holds slot N.
try_lock() {
	local d m pid
	d="$(lock_dir "$1")"
	take_lock "$d" && return 0
	owner_alive "$d" && return 1
	# Reclaim a stale lock under a per-slot mutex, so two waiters cannot both
	# reclaim and the second never removes the lock the first just took. The pid
	# file is written right after mkdir, so a lock without one is only stale once
	# it is older than a minute. A mutex left by a reclaimer that died is cleared
	# after a minute too.
	m="$d.reclaim"
	[[ -z "$(find "$m" -maxdepth 0 -mmin +1 2>/dev/null)" ]] || rmdir "$m" 2>/dev/null || true
	mkdir "$m" 2>/dev/null || return 1
	pid="$(cat "$d/pid" 2>/dev/null || true)"
	if [[ -d "$d" ]] && ! owner_alive "$d" &&
		{ [[ -n "$pid" ]] || [[ -n "$(find "$d" -maxdepth 0 -mmin +1 2>/dev/null)" ]]; }; then
		rm -rf "$d"
		echo "loom-slot: reclaimed stale slot $1 (owner pid ${pid:-unknown})" >&2
	fi
	local rc=1
	take_lock "$d" && rc=0
	rmdir "$m" 2>/dev/null || true
	return "$rc"
}

# release removes the lock only while this process still owns it.
release() {
	local d
	[[ -n "${GOT:-}" ]] || return 0
	d="$(lock_dir "$GOT")"
	[[ "$(cat "$d/pid" 2>/dev/null || true)" == "$$" ]] && rm -rf "$d"
	return 0
}

# group_alive: any live (non-zombie) process left in the command's process
# group. Not `kill -0 -PGID`: the unreaped leader's zombie would count.
group_alive() {
	ps -A -o pgid=,stat= 2>/dev/null | awk -v g="$CHILD" '$1 == g && $2 !~ /^Z/ { found = 1 } END { exit !found }'
}

# stop_child SIG: signal the command's whole process group (make, compose,
# grandchildren), wait for the group to empty (TERM after 5s if SIG was not
# TERM), then KILL whatever is left after LOOM_SLOT_STOP_SECS.
stop_child() {
	[[ -n "${CHILD:-}" ]] || return 0
	kill "-$1" -- "-$CHILD" 2>/dev/null || true
	local waited=0
	while group_alive && (( waited < STOP_SECS * 10 )); do
		# Background jobs of a non-interactive shell ignore INT: follow with
		# TERM after a 5s grace.
		(( waited == 50 )) && [[ "$1" != TERM ]] && kill -TERM -- "-$CHILD" 2>/dev/null
		sleep 0.1
		waited=$((waited + 1))
	done
	if group_alive; then
		echo "loom-slot: process group $CHILD still running after ${STOP_SECS}s; sending KILL" >&2
		kill -KILL -- "-$CHILD" 2>/dev/null || true
		while group_alive; do sleep 0.1; done
	fi
	wait "$CHILD" 2>/dev/null || true
	CHILD=""
}

cmd_status() {
	local n d
	for ((n = 1; n <= SLOTS; n++)); do
		d="$(lock_dir "$n")"
		if [[ -d "$d" ]]; then
			if owner_alive "$d"; then
				printf 'slot %d  held  pid %s  %s\n' "$n" "$(cat "$d/pid")" "$(cat "$d/info" 2>/dev/null)"
			else
				printf 'slot %d  stale (owner gone)\n' "$n"
			fi
		else
			printf 'slot %d  free\n' "$n"
		fi
	done
}

cmd_run() {
	local want=""
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--slot) [[ $# -ge 2 ]] || die "--slot needs a value"; want="$2"; shift 2 ;;
			--slot=*) want="${1#*=}"; shift ;;
			--) shift; break ;;
			*) break ;;
		esac
	done
	[[ $# -gt 0 ]] || die "run needs a command (scripts/loom-slot.sh run [--slot N] -- cmd...)"
	[[ -z "$want" ]] || check_slot "$want"
	CMD_TEXT="$(printf '%s ' "$@" | tr '\n\t' '  ')"
	mkdir -p "$STATE_DIR"

	local start=$SECONDS n announced=""
	GOT=""
	while [[ -z "$GOT" ]]; do
		if [[ -n "$want" ]]; then
			try_lock "$want" && GOT="$want"
		else
			for ((n = 1; n <= SLOTS; n++)); do
				if try_lock "$n"; then GOT="$n"; break; fi
			done
		fi
		[[ -n "$GOT" ]] && break
		if (( WAIT_SECS > 0 && SECONDS - start >= WAIT_SECS )); then
			die "no free slot after ${WAIT_SECS}s"
		fi
		[[ -n "$announced" ]] || { echo "loom-slot: waiting for ${want:+slot }${want:-a free slot}..." >&2; announced=1; }
		local nap=5
		if (( WAIT_SECS > 0 && WAIT_SECS - (SECONDS - start) < nap )); then
			nap=$((WAIT_SECS - (SECONDS - start)))
			(( nap > 0 )) || nap=1
		fi
		sleep "$nap"
	done

	# Release on any exit, and only after the command is gone: on INT/TERM the
	# command's whole process group is stopped first, so a slot is never freed
	# under a running job.
	CHILD=""
	trap 'release' EXIT
	trap 'stop_child INT; exit 130' INT
	trap 'stop_child TERM; exit 143' TERM

	eval "$(slot_env "$GOT")"
	mkdir -p "$TMPDIR"
	echo "loom-slot: slot $GOT (project $LOCAL_MODE_COMPOSE_PROJECT, ports $LOCAL_MODE_FLEETDB_PORT/$LOCAL_MODE_API_PORT/$LOCAL_MODE_UI_PORT, e2e $E2E_PORT/$E2E_FRONTEND_PORT)" >&2
	local rc=0
	# Job control puts the command in its own process group, so the whole tree
	# can be signalled. Keep stdin attached.
	set -m
	"$@" <&0 &
	CHILD=$!
	set +m
	wait "$CHILD" || rc=$?
	CHILD=""
	return "$rc"
}

case "${1:-}" in
	run) shift; cmd_run "$@" ;;
	env) [[ $# -eq 2 ]] || die "usage: loom-slot.sh env <N>"; check_slot "$2"; slot_env "$2" ;;
	status) cmd_status ;;
	*) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
