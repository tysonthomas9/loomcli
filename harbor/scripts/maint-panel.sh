#!/usr/bin/env bash
# Maintainability panel — merged from 3 vetted designs (codex-vetted, fixes folded).
# Identical-machinery metrics over 4 same-spec artifacts. Emits results.json + human table.
set -euo pipefail

MX="${MX:-$HOME/.mx-stage}"
OUT="$MX/results.json"
rm -rf "$MX"; mkdir -p "$MX"

# --- artifact map ------------------------------------------------------------
# bash 3.2 on macOS has no associative arrays — case map instead.
IDS="baseline run19 run20 run21"
src_of() {
  case "$1" in
    baseline) echo /Users/tyson/codebase/code-agents/loomcli/harbor/trials/codex-baseline-1/slack-clone__BV7wtZv/artifacts/app ;;
    run19)    echo /Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-1/slack-clone__tUAmrE8/artifacts/app ;;
    run20)    echo /Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-2/slack-clone__RbVFjFP/artifacts/app ;;
    run21)    echo /Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-dual-1/slack-clone__jxozk75/artifacts/app ;;
  esac
}

# --- fold: pinned versions (vet HIGH x2) -------------------------------------
LIZARD_V=$(python3 -c 'import lizard;print(lizard.version)')
JSCPD_PIN="jscpd@5.0.14"
echo "[panel] lizard=$LIZARD_V jscpd=$JSCPD_PIN"

# --- stage identical scopes (vet: one canonical scope for ALL tools) ---------
for id in $IDS; do
  s=$(src_of "$id")
  [ -d "$s" ] || { echo "FATAL: missing $s" >&2; exit 1; }
  rsync -a --delete \
    --exclude '.git' --exclude 'node_modules' --exclude '__pycache__' --exclude '.venv' \
    --exclude 'data/' --exclude 'timer.sh' --exclude 'package-lock.json' --exclude '*.lock' \
    "$s/" "$MX/$id/"
done

# --- prod/test partition + metrics (one python pass, shared predicates) ------
python3 - "$MX" $IDS <<'PY'
import json, os, re, subprocess, sys, statistics as st

MX, ids = sys.argv[1], sys.argv[2:]
CODE = ('.js', '.mjs', '.cjs', '.py')  # .mjs found in run19 tests (vet: scope must match reality)
# canonical test predicate — identical for every tool (vet HIGH: scope drift)
TEST_RE = re.compile(r'(^|/)(tests?)/|(^|/)test_[^/]*\.py$|\.(test|spec)\.[cm]?[jt]s$|(^|/)conftest\.py$')

def is_test(rel): return bool(TEST_RE.search(rel))

def sloc_comments(path):
    """SLOC + comment lines. Python: tokenize (#) + ast (docstrings) so that
    triple-quoted SQL/data literals count as CODE (codex numbers-vet finding 1).
    JS: line/block comment scan (independently verified against ripgrep)."""
    try: src = open(path, encoding='utf-8', errors='replace').read()
    except OSError: return 0, 0
    lines = src.splitlines()
    nonblank = {i for i, ln in enumerate(lines, 1) if ln.strip()}
    if path.endswith('.py'):
        import ast as _ast, io as _io, tokenize as _tok
        cmt = set()
        try:
            for t in _tok.generate_tokens(_io.StringIO(src).readline):
                if t.type == _tok.COMMENT: cmt.add(t.start[0])
        except Exception: pass
        try:
            tree = _ast.parse(src)
            for node in _ast.walk(tree):
                if isinstance(node, (_ast.Module, _ast.FunctionDef, _ast.AsyncFunctionDef, _ast.ClassDef)):
                    body = getattr(node, 'body', None)
                    if body and isinstance(body[0], _ast.Expr) and isinstance(getattr(body[0], 'value', None), _ast.Constant) \
                       and isinstance(body[0].value.value, str):
                        d = body[0].value
                        for i in range(d.lineno, (getattr(d, 'end_lineno', d.lineno) or d.lineno) + 1): cmt.add(i)
        except SyntaxError: pass
        cmt &= nonblank
        return len(nonblank - cmt), len(cmt)
    # JS/MJS/CJS
    sloc = com = 0; inblk = False
    for ln in lines:
        t = ln.strip()
        if not t: continue
        if inblk:
            com += 1
            if '*/' in t: inblk = False
            continue
        if t.startswith('/*'):
            com += 1
            if '*/' not in t: inblk = True
            continue
        if t.startswith('//'): com += 1; continue
        sloc += 1
    return sloc, com

def pct(xs, p):
    if not xs: return 0
    xs = sorted(xs); k = (len(xs) - 1) * p / 100
    lo, hi = int(k), min(int(k) + 1, len(xs) - 1)
    return round(xs[lo] + (xs[hi] - xs[lo]) * (k - lo), 1)

results = {}
for aid in ids:
    root = os.path.join(MX, aid)
    prod, test, other = [], [], {}
    manifest = globals().setdefault('MANIFEST', [])
    for dp, dns, fns in os.walk(root):
        dns[:] = [d for d in dns if d not in ('.git', 'node_modules', '__pycache__', '.venv')]
        for fn in fns:
            fp = os.path.join(dp, fn); rel = os.path.relpath(fp, root)
            ext = os.path.splitext(fn)[1].lower()
            if ext in CODE:
                (test if is_test(rel) else prod).append((fp, rel))
                manifest.append((aid, 'test' if is_test(rel) else 'prod', rel, 0))
            elif ext in ('.html', '.css', '.json', '.sql', '.sh', '.md') or fn in ('Makefile',) or fn.startswith('.'):
                if fn in ('Makefile',) or fn.startswith('.'): ext = ext or fn
                # vet MEDIUM: excluded surface must be REPORTED, not silently dropped
                try: n = sum(1 for ln in open(fp, errors='replace') if ln.strip())
                except OSError: n = 0
                key = ext + ('|test' if is_test(rel) else '')
                other[key] = other.get(key, 0) + n
                manifest.append((aid, 'excluded', rel, n))

    def agg(files):
        sl = cm = 0; per_file = []
        for fp, _ in files:
            s, c = sloc_comments(fp); sl += s; cm += c; per_file.append(s)
        return sl, cm, per_file
    p_sloc, p_com, p_files_sloc = agg(prod)
    t_sloc, _, _ = agg(test)

    # lizard: identical machinery for JS+Python (vet: cross-language = descriptive)
    ccn, fnloc = [], []
    if prod:
        cmd = ['python3', '-m', 'lizard', '--csv'] + [fp for fp, _ in prod]
        r = subprocess.run(cmd, capture_output=True, text=True)
        import csv as _csv, io as _io
        for parts in _csv.reader(_io.StringIO(r.stdout)):
            if len(parts) < 3: continue
            try: fnloc.append(int(parts[0])); ccn.append(int(parts[1]))
            except ValueError: continue

    kloc = max(p_sloc, 1) / 1000
    results[aid] = {
        'prod_files': len(prod), 'prod_sloc': p_sloc,
        'test_files': len(test), 'test_sloc': t_sloc,
        'test_to_prod_sloc': round(t_sloc / max(p_sloc, 1), 3),
        'comment_density_pct': round(100 * p_com / max(p_sloc + p_com, 1), 1),
        'files_per_kloc': round(len(prod) / kloc, 1),
        'file_sloc_median': round(st.median(p_files_sloc), 1) if p_files_sloc else 0,
        'file_sloc_p90': pct(p_files_sloc, 90), 'file_sloc_max': max(p_files_sloc) if p_files_sloc else 0,
        'functions': len(ccn),
        'ccn_median': round(st.median(ccn), 1) if ccn else 0, 'ccn_p90': pct(ccn, 90),
        'ccn_gt10_pct': round(100 * sum(1 for c in ccn if c > 10) / max(len(ccn), 1), 1),
        'fn_nloc_p90': pct(fnloc, 90),
        'fn_nloc_gt60_pct': round(100 * sum(1 for n in fnloc if n > 60) / max(len(fnloc), 1), 1),
        'excluded_surface_lines': other,
    }
with open(os.path.join(MX, 'scope-manifest.tsv'), 'w') as mh:
    for row in globals().get('MANIFEST', []): mh.write('\t'.join(str(x) for x in row) + '\n')
json.dump(results, open(os.path.join(MX, 'metrics.json'), 'w'), indent=1)
print('[panel] metrics done')
PY

# --- duplication on a PROD-ONLY mirror (vet HIGH: identical scope) -----------
for id in $IDS; do
  M="$MX/dupmirror-$id"; rm -rf "$M"; mkdir -p "$M"
  ( cd "$MX/$id" && find . \( -name '*.js' -o -name '*.mjs' -o -name '*.cjs' -o -name '*.py' \) \
      ! -path '*/test/*' ! -path '*/tests/*' ! -name 'test_*.py' \
      ! -name '*.test.*' ! -name '*.spec.*' ! -name 'conftest.py' \
      ! -path '*/node_modules/*' -print0 2>/dev/null \
    | while IFS= read -r -d '' f; do mkdir -p "$M/$(dirname "$f")"; cp "$f" "$M/$f"; done )
  npx --yes "$JSCPD_PIN" --min-tokens 50 --reporters json --silent \
      --output "$MX/jscpd-$id" "$M" >/dev/null 2>&1 \
    || { echo "FATAL: jscpd failed for $id" >&2; exit 1; }
  [ -s "$MX/jscpd-$id/jscpd-report.json" ] || { echo "FATAL: no jscpd report for $id" >&2; exit 1; }
done
echo "[panel] duplication done"

# --- git process signals, same scope rules (vet MEDIUM) ---------------------
python3 - "$MX" $IDS <<'PY' > "$MX/git.json"
import json, os, re, subprocess, sys
MX, ids = sys.argv[1], sys.argv[2:]
SRCS = {
 'baseline':'/Users/tyson/codebase/code-agents/loomcli/harbor/trials/codex-baseline-1/slack-clone__BV7wtZv/artifacts/app',
 'run19':'/Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-1/slack-clone__tUAmrE8/artifacts/app',
 'run20':'/Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-2/slack-clone__RbVFjFP/artifacts/app',
 'run21':'/Users/tyson/codebase/code-agents/loomcli/harbor/trials/loom-generic-tasks-dual-1/slack-clone__jxozk75/artifacts/app',
}
TEST_RE = re.compile(r'(^|/)(tests?)/|(^|/)test_[^/]*\.py$|\.(test|spec)\.[cm]?[jt]s$|(^|/)conftest\.py$')
SKIP = re.compile(r'(^|/)(\.git|node_modules|__pycache__|data)/|package-lock\.json|\.lock$|timer\.sh$')
out = {}
for aid in ids:
    d = SRCS[aid]
    if not os.path.isdir(os.path.join(d, '.git')):
        out[aid] = {'has_git': False}; continue
    log = subprocess.run(['git','-C',d,'log','--numstat','--format=@@%H'],
                         capture_output=True, text=True).stdout
    commits = 0; churn = 0; touched = {}; test_coupled = 0; sizes = []
    cur_files = set(); cur_lines = 0
    def flush():
        global commits, churn, test_coupled, cur_lines
        if not cur_files and cur_lines == 0: return
        commits += 1; sizes.append(cur_lines); churn += cur_lines
        if any(TEST_RE.search(f) for f in cur_files) and any(not TEST_RE.search(f) for f in cur_files):
            test_coupled += 1
    for line in log.splitlines():
        if line.startswith('@@'):
            flush(); cur_files = set(); cur_lines = 0; continue
        parts = line.split('\t')
        if len(parts) != 3: continue
        a, dl, f = parts
        if SKIP.search(f) or not f.endswith(('.js','.mjs','.cjs','.py')): continue
        try: n = int(a) + int(dl)
        except ValueError: continue
        cur_files.add(f); cur_lines += n; touched[f] = touched.get(f, 0) + 1
    flush()
    rework = sum(1 for v in touched.values() if v > 1)
    out[aid] = {'has_git': True, 'code_commits': commits,
                'median_commit_lines': __import__('statistics').median(sizes) if sizes else 0,
                'total_churn_lines': churn,
                'files_touched': len(touched),
                'files_rewritten_pct': round(100*rework/max(len(touched),1), 1),
                'test_coupled_commit_pct': round(100*test_coupled/max(commits,1), 1)}
print(json.dumps(out, indent=1))
PY
echo "[panel] git signals done"

# --- assemble ----------------------------------------------------------------
python3 - "$MX" "$OUT" $IDS <<'PY'
import json, os, sys
MX, OUT, ids = sys.argv[1], sys.argv[2], sys.argv[3:]
m = json.load(open(os.path.join(MX,'metrics.json')))
g = json.load(open(os.path.join(MX,'git.json')))
for aid in ids:
    p = os.path.join(MX, f'jscpd-{aid}', 'jscpd-report.json')
    if not os.path.exists(p): raise SystemExit(f'FATAL: missing jscpd report for {aid}')
    dup = json.load(open(p))['statistics']['total']['percentage']  # no silent 0.0 fallback
    m[aid]['dup_pct'] = round(float(dup), 2)
    m[aid]['git'] = g.get(aid, {})
json.dump(m, open(OUT,'w'), indent=1)

LANG = {'baseline':'JS','run19':'JS','run20':'PY','run21':'PY'}
GATES = {'baseline':'2/5','run19':'3/5','run20':'0/5','run21':'0/5'}
rows = [('metric', *ids), ('language', *[LANG[i] for i in ids]), ('gates', *[GATES[i] for i in ids])]
keys = ['prod_files','prod_sloc','files_per_kloc','file_sloc_median','file_sloc_p90','file_sloc_max',
        'functions','ccn_median','ccn_p90','ccn_gt10_pct','fn_nloc_p90','fn_nloc_gt60_pct',
        'dup_pct','comment_density_pct','test_files','test_sloc','test_to_prod_sloc']
for k in keys: rows.append((k, *[str(m[i].get(k,'')) for i in ids]))
for k in ['code_commits','median_commit_lines','total_churn_lines','files_rewritten_pct','test_coupled_commit_pct']:
    rows.append(('git:'+k, *[str(m[i]['git'].get(k,'n/a')) for i in ids]))
w = [max(len(str(r[c])) for r in rows) for c in range(len(ids)+1)]
for r in rows:
    print('  '.join(str(x).ljust(w[i]) for i,x in enumerate(r)))
print()
for aid in ids:
    print(f"excluded surface {aid}: {m[aid]['excluded_surface_lines']}")
print(f"\nresults -> {OUT}")
PY
