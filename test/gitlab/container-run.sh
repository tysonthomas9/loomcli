#!/bin/sh
set -eu
mkdir /work
tar -C /src --exclude=.git --exclude=.intent -cf - . | tar -C /work -xf -
cd /work
for version in min latest; do
  export PATH="/opt/git-$version/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
  export GITLAB_GIT_VERSION="$version"
  export GITLAB_SOURCE_ARCHIVE=/lab/loomcli.tar
  export CGO_ENABLED=0
  printf '\nGit lab %s: %s\n' "$version" "$(git --version)"
  go test -v -tags gitlab_real -count=1 -timeout=20m ./internal/loomgit/gitlab
done
