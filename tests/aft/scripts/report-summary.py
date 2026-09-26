#!/usr/bin/env python3
"""Render an aft last-run.json as GitHub-flavored markdown (for $GITHUB_STEP_SUMMARY)."""
import json
import sys

path = sys.argv[1] if len(sys.argv) > 1 else "tests/aft/reports/last-run.json"
with open(path) as f:
    run = json.load(f)

icon = {"passed": "✅", "healed": "⚡", "failed": "❌", "skipped": "⏭️"}
tests = run["tests"]
counts = {s: sum(1 for t in tests if t["status"] == s) for s in ("passed", "healed", "failed", "skipped")}
tally = ", ".join(f"{v} {k}" for k, v in counts.items() if v) or "no tests ran"

mode = run["mode"] if run.get("agentEnabled") else "no-agent"
print(f"## aft e2e — {tally} ({mode} mode, {run['durationMs'] / 1000:.1f}s)\n")
print("| suite | test | status | time |")
print("|---|---|---|---|")
for t in tests:
    print(f"| {t['suite']} | {t['name']} | {icon.get(t['status'], '')} {t['status']} | {t['durationMs'] / 1000:.1f}s |")

verdicts = [(t, s) for t in tests for s in t["steps"] if s.get("verdict")]
if verdicts:
    print("\n### Agent verdicts\n")
    for t, s in verdicts:
        v = s["verdict"]
        confidence = f" _(confidence: {v['confidence']})_" if v.get("confidence") else ""
        print(f"- **{t['suite']} / {t['name']}**, step {s['index'] + 1} `{s['label']}`: {v['root_cause']}{confidence}")
        if v.get("suggested_fix"):
            print(f"  - suggested fix: `{json.dumps(v['suggested_fix'])}`")

failures = [t for t in tests if t["status"] == "failed"]
if failures:
    sys.exit(0)  # summary rendering never fails the build; the test step already did
