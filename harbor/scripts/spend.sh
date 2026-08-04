#!/usr/bin/env bash
# Aggregate spend accounting across every codex session in $CODEX_HOME/sessions.
# Prints JSON: {"input_tokens": N, "output_tokens": N, "est_cost_usd": F, "sessions": N}
#
# Rationale (codex R4): loom's usage.jsonl is written post-return/best-effort and
# daemon workers can be reaped before finalization, so the harness sums Codex's
# OWN session records instead. Cost is a conservative estimate from a price
# table (override via LOOM_MARATHON_PRICE_IN/OUT, USD per 1M tokens).
# In stub mode the fake codex appends the same event shape to
# $CODEX_HOME/sessions/stub-rollout.jsonl, so this path is exercised for free.
set -uo pipefail

SESSIONS_DIR="${CODEX_HOME:-$HOME/.codex}/sessions"
PRICE_IN="${LOOM_MARATHON_PRICE_IN:-1.75}"
PRICE_OUT="${LOOM_MARATHON_PRICE_OUT:-14.0}"

python3 - "$SESSIONS_DIR" "$PRICE_IN" "$PRICE_OUT" <<'PY'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
price_in, price_out = float(sys.argv[2]), float(sys.argv[3])
total_in = total_out = files = 0

def usage_from(obj):
    """Tolerant extraction across codex versions: look for token counters in
    usage / info.total_token_usage / payload.* shapes."""
    if not isinstance(obj, dict):
        return None
    for key in ("usage",):
        u = obj.get(key)
        if isinstance(u, dict) and ("input_tokens" in u or "output_tokens" in u):
            return int(u.get("input_tokens") or 0), int(u.get("output_tokens") or 0)
    info = obj.get("info")
    if isinstance(info, dict):
        t = info.get("total_token_usage")
        if isinstance(t, dict):
            return int(t.get("input_tokens") or 0), int(t.get("output_tokens") or 0)
    payload = obj.get("payload")
    if isinstance(payload, dict):
        return usage_from(payload)
    return None

if root.is_dir():
    for f in root.rglob("*.jsonl"):
        # Token counters within one session are cumulative in some codex
        # versions; take the per-file MAX to avoid double counting, then sum
        # across sessions.
        best_in = best_out = 0
        try:
            for line in f.read_text(errors="replace").splitlines():
                line = line.strip()
                if not line or not line.startswith("{"):
                    continue
                try:
                    got = usage_from(json.loads(line))
                except json.JSONDecodeError:
                    continue
                if got:
                    best_in = max(best_in, got[0])
                    best_out = max(best_out, got[1])
        except OSError:
            continue
        if best_in or best_out:
            files += 1
            total_in += best_in
            total_out += best_out

cost = total_in / 1e6 * price_in + total_out / 1e6 * price_out
print(json.dumps({
    "input_tokens": total_in,
    "output_tokens": total_out,
    "est_cost_usd": round(cost, 4),
    "sessions": files,
}))
PY
