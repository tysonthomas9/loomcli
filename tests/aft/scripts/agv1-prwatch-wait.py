#!/usr/bin/env python3
"""PR-watch sweep and wake waits for agents-v1-pr-watch (OR8).

The dispatcher sweeps PR watches every two minutes, longer than an AFT run
step may last (120 s), so a wait is spread over steps through a state file:

  agv1-prwatch-wait.py begin-sweep <state>
      Wait for the next sweep: one that reads PR 7 after now, and is over
      once its reads stop for 3 s.
  agv1-prwatch-wait.py begin-wakes <state> <ws> <agent_id> <n>
      Wait until the agent was handed at least n PR-watch wakes.
  agv1-prwatch-wait.py poll <state>
      Poll for up to 90 s; a no-op once done. Fails only past the wait's
      300 s deadline.
  agv1-prwatch-wait.py end <state>
      Fail unless the wait is done.
  agv1-prwatch-wait.py wakes <ws> <agent_id>
      Print the PR-watch wakes the agent was handed, as a JSON list of texts.

A wake is a saved message.delivered event whose text starts "Update on pull
request". Uses $AFT_FAKE_GH_BASE and $AFT_BASE_URL.
"""
import json, os, sys, time, urllib.request

PR = "/repos/loom-e2e/agv1-prwatch/pulls/7"
DEADLINE_S, POLL_S = 300, 90


def get(url):
    with urllib.request.urlopen(url, timeout=30) as r:
        return json.loads(r.read())


def requests():
    return get(os.environ["AFT_FAKE_GH_BASE"] + "/__requests")


def texts(node):
    if isinstance(node, str):
        yield node
    elif isinstance(node, dict):
        for v in node.values():
            yield from texts(v)
    elif isinstance(node, list):
        for v in node:
            yield from texts(v)


def wakes(ws, agent):
    url = f"{os.environ['AFT_BASE_URL']}/api/workspaces/{ws}/v1/agents/{agent}/events?kind=message.delivered&limit=1000"
    out = []
    for e in get(url)["events"]:
        out.extend([t for t in texts(e.get("payload")) if t.startswith("Update on pull request")][:1])
    return out


def done(st):
    """Whether st's wait is over, updating st."""
    if st["kind"] == "wakes":
        return len(wakes(st["ws"], st["agent"])) >= st["n"]
    reqs = requests()
    if "seen" not in st:
        if not any(r["url"].split("?")[0] == PR for r in reqs[st["mark"]:]):
            return False
        st["seen"] = len(reqs)
        time.sleep(3)
        reqs = requests()
    while len(reqs) != st["seen"]:  # the sweep is over once its reads stop
        st["seen"] = len(reqs)
        time.sleep(3)
        reqs = requests()
    return True


def save(path, st):
    with open(path, "w") as f:
        json.dump(st, f)


cmd, args = sys.argv[1], sys.argv[2:]
if cmd == "begin-sweep":
    save(args[0], {"kind": "sweep", "mark": len(requests()), "deadline": time.time() + DEADLINE_S, "done": False})
elif cmd == "begin-wakes":
    save(args[0], {"kind": "wakes", "ws": args[1], "agent": args[2], "n": int(args[3]),
                   "deadline": time.time() + DEADLINE_S, "done": False})
elif cmd == "poll":
    st = json.load(open(args[0]))
    stop = time.time() + POLL_S
    while not st["done"]:
        st["done"] = done(st)
        save(args[0], st)
        if st["done"]:
            break
        if time.time() > st["deadline"]:
            sys.exit(f"{st['kind']} wait timed out after {DEADLINE_S}s")
        if time.time() > stop:
            break
        time.sleep(2)
elif cmd == "end":
    st = json.load(open(args[0]))
    if not st["done"]:
        sys.exit(f"{st['kind']} wait not done")
elif cmd == "wakes":
    print(json.dumps(wakes(args[0], args[1])))
else:
    sys.exit("usage: see the docstring")
