#!/usr/bin/env python3
"""Helper to append JSONL assertion results for upgrade-disaster-multi-tenant validation."""
import sys, json

SCRATCH = "reports/scratch-upgrade-disaster-multi-tenant.jsonl"

def record(aid, title, status, command_run, actual_result, expected_result, log_evidence="", notes=""):
    line = {
        "id": aid,
        "title": title,
        "status": status,
        "command_run": command_run,
        "actual_result": actual_result,
        "expected_result": expected_result,
        "log_evidence": log_evidence,
        "notes": notes,
    }
    with open(SCRATCH, "a") as f:
        f.write(json.dumps(line) + "\n")
    print(f"[{status.upper():8}] {aid}")

if __name__ == "__main__":
    # Called with JSON blob on stdin or as CLI arg
    if len(sys.argv) > 1:
        data = json.loads(sys.argv[1])
    else:
        data = json.loads(sys.stdin.read())
    record(**data)
