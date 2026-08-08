#!/usr/bin/env python3
# PROTOTYPE (LOOMCLI-62) — throwaway. Feeds fixture sessions to `codex exec
# --output-schema` with judge prompt v1, mirroring the input composition
# decided in LOOMCLI-61 (session-record header + verbatim canonical
# transcript + full diff) and the invocation pattern of
# internal/workflows/builtin/github-review-task-runner.ts.
import argparse
import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURES = os.path.normpath(os.path.join(HERE, "..", "..", "fixtures", "agent-observability"))
OUTDIR = os.path.join(HERE, "outputs")

PROMPT_VERSION = "v1"
DEFAULT_MODEL = "gpt-5.6-sol"
CODEX = os.environ.get("LOOM_CODEX_BIN", "codex")

DEFAULT_SESSIONS = [
    "20260716-193040-codex-coder--2d920713",       # happy-path coder, 1-file diff
    "20260716-225541-codex-coder--9748ccfa",       # completed but sleep-900 (efficiency anchor)
    "20260716-230149-fixture-child-coder--81dd921c",  # watchdog no-progress kill, failed
    "20260716-193040-codex-planner--3660c4d1",     # planner (no diff deliverable)
]

TAG_ALLOWLIST = {
    "false_success_claim", "incomplete_task", "instruction_violation",
    "idle_wait", "redundant_work", "tool_misuse", "hallucinated_state",
    "scope_creep", "env_or_dependency_failure", "killed_or_truncated",
    "unsafe_operation", "verification_skipped",
}
CATEGORIES = ["harness", "linter", "prompt", "skill"]
DIMS = ["outcome_success", "instruction_adherence", "efficiency", "tool_use_quality"]


def load_fleetdb_record(session_id):
    path = os.path.join(FIXTURES, "fleetdb-agent-sessions.json")
    for rec in json.load(open(path))["agent_sessions"]:
        if rec["session_id"] == session_id:
            return rec
    return None


def render_header(session_id, meta, rec):
    def pick(*vals):
        for v in vals:
            if v not in (None, "", 0.0):
                return v
        return "(unknown)"

    lines = [
        "=== SESSION RECORD (harness ground truth) ===",
        f"session_id:        {session_id}",
        f"agent:             {pick(meta.get('agent_name'), rec and rec.get('agent_id'))}"
        f" (kind: {pick(rec and rec.get('kind'), 'task')}, backend: {pick(meta.get('backend'))})",
        f"task:              {pick(meta.get('task_id'), rec and rec.get('task_id'), '(none)')} (title unavailable in fixture)",
        f"phase:             {pick(meta.get('phase'), rec and rec.get('phase'))}",
        f"final_status:      {pick(meta.get('status'), rec and rec.get('status'))}",
        f"exit_code:         {meta.get('exit_code', rec.get('exit_code') if rec else '(unknown)')}",
        f"error_class:       {pick(rec and rec.get('error_class'), '(none recorded)')}",
        f"started_at:        {pick(meta.get('started_at'))}",
        f"ended_at:          {pick(meta.get('ended_at'), rec and rec.get('finished_at'))}",
        f"duration_s:        {pick(meta.get('duration_s'))}",
        f"parent_session_id: {pick(rec and rec.get('parent_session_id'), '(none)')}",
        f"attempt:           {pick(rec and rec.get('attempt'), meta.get('attempt_num'), '0')}",
        f"tokens:            in={meta.get('input_tokens', '?')} out={meta.get('output_tokens', '?')}",
        f"diff_stat:         files_changed={meta.get('files_changed', '?')}"
        f" +{meta.get('lines_added', '?')} -{meta.get('lines_removed', '?')}",
    ]
    return "\n".join(lines)


def render_entry(e):
    head = f"-- entry {e.get('seq')} | {e.get('timestamp', '?')} | {e.get('role', '?').upper()} {e.get('type', '?')}"
    if e.get("tool_name") or e.get("tool_use_id"):
        head += f" [{e.get('tool_name', '')} {e.get('tool_use_id', '')}]".rstrip()
    parts = [head]
    if e.get("text"):
        parts.append(e["text"])
    if e.get("tool_input") is not None:
        ti = e["tool_input"]
        parts.append(ti if isinstance(ti, str) else json.dumps(ti, indent=1))
    if e.get("output") is not None:
        out = e["output"]
        parts.append(out if isinstance(out, str) else json.dumps(out, indent=1))
    return "\n".join(parts)


def compose(session_id):
    sdir = os.path.join(FIXTURES, "sessions", session_id)
    meta = json.load(open(os.path.join(sdir, "metadata.json")))
    rec = load_fleetdb_record(session_id)

    canon_path = os.path.join(sdir, "canonical_transcript.json")
    if not os.path.exists(canon_path):
        raise SystemExit(f"{session_id}: no canonical_transcript.json — not judgeable in this prototype")
    entries = json.load(open(canon_path))["data"]["entries"]

    rubric = open(os.path.join(HERE, "judge_prompt_v1.md")).read()
    header = render_header(session_id, meta, rec)
    transcript = "\n\n".join(render_entry(e) for e in entries)

    diff_path = os.path.join(sdir, "diff.patch")
    if os.path.exists(diff_path):
        diff_block = "=== FULL DIFF PRODUCED BY THE SESSION ===\n" + open(diff_path).read()
    else:
        diff_block = "=== FULL DIFF PRODUCED BY THE SESSION ===\n(no diff was produced by this session)"

    return "\n\n".join([
        rubric,
        header,
        f"=== TRANSCRIPT ({len(entries)} canonical entries, verbatim, no truncation) ===",
        transcript,
        diff_block,
        "Return ONLY the JSON object matching the output schema.",
    ])


def validate(result):
    warnings = []
    for d in DIMS:
        v = result.get("scores", {}).get(d)
        if not isinstance(v, int) or not (0 <= v <= 100):
            warnings.append(f"score {d}={v!r} not an int in 0-100")
    for t in result.get("error_taxonomy_tags", []):
        if t not in TAG_ALLOWLIST and not t.startswith("other:"):
            warnings.append(f"tag {t!r} not in allowlist and not other:-prefixed")
    cats = result.get("improvement_categories", {})
    for c in CATEGORIES:
        if len(cats.get(c, [])) > 3:
            warnings.append(f"category {c} has >3 insights")
    return warnings


def judge(session_id, model, dry_run):
    prompt = compose(session_id)
    os.makedirs(OUTDIR, exist_ok=True)
    prompt_path = os.path.join(OUTDIR, f"{session_id}.prompt.txt")
    with open(prompt_path, "w") as f:
        f.write(prompt)
    print(f"[{session_id}] judge input: {len(prompt)} bytes -> {prompt_path}")
    if dry_run:
        return None

    work = tempfile.mkdtemp(prefix="judge-proto-")
    out_path = os.path.join(work, "last-message.txt")
    subprocess.run(
        [CODEX, "exec", "--skip-git-repo-check", "--sandbox", "read-only",
         "-C", work, "--model", model,
         "--output-schema", os.path.join(HERE, "output.schema.json"),
         "--output-last-message", out_path, "-"],
        input=prompt.encode(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        timeout=600, check=True,
    )
    raw = open(out_path).read().strip()
    start, end = raw.index("{"), raw.rindex("}") + 1
    result = json.loads(raw[start:end])

    envelope = {
        "session_id": session_id,
        "judge_model": model,
        "judge_prompt_version": PROMPT_VERSION,
        "eval_id": f"eval-{session_id}-{PROMPT_VERSION}",
        "validation_warnings": validate(result),
        "result": result,
    }
    out_json = os.path.join(OUTDIR, f"{session_id}.json")
    with open(out_json, "w") as f:
        json.dump(envelope, f, indent=2)
    print(f"[{session_id}] judged -> {out_json}")
    return envelope


def main():
    ap = argparse.ArgumentParser(description="PROTOTYPE judge runner (LOOMCLI-62)")
    ap.add_argument("sessions", nargs="*", default=None)
    ap.add_argument("--model", default=DEFAULT_MODEL)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    sessions = args.sessions or DEFAULT_SESSIONS
    results = []
    for sid in sessions:
        try:
            env = judge(sid, args.model, args.dry_run)
            if env:
                results.append(env)
        except subprocess.TimeoutExpired:
            print(f"[{sid}] TIMEOUT after 600s", file=sys.stderr)
        except subprocess.CalledProcessError as e:
            print(f"[{sid}] codex exec failed: {e}", file=sys.stderr)

    for env in results:
        r = env["result"]
        print(f"\n=== {env['session_id']} ({env['eval_id']}) ===")
        print("  scores:", json.dumps(r["scores"]))
        print("  tags:  ", json.dumps(r["error_taxonomy_tags"]))
        if env["validation_warnings"]:
            print("  WARNINGS:", env["validation_warnings"])


if __name__ == "__main__":
    main()
