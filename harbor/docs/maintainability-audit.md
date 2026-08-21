# Maintainability audit: four same-spec artifacts, four AI processes

2026-08-06/07. Two-stage instrument set, each stage codex-vetted twice (design
vet + numbers vet); every headline number independently re-derived by codex
via AST/tokenize recomputation before being reported.

## Subjects

| id | process | lang | size | benchmark result |
|---|---|---|---|---|
| baseline | single-session codex, 45 min, $3 | Node/JS | 3 files / 1236 SLOC | 2/5 gates, ux 0.9375 |
| run19 | loom ensemble (lead + workers + product QA), 3h20m, $178 | Node/JS | 49 / 5775 | 3/5 gates, ux 0.9375 |
| run20 | same ensemble config, second draw | Python | 19 / 5452 | 0/5 gates, ux 0.9375 |
| run21 | ensemble + second (backend) QA | Python | 35 / 4514 | 0/5 gates, ux 0.9375 |

All four built to the identical slack-clone spec (3-node clustered HTTP,
WebSocket, IRC gateway, SPA). Artifacts carry full multi-agent git history.

## Instruments

- **Panel** (`harbor/scripts/maint-panel.sh`): identical-machinery metrics —
  lizard 1.23.0 (CCN/function length, one parser for JS+Python), jscpd 5.0.14
  (token duplication, prod-only mirror), AST/tokenize SLOC+comments, file-size
  distributions, git process signals. Density-normalized; canonical prod/test
  partition shared by every tool; per-file scope manifest emitted.
- **SonarQube CE 26.8** (`maint-sonar.sh`, podman): SQALE debt ratio, smells,
  cognitive complexity. Whole-tree scope, labeled as such; A–E rating
  saturates (all four rate A) so densities are the discriminators.
- **Coupling** (`maint-coupling.sh`): AST-resolved import graph (python `ast`;
  JS via pinned madge 8.0.0), Tarjan SCCs for cycles, depth on the condensed
  DAG, fan-in/out, god modules.
- **Semgrep 1.171.0** (`maint-semgrep.sh`, pinned container): p/default +
  p/security-audit over the prod-only mirror.
- **Sampled fault injection** (`maint-mutation.sh`): 8 deterministic
  single-point mutants per artifact into prod code, artifact's OWN suite
  re-run inside the benchmark's task image (`--network none`, throwaway
  container per mutant, bounded by timeout; hang = killed per convention).
  Chosen over full Stryker/mutmut per codex stage-2 vet.
- **Independent conformance** (already existed): the benchmark's own
  129-test API suite + IRC/crash/chaos/frontend gates — the shared black-box
  suite codex named as the highest-value missing measurement.

## Results

### Identical-machinery panel (prod scope)

| metric | baseline | run19 | run20 | run21 |
|---|---|---|---|---|
| prod files / SLOC | 3 / 1236 | 49 / 5775 | 19 / 5452 | 35 / 4514 |
| median file SLOC | 286 | **83** | 206 | **103** |
| p90 / max file SLOC | 796 / 924 | **245 / 481** | 634 / 1269 | **264 / 463** |
| CCN>10 functions % | **1.9** | 2.5 | 5.0 | **4.1** |
| duplication % (jscpd) | **0.0** | 1.1 | 1.11 | **0.65** |
| comment density % | 0.0 | 0.0 | 0.5 | 0.0 |
| test files / SLOC | **0 / 0** | 14 / 2591 | 11 / 2687 | 19 / 2009 |

### SonarQube (whole staged tree — Sonar-vs-Sonar comparisons only)

| | baseline | run19 | run20 | run21 |
|---|---|---|---|---|
| debt ratio % | 1.6 | **0.5** | 0.6 | **0.2** |
| smells / KLOC | 28.1 | **10.8** | 49.6 | **6.2** |
| cognitive / KLOC | 515.8 | **149.8** | 176.1 | **154.1** |
| bugs | 15 | 23 | **1** | 12 |

### Coupling (within-language only)

| | baseline | run19 | run20 | run21 |
|---|---|---|---|---|
| modules / edges | 3 / 0 | 49 / 166 | 18 / 62 | 28 / 113 |
| circular deps | 0 (trivial) | **0** | 1 (2 mods, 11%) | 1 (**7 mods, 25%**) |
| max fan-in / DAG depth | 0 / 0 | 31 / 7 | 14 / 6 | 19 / 6 |

### Semgrep (prod scope, findings per KLOC)

baseline **4.0** · run19 **0.3** · run20 0.9 · run21 1.1.
Both Python artifacts produced the *identical* finding set (3× raw SQL
execution, formatted SQL, SHA-1) — a process fingerprint.

### Test effectiveness

| | own-suite baseline | mutation score (8 mutants) |
|---|---|---|
| baseline | no tests exist | **undefined** |
| run19 | green | **62%** (4 by failure, 1 by hang) |
| run20 | green | **75%** |
| run21 | **red — fails 3/74 of its own tests** | **62%** |

### Self-authored tests vs independent conformance

| | own tests | independent API suite |
|---|---|---|
| baseline | none | **109/129** (highest) |
| run19 | 14 files | 103/129 |
| run20 | 11 files | 107/129 |
| run21 | **19 files** (most) | **93/129** (lowest) |

## Findings

**F1 — On the only like-for-like process comparison (JS pair), the ensemble
wins internal quality decisively — and the claim is safe.** run19 beats the
single-session baseline on every instrument: 13× lower Semgrep density, 2.6×
lower smell density, 3.4× lower cognitive complexity per KLOC, files a third
the median size, a fully acyclic 49-module dependency graph (madge-confirmed),
and a real test suite (mutation 62%) where the baseline has none. It is immune
to the "it just did less" objection because run19 also scored *higher* on the
independent gates (3/5 vs 2/5). Baseline's only wins: duplication (0.0% vs
1.1%) and slightly fewer high-CCN functions.

**F2 — Test volume and externally-verified correctness are inverted across
this set.** The artifact with zero tests scores highest on the independent API
suite; the artifact with the most test files scores lowest. Self-authored
tests measure agreement between an agent's tests and the same agent's
implementation — not correctness against the spec.

**F3 — The ensembles' suites are real but porous.** Mutation scores 62–75%:
the suites catch most injected faults (including via a hang — see F7) but
individual modules are dark: both mutants in run19's `store.js` survived, and
2 of 3 mutants in run21's IRC gateway survived.

**F4 — Adding the second QA agent did not buy quality.** run21 vs run20
(functionality matched at 0/5 gates): cleaner code shape (8× lower smell
density, half the max file size, less duplication) but worse engineering
discipline — a 7-module import cycle entangling 25% of the codebase, a lower
mutation score, a suite that fails 3 of its own 74 tests at delivery, and the
lowest independent API score of all four artifacts. "Cleaner structure, more
flagged defects" is the honest summary.

**F5 — Nobody comments anything.** All four artifacts: ≤0.5% comment density.
(An earlier 7.6%/12.8% Python reading was a measurement bug — triple-quoted
SQL literals counted as docstrings — caught by the codex numbers vet.)

**F6 — A mutant can hang a suite instead of failing it.** A single `>`→`<=`
in run19's `realtime-hub.js` made its WebSocket smoke test wait forever for
an event that never arrives. In CI that is a stuck pipeline, not a red build.
Tests that await asynchronous events need deadlines.

**F7 — The benchmark is blind to all of this.** run20 and run21 have
*identical* benchmark scores and an 8× difference in smell density; the
baseline and run19 differ by 13× on security-pattern density inside a 0.1
site-partial gap. None of these instruments' signals appear in the score.

## Measurement integrity

Every stage was codex-vetted before its numbers were reported; the vets found
and forced fixes for real bugs at each stage: `.mjs` files silently out of
scope; Python SQL-literals-as-comments (HIGH — flipped a conclusion); a JS
mutation classifier that greppped for unittest patterns Node never prints
(flipped run19's mutation score from 0% to 62%); an upper-median; jscpd
failures maskable as 0.0%; and one contaminated sweep caused by editing a bash
script while a prior process was still executing it. Binding interpretation
rules: advantage claims within same-language pairs only; densities not
absolutes; n=1 per configuration — artifact-level contrasts, not causal
process effects.

## Data

All results JSON/CSV, both codex vet reports, and the stage-2 plan vet are in
the trials evidence repo under `docs/` (maintainability-*.json,
codex-maintainability-numbers-vet*.md, codex-stage2-plan-vet.md). Scripts:
`harbor/scripts/maint-{panel,sonar,coupling,semgrep,mutation}.sh`.
