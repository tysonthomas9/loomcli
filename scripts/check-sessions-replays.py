#!/usr/bin/env python3
"""Run tagged behavioral replays against v5 or a branch carrying the fixes."""

from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
CASES = {
    "./internal/driver": ["TestSessionsReplay238ReopenTerminal", "TestSessionsReplay375UsageOnClose"],
    "./internal/sessions": ["TestSessionsReplayX2TerminalOnce", "TestSessionsReplay175CanonicalSinkRedaction"],
    "./internal/sessions/transcript/codex": ["TestSessionsReplay244ModernCodexMessage"],
    "./internal/cli/hooks": ["TestSessionsReplay690SubagentPath"],
    "./internal/cli/cleanup": ["TestSessionsReplay558UsageReadsSessionIndex"],
    "./internal/webui/svcimpl": ["TestSessionsReplayX3RemoteTranscriptFlag"],
}


def main() -> int:
    fixed = sys.argv[1:] == ["--expect-fixed"]
    if sys.argv[1:] and not fixed:
        print("usage: check-sessions-replays.py [--expect-fixed]", file=sys.stderr)
        return 2
    failures = 0
    for package, names in CASES.items():
        for name in names:
            result = subprocess.run(
                ["go", "test", "-tags", "sessionsreplay", package,
                 "-run", f"^{name}$", "-count=1"],
                cwd=ROOT, capture_output=True, text=True, check=False,
            )
            output = result.stdout + result.stderr
            ok = (result.returncode == 0 if fixed else
                  result.returncode != 0 and f"--- FAIL: {name}" in output)
            if not ok:
                failures += 1
            print(f"{name}: {'OK' if ok else 'UNEXPECTED'}; "
                  f"exit={result.returncode}; expected={'pass' if fixed else 'named v5 failure'}")
            if not ok:
                print(output[-2000:])
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
