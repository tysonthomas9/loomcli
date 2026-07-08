#!/usr/bin/env python3
"""Generate the aft coverage census from the web UI source.

The census is the app's testable surface — routes, data-testids, API endpoints —
derived from the frontend source at run time so it can never go stale. aft joins
each run's per-test traces against it and reports what no test touched.

Usage: gen-census.py [--frontend <dir>] [--out <file>]
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

# Files whose strings describe test fixtures, not app surface.
EXCLUDE = re.compile(r"__tests__|__mocks__|\.test\.|\.stories\.|/test/|TestFixtures")


def source_files(root: Path) -> list[Path]:
    return [
        p
        for p in sorted(root.rglob("*"))
        if p.suffix in (".ts", ".tsx") and not EXCLUDE.search(p.as_posix())
    ]


def extract_routes(router: Path) -> list[str]:
    """Route table from router.tsx: root paths start with '/'; the rest are
    children of /ws/:workspaceId (the App shell). Wildcard/index entries skipped."""
    src = router.read_text()
    routes: set[str] = set()
    for m in re.finditer(r'path:\s*"([^"]+)"', src):
        path = m.group(1)
        if path in ("*",) or path.endswith("*"):
            continue
        routes.add(path if path.startswith("/") else f"/ws/:workspaceId/{path}")
    return sorted(routes)


def normalize_dynamic(s: str) -> str:
    """Dynamic pieces become one path segment: `${...}` and `{id}` -> `:param`."""
    s = re.sub(r"\$\{[^}]*\}", ":param", s)
    s = re.sub(r"\{[A-Za-z_][A-Za-z0-9_]*\}", ":param", s)
    return re.sub(r"(:param)+", ":param", s)  # adjacent interpolations are still one segment


def extract_testids(files: list[Path]) -> list[str]:
    ids: set[str] = set()
    for f in files:
        src = f.read_text()
        for m in re.finditer(r'data-testid="([^"]+)"', src):
            ids.add(m.group(1))
        # data-testid={`prefix-${expr}`} -> prefix-*  (glob; aft matches * within a segment)
        for m in re.finditer(r"data-testid=\{`([^`]+)`\}", src):
            ids.add(re.sub(r"\$\{[^}]*\}", "*", m.group(1)))
        # data-testid={cond ? "a" : "b"} and other brace expressions with string literals
        for m in re.finditer(r'data-testid=\{[^`}]*?"([^"]+)"[^}]*\}', src):
            ids.add(m.group(1))
    return sorted(ids)


def extract_endpoints(files: list[Path]) -> list[str]:
    eps: set[str] = set()
    for f in files:
        src = f.read_text()
        # wsUrl(ws, "/path") / wsUrl(ws, `/path/${x}`) -> /api/workspaces/:param/path
        # (same :param name as literal-string extraction so duplicate shapes merge)
        for m in re.finditer(r'wsUrl\([^,)]+,\s*(?:"(/[^"]*)"|`(/[^`]*)`)', src):
            path = normalize_dynamic(m.group(1) or m.group(2)).split("?")[0]
            eps.add(f"/api/workspaces/:param{path}")
        # literal or template "/api/..." strings anywhere in app source
        for m in re.finditer(r'["`](/api/[^"`\s]*)["`]', src):
            path = normalize_dynamic(m.group(1)).split("?")[0]
            eps.add(path)
    # drop entries shadowed by their own :param form to keep the census deduplicated
    return sorted(eps)


def main() -> int:
    default_frontend = Path(__file__).resolve().parents[3] / "internal/webui/frontend/src"
    ap = argparse.ArgumentParser()
    ap.add_argument("--frontend", type=Path, default=default_frontend)
    ap.add_argument("--out", type=Path, default=Path("census.json"))
    args = ap.parse_args()

    router = args.frontend / "router.tsx"
    if not router.is_file():
        print(f"gen-census: router not found at {router}", file=sys.stderr)
        return 1

    files = source_files(args.frontend)
    census = {
        "routes": extract_routes(router),
        "testids": extract_testids(files),
        "endpoints": extract_endpoints(files),
    }
    args.out.write_text(json.dumps(census, indent=2) + "\n")
    print(
        f"{args.out}: {len(census['routes'])} routes, "
        f"{len(census['testids'])} testids, {len(census['endpoints'])} endpoints"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
