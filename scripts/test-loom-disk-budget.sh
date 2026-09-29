#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
tmp=$(mktemp -d)
trap 'rm -r "$tmp"' EXIT
mkdir -p "$tmp/home" "$tmp/cache/00" "$tmp/bin"

python3 - "$tmp/cache/00" <<'PY'
import os, sys, time
for name, hours in (("old-a", 26), ("recent-d", 8), ("current-a", 1)):
    path = os.path.join(sys.argv[1], name)
    with open(path, "wb") as f:
        f.write(b"cache")
    age = time.time() - hours * 3600
    os.utime(path, (age, age))
path = os.path.join(sys.argv[1], "old-exec-d")
os.mkdir(path)
with open(os.path.join(path, "binary"), "wb") as f:
    f.write(b"executable")
age = time.time() - 26 * 3600
os.utime(path, (age, age))
PY

cat > "$tmp/bin/fake-podman" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PODMAN_CALLS"
if [[ ${1:-} == --connection ]]; then
  [[ ${2:-} == podman-machine-default ]] || exit 2
  shift 2
fi
case "$1 ${2:-}" in
  'info ') exit 0;;
  'image ls') printf '%s\n' 'localhost/loomcli-p15-reset-loom:latest|111' 'localhost/loomcli-local-mode-loom:latest|222' 'localhost/loomcli-p15-reset-loom-claude:latest|777' 'docker.io/library/loomcli-local-mode-p14-fleet-db:latest|666' 'localhost/loomgit-lab:current|555' 'localhost/loomgit-lab:old|333' 'unrelated:latest|444';;
  'image inspect')
    case "$*" in
      *'{{.Id}}'*) echo sha256:111;;
      *loomcli-p15-reset-loom-claude:latest*) echo '{"com.docker.compose.project":"loomcli-local-mode"}';;
      *loomcli-local-mode-loom:latest*) echo '{"com.docker.compose.project":"loomcli-local-mode"}';;
      *) echo '{}';;
    esac;;
  'image rm') exit 0;;
  'ps -a')
    case "$*" in *'{{.Image}}'*) echo 'docker.io/library/loomcli-p15-reset-loom:latest';; *) echo 111;; esac;;
  'machine ssh') echo 'fstrim: 1048576 bytes';;
  *) exit 2;;
esac
SH
chmod +x "$tmp/bin/fake-podman"
ln -s fake-podman "$tmp/bin/podman"
export PATH="$tmp/bin:$PATH"
export HOME="$tmp/home"
export LOOM_DISK_GOCACHE="$tmp/cache"
export LOOM_DISK_PODMAN="$tmp/bin/fake-podman"
export LOOM_DISK_CLEANUP_LOG="$tmp/cleanup.log"
export PODMAN_CALLS="$tmp/podman.calls"

if LOOM_HEAVY_LOCK_HELD=$$ LOOM_DISK_FREE_KIB_OVERRIDE=$((10 * 1024 * 1024)) \
    "$root/scripts/loom-disk-budget.sh" 2>"$tmp/unlocked.err"; then
    echo 'budget cleanup ran without owning the heavy lock' >&2
    exit 1
fi
test -e "$tmp/cache/00/old-a"
grep -q 'requires the heavy-run lock' "$tmp/unlocked.err"

# Variables expand in the child shell after the wrapper's preflight.
# shellcheck disable=SC2016
LOOM_DISK_FREE_KIB_OVERRIDE=$((30 * 1024 * 1024)) "$root/scripts/with-heavy-lock.sh" \
    bash -c 'test ! -e "$LOOM_DISK_GOCACHE/00/old-a" && test -e "$LOOM_DISK_GOCACHE/00/recent-d"'
test ! -e "$tmp/cache/00/old-a"
test ! -e "$tmp/cache/00/old-exec-d"
test -e "$tmp/cache/00/recent-d"
LOOM_DISK_FREE_KIB_OVERRIDE=$((10 * 1024 * 1024)) "$root/scripts/with-heavy-lock.sh" true
test ! -e "$tmp/cache/00/recent-d"
test -e "$tmp/cache/00/current-a"
grep -q 'removed image localhost/loomgit-lab:old' "$LOOM_DISK_CLEANUP_LOG"
grep -q 'removed image docker.io/library/loomcli-local-mode-p14-fleet-db:latest' "$LOOM_DISK_CLEANUP_LOG"
grep -q 'kept newest Git-lab image localhost/loomgit-lab:current' "$LOOM_DISK_CLEANUP_LOG"
grep -q 'kept in-use image localhost/loomcli-p15-reset-loom:latest' "$LOOM_DISK_CLEANUP_LOG"
grep -q 'using connection podman-machine-default' "$LOOM_DISK_CLEANUP_LOG"
grep -q '^--connection podman-machine-default image ls' "$PODMAN_CALLS"
grep -q '^--connection podman-machine-default image rm localhost/loomgit-lab:old' "$PODMAN_CALLS"
if grep -q 'image rm .*loomcli-local-mode-loom:latest' "$PODMAN_CALLS"; then exit 1; fi
if grep -q 'image rm .*loomcli-p15-reset-loom-claude:latest' "$PODMAN_CALLS"; then exit 1; fi
if grep -q 'volume' "$PODMAN_CALLS"; then exit 1; fi

: > "$PODMAN_CALLS"
LOOM_DISK_FREE_KIB_OVERRIDE=$((50 * 1024 * 1024)) "$root/scripts/with-heavy-lock.sh" true
test ! -s "$PODMAN_CALLS"

cat > "$tmp/bin/stat" <<'SH'
#!/usr/bin/env bash
exit 91
SH
cat > "$tmp/bin/df" <<'SH'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf 'fake 50000000 10000000 31457280 25%% /\n'
SH
chmod +x "$tmp/bin/stat" "$tmp/bin/df"
python3 - "$tmp/cache/00/linux-a" <<'PY'
import os, sys, time
with open(sys.argv[1], "wb") as f:
    f.write(b"portable-size")
age = time.time() - 26 * 3600
os.utime(sys.argv[1], (age, age))
PY
"$root/scripts/with-heavy-lock.sh" true
test ! -e "$tmp/cache/00/linux-a"
grep -q '31457280 KiB free' "$LOOM_DISK_CLEANUP_LOG"

"$root/scripts/remove-local-mode-images.sh" podman loomcli-p15-reset \
    loomcli-p15-reset-loom:latest loomcli-p15-reset-fleet-db:latest
grep -q 'image rm loomcli-p15-reset-fleet-db:latest' "$PODMAN_CALLS"
if grep -q 'image rm loomcli-p15-reset-loom:latest' "$PODMAN_CALLS"; then exit 1; fi
: > "$PODMAN_CALLS"
"$root/scripts/remove-local-mode-images.sh" podman loomcli-local-mode \
    loomcli-local-mode-loom:latest
test ! -s "$PODMAN_CALLS"
echo 'disk budget fake tests passed'
