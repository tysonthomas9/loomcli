# PROTOTYPE — judge prompt v1 (LOOMCLI-62) — throwaway, do not ship

Answers one question: **does judge prompt v1 produce eval outputs worth
storing** when run against the real fixture sessions
(`docs/design/fixtures/agent-observability/`, LOOMCLI-54)?

Not production code. The production home for the prompt is the builtin
workflow `session-eval-task-runner.ts` (Phase B); this directory exists only
so the rubric can be reacted to and iterated fast. Capture per the prototype
skill: throwaway branch + assets linked from LOOMCLI-62.

## Run

```
python3 docs/design/prototypes/judge-prompt-v1/run_judge.py <session-id> [...]
```

- No args → runs the four contrast sessions (happy coder, sleep-900 coder,
  watchdog-killed child, planner).
- `--dry-run` composes the judge input without calling codex.
- `--model` defaults to `gpt-5.6-sol` (the v1 pin candidate); always passed
  explicitly as `codex exec --model` per LOOMCLI-53.

Outputs land in `outputs/`: `<sid>.prompt.txt` (exact judge input) and
`<sid>.json` (validated judge output + envelope).

## What to react to (the four decisions this drafts)

1. **Score anchors** — `judge_prompt_v1.md` §Scores: six bands per
   dimension; do the fixture scores land where you'd put them?
2. **Error taxonomy** — allowlist + `other:` escape (12 tags drafted).
3. **Insight instructions** — per harness|linter|prompt|skill, "change X so
   that Y", empty-preferred-over-filler.
4. **prompt_version scheme** — plain monotonic `v1`, `v2`, … (identity
   token, equality-compared only; model change ⇒ bump).

Reacted 2026-07-16 (all four locked, see LOOMCLI-62 resolution):
`idle_wait` records fact not fault; `score_rationales` added to the eval
record schema; efficiency = agent-chosen waste, now explicit in the prompt;
rubric v1 locked. Known gap: the fixture set has no instruction-violating or
false-success session, so adherence anchors below ~94 and the
`false_success_claim` tag are untested.
