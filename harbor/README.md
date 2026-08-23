# loom × SWE-Marathon — the loom agent ensemble as a Harbor benchmark agent

This directory makes the **entire loom multi-agent product** (lead / planner /
coder / critic on the codex backend, coordinated through fleet-db) run as a
custom agent on the [SWE-Marathon](https://www.swe-marathon.org) benchmark
(Harbor 0.20.0). The thing under test is loom's ensemble delivering one app
end-to-end — not a scripted single-AI pipeline.

Design doc (codex-vetted through 4 rounds): `~/.claude/plans/refactored-petting-steele.md`.
Companion for the Phase-3 driver-path variant: `~/.claude/plans/driver-task-runner-reference.md`.

## Layout

```
loom_harbor/          Harbor adapter (BaseInstalledAgent): install bundle+codex, run orchestrate.sh
bundle/build-bundle.sh  cross-compiles static linux loom + fleet-db (arm64/amd64) into bundle/dist/
scripts/bootstrap.sh    in-container: /app baseline, workspace, daemon profile, agentdefs
                        (planner=plan/needs_plan, coder-1=task/has_design), prompt override
                        staging, codex trust, preflight asserts
scripts/orchestrate.sh  heartbeat: lead seed -> daemon -> loop(lead orchestrate pass ->
                        critic per (task,attempt) -> ATOMIC check-before-FF integration gate ->
                        harness close) -> finalize (daemon stop, port sweep, evidence)
scripts/integration-check.sh  cheap candidate check in a disposable worktree (never touches /app)
scripts/spend.sh        aggregates codex session token usage -> est_cost_usd (the spend rail)
prompts/                lead-autonomous, lead-orchestrate, critic, fleet_task-override
stub/codex              fake codex for the free dry-run (drives every status hop, incl. the
                        deliberate integration-gate failure + revision loop)
test/run-stub-trial.sh  free full-mechanics trial under real Harbor + real task image
```

## The non-negotiable invariant

`/app` (the tree the verifier scores) only ever advances by **fast-forward to a
candidate that already passed the integration check in a disposable worktree**.
A failed check leaves `/app` byte-identical and reopens the task with feedback.
The harness — never an LLM — merges and closes. `test/run-stub-trial.sh` proves
this with a deliberately broken commit (T2 attempt 1).

## Ensemble lifecycle (all product-native)

1. **Lead seed** (headless one-shot `loom lead --prompt prompts/lead-autonomous.md
   --message "$(cat instruction.md)"`, `LOOM_LEAD_CONTROLLED=0`, stdin `/dev/null`):
   creates the epic + 6-10 tasks with dependency edges. No designs.
2. **Planner** (daemon-supervised, built-in `plan` role, task-filter `needs_plan`):
   claims design-less or needs-revision tasks, writes `--design`, sets
   `--status review --assignee ""` (built-in template already does this).
3. **Lead orchestrate pass** (periodic one-shot): approves plan-stage reviews
   (`--status open --remove-label needs-revision`) or rejects with FEEDBACK.
   Never touches implementation reviews; never closes.
4. **Coder** (daemon-supervised, built-in `task` role, task-filter `has_design`,
   ONE persistent worktree so successive tasks accumulate): implements, commits,
   comments `IMPL-DONE attempt=<n> commit=<sha>`, sets `--status review`,
   `loom complete`. Never closes, never publishes PRs (per-worktree
   `loom-prompts/fleet_task.md` override).
5. **Critic** (harness-invoked one-shot per (task, attempt) in a disposable
   detached checkout of the candidate): writes `CRITIC-VERDICT.txt`
   (`REVIEW attempt=<n> commit=<sha> APPROVED|CHANGES-REQUESTED — reason`);
   a missing/mismatched verdict counts as CHANGES-REQUESTED.
6. **Integration gate (harness, deterministic)**: candidate == coder head,
   `/app` HEAD is its ancestor, check passes in a disposable worktree →
   `git -C /app merge --ff-only` → `INTEGRATED` record → harness closes the task
   (which unblocks dependents in fleet-db's ready-queue).

## Running

```sh
# 0. one-time: build the linux bundle
harbor/bundle/build-bundle.sh

# 1. free full-mechanics dry-run (real Harbor, real image, fake codex)
harbor/test/run-stub-trial.sh

# 2. real paid trial (requires codex auth + ANTHROPIC_API_KEY for the CUA verifier;
#    per-trial spend confirmation is project policy)
cd ../swe-marathon && PYTHONPATH=../loomcli/harbor harbor run \
  -p tasks/slack-clone -a loom_harbor:LoomAgent -e docker \
  --ak spend_cap_usd=90 -o jobs --job-name loom-trial-1 -n 1 -y
```

Agent kwargs (`--ak k=v`): `stub`, `budget_secs` (14400), `reserve_secs` (2400),
`cadence_secs` (360), `spend_cap_usd` (90), `max_agents` (2),
`codex_auth_json_path` (default `~/.codex/auth.json`), `codex_npm_version`.

### Watching a trial from an agent session (cheap)

A trial runs 40 min–4 h and writes `<job>/<trial>/agent/orchestrate.log`. Tailing it from
the main (expensive) model re-spends the whole cached context on every notification — 27
`pass N` lines at 90 s cadence cost more than the trial. What worked on `team-cursor-122710`
(2026-08-22; the watcher's report was checked line-for-line against the logs):

1. Launch harbor **detached** (`subprocess.Popen(..., start_new_session=True, stdin=DEVNULL)`
   with stdout to a launcher log) so no tool timeout can kill it; the launcher log's final
   `HARBOR_EXIT=<n>` line is the terminal marker.
2. Hand the watch to a **Sonnet subagent** (read-only brief): paths of orchestrate.log, the
   launcher log, the evidence dir; the expected end time; "poll every 2 min, ignore routine
   `pass N (t+…)` lines, note `seeded:` / `impl-review:` / `INTEGRATED` / rejects / `finalize`;
   grep `daemon.out` for `classified error|fast-fail|liveness|FATAL`; when `HARBOR_EXIT`
   appears report ≤25 lines: exit code + finalize lines, every integration (task, attempt,
   delivered_by) and reject, last spend + `cursor-usage` file/result counts, daemon error
   lines or `none`, argv prompt-leak check". Forbid reading credential files by name.
3. **Do not rely on the subagent waking itself.** Its background poll ended its turn and it
   never re-reported; it produced the correct report only when messaged. So the parent arms
   one cheap fallback — a background `until grep -q HARBOR_EXIT <launcher.log>; do sleep 60;
   done` — and on that single wake-up sends the watcher "the trial has finished, report now".

Cost on that run: ~92k Sonnet tokens for the whole 33-minute watch + report, versus one
main-model turn per log line before.

### Keeping run evidence

`harbor/test/jobs/` is gitignored (hundreds of MB per run: fleet-db-noisy `daemon.out`, git
mirrors, redis snapshots, per-turn usage files). What later analysis needs is exported with
`harbor/scripts/export-run.sh <job> [--no-raw]` into **`harbor/runs/<job>/`** and committed:
orchestrate/integration/lead-passes logs, `daemon-filtered.log`, task ledger (`final-issues*.json`),
critic verdicts, `verifier/` (metrics + pytest JSONs, no screenshots), judge `ux.json`/verdicts/
driver report, the app snapshot, raw worker transcripts (`transcripts/`, ~25 MB; `--no-raw` skips)
and readable per-session digests (`digests/`, via `scripts/digest-transcript.py`), plus any
`analysis/` reports. ~30 MB per run with raw transcripts. The headless cursor lead's own
transcript is not captured by the runtime yet (known gap) — its work is visible in the ledger.

## Known deviations / notes

- The plan's optional `SHELL=/bin/false` guard on lead one-shots is intentionally
  NOT used: it risks breaking the backend's own shell tool; seed failure is
  detected instead by the epic-exists check (retry once, then hard-fail).
- v1 simplification: the harness runs the critic on EVERY implementation review
  (a strict superset of "lead flags which merit review"); the lead's flagging is
  advisory only.
- The spend rail sums codex's own session records (`scripts/spend.sh`), checked
  before each pass; it cannot preempt an in-flight session (documented
  limitation; loom's `usage.jsonl` is unreliable on the reaped Go leaf — codex R4).
- Real-mode codex npm pin defaults to `@openai/codex@0.142.5` (host-matching); override with
  `--ak codex_npm_version=...`.
- The stub trial runs with `--disable-verification` and a FORCED dummy
  `ANTHROPIC_API_KEY`: `tests/test.sh` reaches the CUA stage whenever `/app`
  serves health (gates only affect reward, not control flow — Opus review
  finding), so running the verifier on the stub app would spend real CUA money
  or hard-fail on the dummy key. Verifier plumbing is proven by the NOP gate.
- `LOOM_LEAD_CONTROLLED=0` + stdin `/dev/null` is deliberate: the two Opus
  reviewers disagreed on this path; direct source reading resolves it —
  `defaultCodexInvoker` (backend_codex.go) checks `isTerminal(os.Stdin)` and
  falls back to the one-shot `codex exec` path for non-TTY stdin, so the lead
  never attempts a TUI. The lead's error-path shell fallback masking a failed
  seed is defended by the epic-exists check (retry once, then hard-fail).
- Verified after review: two `pipe | python3 - <<heredoc` bugs (heredoc steals
  stdin from the pipe) were found by the Opus pass and fixed to `python3 -c`.
