#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

workspace="$tmp/workspace"
profile="$workspace/.loom/agent-profiles/test-agent/claude"
bin="$tmp/bin"
mkdir -p "$profile" "$bin"
printf '{}\n' >"$profile/.manifest.json"

cat >"$bin/claude" <<EOF
#!/usr/bin/env bash
printf '%s\\n' "\$*" >>"$tmp/claude-calls"
[[ "\${1:-}" == "setup-token" ]] || exit 2
printf 'Claude setup completed; copy the token shown by Claude.\\n'
EOF
chmod +x "$bin/claude"

token='sk-ant-oat01-test-profile-token'
output="$tmp/output"
printf '\ny\n%s\n' "$token" | PATH="$bin:$PATH" \
  "$repo_root/scripts/setup-profile-token.sh" test-agent --workspace "$workspace" >"$output"

grep -Fx 'setup-token' "$tmp/claude-calls" >/dev/null
[[ "$(cat "$profile/oauth-token")" == "$token" ]]
[[ "$(stat -f '%Lp' "$profile/oauth-token" 2>/dev/null || stat -c '%a' "$profile/oauth-token")" == "600" ]]
if grep -F "$token" "$output" >/dev/null; then
  echo "token leaked to wizard output" >&2
  exit 1
fi

rm "$profile/oauth-token"
if printf '\ny\n\n' | PATH="$bin:$PATH" \
  "$repo_root/scripts/setup-profile-token.sh" test-agent --workspace "$workspace" >/dev/null 2>&1; then
  echo "expected empty token refusal" >&2
  exit 1
fi
[[ ! -e "$profile/oauth-token" ]]

if printf '\ny\n%s\n' "$token" | PATH="$bin:$PATH" \
  "$repo_root/scripts/setup-profile-token.sh" missing --workspace "$workspace" >/dev/null 2>&1; then
  echo "expected missing profile refusal" >&2
  exit 1
fi

echo "setup-profile-token tests passed"
