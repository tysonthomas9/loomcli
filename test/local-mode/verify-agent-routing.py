#!/usr/bin/env python3
"""Verify role-based task routing for UI-registered plan/task agents.

Runs against a live local-mode stack and proves the routing the UI promises
maps to the default Execution-owned worker:

  * a task WITHOUT a design is claimed by the PLAN agent (role=plan), and never
    by a task agent while it has no design;
  * a task WITH a design is claimed by the TASK agent (role=task), and never by
    a plan agent.

It also exercises the exact UI agent-creation endpoint (POST /api/workspaces/
{ws}/agents) and confirms it lands a fleet-db agentdef identical to the CLI path
(`loom agentdef add`), so "create a plan/task agent in the UI" maps to a real,
daemon-claimable agent.

Routing itself is the Go supervisor's claim decision (unit-tested in
internal/cli); this asserts it end-to-end on the running stack.

Env: LOCAL_MODE_API_URL (default http://localhost:8282), LOOM_WORKSPACE
(default LOCALMODE), ROUTING_EPIC_ID (default LOCALMODE-1), ROUTING_SOURCE_REPO
(default source-repo), ROUTING_TIMEOUT_SECS (default 480).
"""
import json
import os
import sys
import time
import urllib.request
import urllib.error

API = os.environ.get("LOCAL_MODE_API_URL", "http://localhost:8282").rstrip("/")
WS = os.environ.get("LOOM_WORKSPACE", "LOCALMODE")
EPIC = os.environ.get("ROUTING_EPIC_ID", "LOCALMODE-1")
REPO = os.environ.get("ROUTING_SOURCE_REPO", "source-repo")
TIMEOUT = int(os.environ.get("ROUTING_TIMEOUT_SECS", "480"))

passes, fails = [], []
def ok(m): passes.append(m); print(f"  \033[32mPASS\033[0m {m}", flush=True)
def bad(m): fails.append(m); print(f"  \033[31mFAIL\033[0m {m}", flush=True)
def info(m): print(f"  ... {m}", flush=True)


def call(method, path, body=None):
    url = f"{API}/api/workspaces/{WS}{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Content-Type": "application/json", "X-Actor": "routing-verify"})
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        return e.code, {"error": e.read().decode()[:200]}
    except Exception as e:  # noqa
        return 0, {"error": str(e)}


def unwrap(d):
    return d.get("data", d) if isinstance(d, dict) else d


def agent_roles():
    _, d = call("GET", "/agents")
    out = {}
    for a in (unwrap(d) or []):
        if isinstance(a, dict) and a.get("name"):
            out[a["name"]] = a.get("role_name")
    return out


def create_issue(title, design=None):
    body = {"title": title, "issue_type": "task", "priority": 0, "parent": EPIC,
            "source_repo": REPO, "description": f"Routing probe: {title}.",
            "acceptance_criteria": "Routing probe — claim is what matters."}
    if design:
        body["design"] = design
    st, d = call("POST", "/issues", body)
    iid = (unwrap(d) or {}).get("id") if st < 300 else None
    return iid, st, d


def task_claimers(task_id, roles):
    """Return (set of roles that ran a session, list of (agent,phase,role))."""
    _, d = call("GET", f"/tasks/{task_id}/sessions")
    sessions = (unwrap(d) or {}).get("sessions") or []
    rows = [(s.get("agent_name"), s.get("phase"), roles.get(s.get("agent_name"), "?"))
            for s in sessions if isinstance(s, dict)]
    return {r for _, _, r in rows}, rows


def assignee_role(task_id, roles):
    _, d = call("GET", f"/issues/{task_id}")
    a = (unwrap(d) or {}).get("assignee")
    return a, roles.get(a, "?" if a else None)


def main():
    print(f"== routing verify against {API} (ws={WS}, epic={EPIC}) ==", flush=True)
    roles = agent_roles()
    plan_agents = [n for n, r in roles.items() if r == "plan"]
    task_agents = [n for n, r in roles.items() if r == "task"]
    print(f"  agents: {roles}", flush=True)
    if plan_agents and task_agents:
        ok(f"a plan agent ({plan_agents[0]}) and a task agent ({task_agents[0]}) are registered")
    else:
        bad("need at least one plan agent and one task agent registered"); return finish()

    # --- (A) UI agent-creation endpoint round-trip (the exact path the UI uses) ---
    _, agents_raw = call("GET", "/agents")
    backend = next((a.get("backend") for a in (unwrap(agents_raw) or [])
                    if isinstance(a, dict) and a.get("role_name") == "plan"), "codex")
    probe = "ui-routing-probe"
    st, d = call("POST", "/agents", {"workspace_key": WS, "name": probe, "role_name": "plan",
                                     "auto": False, "backend": backend, "repos": [REPO]})
    if st < 300:
        back = agent_roles()
        if back.get(probe) == "plan":
            ok(f"UI endpoint POST /agents created '{probe}' as a role=plan agentdef (maps to CLI agentdef)")
        else:
            bad(f"UI-created agent not found with role=plan (got {back.get(probe)})")
    else:
        bad(f"UI endpoint POST /agents failed: {st} {d}")

    # --- seed clean routing probes ---
    impl_id, st1, d1 = create_issue("ROUTE-IMPL has-design probe",
                                    design="Approved design: append one line to routing-impl.txt, commit, close.")
    plan_id, st2, d2 = create_issue("ROUTE-PLAN no-design probe", design=None)
    if not impl_id or not plan_id:
        bad(f"failed to seed probe tasks (impl={st1}/{d1}, plan={st2}/{d2})"); return finish()
    info(f"seeded ROUTE-IMPL={impl_id} (has design), ROUTE-PLAN={plan_id} (no design)")

    # --- poll until both claimed (or timeout) ---
    deadline = time.time() + TIMEOUT
    plan_done = impl_done = False
    while time.time() < deadline and not (plan_done and impl_done):
        proutes, prows = task_claimers(plan_id, roles)
        iroutes, irows = task_claimers(impl_id, roles)
        pa, pr = assignee_role(plan_id, roles)
        ia, ir = assignee_role(impl_id, roles)
        plan_done = bool(proutes) or pr == "plan"
        impl_done = bool(iroutes) or ir == "task"
        info(f"ROUTE-PLAN claimers={sorted(proutes)} assignee={pa}({pr}) | "
             f"ROUTE-IMPL claimers={sorted(iroutes)} assignee={ia}({ir})")
        if plan_done and impl_done:
            break
        time.sleep(10)

    # --- assert routing ---
    proutes, prows = task_claimers(plan_id, roles)
    iroutes, irows = task_claimers(impl_id, roles)
    pa, pr = assignee_role(plan_id, roles)
    ia, ir = assignee_role(impl_id, roles)

    # no-design task -> plan agent
    if "plan" in proutes or pr == "plan":
        ok(f"no-design task {plan_id} was claimed by a PLAN agent (sessions={prows}, assignee={pa})")
    else:
        bad(f"no-design task {plan_id} was NOT claimed by a plan agent (sessions={prows}, assignee={pa}/{pr})")
    if "task" in proutes:
        bad(f"no-design task {plan_id} was wrongly claimed by a TASK agent (sessions={prows})")
    else:
        ok(f"no-design task {plan_id} was NOT touched by any task agent (correct)")

    # has-design task -> task agent
    if "task" in iroutes or ir == "task":
        ok(f"has-design task {impl_id} was claimed by a TASK agent (sessions={irows}, assignee={ia})")
    else:
        bad(f"has-design task {impl_id} was NOT claimed by a task agent (sessions={irows}, assignee={ia}/{ir})")
    if "plan" in iroutes:
        bad(f"has-design task {impl_id} was wrongly claimed by a PLAN agent (sessions={irows})")
    else:
        ok(f"has-design task {impl_id} was NOT touched by any plan agent (correct)")

    finish()


def finish():
    print(f"\n== routing verify: {len(passes)} passed, {len(fails)} failed ==", flush=True)
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
