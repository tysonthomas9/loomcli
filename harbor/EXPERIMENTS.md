# Orchestration experiments on SWE-Marathon slack-clone: as-run log + generic rerun protocol

**Part A** records what was actually run, every experimenter intervention
disclosed and classified. **Part B** defines the GENERIC protocol and the
per-arm rerun configuration — now backed by committed, executable
infrastructure, not prose. Revision 2: folds three codex vets (methodology
vet-A: 12 findings; feasibility strip-vet: 4 blockers; agentflow source
verification).

Evidence: `github.com/tysonthomas9/loom-marathon-trials` (private).

---

## The experiment

**Question:** does orchestration structure improve long-horizon code
generation under a fixed budget — and which kind?

**Arms** (the structure axis — B4a/B4b are pre-registered as SEPARATE arms
per vet-A #10):

| Arm | Structure source |
|---|---|
| baseline | none (one session) |
| baseline+persist | none + mechanical relaunch (controls for the persistence variable, vet-A #5) |
| loom | role ensemble: LLM decomposes once, fixed roles execute |
| fractal | emergent tree: LLM decomposes recursively at runtime |
| agentflow-lead (B4a) | plan-and-replan: LLM authors+revises the DAG, engine executes |
| agentflow-fixed (B4b) | fixed DAG: mechanically derived from the spec, engine executes |

**Constants:** task dataset v1.1 (`abundant-ai/swe-marathon` — pin the REPO
COMMIT in the manifest; `task.toml` schema version is 1.0), model `gpt-5.5`,
codex CLI `0.142.5`, verifier + CUA as shipped, **$200/trial generation cap**
(raised 2026-08-02; A1–A3 ran at $90 — tiers are reported separately and
never mixed in comparisons, vet-A #3), 4h wall clock − 40min reserve.

**Intervention classes:** **[O]** operational necessity (strategy-free);
**[S]** structural/strategic bias (FORBIDDEN in generic runs); **[H]** harness
scaffolding (equal for all arms, environment-directed). Corrections from
vet-A #6/#7/#10: the loom per-review critic + retry policy are **[S]** (only
the deterministic merge-safety gate is [H]); the port sweep is [H] **only if
every arm gets the identical pre-verifier sanitation** (as-run it existed for
loom/fractal only — disclosed asymmetry); relaunch-via-resume is [H] **only
under the uniform lifecycle policy below**, else [S].

---

## Part A — as-run log (bias ledgers)

### A1. Baseline `codex-baseline-1` — $90 tier, n=1
45 min self-terminated · $2.68 · **2/5 gates** (IRC 11/11, chaos 3/3, crash
2/3) · API 109/129 · ux 0.9375 · **partial 0.46875**. Ledger: ~empty; NO port
sweep (asymmetry, disclosed); n=1 — insufficient for claims (vet-A #3).

### A2. loom `loom-trial-1` (unscored), `loom-trial-4` — $90 tier
Trial-1: 9/9 drained, 2h54m, $61.07, environment lost pre-verification.
Trial-4: 6/10 at deadline, $60.59, 0/5 gates, API 105/129, ux 0.125,
**partial 0.0625**. Ledger: [O] minimal template-ending fork, `--source-repo`
G12 workaround; **[S] role prompts (task counts, skeleton-first, priorities),
critic-on-every-review + retry policy, serialized coder, cadence**; [H]
deterministic FF gate, mirrors, spend rail, sweep; violation: mid-run
in-container prompt patch (trials 3/4).

### A3. fractal `fractal-trial-1` — $90 tier
Tree = root + 4 children **echoing the [S] preamble's example subsystems
verbatim** (bias demonstrated). 90 steps, **$88.64 (fractal's own ledger) —
money-bound at $90 in 86 min**, then drained. 0/5 gates, IRC 9/11, API
101/129, ux 0.875, **partial 0.4375**. Ledger: [O] caps/auth/done-when; **[S]
subsystem-example preamble, task-derived caps (depth 3/children 4/desc 10),
port rule**; [H] polling, reserve escalation, merge, sweep, mirrors.

### A4. agentflow — not yet run (arms B4a/B4b pre-registered below).

---

## Part B — the GENERIC protocol (rev 2)

### Rules

1. **Prompt purity.** Verbatim `instruction.md` + at most one strategy-free
   tool-native completion sentence. Nothing else.
2. **Tool defaults; forks only for in-container impossibility**, minimal
   diff, committed + hashed in the manifest, admissible only under rule 8.
3. **Caps: parity or physics.** Money $200 and clock 4h−40min are parity.
   Concurrency caps ≤ machine ceiling **counting the root/lead session**
   (VM sustains ~8 codex sessions → children 7 / descendants 7 — these are
   CONCURRENCY caps, not totals; strip-vet #5). Counter caps set far beyond
   feasibility (iters 1000; strip-vet #6). Hidden vendor reserves pinned
   explicitly (fractal `--reserve-budget 0`; strip-vet #4). Disclose which
   cap is expected to bind (fractal: money, ~t+194min at observed burn).
4. **Uniform lifecycle policy (vet-A #5).** EVERY arm, baseline included,
   gets the identical mechanical persistence: if the arm's driver exits with
   wall clock remaining and its completion signal absent, relaunch it
   mechanically (codex: `codex exec resume --last "continue"`; fractal:
   `node start --continue`; loom: daemon supervision) up to the reserve.
   Persistence is thereby a constant, not a hidden treatment.
5. **Uniform pre-verifier sanitation (vet-A #7).** One identical finalize
   wrapper for all arms: port sweep with pre-sweep state logged; any arm that
   required repair is reported as such.
6. **No mid-run intervention.** Fixes apply to the next trial.
7. **Run manifest per trial (vet-A #4):** swe-marathon repo commit, image
   digest, agent/CLI versions (codex, plasma-fractal, agentflow, loom
   bundle SHAs), prompt-bundle file hashes, resolved model ID from session
   records, verifier file hashes, cap set, and the bias ledger.
8. **Workaround admissibility (vet-A #11):** platform failure reproduced
   pre-run, minimal patch with hash, demonstrably strategy-free, applied to
   every repeat of that arm.
9. **Variance (vet-A #9):** n ≥ 3 agent runs per arm per tier; per frozen
   `/app` artifact, k ≥ 2 verifier replays (from the archived snapshot) —
   agent variance and CUA variance reported separately; CUA infra-failures
   are reruns, not zeros.
10. **Ordering (vet-A #12):** arms interleaved in randomized blocks; fresh
    container per trial; stopping rule = the pre-declared n, not budget
    exhaustion mid-comparison. Open-internet dependency drift is an
    acknowledged, unremovable limitation of this task.

### B1. baseline / baseline+persist
Stock `-a codex -m gpt-5.5`. `baseline+persist` adds only the rule-4
relaunch wrapper. $200 tier; rule-5 sanitation wrapper applies.

### B2. loom-generic — infrastructure COMMITTED
Prompt bundle: `harbor/prompts-generic/` (minimal protocol sentences only;
coder override is stock `fleet_task.md` with only the delivery ending
swapped). Critic OFF (the [S] policy removed): valid candidates integrate
through the deterministic gate.

```sh
PYTHONPATH=loomcli/harbor harbor run -p tasks/slack-clone \
  -a loom_harbor:LoomAgent -e docker --env-file <anthropic.env> \
  --ak prompts_profile=generic --ak critic=off \
  --ak spend_cap_usd=200 --ak codex_npm_version=0.142.5 \
  --artifact /app -o trials --job-name loom-generic-N -n 1 -y
```

### B2b. loom-generic-persistent — infrastructure COMMITTED
B2 with exactly ONE changed variable: the lead is the product's own
persistent controlled runtime (`loom lead` → codex app-server thread, alive
for the whole run in tmux) instead of periodic fresh-context one-shots.
Falsifies the context-continuity hypothesis from loom-generic-1's 0.0
(spec has no living owner after the seed pass; `/api/health` lost to
paraphrase). Prompt bundle: `prompts-generic/lead-persistent.md` = the B2
lead-autonomous + lead-orchestrate texts merged verbatim (reworded only
where "act, then stop" is wrong for a live session). Pass messages are the
B2 texts verbatim; the spec arrives as a message on the same channel.
Interventions, all [O]/[H]: `leadmsg` shim (transport only — the product
has no CLI for its own cross-process delivery; calls the same
`leadcontrol.DeliverLeadMessageWithOptions` the driver/webui use), tmux
(pty for the TUI-bound runtime), boot gates (runtime-ready + ACK probe,
fail-fast before spend), finalize lead-freeze + runtime-home archive,
daemon-BEFORE-lead ordering (the daemon's long-lived process holds the one
canonical embedded fleet-db instance every CLI reuses — attempt-1 lost its
registration to a G13-family embedded.lock race between probe CLIs and the
lead's store open; $0.02, aborted by Gate A) plus the in-tree product fix
(lead session registration retries ×3 and logs failures at WARN instead of
silently running an undeliverable controlled lead).

```sh
PYTHONPATH=loomcli/harbor harbor run -p tasks/slack-clone \
  -a loom_harbor:LoomAgent -e docker --env-file <anthropic.env> \
  --ak prompts_profile=generic --ak critic=off --ak lead_mode=persistent \
  --ak spend_cap_usd=200 --ak codex_npm_version=0.142.5 \
  --artifact /app -o trials --job-name loom-generic-persistent-N -n 1 -y
```

AS-RUN (2026-08-03, loom-generic-persistent-4): **partial 0.28125 (correctness
0/5 with health TRUE, ux 0.5625)** — the persistence hypothesis confirmed on
generic-1's exact failure axis (/api/health served correctly; 7/8 tasks
integrated first-attempt; $79.89, 28 sessions, deadline finalize). API suite
77/129: contract fidelity fixed, rubric depth still behind single sessions.
The ux half is an official-pipeline verifier REPLAY on the preserved /app
artifact (fresh task container, same tests/test.sh) after the operator's
Anthropic key hit its monthly cap mid-CUA; the replay reproduced every
correctness number byte-identically, which validates the replay method.
Attempts 1–3 (~$10): #1 Gate-A abort (registration race, above); #2/#3
healthy, launchers externally reaped ~20min — run harbor detached.

### B2c/B2d. verification-ownership fork — infrastructure COMMITTED
The head-to-head the B2b result and the independent codex analysis both point
at: WHO should own epic-level verification. Both arms are B2b byte-identical
plus the SAME verification-duty text (checkout the integrated commit in a
dedicated detached worktree, run the app, exercise it against what the spec
literally states, file corrective tasks quoting the violated spec text; never
edit code). The fork's single variable is ownership:

- **B2c `verify_role=lead`** — the duty is appended to the persistent lead's
  standing prompt (`lead-persistent-verifier.md`); the lead's pass message
  carries the integrated-since-last-pass delta.
- **B2d `verify_role=qa`** — the duty lives in a second persistent controlled
  session registered as agent `qa` (same product runtime as the lead, via
  LOOM_AGENT_NAME; `qa-persistent.md`); the lead stays byte-B2b, and QA gets
  its own pass message with the same delta.

Shared enabling changes, applied to both ([O]/[H]): a detached
`verify-checkout` worktree (the verifier never runs the app in /app),
`marathon-freeports` (kills stray listeners on the app's fixed ports,
sparing harness infrastructure — the MARATHON-9 port-contamination fix), and
the mechanical integrated-delta + epic-id + checkout-path lines in pass
messages. Critic stays off; gate unchanged; workers byte-identical.

```sh
# B2c:  --ak lead_mode=persistent --ak verify_role=lead  --job-name loom-generic-leadverify-N
# B2d:  --ak lead_mode=persistent --ak verify_role=qa    --job-name loom-generic-qa-N
```

Predictions (falsifiable): both arms raise ux (someone finally looks at the
app); B2c compounds knowledge in one mind (spec + code + behavior) and should
approve better plans late-run; B2d isolates verification cleanly but pays a
second session and a lead that stays behavior-blind. If both land ~equal,
ownership doesn't matter and the value was the duty itself; if neither moves
vs B2b, deterministic gates (codex L2) — not roles — are the right lever.

### PROTOCOL AMENDMENT (2026-08-04, operator directive): judging instrument
From run 17 onward the ux half is scored by the claude-judge replica
(`scripts/claude-judge/claude_judge.py`, codex-vetted rev 2; agreement with
the official CUA within 1/16 on both calibration apps: b2c 0.3125 vs
0.28125, b2d 0.9375 vs 1.0). The official CUA runs only on explicit
operator request (e.g. for leaderboard-comparable claims). Mechanics:
harbor launches with a DUMMY-key env file (ANTHROPIC_API_KEY=replica-policy-
no-official-cua) — harbor preflights declared [verifier.env] vars and
refuses to start without the variable (omission ≠ skip; proven 2026-08-04).
The correctness stage is key-free and stays official; the CUA stage
hard-fails on the invalid key (trial records RewardFileNotFoundError;
correctness metrics.json intact; zero key spend); the replica then judges
the /app artifact. Scores computed as 0.5×correctness_binary +
0.5×replica_ux and ALWAYS labeled "replica-ux"; tables must footnote which
instrument judged each run. Runs 1–16 remain official-CUA-judged.

### B2e. lead-verifier + UI walk + drain-continue — infrastructure COMMITTED
The composition arm the fork pre-registered: B2c byte-identical except
(1) two duty sentences in `lead-persistent-verifier-ui.md` — "including
using the application through its web interface as a user would…" (B2d's
winning behavior) and "when a corrective changes an interface, verify the
user-facing behavior that depends on it still works…" (the fix for B2c's
workspace-400 cascade) — and (2) draining no longer finalizes
(`verify_role=lead-ui`): verification passes continue to the deadline
reserve (B2c threw away 43 min here). Two changes, one pre-registered
hypothesis: the ux half requires product-level verification attention.
PREDICTION: gates ≥1/5 held, API ≥100, replica-ux ≥0.8 → blended 0.5–0.6.
FALSIFIED IF: replica-ux ≤0.5 with the UI duty demonstrably exercised.
First run under the replica-ux protocol amendment.

AS-RUN (2026-08-04, loom-generic-leadverify-ui-1): **halves traded, not
stacked — blended 0.46875 (replica-ux)**. The ux leg validated exactly as
predicted: replica-ux 0.9375 (8 PASS + polish PARTIAL, identical shape to
B2d), the B2c entry-flow crater fully absent, 23/23 tasks integrated,
DRAINED-CONTINUE exercised (drained t+3h07, verified to the reserve). But
the correctness legs FALSIFIED: 0/5 gates (B2c's chaos gate lost, crash
back to 1/3), API 79/129 (vs B2c's 101) — the lead's verification budget
went to browser walks instead of the fault-injection/contract probing that
won B2c its gate. Conclusion: within ONE mind the two verification vantages
compete for the same duty cycle. $180.24, 69 sessions. Candidate next
shapes: B2f = lead-verifier + QA role simultaneously (fork union, two
minds), or B2e + a deterministic contract gate (codex L2) so the mind
spends itself on the UI while machines hold the contracts.

```sh
PYTHONPATH=loomcli/harbor harbor run -p tasks/slack-clone \
  -a loom_harbor:LoomAgent -e docker \
  --ak prompts_profile=generic --ak critic=off \
  --ak lead_mode=persistent --ak verify_role=lead-ui \
  --ak spend_cap_usd=200 --ak codex_npm_version=0.142.5 \
  --artifact /app -o trials --job-name loom-generic-leadverify-ui-N -n 1 -y
```

### B2f-revised. verification-as-tasks (lead directs, QA executes) — PLANNED
The post-B2e composition shape, POC-proven 2026-08-05 (all assertions A–F
green; `scratchpad/b2fpoc/`). Resolves B2e's zero-sum finding by splitting
verification INTENT (lead, cheap sentences) from EXECUTION (QA, expensive
walks), coordinated purely through fleet-db tasks.

Arm definition (`verify_role=tasks`), all deltas vs the B2b/B2d code paths:

- **Lead**: `lead-persistent-verifier-tasks.md` — B2c's lead but it runs
  and tests NOTHING; on every pass listing integrations it files
  verification tasks (`--source-repo qa-verify`, spec-quoting, targeted at
  the current head) BEFORE its other duties, then does normal
  review-routing. Never closes.
- **QA**: `qa-persistent-tasks.md` — B2d's persistent QA; each pass it
  (1) drains open `qa-verify` tasks oldest-first (claim via
  `--status in_progress --assignee qa`, execute, close + `QA-RESULT:`
  comment), (2) when the queue is empty, runs its standing whole-spec duty
  (independent vantage preserved), always verifying the CURRENT integrated
  head (named in the pass message). Files correctives `--source-repo app`
  (unlaned pool). Status changes permitted only on qa-verify tasks.
- **Routing**: G12 strict source_repo matching — planner/coder (`--repos
  app`) can never claim `qa-verify` tasks; POC-proven non-vacuously.
  (Product-shaped replacement: docs/design/task-lanes-routing.md.)
- **Pass messages**: both minds, every cadence tick, same mechanical info:
  integrated delta + **`Current integrated head: <sha>`** (the POC's
  multi-integration ambiguity fix) + checkout path + epic id. No
  alternating cadence needed — only QA ever boots the app.
- **Drain semantics**: `open_task_count` includes open qa-verify tasks, so
  "drained" cannot fire while verification is outstanding (desirable);
  drain-continue applies in this mode regardless (verification passes run
  to the deadline reserve).
- **Harness deltas**: orchestrate.sh gains the `tasks` value for
  VERIFY_ROLE (lead-prompt selection, QA startup with the tasks prompt,
  current-head line in VERIFY_INFO); bootstrap adds
  `hide_rate_limit_model_nudge = true` to the container codex config (the
  POC's modal-block lesson); verify-checkout + freeports unchanged
  (QA-only). Everything else byte-identical to B2b/B2d.
- **Judging**: replica-ux per the protocol amendment (dummy-key env file).
- **Cost**: ~$150–200 cap $200; ~4h.

PREDICTION (pre-registered): the lead's targeted verify-tasks direct QA
into contract/fault probes (the B2c vantage) while QA's standing duty
keeps the product walk (the B2d vantage) → gates ≥1/5 AND replica-ux ≥0.8
→ blended ≥0.55. FALSIFIED IF either half lands below its fork-proven
level — which would localize the constraint to single-executor
verification bandwidth (QA can't drain both duties at integration pace).

VET (2026-08-05, codex): EXECUTE-WITH-FIXES — all folded:
- Wiring: `tasks` threaded through prompt selection, QA bring-up, pass
  delivery (lead gets VERIFY_INFO; QA gets its pass), the current-head
  line (tasks mode only, prior arms byte-stable), drain-continue, and an
  app-source guard in the integration sweep.
- Duty cycle rebalanced against queue starvation of the ux-winning
  blanket vantage: QA verifies the current head against the WHOLE spec on
  every pass that lists new integrations, THEN drains ≤2 directed tasks;
  the lead files ≤2 verify-tasks per pass and none when the harness
  reports backlog >8 (rail records QAV-BACKLOG). [H] bounds, ledgered.
- Rails: bootstrap pre-spend probe proves qa-verify create/close in-
  container. CORRECTION (2026-08-05, launch 1): vet finding 2 was RIGHT
  and the POC-based rebuttal was wrong — fleet-db enforces referential
  integrity on source_repo (fa31508, 2026-02), and the POC only passed
  after `loom repo add qa-verify ""` registered the lane (entity in the
  POC snapshot, 06:41Z). The unregistered create died in-container at
  the probe, pre-spend, exactly as the probe was designed to catch;
  bootstrap now registers the virtual lane repo (empty remote) before
  probing, verified green in a tasks-mode stub trial (MARATHON-1).
  Codex nudge suppression in container config; prompt hashes logged at
  bootstrap (sha256sum with shasum fallback).
- Purity: prompt files carry no experiment labels/comments; the [S]-
  flagged phrases (duty ordering, target-enumeration) removed; remaining
  bounds are disclosed [H] rails.
- OBSERVABLES (disambiguate "composition worked" from "B2d again with
  stale verify-tasks"): count of lead-filed qa-verify tasks, claimed/
  closed counts and ages, QA-RESULT coverage, corrective attribution
  (blanket-found vs directed-found), all computable from final-issues +
  comments + integration.log; report them with the score.

#### AS-RUN (2026-08-05, loom-generic-tasks-1, run 19) — THE HALVES STACKED
**Correctness 3/5 gates (chaos 3/3, crash 3/3, IRC 11/11; API 103/129,
frontend journey 0/1) + replica-ux 0.9375 (8 PASS, polish PARTIAL —
B2d/B2e-identical profile) → site-formula partial 0.769 vs public board
best 0.6.** Replica-judged (standing policy; replica calibrated within
1/16 on both fork apps, ran conservative on B2d). First loom run to hold
an engineering half (3 gates — prior best 1) and the ux half (0.9375)
simultaneously; B2e's zero-sum trade broken by giving direction and
execution to different minds. $177.58 / 3h20m full budget / 24 tasks
integrated (8 seed + 16 correctives, every open task drained by
deadline), 2 gate failures both recovered on attempt 2, /app untouched
on both failures. Anti-cheat clean, health true.
- Observables: lead filed 26 verify-tasks (≤2/pass held, backlog rail
  never engaged — max open 2); QA closed 24/26 (2 open at deadline),
  file→close median 1 min / max 6 min (same-pass drain); QA-RESULT
  coverage 24/24 (~2:1 PASS:DEVIATIONS); 16 correctives filed by QA,
  16/16 integrated — titles map directly onto suite items (status_emoji,
  owner_id, /me literal, message.new, channel archive/join, SPA verifier
  hooks). Crash-gate verify-task ("supervisor restarts killed HTTP
  node") filed pass 6, gate PASSED — directed verification visibly
  bought the gate B2e's browser-walking lead missed.
- Launch friction (both pre-spend, both fixed in-tree): (1) podman
  compose banner + ANSI-glued arch string — env var restored, adapter
  strips escapes; (2) qa-verify lane rejected by fleet-db referential
  integrity — the probe caught it at $0, bootstrap now registers the
  lane repo (see CORRECTION above). Cosmetic: seed-pass count logged
  before the async lead turn finished ("1 tasks" vs real 8) — fix
  post-seed count to wait on lead idle before logging.

#### AS-RUN repeat (2026-08-06, loom-generic-tasks-2, run 20) — UX HALF
#### STABLE, CORRECTNESS HALF HIGH-VARIANCE
Identical arm (prompt hashes byte-match run 19). **0/5 gates + replica-ux
0.9375 → site partial 0.469.** API breadth IMPROVED (107/129, loom best),
mechanics improved (30 integrated incl 20 correctives, ZERO integration
failures, epic fully drained, $187.30), ux identical (8 PASS + polish
PARTIAL — 4th consecutive run of this family at 0.9375-1.0). But every
lost gate shares one signature: cross-node fan-out / dense seq / replay
(IRC 7/11 all-bridge-fails, crash 1/3, chaos 1/3) — the clustered
event-log semantics run 19 built as a dedicated seed task.
- The verification loop TARGETED it: 36 verify-tasks filed/closed (median
  1 min), five aimed exactly at cluster faults (cross-node fan-out +
  Redis outage fallback, dense seq under outage, supervisor respawn);
  QA found MORE deviations than run 19 (~53% PASS vs ~67%). Direction
  was right; a 1-2 min manual directed check cannot reproduce the
  multi-node kill/outage choreography the gate pytest orchestrates.
- n=2 verdict: mean site partial 0.619 (still > board 0.6) but gate
  variance is the binding risk — the correctness half rides on whether
  one seed task nails clustered event-log semantics. The systematic fix
  is codex-L2's deterministic evidence gate (run the fault suite, or a
  faithful miniature, inside the loop as an integration-gate check or a
  QA tool) rather than more directed manual checks. Next arm candidate.

### B2g. loom-generic-tasks-dual — dual-QA verification lanes (WIRED, unrun)
User's design (2026-08-06), directly answering the run-19/20 variance
split: the ux half is architecture-stable (persistent product QA, 4
straight runs ≥0.9375) while the gates half swings on clustered
event-log/fault semantics that a product walk cannot exercise and a 1-2
minute directed manual probe cannot reproduce. Add a SECOND persistent
verification mind holding the backend vantage.

- `verify_role=tasks-dual` (additive; `tasks` path byte-stable, proven by
  stub regression). Three persistent sessions: lead (directs), qa
  (product walk, prompt unchanged from B2f), qab (backend: HTTP/WS/IRC
  contracts + fault injection — kill/restart processes, interrupt
  storage, verify delivery/replay/ordering/seq density via documented
  interfaces; encouraged to build REPEATABLE probe scripts in its own
  checkout and re-run per head — the durable-tooling upgrade over B2f's
  ad-hoc checks).
- Two lanes: `qa-verify` (product) + `qa-verify-backend`; bootstrap
  registers + probes both pre-spend; lead files ≤2/pass TOTAL, lane
  chosen by where the risk lives, per-lane >8 backlog rails
  (QAV-BACKLOG / QABV-BACKLOG records).
- Port-collision law: the spec pins app ports, so two verification app
  instances can never run at once → orchestrate ALTERNATES the QA duty
  per pass (odd = qa, even = qab), each with its own integration cursor
  (integ_delta_for) so its pass message lists everything since ITS last
  active pass. Halves each QA's cadence (12 min); B2f closes were
  median-1-min, so headroom is ample. Also halves per-QA spend.
- Fork lineage: B2c proved the contracts vantage wins gates; B2d the
  product vantage wins ux; B2e that one mind can't hold both; B2f that
  direction/execution split works but execution depth on faults is the
  binding constraint. B2g = fork union with three minds.
- PREDICTION (pre-registered): ux ≥0.9 (product QA unchanged); gates
  ≥2/5 with the cross-node fan-out/seq signature specifically caught
  pre-verifier by qab correctives; risk axis = coder bandwidth now
  split across two corrective streams (B2f runs drained fully, so slack
  exists).
- OBSERVABLES: per-lane filed/claimed/closed + ages; probe-script reuse
  evidence in verify-checkout-backend (files + re-run traces); corrective
  attribution per lane; gate outcomes vs qab-lane activity; alternation
  health (no port-collision freeports kills during the other QA's pass).
- Validated free: bash -n; stub tasks-dual (both lane probes green
  in-container, MARATHON-1/-2); stub tasks regression (ALL INVARIANTS
  PROVEN — proven arm untouched). Persistent-session alternation is not
  stub-coverable (same limit as B2f); components are reused proven
  mechanics.

#### CODEX VET (2026-08-06): EXECUTE-WITH-FIXES — all folded
1. CRITICAL alternation only time-based → FOLDED: deliver to a QA only
   when the other's runtime_status is not `active` (ALTERNATION-SKIP
   record); skip keeps the cursor.
2. HIGH MARATHON_APP_BASE is a SHA used as a path → CONFIRMED, also
   silently degraded runs 19/20 (every "Current integrated head:" line
   was EMPTY; QAs worked off delta task@sha entries — noted for those
   as-run records). FOLDED: new MARATHON_APP_DIR=/app; both head sites
   use it.
3. HIGH cursors advanced before delivery known → FOLDED for dual:
   integ_delta_peek/commit split; commit only on delivered|pending,
   QA-DELIVERY-ERROR record otherwise. Lead-cursor variant of the same
   flaw exists in the proven single-QA path (integ_delta) — ACCEPTED
   RISK there (0 delivery errors in 2 full runs; byte-stability of the
   proven arm preferred).
4. HIGH rails ignore in_progress → FOLDED for dual lanes (open +
   in_progress, wording "outstanding"); single-lane tasks rail left
   byte-stable (never engaged in 2 runs).
5. MEDIUM durable-probe instruction too weak → FOLDED as prompt
   strengthening: first-pass REQUIRED probe script (start app, exercise
   interfaces, inject spec-claimed faults, check stated guarantees, log
   output), re-run+extend each later pass. Codex's harness-provided
   deterministic probe wrapper DEFERRED as the next arm if B2g misses
   the gates — B2g stays a clean test of agent-owned verification.
6. MEDIUM purity leak risk → PARTIALLY REBUTTED: dense/replay/sequence/
   fan-out are literal instruction.md text (4/2/2/3 occurrences);
   "ordering" was not — term lists dropped from both new prompts,
   replaced with "the guarantees the specification states".
7. MEDIUM risky path not covered free → FOLDED as bounded in-run smoke:
   the run's first ~5 passes are monitored against explicit assertions
   (qab READY+ACK, alternating deliveries both reach delivered/pending,
   no ALTERNATION-SKIP storm, no freeports kill during the other QA's
   active turn, both cursors advance); violation = abort early, ~$10
   exposure instead of $200. No separate pre-spend run.

#### AS-RUN (2026-08-06, loom-generic-tasks-dual-1, run 21) — PREDICTION
#### FALSIFIED: 0/5 gates + replica-ux 0.9375 → site 0.469
Mechanics flawless: smoke assertions all green, 34 passes, ZERO
ALTERNATION-SKIP / QA-DELIVERY-ERROR, 19 integrated (8 seed + 11
correctives) all first-attempt, $171.54, deadline finalize. UX held at
0.9375 — FIFTH consecutive run of the persistent-QA family at exactly
that profile, now proven robust even at HALF product-QA cadence. But
the gates: the SAME six tests failed as run 20 (cross-node WS fan-out
to a subscriber on another node, replay across a dead node, seq density
across Redis outage, IRC bridge-seq 2) — three runs, one signature, and
only run 19's dedicated event-log seed task ever passed it. API 93/129
(family worst; decomposition variance).
- The backend QA DID its designed job, deeply: built backend_probe.py
  on pass 1 and re-ran/extended it per head (46→58 of the app's own
  tests + launcher smoke each time), live cross-node REST checks over
  real 3-node clusters, HTTP-node respawn and Redis-SIGKILL checks,
  found real contract deviations (MessageObj.mentions shape,
  include_archived) → lanes: backend 11 filed/10 closed (median 2 min),
  product 4/4 (median 10 min); QA-RESULT 50:50 PASS:DEVIATIONS (most
  deviation-heavy run).
- WHY THE GATES STILL FAILED (two mechanisms, both now evidence-backed):
  (1) VOCABULARY: its fault checks verified RECOVERY (respawn, health,
  REST-after-fault) — the gates verify CONTINUITY (a WS subscriber on
  node B receives messages posted via node A DURING the fault, missed
  events replay on resume, seq stays dense THROUGH the outage). No
  agent probe across 3 runs has spontaneously reproduced that
  choreography. (2) TIMING: the fault-hardening correctives landed in
  the final 2 passes — no verify→corrective→fix cycle remained.
- NEW FAILURE MODE: corrective drain LOST the race for the first time —
  23 filed, 12 still open at deadline. Two verification minds generate
  findings faster than one coder drains them (the flagged risk axis).
- CONCLUSION (n=3 of the tasks family: gates {3,0,0}, ux 0.9375×3,
  site mean 0.569): agent-owned verification has hit its ceiling on
  these gates. The evidence now MANDATES the deferred arm — a
  deterministic continuity fault-suite (spec-derived: boot 3 nodes,
  subscribe WS on B, post via A, kill C / kill Redis, assert delivery
  during fault + replay on resume + dense seq) run per-integration as a
  harness gate or a frozen tool the QA invokes. Plus either a second
  coder or verified-priority correctives to fix the drain race.

### MAINTAINABILITY AUDIT (2026-08-06/07) — full report: docs/maintainability-audit.md
Four same-spec artifacts (baseline, runs 19/20/21) measured with a
codex-vetted two-stage instrument set (panel + Sonar + coupling + Semgrep +
sampled fault injection + the benchmark's own conformance suite as the
independent black-box check). Headlines: (F1) on the like-for-like JS pair
the ensemble beats the single session on EVERY quality instrument while also
winning gates — 13× lower Semgrep density, acyclic 49-module graph, real
tests (mutation 62%) vs none; (F2) test volume and independently-verified
correctness are INVERTED across the set (zero-test baseline: API 109/129;
most-tested run21: 93/129); (F4) the second QA agent bought no quality —
cleaner shape but a 7-module import cycle, red own suite (3/74 failing),
lowest mutation score; (F7) the benchmark is blind to all of it (identical
scores, 8× smell-density gap). Instruments and every number were themselves
codex-vetted; four real measurement bugs were caught and fixed before
reporting (SQL-literals-as-comments HIGH, .mjs scope, JS mutation classifier,
edit-while-running contamination). Scripts: scripts/maint-*.sh; data in the
trials repo docs/.

### B2j/B2k. maintainability-ownership FORK (PLANNED, supersedes B2i rev 2)
Full plan: docs/quality-eval-plan.md rev 3. User direction: generic loom
changes only, nothing task-specific; spec delivery untouched. NO metric is
fed to any agent (rev-2 QUALITY-line loop PARKED as a possible third arm) —
so the ENTIRE instrument set is held-out and the leakage question vanishes
structurally. One variable, campaign-style: WHO owns maintainability.
- B2j: persistent ARCHITECT session (`arch`, 4th controlled mind, vantage =
  the codebase as a structure): reviews each integrated head read-only,
  files refactor correctives via the app lane, ARCH: advisory comments on
  in-review designs; never blocks/edits/runs/changes status. Prototypes an
  `architect` stock role. +$20-40.
- B2k: no new agent — the lead's standing prompt gains a short generic
  maintainability section (decomposition preference, structural bar in
  design reviews, refactor correctives). Tests "ownership is a sentence";
  B2e zero-sum shadow watched via lead verify-task filing rate.
Both: base = verify_role=tasks byte-stable; Stage-A scorecard at finalize
(invisible to agents) + full audit toolchain post-run. Evidence targets: 0
cycles, median file ≤120, dup ≤1%, smells/KLOC ≤10, Semgrep ≤0.5/KLOC, own
suite GREEN, mutation ≥75%, blinded judge ≥ untreated; guards: site ≥0.469,
integrations ≥19, ≤$200. Fork verdicts pre-registered (ARCH>LEAD → structure
needs its own mind; LEAD≈ARCH → ship the sentence; both miss → run the
parked feedback arm). Ladder: stub → codex vet → two sequential runs.

#### B2j AS-RUN (2026-08-08, loom-generic-tasks-arch-1, run 22) — DESIGN-GATE
#### STARVATION: integrated=0, score 0.0, $79.23
The architect exercised its design authority immediately and substantively
(4 distinct spec-anchored rejections: IRC bind contract, fail-fast-vs-
resilience supervision conflicts) — and the pipeline NEVER RECOVERED. The
single planner spent the whole run redesigning the two foundational tasks
(start.sh launcher, node supervisor) under review cycles from TWO
reviewers (arch approvals were granted and later invalidated across
redesign rounds — the label churned through the event store); 8 of 11
tasks were never designed; the coder never made a single commit; the
verifier found no /app/start.sh (setup:false, reward 0). qa-verify: 0
filed (correct per duty — nothing ever integrated). Replica ux: NOT run
(no application exists to judge). Wiring itself behaved perfectly: zero
protocol violations, no gate ever engaged because no candidate ever
reached it.
- VERDICT: authority at the DESIGN stage without rejection bounds is a
  pipeline-serializing hazard: fail-open covers a SILENT architect, but
  nothing bounds REPEATED rejections, and one planner behind a strict
  design gate starves everything downstream. The rejection-spiral risk
  materialized at the checkpoint we considered cheap, not the one we
  guarded (integration).
- B2j-rev2 FIX (pre-registered for any rerun): (1) rejection bound — a
  design rejected twice is decided by the lead (arch comment becomes
  advisory); (2) arch design-gate applies to FIRST drafts only; redesign
  rounds go straight to the lead; (3) starvation valve — if no task has
  reached implementation by pass N (~8), the design gate becomes
  advisory for the remainder; (4) consider a second planner.
- Comparison note: same spend rate as untreated runs (~$2.4/pass) but
  ~40% lower total ($79 vs $178) because idle workers cost nothing —
  starvation is cheap, which itself is diagnostic.

#### B2k AS-RUN (2026-08-08, loom-generic-tasks-leadm-1, run 23) + FORK VERDICT
B2k: 25/25 integrated (fully drained), $191.42, deadline finalize — but
**0/5 gates, API 49/129 (family WORST), replica-ux 0.9375 → site 0.469.**
The maintainability sentence was INERT on every channel: ZERO refactor
tasks filed (its most concrete instruction), verify-task machinery
unaffected (34 filed/34 closed — no zero-sum attention theft), and the
artifact structurally indistinguishable-from-untreated (median file 100.5
≈ run21; dup 1.85% = family worst; max file 935). Most test code of any
run (41 files, ratio 0.57) against the worst independent API score — the
test-volume/correctness inversion at its most extreme. Behavioral
replication of Zhu et al.: prompt exhortation does nothing (p>0.8).

**FORK VERDICT (B2j vs B2k, both from frozen commit daddc68d1):**
- Exhortation (B2k): INERT — pre-registered category confirmed.
- Authority (B2j): not "ineffective" or "unexercised" but a category the
  pre-registration lacked: OVER-EXERCISED — design-stage authority
  without rejection bounds starved the pipeline to zero.
- CRITICAL SCOPE NOTE: the integration-stage gate — the checkpoint we
  hardened through two vets and 43 lifecycle assertions — was NEVER
  TESTED in vivo: no candidate ever reached it. Integration-stage
  authority remains an open question; design-stage authority as wired is
  answered (destructive without bounds).
- UX LAW extended: SIX consecutive runs at replica-ux 0.9375 (19, 20, 21,
  dual, arch-n/a excluded, leadm) — the persistent-product-QA half is
  invariant under every intervention tried; all variance lives in the
  correctness half.
- NEXT (pre-registered options): B2j-rev2 (bounded authority: 2-rejection
  cap, first-drafts-only design gate, starvation valve — and note the
  campaign evidence pattern actually favors testing INTEGRATION-stage
  authority alone, since the task machinery demonstrably moves agents and
  that checkpoint is unexercised), or the parked CI-style feedback arm.
  Cost note: fork total $270.65 for two runs.

### B3. fractal-generic — infrastructure COMMITTED
Mission mode `generic` (verbatim spec + finish sentence — the hardcoded
preamble is bypassed; strip-vet #3); hidden reserve pinned to 0; concurrency
caps at the machine ceiling.

```sh
PYTHONPATH=loomcli/harbor harbor run -p tasks/slack-clone \
  -a fractal_harbor:FractalAgent -m gpt-5.5 -e docker --env-file <anthropic.env> \
  --ak mission_mode=generic --ak reserve_budget=0 --ak max_cost=200 \
  --ak max_children=7 --ak max_descendants=7 --ak max_depth=8 --ak max_iters=1000 \
  --artifact /app -o trials --job-name fractal-generic-N -n 1 -y
```

Expected binding cap: money (~$200 ≈ t+194min at fractal-trial-1's burn
rate), before the wall clock. Disclosed, not hidden.

### B4a. agentflow-lead (plan-and-replan) — SOURCE-VERIFIED design
A codex LEAD session receives the verbatim spec + a mechanical tool
reference and generates AND drives the pipeline: author `pipeline.yaml` →
`agentflow validate` → `agentflow run --work-dir=/app` → read engine
results → revise → re-run, until done-when or budget/time end. Uniform
rule-4 relaunch applies.

Source-verified constraints baked into the tool reference (agentflow-go
main): delegating steps are **`type: shell`** with
`command: codex exec --model gpt-5.5 -- "$(cat <brief-file>)"` — the lead
writes its own brief files (`type: prompt` composition targets the claude
executor and must not be pointed at subprocess); `eval_loop`
(gate/fix_steps/max_passes) is real — fix steps are skipped in the main pass
and run on gate failure; steps run with cwd = the run dir (`--work-dir`);
per-step prompts/outputs are auto-saved (durability); DAG revisions between
runs should use fresh runs + `when:` conditions rather than `--resume`
(checkpoint skips by saved step state and predates the edit).

### B4b. agentflow-fixed (fixed DAG, disclosed-authored)
`pipeline.yaml` mechanically derived from the spec by a stated rule (one
step per top-level requirement section; edges = the spec's own order;
uniform retry/eval_loop; explicitly NO lesson-informed reordering). The
derivation rule ships in the manifest. A separate arm, never conflated with
B4a (vet-A #10).

### Bias-ledger + manifest template

```
trial: <name>  arm: <...>  tier: $200  date:
manifest: swe-marathon <commit> · image <digest> · codex <ver> ·
          <tool> <ver/sha> · prompts <sha256 list> · model <resolved> ·
          verifier <test.sh sha>
[O]: <auth, caps, completion sentence, admissible workarounds (rule 8)>
[S]: <MUST be empty>
[H]: <standard set: mirrors, spend rail, uniform sweep, uniform lifecycle>
sanitation: <pre-sweep state: clean | repaired(ports...)>
violations: <none | ...>
```

---

## Status

- As-run ($90 tier, disclosed-bias, n=1 each): baseline 0.469 · fractal
  0.4375 · loom 0.0625 — suggestive, not conclusive (vet-A #3/#9).
- Generic queue ($200 tier): baseline×3, baseline+persist×3, loom-generic×3,
  fractal-generic×3, agentflow-lead×3 (+B4b as budget allows), randomized
  blocks, k=2 verifier replays per artifact.
- Implemented: generic prompt bundle, loom critic-off mode, fractal generic
  mission + reserve-budget pin, adapter knobs. Pending: uniform
  lifecycle/sanitation wrappers for the baseline arms; `agentflow_harbor`
  build; manifest generator.
