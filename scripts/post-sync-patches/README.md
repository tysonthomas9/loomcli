# Post-Sync Patches

Patches in this directory transform beads library files into loom-specific versions
after `sync-beads.sh` copies and rewrites import paths.

## How It Works

1. `sync-beads.sh` copies files from `third_party/beads/internal/` to `internal/`
2. Import paths are rewritten from `github.com/steveyegge/beads/internal/` to `github.com/tysonthomas9/loomcli/internal/`
3. Patches in this directory are applied via `git apply` to make loom-specific changes

## Naming Convention

Patches use numeric prefixes for deterministic ordering: `NNN-description.patch`

## Current Patches

- **001-rpc-client-thread-safety.patch** — Adds `sync.RWMutex` to `Client` struct for
  thread-safe `SetTimeout`/`SetDatabasePath`/`SetActor`, and introduces `executeWithTimeout`
  to replace the thread-unsafe timeout mutation in `WaitForMutations`.

- **002-rpc-socket-path-inline-normalize.patch** — Replaces `internal/utils` import with
  an inlined `normalizePathForComparison` function, eliminating the external dependency.

## Regenerating Patches

When upstream beads changes break a patch:

1. Run `sync-beads.sh` with patches temporarily removed (or comment out the patch loop)
2. The files in `internal/rpc/` will be in the post-import-rewrite state (no loom patches)
3. Manually apply the loom-specific changes to the affected files
4. Generate new patches:
   ```bash
   git diff internal/rpc/client.go > scripts/post-sync-patches/001-rpc-client-thread-safety.patch
   git diff internal/rpc/socket_path.go > scripts/post-sync-patches/002-rpc-socket-path-inline-normalize.patch
   ```
5. Verify by running `sync-beads.sh` end-to-end
