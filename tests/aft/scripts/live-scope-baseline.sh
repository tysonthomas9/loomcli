#!/usr/bin/env bash
# Snapshot a directory tree so a live case can later prove the model changed
# nothing outside its instructions. Pairs with live-scope-verify.sh.
#
# Extracted from li-custom-prompt.test.yaml so every live case shares one oracle
# instead of each carrying its own copy (LW-1/LW-2 carried none at all).
#
# What is captured, and why each part is load-bearing:
#   * file digest AND mode bits — `chmod +x` on a checked-in script is a real change
#     that a content hash alone misses.
#   * per-repo refs + HEAD — .git internals are excluded from the file scan (index
#     and log churn is noise), so a commit, a new branch or a moved tag would
#     otherwise be invisible to a case whose instructions forbid git.
#   * only real repo roots (a dir containing .git). `git -C <any subdir>` answers
#     from the PARENT repo, so treating every subdirectory as a repo reports the
#     same repo many times and produces phantom diffs.
#   * deliberately NO `git status --porcelain`: the artifact a live case is told to
#     create is untracked, so status always differs between baseline and check.
#     Working-tree changes are covered symmetrically by the file scan instead.
#
# Usage: live-scope-baseline.sh <root> <baseline-file>
set -euo pipefail

root="${1:?usage: live-scope-baseline.sh <root> <baseline-file>}"
out="${2:?usage: live-scope-baseline.sh <root> <baseline-file>}"

python3 - "$root" "$out" <<'PY'
import hashlib, pathlib, subprocess, sys

root = pathlib.Path(sys.argv[1])
lines = []

for path in sorted(root.rglob("*")):
    if not path.is_file() or ".git" in path.parts:
        continue
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    mode = oct(path.stat().st_mode & 0o777)
    lines.append(f"F {digest}:{mode}  {path.relative_to(root)}")

for repo in [root, *sorted(p for p in root.iterdir() if p.is_dir())]:
    if not (repo / ".git").exists():
        continue
    refs = subprocess.run(["git", "-C", str(repo), "for-each-ref", "--format=%(refname) %(objectname)"],
                          capture_output=True, text=True)
    if refs.returncode != 0:
        continue
    head = subprocess.run(["git", "-C", str(repo), "rev-parse", "HEAD"],
                          capture_output=True, text=True)
    rel = "." if repo == root else str(repo.relative_to(root))
    blob = refs.stdout + "|" + head.stdout
    lines.append(f"G {hashlib.sha256(blob.encode()).hexdigest()}  {rel}")

pathlib.Path(sys.argv[2]).write_text("\n".join(lines) + "\n", encoding="utf-8")
print(f"baseline: {len(lines)} entries from {root}")
PY
