#!/usr/bin/env bash
# Standards-anchored stage: SonarQube CE (SQALE / ISO 25010-aligned) over the same
# staged scopes the identical-machinery panel used. Run maint-panel.sh FIRST.
# Codex-vet fixes folded: wait for the CE task to reach SUCCESS (not just an empty
# queue), scripted admin-password rotation, recorded server/plugin versions,
# $HOME staging (podman applehv only auto-mounts $HOME).
set -euo pipefail

MX="${MX:-$HOME/.mx-stage}"
IDS="baseline run19 run20 run21"
PW='MxEval12345_'
NET=sonarnet
[ -d "$MX/baseline" ] || { echo "FATAL: run maint-panel.sh first (missing $MX/baseline)" >&2; exit 1; }

podman network create "$NET" >/dev/null 2>&1 || true
podman rm -f sonarqube >/dev/null 2>&1 || true
podman run -d --name sonarqube --network "$NET" -p 9000:9000 \
  -e SONAR_ES_BOOTSTRAP_CHECKS_DISABLE=true docker.io/library/sonarqube:community >/dev/null
echo "[sonar] booting…"
for i in $(seq 1 90); do
  curl -sf http://localhost:9000/api/system/status 2>/dev/null | grep -q '"UP"' && break
  sleep 5
  [ "$i" = 90 ] && { echo "FATAL: SonarQube never came UP" >&2; podman logs --tail 30 sonarqube >&2; exit 1; }
done
echo "[sonar] UP after ~$((i*5))s"

curl -sf -u admin:admin -X POST \
  "http://localhost:9000/api/users/change_password?login=admin&previousPassword=admin&password=$PW" >/dev/null 2>&1 || true
TOKEN=$(curl -sf -u "admin:$PW" -X POST "http://localhost:9000/api/user_tokens/generate?name=mx$RANDOM" | jq -r .token)
[ -n "$TOKEN" ] && [ "$TOKEN" != null ] || { echo "FATAL: token generation failed" >&2; exit 1; }
curl -sf -u "admin:$PW" http://localhost:9000/api/server/version > "$MX/sonar-version.txt" || true
echo "[sonar] version $(cat "$MX/sonar-version.txt" 2>/dev/null) token ok"

for id in $IDS; do
  echo "[sonar] scanning $id"
  podman run --rm --network "$NET" \
    -v "$MX/$id:/usr/src:ro,z" \
    -e SONAR_HOST_URL=http://sonarqube:9000 \
    -e SONAR_TOKEN="$TOKEN" \
    docker.io/sonarsource/sonar-scanner-cli \
      -Dsonar.projectKey="mx-$id" -Dsonar.projectName="mx-$id" \
      -Dsonar.sources=. \
      -Dsonar.exclusions='**/node_modules/**,**/data/**,**/*.min.js' \
      -Dsonar.tests='' \
      -Dsonar.scm.disabled=true > "$MX/sonar-scan-$id.log" 2>&1 \
    || { echo "FATAL: scanner nonzero for $id (see $MX/sonar-scan-$id.log)" >&2; tail -5 "$MX/sonar-scan-$id.log" >&2; exit 1; }
  # vet fix: poll the submitted CE task to a terminal state, not just queue length
  TASK=$(grep -oE 'task\?id=[A-Za-z0-9_-]+' "$MX/sonar-scan-$id.log" | tail -1 | cut -d= -f2 || true)
  for j in $(seq 1 60); do
    if [ -n "$TASK" ]; then
      ST=$(curl -sf -u "admin:$PW" "http://localhost:9000/api/ce/task?id=$TASK" | jq -r .task.status 2>/dev/null || echo '')
    else
      ST=$(curl -sf -u "admin:$PW" "http://localhost:9000/api/ce/component?component=mx-$id" \
           | jq -r 'if (.queue|length)==0 and (.current.status? // "SUCCESS")=="SUCCESS" then "SUCCESS" else "PENDING" end' 2>/dev/null || echo '')
    fi
    case "$ST" in
      SUCCESS) break ;;
      FAILED|CANCELED) echo "FATAL: CE task $ST for $id" >&2; exit 1 ;;
    esac
    sleep 5
  done
  [ "$ST" = SUCCESS ] || { echo "FATAL: CE task never reached SUCCESS for $id (last=${ST:-unknown})" >&2; exit 1; }
  curl -sf -u "admin:$PW" "http://localhost:9000/api/ce/component?component=mx-$id" > "$MX/sonar-ce-$id.json" 2>/dev/null || true
  echo "  analysis $id -> $ST"
done

METRICS='sqale_index,sqale_debt_ratio,sqale_rating,code_smells,cognitive_complexity,complexity,duplicated_lines_density,comment_lines_density,ncloc,files,functions,reliability_rating,security_rating,bugs,vulnerabilities'
python3 - "$MX" "$PW" "$METRICS" $IDS <<'PY'
import json, subprocess, sys
MX, PW, METRICS, ids = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
out = {}
for aid in ids:
    r = subprocess.run(['curl','-sf','-u',f'admin:{PW}',
        f'http://localhost:9000/api/measures/component?component=mx-{aid}&metricKeys={METRICS}'],
        capture_output=True, text=True)
    try: ms = json.loads(r.stdout)['component']['measures']
    except Exception: out[aid] = {'error': 'no measures'}; continue
    out[aid] = {m['metric']: m.get('value') for m in ms}
json.dump(out, open(f'{MX}/sonar.json','w'), indent=1)

RATING = {'1.0':'A','2.0':'B','3.0':'C','4.0':'D','5.0':'E'}
keys = ['ncloc','files','functions','sqale_rating','sqale_debt_ratio','sqale_index','code_smells',
        'cognitive_complexity','duplicated_lines_density','comment_lines_density','bugs','vulnerabilities']
rows = [('sonar_metric', *ids)]
for k in keys:
    vals = []
    for i in ids:
        v = out[i].get(k, '-')
        if k == 'sqale_rating' and v in RATING: v = RATING[v]
        vals.append(str(v))
    rows.append((k, *vals))
# density normalisation (vet: absolute counts meaningless across 3..56-file artifacts)
sm = [out[i].get('code_smells'), out[i].get('ncloc')] and None
dens = []
for i in ids:
    try: dens.append(str(round(1000*float(out[i]['code_smells'])/float(out[i]['ncloc']), 1)))
    except Exception: dens.append('-')
rows.append(('code_smells_per_kloc', *dens))
cog = []
for i in ids:
    try: cog.append(str(round(1000*float(out[i]['cognitive_complexity'])/float(out[i]['ncloc']), 1)))
    except Exception: cog.append('-')
rows.append(('cognitive_per_kloc', *cog))
print('scope: WHOLE staged analyzable tree (prod+tests+assets) — NOT the panel prod-only scope;')
print('compare Sonar rows only against other Sonar rows, and only within language.\n')
w = [max(len(str(r[c])) for r in rows) for c in range(len(ids)+1)]
for r in rows: print('  '.join(str(x).ljust(w[i]) for i,x in enumerate(r)))
print(f"\nsonar -> {MX}/sonar.json")
PY
podman rm -f sonarqube >/dev/null 2>&1 || true
echo "[sonar] done (server removed)"
