#!/usr/bin/env bash
# Opt-in private Claude login for LOCAL_MODE_AGENTS_REAL=1 Agent API stacks
# (LOCAL_MODE_CLAUDE_COPY=1).
#
#   real-claude-copy.sh make <copy>    before `up`
#   real-claude-copy.sh remove <copy>  after `down`
#
# `make` reads the host's current Claude Code login (on macOS the Keychain item
# "Claude Code-credentials", where the live login is kept; otherwise, or when
# that item is missing, ~/.claude/.credentials.json) and writes it to <copy>, a
# mode-600 file in a mode-700 folder owned by one compose project, keeping
# only the Claude login (claudeAiOauth) with its OAuth refresh token REMOVED. The stack mounts only <copy>, read-only, so a
# container can never refresh the login: refresh tokens rotate, and a refresh
# inside a stack would sign the host's own claude out. The stack uses the
# current access token until it expires; then run `claude` on the host and
# down/up the stack. A login with no access token, or one expiring within
# LOCAL_MODE_CLAUDE_MIN_MINUTES (default 30), refuses the boot.
# A re-up of the same project replaces the copy with a fresh one.
#
# `remove` deletes <copy> and its folder (that project only).
#
# Prints paths only, never credential contents.
# Knob: LOCAL_MODE_CLAUDE_SOURCE (a credentials file to read instead).
set -euo pipefail

usage() { echo "usage: real-claude-copy.sh make|remove <copy path>" >&2; exit 2; }
[ "$#" -eq 2 ] || usage
cmd="$1" copy="$2"
case "$copy" in /*) ;; *) echo "local-mode: the Claude copy path must be absolute: $copy" >&2; exit 2 ;; esac
case "/$copy/" in */../*|*/./*) echo "local-mode: the Claude copy path must not contain . or .. parts: $copy" >&2; exit 2 ;; esac
dir="$(dirname "$copy")"
hostdir="${HOME}/.claude"

canon() {
  local p="$1" rest=""
  while [ ! -d "$p" ]; do
    rest="/$(basename "$p")$rest"
    p="$(dirname "$p")"
  done
  printf '%s%s\n' "$(cd -P -- "$p" && pwd -P)" "$rest"
}
# The copy must never land in (or under) the host Claude folder, through any
# alias or symlink, and must not itself be a symlink.
outside_host() {
  local d h
  d="$(canon "$dir")/" h="$(canon "$hostdir")/"
  case "$d" in "$h"*) echo "local-mode: the Claude copy must live outside $hostdir" >&2; exit 2 ;; esac
  if [ -L "$copy" ] || { [ -e "$copy" ] && [ ! -f "$copy" ]; }; then
    echo "local-mode: $copy is not a regular file; remove it by hand" >&2; exit 2
  fi
}
outside_host

case "$cmd" in
  remove)
    rm -f -- "$copy" "$copy.tmp"
    rmdir -- "$dir" 2>/dev/null || true
    rmdir -- "$(dirname "$dir")" 2>/dev/null || true
    exit 0
    ;;
  make) ;;
  *) usage ;;
esac

command -v jq >/dev/null 2>&1 \
  || { echo "local-mode: jq is needed to make the private Claude copy" >&2; exit 1; }

# Emit the host login on stdout (piped straight into jq, never shown).
source_login() {
  if [ -n "${LOCAL_MODE_CLAUDE_SOURCE:-}" ]; then
    cat -- "$LOCAL_MODE_CLAUDE_SOURCE"
  elif command -v security >/dev/null 2>&1 \
    && security find-generic-password -s "Claude Code-credentials" >/dev/null 2>&1; then
    security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null
  else
    cat -- "$hostdir/.credentials.json"
  fi
}

(umask 077; mkdir -p "$dir")
chmod 700 "$dir"
outside_host
tmp="$copy.tmp"
fail() {
  rm -f -- "$tmp"
  rmdir -- "$dir" 2>/dev/null || true
  echo "local-mode: $1; not starting the REAL stack" >&2
  exit 1
}
rm -f -- "$tmp"
# Any exit before the copy is published (a failed chmod or mv included)
# removes the credential-bearing temp file.
trap 'rm -f -- "$tmp"' EXIT

min="${LOCAL_MODE_CLAUDE_MIN_MINUTES:-30}"
case "$min" in ''|*[!0-9]*) fail "LOCAL_MODE_CLAUDE_MIN_MINUTES must be a whole number" ;; esac
# Bounded (at most 4 digits, i.e. under a week) so the cutoff cannot overflow.
[ "${#min}" -le 4 ] || fail "LOCAL_MODE_CLAUDE_MIN_MINUTES must be at most 9999"
min=$((10#$min))
until_ms=$(( ($(date +%s) + min * 60) * 1000 ))

# One silent transform: keep only claudeAiOauth, without its refresh token
# (other entries, such as MCP servers' OAuth tokens, are dropped). jq -e
# fails (without output) unless the access token is present and still valid.
( umask 077
  set -o pipefail
  source_login 2>/dev/null | jq -e --argjson until "$until_ms" '
    if (.claudeAiOauth.accessToken | type) == "string"
       and (.claudeAiOauth.accessToken | length) > 0
       and ((.claudeAiOauth.expiresAt // 0) | tonumber? // 0) > $until
    then {claudeAiOauth: (.claudeAiOauth | del(.refreshToken))} else error("unusable") end' \
    > "$tmp" 2>/dev/null
) || fail "the host Claude login is missing, unreadable, or expires within ${min} minutes; run \`claude\` on the host to refresh it"
jq -e '.claudeAiOauth | has("refreshToken") | not' "$tmp" >/dev/null 2>&1 \
  || fail "could not remove the refresh token from the copy"
chmod 600 "$tmp"
mv -f -- "$tmp" "$copy"
trap - EXIT
echo "local-mode: copied the Claude login (no refresh token) to $copy (private to this project)"
