#!/usr/bin/env bash
# Shared staging for the builtin workflow .ts files. The workflow sources import
# the bare specifiers @loom/sdk, @flue/runtime and @daytona/sdk; both the bundle
# build (rebuild-builtin-bundle.sh) and the node unit tests
# (test-builtin-workflows.sh) need them symlinked into a STAGE/node_modules so
# node resolves them identically. This is the one place that wiring lives.
#
# SOURCED, not executed. Callers must already have set ROOT and FLUE_REPO and
# created the STAGE dir; they write their own package.json + copy sources after
# calling stage_builtin_node_modules (the two callers differ there).

# stage_daytona_sdk <STAGE>
# Symlink @daytona/sdk into STAGE/node_modules, resolved to its real directory.
# @daytona/sdk is a transitive dependency, so it lives only in flue's pnpm flat
# virtual store (node_modules/.pnpm/node_modules) — not at the repo-root
# node_modules — so point require.resolve there. require.resolve (not a bare
# `[ -d ]`) confirms the package is actually loadable. Fail loud: the daytona
# runner statically imports the SDK, so a missing one is an environment bug,
# never a silent skip.
stage_daytona_sdk() {
  local stage="$1" sdk
  sdk="$(node -e 'process.stdout.write(require("path").dirname(require.resolve("@daytona/sdk/package.json",{paths:[process.argv[1]]})))' \
        "$FLUE_REPO/node_modules/.pnpm/node_modules")" || {
    echo "ERROR: @daytona/sdk not resolvable from $FLUE_REPO (run 'pnpm install' in flue)" >&2
    return 1
  }
  ln -s "$sdk" "$stage/node_modules/@daytona/sdk"
}

# stage_builtin_node_modules <STAGE>
# Create STAGE/node_modules and symlink the three bare specifiers the builtin
# workflow .ts files import.
stage_builtin_node_modules() {
  local stage="$1"
  mkdir -p "$stage/node_modules/@loom" "$stage/node_modules/@flue" "$stage/node_modules/@daytona"
  ln -s "$ROOT/sdk" "$stage/node_modules/@loom/sdk"
  ln -s "$FLUE_REPO/packages/runtime" "$stage/node_modules/@flue/runtime"
  stage_daytona_sdk "$stage"
}
