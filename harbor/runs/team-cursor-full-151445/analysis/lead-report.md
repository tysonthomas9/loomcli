# LEAD lens report: SWE-Marathon `team-cursor-full-151445`

## Executive assessment

1. Final score was 0.1875. Correctness was binary zero: `gates_passed: 0`,
   `gates_total: 5`, with API 76/129, IRC 0/11, crash 1/3, chaos 1/3, and
   frontend journey 0/1. Evidence: `verifier/metrics.json:3-40`.
2. The task breakdown was not aligned to the reward. It described the right
   product surface, but the lead let the five reward-critical gates be
   represented by incomplete or unclaimed work. IRC (`MARATHON-15`) and
   cross-node/Redis resilience (`MARATHON-14`) were still `open`; the SPA chat
   task (`MARATHON-17`) was still `review`; files (`MARATHON-10`) was still
   `review`. Evidence: `digests/task-ledger.md:13-20,81-91`.
3. The dominant failure is orchestration/lead prioritization, amplified by a
   prompt that optimized for design review and independent tasks rather than
   binary gate completion. Product defects then followed: no IRC listener, no
   WebSocket route/fan-out, and no chat UI.
4. The lead transcript was not exported. Therefore claims about the lead's
   exact internal reasoning are bounded to the ledger, pass messages, daemon
   claims, integration log, and final artifact. Evidence: `README.md:11-14` and
   `digests/task-ledger.md:1`.

## Reward alignment and breakdown

5. The spec makes the cross-node event path and IRC first-class requirements:
   dense per-channel sequence, node survival, Redis-outage fan-out, and a
   bidirectional IRC bridge. Evidence: `spec/instruction.md:1-11,104-130`.
6. The ledger did create tasks for those areas, but their scheduling metadata
   put them behind a serial architect gate: `MARATHON-14` priority 0 with
   `architect,backend`, `MARATHON-15` priority 1 with `architect,backend`, and
   `MARATHON-13` priority 0 but still `in_progress`. Evidence:
   `digests/task-ledger.md:16-18,77-83`.
7. The files area was not merely late; it was left at `review` and had no
   implementation. Its title is explicit: “Files: upload, metadata, download,
   attach.” Evidence: `digests/task-ledger.md:13`.
8. The frontend reward was also explicitly described. `MARATHON-17` names
   channel creation, message history/composer, edit/delete, threads, reactions,
   empty states, and stable selectors, but remained `review`. Evidence:
   `digests/task-ledger.md:89-91`.
9. The breakdown over-invested in adjacent backend breadth: 15 tasks were
   integrated, including DMs, groups/invitations, settings, and verification
   tasks, while the gate owners never landed. Integration order shows M2, M3,
   M19, M20, M4, M21, M22, M5, M16, M6, M7, M12, M8, M9, and M18. Evidence:
   `orchestrate.log:495-550` and `app-git-log.txt:1-26`.
10. This is not a claim that those features were useless; it is a prioritization
    defect under a binary reward. Once auth, workspace, channel, and basic
    message APIs were sufficient to support a thin end-to-end slice, the next
    task should have been the gate, not more breadth.

## Lead sequencing and prioritization

11. Created-at order and integration order show a mostly FIFO/dependency walk:
    scaffold/auth/workspaces/channels/messages first, then SPA shell/settings,
    with no visible evidence of a gate-weighted reordering. The ledger timestamps
    place M10/M11 before M13-M18, but the integrated head reached M18 while M10,
    M11, M13, M14, M15, and M17 remained unfinished. Evidence:
    `digests/task-ledger.md:5-24`.
12. The lead's priority values did not produce reward-aware scheduling. For
    example, M14 was priority 0 but remained open, while priority-2 M9 and
    priority-2 M18 were integrated. Evidence: `digests/task-ledger.md:12,17,21`.
13. Architect labels became a hard cost center. M14, M15, and M17 needed
    design review before implementation; M17 was still `review` at finalization.
    The prompt explicitly says review tasks carrying `architect` are lead-owned
    and must be approved or rejected. Evidence: `prompts/lead-persistent-team.md:36-50`.
14. The process allowed a review to consume the opportunity without a deadline
    override. M18 required a critic rejection and second attempt, and integrated
    at 01:19:44, while the more valuable M17 remained in review. Evidence:
    `orchestrate.log:546-550` and `digests/task-ledger.md:89-95`.
15. Improvement (testable): add a scheduler invariant: once a task maps to a
    binary gate, it outranks all non-gate tasks after the minimum dependency
    set is complete. A run passes this check only if every gate has an assigned
    implementer and a QA task before any optional breadth task is claimed.

## Orchestrator messages versus what changed

16. Pass 1 began with about 198 minutes remaining and pass 29 still said about
    3 minutes remained. Every message was a generic “Follow your standing rules:
    review”; none names a gate, asks for a smoke test, or instructs abandoning
    optional work. Evidence: `lead-passes.log:3-31`.
17. The orchestrator did send repeated opportunities to review, but the result
    was not gate completion. Integration log shows no M14, M15, M17, or M10
    integration before finalization. Evidence: `orchestrate.log:492-553` and
    `digests/task-ledger.md:13,17-20`.
18. At 01:25:44 (nine minutes remaining) pass 28 was sent; at 01:31:45 (three
    minutes) pass 29 was sent; finalization happened at 01:35:10. There is no
    recorded emergency reprioritization between those messages. Evidence:
    `lead-passes.log:30-31` and `orchestrate.log:551-555`.
19. Improvement (testable): make every pass message include a live scoreboard:
    `gates passed/total`, gate owner/status, minutes left, and the highest-value
    executable task. At 30 minutes remaining the message must say “stop new
    designs; assign gate tasks”; at 10 minutes it must say “integrate the
    smallest end-to-end slice and run the relevant gate.”

## Lead behavior findings

### Finding L1 — No gate-first plan (highest impact)

20. Classification: agent behavior failure, with a harness prompt enabling it.
21. Evidence: the lead seeded/inherited 22 issues, but the final ledger still
    has the four directly named reward gaps: M10 review, M14 open, M15 open,
    M17 review. Evidence: `digests/task-ledger.md:3-24`.
22. Impact: all five correctness gates were zero even though health passed;
    this dominates the final score. Evidence: `verifier/metrics.json:3-40`.
23. Improvement: require the lead to write a gate matrix in the epic notes:
    API, crash, chaos, frontend journey, IRC; each row must name an owning task,
    dependency, implementer, and verification command. Refuse finalization if
    any row is unassigned or in review.

### Finding L2 — Failed to re-plan under explicit time warnings

24. Classification: agent behavior failure.
25. Evidence: warnings decreased from 198 minutes at pass 1 to 36 at pass 25,
    27 at pass 26, 17 at pass 27, 9 at pass 28, and 3 at pass 29, with no
    changed task strategy visible in the ledger or integration log. Evidence:
    `lead-passes.log:3,27-31` and `orchestrate.log:543-553`.
26. Improvement: add mandatory time-boxed replanning at 60, 30, and 10 minutes.
    The lead must explicitly cancel/deprioritize unstarted work, approve any
    gate design immediately if it is implementation-ready, and reserve the last
    10 minutes for integration plus one gate run.

### Finding L3 — Design-gate cost was not treated as scarce

27. Classification: harness/process failure causing agent behavior failure.
28. Evidence: the lead prompt requires architect review before implementation
    (`prompts/lead-persistent-team.md:36-50`), while the architect prompt says
    the architect writes design only and cannot implement (`prompts/team-architect-override.md:16-20`).
    M17 consequently remained review, and M14/M15 remained open. Evidence:
    `digests/task-ledger.md:81-91`.
29. Improvement: for gate-critical tasks, use a “thin design” path: one-page
    acceptance checklist, automatic approval after a bounded timeout, and direct
    implementation claim. Preserve full design review for non-critical breadth.
    Test: no gate-critical task may remain `architect`/`review` for two passes.

### Finding L4 — QA task generation did not target the official gates

30. Classification: harness/prompt/process failure.
31. Evidence: the prompt only says “at most two verification tasks” on an
    integration pass and none when a QA backlog rail is engaged:
    `prompts/lead-persistent-team.md:58-70`. The resulting QA tasks are M19-22
    (boot, respawn, auth, token), not IRC, chaos, frontend journey, or full API
    gate. Evidence: `digests/task-ledger.md:22-25`.
32. Improvement: replace the generic quota with five mandatory verifier tasks
    or one task with five explicit subcommands, and run them after each critical
    integration. A QA task is not “done” unless it reports the official gate's
    pass/fail count and a defect link.

### Finding L5 — No reliable lead evidence/ack path

33. Classification: harness/process failure.
34. Evidence: `README.md:11-14` says the lead transcript was not captured;
    `lead-passes.log:3-31` records only `deliver=pending` messages. This makes
    it impossible to distinguish an ignored pass, a failed command, or a late
    approval from the artifact alone.
35. Improvement: persist each lead response, command result, task mutation, and
    a pass summary. The orchestrator should fail loudly if a pass receives no
    acknowledged response within its interval, rather than continuing with a
    silent lead.

## Agent/session behavior evidence

36. The architect session repeatedly followed the design-only workflow and
    reported designs saved in review, e.g. M2 at `digests/app-architect-1.md:200-223`
    and M3 at `:469-505`. That behavior matches its role, but it creates a
    throughput bottleneck when the lead does not promptly approve.
37. M8's notes explicitly say its live cluster pytest was “left unrun” because
    port 8000 was held by another agent; it substituted dual TestClient coverage.
    Evidence: `digests/task-ledger.md:57-59`.
38. Classification: agent behavior failure (verification substitution) and
    harness scheduling failure (shared port contention).
39. Improvement: provide an isolated, harness-owned cluster fixture/port lock
    for each QA run; require a real `start.sh` gate before an implementation can
    be marked verified. Test failure: a task cannot claim “live boundary” while
    its own note says the live suite was not run.
40. The final orchestrator reported `integrated=15 failures=0`, but correctness
    still had 53 API failures and all IRC tests failed at socket connect. This is
    not necessarily a false claim by an individual agent—the harness's
    “integrated” metric means merge success, not product correctness—but the
    status wording is dangerously easy to read as done. Evidence:
    `orchestrate.log:553-555` and `verifier/metrics.json:32-40`.
41. Improvement: rename the metric to `merged=15`; reserve `complete` for a
    task whose official acceptance command passed.

## Product defects in `app/`

### Finding P1 — IRC gateway is absent

42. Classification: product defect, enabled by missing task execution.
43. Evidence: all 11 IRC tests failed with `ConnectionRefusedError` on the
    `:6667` socket. Evidence: `verifier/irc_pytest.json:7-28`.
44. The final app has HTTP routers only; `app/server/app.py:14-55` includes no
    IRC server startup, and `app/start.sh:1-49` has no gateway process.
45. Fix: implement a supervised `:6667` listener with token auth, NICK/USER,
    JOIN/NAMES/PING/PRIVMSG/QUIT, numerics, and bidirectional bridge into the
    shared message/event store. Add a smoke test that opens a socket before
    integration.

### Finding P2 — WebSocket and fan-out are absent

46. Classification: product defect.
47. Evidence: crash and chaos WebSocket tests receive HTTP 403, and crash
    restart also fails: `verifier/crash_pytest.json:28-47` and
    `verifier/chaos_pytest.json:28-49`.
48. `app/server/app.py:48-55` registers no `/api/ws` route. The event hooks are
    explicitly no-ops: `app/server/events.py:8-20,23-35,38-50,53-65,68-88`.
49. Fix: add authenticated subscribe/resume, SQLite dense event allocation in
    the same transaction as writes, local delivery, Redis cross-node delivery,
    Redis-outage fallback, and supervisor health checks. Run crash and chaos
    tests before declaring M13/M14 complete.

### Finding P3 — SPA chat surface was never integrated

50. Classification: product defect caused by M17 not being claimed/approved.
51. Evidence: UX judge reports no channel-create control, no message list, no
    composer, no thread panel activation, and no reaction picker. Evidence:
    `judge/ux.json:15-68`.
52. The final HTML contains channel list and a static “Select a channel”
    placeholder, but no chat controls: `app/static/index.html:127-171`.
    `app/static/js/shell.js:150-176` only sets the title and hides the
    placeholder; there is no message fetch/render path there.
53. Fix: integrate `chat_ui.js` from the M17 design, wire it to M7/M8 REST,
    render the empty state, composer, rows, edit/delete, thread replies, and
    emoji picker. Add the CUA journey as a required pre-finalization test.

### Finding P4 — SPA onboarding selector/visibility defect

54. Classification: product defect.
55. Evidence: frontend journey failed because Playwright found the display-name
    input but it was not visible: `verifier/journey_pytest.json:19-28`.
56. The HTML marks the auth modal hidden initially and places display name in the
    same form (`app/static/index.html:12-54`); the UI must explicitly switch
    modal mode before the journey fills registration fields.
57. Fix: make the signup/login mode transition deterministic, expose only the
    active mode's controls, and add a browser test that fills display name from
    a fresh page.

### Finding P5 — API breadth was integrated without final-gate verification

58. Classification: product defects plus verification failure.
59. Evidence: primary API suite passed only 76/129. Representative failures
    include files 404, slash commands posting literal bodies, WebSocket 403,
    missing read state, and wrong ordering/status codes. Evidence:
    `verifier/test-stdout.txt:32-49,62-84,87-107,142-149`.
60. Fix: run the official suite on the integrated snapshot, group failures by
    owning task, and block finalization until the selected gate is green or the
    run has explicitly switched to a score-maximizing fallback plan.

## Recommended replacement prompt/process

61. Add to the lead prompt immediately after decomposition:
    “Before any optional task, create a five-row gate matrix for API, crash,
    chaos, frontend journey, and IRC. Claim/assign one executable owner per
    row. Gate tasks outrank all breadth tasks after their dependencies are
    satisfied.”
62. Add: “At 60/30/10 minutes remaining, stop and re-plan. At 10 minutes,
    approve thin gate designs, stop new design work, integrate the smallest
    end-to-end slice, and run the official gate commands.”
63. Add: “A task is not complete because a candidate merged. Completion requires
    its acceptance command and the relevant grader gate result. Report merged,
    verified, and gate-passed as separate fields.”
64. Change architect gating: gate-critical tasks get a one-page design and
    automatic approval after two passes; design review cannot block a task past
    the 60-minute checkpoint.
65. Change QA generation: create API/crash/chaos/frontend/IRC verification
    tasks at seed time, with exact commands and artifacts. Do not use a generic
    “at most two” quota for a binary all-gates reward.
66. Add frontend weighting: once auth plus workspace shell works, prioritize the
    minimal channel-create/message/composer journey because it earns both the
    frontend gate and several UX rubric points. Defer settings, DMs, groups, and
    polish until that journey is green.
67. Add claim/yield rules: a worker may yield only after writing a machine-readable
    blocker and the orchestrator must immediately offer the task to the best-fit
    idle worker. A gate task may not remain `open` or `review` across two passes
    without an escalation record.
68. Add finalization guard: refuse deadline finalization when `gates_passed < 5`
    unless the lead has recorded a final score-maximizing decision and the exact
    unverified blockers. This makes the tradeoff visible and prevents “15
    integrated” from being mistaken for “15 accepted.”

## Bottom line

69. The lead had the right broad decomposition but optimized for orderly
    architect-reviewed breadth. The grader rewarded a narrower, gate-first
    vertical slice. The most valuable correction is therefore process-level:
    make the five gates the top-level plan, remove design latency for those
    tasks, re-plan on a timer, and treat merged code as unverified until the
    official gate proves it.
