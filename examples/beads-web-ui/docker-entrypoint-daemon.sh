#!/bin/sh
set -e

# Initialize beads database if not already done
if [ ! -f "$HOME/.beads/beads.db" ]; then
    echo "Initializing beads database..."
    # Use --skip-hooks since no git, and quiet mode to reduce output
    bd init --skip-hooks --skip-merge-driver -q || true
fi

# Start the daemon in foreground mode (local-only, no git sync)
exec bd daemon start --foreground --local
