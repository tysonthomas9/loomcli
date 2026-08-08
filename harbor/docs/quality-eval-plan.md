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

### Arm B2j — a dedicated ARCHITECT agent

A fourth persistent controlled session (same proven machinery as lead/qa),
agent name `arch`, holding one vantage: the structure of the system. Per the
campaign law — minds verify what their vantage makes visible — QA's vantage
is the product, the lead's is the plan; nobody currently looks at the code
AS A CODEBASE. The architect does, generically:

- Receives the spec verbatim (READY protocol, like QA), then on each pass
  whose message lists integrations: reads the integrated head in its own
  read-only checkout and assesses structure — module boundaries and
  responsibilities, dependency direction and cycles, duplication, size and
  complexity of the pieces, whether tests accompany behavior.
- Acts through the existing task machinery ONLY:
  - files refactor/structure corrective tasks (`--source-repo app`, parent
    epic), each quoting the concrete structural problem observed;
  - may comment `ARCH: ...` advisory feedback on design tasks sitting in
    review (inert to the lead's review-routing protocol, which keys on the
    exact IMPL-DONE marker form and the needs-revision label).
- Never blocks anything, never edits code, never runs the app (structure
  review is reading — no port conflicts, so no alternation machinery
  needed), never changes any task status. Cost estimate +$20–40/run.

Product framing: this prototypes an `architect` role for loom's stock role
library (plan / task / review / architect), the same way the qa arms
prototyped the verify role.

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

## Ladder

1. Free: stub assertions for B2j wiring (arch session bring-up, delivery,
   no status changes possible), prompt purity re-read, `bash -n`.
2. Codex vet of both arm diffs (standing discipline — five for five on
   catching real bugs).
3. Two paid runs, sequential: `loom-generic-tasks-arch-1`, then
   `loom-generic-tasks-leadm-1` (~$180–200 each), replica judge, full audit
   toolchain post-run, then the fork verdict.
