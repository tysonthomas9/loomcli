#!/usr/bin/env bash
# One-off cleanup of test-fixture pollution in a loom workspace's session ledger.
#
# Test runs that resolved the real workspace runtime dir appended rows for
# agent names that never existed (001, test, worker-1, custom-agent, ...). Those
# rows skew every session-health and success-rate reading taken from the ledger,
# and their session directories are pure disk cost.
#
# The keep-set is DERIVED from <runtime-dir>/profiles/ — the configured agents —
# never from a hard-coded name list. A denylist of "known bad" names is what
# makes the numbers wrong in a different direction: it keeps whichever fixture
# names nobody thought to enumerate. Anything that is not a configured agent is
# a drop candidate.
#
#   scrub-session-fixtures.sh --workspace PUPPET               # dry run (default)
#   scrub-session-fixtures.sh --workspace PUPPET --apply       # rewrite the ledgers
#   scrub-session-fixtures.sh --workspace PUPPET --apply --prune-dirs
#
# ORDERING: running --apply is only worth doing once the writer-containment fix
# is live in the build the fleet actually runs. Scrubbing before that just makes
# room for the next test run to re-pollute.

set -euo pipefail

usage() {
    cat >&2 <<'USAGE'
Usage: scrub-session-fixtures.sh (--workspace NAME | --runtime-dir DIR) [options]

Target selection (exactly one required):
  --workspace NAME     workspace under ${LOOM_CONFIG_DIR:-$HOME/.loom}/workspaces
  --runtime-dir DIR    explicit runtime dir (the one holding sessions/ and profiles/)

Options:
  --dry-run            report only, change nothing (DEFAULT)
  --apply              rewrite sessions/index.jsonl and usage.jsonl
  --prune-dirs         also delete fixture session directories; gated separately
                       from --apply and honoured only together with it
  -h, --help           this message

Exit codes: 0 ok, 1 refused (daemon running / bad target), 2 usage error.
USAGE
}

RUNTIME_DIR=""
WORKSPACE=""
APPLY=0
PRUNE_DIRS=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --workspace)
            [[ $# -ge 2 ]] || { echo "error: --workspace needs a value" >&2; exit 2; }
            WORKSPACE="$2"; shift 2 ;;
        --runtime-dir)
            [[ $# -ge 2 ]] || { echo "error: --runtime-dir needs a value" >&2; exit 2; }
            RUNTIME_DIR="$2"; shift 2 ;;
        --apply)      APPLY=1; shift ;;
        --prune-dirs) PRUNE_DIRS=1; shift ;;
        --dry-run)    APPLY=0; shift ;;
        -h|--help)    usage; exit 0 ;;
        *)            echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
    esac
done

if [[ -n "$WORKSPACE" && -n "$RUNTIME_DIR" ]]; then
    echo "error: pass --workspace or --runtime-dir, not both" >&2
    exit 2
fi
if [[ -z "$WORKSPACE" && -z "$RUNTIME_DIR" ]]; then
    echo "error: one of --workspace or --runtime-dir is required" >&2
    usage
    exit 2
fi
if [[ -n "$WORKSPACE" ]]; then
    # Mirrors bootstrap.WorkspaceDir: <LOOM_CONFIG_DIR|$HOME/.loom>/workspaces/<name>
    RUNTIME_DIR="${LOOM_CONFIG_DIR:-$HOME/.loom}/workspaces/$WORKSPACE"
fi

command -v jq >/dev/null 2>&1 || { echo "error: jq is required" >&2; exit 2; }

if [[ ! -d "$RUNTIME_DIR" ]]; then
    echo "error: runtime dir does not exist: $RUNTIME_DIR" >&2
    exit 1
fi

PROFILES_DIR="$RUNTIME_DIR/profiles"
SESSIONS_DIR="$RUNTIME_DIR/sessions"
INDEX_FILE="$SESSIONS_DIR/index.jsonl"
USAGE_FILE="$RUNTIME_DIR/usage.jsonl"
LOCK_FILE="$RUNTIME_DIR/daemon.lock"
STAMP="$(date +%Y%m%d-%H%M%S)"

# ── keep-set ────────────────────────────────────────────────────────────────
# Configured agents are the immediate subdirectories of profiles/, minus the
# `_`-prefixed ones (_templates and friends are not agents). An ABSENT
# profiles/ dir means "no opinion" — with no keep-set every row would look like
# a fixture, so the only safe action is none at all.
if [[ ! -d "$PROFILES_DIR" ]]; then
    echo "refusing: no profiles/ dir under $RUNTIME_DIR — cannot derive a keep-set" >&2
    exit 1
fi

KEEP_NAMES="$(find "$PROFILES_DIR" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; \
    | grep -v '^_' | sort)"
if [[ -z "$KEEP_NAMES" ]]; then
    echo "refusing: profiles/ holds no configured agents — cannot derive a keep-set" >&2
    exit 1
fi
KEEP_JSON="$(printf '%s\n' "$KEEP_NAMES" | jq -R -s -c 'split("\n") | map(select(length > 0))')"

echo "runtime dir : $RUNTIME_DIR"
echo "keep-set    : $(printf '%s' "$KEEP_NAMES" | tr '\n' ' ')"
echo "mode        : $([[ $APPLY -eq 1 ]] && echo APPLY || echo 'DRY RUN (default)')"
[[ $PRUNE_DIRS -eq 1 ]] && echo "prune-dirs  : requested"
echo

# ── daemon guard ────────────────────────────────────────────────────────────
# index.jsonl is append-only and live: a running daemon can append between our
# read and our rename, and the rename would silently drop those rows.
daemon_holder() {
    [[ -e "$LOCK_FILE" ]] || return 1
    if command -v lsof >/dev/null 2>&1; then
        local pids
        pids="$(lsof -t "$LOCK_FILE" 2>/dev/null || true)"
        if [[ -n "$pids" ]]; then
            printf '%s' "$pids" | tr '\n' ' '
            return 0
        fi
    fi
    # Fallback for hosts without lsof: the lock file's own PID record.
    local pid
    pid="$(jq -r '.pid // empty' <"$LOCK_FILE" 2>/dev/null || true)"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        printf '%s' "$pid"
        return 0
    fi
    return 1
}

# ── reporting ───────────────────────────────────────────────────────────────
# Rows we cannot parse are never dropped: an unreadable line is not evidence of
# a fixture, and this script must not be how data goes missing.
agent_names_of() {
    jq -R -r '(try fromjson catch null) as $r
        | if $r == null then "<unparseable>"
          else ($r.agent_name // "<missing>") end' "$1"
}

report_file() {
    local label="$1" file="$2"
    if [[ ! -f "$file" ]]; then
        echo "$label: $file — not present, nothing to do"
        echo
        return
    fi
    local total keep drop
    total="$(wc -l <"$file" | tr -d ' ')"
    keep=0; drop=0
    echo "$label: $file"
    printf '  %-24s %8s  %s\n' "agent_name" "rows" "verdict"
    while read -r count name; do
        local verdict="DROP"
        if [[ "$name" == "<unparseable>" ]]; then
            verdict="KEEP (unparseable)"
        elif printf '%s\n' "$KEEP_NAMES" | grep -qxF -- "$name"; then
            verdict="KEEP"
        fi
        if [[ "$verdict" == DROP ]]; then
            drop=$((drop + count))
        else
            keep=$((keep + count))
        fi
        printf '  %-24s %8s  %s\n' "$name" "$count" "$verdict"
    done < <(agent_names_of "$file" | sort | uniq -c | sort -rn | awk '{c=$1; $1=""; sub(/^ /,""); print c, $0}')
    printf '  %-24s %8s -> %s kept, %s dropped\n' "TOTAL" "$total" "$keep" "$drop"
    echo
}

# Rewrite in place: temp file in the same directory, mode 0600, atomic rename.
# 0600 is sessions.sessFilePerm, which finalize_test.go asserts — widening it
# here would turn a cleanup into a permissions regression.
rewrite_file() {
    local file="$1" backup tmp
    [[ -f "$file" ]] || { echo "  $file: not present, skipped"; return; }
    backup="$file.bak-$STAMP"
    cp -p "$file" "$backup"
    chmod 0600 "$backup"
    tmp="$(mktemp "$(dirname "$file")/.scrub-XXXXXX")"
    chmod 0600 "$tmp"
    jq -R -r --argjson keep "$KEEP_JSON" '
        . as $line
        | (try fromjson catch null) as $row
        | if $row == null then $line
          elif (($row.agent_name // "") | IN($keep[])) then $line
          else empty end' "$file" >"$tmp"
    mv "$tmp" "$file"
    chmod 0600 "$file"
    echo "  $file: $(wc -l <"$backup" | tr -d ' ') -> $(wc -l <"$file" | tr -d ' ') rows (backup: $backup, mode $(stat -f '%Lp' "$file" 2>/dev/null || stat -c '%a' "$file"))"
}

# Session directory names look like <YYYYMMDD>-<HHMMSS>-<agent>--<hash>; the
# agent name itself contains hyphens, so the `--` before the hash is the anchor.
dir_agent_name() {
    local base="$1"
    [[ "$base" =~ ^[0-9]{8}-[0-9]{6}-(.+)--[^-]+$ ]] || return 1
    printf '%s' "${BASH_REMATCH[1]}"
}

collect_fixture_dirs() {
    local d base name
    for d in "$SESSIONS_DIR"/*/; do
        [[ -d "$d" ]] || continue
        base="$(basename "$d")"
        if ! name="$(dir_agent_name "$base")"; then
            continue  # unrecognised layout: never touched
        fi
        printf '%s\n' "$KEEP_NAMES" | grep -qxF -- "$name" && continue
        printf '%s\t%s\n' "$name" "${d%/}"
    done
}

report_file "sessions index" "$INDEX_FILE"
report_file "usage ledger  " "$USAGE_FILE"

FIXTURE_DIRS=""
if [[ -d "$SESSIONS_DIR" ]]; then
    FIXTURE_DIRS="$(collect_fixture_dirs || true)"
fi
if [[ -n "$FIXTURE_DIRS" ]]; then
    echo "session directories not owned by a configured agent:"
    printf '%s\n' "$FIXTURE_DIRS" | cut -f1 | sort | uniq -c | sort -rn \
        | awk '{printf "  %-24s %8s\n", $2, $1}'
    printf '  %-24s %8s\n' "TOTAL" "$(printf '%s\n' "$FIXTURE_DIRS" | wc -l | tr -d ' ')"
else
    echo "session directories not owned by a configured agent: none"
fi
echo

if [[ $APPLY -eq 0 ]]; then
    echo "DRY RUN — nothing was modified. Re-run with --apply (and --prune-dirs"
    echo "for the directories) once the numbers above look right."
    exit 0
fi

if holder="$(daemon_holder)"; then
    echo "refusing to modify: the loom daemon holds $LOCK_FILE (pid $holder)." >&2
    echo "index.jsonl is append-only and live; stop the daemon first." >&2
    exit 1
fi

echo "rewriting ledgers:"
rewrite_file "$INDEX_FILE"
rewrite_file "$USAGE_FILE"
echo

if [[ $PRUNE_DIRS -eq 1 ]]; then
    if [[ -z "$FIXTURE_DIRS" ]]; then
        echo "prune-dirs: nothing to remove"
    else
        count=0
        while IFS=$'\t' read -r _ path; do
            [[ -n "$path" ]] || continue
            rm -rf -- "$path"
            count=$((count + 1))
        done <<<"$FIXTURE_DIRS"
        echo "prune-dirs: removed $count session directories"
    fi
else
    echo "prune-dirs: not requested — session directories left in place"
fi
echo
echo "done. Backups: *.bak-$STAMP"
