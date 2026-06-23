#!/usr/bin/env bash
set -euo pipefail

# Build, sign (Developer ID), notarize, and staple the Loom Agents macOS desktop
# app, then wrap it in a notarized + stapled DMG ready to upload to a GitHub
# release.
#
# Local notarization uses an existing `notarytool` keychain profile (created via
# `xcrun notarytool store-credentials`), so no raw Apple credentials are handled
# here. CI uses env-var secrets + tauri-action instead (see
# .github/workflows/desktop-release.yml).
#
# Required env:
#   NOTARY_PROFILE     notarytool keychain profile name
# Optional env:
#   SIGNING_IDENTITY   Developer ID Application identity (default below)
#   APP_VERSION        overrides the version parsed from tauri.conf.json
#   SKIP_BUILD=1       reuse the existing signed .app (skip `npm run build`)
#
# Usage:
#   NOTARY_PROFILE=my-profile bash scripts/release-macos.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DESKTOP_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

SIGNING_IDENTITY="${SIGNING_IDENTITY:-Developer ID Application: Tyson Kuthur Thomas (BN879H59CY)}"
APP_NAME="Loom Agents"
APP_BUNDLE="${DESKTOP_DIR}/src-tauri/target/release/bundle/macos/${APP_NAME}.app"
RELEASE_DIR="${DESKTOP_DIR}/dist-release"

log() { echo "[release] $*"; }
die() { echo "[release] error: $*" >&2; exit 1; }

if [[ -z "${NOTARY_PROFILE:-}" ]]; then
  die "NOTARY_PROFILE is required (your 'notarytool store-credentials' profile name)"
fi

# Confirm the signing identity exists before doing any expensive work.
if ! security find-identity -v -p codesigning | grep -qF "${SIGNING_IDENTITY}"; then
  die "signing identity not found in keychain: ${SIGNING_IDENTITY}"
fi

APP_VERSION="${APP_VERSION:-$(node -p "require('${DESKTOP_DIR}/src-tauri/tauri.conf.json').version" 2>/dev/null || echo "0.0.0")}"
OUT_DMG="${RELEASE_DIR}/Loom-Agents-${APP_VERSION}-aarch64.dmg"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

stop_running_app() {
  # The bundle we're about to overwrite may be running from this exact path.
  if [[ -d "${APP_BUNDLE}" ]]; then
    log "stopping any running ${APP_NAME}.app before rebuild"
    osascript -e "tell application \"${APP_NAME}\" to quit" >/dev/null 2>&1 || true
    sleep 2
    for name in loom fleet-db loom-desktop; do
      pkill -TERM -f "${APP_BUNDLE}/Contents/MacOS/${name}" >/dev/null 2>&1 || true
    done
    sleep 1
    for name in loom fleet-db loom-desktop; do
      pkill -KILL -f "${APP_BUNDLE}/Contents/MacOS/${name}" >/dev/null 2>&1 || true
    done
  fi
}

# Submit an artifact to the notary service and wait for the verdict; on failure,
# dump the notary log so we can see exactly which binary was rejected. Does NOT
# staple — the submitted artifact (e.g. a .zip of the app) is often not the thing
# you staple (you staple the .app/.dmg). Callers staple explicitly afterwards.
notarize() {
  local artifact="$1"
  local submit_out
  submit_out="$(mktemp)"
  log "submitting $(basename "${artifact}") to the notary service (this can take several minutes)"
  if xcrun notarytool submit "${artifact}" \
      --keychain-profile "${NOTARY_PROFILE}" \
      --wait 2>&1 | tee "${submit_out}"; then
    if grep -q "status: Accepted" "${submit_out}"; then
      rm -f "${submit_out}"
      return 0
    fi
  fi
  local sub_id
  sub_id="$(grep -Eo 'id: [0-9a-f-]{36}' "${submit_out}" | head -1 | awk '{print $2}')"
  if [[ -n "${sub_id}" ]]; then
    log "notarization did not succeed; fetching log for submission ${sub_id}" >&2
    xcrun notarytool log "${sub_id}" --keychain-profile "${NOTARY_PROFILE}" >&2 || true
  fi
  rm -f "${submit_out}"
  die "notarization failed for ${artifact}"
}

# ---------------------------------------------------------------------------
# 1. Build the signed .app
# ---------------------------------------------------------------------------

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  stop_running_app
  log "building signed app bundle with identity: ${SIGNING_IDENTITY}"
  (
    cd "${DESKTOP_DIR}"
    # APPLE_SIGNING_IDENTITY makes Tauri codesign the app + nested sidecars with
    # hardened runtime. No notary env vars -> Tauri signs but skips notarization
    # (we do that ourselves below, against the keychain profile).
    APPLE_SIGNING_IDENTITY="${SIGNING_IDENTITY}" npm run build
  )
else
  log "SKIP_BUILD=1 -> reusing existing ${APP_BUNDLE}"
fi

[[ -d "${APP_BUNDLE}" ]] || die "app bundle not found at ${APP_BUNDLE}"

# ---------------------------------------------------------------------------
# 2. Defensive re-sign: guarantee hardened runtime + timestamp on every
#    nested binary, then re-seal the bundle. (No-op if Tauri already did it.)
# ---------------------------------------------------------------------------

log "re-signing nested binaries with hardened runtime"
for nested in fleet-db loom loom-desktop; do
  bin="${APP_BUNDLE}/Contents/MacOS/${nested}"
  [[ -f "${bin}" ]] || continue
  codesign --force --options runtime --timestamp \
    --sign "${SIGNING_IDENTITY}" "${bin}"
done
log "re-sealing app bundle"
codesign --force --options runtime --timestamp \
  --sign "${SIGNING_IDENTITY}" "${APP_BUNDLE}"
codesign --verify --deep --strict --verbose=2 "${APP_BUNDLE}"

# ---------------------------------------------------------------------------
# 3. Notarize + staple the .app (so the app is offline-valid inside the DMG)
# ---------------------------------------------------------------------------

APP_ZIP="$(mktemp -d)/loom-agents.zip"
log "zipping app for notarization"
ditto -c -k --keepParent "${APP_BUNDLE}" "${APP_ZIP}"
notarize "${APP_ZIP}"                # notarize via the zip...
log "stapling ${APP_NAME}.app"
xcrun stapler staple "${APP_BUNDLE}" # ...then staple the actual .app bundle
rm -f "${APP_ZIP}"

# ---------------------------------------------------------------------------
# 4. Build the DMG from the stapled .app, then sign it
# ---------------------------------------------------------------------------

mkdir -p "${RELEASE_DIR}"
rm -f "${OUT_DMG}"
if command -v create-dmg >/dev/null 2>&1; then
  log "building DMG with create-dmg (drag-to-Applications layout)"
  create-dmg \
    --volname "${APP_NAME}" \
    --app-drop-link 480 170 \
    --icon "${APP_NAME}.app" 160 170 \
    --window-size 660 360 \
    "${OUT_DMG}" "${APP_BUNDLE}" || die "create-dmg failed"
else
  log "create-dmg not found; building drag-install DMG with hdiutil (+ /Applications symlink)"
  # Stage the app next to an /Applications symlink so the DMG nudges users to
  # install into /Applications. This matters: a quarantined app launched from a
  # disk image or Downloads is run via App Translocation (read-only randomized
  # path), where the bundled loom sidecar cannot start. (brew install create-dmg
  # for a prettier drag-to-Applications layout.)
  DMG_PARENT="$(mktemp -d)"
  DMG_STAGE="${DMG_PARENT}/stage"
  mkdir -p "${DMG_STAGE}"
  cp -R "${APP_BUNDLE}" "${DMG_STAGE}/"
  ln -s /Applications "${DMG_STAGE}/Applications"
  hdiutil create -volname "${APP_NAME}" -srcfolder "${DMG_STAGE}" \
    -ov -format UDZO "${OUT_DMG}"
  rm -rf "${DMG_PARENT}"
fi
log "signing DMG"
codesign --force --timestamp --sign "${SIGNING_IDENTITY}" "${OUT_DMG}"

# ---------------------------------------------------------------------------
# 5. Notarize + staple the DMG
# ---------------------------------------------------------------------------

notarize "${OUT_DMG}"
log "stapling $(basename "${OUT_DMG}")"
xcrun stapler staple "${OUT_DMG}"

# ---------------------------------------------------------------------------
# 6. Verify (hard-fail on any problem)
# ---------------------------------------------------------------------------

log "verifying signed/notarized artifacts"
xcrun stapler validate "${OUT_DMG}"
spctl -a -t open --context context:primary-signature -vvv "${OUT_DMG}"
codesign --verify --deep --strict --verbose=2 "${APP_BUNDLE}"

# ---------------------------------------------------------------------------
# 7. Done — print upload command
# ---------------------------------------------------------------------------

log "success: ${OUT_DMG}"
cat <<EOF

Signed, notarized, and stapled:
  ${OUT_DMG}

Upload to a GitHub release with:
  gh release create desktop-v${APP_VERSION} \\
    "${OUT_DMG}" \\
    --repo tysonthomas9/loomcli \\
    --title "Loom Agents ${APP_VERSION} (Apple Silicon)" \\
    --notes "Signed + notarized macOS desktop app. Apple Silicon (arm64)."
EOF
