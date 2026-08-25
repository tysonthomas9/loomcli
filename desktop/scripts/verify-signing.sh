#!/usr/bin/env bash
set -euo pipefail

# DEV-V5-38 SIG-* checks — assert the "Loom Agents" bundle's signature is
# notarization-ready, with the embedded Node entitled for V8's JIT.
#
# Two ways to use it:
#   • sourced   — defines verify_node_entitlements / verify_app_signing for
#                 release-macos.sh (and the Slice 7 acceptance harness).
#   • executed  — `verify-signing.sh <path-to-.app>` runs the full app check.
#
# Kept dependency-free (only codesign/spctl) and side-effect-free so it can run
# in an acceptance harness against any built bundle.

_sig_log() { echo "[verify-signing] $*"; }
_sig_die() { echo "[verify-signing] error: $*" >&2; exit 1; }

# verify_node_entitlements <path-to-node-macho>
# The notary rejects a hardened-runtime binary that JITs unless it carries
# allow-jit, and rejects any Developer-ID binary that still carries
# get-task-allow (nodejs.org ships it). Assert both, plus that the hardened
# runtime is actually on. Substring matches so we don't depend on the exact key
# namespace (allow-jit is com.apple.security.cs.allow-jit; get-task-allow is
# com.apple.security.get-task-allow).
verify_node_entitlements() {
  local node_bin="$1"
  [[ -f "${node_bin}" ]] || _sig_die "embedded node not found: ${node_bin}"
  local ents
  ents="$(codesign -d --entitlements :- "${node_bin}" 2>/dev/null || true)"
  grep -q "allow-jit" <<<"${ents}" \
    || _sig_die "embedded node is missing the allow-jit entitlement"
  # Negative assertion via `if` — a bare `grep && die` would trip `set -e` on the
  # (good) no-match case, where grep exits non-zero.
  if grep -q "get-task-allow" <<<"${ents}"; then
    _sig_die "embedded node still carries get-task-allow (the notary will reject it)"
  fi
  codesign -dvvv "${node_bin}" 2>&1 | grep -Eq 'flags=0x[0-9a-f]+\([^)]*runtime' \
    || _sig_die "embedded node is not signed with the hardened runtime (--options runtime)"
  _sig_log "node entitlements OK: allow-jit present, get-task-allow absent, hardened runtime on"
}

# verify_app_signing <path-to-.app>
# Full SIG-* gate for the built bundle: valid deep signature, entitled node, and
# (when spctl is available) a passing Gatekeeper exec assessment.
verify_app_signing() {
  local app="$1"
  [[ -d "${app}" ]] || _sig_die "app bundle not found: ${app}"
  codesign --verify --deep --strict --verbose=2 "${app}" \
    || _sig_die "codesign --verify --deep --strict failed for ${app}"
  verify_node_entitlements "${app}/Contents/MacOS/node"
  if command -v spctl >/dev/null 2>&1; then
    spctl -a -t exec -vv "${app}" \
      || _sig_die "spctl Gatekeeper assessment rejected ${app}"
  fi
  _sig_log "SIG-* checks passed for ${app}"
}

# Run the app check when executed directly (not when sourced). The `:-` default
# keeps this safe under `set -u` when sourced from a context with an empty
# BASH_SOURCE (e.g. `bash -c` or an acceptance harness).
if [[ "${BASH_SOURCE[0]:-}" == "${0}" ]]; then
  [[ $# -ge 1 ]] || _sig_die "usage: verify-signing.sh <path-to-.app>"
  verify_app_signing "$1"
fi
