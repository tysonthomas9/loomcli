# Plan: maintainability as a first-class outcome of the eval (arm B2i-quality)

Goal, in the user's words: rerun the experiment so that the tools from the
maintainability audit are part of the eval, and see how maintainable the code
we get becomes. Two integration surfaces, deliberately separated:

- **Stage A — measure every run** (no behavior change): the audit toolchain
  runs automatically per trial, producing a quality scorecard next to
  gates+ux. This makes maintainability a tracked outcome forever.
- **Stage B — feed the loop** (the arm under test): bounded quality signals
  are injected into the ensemble's existing feedback machinery so the agents
  *act* on quality during the run. B2i = the proven `verify_role=tasks`
  config (run 19's, the best overall performer) + Stage B instrumentation.

The audit's F4 finding argues against building this on the dual-QA arm: the
second verification mind bought no quality. Quality pressure here goes through
the harness (deterministic) and the lead (direction), per the campaign's
division-of-labor law — not through another agent.

## Stage A — per-trial quality scorecard (always on, all future arms)

1. **In-container fast panel at finalize**: a `quality-report` step in
   orchestrate's finalize runs lizard (pip-pinned at bootstrap) + jscpd (npx
   pinned; node already present) + the coupling script over `/app` prod scope
   and writes `/logs/agent/quality.json`. Runtime budget ≤60s (measured:
   lizard 0.2s, jscpd ~3s, coupling ~1s). Failure of the quality step must
   never fail the trial (advisory instrumentation).
2. **Own-suite verdict at finalize**: run the artifact's own test suite once
   in-container, bounded (420s), record `own_suite: {rc, failures}` in
   quality.json. Objective, cheap, and directly addresses audit F4 (run21
   shipped red).
3. **Host-side deep kit post-run** (unchanged, on demand): SonarQube,
   Semgrep, sampled fault injection via the existing `maint-*.sh` scripts —
   too heavy for in-container, run against the artifact afterward like the
   replica judge.
4. **Scorecard row** added to the campaign matrix per run: median file SLOC,
   CCN>10 %, dup %, cycles, smells/KLOC (post-run Sonar), mutation score,
   own-suite green/red.

## Stage B — in-loop quality signals (the B2i arm)

**B1. Integration-gate quality check (harness, deterministic, advisory v1).**
At each impl-review, alongside the existing integration check in the
disposable worktree, compute on the candidate: functions with CCN>10 (delta
vs current /app), files >300 SLOC (delta), jscpd duplication % (delta), new
import cycles (coupling script delta). Append one `QUALITY` line to the
integration record. Advisory in v1 — it never blocks a fast-forward. (A
blocking v2 with thresholds only if v1 shows agents ignore the signal;
blocking risks stalling integration throughput, which runs 19–21 show is the
score's engine.)

**B2. Lead quality digest (one line per pass — zero-sum attention law).**
The pass message gains at most one sentence when integrations occurred, e.g.:
`Quality since last pass: +2 functions over complexity 10, largest file now
612 lines, duplication 1.8% (+0.4), 1 new import cycle.` The lead's existing
duty ("file tasks for what you judge most needs checking / fixing") lets it
convert this into ordinary corrective tasks through the existing app lane —
no new lanes, no new agents, no prompt-purity risk (generic engineering
norms, zero benchmark references).

**B3. Coder template duty (modest).** The `fleet_task.md` override gains a
short maintainability clause: keep functions small and single-purpose; do not
copy code between modules — extract and import instead; every behavior change
lands with a test that fails without it; asynchronous waits in tests must
carry deadlines (audit F6). Prompt exhortations are historically weak alone;
this rides behind the structural signals of B1/B2.

**B4. Lead seed guidance (one sentence).** The seed duty already asks for a
decomposition; add: prefer small, single-responsibility modules with acyclic
dependencies. (Cheap; the JS/Python decomposition split in runs 19/20 suggests
seed-time structure is where module shape is decided.)

Explicit non-changes: QA prompts byte-stable (their halves are proven; audit
F4 says more verification attention ≠ quality); verifier untouched; no
quality metric ever blocks integration in v1.

## Pre-registered outcomes (vs runs 19–21 as baseline)

Quality targets for the B2i artifact:
- median prod file ≤120 SLOC; max ≤500 (runs 19/21 hit this; 20 didn't)
- 0 import cycles (only run 19 achieved this)
- duplication ≤1.0%
- Sonar smells/KLOC ≤10 (best so far 6.2)
- own suite green at finalize (run21 failed this)
- mutation score ≥75% on the 8-mutant sample (best so far 75%)
- comment density ≥2% would be a bonus; not a target (nobody hit 0.5%)

Non-regression guards (the real experimental question — does quality pressure
tax the score?):
- gates + replica-ux within the observed band of the tasks family
  (site partial ≥0.469; integrations ≥19; drained or near-drained)
- spend ≤ $200 cap; pass cadence unchanged
Watch for Goodharting: agents splitting files to game size metrics shows up as
rising edge counts/cycles — the coupling delta in B1 is the tripwire.

## Validation ladder (before any spend)

1. `bash -n` + stub trial: quality-report step runs in-container on the stub
   app, quality.json well-formed, QUALITY lines appear in integration records,
   advisory failure injected (broken jscpd) does not fail the trial.
2. Codex vet of the wiring diff + prompts (standing discipline; the last
   three vets each caught a real bug).
3. One paid run: `loom-generic-tasks-quality-1`, base kwargs of run 19 +
   `quality_loop=1`, $200 cap, replica judge, full audit toolchain post-run.

## Open questions (flagged, not blocking)

- Whether B2h (deterministic continuity fault-gate, the gates-variance fix)
  should stack into the same run or stay a separate arm. Default: separate —
  one variable per trial, per campaign protocol.
- Semgrep in-container at finalize: feasible (pip install at bootstrap,
  pinned) but adds a network dependency at install time; deferred to host-side
  post-run for v1.
