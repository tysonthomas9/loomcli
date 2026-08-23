# App architect review — `app-architect-1`

## Executive conclusion

The architect role was not worth its cost in this run. It produced useful early
contracts, but it consumed 17 sessions / roughly 4,574 seconds (76 minutes)
while the final score was 0.1875 and correctness reward was zero [README.md:3-8].
The main loss was not lack of architectural prose; it was that design-only work
continued after implementation and verification were the binding constraints.

The strongest causal chain is:

1. A rigid prompt forced every architect session to be “Design Only - No
   Implementation” [digests/app-architect-1.md:17].
2. The daemon repeatedly scheduled the architect with `reason="fallback: no
   skill match"`, including tasks with no implementer [daemon-filtered.log:2813,
   2934, 3730, 4872].
3. The architect spent 477s on MARATHON-8, 459s on MARATHON-13, and 459s on
   MARATHON-17, but MARATHON-13 remained in progress and MARATHON-17 remained
   review [digests/app-architect-1.md:2816-3214, 3945-4304, 4723-5260;
   digests/task-ledger.md:16-20].
4. The unfinished contracts were exactly the high-value gates: WebSocket
   subscribe/resume, dense cross-node sequencing, IRC :6667, and the SPA
   messaging surface [digests/task-ledger.md:16-20].

## Session inventory and cost

The digest records these sessions and durations:

| Session | Task | Duration | Outcome |
|---|---|---:|---|
| 1 | MARATHON-2 | 208s | design saved; no implementation |
| 2 | MARATHON-3 | 198s | design saved; no implementation |
| 3 | MARATHON-5 | 187s | design saved; no implementation |
| 4 | MARATHON-4 | 170s | design saved; no implementation |
| 5 | MARATHON-5 redo | 128s | redesign after prior design |
| 6 | MARATHON-16 | 223s | design saved; no implementation |
| 7 | MARATHON-6 | 197s | design saved; no implementation |
| 8 | MARATHON-7 | 251s | design saved; no implementation |
| 9 | MARATHON-12 attempt | incomplete | rate-limited before saving |
| 10 | MARATHON-12 retry | 408s | design saved; no implementation |
| 11 | MARATHON-8 | 477s | design saved; no implementation |
| 12 | MARATHON-9 | 249s | design saved; no implementation |
| 13 | MARATHON-18 | 274s | design saved; no implementation |
| 14 | MARATHON-13 | 459s | design saved; task still in progress |
| 15 | MARATHON-11 | 301s | design saved; task still in progress |
| 16 | MARATHON-17 | 459s | design saved; task still review |
| 17 | MARATHON-10 | 187s | design saved; task still review |

The exact session/result records are in the digest [digests/app-architect-1.md:16-17,
222, 248, 504, 529, 732, 758, 934, 957, 1164, 1188, 1460, 1485, 1713,
1738, 2084, 2109, 2368, 2791, 2816, 3214, 3240, 3482, 3508, 3922, 3945,
4304, 4329, 4698, 4723, 5260, 5281, 5569].

### Finding A1 — repeated redesign was wasted capacity (high impact)

Evidence: MARATHON-5 was designed in session 3, then claimed again as session
5; the second session explicitly says it is a new design and saves another
23,608-character design [digests/app-architect-1.md:529-732, 957-1164].
The daemon shows the later claim at 22:53:56Z, while backend implementation
was only claimed at 23:00:08Z [daemon-filtered.log:1233-1245, 1408-1414].

Impact: the second architecture pass did not increase shipped behavior; it
delayed the implementer and consumed 128s during a schedule already ending at
01:35Z [README.md:28].

Improvement: add a scheduler invariant: `has_design=true` makes an architect
task ineligible unless a reviewer adds `needs-revision` with a concrete defect.
The prompt should say: “Do not redesign an existing design; review the diff and
either mark it accepted or report one failing contract.”

### Finding A2 — the agent did not convert discoveries into verification (high)

Evidence: session 1 discovered the required ports and SIGKILL behavior and
saved them as prose, ending “No implementation was done”
[digests/app-architect-1.md:190-232]. Later, session 14 designed WebSocket
fan-out, but the final tests rejected every WebSocket handshake with HTTP 403
[digests/app-architect-1.md:3945-4304; verifier/pytest.json:1251-1323].

Impact: the architect knew the contract but had no responsibility to run a
minimal executable check or stop design work when the implementation was absent.

Improvement: require every design to include a runnable contract probe (for
example `curl` on :8000/:8001/:8002, a TCP connect to :6667, and one WebSocket
handshake). For design-only tasks, the agent must report “unverified” rather
than imply completion.

### Finding A3 — rate-limit recovery caused a duplicate session and lost work
(medium)

Evidence: the first MARATHON-12 attempt exited with `[RateLimited] : usage
limit`, saved a checkpoint, reset the orphan to open, and waited 60s before
retrying [daemon-filtered.log:3014-3028]. The retry then spent 408s
[digests/app-architect-1.md:2109-2368, 2368-2791].

Improvement: checkpoint the design artifact incrementally, preserve the task in
`blocked/rate_limited` rather than reopening it as ordinary work, and route the
retry to an implementer or a smaller continuation prompt.

## Harness / prompt / process findings

### Finding H1 — design gate scope was far too broad (highest process impact)

Evidence: the role prompt says “CREATE DESIGNS, not implement them” and “one
task” [digests/app-architect-1.md:17]. Yet the final ledger contains unfinished
architect-labeled tasks: MARATHON-10 and MARATHON-17 are `review`, MARATHON-14
is `open`, and MARATHON-15 is `open` [digests/task-ledger.md:13-20].

Improvement: gate only cross-cutting tasks with at least two implementers or a
new shared contract (initial schema, event envelope, process topology). Small
tasks such as files, SPA controls, and a narrow REST route should be “design by
implementer”: a short plan in the implementation PR, then code and tests.

### Finding H2 — no claim rule prevented design starvation (highest)

Evidence: architect claims repeatedly use `fallback: no skill match`, including
MARATHON-7, MARATHON-12, MARATHON-8, MARATHON-13, MARATHON-17, and MARATHON-10
[daemon-filtered.log:2813, 2934, 3730, 4043, 4872, 5431]. During long waits,
the daemon logs the architect sitting in 30-second restart waits
[daemon-filtered.log:3270-3671].

Impact: the fallback policy treated “architect available” as better than “task
unclaimed,” even where no compatible implementer was available.

Improvement: never fallback-claim an architect task when `has_design=true`, or
when its dependency graph has no eligible implementer. Emit a blocked reason
with the missing role and expose it to the lead.

### Finding H3 — blocked-on-design was not measured or enforced (high)

Evidence: the ledger has no explicit blocked state or `has_design` column in its
summary table; the final state instead leaves MARATHON-14/15 open and
MARATHON-13 in progress [digests/task-ledger.md:3, 16-18]. The design for
MARATHON-5 even notes that it “Blocks MARATHON-6/7/16” while the task is still
being handed back for review [digests/app-architect-1.md:698-714].

Improvement: record `waiting_for_design_since`, `design_owner`, and
`implementer_claimed_at`. Alert after 2 minutes and let the lead bypass the gate
for a small task. Report counts per task at every pass; do not infer blocking
from prose notes.

### Finding H4 — lead visibility was insufficient

Evidence: README says the headless lead transcript was not captured and must be
reconstructed from several logs [README.md:10-17]. That makes it impossible to
prove why review tasks were approved, why MARATHON-14/15 were left open, or why
the final prioritization favored more designs over final gates.

Improvement: persist every lead decision as a structured event: task, old/new
priority, reason, design gate decision, implementation owner, and verification
result. Make “deadline mode” automatically prioritize failing official gates.

### Finding H5 — no design-size cap encouraged over-analysis

Evidence: designs reached 18,024, 20,666, 23,608, 27,298, and 28,664 characters
[digests/app-architect-1.md:200, 481, 698, 5045, 5218; digests/task-ledger.md:33-45].
The longest sessions were also late, high-value tasks: MARATHON-8 477s,
MARATHON-13 459s, and MARATHON-17 459s [digests/app-architect-1.md:2816-3214,
3945-4304, 4723-5260].

Improvement: cap a normal design at 4,000 characters / 120 seconds, with a
required table of routes, schemas, invariants, tests, and risks. Require explicit
approval for an exception, and stop when all acceptance tests are mapped.

## Product defects in `app/`

### Finding P1 — WebSocket endpoint is unusable (highest score impact)

Evidence: official tests fail at the handshake with “server rejected WebSocket
connection: HTTP 403” for cluster fan-out, resume, ordering, profile updates,
crash, and chaos [verifier/pytest.json:381, 1251-1323;
verifier/crash_pytest.json:22-57; verifier/chaos_pytest.json:22-58].
The result is 76/129 API tests, 1/3 crash tests, and 1/3 chaos tests
[verifier/metrics.json:2-18].

Improvement/code fix: implement `/api/ws` authentication exactly as the tests
call it (`?token=`), subscribe/resume replay from durable events, and add an
integration test on each of :8000, :8001, and :8002 before closing MARATHON-13.

### Finding P2 — node respawn contract fails on :8001 (high)

Evidence: crash verification says “node on :8001 did not come back up” after
kill, although the surviving-node test passed [verifier/crash_pytest.json:9-57].
This directly contradicts the scaffold design’s required “respawns any of those
children after SIGKILL” [digests/task-ledger.md:33-35].

Improvement/code fix: supervise each child by PID, restart on any exit including
SIGKILL, use a per-node port-health check, and test repeated kill/restart cycles
for all three ports. Do not close the scaffold gate based only on initial health.

### Finding P3 — IRC was never implemented or scheduled to completion (high)

Evidence: all 11 IRC tests failed; the first failure is `ConnectionRefusedError`
on `IRC_PORT` [verifier/irc_pytest.json:1-28]. The ledger leaves MARATHON-15
`open`, despite the epic requiring IRC on :6667 [digests/task-ledger.md:17-18;
README.md:3-4].

Improvement/code fix: make MARATHON-15 a critical-path task with a backend owner;
implement the registration/welcome, PING/PONG, JOIN numerics, PRIVMSG bridge,
nick collision, unknown command, and QUIT tests. Gate finalization on a TCP
listener at 127.0.0.1:6667 and at least one web-to-IRC round trip.

### Finding P4 — Redis-outage writes pass, but fallback fan-out and dense seq do
not (high)

Evidence: chaos passed only `test_writes_still_succeed_while_redis_is_down`;
the fallback fan-out and dense-seq tests fail at the same 403 WebSocket handshake
[verifier/chaos_pytest.json:9-58]. This means the durable-write part of the
design was implemented, but the observable cross-node contract was not.

Improvement/code fix: allocate `seq` transactionally in SQLite, persist events
before publish, replay missed events by `last_seq`, and use a polling/fallback
path when Redis is unavailable. Add a test that kills Redis, writes on node A,
and observes the event on node B.

### Finding P5 — SPA has auth shell but no core chat workflow (highest UX impact)

Evidence: the UX driver found zero `textarea` elements, a message pane containing
only a header/placeholder, and no `createChannel`/“New channel” control
[judge/driver-report.txt:5-14]. The README summarizes the same failures: no
channel-create control, message list, or composer [README.md:5-8].

Improvement/code fix: implement channel creation and duplicate-name feedback;
render channel messages with author/avatar/time/body; add a composer with empty
validation; wire edit/delete, thread panel, reaction picker/badges, topic
display, and hard-reload persistence to the existing REST APIs. Add one
Playwright journey covering create → select → post → reload → reply → react.

### Finding P6 — REST contract gaps remain in completed areas (medium)

Evidence: official tests report incorrect 201 vs 404/403 slash-command behavior,
missing `channel` envelope, group mentions resolving to an empty set, invitation
404s, and an admin being allowed to promote to admin [verifier/pytest.json:765-803,
879-940, 987-1066, 1113-1114].

Improvement/code fix: treat official tests as the contract, add focused regression
tests for each failing route, and require response-envelope snapshots (`{channel}`,
`{message}`, `{error:{code,message}}`) at every HTTP node.

## Was the architect role worth its cost?

For this run, no. It helped establish SQLite/WAL, the three HTTP ports, shared
auth, and some response-shape conventions; those early foundations did ship
[digests/task-ledger.md:33-43]. But the role spent more than an hour producing
large designs while the final artifact lacked the exact capabilities that the
design backlog called out. Fifteen tasks were integrated, yet the score still
reported zero correctness gates and no IRC [README.md:5-8].

Recommended replacement:

1. Keep one architect pass for the initial topology, shared schema, event
   envelope, and security boundaries.
2. Cap each design at 4,000 characters and 120 seconds.
3. Use design-by-implementer for small REST, files, and SPA tasks.
4. Make a design gate expire after 2 minutes without an implementer claim.
5. Require the architect to run contract probes and mark them verified/unverified.
6. Reserve the last 25% of the budget for official gates and UX journeys.
7. In deadline mode, stop all new designs when any critical task is open,
   review, or failing a gate; reassign the architect to implement or verify it.
