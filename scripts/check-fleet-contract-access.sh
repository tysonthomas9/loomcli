#!/usr/bin/env bash
set -euo pipefail

# Check availability only. Checkout verifies the credential's read access.
# Never print the token or enable shell tracing here.
if [[ -z "${FLEET_DB_REPO_TOKEN:-}" ]]; then
  echo "::error title=FleetDB contract verification blocked::FLEET_DB_REPO_TOKEN is required to read BrowserOperator/fleet-db. Pinned producer and upstream drift checks did not execute."
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    echo "FleetDB contract verification BLOCKED: repository read token is unavailable." >> "$GITHUB_STEP_SUMMARY"
  fi
  exit 1
fi
