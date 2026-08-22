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
#
# Cursor roles (lead/workers on cursor-agent): cursor keeps no token records
# on disk, so the harness's cursor-agent shim tees cursor's OWN stream-json
# system/result events (model + usage) into $LOOM_MARATHON_CURSOR_USAGE_DIR,
# one file per -p process (= one turn), written by cursor itself before exit.
# Each turn is priced by the model named in its system event (table from
# cursor.com/docs/models, USD per 1M tokens; unknown/auto models fall back to
# LOOM_MARATHON_CURSOR_PRICE_IN/OUT/CACHE_READ/CACHE_WRITE, opus-class by
# default so the cap errs high). est_cost_usd = codex + cursor.
set -uo pipefail

SESSIONS_DIR="${CODEX_HOME:-$HOME/.codex}/sessions"
PRICE_IN="${LOOM_MARATHON_PRICE_IN:-1.75}"
PRICE_OUT="${LOOM_MARATHON_PRICE_OUT:-14.0}"
CURSOR_DIR="${LOOM_MARATHON_CURSOR_USAGE_DIR:-/logs/agent/cursor-usage}"
CURSOR_PRICE_IN="${LOOM_MARATHON_CURSOR_PRICE_IN:-5.0}"
CURSOR_PRICE_OUT="${LOOM_MARATHON_CURSOR_PRICE_OUT:-25.0}"
CURSOR_PRICE_CR="${LOOM_MARATHON_CURSOR_PRICE_CACHE_READ:-0.5}"
CURSOR_PRICE_CW="${LOOM_MARATHON_CURSOR_PRICE_CACHE_WRITE:-6.25}"

python3 - "$SESSIONS_DIR" "$PRICE_IN" "$PRICE_OUT" "$CURSOR_DIR" \
  "$CURSOR_PRICE_IN" "$CURSOR_PRICE_OUT" "$CURSOR_PRICE_CR" "$CURSOR_PRICE_CW" <<'PY'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
price_in, price_out = float(sys.argv[2]), float(sys.argv[3])
cursor_root = pathlib.Path(sys.argv[4])
cursor_default = tuple(float(x) for x in sys.argv[5:9])  # in, out, cache_read, cache_write

# (substring match on the lower-cased model name, first hit wins) -> USD per
# 1M tokens: input, output, cache read, cache write. cursor.com/docs/models,
# fetched 2026-08-22. Display names look like "Cursor Grok 4.6 High Fast".
CURSOR_PRICES = [
    ("grok 4.6 high fast", (4.0, 12.0, 1.0, 0.0)), ("grok 4.6 fast", (4.0, 12.0, 1.0, 0.0)),
    ("grok 4.6", (2.0, 6.0, 0.5, 0.0)),
    ("grok 4.5 high fast", (4.0, 18.0, 1.0, 0.0)), ("grok 4.5 fast", (4.0, 18.0, 1.0, 0.0)),
    ("grok 4.5", (2.0, 6.0, 0.5, 0.0)),
    ("composer 2.5 fast", (3.0, 15.0, 0.5, 0.0)), ("composer 2.5", (0.5, 2.5, 0.2, 0.0)),
    ("fable 5", (10.0, 50.0, 1.0, 12.5)),
    ("opus 5", (5.0, 25.0, 0.5, 6.25)), ("opus 4", (5.0, 25.0, 0.5, 6.25)),
    ("sonnet 5", (2.0, 10.0, 0.2, 2.5)), ("sonnet 4", (3.0, 15.0, 0.3, 3.75)),
    ("haiku", (1.0, 5.0, 0.1, 1.25)),
    ("codex 5.3", (1.75, 14.0, 0.175, 0.0)), ("gpt 5.3", (1.75, 14.0, 0.175, 0.0)),
    ("gpt 5.2", (1.75, 14.0, 0.175, 0.0)),
    ("sol", (4.0, 20.0, 0.4, 5.0)), ("luna", (0.2, 1.2, 0.02, 0.25)), ("terra", (2.0, 12.0, 0.2, 2.5)),
    ("gpt 5.5", (5.0, 30.0, 0.5, 0.0)), ("gpt 5.4", (2.5, 15.0, 0.25, 0.0)),
    ("gemini 3.7 flash", (0.75, 3.5, 0.075, 0.0)), ("gemini 3.6 flash", (1.5, 7.5, 0.15, 0.0)),
    ("gemini", (2.0, 12.0, 0.2, 0.0)),
    ("kimi k3", (3.0, 15.0, 0.3, 0.0)), ("kimi", (0.95, 4.0, 0.19, 0.0)),
]

def cursor_price(model):
    m = (model or "").lower().replace("-", " ").replace("_", " ")
    for key, price in CURSOR_PRICES:
        if key in m:
            return price, True
    return cursor_default, False
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

# ---- cursor turns (shim-captured system/result events, one file per turn) ----
def tok(u, *keys):
    for k in keys:
        if u.get(k) is not None:
            return int(u.get(k) or 0)
    return 0

c_in = c_out = c_cr = c_cw = c_turns = unpriced = 0
c_cost = 0.0
by_model = {}
if cursor_root.is_dir():
    for f in sorted(cursor_root.glob("*.jsonl")):
        model = ""
        try:
            lines = f.read_text(errors="replace").splitlines()
        except OSError:
            continue
        for line in lines:
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            if ev.get("type") == "system" and ev.get("model"):
                model = str(ev["model"])
            elif ev.get("type") == "result" and isinstance(ev.get("usage"), dict):
                u = ev["usage"]
                ti = tok(u, "inputTokens", "input_tokens")
                to = tok(u, "outputTokens", "output_tokens")
                tcr = tok(u, "cacheReadTokens", "cache_read_tokens")
                tcw = tok(u, "cacheWriteTokens", "cache_write_tokens")
                (pi, po, pcr, pcw), known = cursor_price(model)
                if not known:
                    unpriced += 1
                turn_cost = (ti * pi + to * po + tcr * pcr + tcw * pcw) / 1e6
                c_in += ti; c_out += to; c_cr += tcr; c_cw += tcw
                c_turns += 1; c_cost += turn_cost
                bm = by_model.setdefault(model or "unknown",
                                         {"turns": 0, "input_tokens": 0, "output_tokens": 0,
                                          "cache_read_tokens": 0, "est_cost_usd": 0.0})
                bm["turns"] += 1; bm["input_tokens"] += ti; bm["output_tokens"] += to
                bm["cache_read_tokens"] += tcr
                bm["est_cost_usd"] = round(bm["est_cost_usd"] + turn_cost, 4)

print(json.dumps({
    "input_tokens": total_in,
    "output_tokens": total_out,
    "codex_est_cost_usd": round(cost, 4),
    "sessions": files,
    "cursor_turns": c_turns,
    "cursor_input_tokens": c_in,
    "cursor_output_tokens": c_out,
    "cursor_cache_read_tokens": c_cr,
    "cursor_cache_write_tokens": c_cw,
    "cursor_est_cost_usd": round(c_cost, 4),
    "cursor_unpriced_turns": unpriced,
    "cursor_by_model": by_model,
    "est_cost_usd": round(cost + c_cost, 4),
}))
PY
