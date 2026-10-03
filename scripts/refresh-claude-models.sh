#!/usr/bin/env bash
# Refresh the bundled Claude model snapshot from models.dev (the OpenCode
# model registry). Loom never fetches it at runtime: run this, review the
# diff, rebuild, and new Anthropic models appear in the Claude picker.
set -euo pipefail

URL="${MODELS_DEV_URL:-https://models.dev/api.json}"
OUT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/loomharness/claude/models.json"

curl -fsSL --max-time 60 "$URL" | jq --sort-keys '.anthropic.models | map_values({
  id, name, family, release_date, status,
  context: .limit.context,
  input: .modalities.input,
  effort: ([.reasoning_options[]? | select(.type == "effort") | .values] | first)
} | with_entries(select(.value != null)))' > "$OUT.tmp"
jq -e 'length > 0' "$OUT.tmp" > /dev/null
mv "$OUT.tmp" "$OUT"
echo "wrote $OUT ($(jq length "$OUT") models)"
