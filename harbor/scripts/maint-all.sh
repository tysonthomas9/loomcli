#!/usr/bin/env bash
# One-command quality scorecard for a set of trial artifacts.
#
#   harbor/scripts/maint-all.sh [--with-sonar] [--with-mutation] [id=path ...]
#
# With no artifact args it scores the four reference artifacts (see QUALITY.md).
# Pass id=path pairs to score new runs, e.g.
#   maint-all.sh loom-generic-tasks-dual-1=/path/to/artifacts/app
#
# Fast tier (always): panel (complexity/duplication/size/git) + coupling + semgrep.
# Slow tiers (opt-in): --with-sonar (~10 min, boots SonarQube), --with-mutation
# (~15 min per artifact, runs the artifact's own suite in the task image).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
MX="${MX:-$HOME/.mx-stage}"
WITH_SONAR=0; WITH_MUT=0; PAIRS=()
for a in "$@"; do
  case "$a" in
    --with-sonar) WITH_SONAR=1 ;;
    --with-mutation) WITH_MUT=1 ;;
    *=*) PAIRS+=("$a") ;;
    *) echo "unknown arg: $a" >&2; exit 1 ;;
  esac
done
[ ${#PAIRS[@]} -gt 0 ] && export MAINT_ARTIFACTS="${PAIRS[*]}"

echo "== panel =="   ; bash "$HERE/maint-panel.sh"
echo "== coupling ==" ; bash "$HERE/maint-coupling.sh"
echo "== semgrep ==" ; bash "$HERE/maint-semgrep.sh"
[ "$WITH_SONAR" = 1 ] && { echo "== sonar =="; bash "$HERE/maint-sonar.sh"; }
[ "$WITH_MUT" = 1 ]   && { echo "== mutation =="; N="${N:-8}" bash "$HERE/maint-mutation.sh"; }

python3 - "$MX" <<'PY'
import json, os, sys, glob
MX = sys.argv[1]
def load(n):
    p = os.path.join(MX, n)
    return json.load(open(p)) if os.path.exists(p) else {}
panel, coup, semg, sonar = load('results.json'), load('coupling.json'), load('semgrep.json'), load('sonar.json')
mut = {}
for f in glob.glob(os.path.join(MX, 'mut-*.csv')):
    aid = os.path.basename(f)[4:-4]
    for line in open(f):
        p = line.strip().split(',')
        if len(p) >= 5 and p[1] == 'SCORE':
            mut[aid] = {'killed': int(p[2]), 'applied': int(p[3]), 'score_pct': p[4]}
card = {}
for aid in panel:
    card[aid] = {
        'size': {k: panel[aid].get(k) for k in ('prod_files','prod_sloc','file_sloc_median','file_sloc_max')},
        'complexity': {k: panel[aid].get(k) for k in ('ccn_p90','ccn_gt10_pct','fn_nloc_p90')},
        'duplication_pct': panel[aid].get('dup_pct'),
        'comment_density_pct': panel[aid].get('comment_density_pct'),
        'tests': {'files': panel[aid].get('test_files'), 'ratio': panel[aid].get('test_to_prod_sloc'),
                  'mutation': mut.get(aid)},
        'architecture': {k: coup.get(aid, {}).get(k) for k in ('modules','cycles','largest_cycle','pct_modules_in_cycles','max_fan_in','dag_depth')},
        'semgrep_per_kloc': semg.get(aid, {}).get('per_kloc'),
        'sonar': {k: sonar.get(aid, {}).get(k) for k in ('sqale_debt_ratio','code_smells','ncloc')} if sonar else None,
        'process': panel[aid].get('git'),
    }
json.dump(card, open(os.path.join(MX, 'scorecard.json'), 'w'), indent=1)
ids = list(card)
rows = [('scorecard', *ids)]
def g(a, *path):
    v = card[a]
    for k in path:
        v = (v or {}).get(k) if isinstance(v, dict) else None
    return '-' if v is None else str(v)
for label, path in [('median file SLOC',('size','file_sloc_median')), ('max file SLOC',('size','file_sloc_max')),
                    ('CCN>10 %',('complexity','ccn_gt10_pct')), ('duplication %',('duplication_pct',)),
                    ('test ratio',('tests','ratio')), ('mutation %',('tests','mutation','score_pct')),
                    ('circular deps',('architecture','cycles')), ('% mods in cycles',('architecture','pct_modules_in_cycles')),
                    ('semgrep/KLOC',('semgrep_per_kloc',)), ('sonar debt ratio',('sonar','sqale_debt_ratio'))]:
    rows.append((label, *[g(a, *path) for a in ids]))
w = [max(len(str(r[c])) for r in rows) for c in range(len(ids)+1)]
for r in rows: print('  '.join(str(x).ljust(w[i]) for i, x in enumerate(r)))
print(f"\nscorecard -> {MX}/scorecard.json")
PY
