# Evidence pack — SWE-Marathon run `team-cursor-full-151445` (all-cursor team of 4 + headless cursor lead)

Task: build "Huddle", a Slack-like team chat cluster (spec: `spec/instruction.md`; the official grader
is `spec/tests/` — pytest gates + a CUA/UX rubric `spec/tests/rubric.json`). Budget 3h20m work, $180 cap.
Outcome: 15 tasks integrated, $65 spent, but **score 0.1875**: correctness reward 0 (gates 0/5; pytest 76/129;
IRC 0/11; crash 1/3; chaos 1/3; journey 0/1) and replica UX 0.375 (auth + layout PASS; polish/realism
PARTIAL; channels/messaging/threads/reactions FAIL — the SPA has no channel-create control, no message
list, no composer).

Team (see `prompts/` for the exact role prompts; `team-agents.tsv` for the roster):
- lead (headless cursor-agent, persistent): seeds epic + tasks, approves designs, reviews, re-prioritizes.
  Its transcript was NOT captured; reconstruct from `digests/task-ledger.md` (every task's notes/design),
  `lead-passes.log` (orchestrator → lead messages), `daemon-filtered.log`, `orchestrate.log`.
- app-architect-1, backend-dev-1, frontend-dev-1, qa-engineer-1: loom workers, one cursor-agent print-mode
  session per claimed task. Raw stream-json in `transcripts/<agent>.log`; READ THE DIGESTS FIRST:
  `digests/<agent>.md` (per session: prompt head, every shell command + truncated output, edits,
  assistant text, final result with tokens). Regenerate with `python3 digest.py transcripts/X.log`.

Other evidence:
- `app/` — the final integrated artifact (what was graded). `app-git-log.txt` — integration history.
- `critic/critic-MARATHON-N-*.log`, `critic/check-*.log` — per-candidate review gate (codex critic) verdicts.
- `integration.log`, `orchestrate.log` — what got integrated when; `usage-summary.json` — spend.
- `verifier/` — official correctness run on the artifact: `metrics.json`, `pytest.json`, `irc_pytest.json`,
  `crash_pytest.json`, `chaos_pytest.json`, `journey_pytest.json`, `test-stdout.txt`, `server.log`.
- `judge/` — replica UX judge: `driver-report.txt` (what the browsing agent saw), `verdicts.json`, `ux.json`.
- `daemon-filtered.log` — loom daemon events (claims, yields, restarts, classified errors) without fleet-db noise.

Timeline: seed pass 22:15Z; finalize 01:35Z (deadline). Times in transcripts are UTC.
