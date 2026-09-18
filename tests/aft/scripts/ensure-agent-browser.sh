#!/usr/bin/env bash
# ensure-agent-browser.sh — fail-fast/self-heal preflight for the aft browser driver.
#
# Cleanup tools sweep the Chrome binary agent-browser launches while leaving the
# CLI itself intact, so every suite dies at `agent-browser set viewport` long
# after harness start. agent-browser resolves its browser from
# ~/.agent-browser/config.json executablePath, falling back to a hardcoded
# ms-playwright cache path — it does NOT scan its own install dir, so a fresh
# `agent-browser install` still fails until config points at it.
#
# macOS-only (the sweep + paths are macOS phenomena); a no-op elsewhere so CI
# (Linux) keeps its own provisioning.
set -euo pipefail

[[ "$(uname)" == "Darwin" ]] || exit 0

if ! command -v agent-browser >/dev/null 2>&1; then
    echo "[aft] agent-browser not on PATH — install it (npm i -g agent-browser) before running aft" >&2
    exit 1
fi

CONFIG="$HOME/.agent-browser/config.json"

config_exe() {
    [[ -f "$CONFIG" ]] || return 0
    python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[1])).get("executablePath", ""))
except Exception:
    pass' "$CONFIG"
}

managed_chrome() {
    # `|| true`: under pipefail a no-match ls fails the pipeline, which set -e
    # would turn into a silent script death right where the heal path starts.
    ls -d "$HOME/.agent-browser/browsers"/chrome-*/"Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" 2>/dev/null | sort | tail -1 || true
}

exe="$(config_exe)"
if [[ -n "$exe" ]]; then
    # config pins a browser: live -> done, dead -> repair below
    [[ -x "$exe" ]] && exit 0
else
    # no config: agent-browser uses its hardcoded ms-playwright default
    default_chrome="$(ls -d "$HOME/Library/Caches/ms-playwright"/chromium-*/chrome-mac*/"Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing" 2>/dev/null | tail -1 || true)"
    [[ -n "$default_chrome" && -x "$default_chrome" ]] && exit 0
fi

chrome="$(managed_chrome)"
if [[ -z "$chrome" ]]; then
    echo "[aft] agent-browser Chrome missing — running 'agent-browser install'..."
    agent-browser install
    chrome="$(managed_chrome)"
fi
if [[ -z "$chrome" || ! -x "$chrome" ]]; then
    echo "[aft] agent-browser install did not produce a launchable Chrome; run 'agent-browser install' manually" >&2
    exit 1
fi

if [[ -f "$CONFIG" ]]; then
    cp "$CONFIG" "$CONFIG.bak-aft"
    echo "[aft] repointing $CONFIG at $chrome (previous config backed up to config.json.bak-aft)"
else
    echo "[aft] writing $CONFIG -> $chrome"
fi
mkdir -p "$(dirname "$CONFIG")"
CHROME_PATH="$chrome" python3 -c 'import json,os
path = os.path.expanduser("~/.agent-browser/config.json")
cfg = {}
try:
    cfg = json.load(open(path))
except Exception:
    pass
cfg["executablePath"] = os.environ["CHROME_PATH"]
json.dump(cfg, open(path, "w"))'
echo "[aft] agent-browser Chrome ready: $chrome"
