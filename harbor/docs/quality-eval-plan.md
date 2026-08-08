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

## Leakage and validity (the objection that reshaped this design)

Feeding quality metrics to the agents and then scoring on those same metrics
would make the result circular: it would measure compliance with a shown
rubric, not maintainability, and it would invite Goodharting (file-splitting
to duck size metrics, restructuring duplication below jscpd's token window,
CCN-shaving that moves complexity into the call graph). Two rules fix this:

1. **Instruments are split into FED-BACK and HELD-OUT sets, fixed before the
   run.** Fed-back (visible to agents via the QUALITY line and digest — these
   become *compliance* data and can never support the headline claim): CCN,
   duplication %, file size, import cycles. Held-out (no prompt, message, or
   in-container artifact references them in any form — the *evidence* set):
   Sonar smell density and cognitive/KLOC (different rule engine from
   anything fed back), Semgrep density, the blinded LLM judge (naming,
   abstraction, error handling — orthogonal to every fed-back number; the
   strongest held-out instrument), own-suite-green, and the benchmark
   gates/ux as non-regression guards. Semi-held-out, flagged: the mutation
   score — the coder duty "a test that fails without the change" is generic
   TDD practice but is also exactly what kills mutants, so that instrument is
   partially coupled to the prompt and is reported with that caveat.
2. **Inference logic pre-registered:** fed-back improves AND held-out
   improves → the quality pressure generalized (the claim stands). Fed-back
   improves but held-out does not → gaming detected (that is the finding;
   no quality claim). Neither improves → the loop is inert.

Framing consequence: B2i is NOT a measurement of whether the loom process
naturally writes maintainable code — the audit already answered that,
unprompted, and B2i's agents are no longer blind. B2i is a treated-vs-
untreated TOOLING experiment: runs 19–21 are the untreated baseline and the
claim under test is "loom + a quality loop delivers maintainable code without
taxing the score" — the product question, and the same standard as real CI
quality gates, which developers are shown by design. The benchmark eval
itself (gates/ux/verifier) remains never-referenced, as in every prior arm.

## Pre-registered outcomes (vs runs 19–21 as untreated baseline)

Compliance targets (fed-back set — reported, never evidence):
- median prod file ≤120 SLOC; max ≤500; 0 import cycles; duplication ≤1.0%

Evidence targets (held-out set — the claim rests here):
- Sonar smells/KLOC ≤10 and cognitive/KLOC ≤160 (bests so far: 6.2 / 149.8)
- Semgrep ≤0.5 findings/KLOC
- blinded LLM-judge maintainability ≥ untreated artifacts (protocol from the
  hybrid design: fixed anchored rubric, redacted paths, numbered lines)
- own suite green at finalize (run21 failed this)
- mutation ≥75% on the 8-mutant sample (semi-held-out; reported with caveat)
- comment density ≥2% would be a bonus; not a target (nobody hit 0.5%)

Non-regression guards (the other half of the experimental question — does
quality pressure tax the score?):
- gates + replica-ux within the tasks-family band (site partial ≥0.469;
  integrations ≥19; drained or near-drained)
- spend ≤ $200 cap; pass cadence unchanged
Goodhart tripwires beyond the split: rising edge count/new cycles alongside
falling file sizes; duplication migrating just under the 50-token jscpd
window (checked post-run at min-tokens 30 as a sensitivity pass).

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
