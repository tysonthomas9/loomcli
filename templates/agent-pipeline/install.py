#!/usr/bin/env python3
"""Install (or update) the agent-pipeline template into a workspace.

    install.py <params/NAME.yaml | NAME> [--dry-run] [--flush] [--smoke] [--skip-apply]

One template, per-workspace params. Renders pipeline.yaml, integration.yaml and
prompts/ from the single template source, diffs against the live workspace,
backs up anything it changes, applies the spec with the supervisor stopped, and
restarts the supervisor. The ONLY intended per-workspace differences are the
params: model (opus everywhere, sonnet in TEST), merge strategy
(pr-stack-local | direct-push), and the repo list/contract blocks.

--flush is allowed ONLY for TEST: it resets the fleet-db workspace (delete +
recreate), clears sessions and daemon scratch, resets the sandbox git fixture
to a recorded baseline, then re-applies and restarts. That is what makes
template iteration repeatable: flush -> install -> smoke -> observe -> flush.

--smoke seeds one end-to-end task (TEST only) that must traverse
plan -> critic -> decomposer -> coder -> tester -> integrator to `delivered`.
"""
import argparse, datetime, difflib, json, os, shutil, subprocess, sys, time, urllib.request
from pathlib import Path

import yaml

T = Path(__file__).resolve().parent
HOME = Path.home()
FDB = "http://127.0.0.1:3011"
DEPLOYER = "http://127.0.0.1:3010"
ACTOR = "loom"
KEY_FILE = HOME / "local-stack/fleet-db-admin-key.txt"
MANIFEST = HOME / "local-stack/loom-stack-deployed.json"
OUTPUT_TIMEOUT = "2700"

SHARED_PROMPTS = ("critic.md", "decomposer.md", "tester.md", "observer.md")


def log(m): print(m, flush=True)


def fail(m):
    print(f"error: {m}", file=sys.stderr); sys.exit(2)


def sh(cmd, cwd=None, env=None, check=True, timeout=600):
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    if check and r.returncode != 0:
        fail(f"command failed ({' '.join(cmd)}):\n{r.stdout[-1500:]}\n{r.stderr[-1500:]}")
    return r


def api(base, path, payload=None, method=None, headers=None):
    req = urllib.request.Request(base + path,
                                 data=json.dumps(payload).encode() if payload is not None else None,
                                 method=method or ("POST" if payload is not None else "GET"),
                                 headers={"Content-Type": "application/json",
                                          "X-Actor": ACTOR, **(headers or {})})
    with urllib.request.urlopen(req, timeout=15) as f:
        body = f.read()
        return json.loads(body) if body.strip() else {}


def render(text, params):
    for k, v in params.items():
        text = text.replace("{{%s}}" % k, v if isinstance(v, str) else json.dumps(v))
    leftovers = [l for l in text.splitlines() if "{{" in l]
    if leftovers:
        fail(f"unrendered placeholders remain: {leftovers[:3]}")
    return text


def build_tree(p):
    """Render the whole file set into memory: {relpath: content}."""
    strategy = p["strategy"]
    subs = {"workspace": p["workspace"], "workspace_dir": p["workspace_dir"],
            "model": p["model"], "backend": p["backend"],
            "repos": "[" + ", ".join(p["repos"]) + "]"}
    out = {}
    out["pipeline.yaml"] = render((T / "pipeline.yaml.tmpl").read_text(), subs)
    head = (T / f"integration.head.{strategy}.yaml").read_text()
    block = (T / "integration-repos" / f"{p['name']}.yaml").read_text()
    out["integration.yaml"] = render(head, subs) + "repos:\n" + render(block, subs)
    for f in SHARED_PROMPTS:
        out[f"prompts/{f}"] = render((T / "prompts" / f).read_text(), subs)
    publish = (T / "prompts" / f"worker.publish.{strategy}.md").read_text().rstrip()
    worker = (T / "prompts/worker.md").read_text().replace("{{publish_rules}}", publish)
    out["prompts/worker.md"] = render(worker, subs)
    out["prompts/integrator.md"] = render((T / "prompts" / f"integrator.{strategy}.md").read_text(), subs)
    return out


def write_tree(ws_dir, tree, dry):
    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    backup = ws_dir / ".template-backups" / stamp
    changed = []
    for rel, new in tree.items():
        dst = ws_dir / rel
        old = dst.read_text() if dst.exists() else None
        if old == new:
            continue
        changed.append(rel)
        diff = list(difflib.unified_diff((old or "").splitlines(), new.splitlines(),
                                         fromfile=f"live/{rel}", tofile=f"template/{rel}", lineterm=""))
        log(f"  ~ {rel}: {sum(1 for l in diff if l.startswith('+'))-1}+ "
            f"{sum(1 for l in diff if l.startswith('-'))-1}-")
        if not dry:
            if old is not None:
                b = backup / rel
                b.parent.mkdir(parents=True, exist_ok=True)
                b.write_text(old)
            dst.parent.mkdir(parents=True, exist_ok=True)
            dst.write_text(new)
    if changed and not dry:
        log(f"  backups of replaced files: {backup}")
    if not changed:
        log("  all files already match the template")
    return changed


def fleet_env(ws):
    env = dict(os.environ, LOOM_FLEET_DB_URL=FDB, LOOM_FLEET_DB_ACTOR=ACTOR,
               LOOM_FLEET_DB_API_KEY=KEY_FILE.read_text().strip(), LOOM_WORKSPACE=ws)
    env.pop("LOOM_SERVER_URL", None)
    return env


def deployed_loom():
    cur = json.loads(MANIFEST.read_text())["current"]["loom"]
    return Path(cur["worktree"]) / "loom"


def ensure_bin_loom(ws_dir):
    binary = deployed_loom()
    link = ws_dir / "bin/loom"
    link.parent.mkdir(exist_ok=True)
    if link.is_symlink() or link.exists():
        if link.is_symlink() and link.resolve() == binary.resolve():
            return
        link.unlink()
    link.symlink_to(binary)
    log(f"  bin/loom -> {binary}")


def pm2(*args, check=False):
    return sh(["pm2", *args], check=check)


def supervisor_cycle(app, action):
    r = pm2(action, app)
    log(f"  pm2 {action} {app}: rc={r.returncode}")


def apply_spec(ws, ws_dir, app, dry):
    loom = str(ws_dir / "bin/loom")
    env = fleet_env(ws)
    r = sh([loom, "workspace", "apply", "-f", str(ws_dir / "pipeline.yaml"), "--dry-run"],
           cwd=ws_dir, env=env)
    log("  apply --dry-run: ok")
    if dry:
        return
    supervisor_cycle(app, "stop")
    try:
        sh([loom, "workspace", "apply", "-f", str(ws_dir / "pipeline.yaml")], cwd=ws_dir, env=env)
        log("  apply: ok")
    finally:
        supervisor_cycle(app, "start")


def flush_test(p):
    """TEST only: wipe the board, scratch state, and the sandbox git fixture."""
    ws, ws_dir = p["workspace"], Path(p["workspace_dir"])
    if ws != "TEST":
        fail("--flush is allowed only for the TEST workspace")
    log("flush TEST:")
    try:
        row = api(FDB, f"/api/v1/admin/workspaces/{ws}")
        row = row.get("workspace", row)
        existed = True
    except Exception:
        row, existed = {}, False  # a previous flush died between delete and recreate
    supervisor_cycle(p["supervisor_app"], "stop")
    try:
        if existed:
            api(FDB, f"/api/v1/admin/workspaces/{ws}?force=true", method="DELETE")
            log("  fleet-db workspace deleted")
        # Recreate through the CLI, the same path the real workspaces were made
        # through — the raw admin POST validates repos as org/repo and rejects
        # the bare source-repo ids the pipeline routes on.
        loom = str(ws_dir / "bin/loom")
        env = fleet_env(ws)
        sh([loom, "workspace", "add", ws,
            "--description", row.get("description") or "rehearsal rig",
            "--branch", row.get("default_branch") or "main"], env=env)
        for rid, spec in (p.get("repo_remotes") or {}).items():
            sh([loom, "repo", "add", rid, spec["url"],
                "--branch", spec.get("branch", "main"),
                "--source-repo-id", rid], env=env)
        log("  fleet-db workspace recreated (empty board, repos re-attached)")
    except Exception:
        supervisor_cycle(p["supervisor_app"], "start")  # never leave TEST unsupervised on a failed flush
        raise

    for sub in ("sessions",):
        d = ws_dir / sub
        if d.exists():
            for e in d.iterdir():
                shutil.rmtree(e, ignore_errors=True) if e.is_dir() else e.unlink(missing_ok=True)
            log(f"  cleared {d}")
    for f in ("daemon.pid", "daemon.lock"):
        (ws_dir / f).unlink(missing_ok=True)

    # git fixture reset: record a baseline once, then always return to it.
    sandbox = ws_dir / "sandbox"
    baseline_f = T / ".test-sandbox-baseline"
    if not baseline_f.exists():
        sha = sh(["git", "-C", str(sandbox), "rev-parse", "origin/main"]).stdout.strip()
        baseline_f.write_text(sha)
        log(f"  recorded sandbox baseline {sha[:12]} (first flush)")
    base = baseline_f.read_text().strip()
    origin = ws_dir / "origins/sandbox.git"
    sh(["git", "-C", str(origin), "update-ref", "refs/heads/main", base])
    for ref in sh(["git", "-C", str(origin), "for-each-ref", "--format=%(refname)", "refs/heads/loom/"]).stdout.split():
        sh(["git", "-C", str(origin), "update-ref", "-d", ref])
    log(f"  origin main reset to {base[:12]}, loom/* branches dropped")
    trees = [sandbox] + sorted((ws_dir / "worktrees").glob("**/.git"))
    for g in trees:
        tree = g if g.is_dir() and g.name != ".git" else g.parent
        sh(["git", "-C", str(tree), "fetch", "origin"], check=False)
        sh(["git", "-C", str(tree), "checkout", "-f", "main"], check=False)
        sh(["git", "-C", str(tree), "reset", "--hard", base], check=False)
        sh(["git", "-C", str(tree), "clean", "-fd"], check=False)
        for br in sh(["git", "-C", str(tree), "for-each-ref", "--format=%(refname:short)",
                      "refs/heads/loom/"], check=False).stdout.split():
            sh(["git", "-C", str(tree), "branch", "-D", br], check=False)
    log(f"  reset {len(trees)} checkout(s) to baseline")


def smoke(p):
    ws, ws_dir = p["workspace"], Path(p["workspace_dir"])
    if ws != "TEST":
        fail("--smoke is TEST-only")
    loom = str(ws_dir / "bin/loom")
    title = f"smoke {datetime.datetime.now().strftime('%H%M%S')}: add slugify.py utility with tests, wire into make gate"
    desc = """Add a small `slugify.py` utility to the sandbox repo, following the exact pattern of
`rot13.py` / `uuidgen.py`:

* `slugify.py`: `slugify(text) -> str` lowercases, replaces runs of non-alphanumerics with `-`,
  strips leading/trailing `-`. A `__main__` block that slugifies argv.
* `tests/test_slugify.py`: unittest cases covering normal text, punctuation runs, leading/trailing
  junk, and the empty string.
* Wire the new test module into BOTH the `gate` and `test` targets in the Makefile, matching how
  the existing modules are listed.

Acceptance: `make gate` green including the new tests. This is a smoke task: the point is to
traverse every pipeline stage to `delivered`, so keep the change minimal and boring."""
    r = sh([loom, "data", "create", "--type", "task", "--source-repo", "sandbox",
            "--title", title, "--description", desc, "--priority", "2", "--output", "json"],
           env=fleet_env(ws))
    import re
    m = re.search(r"TEST-\d+", r.stdout)
    log(f"  smoke task seeded: {m.group(0) if m else r.stdout[:120]}")
    return m.group(0) if m else None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("params")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--flush", action="store_true")
    ap.add_argument("--smoke", action="store_true")
    ap.add_argument("--skip-apply", action="store_true")
    a = ap.parse_args()

    pf = Path(a.params)
    if not pf.exists():
        pf = T / "params" / (a.params.lower().removesuffix(".yaml") + ".yaml")
    if not pf.exists():
        fail(f"params not found: {a.params}")
    p = yaml.safe_load(pf.read_text())
    p["name"] = pf.stem
    ws_dir = Path(p["workspace_dir"])
    if not ws_dir.exists():
        fail(f"workspace dir missing: {ws_dir} (create the workspace first)")

    log(f"install {p['workspace']} (model={p['model']}, strategy={p['strategy']}, dry={a.dry_run})")
    if a.flush:
        if a.dry_run:
            fail("--flush with --dry-run makes no sense")
        flush_test(p)
    tree = build_tree(p)
    write_tree(ws_dir, tree, a.dry_run)
    if not a.dry_run:
        ensure_bin_loom(ws_dir)
    if not a.skip_apply:
        apply_spec(p["workspace"], ws_dir, p["supervisor_app"], a.dry_run)
    if a.smoke and not a.dry_run:
        smoke(p)
    log("done")


if __name__ == "__main__":
    main()
