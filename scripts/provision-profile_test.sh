#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

workspace="$tmp/workspace"
claude_source="$tmp/claude-source"
codex_source="$tmp/codex-source"
bin="$tmp/bin"
mkdir -p "$workspace" "$claude_source" "$codex_source" "$bin"

printf 'claude rules\n' >"$claude_source/CLAUDE.md"
printf '{"model":"sonnet"}\n' >"$claude_source/settings.json"
printf 'must-not-copy\n' >"$claude_source/.credentials.json"
printf 'model = "gpt-5"\n' >"$codex_source/config.toml"
printf 'codex rules\n' >"$codex_source/AGENTS.md"
printf 'must-not-copy\n' >"$codex_source/auth.json"

cat >"$bin/claude" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == "--version" ]] || exit 2
printf '2.1.259 (Claude Code)\nignored second line\n'
EOF
cat >"$bin/codex" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == "--version" ]] || exit 2
printf 'codex-cli 0.152.1\n'
EOF
chmod +x "$bin/claude" "$bin/codex"

PATH="$bin:$PATH" "$repo_root/scripts/provision-profile.sh" test-agent \
  --workspace "$workspace" \
  --claude-source "$claude_source" \
  --codex-source "$codex_source"

root="$workspace/.loom/agent-profiles/test-agent"
[[ -f "$root/claude/CLAUDE.md" ]]
[[ -f "$root/claude/settings.json" ]]
[[ ! -e "$root/claude/.credentials.json" ]]
[[ -f "$root/codex/config.toml" ]]
[[ -f "$root/codex/AGENTS.md" ]]
[[ ! -e "$root/codex/auth.json" ]]

python3 - "$root" <<'PY'
import hashlib
import json
import pathlib
import stat
import sys

root = pathlib.Path(sys.argv[1])
expected = {
    "claude": (["CLAUDE.md", "settings.json"], "2.1.259 (Claude Code)"),
    "codex": (["AGENTS.md", "config.toml"], "codex-cli 0.152.1"),
}
for harness, (files, version) in expected.items():
    directory = root / harness
    manifest_path = directory / ".manifest.json"
    manifest = json.loads(manifest_path.read_text())
    assert manifest["files"] == files, manifest
    digest = hashlib.sha256()
    for rel in files:
        digest.update(rel.encode())
        digest.update(b"\0")
        digest.update((directory / rel).read_bytes())
    assert manifest["fingerprint"] == digest.hexdigest(), manifest
    assert manifest["harness_version"] == version, manifest
    assert stat.S_IMODE(manifest_path.stat().st_mode) == 0o644
PY

if PATH="$bin:$PATH" "$repo_root/scripts/provision-profile.sh" test-agent \
  --workspace "$workspace" \
  --claude-source "$claude_source" \
  --codex-source "$codex_source" >/dev/null 2>&1; then
  echo "expected existing profile refusal without --force" >&2
  exit 1
fi

printf 'updated rules\n' >"$claude_source/CLAUDE.md"
PATH="$bin:$PATH" "$repo_root/scripts/provision-profile.sh" test-agent \
  --workspace "$workspace" \
  --claude-source "$claude_source" \
  --codex-source "$codex_source" \
  --force >/dev/null
grep -Fx 'updated rules' "$root/claude/CLAUDE.md" >/dev/null

echo "provision-profile tests passed"
