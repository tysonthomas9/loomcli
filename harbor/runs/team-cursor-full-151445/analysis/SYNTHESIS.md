# Synthesis — Huddle SWE-Marathon run

## Verdict

The score was 0.1875 because the run shipped a healthy shell and a broad but
partially conforming REST foundation while leaving the grader's critical path
unfinished: correctness was binary `0/5` (76/129 API tests, 0/11 IRC, 1/3
crash, 1/3 chaos, 0/1 journey), and the UX judge saw only auth/layout plus a
blank chat shell. The primary cause was orchestration: the lead allowed
architect-reviewed breadth, settings, and duplicate narrow QA work to consume
the implementation window while M10 files, M13/14 WebSocket/resilience, M15
IRC, and M17 chat remained open, in progress, or review. That scheduling error
was amplified by prompts that made design-only review and task-local green
tests look like completion, plus fixed-port contention; it ultimately exposed
real product defects (no :6667 listener, no registered `/api/ws`, no event
fan-out, no message composer/list, and a hidden onboarding field). The score
was therefore not mainly lost to polish: it was lost by failing to convert the
known gate contracts into one tested, integrated vertical slice before
finalization.

## Ranked root causes

Rank is by expected score leverage, confidence from the final artifact, and
avoidable opportunity cost. “Impact” describes the score surface it cost; a
single failed gate contributed zero to correctness because the correctness
reward required all five gates.

| Rank | Root cause | Score impact | Evidence pointers | Classification |
|---:|---|---|---|---|
| 1 | No gate-first scheduling or finalization barrier. The lead let optional breadth and M18 integrate while M10/M14/M15 were open and M17 was review. | Cost all correctness reward: `gates_passed=0/5`; also left the journey and major UX surfaces unowned. | Lead report §L1–L2; `digests/task-ledger.md:13-24`; `orchestrate.log:495-555`; `verifier/metrics.json:3-40`. | Harness/prompt/process, expressed as lead behaviour |
| 2 | M17 was routed to a design-only architect near the deadline, so the core chat journey never had an implementation owner. | Cost frontend journey `0/1`, UX channels/messaging/threads/reactions/validation failures, and most polish/realism points. | Frontend report §§“Why MARATHON-17 never shipped”, P0s; `daemon-filtered.log:5197-5208`; `digests/app-architect-1.md:5250-5265`; `app/static/index.html:141-171`; `judge/driver-report.txt:5-20`. | Harness/process plus product defect |
| 3 | WebSocket/event infrastructure was designed too late and never delivered or wired. | Cost API cluster/ordering/load failures and both WS-dependent crash/chaos cases; crash `1/3`, chaos `1/3`, API gate false. | Architect report P1/P4; backend report §“IRC and resilience gates”; `app/server/app.py:48-55`; `app/server/events.py:1-87`; `verifier/crash_pytest.json:28-47`; `verifier/chaos_pytest.json:28-50`. | Product defect enabled by scheduling |
| 4 | IRC was never implemented or started. | Cost the entire IRC gate, `0/11`, so no chance of correctness reward. | Lead/backend/QA reports P1; `digests/task-ledger.md:17-18`; `app/start.sh:40-45`; `verifier/irc_pytest.json:7-40`; `app-git-log.txt:1-26`. | Product defect plus process failure |
| 5 | QA and implementers treated private, task-local suites as “full” and did not run the official grader proxy. | Allowed 53 API failures, absent IRC/WS, crash recovery, chaos, and journey defects to reach finalization undetected. | QA report §§27–35; backend report §§“Final score” and “Per-session review”; `digests/backend-dev-1.md:3587-3590,4155-4244`; `digests/qa-engineer-1.md:178,412,689,918-939`; `verifier/test-stdout.txt:821-852`. | Harness/prompt/process plus agent behaviour |
| 6 | The final SPA was only a shell: selecting a channel changed the header but never fetched or rendered messages. | Cost UX messaging, empty state, thread/reaction reachability, validation and realism; judge passed auth/layout only. | Frontend report P0; grader-gap “Product defects”; `app/static/js/shell.js:150-176`; `app/static/index.html:149-171`; `judge/verdicts.json:11-68`; `judge/ux.json:15-68`. | Product defect |
| 7 | Official API contracts were not checked after integration; missing files/search/read-state/slash routes and several wrong status/order contracts remained. | Cost 53/129 API tests and therefore the API gate. The six/seven missing file tests, search, slash, mentions/read state, ordering, DM, and profile failures were additive. | Backend report §§“Mapping the 53 API failures”; grader-gap score-backward matrix; `app/server/app.py:48-55`; `app/server/channels/routes.py:460-556`; `app/server/messages/store.py:184-215`; `verifier/pytest.json:7-15`. | Product defects plus verification failure |
| 8 | Architect review was too broad and too slow: 17 sessions, repeated redesign, very large designs, and fallback claims displaced implementation. | Lost roughly 76 minutes of a 200-minute run; indirectly cost M13/M15/M17 delivery and final verification. | Architect report session inventory and A1/H1/H2/H5; `digests/app-architect-1.md:16-17,529-732,3945-4304,4723-5260`; `daemon-filtered.log:2813,2934,3730,4872`. | Harness/prompt/process |
| 9 | Shared fixed ports and supervisor cleanup consumed agent turns and made “live” verification unreliable. | Directly contributed to skipped cluster/live checks and late frontend cleanup; it did not itself explain every product failure, but reduced time for gates. | Frontend report P2; backend report agent behaviour §4; QA report H5; `digests/frontend-dev-1.md:789-825,1073-1115`; `digests/qa-engineer-1.md:310-329,790-803`; `daemon-filtered.log:452-503`. | Harness/process, with agent cleanup behaviour |
| 10 | Onboarding form visibility was not rerun against the official journey after frontend integration. | Cost the entire journey gate `0/1`, independently of the missing chat UI. | Lead/frontend reports P4/P1; `app/static/index.html:12-54`; `app/static/js/auth_ui.js`; `verifier/journey_pytest.json:17-29`. | Product defect plus process failure |

### Score accounting sanity check

The evidence supports this decomposition of the observed score:

- Correctness stage: `reward=0`, `partial_score=0.0`, `gates_passed=0`,
  `gates_total=5` (`verifier/metrics.json:16-40`). This is why 76 passing API
  tests did not produce partial correctness credit.
- API gate: 76/129 passed, with 53 failures. The failures combine absent
  capabilities (files, search, WebSocket) and contract mismatches (ordering,
  status codes, profiles, mentions/read state, slash commands, DMs).
- IRC gate: 0/11, primarily one root failure—no socket listener—rather than
  eleven independent protocol defects. The first connection refusal prevents
  registration, PING, JOIN, and bridge assertions from running meaningfully.
- Crash gate: 1/3. The surviving HTTP-node test passed; the WS fan-out test
  failed at handshake and the resume test failed when :8001 did not return.
- Chaos gate: 1/3. Redis-down writes passed; fallback fan-out and dense-seq
  observation failed at the same WS boundary.
- Frontend/journey gate: 0/1 because the official onboarding flow could not
  fill the hidden display-name field. Separately, the UX judge confirmed that
  the post-auth chat controls were absent, so fixing onboarding alone would
  not recover the UX score.
- UX stage: auth and layout passed, while channels, messaging, threads, and
  reactions failed; polish and realism were partial (`judge/verdicts.json`,
  `judge/ux.json`). The blank pane is therefore both a product defect and a
  missed opportunity to earn incremental UX points even if correctness stayed
  binary.

This also explains why “health=true” is not a contradiction. Health proves the
three HTTP processes can answer the health endpoint; it does not prove the
WebSocket, IRC, supervisor recovery, Redis fallback, API contract, or browser
journey surfaces that the grader scores.

## Disagreements and evidence checks

The reports mostly agree. Where they differ, the final files and verifier
outputs resolve the issue as follows.

1. **“The supervisor exists” versus “respawn is broken.”** The architect and
   grader-gap reports correctly identify a crash-gate failure, but the stronger
   wording “no supervisor” would be wrong. `app/start.sh:192-217` contains a
   respawn loop for Redis and all three nodes. The official result is still
   decisive: surviving-node HTTP passed, but the tested :8001 recovery failed
   and the WS fan-out path returned 403 (`verifier/crash_pytest.json:19-47`).
   Correct conclusion: the intended supervisor is present, but its observed
   runtime recovery contract is unreliable or mismatched with the grader; do
   not call it wholly absent.

2. **“Redis outage works” versus “chaos works.”** The backend/architect reports
   distinguish these correctly; any broad claim that chaos passed is wrong.
   `verifier/chaos_pytest.json:19-50` shows only
   `test_writes_still_succeed_while_redis_is_down` passed. Fallback fan-out and
   dense sequence both failed at the WebSocket handshake. Correct conclusion:
   durable writes survive Redis loss, but the observable cross-node delivery
   and sequence gate do not.

3. **QA’s 6667 check versus the product requirement.** QA legitimately found
   port 6667 unbound in its scaffold test and treated that as expected, but
   that expectation conflicts with the actual product spec/grader. The final
   IRC report is unambiguous: 11 tests, 0 passed, connection refused
   (`verifier/irc_pytest.json:7-40`), and `app/start.sh` starts Redis plus HTTP
   nodes but no IRC service. Thus QA’s local observation was right; its
   acceptance implication was wrong because the test was not a grader proxy.

4. **“M17 was not implemented” versus “frontend failed.”** These are not
   competing explanations. The frontend report correctly attributes the
   routing failure to the harness, while the judge correctly classifies the
   resulting blank pane as a product defect. The artifact proves both:
   `shell.js` hides the placeholder and sets a title but has no message fetch or
   render path (`app/static/js/shell.js:150-176`), and the judge found no
   textarea/create-channel control (`judge/driver-report.txt:5-20`).

5. **Exact API file-test count.** Grader-gap says six file tests; backend says
   seven. The exact count is less important than the shared evidence: the
   final artifact has no `app/server/files/` package, no integrated M10 commit,
   and the official suite reports file endpoints as 404
   (`verifier/test-stdout.txt:786-792`; `app-git-log.txt:1-26`). I therefore
   avoid using either count as a score claim and describe the missing file
   capability as an API-gate cluster.

6. **Whether QA was “busywork.”** QA’s tests were real and useful for boot and
   auth, so calling them worthless is wrong. The higher-confidence conclusion
   is opportunity cost and coverage mismatch: four focused tasks closed while
   no one ran the five official gates, and one auth task duplicated another
   (`reports/qa-engineer-1-report.md:21-30,36-55`; `integration.log:5-9`).

## Prioritized improvement plan

Items are ordered roughly by expected score effect per implementation effort.
“Effect” is directional: correctness remains binary unless all five gates pass,
while UX points can improve incrementally.

### Lead prompt

1. **Create a five-row gate matrix immediately after decomposition.** Each row
   must name the exact command, owner, dependency, latest result, and expected
   score surface for API, IRC, crash, chaos, and frontend/journey. Finalization
   fails if a row has no implementer or current result. Expected effect: prevents
   another `0/5` surprise; very high effect, minutes to add.
2. **Add deadline-mode checkpoints at 60/30/10 minutes.** At 30 minutes stop
   optional designs; at 10 minutes integrate the smallest end-to-end slice and
   run the official smoke. The lead must record which breadth tasks were
   cancelled and why. Expected effect: protects the last verification window
   and increases probability of flipping at least one binary gate.
3. **Make gate tasks outrank breadth after minimum dependencies.** Require an
   eligible implementer for each P0/P1 capability before settings, DMs, or
   duplicate QA can claim capacity. Expected effect: shifts time to IRC, WS,
   M17, files, and official API failures; high effect, low effort.
4. **Separate `merged`, `verified`, and `gate-passed` in every pass.** A merge
   or local test cannot close a task. Expected effect: exposes false green state
   early and makes re-planning evidence-based; medium-high effect, low effort.

### Role prompts

1. **Frontend DoD must be a browser vertical slice.** Require visible controls
   and a test for create channel → select → post → reload → thread → reaction;
   M17 must be assigned only to an implementation-capable frontend role.
   Expected effect: recovers most UX criteria and journey reachability; very high
   effect, low prompt effort.
2. **Backend DoD must run the official contract command on a clean cluster.**
   Require path, count, failed/skipped list, exit code, and exact integrated
   commit; excluded cluster/WS tests make the task `blocked`, not green.
   Expected effect: catches API/WS contract drift before merge; high effect.
3. **QA becomes a grader proxy.** First run the five official gates and publish
   a gate ledger; focused tests supplement uncovered failures and may not replace
   them. Expected effect: makes 53 API failures and absent IRC/WS visible while
   there is still time; high effect, modest implementation effort.
4. **Architect prompt becomes thin-design and contract-probe.** Cap ordinary
   designs at 4,000 characters/120 seconds; no redesign if `has_design=true`
   without a named defect; include one runnable probe and label it verified or
   unverified. Expected effect: returns tens of minutes to implementation; high
   effect, low effort.

### Harness + process

1. **Add an eligibility invariant:** a frontend implementation task cannot be
   claimed by a design-only role, and fallback cannot cross capability domains.
   Expected effect: directly prevents the M17 failure; very high effect, small
   scheduler change.
2. **Run a serialized official verifier after every critical merge and at
   finalization.** Store gate counts and first failures in the task ledger;
   block finalization when a critical task is open/review without an explicit
   score-maximizing decision. Expected effect: prevents late discovery and
   focuses fixes; very high effect.
3. **Provide isolated ports, data directories, and process ownership per worker.**
   The official grader may retain a serialized fixed-port reservation, but agents
   should not use `pkill` or share Redis/config state. Expected effect: recovers
   verification time and makes crash/chaos evidence trustworthy; medium-high.
4. **Make design waiting observable and expiring.** Record design owner,
   implementer, wait age, and `needs-revision`; auto-bypass small gate designs
   after two passes and alert the lead. Expected effect: reduces architect
   starvation and late fallback claims; medium-high.
5. **Add static/browser preflight selectors before integration.** Fail a frontend
   candidate if required chat selectors (`message-list`, composer, create modal,
   thread, emoji picker) are absent, then run the official onboarding journey.
   Expected effect: catches the blank shell in seconds; high UX effect, low effort.

## Single smallest change before the next run

Add one mandatory lead-prompt rule immediately after task creation:

> “Before any non-gate task is claimed, write the five-row official gate matrix
> with an eligible owner and command. At finalization, refuse to finish while
> any gate row is unassigned, unrun, or backed only by task-local tests.”

This is the smallest change with the best leverage: it does not require new
product code, but it would have made M17/IRC/WS absence visible at the first
planning pass and prevented “15 integrated” from being mistaken for a passing
run.
