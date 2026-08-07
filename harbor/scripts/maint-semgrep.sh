#!/usr/bin/env bash
# Stage-2 S3: Semgrep over the SAME prod-only scope the panel used.
# Codex stage-2-vet: pinned tool image (no cold host install), tests/data excluded.
set -euo pipefail
MX="${MX:-$HOME/.mx-stage}"
IMG=docker.io/semgrep/semgrep:1.171.0
if [ -n "${MAINT_ARTIFACTS:-}" ]; then IDS=""; for kv in $MAINT_ARTIFACTS; do IDS="$IDS ${kv%%=*}"; done; IDS="${IDS# }"; else IDS="baseline run19 run20 run21"; fi
for _id in $IDS; do [ -d "$MX/dupmirror-$_id" ] || { echo "FATAL: run maint-panel.sh first (missing dupmirror-$_id)" >&2; exit 1; }; done
for id in $IDS; do
  echo "[semgrep] $id"
  podman run --rm -v "$MX/dupmirror-$id:/src:ro,z" "$IMG" \
    semgrep --config p/default --config p/security-audit --json --quiet --metrics off /src \
    > "$MX/semgrep-$id.json" 2>"$MX/semgrep-$id.err" \
    || { echo "FATAL: semgrep failed for $id"; tail -3 "$MX/semgrep-$id.err"; exit 1; }
done
python3 - "$MX" $IDS <<'PY'
import json, sys
from collections import Counter
MX, ids = sys.argv[1], sys.argv[2:]
panel = json.load(open(f'{MX}/results.json'))
out = {}
for aid in ids:
    d = json.load(open(f'{MX}/semgrep-{aid}.json'))
    res = d.get('results', [])
    sev = Counter(r['extra'].get('severity','INFO') for r in res)
    kloc = max(panel[aid]['prod_sloc'],1)/1000
    out[aid] = {'total': len(res), 'per_kloc': round(len(res)/kloc,1),
                'ERROR': sev.get('ERROR',0), 'WARNING': sev.get('WARNING',0), 'INFO': sev.get('INFO',0),
                'top_rules': [f'{k} x{v}' for k,v in Counter(r['check_id'].split('.')[-1] for r in res).most_common(4)],
                'errors_in_scan': len(d.get('errors', []))}
json.dump(out, open(f'{MX}/semgrep.json','w'), indent=1)
keys=['total','per_kloc','ERROR','WARNING','INFO','errors_in_scan']
rows=[('semgrep_metric',*ids)]+[(k,*[str(out[i][k]) for i in ids]) for k in keys]
w=[max(len(str(r[c])) for r in rows) for c in range(len(ids)+1)]
for r in rows: print('  '.join(str(x).ljust(w[i]) for i,x in enumerate(r)))
print()
for i in ids: print(f'{i} top rules: {out[i]["top_rules"]}')
print(f'\nsemgrep -> {MX}/semgrep.json')
PY
