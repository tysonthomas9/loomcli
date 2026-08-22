# Plan: `team` arm — run the SWE-Marathon harness on loom's first-class Team Templates

Status: rev 3 (2026-08-21) — vet fixes 1–8 IMPLEMENTED (codex, Claude-verified). P1–P5 green: P4 `test-team-gate.sh` 41/41; P5 stub dry-run `run-stub-trial.sh --team` job `stub-ensemble-173242` **11/11 ALL INVARIANTS PROVEN** (4/4 integrated incl. QA delivery, T2 attempt-1 gate failure with /app untouched, attempt 2 landed by the other implementer, 0 invariant violations). Residual risk before a paid run: the persistent `lead-persistent-team.md` path is not stub-testable (stub forces oneshot). No spend so far.

## POC results (host, free, loom built from `swe-marathon-team` = feature + harness merge, conflict-free)

- **P1 green**: `loom template apply fullstack-app --json` → created=9, materialized=4 worktrees on
  `TEAMPOC/<agent>` branches; re-apply → 9 `skipped_match`. **Needs fleet-db ≥ origin/main #151**
  (role `exclude_labels`); the bundle's pinned fleet-db `bccb9e9` rejects it with
  `unknown field "exclude_labels"`. Also: this branch's seeded `plan` role already carries
  `exclude_labels: [architect]`.
- **Product bug (macOS only)**: `AgentBranchName = <WORKSPACE_KEY>/<agent>` + `workspace create`
  defaulting `--branch` to the workspace name → `refs/heads/teampoc` (file) collides with
  `refs/heads/TEAMPOC/` (dir) on case-insensitive APFS; all 4 worktrees fail to materialize.
  Linux containers unaffected; host-side tests must use a non-prefix base branch.
- **P2 green** (production `cli.MatchTask` × stored role constraints): architect-labeled → only
  `app-architect`; designed+open+label-stripped → `backend-dev`, `frontend-dev` **and `qa-engineer`**
  (R1 confirmed; `backend` label only rescores 162 vs 10 — R2 confirmed); designed+`needs-revision`
  → nobody with an agent (deadlock confirmed); review → nobody; unlabeled undesigned → nobody.
- **P3 green**: `builtin:team-frontend-dev` honors `$WT/loom-prompts/team-frontend-dev.md`; stock
  prompt contains `loom data close`.
- CLI flag is `--label` (repeatable), not `--labels` — every occurrence below is to be read that way.

## Codex vet (REVISE) — required fixes, by severity

1. **R1/R4 in the product bundles** (not fixable post-apply: `loom role set` has no labels setter;
   `agentdef update` can't change repos; skills/max_priority/path_patterns don't partition;
   `cross_repo` → empty repos → `ResolveAgentRepos` returns nil → daemon ready query is NOT
   source-repo filtered, so devs see `qa-verify` tasks): give `qa-engineer`
   `task_filter: any, labels: [qa], exclude_labels: [architect]`, devs `exclude_labels:
   [architect, qa]`. `frontend-dev`/`backend-dev`/`qa-engineer` are shared across bundles and
   `checkSharedRoles` rejects divergence → change every bundle + bump revisions, or rename.
   The lead must then file QA work with `--label qa` and a defined completion contract.
2. **Reopen transitions** (three distinct paths, one `reopen_task` can't express them):
   (a) check-fail → dev: `--status open --remove-label architect --remove-label needs-revision
   --assignee ""`; (b) stale-base → dev: same + `STALE-BASE` comment, never `needs-revision`;
   (c) lead design reject → architect: `--add-label architect --add-label needs-revision`.
   **Approval must remove BOTH `architect` and `needs-revision`** (rev-1 removed only
   `architect` → revised designs deadlock). Team dev/QA overrides' "design unviable" path must
   add `architect` (stock adds only `needs-revision` → same deadlock). Open: a stale candidate
   reclaimed by the *other* dev lacks the commit — rework prompt must cherry-pick the recorded
   candidate, or enforce same-lane reclaim.
3. **Gate**: do not replay a merge in `/app`. In the detached gate worktree at `app_before`:
   `merge --ff-only $sha` else `merge --no-ff --no-edit $sha`; run integration-check; verify
   `/app` still at `app_before`; `git -C /app merge --ff-only $gate_head`. Provenance via
   `merge-base --is-ancestor $sha <wt HEAD>` over the delivery worktrees (incl. QA's if it can
   emit IMPL-DONE). Add `INTEGRATION-STALE` to `attempt_handled`'s ledger. Keep arch queue
   machinery disabled (assumes one linear branch).
4. **fleet-db bump**: `build-bundle.sh` builds `FLEETDB_DIR|../fleet-db` verbatim; point it at a
   commit ≥ #151 and assert the capability in bundle preflight.
5. **bootstrap rewrite**: keep `MARATHON_CODER_WT`/`PLANNER_WT` consumers alive (finalize uses
   them under `set -u`) or rewrite every consumer; trust-level loop over all materialized
   worktrees (it currently names planner/coder explicitly); `max_agents=4` set *before* apply.
6. Architect worktree is never synced with `/app` main by the supervisor → add the same
   `git merge main` pre-step to the architect override.
7. **P5 redesign**: stub mode forces `lead_mode=oneshot` and the fake codex has no app-server,
   so persistent team mode can't be stub-tested as written. Stub needs: `--label architect`
   seeding, architect design + approval label transitions, R1 resolution, and the 5-commit
   assertion relaxed for merge histories.
8. Four concurrent workers × fixed app ports → shared port lock or per-agent ports (free-port
   helper only installs with a verifier role); finalize emergency kill matches
   `loom task|plan|daemon` — add `agent`.
9. Minor: 360 s cadence does not bound approval latency (delivery may queue behind an active
   turn); IMPL-DONE contract cite is `fleet_task-override.md:120-127`.

Go/no-go unchanged: P1–P5 green and fixes 1–5 landed before any paid run.


## 0. Why

Every loom arm so far (B2b…B2k) hand-wires its agent team inside `harbor/`:
`loom agentdef add planner/coder-1`, plus persistent `loom lead` tmux sessions
named `qa`/`qab`/`arch` driven by `leadmsg`. The product now has first-class
teams: `loom template apply <ID>` (branch `feat/onboarding-templates-v1`,
`internal/teamtemplate/`, bundles `internal/teamtemplate/bundles/*.yaml`). The
`fullstack-app` bundle is almost exactly the shape B2j (architect) + B2d/B2f
(QA worker) were reaching for, but as roles/agents the daemon supervises:

| role | kind | task_filter | labels | exclude_labels | prompt |
|---|---|---|---|---|---|
| app-architect | worker | any | [architect] | — | builtin:team-architect |
| frontend-dev | worker | has_design | — | [architect] | builtin:team-frontend-dev |
| backend-dev | worker | has_design | — | [architect] | builtin:team-backend-dev |
| qa-engineer | worker | has_design | — | [architect] | builtin:team-qa |
| code-reviewer | interactive | — | — | — | builtin:pr-review (no agent) |

Hypothesis to test: a product-supported team (architect → split implementers →
QA) recovers run19's correctness half (3/5 gates) without the B2j design-gate
starvation, because the design gate here is label-routing + lead approval, not
an arch session with reject authority. Secondary: does two implementers in
parallel hurt the UX half (B2c-style contract breakage) or help it.

## 1. Branch / build

- `swe-marathon-harness` and `feat/onboarding-templates-v1` share base
  `662523caa`; `git merge-tree` of the two is conflict-free (harness touches only
  `harbor/`, `lead.go` G14 fix, `.gitignore`, a design doc; the two prompt
  edits are byte-identical on both branches).
- New branch `swe-marathon-team` = `feat/onboarding-templates-v1` + merge of
  `swe-marathon-harness`, in worktree `../loomcli-worktrees/marathon-team`.
  The user's `onboarding-templates-v1` worktree is not modified.
- `harbor/bundle/build-bundle.sh` builds whatever is checked out, so the
  bundle carries the template CLI + G12–G14 fixes. `bin/VERSION` records the SHA.

## 2. Adapter kwarg

`loom_harbor/__init__.py`: `team: str = "off"` → env `LOOM_MARATHON_TEAM=<id>`.
Shape guards in `orchestrate.sh`: `team != off` requires
`lead_mode=persistent`, forbids `arch=on`, `verify_role` must be `off` or
`tasks` (the template supplies its own QA worker; `tasks` additionally keeps the
lead-filed `qa-verify` lane — see §6 risk R4). Stub forces `team` through
unchanged (stub needs new branches, §7).

## 3. bootstrap.sh changes (the only product-surface swap)

Replace `bootstrap.sh:100-105` (`loom agentdef add planner …` /
`coder-1 …`) with:

```
loom template apply "$TEAM" --json > "$MH/template-apply.json"
# assert created=9 skipped=0 failed=0 (5 roles + 4 agents), materialized=4
```

- Daemon profile `max_agents` → 4 (adapter default for `team` arm; currently 2).
- Worktree assertions (`bootstrap.sh:107-110`) become a loop over
  `loom template show "$TEAM" --json | jq -r '.agents[].name'` →
  `/work/ws/worktrees/app/<agent>`; export `MARATHON_IMPL_WTS` (space list of
  the frontend-dev-1 / backend-dev-1 worktrees) instead of a single
  `MARATHON_CODER_WT`. Common-dir assertion (`:274-281`) loops the same list.
- Prompt overrides: the harness already overrides the coder prompt via
  `$WT/loom-prompts/fleet_task.md`. `generateWorkerPromptWith` routes
  `builtin:team-*` through the same `loadTemplate(id)` hook
  (`internal/cli/agent/worker_prompt.go:32`) with cwd = the agent worktree
  (`supervisor/spawn.go:40`). So: write
  `$WT/loom-prompts/team-frontend-dev.md`, `team-backend-dev.md`,
  `team-qa.md` overrides per worktree. **Required, not optional**: the stock
  team-*-dev prompts end with `loom data close <id>` (Step 7), which bypasses
  the harness integration gate (the gate is the sole closer). Override deltas:
  1. Step 0: `git merge --no-edit <integration-branch>` before starting (build
     on the integrated head; surfaces conflicts early).
  2. Step 7: replace `loom data close` with the existing IMPL-DONE contract
     (`loom data comment <id> "IMPL-DONE attempt=<n> commit=<sha>"`,
     `--status review --assignee ""`, never close) — verbatim from
     `fleet_task-override.md:118-127`.
  3. If the task already carries IMPL-DONE comments and a `STALE-BASE` comment,
     merge the integration branch, re-run tests, re-signal.
  The architect prompt is used unmodified (`--design` + `--status review` is
  exactly the planner hand-off the lead already reviews).
- codex `trust_level` entries for the four worktrees (`bootstrap.sh:132-150`
  already loops worktrees; confirm it picks up all four).

## 4. Lead prompt variant `lead-persistent-team.md`

Derived from `lead-persistent-verifier-tasks.md`, three deltas:

1. Seeding: every implementation task is created **with `--labels architect`**
   (`loom data create --type task --parent <EPIC> --source-repo app --labels
   architect …`). Soft routing hints as extra labels `frontend` / `backend`:
   role `skills` score against task labels (`task_router.go:326`,
   +50/match, fallback score 10 on zero match) — advisory only, since both
   dev roles share the same hard filter.
2. Design review protocol (replaces the needs-revision-only rule): a
   review-status task is lead-owned iff it has a design AND no valid
   `IMPL-DONE` marker. Approve = `loom data update <id> --status open
   --remove-label architect --assignee ""` (the label strip is what lets
   implementers claim: their roles `exclude_labels: [architect]`). Reject =
   `FEEDBACK:` comment + `--status open --add-label needs-revision --assignee ""`
   (label `architect` stays, so the architect — `task_filter: any` — re-claims;
   `team-architect.md` Step 1.5 handles the revision).
3. Unchanged: verification-direction duty (files `qa-verify` tasks when
   `verify_role=tasks`), never close, never run agents.

Harness fail-open (B2j lesson, reuse the gatelib 2-pass pattern): any
review-status task with a design, `architect` label, no IMPL-DONE, untouched
for 2 consecutive passes is auto-approved by orchestrate.sh with a
`DESIGN-AUTO-APPROVED` log line + comment. Counts reported in usage-summary.

## 5. Integration gate generalization (gatelib.sh)

Current `integrate()` assumes one coder branch: candidate must be an ancestor
of `$MARATHON_CODER_WT` HEAD and `/app` HEAD must be an ancestor of the
candidate, then `git -C /app merge --ff-only`. With two implementer branches
(`MARATHON/frontend-dev-1`, `MARATHON/backend-dev-1`) the second condition
fails whenever the other implementer landed first.

Change (team arm only, behind `$TEAM != off`; legacy path untouched):
- Candidate provenance: `git branch --contains <sha>` must include one of the
  template agent branches (replaces the single-worktree ancestry check).
- Disposable gate worktree checked out at `/app` HEAD, then
  `git merge --no-ff --no-edit <sha>`:
  - merge conflict → `STALE-BASE` comment, `--status open --assignee ""`
    **without** `needs-revision` (a designed task with `needs-revision` would
    be claimable by nobody: devs need `ReadyToImplement`, the architect needs the
    stripped `architect` label). Logged `INTEGRATION-STALE`.
  - merge ok → `integration-check.sh` as today; pass → replay the identical
    merge in `/app` (`git -C /app merge --no-ff --no-edit <sha>`), record
    `INTEGRATED … app_after=<merge sha>`, close; fail → existing
    `reopen_task` path (needs-revision back to the architect *and* re-add
    `architect` label so it is claimable — `reopen_task` gains that when
    `$TEAM != off`).
- `current_marker` / `attempt_handled` / idempotency ledger unchanged.
- When `/app` HEAD is already an ancestor of the candidate the merge is a
  fast-forward anyway, so single-implementer runs behave as before.

## 6. Known risks / open questions (vet these hardest)

- **R1 shared pool dev ↔ QA.** `qa-engineer` claims the same
  `has_design ∧ ¬architect ∧ ¬needs-revision ∧ open` pool as the two devs
  (`team-qa.md:28`). Claims are atomic, so QA can steal an unimplemented task,
  write tests against nothing, and park it in review. `loom role set` exposes
  no `labels`/`exclude_labels` setter, so the harness cannot post-apply
  narrow QA to a `qa` label without editing fleet-db directly. Options:
  (a) accept, measure the steal rate; (b) not start `qa-engineer-1`
  (`loom agentdef`/desired_state stopped) and keep the proven lead-filed
  `qa-verify` lane (verify_role=tasks) — but then the "team" is 3 agents;
  (c) product fix: bundle gives qa-engineer `labels: [qa]` and devs
  `exclude_labels: [architect, qa]`, lead files QA tasks with `--labels qa`.
  Leaning (c) as a one-line bundle change on the feature branch, since it is a
  real product gap, not harness-specific. Needs confirmation.
- **R2 frontend/backend split is advisory.** Both dev roles have identical hard
  filters; a backend task may be claimed by frontend-dev-1. Skills-scoring via
  labels is the only lever. Accept for v1, measure mis-routing from
  assignee vs label.
- **R3 design gate latency.** Architect → review → lead approval → dev. The
  lead pass cadence (360 s) bounds approval latency; fail-open at 2 passes.
  Compare integrated-task count vs run19 at equal wall-clock.
- **R4 verify_role=tasks + cross_repo.** Template agents are `cross_repo: true`
  (all workspace repos). The `qa-verify` lane is a registered repo
  (`loom repo add qa-verify ""`), so devs with cross_repo would also see
  `qa-verify` tasks — G12's `--repos app` guard no longer applies. Need to
  confirm whether `source_repo` routing or the `--repos` affinity is what
  gates claims on this branch; if cross_repo defeats it, v1 runs
  `verify_role=off` and relies on the template QA (making R1 more important).
- **R5 spend.** 4 supervised codex workers + persistent lead vs 2 + lead. Cap
  unchanged ($90 default; campaign used higher). Estimate ~1.5–2× run19
  ($178).
- **R6 worker-side merge.** Step-0 `git merge main` in a worktree whose branch
  is namespaced `MARATHON/<agent>`; `main` is visible because all worktrees
  share `/app`'s common dir. Conflict in the worker is resolved by the
  worker (codex) — same class as today's coder, but now it happens.

## 7. POC (free, before any spend)

P1 (host, local mode): on `swe-marathon-team`, `loom workspace create` with a
throwaway `app` repo → `loom template apply fullstack-app --json`: assert
9 created, 4 worktrees on `MARATHON/<agent>` branches, re-apply → 9 skipped_match.
P2 (host): routing table. Create tasks: (i) `--labels architect`,
(ii) designed+open no label, (iii) designed+`needs-revision`, (iv) review
status. For each role's constraints assert which match via the daemon claim
path (`loom daemon` with localdogfood-style stub or `SelectBestTask` test
harness) — specifically that (i) matches only architect, (ii) matches all
three non-architect workers, (iii) matches nobody, (iv) matches nobody.
P3 (host): prompt override reaches `builtin:team-frontend-dev` — drop
`loom-prompts/team-frontend-dev.md` in the worktree, render with
`loom agent <wt> --prompt builtin:team-frontend-dev --dry-run`/equivalent, grep
the IMPL-DONE text.
P4 (host, model-free, like `test/test-arch-gate.sh`): two implementer branches
diverging from `/app`; gate integrates A (ff), then B (true merge), then a
conflicting C → `INTEGRATION-STALE` + task reopened without needs-revision.
P5 (container, stub): extend `harbor/stub/codex` with branches keyed on the
team prompt headers ("You are a disciplined software architect", the dev
and QA headers); `test/run-stub-trial.sh` with
`STUB_EXTRA_AKS="--ak team=fullstack-app --ak lead_mode=persistent"` must
reproduce the existing 8 assertions plus: template-apply.json created=9, ≥1
`DESIGN-APPROVED` by the stub lead, both dev branches integrated, 0
`INVARIANT-VIOLATION`.

Go/no-go for a paid run: P1–P5 green and R1/R4 resolved.

Status: spec A + B landed; stub dry-run NOT yet run.

## Status (rev 5, 2026-08-22)

- Host runs of the template team (real codex and real cursor-agent, tiny stdlib app, 2 tasks
  each): architect → lead approval → implementer (yield rule routed `backend`-labeled work to
  backend-dev-1) → QA lane, all lanes green on both backends. Cursor was faster (95s/71s/150s).
- Product fixes found by those runs, on this branch and on `fix/team-template-runtime` (off
  feat/onboarding-templates-v1, gate green): agent-shell `loom` shadowing (zsh rc shim,
  eb1f09404 + b3caa4309), `workspace create` silent skipped checkout (9bdca6269), daemon socket
  path fallback (206336833), cross-agent skill yield (3881c0882).
- **Paid container run `team-small-002151`** (32 min work window, $6.92, persistent lead,
  verification off): the persistent-lead path is PROVEN — lead seeded 1 epic + 16 tasks in one
  turn, rejected the first design with spec-anchored FEEDBACK (`/api/health` contract), the
  architect revised via the needs-revision+architect route, the lead approved with the exact
  label stripping, frontend-dev-1 yielded and backend-dev-1 claimed. integrated=0 only because
  design (17 min) + revision (4 min) + implementation overran the window; no harness failures,
  0 INVARIANT-VIOLATION. Lead bug found and fixed (96d4160e3): it reset a freshly claimed
  in_progress task to open during seeding.
- **All-cursor arm wired (a34b9b670 + f8074adfa):** `--ak backend=cursor` runs lead AND
  workers on cursor-agent (`--ak cursor_api_key_path=<file>`; default
  `~/.cursor/marathon-api-key`; the adapter only uploads the file). The lead cannot run under
  the harness-wrapper PTY runtime — the wrapper has no cursor turn detector, so a supervised
  cursor TUI never reports idle (verified: stuck `active` at "Add a follow-up"), and it opens a
  Workspace Trust dialog first. loom now has a headless controlled lead runtime for cursor:
  seed turn + one `cursor-agent -p --force --trust --resume <chat>` process per inbox message,
  same leadmsg `--status`/delivery contract (host-verified: idle in 10s, queued message drained
  in ~2s, memory across turns). Workers are `-p --force` (print mode accepts `-f` for trust).
  cursor-agent installs cleanly in the task image family (ubuntu:24.04 arm64, 2026.08.11).
  Spend accounting (46ce57f25): the PATH `cursor-agent` is a shim that tees each turn's
  `system` (model) + `result` (usage) events into `/logs/agent/cursor-usage/` and spend.sh
  prices them per model (cursor.com/docs/models table; cap = codex + cursor). `--ak
  cursor_model=<id>` pins the model (default `auto`, priced opus-class when the served model
  is unknown). loom's own cursor usage parsing was fixed too (8f434d6a7, camelCase keys).
- **Codex vet of the cursor arm (REVISE, 12 findings) → fixed in d19e24040 + 2a7578b4c.**
  Real bugs it caught: the shim gated capture on `[ ! -t 1 ]` but workers run under loom's
  harness PTY, so no worker turn was metered (F1); a cross-process `leadmsg` on a busy
  headless lead leased the message and handed it back, reordering the queue (F2); a missing
  binary was reported "delivered" (F4); usage was counted on any event (F11); an all-cursor
  run still demanded codex auth (F5). Shim is now parent + FIFO + reader (exec + process
  substitution lost the final `result` line in ~1/6 PTY runs) and refuses unmetered turns
  (exit 97); bootstrap proves the usage dir writable at $0. Known, pre-existing, NOT fixed:
  (F3) `internal/cli/root.go` re-raises SIGTERM immediately, so no controlled runtime ever
  persists `disconnected` on finalize's pkill — mitigated for cursor by Pdeathsig=SIGKILL on
  every turn (no orphaned paid turn); (F7) runtime metadata updates are full-map writes
  without CAS — a concurrent `loom lead` vs deliverer write can drop a key (needs fleet-db
  CAS; rare, status re-persists each turn).
- Next: capped all-cursor container trial (`launch-team-cursor.sh`, needs the key file), then
  the full-budget run (`budget_secs=14400`) with the replica judge on the better backend.

