#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/provision-profile.sh <agent> [options]

Create verified Claude and Codex configuration profiles under:
  <workspace>/.loom/agent-profiles/<agent>/<harness>

Options:
  --workspace DIR       Workspace runtime directory (default: current directory)
  --claude-source DIR   Claude configuration source (default: CLAUDE_CONFIG_DIR or ~/.claude)
  --codex-source DIR    Codex configuration source (default: CODEX_HOME or ~/.codex)
  --force               Atomically replace an existing agent profile
  -h, --help            Show this help

Only stable configuration is copied and fingerprinted. Credential and session
files are deliberately excluded. Use scripts/setup-profile-token.sh separately
to give a Claude profile its own long-lived identity.
EOF
}

die() {
  printf 'provision-profile: %s\n' "$*" >&2
  exit 1
}

[[ $# -gt 0 ]] || { usage >&2; exit 2; }
agent="$1"
shift

workspace="${LOOM_WORKSPACE_RUNTIME_DIR:-$PWD}"
claude_source="${PROFILE_SOURCE_CLAUDE_DIR:-${CLAUDE_CONFIG_DIR:-${HOME}/.claude}}"
codex_source="${PROFILE_SOURCE_CODEX_DIR:-${CODEX_HOME:-${HOME}/.codex}}"
force=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --workspace)
      [[ $# -ge 2 ]] || die "--workspace requires a directory"
      workspace="$2"
      shift 2
      ;;
    --claude-source)
      [[ $# -ge 2 ]] || die "--claude-source requires a directory"
      claude_source="$2"
      shift 2
      ;;
    --codex-source)
      [[ $# -ge 2 ]] || die "--codex-source requires a directory"
      codex_source="$2"
      shift 2
      ;;
    --force)
      force=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ -n "$agent" && "$agent" != "." && "$agent" != ".." && "$agent" != */* && "$agent" != *\\* ]] ||
  die "agent must be one non-empty path segment"
[[ -d "$workspace" ]] || die "workspace directory does not exist: $workspace"
command -v python3 >/dev/null 2>&1 || die "python3 is required"

profiles_root="$workspace/.loom/agent-profiles"
target="$profiles_root/$agent"
mkdir -p "$profiles_root"
if [[ -e "$target" && "$force" -ne 1 ]]; then
  die "profile already exists: $target (rerun with --force to replace it)"
fi

stage="$(mktemp -d "$profiles_root/.${agent}.tmp.XXXXXX")"
backup=""
cleanup() {
  [[ ! -e "$stage" ]] || rm -rf "$stage"
  if [[ -n "$backup" && -e "$backup" && ! -e "$target" ]]; then
    mv "$backup" "$target"
  fi
}
trap cleanup EXIT

write_harness_profile() {
  local harness="$1" source="$2" binary="$3"
  shift 3
  [[ -d "$source" ]] || return 0

  local directory="$stage/$harness"
  local copied=()
  local rel
  for rel in "$@"; do
    if [[ -f "$source/$rel" ]]; then
      mkdir -p "$directory/$(dirname "$rel")"
      cp -p "$source/$rel" "$directory/$rel"
      copied+=("$rel")
    fi
  done
  (( ${#copied[@]} > 0 )) || return 0

  command -v "$binary" >/dev/null 2>&1 || die "$binary is required to pin the $harness profile version"
  local version
  version="$($binary --version | sed -n '1{s/^[[:space:]]*//;s/[[:space:]]*$//;p;q;}')"
  [[ -n "$version" ]] || die "$binary --version produced no version"

  python3 - "$directory" "$version" "${copied[@]}" <<'PY'
import hashlib
import json
import os
import pathlib
import sys
import tempfile

directory = pathlib.Path(sys.argv[1])
version = sys.argv[2]
files = list(sys.argv[3:])
digest = hashlib.sha256()
for rel in files:
    digest.update(rel.encode())
    digest.update(b"\0")
    digest.update((directory / rel).read_bytes())
manifest = {
    "files": files,
    "fingerprint": digest.hexdigest(),
    "harness_version": version,
}
fd, temporary = tempfile.mkstemp(prefix=".manifest.json.tmp-", dir=directory)
try:
    with os.fdopen(fd, "w") as stream:
        json.dump(manifest, stream, indent=1)
    os.chmod(temporary, 0o644)
    os.replace(temporary, directory / ".manifest.json")
except BaseException:
    try:
        os.unlink(temporary)
    except FileNotFoundError:
        pass
    raise
PY
}

write_harness_profile claude "$claude_source" claude CLAUDE.md settings.json
write_harness_profile codex "$codex_source" codex AGENTS.md config.toml
[[ -d "$stage/claude" || -d "$stage/codex" ]] ||
  die "no supported configuration files found in $claude_source or $codex_source"

if [[ -e "$target" ]]; then
  backup="$profiles_root/.${agent}.backup.$$"
  mv "$target" "$backup"
fi
mv "$stage" "$target"
stage=""
if [[ -n "$backup" ]]; then
  rm -rf "$backup"
  backup=""
fi
trap - EXIT

printf 'Provisioned profile %s at %s\n' "$agent" "$target"
find "$target" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | sed 's/^/  - /'
