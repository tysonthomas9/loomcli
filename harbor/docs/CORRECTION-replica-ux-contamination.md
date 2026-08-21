# CORRECTION: replica-UX contamination (2026-08-08)

**Severity: HIGH. Retracts a headline claim. Found by a codex second-opinion
audit of the "constant 0.9375" the user flagged as suspicious.**

## The defect
A leaked host process — `http_node.py` from the **b2d artifact**
(`loom-generic-qa-1/slack-clone__CAubCQL`, "Huddle") — was left listening on
`127.0.0.1:18000-18002` from 2026-08-04. The replica judge
(`harbor/scripts/claude-judge/claude_judge.py`) drives agent-browser at
`http://127.0.0.1:18000/` and gated boot only on `/api/health`, which the
squatter answered. So podman's fresh per-run container never owned the port,
and **every judge run from b2d onward browsed b2d's app, not the artifact
under test.** Three driver reports (b2e, leadm1, tasks2retest) explicitly
flagged the source/live mismatch; it was archived but not read until the
audit.

## Corrected replica-UX (clean re-judge, app identity verified post-fix)

| run | contaminated ux | **clean ux** | gates | contaminated site | **REAL site** |
|---|---|---|---|---|---|
| b2c (leadverify-1) | 0.3125 | 0.3125 | 1/5 | 0.256 | 0.256 | valid, pre-leak |
| b2d (qa-1) | 0.9375 | 0.9375 | 0/5 | 0.469 | 0.469 | valid, own app == squatter |
| b2e (leadverify-ui-1) | 0.9375 | **0.828** | 0/5 | 0.469 | **0.414** |
| **run19 (tasks1)** | 0.9375 | **0.219** | 3/5 | ~~0.769~~ | **0.409** |
| run20 (tasks2) | 0.9375 | 0.9375 | 0/5 | 0.469 | 0.469 | clean == contaminated by coincidence |
| run21 (dual1) | 0.9375 | **0.5625** | 0/5 | 0.469 | **0.281** |
| B2k (leadm1) | 0.9375 | **0.8125** | 0/5 | 0.469 | **0.406** |

## What is RETRACTED
1. **run19 does NOT beat the public board.** Real site partial **0.409**, not
   0.769. Its SPA is broken past auth (no default channel, composer disabled,
   no messages render) — independently corroborated by the benchmark's OWN
   `frontend_gate=False` + `journey 0/1`, which contradicted the contaminated
   UX all along. No loom tasks-family run beats the board's 0.60.
2. **The "UX invariant / spec-conformance ceiling at 0.9375" law is VOID.**
   Clean scores span 0.219 → 0.5625 → 0.8125 → 0.828 → 0.9375: the judge
   discriminates fine. The "six identical" constant was the leaked b2d app
   scored six times. My earlier "ceiling not bug" verdict was WRONG — reached
   through contaminated evidence, overstating the independence of reports that
   were actually all about b2d's app.

## What STANDS (separate instruments, uncontaminated)
- All CORRECTNESS numbers (gates, API suite): official Harbor verifier, its
  own container on ports 8000-8002. Unaffected.
- B2j design-gate starvation (integrated=0): no app built, no judge ran.
- B2k maintainability-instrument inertness: structure/coupling/mutation
  metrics, not UX. Its UX correction (0.9375→0.8125) does not change the
  "exhortation inert" finding.
- The maintainability audit (F1-F7): panel/Sonar/coupling/Semgrep/mutation —
  no judge involved.

## The fix (committed)
`claude_judge.py` now fails CLOSED on this class:
1. `_assert_host_ports_free()` before every boot — raises if 18000-18002 are
   already bound (this alone would have prevented the bug).
2. Post-boot `podman port` identity assert — confirms host 18000 maps to THIS
   container (SPA-safe; replaced a served-canary that false-failed on
   catch-all routing).

## Process failures that let this run for four days / six runs
- Health-only boot gate (any slack-clone answers /api/health) — now port+identity.
- Archived driver reports flagged the mismatch; not read. Evidence dumps must
  be spot-read, not just stored.
- The contaminated UX contradicted the official frontend_gate for run19; the
  two signals were never cross-checked. Cross-instrument consistency is now a
  required check before any UX-derived claim.
- Credit: the user's "this looks like a bug" + codex's independent audit
  (docs/codex-judge-contamination-audit.md) surfaced it.
