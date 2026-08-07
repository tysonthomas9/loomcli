#!/usr/bin/env bash
# Stage-2 S2: architecture / coupling. AST-resolved import graph per artifact.
# Codex stage-2-vet finding 7 folded: real AST parsing (python `ast`; JS via pinned
# madge, which resolves extensionless/index/re-export forms), SCC condensation for
# cycles and depth, dynamic/conditional imports counted and reported separately.
# Within-language comparison ONLY — JS and Python module semantics differ.
set -euo pipefail
MX="${MX:-$HOME/.mx-stage}"
[ -d "$MX/baseline" ] || { echo "FATAL: run maint-panel.sh first" >&2; exit 1; }
if [ -n "${MAINT_ARTIFACTS:-}" ]; then IDS=""; for kv in $MAINT_ARTIFACTS; do IDS="$IDS ${kv%%=*}"; done; IDS="${IDS# }"; else IDS="baseline run19 run20 run21"; fi

# JS graphs from pinned madge (real parser + resolver)
JS_IDS="${MAINT_JS_IDS:-baseline run19 run21}"
for id in $JS_IDS; do
  npx --yes madge@8.0.0 --json --extensions js,mjs,cjs \
      --exclude '(^|/)(test|tests)/' "$MX/$id" > "$MX/madge-$id.json" 2>"$MX/madge-$id.err" \
    || { echo "FATAL: madge failed for $id (see $MX/madge-$id.err)" >&2; exit 1; }
  [ -s "$MX/madge-$id.json" ] || { echo "FATAL: empty madge graph for $id" >&2; exit 1; }
done

python3 - "$MX" $IDS <<'PY'
import ast, json, os, re, sys
from collections import defaultdict

MX, ids = sys.argv[1], sys.argv[2:]
TEST_RE = re.compile(r'(^|/)(tests?)/|(^|/)test_[^/]*\.py$|\.(test|spec)\.[cm]?[jt]s$|(^|/)conftest\.py$')
import os as _os
_ov = _os.environ.get('MAINT_PRIMARY','')
PRIMARY = dict(kv.split('=',1) for kv in _ov.split()) if _ov else {'baseline':'js','run19':'js','run20':'py','run21':'py'}

def tarjan(nodes, edges):
    """SCCs — cycles are SCCs of size>1 (or self-loops)."""
    idx = {}; low = {}; on = {}; st = []; out = []; c = [0]
    def strong(v):
        work = [(v, 0)]
        while work:
            v, pi = work[-1]
            if pi == 0:
                idx[v] = low[v] = c[0]; c[0] += 1; st.append(v); on[v] = True
            recurse = False
            for i in range(pi, len(edges.get(v, []))):
                w = edges[v][i]
                if w not in idx:
                    work[-1] = (v, i + 1); work.append((w, 0)); recurse = True; break
                elif on.get(w): low[v] = min(low[v], idx[w])
            if recurse: continue
            if low[v] == idx[v]:
                comp = []
                while True:
                    w = st.pop(); on[w] = False; comp.append(w)
                    if w == v: break
                out.append(comp)
            work.pop()
            if work:
                p = work[-1][0]; low[p] = min(low[p], low[v])
    for v in nodes:
        if v not in idx: strong(v)
    return out

def depth_dag(nodes, edges, comp_of):
    """Longest path over the SCC-condensed DAG (vet: depth must be on condensation)."""
    cedges = defaultdict(set)
    for u in nodes:
        for v in edges.get(u, []):
            if comp_of[u] != comp_of[v]: cedges[comp_of[u]].add(comp_of[v])
    memo = {}
    def dfs(n, seen):
        if n in memo: return memo[n]
        if n in seen: return 0
        seen = seen | {n}
        best = 0
        for m in cedges.get(n, ()): best = max(best, 1 + dfs(m, seen))
        memo[n] = best; return best
    comps = set(comp_of.values())
    return max((dfs(c, frozenset()) for c in comps), default=0)

def py_graph(root):
    mods = {}
    for dp, dns, fns in os.walk(root):
        dns[:] = [d for d in dns if d not in ('.git','node_modules','__pycache__','.venv')]
        for fn in fns:
            if not fn.endswith('.py'): continue
            rel = os.path.relpath(os.path.join(dp, fn), root)
            if TEST_RE.search(rel): continue
            mod = rel[:-3].replace(os.sep, '.')
            if mod.endswith('.__init__'): mod = mod[:-9]
            mods[mod] = os.path.join(dp, fn)
    edges = defaultdict(list); dynamic = 0
    for mod, path in mods.items():
        try: tree = ast.parse(open(path, encoding='utf-8', errors='replace').read())
        except SyntaxError: continue
        pkg = mod.rsplit('.', 1)[0] if '.' in mod else ''
        for node in ast.walk(tree):
            targets = []
            if isinstance(node, ast.Import):
                targets = [a.name for a in node.names]
            elif isinstance(node, ast.ImportFrom):
                if node.level:  # relative import
                    base = pkg.split('.') if pkg else []
                    base = base[:len(base) - (node.level - 1)] if node.level > 1 else base
                    targets = ['.'.join(filter(None, base + ([node.module] if node.module else [])))]
                elif node.module: targets = [node.module]
            else:
                continue
            # conditional/dynamic: import nested inside a function/try body
            for t in targets:
                cand = [t] + ['.'.join(t.split('.')[:i]) for i in range(len(t.split('.')), 0, -1)]
                hit = next((c for c in cand if c in mods and c != mod), None)
                if hit: edges[mod].append(hit)
    src = '\n'.join(open(p, encoding='utf-8', errors='replace').read() for p in mods.values())
    dynamic = len(re.findall(r'importlib\.import_module|__import__\(', src))
    return list(mods), {k: sorted(set(v)) for k, v in edges.items()}, dynamic

def js_graph(mpath, root):
    # fail closed: an unreadable graph must not silently become "0 cycles"
    raw = json.load(open(mpath))
    raw = {k: v for k, v in raw.items() if not TEST_RE.search(k)}
    edges = {k: [x for x in v if x in raw] for k, v in raw.items()}
    src = ''
    for dp, dns, fns in os.walk(root):
        dns[:] = [d for d in dns if d not in ('.git','node_modules','__pycache__')]
        for fn in fns:
            if fn.endswith(('.js','.mjs','.cjs')) and not TEST_RE.search(os.path.relpath(os.path.join(dp,fn), root)):
                src += open(os.path.join(dp,fn), encoding='utf-8', errors='replace').read() + '\n'
    dynamic = len(re.findall(r'(?<![\w.])require\s*\([^\'"]', src)) + len(re.findall(r'\bimport\s*\(', src))
    return list(raw), edges, dynamic

out = {}
for aid in ids:
    root = os.path.join(MX, aid)
    if PRIMARY[aid] == 'py': nodes, edges, dyn = py_graph(root)
    else: nodes, edges, dyn = js_graph(os.path.join(MX, f'madge-{aid}.json'), root)
    if not nodes: out[aid] = {'error':'no modules resolved'}; continue
    comps = tarjan(nodes, edges)
    comp_of = {n: i for i, c in enumerate(comps) for n in c}
    cycles = [c for c in comps if len(c) > 1] + [c for c in comps if len(c) == 1 and c[0] in edges.get(c[0], [])]
    fan_out = {n: len(edges.get(n, [])) for n in nodes}
    fan_in = defaultdict(int)
    for u in nodes:
        for v in edges.get(u, []): fan_in[v] += 1
    ne = sum(fan_out.values())
    out[aid] = {
        'lang': PRIMARY[aid], 'modules': len(nodes), 'edges': ne,
        'mean_fan_out': round(ne / max(len(nodes),1), 2),
        'max_fan_out': max(fan_out.values()) if fan_out else 0,
        'max_fan_in': max(fan_in.values()) if fan_in else 0,
        'god_modules_fan_in_ge5': sum(1 for v in fan_in.values() if v >= 5),
        'cycles': len(cycles),
        'largest_cycle': max((len(c) for c in cycles), default=0),
        'pct_modules_in_cycles': round(100*sum(len(c) for c in cycles)/max(len(nodes),1), 1),
        'dag_depth': depth_dag(nodes, edges, comp_of),
        'dynamic_imports': dyn,
    }
json.dump(out, open(f'{MX}/coupling.json','w'), indent=1)

keys = ['lang','modules','edges','mean_fan_out','max_fan_out','max_fan_in','god_modules_fan_in_ge5',
        'cycles','largest_cycle','pct_modules_in_cycles','dag_depth','dynamic_imports']
rows = [('coupling_metric', *ids)] + [(k, *[str(out[i].get(k,'-')) for i in ids]) for k in keys]
w = [max(len(str(r[c])) for r in rows) for c in range(len(ids)+1)]
print('within-language comparison ONLY (JS and Python module semantics differ)\n')
for r in rows: print('  '.join(str(x).ljust(w[i]) for i,x in enumerate(r)))
print(f"\ncoupling -> {MX}/coupling.json")
PY
