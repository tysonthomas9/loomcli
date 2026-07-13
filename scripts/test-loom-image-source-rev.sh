#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER="$SCRIPT_DIR/loom-image-source-rev.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

repo="$TMP/repo"
mkdir -p "$repo/cmd" "$repo/internal"
git -C "$repo" init -q
git -C "$repo" config user.name test
git -C "$repo" config user.email test@example.invalid
printf 'module example.invalid/test\n' >"$repo/go.mod"
printf 'FROM scratch\n' >"$repo/Dockerfile.dev"
printf 'package main\n' >"$repo/cmd/main.go"
git -C "$repo" add .
git -C "$repo" commit -qm initial

head="$(git -C "$repo" rev-parse HEAD)"
clean="$($HELPER "$repo")"
[[ "$clean" == "$head" ]] || { printf 'clean identity mismatch\n' >&2; exit 1; }

printf 'outside build context\n' >"$repo/README.md"
outside="$($HELPER "$repo")"
[[ "$outside" == "$clean" ]] || { printf 'outside change altered identity\n' >&2; exit 1; }

printf '// first dirty state\n' >>"$repo/cmd/main.go"
dirty_one="$($HELPER "$repo")"
[[ "$dirty_one" == "$head-dirty-"* ]] || { printf 'dirty identity missing fingerprint\n' >&2; exit 1; }

printf '// second dirty state\n' >>"$repo/cmd/main.go"
dirty_two="$($HELPER "$repo")"
[[ "$dirty_two" != "$dirty_one" ]] || { printf 'different dirty states shared identity\n' >&2; exit 1; }

git -C "$repo" checkout -q -- cmd/main.go
printf 'package extra\n' >"$repo/internal/extra.go"
untracked_one="$($HELPER "$repo")"
printf 'package changed\n' >"$repo/internal/extra.go"
untracked_two="$($HELPER "$repo")"
[[ "$untracked_one" != "$untracked_two" ]] || { printf 'untracked content did not alter identity\n' >&2; exit 1; }

printf 'loom image source revision tests passed\n'
