#!/usr/bin/env python3
"""Check the bounded session model and its named legacy mutations with TLC."""

import os
from pathlib import Path
import hashlib
import re
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / "test/formal/sessions"
JAR = Path(os.environ.get("LOOM_TLA_JAR", Path.home() / ".cache/loom-tla/v1.7.4/tla2tools.jar"))
JAR_SHA256 = "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"
CASES = {
    "fixed": (0, "No error has been found"),
    "238-legacy": (12, "Invariant NoOpenAfterParentTerminal is violated"),
    "238-terminal-legacy": (12, "Invariant TaskSessionTerminalOnce is violated"),
    "240-legacy": (12, "Invariant NoCrossAttemptClose is violated"),
    "241-barrier-legacy": (12, "Invariant NoFinishFailure is violated"),
    "241-sweep-legacy": (13, "Temporal properties were violated"),
    "682-legacy": (12, "Invariant CompletionNotificationIssued is violated"),
    "682-notify-mutation": (12, "Invariant CompletionNotificationIssued is violated"),
    "682-poll-mutation": (13, "Temporal properties were violated"),
    "X1-legacy": (12, "Invariant NoHealWhileLive is violated"),
    "X4-legacy": (12, "Invariant DriverLeaseLossNoticed is violated"),
    "evidence-fixed": (0, "No error has been found"),
    "373-legacy": (12, "Invariant CompletedImpliesEvidenceOrExplicitCaptureFailure is violated"),
    "H6-legacy": (12, "Invariant UsageNeverOverwrittenByZero is violated"),
    "375-usage-legacy": (12, "Invariant UsageNeverFabricated is violated"),
}
MUTATIONS = {
    "238-legacy": {"TerminalGuard": "FALSE"},
    "238-terminal-legacy": {"TerminalGuard": "FALSE"},
    "240-legacy": {"AttemptFence": "FALSE"},
    "241-barrier-legacy": {"FinishBarrier": "FALSE"},
    "241-sweep-legacy": {"ParentSweep": "FALSE"},
    "682-legacy": {"NotifyOnFinish": "FALSE"},
    "682-notify-mutation": {"NotifyOnFinish": "FALSE"},
    "682-poll-mutation": {"PollEnabled": "FALSE"},
    "X1-legacy": {"LivenessHeal": "FALSE"},
    "X4-legacy": {"NoticeLeaseLoss": "FALSE"},
    "373-legacy": {"EvidenceBarrier": "FALSE"},
    "H6-legacy": {"PreserveUsage": "FALSE"},
    "375-usage-legacy": {"HonestUsage": "FALSE"},
}


def size_bytes(path: Path) -> int:
    return sum(p.stat().st_size for p in path.rglob("*") if p.is_file())


def main() -> int:
    if not JAR.is_file():
        print(f"TLC jar missing: {JAR}", file=sys.stderr)
        return 2
    if hashlib.sha256(JAR.read_bytes()).hexdigest() != JAR_SHA256:
        print(f"TLC jar checksum mismatch: {JAR}", file=sys.stderr)
        return 2
    selected = sys.argv[1:] or list(CASES)
    unknown = set(selected) - CASES.keys()
    if unknown:
        print(f"Unknown configs: {', '.join(sorted(unknown))}", file=sys.stderr)
        return 2
    scratch = Path(tempfile.mkdtemp(prefix="loom-sessions-tlc-"))
    print(f"TLC scratch and traces: {scratch}")
    failures = 0
    for name in selected:
        if name in MUTATIONS:
            baseline = (MODEL / ("evidence-fixed.cfg" if name in
                         {"373-legacy", "H6-legacy", "375-usage-legacy"} else "fixed.cfg")).read_text()
            variant = (MODEL / f"{name}.cfg").read_text()
            for key, value in MUTATIONS[name].items():
                if f"  {key} = TRUE" not in baseline or f"  {key} = {value}" not in variant:
                    print(f"{name}: mutation gate lacks {key}={value}", file=sys.stderr)
                    return 2
        meta = scratch / name
        meta.mkdir()
        log = scratch / f"{name}.log"
        command = ["java", "-Xmx512m", "-XX:+UseParallelGC", "-cp", str(JAR),
                   "tlc2.TLC", "-deadlock", "-cleanup", "-workers", "1",
                   "-metadir", str(meta), "-config", f"{name}.cfg",
                   "Evidence.tla" if name in {"evidence-fixed", "373-legacy", "H6-legacy", "375-usage-legacy"}
                   else "Sessions.tla"]
        with log.open("w") as output:
            process = subprocess.Popen(command, cwd=MODEL, stdout=output, stderr=subprocess.STDOUT)
            while process.poll() is None:
                if size_bytes(meta) >= 900 * 1024 * 1024:
                    process.kill()
                    process.wait()
                    print(f"{name}: scratch cap reached; stopped", file=sys.stderr)
                    return 3
                time.sleep(0.1)
        body = log.read_text(errors="replace")
        expected_code, diagnostic = CASES[name]
        distinct = re.findall(r"([\d,]+) distinct states found", body)
        depths = re.findall(r"depth of the complete state graph search is (\d+)", body)
        traces = re.findall(r"State (\d+):", body)
        progress = re.findall(r"Progress\((\d+)\)", body)
        count = int(distinct[-1].replace(",", "")) if distinct else 0
        depth = int(depths[-1]) if depths else max([int(x) for x in traces + progress], default=0)
        ok = process.returncode == expected_code and diagnostic in body and count >= 10 and depth >= 2
        if not ok:
            failures += 1
        print(f"{name}: {'OK' if ok else 'UNEXPECTED'}; exit={process.returncode}; "
              f"distinct={count}; depth={depth}; expected={diagnostic}")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
