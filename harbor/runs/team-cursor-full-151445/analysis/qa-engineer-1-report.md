# QA lens report: qa-engineer-1

## Executive conclusion

1. QA performed four genuine local checks, but it did not act as a grader proxy.
   The sessions tested only cluster boot/respawn and auth/token seams; none ran
   `spec/tests/`, the official five-gate suite, or a comparable full matrix.
2. The gap is material, not cosmetic. The final artifact scored 0.1875: correctness
   reward 0, 0/5 gates, 76/129 API tests, 0/11 IRC, crash 1/3, chaos 1/3, and
   journey 0/1 (`README.md`, lines 1-7; `verifier/metrics.json`, lines 3-40).
3. QA therefore missed failures that were directly testable during the run:
   missing IRC, broken WebSocket auth/fan-out, crash recovery, Redis fallback,
   frontend selectors/flows, and the end-to-end journey.
4. The strongest process improvement is to make QA continuously run the official
   spec/tests and maintain an explicit five-gate coverage ledger. A QA task may
   add focused tests, but it cannot report “done” while the grader proxy is red.

## What each verification task actually checked

### MARATHON-19 — cluster boot, Redis, and GET /

5. Session 1 began at `2026-08-22T22:37:23Z` and ran until about 22:39:37
   (`digests/qa-engineer-1.md`, lines 16, 226-245; `daemon-filtered.log`,
   lines 731-776).
6. It read the epic and scaffold task, then created
   `tests/test_cluster_boot.py` (`digests/qa-engineer-1.md`, lines 56-59,
   104-138).
7. The live checks were narrow and concrete: health on ports 8000/8001/8002,
   Redis `PING`, `GET /` HTML, foreground supervisor liveness, no Docker, and
   port 6667 unbound (`digests/qa-engineer-1.md`, lines 104-131, 235-240).
8. It ran `marathon-portlock .venv/bin/pytest tests/ -v`, collected 21, and got
   21 passing; it also deliberately broke the node-id assertion and observed a
   failure (`digests/qa-engineer-1.md`, lines 153-178, 180-190).
9. This is valid evidence for the scaffold contract, but it is not API-gate
   coverage. It did not exercise channels, messages, threads, reactions,
   WebSockets, IRC, frontend selectors, or the official fixtures.
10. The agent said “no defects filed” and “cluster boot matches”
    (`digests/qa-engineer-1.md`, lines 235-240). That claim was appropriately
    scoped to boot, but the task was integrated as a QA pass without recording
    the untested gates.

### MARATHON-20 — SIGKILL respawn

11. Session 2 ran from approximately 22:39:39 to 22:41:54
    (`daemon-filtered.log`, lines 785-843; `digests/qa-engineer-1.md`,
    lines 267-272).
12. It found a stale `start.sh` in a deleted critic checkout with no listeners,
    then proceeded with its own fixture (`digests/qa-engineer-1.md`, lines
    310-329, 341-345). This was useful diagnosis, but it consumed turns on
    environment cleanup rather than integrating official tests.
13. The added `tests/test_cluster_respawn.py` checked: one HTTP node dies and
    returns with the same node id, peer nodes stay healthy, Redis recovers after
    SIGKILL, and 6667 remains unbound (`digests/qa-engineer-1.md`, lines
    377-398, 407-425, 472-478).
14. It ran four focused tests and then the local suite, reporting 25 passed; a
    timeout mutation supplied a red check (`digests/qa-engineer-1.md`, lines
    383-409, 412-424).
15. The test did not verify WebSocket resume/fan-out after a dead node. The
    official crash gate later failed exactly there: only 1/3 passed, with
    “node on :8001 did not come back up” and a WebSocket 403
    (`verifier/crash_pytest.json`, lines 7-15, 19-47).
16. This is the clearest missed defect: QA tested process recovery at the HTTP
    health level, while the grader tests recovery at the product-observable
    event/WS level.

### MARATHON-21 — auth register/login/me envelopes

17. Session 3 started at 22:54:04 and completed at about 22:55:55
    (`digests/qa-engineer-1.md`, lines 505-510, 701-760; `daemon-filtered.log`,
    lines 1239-1268).
18. It ran existing auth unit tests, added a username-with-space case, and added
    `tests/test_auth_live.py` against a live `start.sh` cluster
    (`digests/qa-engineer-1.md`, lines 558-632).
19. The checks covered register 201 shape, duplicate 409, invalid username and
    short password 400, login 200/wrong password 401, `/me` bearer behavior,
    and cross-node token acceptance (`digests/qa-engineer-1.md`, lines 741-766).
20. It reported 38 passed and no defects, after a red mutation of the live
    username assertion (`digests/qa-engineer-1.md`, lines 633-689, 701-766).
21. This was the most relevant QA task to the official API suite, but it still
    covered only auth. It did not run the remaining API tests or inspect the
    failures that later appeared in channel authorization, pagination, DMs,
    search, WebSockets, and profiles (`verifier/pytest.json`, lines 117-149,
    151-226, 292-330).

### MARATHON-22 — cluster-wide bearer acceptance

22. Session 4 ran from approximately 22:55:57 to 22:57:45
    (`daemon-filtered.log`, lines 1277-1327; `digests/qa-engineer-1.md`,
    lines 782-805).
23. It added `tests/test_auth_cluster_token.py` and checked register/login from
    8000, `/me` on 8001/8002, unauthenticated health, and missing/invalid peer
    bearer cases (`digests/qa-engineer-1.md`, lines 824-889, 999-1003).
24. The focused run collected 12 and passed all 12 after a red mutation
    (`digests/qa-engineer-1.md`, lines 890-940, 1001-1003).
25. This substantially duplicates MARATHON-21’s cross-node assertion: session 3
    already reported “token cross :8000→:8001/:8002” (`digests/qa-engineer-1.md`,
    lines 741-766). It added confidence, but low new product coverage.
26. The critic approved it based on the same narrow scope: “Live-cluster tests
    cover register/login token ... health ... peer 401 negatives”
    (`critic/critic-MARATHON-22-1.log`, line 359).

## Did QA run the real grader or five gates?

27. No. Every QA command shown in the digest targets the agent checkout’s
    `tests/` directory, for example `pytest tests/`, `pytest tests/test_auth.py
    tests/test_auth_live.py`, or `pytest tests/test_auth_cluster_token.py`
    (`digests/qa-engineer-1.md`, lines 178, 412, 689, 918-939).
28. The official verifier, by contrast, ran 129 primary tests and got 76 passed
    and 53 failed (`verifier/pytest.json`, lines 7-15; `verifier/metrics.json`,
    lines 32-40).
29. The official five-gate result was: API false, IRC false, crash false, chaos
    false, frontend false; `gates_passed: 0`, `gates_total: 5`
    (`verifier/metrics.json`, lines 3-24).
30. The missing gate-equivalent checks are evidenced directly by the verifier:

    - IRC: 0/11, mostly connection refused on 6667 (`verifier/metrics.json`,
      lines 20-24; `verifier/test-stdout.txt`, lines 825-979).
    - Crash: 1/3, including failed respawn and WS 403 (`verifier/crash_pytest.json`,
      lines 19-47).
    - Chaos: 1/3; writes survived Redis loss, but fallback fan-out and dense
      sequence failed with WS 403 (`verifier/chaos_pytest.json`, lines 19-50).
    - Frontend/E2E: required testids and all four browser tests failed
      (`verifier/test-stdout.txt`, lines 1100-1235).
    - Journey: onboarding failed because the display-name input was not visible
      (`verifier/journey_pytest.json`, lines 17-29).

31. QA’s “red check” practice proved assertion sensitivity, not product coverage.
    A test can go red when its expected status is changed while still omitting
    the entire grader surface. The prompt should have required both properties:
    test sensitivity and gate membership.

## Did QA catch a later grader defect?

32. No defect was filed in any of the four sessions. Session 1 explicitly says
    “No defects were found” (`digests/qa-engineer-1.md`, lines 190-236); session
    2 says “all resilience checks passed; no defects” (lines 469-489); sessions
    3 and 4 record “defects filed: none” (lines 741-766, 999-1003).
33. Consequently, QA caught none of the later grader failures. The strongest
    counterexample is MARATHON-20: its local respawn tests passed, yet the
    official crash gate failed to respawn 8001 and failed WS event setup
    (`verifier/crash_pytest.json`, lines 28-47).
34. Another is the deliberate 6667 blind spot. MARATHON-19/20 treated 6667 being
    unbound as expected (`digests/qa-engineer-1.md`, lines 131, 472-487), while
    the product grader required an IRC gateway and all 11 IRC tests failed
    (`verifier/test-stdout.txt`, lines 825-979). This is both a coverage failure
    and a product defect in `app/`.
35. The API suite also had failures outside QA’s lane: 404 instead of 403/200,
    wrong message pagination ordering, missing search, WS 403, and profile
    defaults/validation (`verifier/pytest.json`, lines 117-149, 151-226,
    292-330). No QA task inspected or filed these.

## Classification of findings

### Agent behaviour failures

36. A1 — Wrong stopping criterion (high impact). Each session stopped after its
    task-local suite and stated “done”/“no defects”; evidence: lines 190-240,
    469-489, 701-766, and 998-1003 of the QA digest. Improvement: prompt the
    agent to run the official suite or explicitly mark the task incomplete when
    any gate is unrepresented; “done” must include a gate coverage table and
    current pass/fail counts.
37. A2 — Duplicate, low-yield work (medium-high). MARATHON-22 repeated the
    cross-node token check already reported in MARATHON-21 (digest lines 741-766
    and 877-889). Improvement: before writing tests, compare prior QA commits and
    select uncovered grader tests; disallow a new QA task whose planned coverage
    is a strict subset of an existing test.
38. A3 — Overconfidence from local green (high). “All resilience checks passed”
    was true only for four hand-written tests, but the task was integrated as a
    QA pass while official crash/chaos/IRC remained untested (digest lines
    469-489; verifier metrics lines 3-24). Improvement: require language such as
    “focused checks pass; grader gates not run” unless the full proxy is green.
39. A4 — Time spent on environment symptoms without escalation (medium). Session
    2 found a stale deleted-worktree supervisor and session 4 found port residue
    (`digests/qa-engineer-1.md`, lines 310-329, 790-803, 903-916). Improvement:
    after one cleanup/retry, record a harness blocker and move to an isolated
    official run rather than repeatedly reusing shared fixed ports.

### Harness/prompt/process failures

40. H1 — The QA prompt defines a focused-task lane, not a grader-proxy lane. It
    says “one task,” “write and run tests against the design,” and “do NOT fix
    application code” (`prompts/team-qa-override.md`, lines 13-22, 48-75), but
    never requires `spec/tests/` or five-gate accounting. Improvement: add a
    mandatory Step 2d: run the official command, record all five gates, and map
    every gate to tests executed; focused tests supplement, never substitute.
41. H2 — Verification tasks were created as independent P1 work while core
    product work remained unfinished. The ledger shows MARATHON-19..22 closed,
    while IRC MARATHON-15 was open, WS/fan-out work was in progress/open, and
    SPA chat MARATHON-17 remained review (`digests/task-ledger.md`, lines 77-104).
    Improvement: schedule QA continuously after each integration, but prioritize
    failing grader gates over new narrow “Verify” tasks; create defect tasks with
    `architect` routing immediately.
42. H3 — The integration gate treated test-only commits as passing candidates.
    All four were integrated with `check=pass` (`integration.log`, lines 5-9),
    even though the tests did not represent the official score. Improvement: the
    gate must run the grader proxy on the post-merge artifact and reject a QA
    candidate that has no recorded gate matrix or has a proxy regression.
43. H4 — No defect feedback loop. The prompt correctly says to file defects
    rather than fix app code (`prompts/team-qa-override.md`, lines 17-20,
    86-93), but nothing required QA to file defects for failures in the broader
    suite. Improvement: auto-create one defect task per failing gate/test cluster
    with reproduction, expected/actual, commit, and owning lane.
44. H5 — Shared fixed-port scheduling caused contamination and wasted turns.
    The QA digest repeatedly shows `marathon-freeports`, stale processes, and
    leftover `redis.conf`/logs (lines 310-329, 430-459, 790-803, 945-981).
    Improvement: provision per-run ports/data directories for the grader proxy, or
    reserve the official port lock for one serialized QA owner and fail fast on
    foreign listeners.
45. H6 — The critic reviewed scope-local success. The MARATHON-22 verdict says
    “APPROVED” solely because 12 focused tests passed (`critic/critic-MARATHON-22-1.log`,
    line 359). Improvement: critic acceptance must require an evidence link to
    the gate matrix, not just the candidate’s own test count.

### Product defects in `app/`

46. P1 — IRC gateway is absent or unreachable: 0/11 and connection refused on
    6667 (`verifier/metrics.json`, lines 20-24; `verifier/test-stdout.txt`, lines
    833-843, 967-979). Code fix: implement/start the IRC listener, authenticate
    against the shared token store, and add bidirectional IRC↔HTTP/WS tests.
47. P2 — WebSocket connections reject valid clients with HTTP 403, breaking
    crash fan-out and chaos fallback (`verifier/crash_pytest.json`, lines 28-47;
    `verifier/chaos_pytest.json`, lines 28-50). Code fix: align `/api/ws` token
    parsing/auth and origin/handshake behavior with the spec; add a real two-node
    subscribe/post/replay test.
48. P3 — Supervisor/node recovery is not reliable: node 8001 did not return in
    the official crash gate (`verifier/crash_pytest.json`, lines 40-47). Code fix:
    make `start.sh` track child PIDs, reap/restart each child deterministically,
    and test repeated SIGKILL with bounded recovery.
49. P4 — Redis outage fallback does not deliver cross-node fan-out or preserve
    dense sequence behavior (`verifier/chaos_pytest.json`, lines 28-50). Code fix:
    implement the documented non-Redis event path and verify writes, fan-out, and
    sequence continuity while Redis is unavailable.
50. P5 — SPA lacks required chat controls/rendering. The official frontend and
    e2e gates failed (`verifier/test-stdout.txt`, lines 1100-1235), and README
    summarizes “no channel-create control, no message list, no composer”
    (`README.md`, lines 1-7). Code fix: ship the MARATHON-17 channel/message/
    thread/reaction DOM and wire it to REST/WS, then run the CUA journey.
51. P6 — API contract defects remain: channel authorization returns 404 instead
    of 403/200, pagination ordering is wrong, search is 404, WS is 403, and
    profile defaults/validation fail (`verifier/pytest.json`, lines 117-149,
    151-226, 292-330). Code fix: repair route registration/lookup semantics,
    order messages by the specified sequence/time key, mount search, fix WS auth,
    and enforce profile defaults/limits.

## Was QA on the critical path or busywork?

52. It was both real work and low-leverage work. The four sessions added 210,
   284, 299, and 255 test lines and were integrated at 22:45:55, 22:47:14,
   23:01:10, and 23:03:22 (`digests/qa-engineer-1.md`, lines 218-223, 451-459,
   731-740, 963-970; `integration.log`, lines 5-9).
53. Those integrations consumed review/integration slots while the official
   product-critical failures remained undiscovered; the run later finalized with
   15 integrations at 01:35:15 (`orchestrate.log`, line 555). There is no evidence
   that QA directly blocked a needed implementation, so “critical path” should
   not be overstated; the evidence supports opportunity cost, not proven elapsed
   time lost.
54. The duplicate auth work especially looks like busywork relative to the score:
   MARATHON-22 added 12 tests for a behavior already covered in MARATHON-21,
   while IRC, WS, crash, chaos, and frontend gates had no QA owner.
55. Recommended staffing rule: one QA owner continuously runs the grader proxy;
   focused QA tasks are pulled only from uncovered/failed gates. A passing focused
   test commit is not a scheduling success if the proxy remains red.

## Proposed operating model: QA as grader proxy

56. At every integrated head, run `spec/tests/` in a clean, serialized fixture and
   publish a ledger with columns: gate, command, tests, passed, failed, blocker,
   owning task, last commit.
57. Require this evidence before closing any QA task:

   | Gate | Required evidence |
   |---|---|
   | API | official API/spec suite result, not only task-local tests |
   | IRC | all 11 tests, including connection and bridge behavior |
   | Crash | node kill, peer service, WS fan-out, resume |
   | Chaos | Redis-down writes, fallback fan-out, dense seq |
   | Frontend/journey | selector suite plus browser journey/CUA result |

58. Add prompt wording: “You are the team’s grader proxy. First run the official
   spec/tests against the integrated head. If any gate fails, reproduce and file
   defects; do not claim QA pass. Then add focused tests only for uncovered
   acceptance criteria. Report focused results and official results separately.”
59. Add a process rule: no `IMPL-DONE` or integration of a QA candidate without a
   current ledger and explicit “not run” entries. “Not run” must remain visible,
   not collapse into “pass.”
60. Add a defect rule: failures automatically create architect-routed tasks with
   the exact verifier test name and short trace. The original QA task stays open
   or blocked until the gate is green, while implementation tasks proceed in
   parallel.
61. Add a scheduling rule: after each implementation merge, rerun only impacted
   gates plus the full five-gate smoke; reserve the final 30 minutes for a clean
   complete grader run and fix triage, not new duplicate verification tasks.
62. Success criterion for the next run: QA reports the same numbers the official
   grader will see, catches at least one real defect before finalization, and leaves
   a gate-by-gate audit trail that makes “0/5” impossible to discover only after
   the run ends.
