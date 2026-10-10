#!/usr/bin/env python3
"""PR-watch sweep and wake waits for agents-v1-pr-watch (OR8).

The dispatcher sweeps PR watches every two minutes, longer than an AFT run
step may last (120 s), so a wait is spread over steps through a state file:

  agv1-prwatch-wait.py begin-sweep <state> [<mark>]
      Wait until a whole sweep that read PR 7 after now (or after request
      <mark> of fake-github's log, which /__fixture returns) is over: fake-github
      logged all of its reads, and then all of the next sweep's. Sweeps run
      one after another, so that proves the first one also decided and sent
      whatever it would, and a fixture patched now waits for a later sweep.
  agv1-prwatch-wait.py begin-gone <state> [<mark>]
      Wait for a whole sweep after now (or <mark>), then 150 s, more than a
      sweep interval, in which no sweep reads PR 7: its watch is gone, and
      the last sweep that read it is over.
  agv1-prwatch-wait.py begin-wakes <state> <ws> <agent_id> <n>
      Wait until the agent was handed at least n PR-watch wakes.
  agv1-prwatch-wait.py poll <state>
      Poll for up to 90 s; a no-op once done. Fails only past the wait's
      420 s deadline.
  agv1-prwatch-wait.py end <state>
      Fail unless the wait is done.
  agv1-prwatch-wait.py wakes <ws> <agent_id>
      Print the PR-watch wakes the agent was handed, as a JSON list of texts.
  agv1-prwatch-wait.py pr-events <ws> <agent_id>
      Print how many PR-watch wake events (waiting or handed) the agent has.

A wake is a Send from loom:pr-watch: a saved message.waiting event while it
waits for the agent's turn, and a message.delivered event once handed over.
Uses $AFT_FAKE_GH_BASE and $AFT_BASE_URL.
"""
import json, os, sys, time, urllib.request

PR = "/repos/loom-e2e/agv1-prwatch/pulls/7"
# Every read one PR-watch sweep makes of PR 7 (internal/prwatch Observe),
# after the PR itself; the host viewer is cached, so /user is not one.
SWEEP_READS = ("/check-runs", "/status", "/issues/7/comments", "/pulls/7/reviews", "/pulls/7/comments")
SETTLE_S = 5  # after a sweep's last read, for its in-process decide and send
SENDER = "loom:pr-watch"
QUIET_S = 150  # longer than the dispatcher's two-minute sweep interval
DEADLINE_S, POLL_S = 420, 90


def get(url):
    with urllib.request.urlopen(url, timeout=10) as r:
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


def events(ws, agent, kind):
    url = f"{os.environ['AFT_BASE_URL']}/api/workspaces/{ws}/v1/agents/{agent}/events?kind={kind}&limit=1000"
    return get(url)["events"]


def pr_events(ws, agent):
    """Every saved event of a PR-watch wake, waiting or handed over."""
    return [e for kind in ("message.waiting", "message.delivered") for e in events(ws, agent, kind)
            if SENDER in list(texts(e))]


def wakes(ws, agent):
    """The texts of the PR-watch wakes the agent was handed."""
    out = []
    for e in events(ws, agent, "message.delivered"):
        if SENDER in list(texts(e)):
            found = [t for t in texts(e.get("payload")) if t.startswith("Update on pull request")]
            out.append(found[0] if found else json.dumps(e.get("payload")))
    return out


def sweep_end(paths):
    """The index just past the reads of the first whole sweep in paths, or None."""
    if PR not in paths:
        return None
    first = paths.index(PR)
    hits = [next((i for i, p in enumerate(paths) if i > first and p.endswith(r)), None) for r in SWEEP_READS]
    return None if None in hits else max(hits) + 1


def done(st):
    """Whether st's wait is over, updating st; each call is one quick probe."""
    if st["kind"] == "wakes":
        return len(wakes(st["ws"], st["agent"])) >= st["n"]
    paths = [r["url"].split("?")[0] for r in requests()[st["mark"]:]]
    if "complete_at" not in st:
        end = sweep_end(paths)
        if end is None:
            return False
        st["complete_at"], st["reads"] = time.time(), st["mark"] + end
    later = paths[st["reads"] - st["mark"]:]
    if st["kind"] == "sweep":  # the next sweep read everything too
        return sweep_end(later) is not None
    if PR in later:  # gone: a later sweep still read the PR
        sys.exit("a sweep read PR 7 again, so its watch is not gone")
    return time.time() >= st["complete_at"] + QUIET_S


def save(path, st):
    with open(path, "w") as f:
        json.dump(st, f)


cmd, args = sys.argv[1], sys.argv[2:]
if cmd in ("begin-sweep", "begin-gone"):
    mark = int(args[1]) if len(args) > 1 else len(requests())
    save(args[0], {"kind": cmd[6:], "mark": mark, "deadline": time.time() + DEADLINE_S, "done": False})
elif cmd == "begin-wakes":
    save(args[0], {"kind": "wakes", "ws": args[1], "agent": args[2], "n": int(args[3]),
                   "deadline": time.time() + DEADLINE_S, "done": False})
elif cmd == "poll":
    st = json.load(open(args[0]))
    stop = time.time() + POLL_S
    while not st["done"]:
        if time.time() > st["deadline"]:
            sys.exit(f"{st['kind']} wait timed out after {DEADLINE_S}s")
        st["done"] = done(st)
        if st["done"] and time.time() > st["deadline"]:
            sys.exit(f"{st['kind']} wait timed out after {DEADLINE_S}s")
        save(args[0], st)
        if st["done"] or time.time() > stop:
            break
        time.sleep(2)
elif cmd == "end":
    st = json.load(open(args[0]))
    if not st["done"]:
        sys.exit(f"{st['kind']} wait not done")
elif cmd == "wakes":
    print(json.dumps(wakes(args[0], args[1])))
elif cmd == "pr-events":
    print(len(pr_events(args[0], args[1])))
else:
    sys.exit("usage: see the docstring")
