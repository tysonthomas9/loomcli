#!/usr/bin/env bash
set -euo pipefail

APP_BUNDLE="${1:-}"
if [[ -z "${APP_BUNDLE}" ]]; then
  echo "usage: $0 <app-bundle>" >&2
  exit 2
fi
if [[ "$(uname -s)" != "Darwin" ]]; then
  exit 0
fi
if ! command -v codesign >/dev/null 2>&1; then
  echo "codesign is required to seal the packaged macOS app" >&2
  exit 127
fi
if [[ ! -d "${APP_BUNDLE}" ]]; then
  echo "app bundle not found at ${APP_BUNDLE}" >&2
  exit 1
fi

# Tauri's local ad-hoc build may leave only its main Mach-O linker-signed.
# Re-seal the outer bundle after resources and sidecars are populated so a
# local refresh has the same strict resource-integrity guarantee as release.
SIGNING_IDENTITY="${APPLE_SIGNING_IDENTITY:--}"
SIGN_ARGS=(--force)
if [[ "${SIGNING_IDENTITY}" != "-" ]]; then
  SIGN_ARGS+=(--options runtime --timestamp)
fi
SIGN_ARGS+=(--sign "${SIGNING_IDENTITY}")

echo "[desktop] sealing app bundle"
codesign "${SIGN_ARGS[@]}" "${APP_BUNDLE}"
codesign --verify --deep --strict --verbose=2 "${APP_BUNDLE}"

NODE_RUNTIME="${APP_BUNDLE}/Contents/Resources/runtime/node"
if [[ -x "${NODE_RUNTIME}" ]]; then
  codesign --verify --strict --verbose=2 "${NODE_RUNTIME}"
  if ! codesign -d --entitlements - "${NODE_RUNTIME}" 2>&1 | grep -q 'com.apple.security.cs.allow-jit'; then
    echo "packaged Node.js runtime is missing its JIT entitlement" >&2
    exit 1
  fi
fi
