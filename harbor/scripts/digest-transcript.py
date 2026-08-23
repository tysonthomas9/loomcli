#!/usr/bin/env python3
"""Turn a cursor-agent stream-json transcript (one loom worker, many sessions) into a
readable per-session digest: the task prompt head, every tool call (command + truncated
result), assistant text, thinking (short), and the turn result with usage.
Usage: digest.py <transcript.log> > digest.md"""
import json, sys, re
from datetime import datetime, timezone
def ts(ms): return datetime.fromtimestamp(ms/1000, tz=timezone.utc).strftime("%H:%M:%S") if ms else "--:--:--"
def trunc(s, n): s = s if isinstance(s, str) else json.dumps(s); return s if len(s) <= n else s[:n] + f"… [+{len(s)-n} chars]"
sess = 0; think = []
for raw in open(sys.argv[1], errors="replace"):
    raw = raw.strip()
    if not raw: continue
    if not raw.startswith("{"):
        print(f"\n> STDERR/HARNESS: {trunc(raw, 300)}\n"); continue
    try: o = json.loads(raw)
    except Exception: print(f"\n> BADJSON: {trunc(raw, 200)}\n"); continue
    t = o.get("type"); st = o.get("subtype")
    if t == "user":
        sess += 1
        txt = "".join(c.get("text","") for c in o["message"]["content"] if isinstance(c, dict))
        m = re.search(r"(MARATHON-\d+)", txt); task = m.group(1) if m else "?"
        head = re.sub(r"\s+", " ", txt)[:600]
        print(f"\n\n# SESSION {sess} (task {task}, prompt {len(txt)} chars)\nPROMPT: {head}…\n")
    elif t == "system":
        print(f"[system {st} model={o.get('model')} {ts(o.get('timestamp_ms'))}]")
    elif t == "thinking":
        if st == "delta": think.append(o.get("text",""))
        elif st == "completed":
            s = "".join(think).strip(); think = []
            if s:
                s = re.sub(r"\s+", " ", s); print(f"  (thinking) {trunc(s, 400)}")
    elif t == "assistant":
        txt = "".join(c.get("text","") for c in o.get("message",{}).get("content",[]) if isinstance(c, dict))
        if txt.strip(): print(f"  ASSISTANT: {trunc(txt.strip(), 1500)}")
    elif t == "tool_call":
        tc = o["tool_call"]; k = next(iter(tc)); a = tc[k].get("args", {})
        if st == "started":
            if "shell" in k: print(f"  $ [{ts(o.get('timestamp_ms'))}] {trunc(a.get('command',''), 500)}")
            elif "read" in k: print(f"  READ {a.get('path')}")
            elif "write" in k or "edit" in k or "delete" in k or "apply" in k: print(f"  {k.replace('ToolCall','').upper()} {a.get('path') or a.get('file_path') or ''}")
            else: print(f"  TOOL {k} {trunc(a, 200)}")
        elif st == "completed" and "shell" in k:
            r = tc[k].get("result", {}); s = r.get("success") or r.get("error") or r
            if isinstance(s, dict):
                out = (s.get("stdout") or "") + (("\nSTDERR: " + s["stderr"]) if s.get("stderr") else "")
                print(f"    -> exit={s.get('exitCode')} {trunc(out.strip(), 600)}")
            else: print(f"    -> {trunc(s, 300)}")
        elif st == "completed" and ("write" in k or "edit" in k):
            r = tc[k].get("result", {}); print(f"    -> {trunc(r, 200)}")
    elif t == "result":
        u = o.get("usage", {})
        print(f"\n[RESULT {st} is_error={o.get('is_error')} dur={o.get('duration_ms',0)//1000}s in={u.get('inputTokens')} out={u.get('outputTokens')}]\n  FINAL: {trunc(str(o.get('result','')).strip(), 1500)}")
