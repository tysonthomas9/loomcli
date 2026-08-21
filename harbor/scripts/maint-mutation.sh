#!/usr/bin/env bash
# Stage-2 S5: SAMPLED FAULT INJECTION (codex stage-2-vet: do this now, defer full
# Stryker/mutmut). Seeds N single-point faults into production code, re-runs the
# artifact's OWN suite, and records killed vs survived.
#
# Runs inside the benchmark's own task image (has python3, node, redis, setsid,
# /proc) — codex finding 3: these suites cannot run on macOS. --network none and a
# throwaway container per run: no host ports, no host processes touched.
#
# Killed = the suite reports MORE failures than its own unmutated baseline.
# (Baselines are not all green: run21 fails 3 of its own tests before mutation.)
set -euo pipefail
IMG="${IMG:-docker.io/library/slack-clone__7smcnel__env-main:latest}"
N="${N:-8}"
OUTDIR="${OUTDIR:-$HOME/.mx-stage}"
TRIALS=/Users/tyson/codebase/code-agents/loomcli/harbor/trials

art_path() { case "$1" in
  run19) echo "$TRIALS/loom-generic-tasks-1/slack-clone__tUAmrE8/artifacts/app" ;;
  run20) echo "$TRIALS/loom-generic-tasks-2/slack-clone__RbVFjFP/artifacts/app" ;;
  run21) echo "$TRIALS/loom-generic-tasks-dual-1/slack-clone__jxozk75/artifacts/app" ;;
esac; }
# baseline artifact has ZERO tests -> mutation score is undefined, not zero. Excluded.
SUITE_TIMEOUT="${SUITE_TIMEOUT:-420}"
# Bounded: a mutant that HANGS the suite (observed: run19 test:ws waits forever for
# an event that never arrives) must not block the sweep. Standard mutation-testing
# convention counts a timeout as KILLED; it is tallied separately as well.
suite_cmd() { case "$1" in
  run19) echo "timeout ${SUITE_TIMEOUT}s npm test 2>&1; echo SUITE_RC=\$?" ;;
  run20|run21) echo "timeout ${SUITE_TIMEOUT}s python3 -m unittest discover -s tests 2>&1; echo SUITE_RC=\$?" ;;
esac; }

run_one() { # $1 id, $2 mutation-index (0 = unmutated baseline)
  local id="$1" idx="$2" src; src=$(art_path "$id")
  podman run --rm --network none -v "$src:/src:ro,z" "$IMG" bash -c "
    cp -r /src/. /app/ 2>/dev/null; cd /app
    redis-server --daemonize yes --port 6379 >/dev/null 2>&1 || true
    if [ '$idx' != 0 ]; then
      python3 /dev/stdin '$idx' <<'MUT'
import ast, os, random, re, sys
idx = int(sys.argv[1])
TEST = re.compile(r'(^|/)(tests?)/|(^|/)test_[^/]*\.py\$|\.(test|spec)\.[cm]?[jt]s\$|(^|/)conftest\.py\$')
files = []
for dp, dns, fns in os.walk('/app'):
    dns[:] = [d for d in dns if d not in ('.git','node_modules','__pycache__','data','public','static')]
    for fn in fns:
        if fn.endswith(('.py','.js','.mjs')):
            rel = os.path.relpath(os.path.join(dp,fn), '/app')
            if not TEST.search(rel): files.append(os.path.join(dp,fn))
files.sort()
# deterministic per index: same mutation set every run, reproducible
rnd = random.Random(1000 + idx)
OPS = [('==','!='),('!=','=='),(' < ',' >= '),(' > ',' <= '),(' <= ',' > '),(' >= ',' < '),
       ('True','False'),('False','True'),('true','false'),('false','true'),(' + ',' - ')]
for _ in range(400):
    f = rnd.choice(files)
    try: txt = open(f, encoding='utf-8', errors='replace').read()
    except OSError: continue
    a, b = rnd.choice(OPS)
    hits = [m.start() for m in re.finditer(re.escape(a), txt)]
    if not hits: continue
    p = rnd.choice(hits)
    open(f,'w',encoding='utf-8').write(txt[:p] + b + txt[p+len(a):])
    print(f'MUTATION idx={idx} file={os.path.relpath(f,\"/app\")} op={a.strip()}->{b.strip()} pos={p}', flush=True)
    break
else:
    print(f'MUTATION idx={idx} NONE-APPLIED', flush=True)
MUT
    fi
    $(suite_cmd "$id")
  " 2>&1
}

for id in "${@:-run19 run20 run21}"; do
  echo "=== $id: unmutated baseline ==="
  run_one "$id" 0 > "$OUTDIR/mut-$id-base.log" 2>&1 || true
  BASE_RC=$(grep -m1 -oE 'SUITE_RC=[0-9]+' "$OUTDIR/mut-$id-base.log" | cut -d= -f2 || echo 0); BASE_RC=${BASE_RC:-0}
  BASE_FAIL=$(grep -oE 'failures=[0-9]+|errors=[0-9]+' "$OUTDIR/mut-$id-base.log" | grep -oE '[0-9]+' | paste -sd+ - | bc 2>/dev/null || echo 0)
  BASE_FAIL=${BASE_FAIL:-0}
  echo "$id baseline suite_rc=$BASE_RC failure-count=$BASE_FAIL"
  [ "$BASE_RC" = 0 ] || echo "  NOTE: baseline suite is NOT green (rc=$BASE_RC); kills are judged against this baseline"
  echo "$id,baseline,$BASE_FAIL" > "$OUTDIR/mut-$id.csv"
  killed=0; survived=0; noop=0
  for i in $(seq 1 "$N"); do
    run_one "$id" "$i" > "$OUTDIR/mut-$id-$i.log" 2>&1 || true
    MUT=$(grep -m1 '^MUTATION' "$OUTDIR/mut-$id-$i.log" || echo 'MUTATION NONE')
    F=$(grep -oE 'failures=[0-9]+|errors=[0-9]+' "$OUTDIR/mut-$id-$i.log" | grep -oE '[0-9]+' | paste -sd+ - | bc 2>/dev/null || echo 0)
    F=${F:-0}
    HARD=$(grep -cE 'Traceback|FAILED|npm ERR|not ok' "$OUTDIR/mut-$id-$i.log" || true)
    RC=$(grep -m1 -oE 'SUITE_RC=[0-9]+' "$OUTDIR/mut-$id-$i.log" | cut -d= -f2 || echo 0); RC=${RC:-0}
    if echo "$MUT" | grep -q 'NONE'; then noop=$((noop+1)); verdict=noop
    elif [ "$RC" = 124 ]; then killed=$((killed+1)); verdict=killed-timeout
    elif [ "$RC" != "$BASE_RC" ] && [ "$RC" != 0 ]; then killed=$((killed+1)); verdict=killed-rc$RC
    elif [ "$F" -gt "$BASE_FAIL" ] 2>/dev/null; then killed=$((killed+1)); verdict=killed-morefails
    else survived=$((survived+1)); verdict=survived
    fi
    echo "  [$i/$N] $verdict  ($(echo "$MUT" | sed 's/^MUTATION //' | cut -c1-90))"
    echo "$id,$i,$verdict,rc=$RC,fails=$F,$(echo "$MUT" | sed 's/,/;/g')" >> "$OUTDIR/mut-$id.csv"
  done
  applied=$((killed+survived))
  score=$([ "$applied" -gt 0 ] && echo "scale=0; 100*$killed/$applied" | bc || echo n/a)
  echo "=== $id MUTATION SCORE: $killed killed / $applied applied = ${score}% (noop=$noop) ==="
  echo "$id,SCORE,$killed,$applied,$score" >> "$OUTDIR/mut-$id.csv"
done
