#!/usr/bin/env bash
# sync-beads.sh - Sync beads library packages from third_party/beads/internal/ to internal/
# with import path rewriting from beads module to loomcli module.
#
# Usage: ./scripts/sync-beads.sh
#
# Prerequisites: third_party/beads/ must exist (set up via git subtree, see T5)
#
# Package mapping:
#   third_party/beads/internal/rpc/      → internal/rpc/      (client files only)
#   third_party/beads/internal/types/    → internal/types/
#   third_party/beads/internal/debug/    → internal/debug/
#   third_party/beads/internal/lockfile/ → internal/lockfile/
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

VENDOR_DIR="$REPO_ROOT/third_party/beads/internal"
DEST_DIR="$REPO_ROOT/internal"

# Import path rewrite
OLD_MODULE="github.com/steveyegge/beads/internal"
NEW_MODULE="github.com/tysonthomas9/loomcli/internal"

# Patches directory (populated by T7)
PATCHES_DIR="$SCRIPT_DIR/post-sync-patches"

# Track synced files for summary
SYNCED_FILES=()
ERRORS=()

# Platform-aware sed in-place
sed_inplace() {
    if [[ "$(uname)" == "Darwin" ]]; then
        sed -i '' "$@"
    else
        sed -i "$@"
    fi
}

# --- Validation ---

if [[ ! -d "$VENDOR_DIR" ]]; then
    echo "ERROR: $VENDOR_DIR does not exist."
    echo "Run the git subtree setup first (T5) to populate third_party/beads/."
    exit 1
fi

# --- Sync functions ---

# Copy a single file and track it
copy_file() {
    local src="$1"
    local dest_dir="$2"
    local filename
    filename="$(basename "$src")"

    mkdir -p "$dest_dir"
    cp "$src" "$dest_dir/$filename"
    SYNCED_FILES+=("$dest_dir/$filename")
}

# Sync rpc/ (client files only, skip server/signals/test_helpers/tests)
sync_rpc() {
    local src_dir="$VENDOR_DIR/rpc"
    local dest_dir="$DEST_DIR/rpc"

    if [[ ! -d "$src_dir" ]]; then
        ERRORS+=("WARNING: $src_dir does not exist, skipping rpc sync")
        return
    fi

    echo "Syncing rpc/ (client files only)..."
    local count=0
    for f in "$src_dir"/*.go; do
        [[ ! -f "$f" ]] && continue
        local basename
        basename="$(basename "$f")"

        # Skip server files
        case "$basename" in
            server*.go)      continue ;;
            signals_*.go)    continue ;;
            test_helpers*.go) continue ;;
            *_test.go)       continue ;;
        esac

        copy_file "$f" "$dest_dir"
        count=$((count + 1))
    done
    echo "  Synced $count client files from rpc/"
}

# Sync a full package (all .go files including tests)
sync_package() {
    local pkg="$1"
    local src_dir="$VENDOR_DIR/$pkg"
    local dest_dir="$DEST_DIR/$pkg"

    if [[ ! -d "$src_dir" ]]; then
        ERRORS+=("WARNING: $src_dir does not exist, skipping $pkg sync")
        return
    fi

    echo "Syncing $pkg/..."
    local count=0
    for f in "$src_dir"/*.go; do
        [[ ! -f "$f" ]] && continue
        copy_file "$f" "$dest_dir"
        count=$((count + 1))
    done
    echo "  Synced $count files from $pkg/"
}

# --- Main sync ---

echo "=== Beads Library Sync ==="
echo "Source: $VENDOR_DIR"
echo "Destination: $DEST_DIR"
echo ""

sync_rpc
sync_package "types"
sync_package "debug"
sync_package "lockfile"

# --- Rewrite imports ---

echo ""
echo "Rewriting imports: $OLD_MODULE → $NEW_MODULE"
rewrite_count=0
for f in "${SYNCED_FILES[@]}"; do
    if grep -q "$OLD_MODULE" "$f" 2>/dev/null; then
        sed_inplace "s|$OLD_MODULE|$NEW_MODULE|g" "$f"
        rewrite_count=$((rewrite_count + 1))
    fi
done
echo "  Rewrote imports in $rewrite_count files"

# --- Apply post-sync patches ---

if [[ -d "$PATCHES_DIR" ]]; then
    echo ""
    echo "Applying post-sync patches..."
    patch_count=0
    patch_errors=0
    for patch in "$PATCHES_DIR"/*.patch; do
        [[ ! -f "$patch" ]] && continue
        patch_name="$(basename "$patch")"
        # Check if patch is already applied (reverse-apply check succeeds)
        if git -C "$REPO_ROOT" apply --reverse --check "$patch" 2>/dev/null; then
            echo "  Already applied: $patch_name"
            patch_count=$((patch_count + 1))
        elif git -C "$REPO_ROOT" apply "$patch" 2>/dev/null; then
            echo "  Applied: $patch_name"
            patch_count=$((patch_count + 1))
        else
            echo "  FAILED: $patch_name"
            ERRORS+=("Patch failed to apply: $patch_name")
            patch_errors=$((patch_errors + 1))
        fi
    done
    echo "  Applied $patch_count patches ($patch_errors failures)"
else
    echo ""
    echo "Note: No post-sync patches directory found at $PATCHES_DIR"
    echo "  If go build fails, patches from T7 may be needed."
fi

# --- Build verification ---

echo ""
echo "Verifying build..."
cd "$REPO_ROOT"
if go build ./...; then
    echo "  Build succeeded!"
else
    echo ""
    echo "ERROR: go build failed."
    if [[ ! -d "$PATCHES_DIR" ]]; then
        echo "  Post-sync patches may be needed (see T7)."
    fi
    # Print warnings/errors collected during sync
    if [[ ${#ERRORS[@]} -gt 0 ]]; then
        echo ""
        echo "Issues encountered during sync:"
        for err in "${ERRORS[@]}"; do
            echo "  - $err"
        done
    fi
    exit 1
fi

# --- Summary ---

echo ""
echo "=== Sync Complete ==="
echo "  Total files synced: ${#SYNCED_FILES[@]}"
if [[ ${#ERRORS[@]} -gt 0 ]]; then
    echo ""
    echo "Warnings:"
    for err in "${ERRORS[@]}"; do
        echo "  - $err"
    done
fi
