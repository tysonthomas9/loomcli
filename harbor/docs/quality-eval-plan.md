# Plan rev 3: who should own maintainability? (arms B2j-architect / B2k-lead)

Reframed per user direction: move the needle with GENERIC updates to the loom
system that would apply to any problem — no task-specific tooling, no changes
to how the spec is delivered (the seed message stays the verbatim
instruction, as in every arm). The intervention is loom-role-level: give
somebody the standing responsibility for maintainable structure, and fork on
WHO.

This supersedes rev 2's metric-feedback loop (QUALITY lines / lead digests).
That mechanism is parked as a possible third arm ("CI-style feedback") to run
only if responsibility alone proves inert — because dropping it buys
something valuable: **no quality metric of any kind is shown to any agent, so
the ENTIRE instrument set is held-out** and the leakage problem from rev 2
vanishes structurally rather than by instrument-splitting.

## The fork (campaign-style, one variable: ownership)

Base for both arms: `verify_role=tasks`, the proven run-19 configuration,
byte-stable except for the single change per arm.

### Arm B2j — a dedicated ARCHITECT agent WITH AUTHORITY (rev 4)

Rev-4 change (user decision + research): the only matched-baseline study
found prompt exhortation had no effect on architectural smells (p>0.8),
while our own campaign shows agents reliably respond to the task machinery
(reopens get reworked, feedback gets addressed). So the architect gets
AUTHORITY at two checkpoints, exercised entirely through the existing
board machinery — labels, comments, statuses. No tool output is ever piped
to any agent; the architect reads code and diffs and forms its own
judgment, so the held-out instruments stay uncontaminated.

A fourth persistent controlled session (same proven runtime as lead/qa),
agent name `arch`, vantage: the codebase as a structure. Receives the spec
verbatim (READY protocol). Never edits code, never runs the app (reading
only — no port conflicts, no alternation), never touches tasks outside the
protocol below.

**Checkpoint 1 — design stage (change is cheapest here).** Each pass
message lists designs in review. Arch marks each `--add-label arch-ok`, or
rejects: `ARCH-FEEDBACK:` comment + `needs-revision` + status open (the
standard plan-rework route; planner revises). Lead prompt (B2j variant):
approve only designs carrying `arch-ok`; fail-open — a design in review
across two of the lead's passes with no arch ruling is the lead's to
decide (a silent architect never blocks planning).

**Checkpoint 2 — integration stage.** The harness gate is unchanged
(deterministic check in the disposable worktree, FF-only, atomic) but gains
a required approval BEFORE the fast-forward:
- Sweep finds a valid candidate (IMPL-DONE marker, check passed) → labels
  it `arch-gate` instead of integrating.
- Arch's next pass message lists all arch-gate candidates; its checkout
  shares /app's object store, so `git diff <app-head>..<candidate>` is
  free. Batch ruling per pass → typical added latency ≤1 pass.
- APPROVE: `--add-label arch-ok` → next sweep fast-forwards and closes
  exactly as today (labels cleaned at close).
- REJECT: `ARCH-FEEDBACK:` comment + `--add-label arch-rework` + status
  open. Deliberately NOT `needs-revision` (that means plan-stage rework
  and would misroute to the planner); open + has_design → the coder
  reclaims, reads feedback, revises, IMPL-DONE attempt n+1, same gate.
  Coder template gains one line: address ARCH-FEEDBACK comments on
  reclaimed tasks.
- FAIL-OPEN DEADLINE: a candidate un-ruled for 2 passes integrates anyway
  with an `ARCH-TIMEOUT` record. The architect can slow a merge, never
  stall the pipeline; endgame-burst candidates timeout-integrate rather
  than die at the deadline (disclosed, measured).

Plus refactor corrective tasks against the integrated head — but BOUNDED
(rev 4.1, from the QA-feedback analysis): at most two per pass, filed at
lower priority than behavioral correctives (generic rule: structure
cleanup yields to user-facing defects under deadline). Three filing minds
already lost the drain race in run 21 (12/23 correctives dead at
deadline); the architect's primary authority is the GATE — prevention
over cure.

**Budget fairness:** arch spend (~$40–70 est.) comes out of the SAME $200
cap — no cap raise; if an architecture mind is worth having it must pay
for itself. Displaced spend is part of the measured trade.

**New risks, pre-registered as observables:** rejection-spiral (rework
convergence rate per rejected candidate; fail-open caps damage), added
integration latency (passes from IMPL-DONE to FF vs runs 19–21),
ARCH-TIMEOUT count (how often authority was actually exercised vs
bypassed), arch ruling counts and approve:reject ratio.

**Rubber-stamp detection (rev 4.1 — the QA "PASS is weak evidence"
lesson transferred):** the approve path carries little information by
default, so scrutiny is analytical, post-run: (a) re-examine APPROVED
diffs with the held-out instruments — did waved-through changes introduce
cycles/oversized files anyway?; (b) compare rejected-then-reworked
resubmissions against their first attempts — did rejections measurably
improve structure?; (c) approve-rate ≈100% with unchanged held-out
metrics is verdict-classified as AUTHORITY UNEXERCISED, distinct from
authority-ineffective. ARCH-FEEDBACK stays mandatory on every rejection
(anchored to the concrete structural problem) so (b) is computable;
approvals stay cheap labels.

Product framing: prototypes an `architect` role with gate authority for
loom's stock role library, the way the qa arms prototyped the verify role.

### Arm B2k — the lead owns maintainability

No new agent. The lead's standing prompt (loom-generic, applies to any
problem) gains one short section: maintainability is part of the job —
prefer decompositions of small single-responsibility tasks whose modules
have acyclic dependencies; when reviewing designs, treat structural problems
(tangled dependencies, oversized modules, copy-paste plans, missing tests)
as grounds for revision; when the pass message lists integrations, weigh
whether the integrated structure needs corrective refactor tasks alongside
functional ones.

This tests the cheap hypothesis: ownership may not need a mind, just a
sentence in the right head. B2e warns the other way — one mind's attention
is zero-sum, and the lead already directs verification; the fork measures
exactly this trade.

### Shared, both arms (measurement only — invisible to agents)

- Stage A scorecard stands: fast panel + own-suite verdict run at FINALIZE
  (after the last agent pass; nothing is shown to any agent), quality.json
  in the evidence; Sonar/Semgrep/mutation/LLM-judge run host-side post-run.
- Coder templates, QA prompts, seed delivery: byte-stable. The verifier and
  benchmark criteria: never referenced, as always.

## Leakage posture (simpler than rev 2)

No agent sees any metric, threshold, tool name, or measurement artifact.
The prompts speak only in generic engineering language (the same register as
"write tests", which existing prompts already use). Therefore every
instrument — panel, Sonar, Semgrep, coupling, mutation, blinded LLM judge,
own-suite-green, benchmark gates/ux — is held-out evidence. Residual
coupling, disclosed: prompts naming "acyclic dependencies" and "small
single-responsibility modules" overlap conceptually with the coupling and
file-size instruments; that is the nature of asking for maintainability at
all. The blinded LLM judge and Sonar/Semgrep rule engines remain fully
orthogonal checks, and the Goodhart tripwires from rev 2 (edge/cycle growth
alongside shrinking files; duplication just under the jscpd token window)
stay in the analysis.

## Pre-registered outcomes

Untreated baseline: runs 19–21 (+ single-session baseline for context).
Evidence targets per arm (all held-out): 0 import cycles; median prod file
≤120 SLOC; duplication ≤1.0%; Sonar smells/KLOC ≤10 and cognitive/KLOC
≤160; Semgrep ≤0.5/KLOC; own suite GREEN at finalize; mutation ≥75%;
blinded-judge maintainability ≥ untreated artifacts.
Non-regression guards: site partial within the tasks-family band (≥0.469),
integrations ≥19, spend ≤$200, cadence unchanged.
Fork verdicts: ARCH ≥ targets and LEAD misses → structure needs its own
mind. LEAD ≈ ARCH → ownership is a sentence, not an agent (ship the cheap
one). Both miss → responsibility without feedback is inert → the parked
CI-style feedback arm becomes the next experiment. Watch B2e's shadow in
B2k: if lead verify-task filing drops vs runs 19/20, attention was cannibal-
ized — report it.

## Observables

Architect: tasks filed (count/type/aging, drained?), ARCH comments and
whether commented designs were revised, zero status-changes (protocol
compliance). Lead (B2k): decomposition shape vs runs 19/20 (task count,
module plan), refactor-vs-functional corrective mix, verify-task filing rate
(zero-sum watch). Both: everything in the audit instrument set.

## Variant POC (2026-08-07, ~$1-2 — RUN, results in trials repo docs/archpoc/)

Four judgment-style variants of the architect prompt, each a REAL codex
session judging five PLANTED stimuli (good design, copy-paste design,
clean diff, cycle diff, god-file diff) through the exact rev-4.1 label
protocol on a toy repo, identical stimuli across variants:

| variant | style | verdicts | notes |
|---|---|---|---|
| v1 | generic engineering judgment | 5/5 | clean protocol, ~4.7 min |
| v2 | research-informed criteria | 5/5 | principled vocabulary in feedback |
| v3 | strict 8-item checklist, cite-per-rejection | 4/5 by key — but the "miss" was a TRUE finding | rejected the "clean" diff because its appended test classes sit after unittest.main() and never execute on direct run — a real latent defect the fixture author and v1/v2/v4 all missed (verified empirically) |
| v4 | justify every ruling incl. approvals | 5/5 | ARCH-NOTEs present; ~6 min |

All four: correct label protocol (arch-ok / needs-revision / arch-rework
with ARCH-FEEDBACK on every rejection), zero forbidden status changes.
POC mechanics lessons folded into the runner: worktree prune before
branch reuse; checkout dir named after the repo; --source-repo values
must be registered repos (the qa-verify lesson, again).

DECISION: **v2 (research-informed) for the full B2j run** — verdict-tied
with v1 on these stimuli, but its criteria vocabulary gives coders
actionable feedback grounded in the evidence-ranked list. v3 held as the
escalation option: its demonstrated strictness catches real subtle
defects AND is exactly the rejection-spiral risk (it would have spent a
rework loop on a defect invisible to the standard runner). v4's mandatory
approval notes not adopted — the post-run rubber-stamp audit covers that
need without in-run token cost.

## Ladder

1. ~~Variant POC~~ DONE (above).
2. Free: stub assertions for B2j wiring (arch session bring-up, delivery,
   arch-gate/arch-ok/arch-rework flow, fail-open timeout), prompt purity
   re-read, `bash -n`.
3. Codex vet of both arm diffs (standing discipline — five for five on
   catching real bugs).
4. Two paid runs, sequential: `loom-generic-tasks-arch-1` (v2 prompt),
   then `loom-generic-tasks-leadm-1` (~$180–200 each), replica judge,
   full audit toolchain post-run, then the fork verdict.
