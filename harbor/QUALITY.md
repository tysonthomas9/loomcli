# Code-quality / maintainability evaluation of benchmark artifacts

SWE-Marathon scores **behavior** (5 binary correctness gates + a CUA UX rubric).
It is completely blind to whether the code is maintainable. This document records
a maintainability evaluation run over four artifacts built to the *same*
specification by four different AI processes, the methodology (which was
adversarially vetted three times), the results, and what they do and do not
support.

Provenance: three independent designs (standards-anchored / scriptable-metrics /
hybrid-judge) were generated, each vetted by the codex CLI, merged into one
methodology, executed, and then the *numbers themselves* were vetted twice more.
Vet reports: `docs/codex-maintainability-numbers-vet.md`,
`docs/codex-maintainability-numbers-vet-round2.md`,
`docs/codex-stage2-plan-vet.md` in the trials evidence repo.

## The artifacts

| id | process | lang | prod files / SLOC | own tests | benchmark result |
|---|---|---|---|---|---|
| baseline | single-session codex, 45 min, $3 | JS | 3 / 1236 | **none** | 2/5 gates, API 109/129 |
| run19 | ensemble, lead + workers + product QA | JS | 49 / 5775 | 14 files / 2591 | 3/5 gates, API 103/129 |
| run20 | ensemble, 1 QA agent | PY | 19 / 5452 | 11 files / 2687 | 0/5 gates, API 107/129 |
| run21 | ensemble, 2 QA agents (+backend QA) | PY | 35 / 4514 | 19 files / 2009 | 0/5 gates, API 93/129 |

## Instruments

| script | tool | what it measures | scope |
|---|---|---|---|
| `scripts/maint-panel.sh` | lizard 1.23.0, jscpd 5.0.14, AST SLOC counter, git log | complexity, duplication, size/structure, comment density, process signals | production code only |
| `scripts/maint-sonar.sh` | SonarQube Community 26.8 | SQALE debt ratio, smell density, cognitive complexity, bugs | whole staged tree |
| `scripts/maint-coupling.sh` | python `ast` + madge 8.0.0 | import graph: cycles (SCC), fan-in/out, DAG depth | production code, within-language |
| `scripts/maint-semgrep.sh` | semgrep 1.171.0 (pinned image) | security/correctness patterns, `p/default` + `p/security-audit` | production code only |
| `scripts/maint-mutation.sh` | sampled fault injection, benchmark task image | test EFFECTIVENESS: killed vs survived mutants | artifact's own suite |

Run order: `maint-panel.sh` first (it stages the scopes everything else reuses),
then any of the others. Mutation runs inside the benchmark's own task image
because these suites need Linux, redis, `setsid` and `/proc` — they cannot run
on macOS.

## Results

| metric | baseline (JS) | run19 (JS) | run20 (PY) | run21 (PY) |
|---|---|---|---|---|
| prod files / SLOC | 3 / 1236 | 49 / 5775 | 19 / 5452 | 35 / 4514 |
| median file SLOC | 286 | **83** | 206 | 103 |
| max file SLOC | 924 | 481 | **1269** | 463 |
| CCN>10 % | 1.9 | 2.5 | 5.0 | 4.1 |
| **max function CCN** | 35 | **29** | **130** (`dispatch`) | 97 (`dispatch_api`) |
| duplication % | 0.0 | 1.1 | 1.11 | 0.65 |
| comment density % | 0.0 | 0.0 | 0.5 | 0.0 |
| test:code SLOC | 0.0 | 0.449 | 0.493 | 0.445 |
| **circular deps** | 0 (3 isolated files) | **0 of 49 modules** | 1 (2 mods, 11%) | 1 (**7 mods, 25%**) |
| max fan-in | 0 | 31 | 14 | 19 |
| DAG depth | 0 | 7 | 6 | 6 |
| Sonar debt ratio | 1.6 | 0.5 | 0.6 | **0.2** |
| Sonar smells/KLOC | 28.1 | 10.8 | **49.6** | **6.2** |
| Sonar cognitive/KLOC | **515.8** | 149.8 | 176.1 | 154.1 |
| Semgrep findings/KLOC | **4.0** | **0.3** | 0.9 | 1.1 |
| **mutation score** | n/a (no tests) | 62% (5/8) | **75%** (6/8) | 62% (5/8) |
| own suite green? | n/a | yes | yes | **no — 3 of 74 fail** |

### Findings

1. **For the JS pair the ensemble genuinely produces better code.** run19 beats
   the single-session baseline on every quality instrument — 13x lower Semgrep
   density, 2.6x lower smell density, 3.4x lower cognitive complexity per KLOC,
   files a third the size — *and* scores higher on the independent gates (3/5 vs
   2/5), so it is not winning by implementing less. Its 49 modules form a
   **completely acyclic** graph (independently confirmed by `madge --circular`),
   so the fine decomposition is real structure, not a tangle sliced thinner.
2. **The second QA agent did not improve quality.** run21 is cleaner on code
   shape than run20 (8x lower smell density, half the median file size) but
   worse on every correctness-discipline measure: a 7-module dependency cycle
   entangling 25% of the codebase, a lower mutation score, three of its own
   tests failing, and the lowest independent API score of all four.
3. **Test volume is not test effectiveness, and is inversely related to
   verified correctness here.** The artifact with zero tests scores *highest* on
   the independent API suite (109/129); the artifact with the most test files
   scores *lowest* (93/129). Mutation scores cluster at 62-75% regardless of how
   much test code was written.
4. **Nobody writes comments.** All four artifacts are at or below 0.5% comment
   density. (An earlier 7.6%/12.8% figure for the Python artifacts was a
   measurement bug — see below.)
5. **A mutation in run19's `realtime-hub.js` made its WebSocket test hang
   forever rather than fail.** In CI that is a stuck pipeline, not a red build.
6. **Both Python artifacts hide a god-function behind healthy aggregates.**
   run20's `dispatch` (http_api.py) has cyclomatic complexity **130** and
   run21's `dispatch_api` has **97**, while their CCN>10 rates look benign at
   5.0% and 4.1%. Aggregates conceal exactly the function a maintainer would
   dread; per-function evidence is what matters. Added as `ccn_max` /
   `ccn_max_where` only because a vet demanded thresholds be measurable.

## Interpretation rules (binding)

- **Advantage claims only within a language pair**: baseline-vs-run19 (JS) and
  run20-vs-run21 (Python). Cross-language numbers are descriptive only — JS and
  Python differ in verbosity, idiom, function boundaries and tokenization even
  under identical tooling.
- **Differing functionality blocks advantage claims.** run20-vs-run21 is an
  unusually clean comparison because both scored 0/5 gates.
- **Density normalisation reduces but does not neutralise** the 3-to-56-file
  size confound. Metrics rewarding "many small files" partly reward
  decomposition style.
- **n = 1 per configuration.** These are artifact-level contrasts, not proven
  process effects.
- **Sonar rows compare only to Sonar rows** (different scope: whole tree vs
  production-only), and its A-E rating saturates — all four scored A.
- Automated maintainability metrics correlate weakly with expert judgment.
  These are evidence, not verdicts.

## Measurement bugs found and fixed (do not reintroduce)

Every one of these was caught by adversarial vetting or by verifying a
suspicious number, not by the code working as intended.

1. **Python SQL literals counted as comments** (HIGH). Any triple-quoted block
   starting on its own line was treated as a docstring, so embedded SQL scored
   as documentation: run20's `storage.py` measured 880 SLOC / 398 comments
   versus the AST truth of 1269 / 9. Fixed with `tokenize` + `ast`. This
   invalidated a published conclusion.
2. **`.mjs` files invisible** — run19's 14 test files were all `.mjs`, so it
   reported "0 tests" for a repo with a full suite.
3. **JS kills scored as survivors** — the mutation classifier only recognised
   unittest-style `failures=N`, which Node never prints, so suites exiting
   non-zero counted as "survived". Now the suite **exit code** is the primary
   signal, compared against that artifact's own baseline exit code.
4. **run19's baseline was never green** — `test:runtime` and
   `test:cluster-realtime` fail unmutated because they expect redis under the
   app's own `.runtime/pids`. run19 is scored on `npm test` only; documented,
   not silently dropped.
5. **Unbounded suite runs** — a mutant that hangs blocked the sweep forever.
   All suite invocations are now bounded; a timeout counts as killed (standard
   convention) and is tallied separately.
6. **jscpd failure could masquerade as 0% duplication** — now hard-fails.
7. **Upper median instead of true median** for commit size.
8. **Editing a running bash script corrupts it** — bash reads scripts
   incrementally; a mid-flight edit made the running process mis-parse and two
   writers appended to the same CSV. Check nothing is running before editing.

## Reproducing

```sh
bash harbor/scripts/maint-panel.sh      # stages scopes; ~3 min
bash harbor/scripts/maint-coupling.sh   # ~2 min
bash harbor/scripts/maint-semgrep.sh    # ~5 min, pinned container
bash harbor/scripts/maint-sonar.sh      # ~10 min, podman, boots SonarQube
N=8 bash harbor/scripts/maint-mutation.sh run19 run20 run21   # ~60 min
```

Results land in `~/.mx-stage/` as `results.json`, `coupling.json`,
`semgrep.json`, `sonar.json`, `mut-*.csv`, plus a per-file `scope-manifest.tsv`
so every included and excluded line is auditable.

## Not yet run

The blinded LLM-judge rubric (fixed rubric, redacted paths, numbered lines for
evidence citation) — designed and codex-vetted, still unrun. It is the only
instrument that can assess naming, abstraction quality and error-handling
discipline, to which every tool above is structurally blind.
