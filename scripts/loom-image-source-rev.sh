#!/usr/bin/env bash
set -euo pipefail

# Print a stable identity for the source inputs baked by Dockerfile.dev.
# Clean trees use the commit SHA. Dirty trees add a content fingerprint so two
# different worktrees at the same commit can never share an image identity.

ROOT_DIR="${1:-$(git rev-parse --show-toplevel)}"
BUILD_PATHS=(cmd internal go.mod go.sum Dockerfile.dev)

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
  else
    printf 'loom image source identity requires sha256sum or shasum\n' >&2
    return 1
  fi
}

rev="$(git -C "$ROOT_DIR" rev-parse HEAD)"
if [[ -z "$(git -C "$ROOT_DIR" status --porcelain -- "${BUILD_PATHS[@]}")" ]]; then
  printf '%s\n' "$rev"
  exit 0
fi

fingerprint="$({
  # HEAD-relative binary diff covers staged and unstaged tracked changes.
  git -C "$ROOT_DIR" diff --binary HEAD -- "${BUILD_PATHS[@]}"

  # Git emits untracked paths in stable index order. Include both each path and
  # its blob hash so additions with equal contents but different names differ.
  while IFS= read -r -d '' path; do
    printf 'untracked:%s\0' "$path"
    git -C "$ROOT_DIR" hash-object -- "$path"
  done < <(git -C "$ROOT_DIR" ls-files --others --exclude-standard -z -- "${BUILD_PATHS[@]}")
} | sha256_stream)"

printf '%s-dirty-%s\n' "$rev" "${fingerprint:0:16}"
