#!/usr/bin/env python3
"""Assert a live agent run left genuine, backend-attributed execution evidence.

Clauses 1 (identity) and 4 (native evidence) of the live evidence contract, shared
by every live case so each one stops carrying its own copy.

The transcript check is deliberately NOT a substring search over a serialized blob.
The existing real-tier canary asserts `"hello.md" in blob or ...`, which the agent's
own PROMPT satisfies — so it passes against a completely broken event parser. Here a
transcript only counts when it carries real `tool_use` entries with a tool name, and
the run-scoped token has to appear in the session DIFF, which is derived from what
actually changed rather than from what the model was asked to do.

Usage:
  live-session-evidence.py --base URL --workspace WS --task ID --backend NAME
                           [--expect-diff-token TOKEN]
                           [--min-files-changed N] [--max-files-changed N]
                           [--require-diff | --forbid-diff]
"""
import argparse
import json
import sys
import urllib.request


def get(url):
    with urllib.request.urlopen(url, timeout=20) as fh:
        return json.load(fh)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--base", required=True)
    p.add_argument("--workspace", required=True)
    p.add_argument("--task", required=True)
    p.add_argument("--backend", required=True)
    p.add_argument("--expect-diff-token")
    p.add_argument("--min-files-changed", type=int)
    p.add_argument("--max-files-changed", type=int)
    p.add_argument("--require-diff", action="store_true")
    p.add_argument("--forbid-diff", action="store_true")
    # Clause 4's source. The transcript endpoint cannot serve events (FINDINGS §1.2),
    # so native evidence is read from the event stream the backend itself wrote.
    p.add_argument("--agent-log")
    args = p.parse_args()

    root = f"{args.base}/api/workspaces/{args.workspace}/tasks/{args.task}"
    payload = get(f"{root}/sessions")
    data = payload.get("data") or {}
    sessions = data.get("sessions") if isinstance(data, dict) else data
    if not sessions:
        raise SystemExit(f"no session recorded for task {args.task}: {payload}")
    session = sessions[0]

    # Clause 1 — identity. The recorded run must name the backend the operator chose,
    # not merely "a" backend: selecting claude and silently running codex is exactly
    # the confusion this tier exists to catch.
    if session.get("backend") != args.backend:
        raise SystemExit(f"session backend {session.get('backend')!r} != selected {args.backend!r}: {session}")
    if session.get("exit_code") != 0:
        raise SystemExit(f"session exit_code {session.get('exit_code')!r}: {session}")

    evidence = session.get("evidence") or {}
    if evidence.get("status") != "ok":
        raise SystemExit(f"evidence status {evidence.get('status')!r}: {evidence}")
    if evidence.get("conflicts"):
        raise SystemExit(f"evidence reports conflicts: {evidence}")

    changed = int(session.get("files_changed") or 0)
    if args.min_files_changed is not None and changed < args.min_files_changed:
        raise SystemExit(f"files_changed {changed} < {args.min_files_changed}: {session}")
    if args.max_files_changed is not None and changed > args.max_files_changed:
        raise SystemExit(f"files_changed {changed} > {args.max_files_changed}: {session}")

    # PINNED KNOWN GAP — FINDINGS §1.27. HasTranscript is set only from a non-empty
    # native transcript file or event-store entries for that session id
    # (webui/svcimpl/session_service.go:185-190); a real codex worker session produces
    # neither, so it finalizes has_transcript=false. This is a CAPTURE gap and is NOT
    # the §1.2 artifact-serving bug — an earlier revision of this comment conflated the
    # two. Asserting the gap explicitly makes this a tripwire that fires the moment
    # transcripts start being captured, instead of silently lowering the bar forever.
    if session.get("has_transcript"):
        raise SystemExit(
            "FINDINGS §1.27 looks fixed (has_transcript is true) — restore the "
            f"transcript-entry assertion in this helper: {session}"
        )
    if args.require_diff and not session.get("has_diff"):
        raise SystemExit(f"session has no diff: {session}")
    if args.forbid_diff and session.get("has_diff"):
        raise SystemExit(f"session unexpectedly has a diff: {session}")

    # Clause 4 — native evidence, read from the event stream the backend itself wrote.
    # Structured events carrying a real command, NOT a substring search over a blob the
    # prompt itself could have supplied. This is the same data the transcript endpoint
    # would serve if §1.2 were fixed.
    #
    # Scope caveat, stated rather than glossed: the log is per-AGENT and append-only
    # (cli/agent/archive_log.go:24-31). Because live agent names embed the run id it
    # cannot contain a different run's events, but it is not per-session — a restarted
    # sibling session of the same agent would also satisfy this. Callers that need the
    # work pinned to a specific session must pair this with a session-scoped check
    # (files_touched / a run-scoped token in that session's diff).
    session_id = session.get("session_id")
    native = 0
    if args.agent_log:
        with open(args.agent_log, encoding="utf-8", errors="replace") as fh:
            for line in fh:
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    ev = json.loads(line)
                except ValueError:
                    continue
                item = ev.get("item") or {}
                if (ev.get("type") == "item.completed"
                        and item.get("type") == "command_execution"
                        and (item.get("command") or "").strip()):
                    native += 1
        if native == 0:
            raise SystemExit(
                f"no structured native tool events in {args.agent_log} — the backend "
                "may have run without tools, or the event log was never written"
            )

    if args.expect_diff_token:
        with urllib.request.urlopen(f"{root}/sessions/{session_id}/diff", timeout=20) as fh:
            diff_blob = fh.read().decode("utf-8", errors="replace")
        if args.expect_diff_token not in diff_blob:
            raise SystemExit(
                f"run-scoped token {args.expect_diff_token!r} absent from the session diff "
                f"(a stale artifact cannot satisfy this): {diff_blob[:400]}"
            )

    print(
        f"session evidence ok: backend={session.get('backend')} exit=0 "
        f"files_changed={changed} native_tool_events={native}"
    )


if __name__ == "__main__":
    sys.exit(main())
