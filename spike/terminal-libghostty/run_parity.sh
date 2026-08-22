#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export GOCACHE="$ROOT/.go-cache"
export ZIG_GLOBAL_CACHE_DIR="$ROOT/.zig-cache/global"
export ZIG_LOCAL_CACHE_DIR="$ROOT/.zig-cache/local"
mkdir -p "$ROOT/.go-cache" "$ROOT/.zig-cache/global" "$ROOT/.zig-cache/local" "$ROOT/build"
BIN="$ROOT/build/snapshot"
(cd "$ROOT" && go build -o "$BIN" ./snapshot.go)
TMP="$ROOT/build/corpus"; mkdir -p "$TMP"
printf 'user@host:~$ ls --color=auto\r\n\033[32mbin\033[0m  file.txt\r\n' > "$TMP/prompt.bin"
printf 'before\r\n\033[?1049h\033[2J\033[Hvim-like\r\n\033[?25l\033[?1049lafter\r\n' > "$TMP/alt.bin"
printf '\033[1;31;48;5;25mBold red\033[22;39;49m normal\r\n' > "$TMP/sgr.bin"
printf 'hidden\033[?25l\r\nTUI\033[?25h\r\n' > "$TMP/cursor.bin"
printf 'CSI: \033[31;2mred\033[0m UTF8: €\r\n' > "$TMP/splits.bin"

fail=0
for input in "$TMP"/*.bin; do
  case=$(basename "$input" .bin)
  size=$(wc -c < "$input" | tr -d ' ')
  for cut in $(seq 0 "$size"); do
    snap="$TMP/$case.$cut.vt"
    "$BIN" -input "$input" -cut "$cut" -out "$snap" -cols 80 -rows 24
    if ! (cd "$ROOT" && node oracle.mjs --stream "$input" --snapshot "$snap" --cut "$cut" --cols 80 --rows 24) >/dev/null; then
      echo "FAIL case=$case cut=$cut" >&2; fail=1
    fi
  done
done
if [ "$fail" -ne 0 ]; then exit 1; fi
echo "PASS all corpus cuts"
