#!/usr/bin/env bash
set -Eeuo pipefail

# Retired in the modular-monolith migration. This test used to clone and push
# by placing a hosting credential in runner process state. Repository checkout
# and pull-request delivery now belong to the SourceControl application
# boundary, backed by connector-owned opaque authorization.

echo "ERROR: direct runner PR delivery is retired" >&2
echo "Use the SourceControl checkout and pull-request proof with a configured connector." >&2
exit 2
