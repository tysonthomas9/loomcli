#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd -P)
cd "$repo"
podman ps >/dev/null
if [ "$(uname -s)" = Darwin ]; then
  df -g /System/Volumes/Data
  available_kib=$(df -k /System/Volumes/Data | awk 'NR == 2 { print $4 }')
else
  df -BG .
  available_kib=$(df -k . | awk 'NR == 2 { print $4 }')
fi

run="$(date -u +%Y%m%d%H%M%S)-$$"
name="loomgit-lab-$run"
digest=$(shasum -a 256 test/gitlab/Containerfile test/gitlab/container-run.sh go.mod go.sum | shasum -a 256 | cut -c1-12)
image="loomgit-lab:$digest"
if ! podman image exists "$image"; then
  if [ "$available_kib" -lt 10485760 ]; then
    echo "Git lab image build needs at least 10 GiB free; found $((available_kib / 1048576)) GiB" >&2
    exit 1
  fi
  podman build -f test/gitlab/Containerfile -t "$image" .
fi

scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT HUP INT TERM
pin=31bacba3333e461e55bfee4b5d691dc6df3669e6
git archive --format=tar --output="$scratch/loomcli.tar" "$pin"
podman run --rm --name "$name" --network=none \
  --tmpfs /small:size=1m,mode=1777 \
  -v "$repo:/src:ro" -v "$scratch:/lab:ro" -w /src \
  -e GOFLAGS=-buildvcs=false -e SCENARIO="${SCENARIO:-*}" -e TASK="${TASK:-}" \
  "$image"
