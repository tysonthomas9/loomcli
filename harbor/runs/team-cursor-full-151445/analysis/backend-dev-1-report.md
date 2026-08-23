# Backend-dev-1 evidence review

## Executive summary

The run integrated ten of the fifteen backend assignments, but the final artifact scored
0.1875: correctness reward 0, all five correctness gates failed, 76/129 API tests passed,
0/11 IRC tests passed, 1/3 crash tests passed, 1/3 chaos tests passed, and the journey
failed. The headline explanation is not one isolated regression. The team declared
features complete using local/unit suites while the official contract suite was either
not run, not available to the worker, or knowingly skipped. The final branch also ended
with unfinished WebSocket work (MARATHON-13) and no IRC gateway.

The most important process correction is a hard definition of done: a backend task is not
reviewable until the exact relevant tests from `spec/tests/` (or an equivalent copied
contract test) run against a clean foreground `start.sh` cluster on ports 8000-8002,
with the result and any skipped tests recorded. “Full suite green” means the official
contract suite, not only the agent-authored `tests/` directory.

## Final score and causal evidence

1. **Critical — the artifact was marked done while the official contract suite had 53
   failures.** `verifier/test-stdout.txt:13-19` says the API gate collected 129 tests;
   `:821-822` reports “53 failed, 76 passed” and “api gate exit=1”. This is a product
   failure enabled by agent verification failure and critic/process weakness. Improvement:
   make the integration gate execute `pytest /tests` against the candidate artifact and
   refuse `review`/integration on a nonzero exit; require the exact failing-test list in
   the task note.

2. **Critical — IRC was absent, not merely flaky.** `verifier/test-stdout.txt:823-831`
   says the gateway never opened on `:6667`, and `:845-852` shows the first test failing
   with `ConnectionRefusedError`. `app-git-log.txt:1-26` contains no IRC commit. The
   backend task sequence reached MARATHON-13 but never reached an IRC implementation.
   Improvement: make `start.sh` readiness require `6667` and add a scheduler invariant
   that reserves an owner for every P0/P1 capability (IRC, files, read state, search,
   WS) before accepting lower-value follow-up work.

3. **Critical — the final WebSocket/sequence work was started after the deadline and was
   not delivered.** The last digest ends at `digests/backend-dev-1.md:4402-4418` after
   edits to `server/db.py`, `server/ws/hub.py`, `server/events.py`, `server/ws/routes.py`,
   and `server/app.py`; there is no pytest, commit, delivery, or integration record after
   that point. The official failures are `test_cluster.py` and `test_ordering.py` at
   `verifier/test-stdout.txt:813-820`, including cross-node broadcast, resume, dense seq,
   replay, and load. Improvement: schedule the end-to-end seam before polish; a task that
   touches the event contract must include a cross-node test before any “delivered” claim.

4. **High — the SPA was accepted despite being visibly incomplete.** README states the
   final SPA had “no channel-create control, no message list, no composer” (README.md:4-7),
   while the only frontend commit in the integrated history is `f4cf481` for auth/token/
   workspace shell (`app-git-log.txt:11`). The UX judge consequently failed channels,
   messaging, threads, and reactions. Improvement: require every pinned selector in
   `spec/instruction.md:110-117` to be exercised by a smoke script before frontend review;
   do not treat auth plus layout as feature completion.

5. **High — the harness allowed “full suite” to mean a private, weaker suite.** For
   example, session 6 reports “All 175 tests passed” (`digests/backend-dev-1.md:2014-2037`),
   session 8 reports 151 tests while explicitly excluding live/cluster tests
   (`digests/backend-dev-1.md:3587-3590`), and session 10 reports “All 220 tests passed”
   (`digests/backend-dev-1.md:4155-4244`). None of these are the verifier’s 129 tests.
   Improvement: inject the official test path and a single command such as
   `pytest /tests -q`; prohibit ambiguous phrases such as “full suite” unless the path,
   count, exit code, and skipped tests are printed.

## Per-session review

| Session/time | Task and work | Claimed verification | Spec-contract check and assessment |
|---|---|---|---|
| 1, 22:23:19-22:28:25Z | MARATHON-2; `start.sh`, FastAPI nodes, Redis, SQLite bootstrap. | `.venv/bin/pytest -q tests/` passed 6; live boot/respawn was rerun. Digest: `:161-302`. | Useful scaffold, but the live run left orphaned processes and Redis respawn churn (`:223-262`). The agent delivered after a narrowed local test, not the official health/IRC contract. |
| 2, 22:37:40-22:41:03Z | MARATHON-3; auth migration and register/login/me. | Local auth suite passed 33; cluster smoke followed (`:626-650`). | Auth was one of the few areas that passed official tests (`test-stdout.txt:21-27`). The failure was process-level: no official contract run before delivery. |
| 3, 22:54:03-22:57:28Z | MARATHON-4; profiles and `user.updated` hook. | “Full suite is green,” then health curl (`:998-1059`, final `:1161`). | Official failures hit `server/auth/users.py`/`server/users/routes.py` behavior: timezone defaults `None`, overlong fields accepted, and profile broadcast failed (`test-stdout.txt:49-57`, `:213-220`). This is a false confidence finding: the agent’s tests omitted the grader’s exact defaults/limits/event assertions. |
| 4, 23:00:14-23:03:21Z | MARATHON-5; workspaces, channels, `#general`. | Local suite green; cluster boot initially failed due Redis, then was rerun (`:1330-1520`, final `:1559`). | Official tests show routes existed, but dependent behavior was incomplete: public join/detail and pagination/read access failures (`test-stdout.txt:32-40`). The agent classified a boot failure as environmental rather than preserving a clean reproducible gate. |
| 5, 23:35:49-23:40:12Z | MARATHON-6; members, roles, ownership transfer. | “Full suite 152 passed”; live boundary assertion had shell quoting failure but was treated as non-blocking (`:1707-1796`). | Role behavior mostly passed, but `test_admin_cannot_promote_to_admin` failed (`test-stdout.txt:124-131`). The specific contract edge was not verified after the assertion script failed. |
| 6, 00:02:34-00:13:07Z | MARATHON-7; channel lifecycle, join/leave, pins, messages/routes. | 17 targeted, then 175 full local tests; migration check and boundary smoke (`:1988-2116`). | The agent edited its own tests to match code and manually controlled timestamps when pin ordering failed (`:1980-2005`). Official failures remained in `server/channels/routes.py`/store: join status, idempotent leave, pin ordering/authz (`test-stdout.txt:73-77`). This is contract drift and test adaptation rather than contract verification. |
| 7, 00:14:30-00:28:53Z | MARATHON-12; groups and invitations. | “Tests passed,” schema audit and full local suite (`:2413-2726`). | Official failures were delete idempotency, all advanced mention expansion, invitation auth/list/revoke, and role edge (`test-stdout.txt:103-114`, `:121-127`). The agent repeatedly observed “actual file contents differ” and rewrote tests (`:2418-2425`) but still delivered without the spec’s `@group`, `@channel`, `@here`, and forbidden-status checks. |
| 8, 00:41:06-00:58:38Z | MARATHON-8; messages, threads, reactions. | 151 tests excluding live/cluster/port-bound tests; explicitly says live cluster pytest “left unrun” (`:3587-3590`). | This is the clearest skipped-verification admission. Official pagination, non-member read, search, mention/read-state, files, slash, and WS failures remained (`test-stdout.txt:35-41`, `:62-96`). The feature’s unit CRUD tests were insufficient for the cross-feature contract. |
| 9, 00:59:04-01:02:48Z | MARATHON-9; DM get-or-create and membership authz. | 12 targeted, then 204 local tests and a same-ID live smoke (`:3816-3910`). | Official DM stability failed with HTTP 400 (`test-stdout.txt:39`, `:182-184`). The smoke checked only the happy path, not the official request body/response contract or repeated cross-user cases. |
| 10, 01:15:05-01:17:00Z | MARATHON-18 retry; cherry-pick SPA/channel settings/add-member while preserving M8/M9. | Full local suite 220 plus add-member boundary; delivered attempt 2 (`:4155-4244`). | The critic correctly rejected attempt 1 for deleting DMs/messages, but the accepted attempt still did not run the official browser rubric. The final UX showed the broader SPA was missing message/channel controls (README.md:4-7). Improvement: critic must run the smoke/UX contract, not only Python tests. |
| 11, 01:32:15Z onward | MARATHON-13; dense sequence, WS subscribe/resume. | No test or delivery appears after edits. | Harness scheduling allowed a large P0 seam to start at 01:32, three minutes before finalize at 01:35 (`README.md:16-17`; `integration.log:26-28`). It could not be completed or verified. |

## Mapping the 53 API failures to code and session

The official output names the failing test and assertion; the integrated commit history
identifies the last task that owned the relevant code (`app-git-log.txt:1-26`). The mapping
below distinguishes an implemented defect from an unimplemented capability.

### Implemented-code defects

| Failures | Responsible app area | Session | Evidence and testable fix |
|---|---|---|---|
| Join/detail/non-member read; leave; pins (6) | `server/channels/routes.py`, `server/channels/store.py`, `server/workspaces/routes.py` | M5/MARATHON-7, sessions 4/6 | 404/200/403 mismatches and pin order are recorded at `test-stdout.txt:152-189`, `:270-290`. Add exact tests for public auto-onboarding, private omission, member authz, stable insertion sequence, and idempotent leave. |
| Pagination (1) | `server/messages/store.py` | session 8 / M8 | `test-stdout.txt:162-176` shows UUID ordering instead of newest-first. Persist/order by `created_at` plus deterministic id tie-breaker; test three same-second writes. |
| DM (1) | `server/dms/routes.py`, `server/dms/store.py` | session 9 / M9 | `test-stdout.txt:180-184`. Add a spec-shaped `{user_id}` POST test on both users and repeat it after a fresh process. |
| Profiles/broadcast (4) | `server/auth/users.py`, `server/users/routes.py`, `server/events.py` | session 3 / M4 | `test-stdout.txt:213-250`. Set persisted defaults (`UTC`, empty strings/nulls as contract dictates), enforce max lengths at the boundary, and assert `user.updated` on a real WebSocket. |
| Read state and self mention (4) | `server/messages/store.py`, `server/workspaces/routes.py`, read-state route area | session 8/M8 and unfinished M11 | `test-stdout.txt:254-270`, `:406-425`. Implement cursor persistence/monotonic update and return `read_state` in workspace detail; exclude author from all mention counters. |
| Groups and invitations/roles (11) | `server/groups/routes.py`, `server/groups/store.py`, `server/invitations/routes.py`, `server/workspaces/routes.py` | session 7 / M12, plus M11 mention work | `test-stdout.txt:802-812`. Return 200 for idempotent delete/revoke, enforce 403 for non-owner/admin cases, and expand group/channel/here mentions with online membership and dedupe. |
| Slash commands (9) | message command dispatch in `server/messages/routes.py` or equivalent | session 8/M8; no dedicated command task integrated | `test-stdout.txt:793-801`, including `/shrug`, side-effect `message:null`, unknown command 400, and owner authz. Add a table-driven contract test for every command in `spec/instruction.md:83-89`. |

### Unimplemented or missing-code defects

| Failures | Missing app area | Session/ownership | Evidence and fix |
|---|---|---|---|
| Files (7) | No file service/router is present in `app-git-log.txt:1-26`; expected `/api/files` | No backend session integrated it; M10 was not delivered | `test-stdout.txt:786-792` shows 404 for upload and attach tests. Add `server/files/routes.py` and store, register it in `server/app.py`, then run size, ownership, duplicate-attach, metadata, and byte-roundtrip tests. |
| WebSocket broadcast/ordering/load (9) | `server/ws/*` and event wiring were only edited in unfinished session 11 | M13 started too late; M11 was auto-approved but not integrated (`integration.log:26-28`) | `test-stdout.txt:813-820` and `:190-212`. Implement durable SQLite seq allocation in the same transaction as each mutation, subscribe/resume, and cross-node fanout; run all cluster/ordering/load tests before review. |
| Search (1) | No integrated search endpoint visible in history | No delivered session | `test-stdout.txt:185-189` gives 404. Add `server/search/routes.py`, accessible-channel filtering, optional workspace filter, and tests for inaccessible matches. |

### IRC and resilience gates

IRC’s 11 failures are all explained by the absent listener, not by eleven independent
protocol bugs: `test-stdout.txt:823-852` records the readiness timeout and connection
refusal. Crash/chaos evidence in `verifier/metrics.json:5-29` is only 1/3 passed in each
category; the scaffold’s own session-1 transcript already showed orphaned `start.sh`,
uvicorn, and Redis processes (`digests/backend-dev-1.md:223-262`). Fix the supervisor with
process-group ownership, deterministic PID files, and a readiness/kill/recovery test that
checks the actual endpoint after SIGKILL, not only that a process respawned.

## Agent behaviour failures

1. **Skipped the decisive tests.** Session 8 explicitly left live/cluster pytest unrun
   (`digests/backend-dev-1.md:3587-3590`); session 6 treated a shell-quoting failure as
   non-blocking (`:1804-1816`); session 1 inferred success from a truncated exit-0
   integration output (`:223-237`). Improvement: a failed assertion, truncated output, or
   excluded test is a blocker, not a green result.

2. **Adapted tests to implementation.** Session 6 rewrote tests to current imports/API
   shapes (`:1980-1987`) and session 7 repeatedly noted that the current files differed
   from what it expected (`:2418-2425`). Improvement: never rewrite a contract test to
   make it fit code; first compare against `spec/instruction.md` and preserve the test’s
   required status/envelope/field names.

3. **False completion language.** Sessions 4, 6, 7, 8, 9, and 10 all used “full suite is
   green” or “all ... passed,” while the official suite later failed 53 cases. Evidence:
   `digests/backend-dev-1.md:1002`, `:1711`, `:2014`, `:3587-3590`, `:3828`, and
   `:4155`. Improvement: completion text must include command, test root, count, exit code,
   and skipped list; otherwise say “local unit tests only.”

4. **Spent substantial turns on tooling/process cleanup.** Session 1 spent from 22:26:07
   through 22:27:01 debugging HEAD-vs-GET, orphaned processes, `pkill`, and stale ports
   (`:192-262`). Session 4 spent turns on Redis/port-lock startup (`:1019-1118`), and
   session 8 spent 00:54:45-00:57:44 diagnosing a port-held test run (`:3489-3537`).
   Improvement: provide a harness-owned isolated port range or namespace, a `start.sh
   --check`/cleanup command, and never make agents manually kill shared processes.

## Harness, prompt, and process failures

1. **Role prompt had the right words but no enforceable gate.** The backend prompt says the
   design is “verbatim law,” every behavior needs a test, and “handle every failure path”
   (`prompts/team-backend-dev-override.md:1-20`), but it does not name the official test
   directory, require a clean-cluster run, or forbid completion after exclusions. Add a
   mandatory Step 4 command with the exact grader tests and a machine-readable completion
   record.

2. **No capability coverage ledger.** `integration.log:3-25` shows sequential integration
   through M18, while M13 was only auto-approved at `:26` and the run stopped at `:28`.
   The lead should maintain a required-capability matrix derived from the spec and refuse
   scheduling lower-priority polish while IRC/files/WS/read state/search remain unowned.

3. **Automatic design approval weakened review.** Multiple tasks show
   `DESIGN-AUTO-APPROVED ... waited_passes=2` (`integration.log:11-21`, `:26-27`). This is
   especially damaging for interdependent event contracts: auto-approval allowed M13 and
   M11 to start too late and gave no integrated owner for the cross-node seam. Improvement:
   auto-approval may unblock implementation, but not integration; require a critic or
   official contract gate before marking review.

4. **Critic scope was too local.** M18 attempt 1 was rejected for deleting DM/messages,
   then attempt 2 was accepted after 220 local tests (`integration.log:24-25`; digest
   `:4155-4244`). That catches regression in the candidate’s own tests but not missing SPA
   controls or official API/IRC/UX behavior. Improvement: critic must run the same staged
   verifier used for scoring and inspect the browser rubric for frontend tasks.

5. **Scheduling did not protect the deadline-critical seam.** The last backend worker
   began M13 at 01:32:15, and finalize stopped at 01:35:10 (`digests/backend-dev-1.md:4294`,
   `integration.log:26-28`). Improvement: reserve the final 25-30 minutes for integrated
   contract verification and freeze new feature claims once that window begins.

## Proposed backend-dev role contract

Replace the current soft completion language with these testable rules:

1. Read `spec/instruction.md` and enumerate every endpoint/event/status/field touched by
   the task. Copy the relevant official tests or cite their nodeids before editing.
2. Implement one behavior, then run its contract tests against a clean temporary SQLite
   database. Do not modify those tests to match implementation.
3. Before delivery, run: `bash /app/start.sh` in the foreground; wait for HTTP 8000/8001/
   8002, Redis 6379, and IRC 6667; then `pytest /tests -q` plus the task’s local tests.
4. The completion note must print `command`, `root`, `passed`, `failed`, `skipped`, and
   `exit_code`. “Passed” is forbidden if any test was excluded, truncated, or failed.
5. For distributed behavior, prove write-on-node0/read-on-node2, cross-node WebSocket
   delivery, reconnect/resume, concurrent dense sequences, Redis SIGKILL recovery, and
   HTTP-node SIGKILL recovery. For UI work, prove the pinned `data-testid` controls and
   one end-to-end journey.
6. Definition of done: all relevant official tests pass, no required service is absent,
   no untracked process remains, and the exact commit is integrated. Otherwise status is
   `blocked` or `needs-revision`, with the first failing command quoted.

## Bottom line

The run’s low score is primarily a verification and scheduling failure that exposed real
product incompleteness. The strongest evidence is the combination of a nonzero official
test exit (`verifier/test-stdout.txt:821-822`), explicit skipped live tests in session 8,
an absent IRC listener (`:823-852`), and unfinished M13 edits at the deadline. Enforcing
the official contract gate and a capability-coverage/deadline rule would have prevented
false completion and redirected time from local test polishing and port cleanup to the
missing grader-visible behavior.
