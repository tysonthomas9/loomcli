#!/usr/bin/env python3
"""Unofficial CUA-judge replica using the claude CLI (rev 2, codex-vetted).

Replicates tasks/slack-clone/tests/cua_verifier.py as closely as the claude
CLI allows: same rubric (verbatim criteria + weights), same two-stage design
(one browsing DRIVER session producing the exact structured report template,
then one impartial JUDGE call per criterion with the official system prompt),
same ternary scoring (PASS=1.0 / PARTIAL=0.5 / FAIL=0.0) and weighted
normalization, and (rev 2) the official's hard-fail semantics: a driver or
judge infra failure raises instead of silently scoring FAIL.

Codex-vet fixes in rev 2: host health probes all three nodes; driver nonzero
exit or incomplete report raises; judge calls hard-fail after retries with
raw outputs archived; brace-aware JSON extraction; viewport pinned to the
official 1280x800 window via a mandatory first command; browser sessions are
judge-owned names (judge-a/judge-b) and only those are cleaned; the artifact
is copied to a NEUTRAL path before boot so no run label leaks into the
driver prompt (blinding); boot happens inside try/finally; start.sh is
chmod'ed and requirements.txt installed first (mirrors tests/test.sh).

Documented deviations from the official verifier (unofficial instrument):
  - judge/driver model: claude CLI `opus` alias (official: claude-opus-4-7)
  - browser: host-side agent-browser CLI (official: in-container Chromium
    via harbor Computer1); app reached through published ports 1800x with
    in-container 0.0.0.0 forwarders (official: 127.0.0.1 in-container)
  - source reads: host copy of the SAME artifact bytes at a neutral path
  - temperature: CLI default (official judge: 0); no hard 80-turn cap
Usage: claude_judge.py <label:app_dir> [<label:app_dir> ...]
Writes <outbase>/<label>/{driver-prompt.txt,driver-report.txt,verdicts.json,
ux.json,judges-raw/}
"""

import json
import pathlib
import re
import shutil
import subprocess
import sys
import time

IMAGE = "docker.io/library/slack-clone__geyvx7k__env-main:latest"
RUBRIC = pathlib.Path(
    "/Users/tyson/codebase/code-agents/swe-marathon/tasks/slack-clone/tests/rubric.json"
)
OUTBASE = pathlib.Path(
    "/private/tmp/claude-501/-Users-tyson-codebase-code-agents-loomcli/"
    "50f0f885-3d1d-4acd-b427-65a26ca2e269/scratchpad/claude-judge"
)
CLAUDE = "/Users/tyson/.local/bin/claude"
START_URL = "http://127.0.0.1:18000/"
HOST_PORTS = (18000, 18001, 18002)
CONTAINER = "uxjudge-run"
SESSIONS = ("judge-a", "judge-b")

JUDGE_SYSTEM_PROMPT = (
    "You are an impartial verifier judge. A computer-using agent was "
    "given a small rubric and asked to verify a built application by "
    "actually using its UI in a browser. You will be given:\n"
    "  (a) one rubric criterion (id, label, passing definition),\n"
    "  (b) the CUA's full structured report.\n\n"
    "Decide whether the CUA's evidence supports a PASS, PARTIAL, or "
    "FAIL on this single criterion. Trust the CUA's first-person "
    "report unless it is internally inconsistent. If the evidence is "
    "too vague to support any verdict, default to FAIL with a note.\n\n"
    "Reply with a single JSON object and nothing else:\n"
    "{\n"
    '  "verdict": "PASS" | "PARTIAL" | "FAIL",\n'
    '  "reason": "<1-3 sentences referencing specific evidence>"\n'
    "}\n"
)


class JudgeError(RuntimeError):
    """Hard failure: the instrument (not the app) broke — no score emitted."""


def sh(*args, check=True, timeout=120, **kw):
    return subprocess.run(list(args), check=check, timeout=timeout,
                          capture_output=True, text=True, **kw)


def extract_json(text):
    """Official _extract_json analog: whole-string, then raw_decode prefix,
    then first-brace raw_decode."""
    try:
        obj = json.loads(text)
        if isinstance(obj, dict):
            return obj
    except json.JSONDecodeError:
        pass
    for candidate in (text.lstrip(), text[text.find("{"):] if "{" in text else ""):
        if not candidate:
            continue
        try:
            obj, _ = json.JSONDecoder().raw_decode(candidate)
            if isinstance(obj, dict):
                return obj
        except json.JSONDecodeError:
            continue
    return {}


def load_criteria():
    raw = json.loads(RUBRIC.read_text())["criteria"]
    return [
        {
            "id": c["id"],
            "label": c["label"],
            "passing_definition": c["passing_definition"],
            "weight": float(c.get("weight", 1.0)),
        }
        for c in raw
    ]


def criteria_text(criteria):
    lines = []
    for i, c in enumerate(criteria, 1):
        lines.append(f"{i}. **{c['label']}** (id `{c['id']}`, weight {c['weight']}):")
        lines.append(f"   {c['passing_definition']}")
    return "\n".join(lines)


def report_template(criteria):
    blocks = []
    for c in criteria:
        cu = c["id"].upper()
        blocks.append(
            f"CRITERION_{cu}: PASS | PARTIAL | FAIL\n"
            f"CRITERION_{cu}_EVIDENCE: <1-3 sentences with concrete "
            "evidence from the live UI and/or the source you read.>"
        )
    blocks.append(
        "OVERALL_NOTES: <1-3 sentences summarising your overall impression "
        "and anything that surprised you.>"
    )
    return "\n\n".join(blocks)


def driver_prompt(criteria, app_src):
    return (
        "You are acting as a black-box UI/UX verifier for an "
        "application that another (build) agent just produced. The "
        f"running app is available in your browser at the start URL: {START_URL}\n"
        f"(two additional cluster nodes of the same app serve http://127.0.0.1:18001/ "
        "and http://127.0.0.1:18002/).\n\n"
        "Browser control: use the `agent-browser` CLI via shell for ALL "
        "browser interaction, ALWAYS with the session flag "
        "`--session judge-a` (use `--session judge-b` where a workflow "
        "needs a second, separately-logged-in user). Your FIRST two "
        "commands must be:\n"
        "  agent-browser --session judge-a set viewport 1280 800\n"
        f"  agent-browser --session judge-a open {START_URL}\n"
        "Core commands: `open <url>`, `snapshot -i` (numbered element "
        "refs), `click @e1`, `fill @e2 \"text\"`, `press Enter`, "
        "`screenshot`, `wait --load networkidle`. Re-snapshot after "
        "navigation or DOM changes. Do not open DevTools and do not "
        "fetch the app's internal APIs directly; drive the UI like a "
        "real user.\n\n"
        "You can also issue shell commands to read the build agent's "
        "source tree on demand as supporting evidence — it is at: "
        f"{app_src}\n"
        "(e.g. `ls`, `grep -n`, `sed -n`, `head`). The source tree is NOT "
        "pre-loaded into this prompt; you decide what to read. Prefer "
        "narrow head/grep/sed over wholesale cat of large files.\n\n"
        "You MUST attempt every workflow in the browser before scoring "
        "it.\n\n"
        f"## Rubric\n\n{criteria_text(criteria)}\n\n"
        "## Required structured report\n\n"
        "End by replying with a final report matching this exact shape "
        "(one block, no markdown fences):\n\n"
        f"```\n{report_template(criteria)}\n```\n\n"
        "Use PASS only when the criterion clearly works. Use FAIL "
        "only when you actually attempted the workflow and it "
        "broke. Use PARTIAL when something half-worked. Be honest "
        "— the goal is accurate verification, not high scores."
    )


FORWARDER = (
    "import socket,threading\n"
    "def pipe(a,b):\n"
    "    try:\n"
    "        while True:\n"
    "            d=a.recv(65536)\n"
    "            if not d: break\n"
    "            b.sendall(d)\n"
    "    except OSError: pass\n"
    "    finally:\n"
    "        try: b.shutdown(socket.SHUT_WR)\n"
    "        except OSError: pass\n"
    "def serve(lp,tp):\n"
    "    s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)\n"
    "    s.bind(('0.0.0.0',lp)); s.listen(64)\n"
    "    while True:\n"
    "        c,_=s.accept()\n"
    "        try: u=socket.create_connection(('127.0.0.1',tp))\n"
    "        except OSError: c.close(); continue\n"
    "        threading.Thread(target=pipe,args=(c,u),daemon=True).start()\n"
    "        threading.Thread(target=pipe,args=(u,c),daemon=True).start()\n"
    "for i in range(3):\n"
    "    threading.Thread(target=serve,args=(9000+i,8000+i),daemon=True).start()\n"
    "import time\n"
    "while True: time.sleep(60)\n"
)


def _assert_host_ports_free():
    """Fail CLOSED if the judge's host ports are already bound (codex judge-audit
    2026-08-08: a leaked b2d cluster squatted 18000-18002 and every subsequent
    run drove the browser against IT, not the artifact — six false 0.9375s)."""
    import socket as _sock
    for port in HOST_PORTS:
        with _sock.socket(_sock.AF_INET, _sock.SOCK_STREAM) as t:
            if t.connect_ex(("127.0.0.1", port)) == 0:
                raise JudgeError(
                    f"host port {port} already in use before boot — a stale "
                    f"server is squatting the judge port; the browser would see "
                    f"the wrong app. Free it (lsof -tiTCP:{port} | xargs kill) "
                    f"and re-run.")


def boot_app(name, app_dir):
    _assert_host_ports_free()
    sh("podman", "run", "-d", "--name", name,
       "-p", "18000:9000", "-p", "18001:9001", "-p", "18002:9002",
       IMAGE, "sleep", "infinity")
    sh("podman", "cp", str(app_dir), f"{name}:/app-judge", timeout=300)
    # Mirror tests/test.sh app setup: move into place, chmod, install deps.
    sh("podman", "exec", name, "bash", "-c",
       "rm -rf /app && mv /app-judge /app && mkdir -p /logs && "
       "chmod +x /app/start.sh 2>/dev/null; true")
    # Stamp a per-boot identity canary that a correctly-mapped app serves back.
    import secrets as _secrets
    _c = "JUDGE-CANARY-" + _secrets.token_hex(6)
    boot_app._canary = _c
    sh("podman", "exec", name, "bash", "-c",
       f"printf %s {_c} > /app/.judge-canary && "
       f"printf %s {_c} > /app/public/.judge-canary 2>/dev/null; "
       f"printf %s {_c} > /app/static/.judge-canary 2>/dev/null; "
       f"printf %s {_c} > /app/web/.judge-canary 2>/dev/null; true")
    sh("podman", "exec", name, "bash", "-c",
       "cd /app && if [ -f requirements.txt ]; then "
       "/opt/venv/bin/pip install --quiet -r requirements.txt; fi",
       timeout=600)
    sh("podman", "exec", "-d", name, "python3", "-c", FORWARDER)
    sh("podman", "exec", "-d", name, "bash", "-c",
       "cd /app && ./start.sh > /logs/judge-server.log 2>&1")
    # Health: all three nodes in-container, then all three via host ports
    # (official cua_stage.sh loops every health URL).
    probes = [
        ["podman", "exec", name, "bash", "-c",
         "for p in 8000 8001 8002; do curl -sf http://127.0.0.1:$p/api/health >/dev/null || exit 1; done"],
    ] + [["curl", "-sf", f"http://127.0.0.1:{p}/api/health"] for p in HOST_PORTS]
    for probe in probes:
        ok = False
        for _ in range(30):
            if subprocess.run(probe, capture_output=True).returncode == 0:
                ok = True
                break
            time.sleep(5)
        if not ok:
            log = sh("podman", "exec", name, "tail", "-40",
                     "/logs/judge-server.log", check=False).stdout
            raise JudgeError(f"app never became healthy ({probe}):\n{log}")
    # Identity assert (SPA-safe): confirm host port 18000 is actually mapped to
    # THIS container by podman. Combined with the pre-boot free-port guard this
    # closes the leaked-squatter class without depending on the app serving a
    # static canary route (SPAs catch-all to index.html). A served canary is
    # written too (best-effort, checked only when a static root exists).
    mp = sh("podman", "port", name, "9000", check=False).stdout
    if f":{HOST_PORTS[0]}" not in (mp or ""):
        raise JudgeError(
            f"container {name} port 9000 is not mapped to host {HOST_PORTS[0]} "
            f"(podman port -> {mp!r}); the browser would not reach this app.")


def run_driver(prompt, outdir, criteria):
    (outdir / "driver-prompt.txt").write_text(prompt)
    allowed = [
        "Bash(agent-browser:*)", "Bash(ls:*)", "Bash(grep:*)", "Bash(rg:*)",
        "Bash(sed:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(find:*)",
        "Bash(wc:*)", "Bash(cat:*)",
    ]
    r = subprocess.run(
        [CLAUDE, "-p", prompt, "--model", "opus", "--allowedTools", *allowed],
        capture_output=True, text=True, timeout=3000, cwd=str(outdir),
    )
    report = (r.stdout or "").strip()
    (outdir / "driver-report.txt").write_text(report or r.stderr[-4000:])
    if r.returncode != 0:
        raise JudgeError(f"driver exited {r.returncode}: {r.stderr[-800:]}")
    missing = [c["id"] for c in criteria
               if f"CRITERION_{c['id'].upper()}" not in report]
    if missing:
        raise JudgeError(f"driver report incomplete; missing criteria: {missing}")
    return report


def judge_all(criteria, report, outdir):
    rawdir = outdir / "judges-raw"
    rawdir.mkdir(exist_ok=True)
    verdicts = []
    for c in criteria:
        user = (
            f"Rubric criterion id: {c['id']}\n"
            f"Label: {c['label']}\n"
            f"Passing definition: {c['passing_definition']}\n\n"
            f"CUA verifier's full report:\n{report}"
        )
        verdict = reason = None
        last_err = ""
        for attempt in range(2):
            try:
                r = subprocess.run(
                    [CLAUDE, "-p", user, "--model", "opus",
                     "--system-prompt", JUDGE_SYSTEM_PROMPT],
                    capture_output=True, text=True, timeout=300,
                )
                (rawdir / f"{c['id']}.{attempt}.txt").write_text(
                    f"rc={r.returncode}\n--stdout--\n{r.stdout}\n--stderr--\n{r.stderr[-2000:]}")
                if r.returncode != 0:
                    last_err = f"judge exited {r.returncode}"
                    continue
                parsed = extract_json(r.stdout)
                v = str(parsed.get("verdict", "")).upper()
                if v in {"PASS", "PARTIAL", "FAIL"}:
                    verdict, reason = v, str(parsed.get("reason", ""))
                    break
                last_err = "no valid verdict JSON in judge output"
            except Exception as exc:  # noqa: BLE001
                last_err = f"judge error: {exc}"
        if verdict is None:
            # Official semantics: judge infra failure hard-fails the stage
            # rather than silently degrading the score.
            raise JudgeError(f"judge failed on criterion {c['id']!r}: {last_err}")
        score = {"PASS": 1.0, "PARTIAL": 0.5, "FAIL": 0.0}[verdict]
        verdicts.append({"id": c["id"], "label": c["label"],
                         "weight": c["weight"], "verdict": verdict,
                         "score": score, "reason": reason})
        print(f"[judge] {c['id']}: {verdict} — {reason[:100]}", flush=True)
    (outdir / "verdicts.json").write_text(json.dumps(verdicts, indent=1))
    return verdicts


def cleanup_browser():
    for s in SESSIONS:
        subprocess.run(["agent-browser", "--session", s, "close"],
                       capture_output=True)


def run_one(label, app_dir, criteria):
    outdir = OUTBASE / label
    outdir.mkdir(parents=True, exist_ok=True)
    # Blinding: judge sees only a neutral copy of the artifact.
    neutral = OUTBASE / "app-under-test"
    if neutral.exists():
        shutil.rmtree(neutral)
    shutil.copytree(app_dir, neutral)
    sh("podman", "rm", "-f", CONTAINER, check=False)
    try:
        print(f"== {label}: booting", flush=True)
        boot_app(CONTAINER, neutral)
        cleanup_browser()
        print(f"== {label}: driver session starting", flush=True)
        report = run_driver(driver_prompt(criteria, str(neutral)), outdir, criteria)
        print(f"== {label}: judging", flush=True)
        verdicts = judge_all(criteria, report, outdir)
        total_w = sum(v["weight"] for v in verdicts) or 1.0
        ux = sum(v["weight"] * v["score"] for v in verdicts) / total_w
        (outdir / "ux.json").write_text(json.dumps(
            {"label": label, "ux_unofficial": ux,
             "judge": "claude-cli opus (unofficial replica rev2)",
             "verdicts": verdicts}, indent=1))
        print(f"== {label}: UNOFFICIAL ux = {ux}", flush=True)
        return ux
    finally:
        cleanup_browser()
        subprocess.run(["podman", "rm", "-f", CONTAINER], capture_output=True)
        if neutral.exists():
            shutil.rmtree(neutral, ignore_errors=True)


def main():
    criteria = load_criteria()
    results = {}
    for arg in sys.argv[1:]:
        label, app_dir = arg.split(":", 1)
        results[label] = run_one(label, pathlib.Path(app_dir), criteria)
    print("RESULTS:", json.dumps(results), flush=True)


if __name__ == "__main__":
    main()
