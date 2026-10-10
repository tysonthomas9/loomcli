#!/usr/bin/env python3
"""Check that an Agent API agent reads deleted within a time bound.

Usage: agv1-deleted-within.py URL START SECONDS

URL is the agent's GET /v1/agents/{id}; START is the epoch time the bound
counts from. Every GET is cut off at the bound and its answer counts only if
it arrived inside it. The agent must read state deleted with deleted_at and
history_purged_at set and no Attention, even after a retried Delete (ATT1;
agents-v1-delete-create-retry).
"""
import json
import sys
import time
import urllib.request


def main() -> int:
    url, start, bound = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
    deadline = start + bound
    last = {}
    while True:
        left = deadline - time.time()
        if left <= 0:
            break
        try:
            last = json.load(urllib.request.urlopen(url, timeout=left))
        except Exception as err:  # serve may still be starting
            last = {"error": str(err)}
        arrived = time.time()
        if arrived > deadline:
            break
        if last.get("state") == "deleted" and last.get("deleted_at") and last.get("history_purged_at"):
            if last.get("attention_reason"):
                print(f"the deleted agent still needs attention: {last['attention_reason']}", file=sys.stderr)
                return 1
            print(f"deleted {arrived - start:.2f} s after the start")
            return 0
        time.sleep(min(0.2, max(0.0, deadline - time.time())))
    print(f"not deleted {bound:g} s after the start: {json.dumps(last)}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
