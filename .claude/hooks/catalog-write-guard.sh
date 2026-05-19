#!/bin/bash
# PreToolUse hook for Edit / Write on catalog/*.yaml files.
#
# Reads the tool call from stdin (JSON) and:
#   - Silently passes if the target file is not under catalog/
#   - Silently passes if catalog/.ownership.yaml doesn't exist (no contract)
#   - Otherwise prints an advisory naming the owner agent to stderr
#
# The hook is ADVISORY — it never blocks. The catalog-curator audits
# post-hoc via git diff. To upgrade to hard enforcement, change the
# `exit 0` at the bottom to `exit 2` and the harness will block the call.

set -euo pipefail

# Read the tool call JSON from stdin into a variable.
input=$(cat || true)
if [ -z "${input}" ]; then
  exit 0
fi

# Find the repo root by walking up for a catalog/ directory or .git.
repo_root="$(pwd)"
while [ "${repo_root}" != "/" ] && [ ! -d "${repo_root}/catalog" ] && [ ! -d "${repo_root}/.git" ]; do
  repo_root="$(dirname "${repo_root}")"
done

ownership_file="${repo_root}/catalog/.ownership.yaml"
if [ ! -f "${ownership_file}" ]; then
  exit 0
fi

# Parse the tool name and file path. Prefer jq if available, fall back to grep.
if command -v jq >/dev/null 2>&1; then
  tool_name=$(printf '%s' "${input}" | jq -r '.tool_name // empty' 2>/dev/null || true)
  file_path=$(printf '%s' "${input}" | jq -r '.tool_input.file_path // empty' 2>/dev/null || true)
else
  tool_name=$(printf '%s' "${input}" | grep -o '"tool_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*:[[:space:]]*"\([^"]*\)"/\1/')
  file_path=$(printf '%s' "${input}" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*:[[:space:]]*"\([^"]*\)"/\1/')
fi

# Only act on Edit/Write to a catalog YAML file.
case "${tool_name}" in
  Edit|Write|NotebookEdit) ;;
  *) exit 0 ;;
esac

case "${file_path}" in
  */catalog/*.yaml|catalog/*.yaml|*/catalog/.ownership.yaml|catalog/.ownership.yaml) ;;
  *) exit 0 ;;
esac

# Look up the owner from .ownership.yaml. Match the basename, allow leading
# dot in the key (e.g. `.ownership.yaml`).
filename=$(basename "${file_path}")
owner=$(awk -v key="${filename}:" '
  /^files:/ { in_files=1; next }
  in_files && $1 == key { print $2; exit }
  in_files && $0 ~ /^"\.[^"]+":/ {
    gsub(/"/, "", $1)
    if ($1 == key) { print $2; exit }
  }
' "${ownership_file}")

if [ -z "${owner}" ]; then
  exit 0
fi

# Surface an advisory to stderr so the calling agent sees it in tool output.
{
  echo "[catalog-write-guard] Editing ${file_path}"
  echo "[catalog-write-guard] This file is owned by '${owner}'."
  echo "[catalog-write-guard] If you are not '${owner}', stop and delegate via the Agent tool."
  echo "[catalog-write-guard] If you are intentionally bypassing (e.g. catalog-curator migration), continue."
} >&2

# Advisory — never block.
exit 0
