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
