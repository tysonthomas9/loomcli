# Frontend-dev-1 evidence review

## Executive conclusion

The final score was 0.1875: correctness reward 0, 0/5 correctness gates, 76/129
pytest tests, IRC 0/11, crash 1/3, chaos 1/3, and journey 0/1 (README.md:3-5).
The dominant UX loss was not a missing backend capability. The backend message
routes existed, but the frontend chat task never reached an implementation agent.
The final SPA is an authentication/workspace/settings shell with an unwired
message pane. This is primarily a harness/process and scheduling failure, with
a secondary agent-behavior failure (the implementer never reclaimed or escalated
the high-value chat task), plus a real product defect in the shipped SPA.

The highest-value remedy is to make the core journey a release gate: M16 shell,
then M17 chat, then M18 settings; do not spend the last implementation window on
settings or architecture while M17 is still unimplemented. Require a browser
smoke check that registers, creates a channel, selects it, posts a message,
opens a thread, and adds a reaction before an implementation task can be marked
done or the run can finalize.

## Timeline and session accounting

1. The run began at 22:15Z and frontend-dev-1's first captured session began at
   23:29:25.756Z, 74 minutes later (frontend-dev-1 digest:2,16). The harness had
   repeatedly logged `waiting before restart` for frontend-dev-1, for example at
   22:28:51.181Z (daemon-filtered.log:452), 22:29:21.236Z (:469), and
   22:30:21.517Z (:503).

2. Session 1 was MARATHON-16 (digest:16). It merged main at 23:29:32
   (digest:21), implemented `static/index.html`, CSS, and JS (digest:120-148),
   and ran the focused suite: `66 passed` at 23:32:53 (digest:151-159).
   It also exercised the UI with Playwright and curl across ports 8000-8002
   (digest:160-188). It committed f4cf481 and declared delivery at 23:35:12
   (digest:354-371); the harness integrated it at 23:36:55
   (orchestrate.log:520-521).

3. Session 2 was MARATHON-18, not MARATHON-17 (digest:396). It started at
   00:46:58 (daemon-filtered.log:4582-4583), after another long idle period.
   It implemented settings and a channel-member route, ran 201 tests, and
   delivered 6c6170f at 01:00:18 (digest:797-825). Its own notes say the
   second live pass was blocked by port contention (digest:799), and it left
   `data/redis/redis.conf` dirty (digest:789-795).

4. The critic rejected that M18 candidate at 01:11:25, then the agent was
   restarted for a revision (integration.log:24; digest:874-986). Session 3
   merged main to restore messages/DMs, ran 220 tests, and reported `UI_OK`
   (digest:936-1072). During cleanup it was repeatedly fighting a respawning
   `start.sh` and occupied ports (digest:1073-1115).

5. At 01:14:51.197Z the daemon killed the frontend agent with code -1 and
   classified it as `agenterr: [Timeout] : connection timeout`; it saved a
   checkpoint, reset M18 to open, and waited for restart (daemon-filtered.log:
   5218-5231). This is the requested 01:14:51 silence-watchdog event. The
   digest's last work was cleanup/status checking, not M17 implementation
   (digest:1071-1115).

6. Session 4 was effectively not a frontend implementation session: the
   next relevant task assignment was MARATHON-11 much later, after fallback
   yields at 01:32:02 (daemon-filtered.log:5755-5778). There are only four
   captured frontend sessions, and none is a MARATHON-17 implementation.

## Why MARATHON-17 never shipped

7. The ledger says M17 was the frontend implementation task: it owns channel
   creation, selection, message history/composer, threads, reactions, and the
   normative `channel-*`, `message-*`, `thread-*`, `reaction-*`, `emoji-*`, and
   `create-channel-modal` selectors (digests/task-ledger.md:89-91).

8. M16 explicitly deferred “Messaging, channel CRUD UI, and settings modals”
   to later SPA tasks (digests/task-ledger.md:85-87). M18 explicitly says its
   channel settings UI “does not require MARATHON-17 message composer”
   (digests/task-ledger.md:93-95). Therefore M18 could not legitimately satisfy
   the missing chat surface.

9. The frontend prompt told the worker to select “ONE” task, choose the highest
   priority designed task, and exit if no designed task was available
   (prompts/team-frontend-dev-override.md:4,23-40). It also says “Do NOT keep
   polishing” and “Simply EXIT” after one task (prompts/team-frontend-dev-
   override.md:126-129). That is a reasonable single-task contract, but there
   was no explicit priority override for the core journey or rule saying that a
   chat task must preempt settings work.

10. The daemon assigned M18 to frontend-dev-1 at 00:46:58 with a skills match
   (daemon-filtered.log:4582), while M17 was not assigned to that worker. At
   01:14:18, immediately after architect work ended, the daemon assigned M17 to
   app-architect-1 with `reason="fallback: no skill match"`
   (daemon-filtered.log:5197-5208). The architect prompt explicitly says its
   role is “Design Only - No Implementation” and “You produce designs. You do
   NOT write application code” (prompts/team-architect-override.md:2-20).

11. The architect therefore produced a design and explicitly reported
   “No implementation was done” and completed M17 as review at 01:21:53
   (digests/app-architect-1.md:5250-5265). This is the direct reason M17 never
   shipped: an implementation task was routed to a design-only role near the
   deadline, with no remaining frontend slot or finalization gate that blocked
   on the missing implementation.

12. The daemon also shows fallback inefficiency. Frontend-dev-1 yielded M5 to
   backend-dev-1 at 22:59:54 because it was a “better-fit idle peer”
   (daemon-filtered.log:1408-1414), and later yielded M8 at 00:40:58 for the
   same reason (daemon-filtered.log:4421-4423). These yields were locally
   sensible, but the scheduler treated the worker as a general fallback source
   instead of reserving it for the frontend-critical path.

13. The result was wasted wall time: repeated 30-second restart waits dominate
   the daemon log, while the core frontend task remained unclaimed. The run
   spent the final active window revising M18 and designing M17 rather than
   implementing M17. This is a harness scheduling/process failure, not evidence
   that the frontend agent falsely claimed M17 done.

## Ranked findings

### P0 — Core frontend task was routed to a design-only agent

Type: harness/process failure.

Evidence: M17 was claimed by app-architect-1 at 01:14:18 with
`fallback: no skill match` (daemon-filtered.log:5207), while that role's prompt
forbids implementation (prompts/team-architect-override.md:16-20). Its final
message says “No implementation was done” (digests/app-architect-1.md:5253-5265).

Improvement: add a scheduler invariant: a task labeled `frontend` without
`architect` may only be claimed by a role whose capability includes
`implementation`; “fallback: no skill match” must never cross role boundaries.
At finalization, fail the run if any P0/P1 UX task is `review` with only a design
and no integrated implementation commit.

### P0 — No dependency or priority gate protected the chat journey

Type: harness/process failure.

Evidence: the ledger separates M16 shell, M17 chat, and M18 settings, but M18's
design expressly does not require M17 (digests/task-ledger.md:85-95). The
integration log shows M18 integrated at 01:19:44 while M17 remained design-only
(integration.log:24; digests/app-architect-1.md:5253-5265). Final metrics were
0/5 gates and 0/1 journey (verifier/metrics.json:16-40; verifier/journey_pytest.json:7-29).

Improvement: make M17 a hard predecessor for M18 and for finalization, or make
the lead explicitly order `M16 -> M17 -> M18`. A parallel settings task may run,
but it cannot consume the only frontend implementer while M17 is open.

### P0 — The shipped message pane is a product defect, not merely a test gap

Type: product defect in `app/`.

Evidence: `app/static/index.html:149-162` contains only a hidden header and the
placeholder “Select a channel”; `app/static/index.html:163-171` contains only a
hidden “Thread” placeholder. `setActiveChannel()` only changes active-row
classes, header visibility/title, and hides the placeholder at
`app/static/js/shell.js:150-176`; it never fetches or renders messages.

The judge observed exactly this: zero `textarea` elements on all three nodes,
blank `#general`, and no message list/composer (judge/driver-report.txt:5,
13-14; judge/verdicts.json:19-24).

Improvement: implement `static/js/chat_ui.js` from the M17 design. On channel
selection call the message-list endpoint; render message rows, author/avatar,
timestamp, empty state, composer, post/edit/delete, and reload persistence.
Add a browser test that asserts a posted body survives hard reload.

### P0 — Channel creation was absent from the final UI

Type: product defect in `app/` caused by M17 not shipping.

Evidence: `app/static/index.html:141-146` has the channel list but no create
control. The judge's source search found no `createChannel`, `New channel`, or
`channel-create` (judge/driver-report.txt:10-11), and the judge could only see
`#general` (judge/verdicts.json:11-16).

Improvement: add the M17 `create-channel-modal`, a visible sidebar button, POST
to `/api/workspaces/{slug}/channels`, append the returned row, and show inline
409 duplicate-name feedback. The journey must create a second channel before
selecting it.

### P1 — Threads and reactions were structurally unreachable

Type: product defect in `app/`, downstream of missing M17.

Evidence: the thread aside is permanently hidden in
`app/static/index.html:163-170`, with no message row action to unhide it. The
judge found no hover action or emoji picker and noted the backend endpoints
exist but no `static/js/` caller exists (judge/driver-report.txt:16-20;
judge/verdicts.json:27-40).

Improvement: render per-message thread and reaction controls, open the thread
aside from a message row, post replies through the existing REST routes, and
render an in-DOM emoji grid (not `prompt()`). Add smoke assertions for reply
count and reaction badge.

### P1 — Official journey failed before it exercised the product

Type: product defect plus missing journey smoke gate.

Evidence: `test_journey_team_onboarding` timed out filling
`input[name="display_name"]` because the locator resolved to an element that
was “not visible” (verifier/journey_pytest.json:19-29). This indicates duplicate
or incorrectly hidden auth markup in the SPA: the official test's form-scoped
field is present in the DOM but not visible. The ad hoc judge flow passed auth,
but the official journey still scored 0/1 (verifier/journey_pytest.json:7-28).

Improvement: ensure only the active auth form is mounted or visible; use one
form with a mode switch rather than duplicate hidden fields. Add the exact
official journey as a required smoke test, not only focused unit tests or a
handwritten Playwright script.

### P1 — Agent verification was too narrow to support “done” for the user journey

Type: agent behavior failure and process failure.

Evidence: M16 verified auth/workspace and explicitly noted the thread panel was
hidden (digests/task-ledger.md:85-87). M18's smoke covered settings operations,
not chat (digest:823-825). The final judge found the backend fully built but the
frontend never consumed it (judge/driver-report.txt:3-5, 34). The worker's
“Full pytest: 201 passed” claim was true for its selected suite, but it was not
evidence that the core UX was complete.

Improvement: define frontend DoD in the prompt as externally observable
capabilities, not only local tests: create channel, select channel, see empty
state, post, reload, edit/delete, open thread, react. Require the agent to name
each result in task notes and fail delivery if any selector is absent.

### P2 — Repeated restart and port contention consumed implementation time

Type: harness/process failure, with some avoidable agent cleanup churn.

Evidence: the daemon repeatedly waited 30 seconds for frontend-dev-1
(daemon-filtered.log:452,469,503 and many later entries). M18's notes report
port contention for the second live pass (digest:799), then the agent spent its
last minutes killing respawning `start.sh`, uvicorn, and Redis processes
(digest:1073-1115). The watchdog killed the session at 01:14:51 and reset M18
(daemon-filtered.log:5218-5231).

Improvement: provision an isolated dynamic-port cluster per worktree, make the
supervisor stop a task's process tree on completion, and make the watchdog
distinguish active output/commands from silence. Give the agent a bounded
cleanup helper instead of repeated `pkill` attempts. Reserve the final 20
minutes for the journey gate and prevent new architecture claims after it.

### P2 — Fallback claiming was not deadline-aware

Type: harness/process failure.

Evidence: frontend-dev-1 yielded M5 and M8 to “better-fit idle peer” entries
(daemon-filtered.log:1408-1414, 4421-4423), while M17 later fell to an
architect through `fallback: no skill match` (daemon-filtered.log:5207). The
generic fallback policy optimized local skill matching, not final-score impact.

Improvement: add a critical-path score to claims: unimplemented frontend
journey tasks outrank backend polish/settings; fallback may move work only when
the receiving role is eligible and the sender's critical-path task is either
complete or explicitly protected. Log the rejected alternatives and the reason.

## UX and journey criterion map

| Criterion / step | Evidence in final app | Owning task | Result and fix |
|---|---|---|---|
| Channels: list/create/switch | List only at `index.html:141-146`; no create control | M17 | FAIL; add modal, POST, row selection |
| Messaging: list/post/edit/delete/persist | Pane only at `index.html:149-162`; no fetch in `shell.js:150-176` | M17 | FAIL; ship chat UI and reload test |
| Threads | Hidden placeholder at `index.html:163-170` | M17 | FAIL; message action opens populated aside |
| Reactions | No picker or static JS calls; judge report:19-20 | M17 | FAIL; emoji grid and reaction REST calls |
| Validation feedback | Auth focus/errors work; chat/channel forms absent | M17 | FAIL; empty composer and duplicate-name tests |
| Polish / empty state | Loading/hover exist; selected empty channel is blank | M17 | PARTIAL; friendly empty copy and composer |
| Realism | Sidebar/workspace/#general exist; no topic/message rows/composer | M16 + M17 | PARTIAL/FAIL; render topic and chat content |
| Layout/identity | Two-pane shell and reserved thread aside | M16 | PASS; retain while extending chat |
| Official onboarding journey | Hidden `display_name` field times out | M16 product + gate | FAIL; single visible auth form and run official test |

## Recommended execution order

1. Keep M16 as the shell foundation, but make its official onboarding journey
   pass before opening downstream frontend tasks.
2. Claim and implement M17 immediately after M16. Allow backend and settings
   work in parallel only on separate agents; do not let them displace M17.
3. Run the journey smoke after the first vertical slice: register, create
   workspace, create channel, select channel, post “hello”, reload, open thread,
   add reaction. This catches the missing surface in minutes.
4. Finish M17's edit/delete, validation, and empty states; then run the full
   official journey and UX selector smoke on all three nodes.
5. Merge M18 only after M17's selectors and `shell.js` integration are present.
   Its settings controls can coexist through the specified channel-settings
   event, but M18 is not a substitute for chat.
6. At finalization, block completion on: no open critical frontend task, an
   integrated M17 implementation commit, journey pass, and a report of actual
   browser observations. A design-only task must never count as delivered.

## Bottom line

The agents did produce a coherent M16 shell and M18 settings surface, and the
judge confirmed auth, workspace creation, settings, layout, hover, and loading
behavior. But the final artifact lacked the product's central chat loop because
the scheduler left M17 without an eligible implementation claim. The strongest
testable change is therefore a combined routing invariant plus journey DoD:
M17 must be assigned to a frontend implementer, and the run cannot finish until
the browser can create, select, post, thread, and react.
