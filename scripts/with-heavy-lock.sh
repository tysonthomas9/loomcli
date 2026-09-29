#!/usr/bin/env bash
# Serialize full gates and Git lab runs across all checkouts for this user.
set -euo pipefail

if (( $# == 0 )); then
    echo "usage: with-heavy-lock.sh <command> [args...]" >&2
    exit 2
fi

umask 077
lock_dir="$HOME/.cache/loom/heavy.lock"
mkdir -p "${lock_dir%/*}"
owner_file="$lock_dir/owner"
token="$$.$(date +%s).$RANDOM"
printf -v command '%q ' "$@"
have_lock=false

release() {
    if [[ "$have_lock" == true ]] && { [[ ! -f "$owner_file" ]] || [[ "$(sed -n '3p' "$owner_file")" == "$token" ]]; }; then
        rm -f "$owner_file" "$lock_dir/owner.tmp.$$"
        rmdir "$lock_dir" 2>/dev/null || true
    fi
}
trap release EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

next_report=0
while ! mkdir "$lock_dir" 2>/dev/null; do
    owner_pid=
    if [[ -f "$owner_file" ]]; then
        IFS= read -r owner_pid < "$owner_file" || true
    fi

    stale=false
    if [[ "$owner_pid" =~ ^[0-9]+$ ]]; then
        if ! kill -0 "$owner_pid" 2>/dev/null; then
            stale=true
        fi
    elif [[ -d "$lock_dir" ]]; then
        # Give a new holder time to publish its owner file before takeover.
        modified=$(stat -f %m "$lock_dir" 2>/dev/null || stat -c %Y "$lock_dir")
        if (( $(date +%s) - modified >= 60 )); then
            stale=true
        fi
    fi

    if [[ "$stale" == true ]]; then
        # Only one waiter may remove a stale lock. Other waiters keep seeing
        # the lock until the reaper removes it, then compete for mkdir above.
        reaper="$lock_dir/reaping"
        if mkdir "$reaper" 2>/dev/null; then
            printf '%s\n' "$$" > "$reaper/pid"
            moved_pid=
            if [[ -f "$owner_file" ]]; then
                IFS= read -r moved_pid < "$owner_file" || true
            fi
            if [[ "$moved_pid" =~ ^[0-9]+$ ]] && kill -0 "$moved_pid" 2>/dev/null; then
                rm -f "$reaper/pid"
                rmdir "$reaper"
            else
                rm -f "$owner_file" "$lock_dir"/owner.tmp.*
                rm -f "$reaper/pid"
                rmdir "$reaper"
                if rmdir "$lock_dir" 2>/dev/null; then
                    echo "Reclaimed stale heavy-run lock (PID ${moved_pid:-unknown})." >&2
                fi
            fi
        elif [[ -d "$reaper" ]]; then
            reaper_pid=
            if [[ -f "$reaper/pid" ]]; then
                IFS= read -r reaper_pid < "$reaper/pid" || true
            fi
            reaper_stale=false
            if [[ "$reaper_pid" =~ ^[0-9]+$ ]]; then
                if ! kill -0 "$reaper_pid" 2>/dev/null; then reaper_stale=true; fi
            elif [[ -d "$reaper" ]]; then
                modified=$(stat -f %m "$reaper" 2>/dev/null || stat -c %Y "$reaper")
                if (( $(date +%s) - modified >= 60 )); then reaper_stale=true; fi
            fi
            if [[ "$reaper_stale" == true ]]; then
                rm -f "$reaper/pid"
                if rmdir "$reaper" 2>/dev/null && [[ ! -f "$owner_file" ]]; then
                    rmdir "$lock_dir" 2>/dev/null || true
                fi
            fi
        fi
        continue
    fi

    now=$(date +%s)
    if (( now >= next_report )); then
        if [[ -f "$owner_file" ]]; then
            holder=$(sed -n '4p' "$owner_file")
            started=$(sed -n '2p' "$owner_file")
            echo "Waiting for heavy run: PID $owner_pid, started $started, command $holder" >&2
        else
            echo "Waiting for heavy run: owner metadata is being written." >&2
        fi
        next_report=$((now + 60))
    fi
    sleep 2
done
have_lock=true

started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
metadata="$lock_dir/owner.tmp.$$"
printf '%s\n%s\n%s\n%s\n' "$$" "$started" "$token" "$command" > "$metadata"
mv "$metadata" "$owner_file"
echo "Running heavy command under lock: $command" >&2
"$@"
