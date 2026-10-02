#!/usr/bin/env bash
# Copy the Slack-clone fixture into a fresh git repo with one initial commit.
# Usage: scripts/seed-slack-clone.sh <dest-dir>   (dest must not exist or be empty)
set -euo pipefail

dest="${1:?usage: seed-slack-clone.sh <dest-dir>}"
src="$(cd "$(dirname "${BASH_SOURCE[0]}")/../tests/fixtures/slack-clone" && pwd)"

if [[ -e "$dest" && -n "$(ls -A "$dest")" ]]; then
    echo "seed-slack-clone: $dest is not empty" >&2
    exit 1
fi
mkdir -p "$dest"
cp -R "$src/." "$dest/"
rm -f "$dest/data.json"
git -C "$dest" init -q -b main
git -C "$dest" add -A
git -C "$dest" -c user.name="Loom Fixture" -c user.email="fixture@loom.invalid" commit -q -m "Initial Slack clone"
echo "$dest"
