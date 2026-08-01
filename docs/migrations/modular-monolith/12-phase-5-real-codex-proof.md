# Phase 5 Packaged Desktop Real-Codex Proof

- **Status:** Twenty local positive rows accepted; four GitHub rows remain
  authorization-fenced. The proof-discovered fixes are packaged and final UI
  regression is waiting for macOS unlock
- **Date:** 2026-08-01
- **Loom implementation head:** `b080f316a`
- **FleetDB source head:** `539dd37`
- **Backend:** real Codex CLI using the packaged Desktop user's configured
  authentication
- **Required executions:** 24, comprising two distinct real executions for
  each of the 12 clean-workspace **New Agent** templates

This is the acceptance record for Phase 5's packaged runtime. It is not a
harness plan and it does not permit API-seeded success. Every row must be
initiated and observed through the packaged Desktop UI. CLI, FleetDB, and
GitHub commands may be used only after a run for read-only confirmation.

## Authorization boundary

The operator authorized packaged Desktop execution, the Fleet container gate,
24 real Codex workloads, and restart/crash/fail-closed testing on 2026-07-31.
GitHub mutation authorization named the literal placeholder
`<owner/repository>` rather than an actual repository, so GitHub-backed rows
remain fenced until that value is supplied. Live execution requires:

1. building and launching the packaged macOS Desktop application;
2. starting its Podman/integration dependencies;
3. running 24 real Codex-backed workloads, each of which may make multiple
   paid model calls;
4. creating branches, pull requests, comments, and review comments in one
   explicitly authorized GitHub test repository; and
5. restarting the packaged runtime to verify persistence and recovery.

No credential value, lease token, API key, transcript secret, or raw runtime
environment is copied into this record.

## Exact package preflight

Before the first model call, the proof operator must establish:

- the Desktop package was built from the two source heads above;
- `FLEET_DB_REPO` points to the Phase 5 FleetDB worktree rather than the stale
  default sibling;
- all six built-in workflow bundles are present and the embedded-bundle tests
  pass at `internal/infra/workflowdistribution/authoring`;
- the packaged Node runtime matches `desktop/NODE_VERSION` and passes its JIT
  smoke test;
- the Desktop launcher reports a healthy local runtime;
- Settings reports Codex as installed and authenticated without exposing the
  credential;
- the GitHub credential preflight passes before templates 4 and 5 are
  activated; and
- the authorized repository and its default branch are visible in the UI.

The package must not use `internal/workflows`, hidden environment overrides,
API-created agents, `loom driver run`, or synthetic task completion.

The read-only host preflight on 2026-07-31 found Node `v24.13.1` matching the
pin, npm `11.8.0`, Rust `1.92.0`, Codex CLI `0.145.0`, a discoverable sibling
Flue checkout, the exact Phase 5 FleetDB worktree, and 24 GiB free on the host
data volume. `codex login status` reported `Logged in using ChatGPT`. This
proves host prerequisites only; it is not packaged-runtime authentication or
execution evidence.

The initial packaged preflight built `Loom Agents.app` from Loom `4ffb6919d`
and the explicitly paired FleetDB `ebabc4b`. The build regenerated
and verified all six embedded workflow bundles, packaged Node `v24.13.1`, and
stamped both sidecars with their own repository provenance. The locally sealed
bundle passed `codesign --verify --deep --strict`; the packaged Node retained
`com.apple.security.cs.allow-jit`. Through Computer Use, the app replaced the
stale prior-version runtime using its normal launcher path, opened a healthy
runtime at port 63013, and created the durable workspace
`PHASE5-REAL-CODEX-20260731`. These are preflight facts, not execution rows.

The package was subsequently rebuilt and locally sealed at Loom `193eea2fa`
with the same paired FleetDB `ebabc4b`. The Loom sidecar reports that exact
commit, the packaged-builtin gate passes for all six workflows, and packaged
Node remains `v24.13.1`. Through the Desktop UI, the exact rebuild replaced the
failed prior runtime, became healthy at port 61806, reopened the durable proof
workspace, and emitted the ready event when the operator moved the blocked
Planner task back to Open.

The intervening restart was a valid failure probe. An earlier managed
`loom daemon` survived its local service, remained bound to workspace
`DYNAMIC-WORKFLOWS`, and kept polling the dead Fleet URL from that generation.
The next runtime raced the orphan and repeatedly started short-lived embedded
Fleet processes. Commit `193eea2fa` repairs the owner lifecycle: runtime state
records `daemon_pid`, a graceful service exit waits for the managed child,
crash recovery reaps a recorded orphan, and the supervisor rotates when the
active workspace changes. The confirmed legacy orphan accepted SIGTERM, and
the exact repaired package then started normally through the UI.

The positive Behavior, New Role, and Local Review rows ran on the subsequent
exact package at Loom `512b97e01` and FleetDB `ebabc4b`, after the trusted
bundled runner learned to deliver through the admitted local repository source
without creating or trusting a named `origin`. The package was then rebuilt at
Loom `96dc2b1fb` with FleetDB `ebabc4b` and used for rows 13-24. Those runs
exposed two additional runtime defects described below.

The repaired package is now rebuilt and ad-hoc resealed at Loom `b080f316a`
with FleetDB `539dd37`. Its post-signing SHA-256 values are Loom
`37cec459bd34c8252d0f4809652224d67d6cb44e124a420b5b66cbb8eda89bd9`,
FleetDB `7ff7f630e8dff1d670e54e68c88279770c4db0eff189e9144d4395bc8a0281ed`,
Desktop executable
`92645fe3cc5cac0cb70fb69e38f6396d3a73fcc76edd646fd79234f47eaf869e`,
and packaged Node
`a24fc0b8159b79409a6684222947edd13ffe1ebb3d6650c57a0a70559a56bded`.
All six packaged workflows, strict deep signature verification, Node
`v24.13.1` JIT entitlement/smoke, the full Fleet gate, Loom's 16-step Go gate,
and its six-step frontend gate pass. Launching this repaired generation is
waiting for the macOS screen to be unlocked; no repaired-runtime acceptance is
inferred from the build alone.

## Proof-discovered defects and repairs

Rows 23-24 exposed two defects that are preserved as negative evidence rather
than hidden from the acceptance record:

1. Codex tool calls use a login shell. That shell replaced the packaged PATH
   prefix and selected `/Users/tyson/go/bin/loom`, an older CLI without the
   current fenced IPC surface. Plain `loom data show` therefore bypassed the
   package and received HTTP 401 from embedded FleetDB. Loom `b080f316a`
   creates an owner-only controlled shell startup per agent, pins
   `LOOM_CLI_BIN` to the exact daemon executable, and defines `loom` as that
   executable even after login-shell startup files run.
2. A live Bug-triage run lost its worker registration after approximately 123
   seconds. The broad Planner then acquired the same Bug and later won the next
   Bug's first poll. FleetDB `539dd37` makes same-holder re-claim renew both the
   issue lock and worker registration without emitting another claim event.
   Loom `b080f316a` renews that claim from live process heartbeat and reserves
   fresh untriaged Bugs for a configured Bug role. The Bug role must add the
   `triaged` label; after human approval the Planner becomes eligible and the
   Bug role rejects the marked issue.

The original competing sessions and server timestamps remain defect evidence.
They do not count as extra acceptance rows and are not used to replace any row
in the matrix.

## First admission probe (not an execution row)

Through the packaged Desktop UI, the operator created Behavior Planner agent
`phase5-behavior-planner`, admitted the local fixture repository, and created
task `PHASE5-REAL-CODEX-20260731-1`. The trigger dispatched a real TaskRun, but
it failed before starting Codex (zero tokens, no transcript, no diff) with
`local_worktree_unprovisioned`. The durable task correctly converged to
Blocked, so this attempt is not counted among rows 01-24.

Read-only inspection proved the admitted checkout was a valid linked Git
worktree whose source repository intentionally had no `origin`. FleetDB stored
the clean absolute source path as the repository remote, but Source Control's
checkout verifier accepted only a named remote. Commit `e68159bd6` repairs the
contract without weakening it: a configured remote must still match exactly;
the only fallback is an admitted absolute local source whose Git common
directory is identical to the linked target. The exact ref fetch uses that
admitted path and does not mutate the user's remote configuration. The
packaged retry reached a real nonzero-token Planner transcript and rows 01-02
are accepted. Coder and Local Review additionally use the same token-free
admitted source for exact branch publish and fetch.

## Interactive Lead startup probe (not an execution row)

Creating `phase5-interactive-lead` through the packaged UI exposed a distinct
pre-model failure: Codex app-server remained alive but did not open its loopback
listener within one minute. Its isolated `sqlite_home` had still reconciled the
user's large global Codex state database. The prompt subsequently landed in the
fallback shell and was rejected as an unknown command, so this attempt has no
assistant transcript and does not count as row 13.

Commit `023217a66` gives each controlled Lead generation an isolated
`CODEX_HOME` while symlinking the existing `auth.json` and optional
`config.toml`; credential bytes are not copied. Both app-server and remote TUI
receive the same filtered environment. A local loopback diagnostic using the
same layout opened its listener promptly, the full `internal/leadcontrol` test
package passes, and the subsequent packaged UI sessions accepted as rows
13-14 completed normally.

## Isolation and ordering

Use distinct proof workspaces where task filters would otherwise compete.
Within a workspace, do not activate the next same-filter agent until both runs
for the current agent are terminal and its binding or assignment is disabled
through the UI.

1. Behavior Planner workspace.
2. Behavior Coder plus Local review workspace; Local review consumes two
   local-branch deliveries produced by Coder.
3. Review-triggered New Role workspace.
4. GitHub Bug-fix plus Review loop workspace; Review loop consumes the two PR
   cards produced by Bug-fix.
5. Interactive workspace for Lead, PR Review, and Custom prompt.
6. Advanced workspace. Planner first produces two designs; Task Runner then
   consumes two separately prepared designed tasks; Bug triage uses two
   canonical bug issues.

Every task, agent, DriverRun, TaskRun, and AgentSession identifier must carry a
unique `PHASE5-CODEX-20260731` proof prefix or be recorded next to the prefixed
parent that created it.

## Twenty-four execution rows

An empty scheduled sweep is not a run. Each Behavior or Advanced row requires
a child Codex execution. Interactive rows require normally completed durable
sessions, not sessions cancelled with **Stop**.

| Row | Template | Input shape | Required terminal behavior | Required artifact evidence |
|---:|---|---|---|---|
| 01 | Behavior Planner | Undesigned ready task A | Task in Review, unassigned, nonempty design; child exit 0 | DriverRun, TaskRun, transcript, zero repository diff |
| 02 | Behavior Planner | Undesigned ready task B | Same as row 01 with distinct task and run IDs | Distinct transcript and persisted design |
| 03 | Behavior Coder | Designed ready task A | Task in Review with exact `local-branch:<branch>@<40-hex>` | Exit-zero TaskRun, transcript, commit, nonempty diff |
| 04 | Behavior Coder | Designed ready task B | Same as row 03 with a distinct branch and commit | Distinct transcript and diff |
| 05 | New Role | Review transition for documentation task A | One exact trigger; task returned to Review and unassigned | Exit-zero TaskRun, transcript, documentation diff, no self-retrigger |
| 06 | New Role | Review transition for documentation task B | Same as row 05 with distinct IDs | Distinct transcript and documentation diff |
| 07 | Bug-fix | Ready canonical bug A in authorized GitHub repo | Task returned to Review with PR URL | Child transcript, exit 0, open PR, exact base/head/files |
| 08 | Bug-fix | Ready canonical bug B | Same as row 07 with a distinct PR branch | Distinct transcript and open PR |
| 09 | Review loop | Review card for row 07 PR | COMMENT review posted; cycle label added; card returned to Open | Child transcript, review URL, expected PR head SHA |
| 10 | Review loop | Review card for row 08 PR | Same as row 09 with distinct review/run IDs | Distinct transcript and review comment |
| 11 | Local review | Local branch from row 03 | Exact Review claim and terminal review handoff | Child transcript, durable diff, approval or blocking comment |
| 12 | Local review | Local branch from row 04 | Same as row 11 with distinct IDs | Distinct transcript and comment |
| 13 | Interactive Lead | Normal conversation/session A | Backend exits normally; session Completed with `finished_at` | User and assistant transcript entries; no generic diff required |
| 14 | Interactive Lead | Normal conversation/session B | Same as row 13 with a new session generation | Distinct completed session and transcript |
| 15 | Interactive PR Review | Review request A against an authorized target | Normal exit; completed orchestration session | Request and assistant review transcript; observed `pwd` and Git root |
| 16 | Interactive PR Review | Review request B against a distinct target | Same as row 15 | Distinct completed session and transcript |
| 17 | Interactive Custom prompt | Literal custom prompt conversation A | Normal exit; completed orchestration session | Stored Role prompt plus real user/assistant transcript |
| 18 | Interactive Custom prompt | Conversation B in a new session | Same as row 17 | Distinct completed session and transcript |
| 19 | Advanced Planner | Undesigned Open task A | Design persisted; task Review/unassigned; session Completed | Planning transcript and zero code diff |
| 20 | Advanced Planner | Undesigned Open task B | Same as row 19 with distinct IDs | Distinct transcript and design |
| 21 | Advanced Task Runner | Designed Open task A | Completed delivery or Review local-branch handoff | Implementation transcript, commit, nonempty diff |
| 22 | Advanced Task Runner | Designed Open task B | Same as row 21 with distinct IDs | Distinct transcript and diff |
| 23 | Advanced Bug triage | Canonical Open bug A | Task Review; session Completed; no product-code changes | Triage transcript, reproduction/root-cause comment, zero product diff |
| 24 | Advanced Bug triage | Canonical Open bug B | Same as row 23 with distinct IDs | Distinct transcript and triage comment |

## Evidence captured for every row

Record all applicable fields. A blank required field is a failed row.

| Field | Acceptance rule |
|---|---|
| Agent identity | UI-created agent name and durable agent/binding ID |
| Input identity | Exact task/card/PR/session target shown in the UI |
| Runtime identity | DriverRun and TaskRun, or orchestration AgentSession ID |
| Real backend | Runtime metadata identifies Codex; no stub backend or fake completion sentinel |
| Exit/result | Exit code 0 for accepted child runs; expected terminal state visible in UI |
| Transcript | At least two non-system entries and no HTTP 500 while switching agents |
| Task navigation | Run history exposes a clickable task ID that opens the task in the right-side panel without breaking the agent view |
| Delivery | Exact branch, commit, diff, PR, comment, or review required by the row |
| Screenshot | Before-run settings, running state, terminal state, and transcript/artifact view |

The evidence tree must contain zero matches for:

```text
Completed by the built-in local task runner.
```

It must also contain no issued raw lease token. Token checks compare against
the issued value in process-private verification data; they do not publish the
token into this document.

## Cross-row Phase 5 acceptance

After positive rows complete:

1. switch repeatedly among at least three agents and two runs per agent;
   transcripts must continue loading without HTTP 500;
2. click each visible task ID; it must open in the task side panel and preserve
   the selected agent page;
3. restart the packaged application and verify completed run history,
   transcripts, diffs, task links, and terminal states persist;
4. interrupt one in-progress Codex child by terminating only the owned backend
   process, then verify stale recovery uses the exact owner generation and
   converges without two simultaneous claims;
5. run one task with Codex unavailable and verify it fails closed to Blocked,
   creates no fake transcript/diff/PR, and remains Blocked after restart; and
6. verify deletion of an actively claimed card is rejected with a visible
   conflict while the run can finish.

These failure scenarios supplement the 24 positive executions; they do not
replace any row.

## Read-only post-run checks

After the Desktop-driven actions finish, record every read-only command used.
Permitted examples include `gh pr view`, `git show`, `git diff`, process log
reads, packaged manifest reads, and stable Loom/FleetDB read commands. Do not
repair, requeue, seed, approve, bind, create, or complete work through CLI or
HTTP during acceptance.

## Result ledger

Populate this table only from live evidence.

| Rows | Result | Artifact location | Notes |
|---|---|---|---|
| Admission probe (not counted) | EXPECTEDLY EXCLUDED | Task `PHASE5-REAL-CODEX-20260731-1`; session `flue-promptagent-automation-run-6fcc3847887a8447115b237a54506ee8-PHASE5-REAL-CODEX-20260731-1` | Failed before Codex with `local_worktree_unprovisioned`; zero tokens/transcript/diff; fixed at `e68159bd6`; the distinct retry is row 01 |
| 01-02 | PASS | Planner tasks `PHASE5-REAL-CODEX-20260731-1` and `PHASE5-REAL-CODEX-20260731-2` | Distinct real Codex transcripts and persisted designs; both tasks reached Review unassigned with zero repository diff. A third Planner run is retained as extra evidence. |
| 03 and 11 | PASS | Task `PHASE5-REAL-CODEX-20260731-1`; Coder `automation-run-3fe81b4ca81b5271ed1f5a00a26ae6d0`; Local Review `automation-run-b5f0c53b42c4e9eb697db5ff58dc7048` | Coder delivered `loom/PHASE5-REAL-CODEX-20260731-1@75b06c386c7ea062a10e901699da8afb7b640b82`; Local Review completed with transcript and approval. |
| 04 and 12 | PASS | Task `PHASE5-REAL-CODEX-20260731-4`; Coder `automation-run-f06d2230c5b59f76f3c91e9ac2b2dab0`; Local Review `automation-run-6038117ca334ee04b931bea9bdee3d6f` | Coder delivered `loom/PHASE5-REAL-CODEX-20260731-4@fb3be81b03b44be011de927ac55dc65461af6613`; Local Review completed with a distinct transcript and approval. |
| 05 | PASS | Task `PHASE5-REAL-CODEX-20260731-5`; DriverRun `automation-run-dcc071f9feb1be904f91048bd5fc38f7` | Review-triggered role completed once, returned the task to Review, ran `npm test` (2 passed), and delivered documentation-only branch `loom/PHASE5-REAL-CODEX-20260731-5@5bd00278c986e5634958735eb89b0c0617c8a53d`. |
| 06 | PASS | Task `PHASE5-REAL-CODEX-20260731-6`; DriverRun `automation-run-229b9b804e2213604c7c61d6a0bffb29` | Distinct review-triggered run completed once, returned to Review, ran `npm test` (2 passed), and delivered documentation-only branch `loom/PHASE5-REAL-CODEX-20260731-6@26ee8ba8d6d5d1d0a40ae51ee1aae7468b46fa76`. |
| 07-10 | FENCED | PENDING | The authorized value is still the literal placeholder `<owner/repository>`; no GitHub mutation is attempted until an actual repository is supplied. |
| 13-14 | PASS | Lead sessions `session-adddb569-81c2-4309-a477-717ee7944448` and `session-08582d38-9c2c-475c-a332-c2799fb1466f`; `/tmp/phase5-row13-lead-transcript.json`, `/tmp/phase5-row14-lead-transcript.json` | Distinct normal-exit real-Codex sessions, four transcript entries each; SHA-256 `df6cf37a...` and `82d407e8...`. |
| 15-16 | PASS | PR Review sessions `session-f17e3049-0f0d-42de-995f-bd28c5e2cfca` and `session-3e509bc1-9ddf-46f4-9390-d150984c901a`; `/tmp/phase5-row15-pr-review-transcript.json`, `/tmp/phase5-row16-pr-review-transcript.json` | Distinct completed local review-target conversations; no GitHub mutation was performed. SHA-256 `a9902a42...` and `d7dfb109...`. |
| 17-18 | PASS | Custom Proof Librarian sessions `session-5107e222-26cb-4003-8533-ba270f877e71` and `session-79601615-f903-49bc-aa82-8dacda41cce3` | Stored custom role prompt plus distinct four-entry real-Codex transcripts. SHA-256 `f073c5d3...` and `e0a711c4...`. |
| 19-20 | PASS | Tasks `PHASE5-REAL-CODEX-20260731-7` and `PHASE5-REAL-CODEX-20260731-8`; sessions `20260801-084004-phase5-advanced-planner--76d26a61` and `20260801-171735-phase5-advanced-planner--315d85d0` | Distinct persisted designs and zero code diff; transcript SHA-256 `b840bc71...` and `0c7e1bbf...`. |
| 21-22 | PASS | Sessions `20260801-172733-phase5-advanced-task-runner--c9016b2b` and `20260801-173243-phase5-advanced-task-runner--d53e335e`; commits `4833f71a09777f2059fa0583b889987684c2e47e` and `64a25e3fb6b7f4e4344638ab42bc3740043f8aeb` | Distinct implementation transcripts/diffs; tests passed. Transcript SHA-256 `1f0f2ed5...` and `aa72ed93...`. |
| 23-24 | PASS WITH DEFECT FOLLOW-UP | Bugs `PHASE5-REAL-CODEX-20260731-9` and `PHASE5-REAL-CODEX-20260731-10`; sessions `20260801-173933-phase5-advanced-bug-triage--5398eba2` and `20260801-174622-phase5-advanced-bug-triage--44c7885d` | Both triage runs completed to Review with reproduction/root-cause comments and no product diff. Transcript SHA-256 `fb8439d8...` and `08c3ea5a...`. Their surrounding race exposed and motivated `539dd37`/`b080f316a`; the repaired package regression remains pending unlock. |
| Transcript switching and task-side-panel checks | PARTIAL PASS | Packaged Desktop rows 13-24 and persisted transcript exports | Repeated transcript reads succeeded, but the repaired package must repeat the agent-switch/task-panel UI check after unlock. |
| Restart persistence | PASS ON PRE-REPAIR PACKAGE | `/tmp/phase5-row17-after-restart.json`, `/tmp/phase5-row20-after-restart.json` | Exact transcript bytes persisted across restart; repaired-package restart remains a final regression. |
| Crash/stale recovery | DEFECT REPRODUCED AND PATCHED | Competing Planner/Bug-triage sessions and daemon log timestamps; Fleet `539dd37`, Loom `b080f316a` | Owner loss and clean stop/requeue were observed. New lock/worker renewal and deterministic route reservation pass unit/full gates; packaged regression awaits unlock. |
| Codex-unavailable fail-closed path | PENDING | PENDING | No fake evidence permitted |
| Active-card deletion conflict | PENDING | PENDING | UI conflict required |

---

[Phase 5 decisions and evidence](11-phase-5-decisions-and-evidence.md) ·
[Agent creation templates](../../product/agent-creation-templates.md)
