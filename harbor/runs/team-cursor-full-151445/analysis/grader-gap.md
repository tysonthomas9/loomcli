# Huddle autonomous-run review: the grader gap

## Executive result

The run scored **0.1875**: correctness `0` (`gates_passed: 0/5`) and UX `0.375`.
The dominant cause was integration priority. The backend message API exists, but the
frontend chat task was left at review; the final SPA only renders a shell. The second
dominant cause was scheduling: the run spent the last 26 minutes designing/claiming
MARATHON-11/13 instead of shipping the already-designed, high-score frontend and
IRC work. The final artifact was healthy, so this was not primarily a startup failure.

Evidence basis: `README.md` (outcome/timeline); `verifier/metrics.json:2-26`;
`judge/ux.json`; `judge/driver-report.txt`; `app-git-log.txt`.

## Score-backward matrix

| Gate/test/criterion | Cause and classification | File:line | Task / agent / session | Fix effort | Score impact |
|---|---|---|---|---:|---:|
| API gate; 53 primary pytest failures | Missing M11 search, slash, mentions/read-state; missing M10 files; M13 WS incomplete. Mostly **never attempted**. | `app/server/app.py:48-55` has no search/WS/files routers; `messages/store.py:35-36,184-215` only resolves `@username`. | M11 architect session 15; M13 architect session 14 then backend session 11; M10 architect session 17. | 2–5h | One of five gates; 53/129 tests. |
| `test_search_returns_matching_messages` (404) | `/api/search` was designed but never implemented/mounted. **Never attempted.** | `app/server/app.py:48-55` (no search router). | M11, architect session 15. | 0.5–1h | API gate subset; also search feature. |
| `test_author_self_mention...`, cursor/read tests; workspace read-state empty | No `GET/POST /channels/{id}/read`, no unread bump, no `@channel/@here/@group`. **Never attempted.** | `app/server/channels/routes.py:500-556` ends at pins; `messages/store.py:184-215`. | M11, architect session 15. | 1–2h | Several API tests and badge contract. |
| Slash tests: `/me`, `/shrug`, `/topic`, `/invite`, archive, unknown | POST route stores slash text as ordinary messages; no parser/side-effect dispatch. **Never attempted.** | `app/server/channels/routes.py:103-150` and `500-500`; no slash module in artifact. | M11, architect session 15. | 1–2h | Multiple API failures. |
| `test_file_*` (all 6; 404) | File upload/metadata/download package absent; M8 explicitly leaves `files` empty until M10. **Never attempted.** | `app/server/messages/store.py:228-254`; no `app/server/files/`; `app-git-log.txt` has no M10 commit. | M10, architect session 17. | 1–2h | Six API tests; attachments. |
| `test_websocket_*`, cluster ordering/load | WS event implementation was not integrated; `events.py` remains no-op hooks. **Attempted but wrong/incomplete.** | `app/server/events.py:1-87` says “No-op until MARATHON-13/14”; `app/server/app.py:48-55` has no WS router. | M13, backend session 11 began at `01:32:15`, too late. | 2–4h | API/ordering/cluster failures; API gate. |
| IRC gate 0/11; `ConnectionRefusedError` | No IRC listener on :6667. **Never attempted.** | `app/start.sh:40` reserves only `8000,8001,8002,6379`; no IRC server/module. | M15 remained open in `digests/task-ledger.md:18`; no implementation session. | 2–4h | Entire IRC gate. |
| Crash gate 1/3; node :8001 did not return | Supervisor is present, but the tested respawn path was not reliable under the final artifact/runtime; cross-node fanout also absent. **Integrated then broken / incomplete.** | `app/start.sh:192-212` has respawn loop; `verifier/crash_pytest.json` reports “node on :8001 did not come back up”. | M2 QA sessions 1–2 verified a prior head; final integration changed behavior without final crash gate. | 0.5–1.5h | Entire crash gate. |
| Chaos gate 1/3; Redis-down fanout and dense seq | Redis outage fallback and durable sequence work were not integrated. **Never attempted/incomplete.** | `app/server/events.py:23-87` no-op; no `server/ws/`; no M14 commit. | M14 open; M13 backend session 11 started only at `01:32`. | 2–4h | Entire chaos gate. |
| Journey 0/1 | UI registration contract mismatch: journey fills visible `input[name=display_name]`, but the chosen auth form keeps it hidden. **Integrated then broken / contract mismatch.** | `verifier/journey_pytest.json` trace at `/tests/journey_onboarding.py:98`; inspect `app/static/index.html` auth form and `auth_ui.js`. | M16 frontend session 1; no journey rerun after M18 integration. | 0.25–0.5h | Entire journey gate. |
| UX channels FAIL | No create-channel affordance; only #general is rendered. **Never attempted.** | `app/static/index.html:143-145`; `app/static/js/shell.js:178-197`; no create-channel control in static. | M17, architect session 16 designed it; frontend never claimed M17. | 0.5–1h | Criterion 1.0. |
| UX messaging FAIL | No message list, fetch, composer, or mutation UI. **Never attempted.** | `app/static/index.html:149-161`; `app/static/js/shell.js:150-175` only sets title. | M17, architect session 16; frontend sessions 2–4 stayed on M18. | 1–2h | Criterion 1.0 and blocks other UX criteria. |
| UX threads FAIL | Thread aside is placeholder and never opened. **Never attempted.** | `app/static/index.html:164-170`; `shell.js:45,150-175` has no message/thread wiring. | M17, frontend not assigned. | 0.5–1h | Criterion 0.75. |
| UX reactions FAIL | Backend endpoints exist, but no toolbar/picker/client calls. **Never attempted.** | `app/server/messages/routes.py:236-313` exists; no reaction markup in `index.html:149-171`, no chat JS. | M17, frontend not assigned. | 0.5–1h | Criterion 0.75. |
| UX validation FAIL | Auth validation passes, but empty-message and duplicate-channel checks are impossible without M17 controls. **Never attempted.** | `judge/driver-report.txt`; missing controls above. | M17 not scheduled. | Included above | Criterion 0.5. |
| UX polish/realism PARTIAL | Existing shell polish passes only hover/loading; blank pane lacks empty state, topic, rows, composer. **Never attempted** in chat layer. | `index.html:149-170`; `shell.js:170-175`; `app.css:369` supplies the one passing hover. | M16/M18 shipped shell; M17 absent. | Included above | 0.5 each; realism and polish partial. |
| `test_message_pagination_newest_first`, pin ordering | Ordering contract is wrong (oldest-first result). **Attempted but wrong.** | `app/server/channels/routes.py:460-500` list path; `channels/store.py` list query/order. | M8 backend session 8; no verifier rerun with grader suite. | 0.25h | API failures and ordering score. |
| `test_dm_is_stable_and_bidirectional` (400) | M9 contract requires `workspace_id`; grader sends the older/expected body shape. **Attempted but wrong contract.** | `app/server/dms/routes.py:30-52`; design note in task ledger says workspace is mandatory. | M9 backend session 9. | 0.25–0.5h | API failures. |
| Profile defaults/length tests | Registration writes `timezone=NULL`; final profile validation does not match grader’s max-length expectations. **Attempted but wrong.** | `app/server/auth/users.py:108-127`; `users.py:160-171`. | M3/M4 backend sessions 2–4; QA claimed “38 passed” before final grader. | 0.25–0.5h | API failures. |
| Auth/health/layout UX passes | These are successful baseline capabilities, not causes. | `metrics.json:2-26`; `judge/verdicts.json` auth/layout. | M2–M6, M16, M18. | — | Preserved 2 UX criteria and health. |

## Agent behaviour failures

1. **Wrong priority at the score bottleneck (high impact).** The architect produced a
complete M17 design at `digests/app-architect-1.md:4723` onward, including selectors and
REST seams, but the frontend continued M18. The ledger leaves `MARATHON-17` in review
(`digests/task-ledger.md:20`) while M18 is closed (`:21`). Improvement: add a lead rule:
“After shell/settings, claim the highest-weight unimplemented grader surface; do not
start another design-only task while a UI/API gate is unimplemented.”

2. **Late design work displaced implementation.** Architect session 14/15 begins around
`01:01:43–01:14:11`; backend M13 only starts at `01:32:15`, near the `01:35` deadline.
Improvement: reserve the final 45 minutes for integration and verifier reruns; prohibit
new designs after 70% of budget unless they unlock a currently failing gate.

3. **False completion signals were not score-backed.** Backend M18 reported “220 tests
passed” and “DMs/messages remain intact” (`digests/backend-dev-1.md:4163-4164,4259-4261`),
but the official artifact later had 53 API failures and journey failure. QA similarly
reported “all 21 tests passed” (`digests/qa-engineer-1.md:190`) and “25 passed” (`:425`)
on narrower suites. Improvement: “done” must include the official verifier command,
final integrated commit SHA, and a changed-since-baseline diff; local task tests are
evidence of task correctness only.

4. **Verification was too local and too optimistic.** M8 explicitly recorded 151 tests
excluding live/cluster/port-bound tests (`digests/backend-dev-1.md:4344`), yet the run
never used that exclusion as a risk flag. Improvement: every task with `cluster`, `WS`,
`IRC`, or `frontend` labels must run one real-path smoke test before delivery.

5. **Wasted turns and destructive runtime cleanup.** Frontend session 3 contains repeated
`pkill`/port clearing attempts (`digests/frontend-dev-1.md:1073-1121`) rather than feature
work. Improvement: harness supplies isolated ports/data dirs per agent, and prompt says
never spend >5 minutes repairing shared runtime; report blocked and yield.

## Harness / prompt / process failures

1. **Role/task assignment did not follow score dependency.** At `01:32:02` the daemon
explicitly yielded M13/M11 from frontend to backend, then assigned frontend M11 as a
“fallback: no skill match” (`daemon-filtered.log:5755-5778`). This put a frontend agent on
backend work while the M17 frontend gate was untouched. Fix: score-aware assignment must
reserve frontend capacity for M17 and assign M11/M13 only to backend-capable workers.

2. **No final-gate barrier.** The lead could finalize with M11/M13/M15 open and M17 in
review. The ledger proves this (`digests/task-ledger.md:13-20`). Fix: at finalize, block
integration unless all five gate owners are either green or explicitly marked blocked;
the lead must run metrics and choose the highest expected score/hour fix.

3. **Critic/integration checked task-local quality, not grader coverage.** The process
accepted M16/M18 shell work while no check enforced that the message pane had a composer,
channel creation, or message fetch. Fix: add a static preflight that fails if M17 selectors
(`message-list`, `message-composer`, `create-channel-modal`, `emoji-picker`) are absent.

4. **Prompt omitted a shared “official contract first” loop.** Agents repeatedly relied on
their task designs and narrow suites. Fix prompt wording: “Before coding, enumerate the
official tests/UX criteria your task flips; after coding run those exact tests against the
integrated branch and paste failures, not only passing local tests.”

5. **Yield/claim rules rewarded idle fallback instead of impact.** The daemon’s own log
shows fallback assignment, while open M15 and unclaimed M17 remained. Fix scoring formula:
`expected_gate_points / estimated_minutes`, with unimplemented high-weight UX and IRC
tasks receiving priority bonuses; never auto-claim a different domain on fallback.

## Product defects in `app/`

The most important product defect is the empty chat shell. `index.html:149-161` contains
only a header and “Select a channel”; `shell.js:150-175` sets a title and hides the
placeholder, but does not call history or render rows. This single defect explains
messaging, threads, reactions, validation feedback, polish, and three realism cues.

The backend also has clear missing contracts: no search/files/WS routers in
`server/app.py:48-55`, no slash parser, username-only mentions in
`messages/store.py:35-36,184-215`, and no IRC process in `start.sh:40`. These are product
defects, not merely judge limitations: the official API tests receive 404/201-literal
slash responses, and IRC receives `ConnectionRefusedError`.

## Minimal high-leverage fix set

1. **Ship M17 first (1–2h):** implement `chat_ui.js`, wire it from `app.js`, add channel
create, `GET/POST/PATCH/DELETE` messages, empty state, edit/delete, thread panel, and
emoji picker. This flips messaging, channels, threads, reactions, validation, and most
polish/realism at once; it also makes the journey able to proceed past onboarding.
2. **Add IRC listener (2–3h):** implement the smallest :6667 registration/PING/JOIN/PRIVMSG
bridge and start it from `start.sh`; this alone can flip the whole 11-test gate if kept
contract-focused.
3. **Fix M11 essentials (1–2h):** mount search, slash dispatch, mentions, and read-state
routes; these are discrete and directly recover many API tests.
4. **Run the official verifier on the final integrated commit, then fix startup/respawn
and ordering regressions (0.5–1h).** Do not trust task-local counts.

## What a 3h20m run could realistically have reached

With the same implementation capacity, a realistic ordering was: M17 from ~23:36 onward,
then a 10-minute integrated smoke test, then either M11 essentials or IRC, then the final
verifier. M17 alone likely raises UX from 0.375 to near full and makes journey possible;
M11 essentials could flip the API gate only if the remaining 53 failures were reduced below
the gate threshold. IRC was a clean independent target but likely consumes most remaining
time. M13/M14 chaos/cluster resilience was not realistic after `01:32`; it needed an
earlier backend slot and cross-node verification. M10 files and profile/order cleanup were
small opportunistic fixes, but should follow the high-weight UI/IRC/API surfaces.

Bottom line: the score was primarily lost by leaving an already-specified, high-leverage
frontend task unclaimed and by allowing finalize without official-gate evidence—not by a
single subtle implementation bug.

## Evidence quick index

- `verifier/metrics.json:3-26`: health is true, but all five gates are false and reward is zero.
- `verifier/pytest.json` summary: `129` tests, `76` passed, `53` failed.
- `verifier/irc_pytest.json` summary: `0/11`; the first failure is a socket refusal at `test_irc.py:167`.
- `verifier/crash_pytest.json`: `1/3`; one failure explicitly says node `:8001` did not return.
- `verifier/chaos_pytest.json`: `1/3`; Redis-down fan-out and dense-seq cases fail.
- `verifier/journey_pytest.json`: Playwright times out filling the hidden display-name input.
- `judge/driver-report.txt`: `textarea` count is `0` on `18000/18001/18002`; the pane is blank.
- `judge/verdicts.json`: auth/layout pass; channels, messaging, threads, reactions, validation fail;
  polish and realism are partial.
- `app-git-log.txt`: final history stops at M18; there is no M10, M11, M13, M14, M15, or M17 implementation commit.

Classification convention used here: “never attempted” means no implementation is present in
the final artifact; “attempted but wrong” means an endpoint/UI exists but violates the grader
contract; “integrated then broken” means earlier task evidence passed but the final integrated
artifact or runtime no longer satisfies it. Effort estimates are engineering time, not elapsed
wall-clock time, and assume the existing backend and test fixtures remain available.
