---------------------------- MODULE DaemonAttempt ----------------------------
(***************************************************************************)
(* Finite model of the daemon attempt protocol: ownership leases, issue   *)
(* claims, and session finalization between LoomCLI daemons and FleetDB.  *)
(*                                                                         *)
(* Source basis: LoomCLI 1c6dabfc8 (internal/cli/daemon/supervisor) and    *)
(* FleetDB 40e8431d (internal/storage, internal/service). Server rules    *)
(* come from FleetDB source; nothing here was observed at runtime.        *)
(*                                                                         *)
(* STAGE 1 = ownership leases, 2 = + issue claims, 3 = + sessions.        *)
(* WriteCheck selects what FleetDB compares on agent-scoped writes:       *)
(*   "none"     = current issue/session writes (no ownership check)        *)
(*   "token"    = compare the ownership Token (kept on same-owner reacquire)*)
(*   "fence_eq" = FencingToken equality only (mutation: ignores release    *)
(*                and expiry)                                              *)
(*   "fence"    = FencingToken equal AND lease active AND unexpired on the *)
(*                FleetDB clock, checked atomically with the write (P2)    *)
(* SessionCheck: session status writes also go through WriteCheck.         *)
(* HolderCheck: issue writes also require assignee = writer (proposed).    *)
(* TerminalGuard: session updates never leave a terminal status.          *)
(* GuardedSpawn: spawn only while the ownership heartbeat loop is live and *)
(* local validity holds (proposed; current spawn has no ownership check).  *)
(* Reconcile: after acquiring ownership, fail the agent's unfinished older  *)
(* sessions before claiming work (open PR #396, abandoned-run recorder).   *)
(* SharedActor: every agent claims through one FleetDB actor Srv (the      *)
(* serve/worktree-less fallback of claim.go:237-248; open #761, #541,      *)
(* #348, #92). FALSE = per-agent actor, as those fixes propose.            *)
(* NonAtomicClaim splits claim preflight from its write (#538).             *)
(* HookStatus/HookGuard and DeferredStatus/RespectDeferred model status     *)
(* writes after exit and a human deferred hold (#780, #734).               *)
(* LockOnlyRelease splits lock and assignment release for label-only       *)
(* handoffs (#634); ReviewCrash/RecoverReview cover restart recovery       *)
(* of review status (#381). All new switches are FALSE in old configs.     *)
(*                                                                         *)
(* Time is abstract ticks of the FleetDB Go clock. A daemon may believe   *)
(* its lease is valid for up to Drift ticks past the server expiry (slow   *)
(* local clock), may be paused for up to Pause ticks, and its agent may   *)
(* survive SIGTERM for up to Grace ticks. PgSkew lets the Postgres acquire *)
(* compare expiry against database NOW() running ahead of the Go clock.   *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Hosts, Agents, Issues, NoOne, Srv,
          STAGE, TTL, LockTTL, MaxTime, Drift, Pause, Grace, PgSkew,
          MaxFence, MaxInFlight, MaxInc,
          WriteCheck, HolderCheck, TerminalGuard, GuardedSpawn, SessionCheck,
          Reconcile, SharedActor, NonAtomicClaim, HookStatus, HookGuard,
          DeferredStatus, RespectDeferred, LockOnlyRelease, ReleaseAssignment,
          ReviewCrash, RecoverReview

ASSUME /\ STAGE \in {1, 2, 3}
       /\ WriteCheck \in {"none", "token", "fence_eq", "fence"}
       /\ SessionCheck \in BOOLEAN
       /\ HolderCheck \in BOOLEAN /\ TerminalGuard \in BOOLEAN
       /\ GuardedSpawn \in BOOLEAN
       /\ Reconcile \in BOOLEAN /\ SharedActor \in BOOLEAN
       /\ NonAtomicClaim \in BOOLEAN /\ HookStatus \in BOOLEAN
       /\ HookGuard \in BOOLEAN /\ DeferredStatus \in BOOLEAN
       /\ RespectDeferred \in BOOLEAN /\ LockOnlyRelease \in BOOLEAN
       /\ ReleaseAssignment \in BOOLEAN /\ ReviewCrash \in BOOLEAN
       /\ RecoverReview \in BOOLEAN
       /\ \A n \in {TTL, LockTTL, MaxTime, Drift, Pause, Grace, PgSkew,
                    MaxFence, MaxInFlight, MaxInc} : n \in Nat
       /\ TTL > 0 /\ LockTTL > 0

VARIABLES
    now,        \* FleetDB Go clock
    lease,      \* agent ownership lease per agent (server)
    nextFence,  \* last issued fencing token (also used as fresh Token value)
    issue,      \* issue projection plus claim lock (server)
    sess,       \* AgentSession status per session id (server)
    net,        \* requests sent but not yet applied by the server
    up,         \* daemon process on host is running
    inc,        \* daemon incarnation; OwnerID = <<host, inc>> (hostname-pid)
    ph,         \* supervisor phase per host and agent
    proc,       \* agent child process per host and agent
    my,         \* supervisor's local view of its current attempt
    hb,         \* ownership heartbeat goroutine running for host and agent
    staleW,     \* ghost: kinds of stale write FleetDB applied (see StaleKinds)
    foreignW,   \* ghost: an issue write hit another attempt's claim
    term,       \* ghost: session ids that were finalized (or reconciled)
    sagent      \* agent that started each session id (server session row)

vars == <<now, lease, nextFence, issue, sess, net, up, inc, ph, proc, my, hb,
          staleW, foreignW, term, sagent>>

Owners   == Hosts \X (0..MaxInc)
Sids     == 1..MaxFence
Phases   == {"idle", "owned", "claimed", "running", "exited", "recovered"}
AliveP   == {"run", "stop", "orphan"}
Terminal == {"completed", "failed"}
Unfinished == {"starting", "running"}

NoLease == [owner |-> NoOne, tok |-> 0, fence |-> 0, sid |-> 0, exp |-> 0,
            active |-> FALSE]
NoAtt   == [tok |-> 0, fence |-> 0, sid |-> 0, iss |-> NoOne, sent |-> 0,
            closed |-> FALSE, rec |-> FALSE]
\* asg/holder are FleetDB actors; who is a ghost naming the claiming agent.
NoIssue == [st |-> "open", asg |-> NoOne, holder |-> NoOne, lexp |-> 0,
            who |-> NoOne]

Symm == Permutations(Hosts) \cup Permutations(Agents) \cup Permutations(Issues)

-----------------------------------------------------------------------------
(* Helpers *)

Owner(h) == <<h, inc[h]>>
\* FleetDB actor recorded on claims and compared by issue writes.
ActorOf(a) == IF SharedActor THEN Srv ELSE a
GoLive(a) == lease[a].active /\ now < lease[a].exp       \* Go-clock validity
DbLive(a) == lease[a].active /\ now + PgSkew < lease[a].exp   \* PG acquire
TokenOk(h, a) == lease[a].active /\ lease[a].tok = my[h][a].tok /\ now < lease[a].exp

\* Latest instant the supervisor's timer, pause, and SIGTERM grace let the
\* agent process outlive the last confirmed renewal (sent-time anchor).
DeadBy(h, a) == my[h][a].sent + TTL + Drift + Pause + Grace

PassNow(h, a) ==
    CASE WriteCheck = "none"     -> TRUE
      [] WriteCheck = "token"    -> my[h][a].tok = lease[a].tok
      [] WriteCheck = "fence_eq" -> my[h][a].fence = lease[a].fence
      [] WriteCheck = "fence"    -> my[h][a].fence = lease[a].fence /\ GoLive(a)

Pass(m) ==
    CASE WriteCheck = "none"     -> TRUE
      [] WriteCheck = "token"    -> m.tok = lease[m.a].tok
      [] WriteCheck = "fence_eq" -> m.fence = lease[m.a].fence
      [] WriteCheck = "fence"    -> m.fence = lease[m.a].fence /\ GoLive(m.a)

SessPass(m) == ~SessionCheck \/ Pass(m)

Msg(k, h, a, i) ==
    [k |-> k, a |-> a, i |-> i, sid |-> my[h][a].sid,
     fence |-> my[h][a].fence, tok |-> my[h][a].tok]

Send(S) == /\ Cardinality(net \cup S) <= MaxInFlight
           /\ net' = net \cup S

\* Ghost for contract P2, evaluated when FleetDB applies a write from attempt
\* sid (the fence issued when that attempt acquired):
\*   "superseded" another acquisition started a newer attempt for the agent
\*   "released"   the lease was released and not yet re-acquired
\*   "expired"    the lease is active but past expiry on the FleetDB clock
StaleKinds(a, sid) ==
    (IF sid # lease[a].sid THEN {"superseded"} ELSE {})
    \cup (IF sid = lease[a].sid /\ ~lease[a].active THEN {"released"} ELSE {})
    \cup (IF sid = lease[a].sid /\ lease[a].active /\ now >= lease[a].exp
            THEN {"expired"} ELSE {})
Stale(m) == StaleKinds(m.a, m.sid)
HumanHeld(i) == issue[i].st \in {"closed", "deferred"}
\* A release only ends a lease; releasing one's own expired lease is benign.
StaleRel(m) == IF m.sid # lease[m.a].sid THEN {"superseded"} ELSE {}

\* Ghost: the issue write lands on a claim held by another agent or by a
\* newer attempt of the same agent.
Foreign(m) ==
    \/ /\ issue[m.i].st = "in_progress"
       /\ (issue[m.i].who # m.a \/ m.sid # lease[m.a].sid)
    \/ /\ issue[m.i].st = "closed"
       /\ m.k = "reset"

HolderOk(m) == ~HolderCheck \/
    (issue[m.i].asg = ActorOf(m.a) /\
     (issue[m.i].st = "in_progress" \/
      (DeferredStatus /\ issue[m.i].st = "deferred")))

-----------------------------------------------------------------------------
Init ==
    /\ now = 0
    /\ lease = [a \in Agents |-> NoLease]
    /\ nextFence = 0
    /\ issue = [i \in Issues |-> NoIssue]
    /\ sess = [s \in Sids |-> "none"]
    /\ net = {}
    /\ up = [h \in Hosts |-> TRUE]
    /\ inc = [h \in Hosts |-> 0]
    /\ ph = [h \in Hosts |-> [a \in Agents |-> "idle"]]
    /\ proc = [h \in Hosts |-> [a \in Agents |-> "none"]]
    /\ my = [h \in Hosts |-> [a \in Agents |-> NoAtt]]
    /\ hb = [h \in Hosts |-> [a \in Agents |-> FALSE]]
    /\ staleW = {}
    /\ foreignW = FALSE
    /\ term = {}
    /\ sagent = [s \in Sids |-> NoOne]

-----------------------------------------------------------------------------
(* Environment *)

\* Time advances only if every guarded agent process can still be alive at
\* the next tick; this encodes the bounded ride-out, pause, and grace. A
\* process spawned after its heartbeat loop ended has no ownership deadline.
Guarded(h, a) == (proc[h][a] = "run" /\ hb[h][a]) \/ proc[h][a] = "stop"

Tick ==
    /\ now < MaxTime
    /\ \A h \in Hosts, a \in Agents : Guarded(h, a) => now + 1 < DeadBy(h, a)
    /\ now' = now + 1
    /\ UNCHANGED <<hb, lease, nextFence, issue, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

\* Daemon crash: supervisor state is lost; children become orphans.
Crash(h) ==
    /\ up[h] /\ inc[h] < MaxInc
    /\ up' = [up EXCEPT ![h] = FALSE]
    /\ proc' = [proc EXCEPT ![h] = [a \in Agents |->
                  IF proc[h][a] \in {"run", "stop"} THEN "orphan" ELSE proc[h][a]]]
    /\ ph' = [ph EXCEPT ![h] = [a \in Agents |-> "idle"]]
    /\ my' = [my EXCEPT ![h] = [a \in Agents |-> NoAtt]]
    /\ hb' = [hb EXCEPT ![h] = [a \in Agents |-> FALSE]]
    /\ staleW' = staleW \cup
        (IF ReviewCrash /\ \E i \in Issues : issue[i].st = "review"
         THEN {"review_crashed"} ELSE {})
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, net, inc, foreignW, term, sagent>>

\* Restart with a new OwnerID; the startup orphan sweep kills old children.
Restart(h) ==
    /\ ~up[h]
    /\ up' = [up EXCEPT ![h] = TRUE]
    /\ inc' = [inc EXCEPT ![h] = @ + 1]
    /\ proc' = [proc EXCEPT ![h] = [a \in Agents |->
                  IF proc[h][a] = "orphan" THEN "none" ELSE proc[h][a]]]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, ph, my, staleW, foreignW, term, sagent>>

-----------------------------------------------------------------------------
(* Ownership lease: supervisor/ownership.go; fleet-db redis.go:218-277,    *)
(* postgres/control_plane.go:917-1056.                                      *)

\* T1 AcquireOwnership at the top of the supervise loop (new attempt).
Acquire(h, a) ==
    /\ up[h] /\ ph[h][a] = "idle" /\ proc[h][a] = "none"
    /\ nextFence < MaxFence
    /\ LET f    == nextFence + 1
           same == DbLive(a) /\ lease[a].owner = Owner(h)
           tk   == IF same THEN lease[a].tok ELSE f
       IN /\ ~DbLive(a) \/ same
          /\ lease' = [lease EXCEPT ![a] = [owner |-> Owner(h), tok |-> tk,
                          fence |-> f, sid |-> f, exp |-> now + TTL, active |-> TRUE]]
          /\ nextFence' = f
          /\ my' = [my EXCEPT ![h][a] = [NoAtt EXCEPT !.tok = tk, !.fence = f,
                                          !.sid = f, !.sent = now]]
          /\ ph' = [ph EXCEPT ![h][a] = "owned"]
          /\ hb' = [hb EXCEPT ![h][a] = TRUE]
    /\ UNCHANGED <<now, issue, sess, net, up, inc, proc, staleW, foreignW, term, sagent>>

\* T2 heartbeat success: sent-time becomes the local validity anchor.
HbOk(h, a) ==
    /\ up[h] /\ hb[h][a] /\ TokenOk(h, a)
    /\ lease' = [lease EXCEPT ![a].exp = now + TTL]
    /\ my' = [my EXCEPT ![h][a].sent = now, ![h][a].fence = lease[a].fence]
    /\ UNCHANGED <<hb, now, nextFence, issue, sess, net, up, inc, ph, proc,
                   staleW, foreignW, term, sagent>>

\* Delayed/lost reply: the server renewed, the daemon cannot tell (rides out).
HbLostReply(h, a) ==
    /\ up[h] /\ hb[h][a] /\ TokenOk(h, a)
    /\ lease' = [lease EXCEPT ![a].exp = now + TTL]
    /\ UNCHANGED <<hb, now, nextFence, issue, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

\* Typed heartbeat failure -> arbitrateOwnershipByReacquire (same attempt).
\* Same owner and live keeps the Token and bumps the FencingToken. With no
\* running process the dead-process guard returns false and the heartbeat
\* loop ends; a later spawn then runs with no ownership heartbeat.
HbTypedFail(h, a) ==
    /\ up[h] /\ hb[h][a] /\ ~TokenOk(h, a)
    /\ IF proc[h][a] # "run"
         THEN /\ hb' = [hb EXCEPT ![h][a] = FALSE]
              /\ UNCHANGED <<lease, nextFence, my, proc>>
       ELSE IF ~DbLive(a) \/ lease[a].owner = Owner(h)
         THEN /\ nextFence < MaxFence
              /\ LET f    == nextFence + 1
                     same == DbLive(a)
                     tk   == IF same THEN lease[a].tok ELSE f
                 IN /\ lease' = [lease EXCEPT ![a] = [owner |-> Owner(h), tok |-> tk,
                                   fence |-> f, sid |-> my[h][a].sid,
                                   exp |-> now + TTL, active |-> TRUE]]
                    /\ nextFence' = f
                    /\ my' = [my EXCEPT ![h][a].tok = tk, ![h][a].fence = f,
                                        ![h][a].sent = now]
              /\ UNCHANGED <<proc, hb>>
         ELSE /\ proc' = [proc EXCEPT ![h][a] = "stop"]   \* HeldByOther: kill
              /\ hb' = [hb EXCEPT ![h][a] = FALSE]
              /\ UNCHANGED <<lease, nextFence, my>>
    /\ UNCHANGED <<now, issue, sess, net, up, inc, ph, staleW, foreignW, term, sagent>>

\* continueOwnershipIfWithinValidity gives up once local elapsed >= TTL.
\* The latest point is enforced by Tick through DeadBy.
\* Before spawn there is nothing to kill: the loop still ends (StopAgent is a
\* no-op without a process, health.go:30-38).
KillUnverifiable(h, a) ==
    /\ up[h] /\ hb[h][a] /\ now >= my[h][a].sent + TTL
    /\ proc' = [proc EXCEPT ![h][a] = IF @ = "run" THEN "stop" ELSE @]
    /\ hb' = [hb EXCEPT ![h][a] = FALSE]
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, net, up, inc, ph, my,
                   staleW, foreignW, term, sagent>>

\* Open PR #396 abandoned-run recorder, agent-wide pass: after AcquireOwnership
\* and before claiming, mark every unfinished session the agent started in
\* an earlier attempt as failed (abandoned_run). The recorder's RPCs are
\* synchronous and need the ownership lease, so the pass is one atomic,
\* WriteCheck-gated step; a failed pass leaves rec FALSE and the attempt
\* cannot claim. Late delivery of recorder writes is not modelled.
ReconcileSessions(h, a) ==
    /\ Reconcile /\ STAGE = 3
    /\ up[h] /\ ph[h][a] = "owned" /\ ~my[h][a].rec
    /\ PassNow(h, a)
    /\ LET old == {s \in Sids : /\ sagent[s] = a /\ s # my[h][a].sid
                                /\ sess[s] \in Unfinished}
       IN /\ sess' = [s \in Sids |-> IF s \in old THEN "failed" ELSE sess[s]]
          /\ term' = term \cup old
    /\ my' = [my EXCEPT ![h][a].rec = TRUE]
    /\ staleW' = staleW \cup StaleKinds(a, my[h][a].sid)
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, net, up, inc, ph, proc,
                   foreignW, sagent>>

-----------------------------------------------------------------------------
(* Issue claim: supervisor/claim.go; fleet-db service/issue_service.go.    *)

\* #538: two agents can both finish a non-serializing backend preflight
\* before either writes. The selected issue is held in my.iss locally.
ClaimPreflight(h, a, i) ==
    /\ NonAtomicClaim /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "owned" /\ my[h][a].iss = NoOne
    /\ issue[i].st = "open" /\ issue[i].holder = NoOne
    /\ my' = [my EXCEPT ![h][a].iss = i]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, up, inc, ph, proc,
                   staleW, foreignW, term, sagent>>

\* T5 ClaimTask (atomic lock + projection write in the model). A same-actor
\* reclaim is idempotent; an expired lock allows a stale takeover. FleetDB
\* sees the actor, so under SharedActor a sibling agent passes the
\* same-holder branch (claim.go:241-247).
Claim(h, a, i) ==
    /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "owned"
    /\ (Reconcile /\ STAGE = 3) => my[h][a].rec
    /\ LET I == issue[i]
           x  == ActorOf(a)
           lockFree == I.holder = NoOne \/ I.lexp <= now
       IN IF NonAtomicClaim
            THEN my[h][a].iss = i
            ELSE /\ I.st # "closed"
                 /\ lockFree \/ I.holder = x
                 /\ I.st = "open" \/ I.asg = x \/ lockFree
    /\ PassNow(h, a)
    /\ issue' = [issue EXCEPT ![i] = [st |-> "in_progress", asg |-> ActorOf(a),
                                      holder |-> ActorOf(a), lexp |-> now + LockTTL,
                                      who |-> a]]
    /\ my' = [my EXCEPT ![h][a].iss = i]
    /\ ph' = [ph EXCEPT ![h][a] = "claimed"]
    /\ staleW' = staleW \cup StaleKinds(a, my[h][a].sid)
                   \cup (IF DeferredStatus /\ issue[i].st = "deferred"
                         THEN {"deferred_lost"} ELSE {})
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, proc, foreignW, term, sagent>>

\* Preflight found no work or was gated (supervisor.go:320-330): go straight
\* to ownership release. postExitCleanup is an empty hook (:830-833).
NoWork(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "owned"
    /\ ph' = [ph EXCEPT ![h][a] = "recovered"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, up, inc, proc, my,
                   staleW, foreignW, term, sagent>>

\* Supervisor worker heartbeat during cmd.Wait(): renews the lock to LockTTL
\* when the holder name matches; ownership_lost is ignored (HTTP 200).
WorkerHb(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ my[h][a].iss # NoOne
    /\ issue[my[h][a].iss].holder = ActorOf(a)
    /\ PassNow(h, a)
    /\ issue' = [issue EXCEPT ![my[h][a].iss].lexp = now + LockTTL]
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

-----------------------------------------------------------------------------
(* Attempt lifecycle: spawn, agent writes over IPC, exit, finalize, recover. *)

\* T7 spawn. Current code does not re-check ownership before spawning.
Spawn(h, a) ==
    /\ up[h]
    /\ ph[h][a] = IF STAGE = 1 THEN "owned" ELSE "claimed"
    /\ GuardedSpawn => (hb[h][a] /\ now < my[h][a].sent + TTL)
    /\ ph' = [ph EXCEPT ![h][a] = "running"]
    /\ proc' = [proc EXCEPT ![h][a] = "run"]
    /\ IF STAGE = 3
         THEN /\ sess' = [sess EXCEPT ![my[h][a].sid] = "starting"]
              /\ sagent' = [sagent EXCEPT ![my[h][a].sid] = a]
              /\ Send({Msg("srun", h, a, NoOne)})       \* status -> running
         ELSE UNCHANGED <<sess, net, sagent>>
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, up, inc, my,
                   staleW, foreignW, term>>

\* Stage 1: an abstract agent-scoped write sent through daemon IPC.
AgentWrite(h, a) ==
    /\ STAGE = 1
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ Send({Msg("aw", h, a, NoOne)})
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

\* Stage 2+: the agent closes its issue through IPC (update/close do not
\* check the claim in FleetDB).
AgentClose(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ my[h][a].iss # NoOne /\ ~my[h][a].closed
    /\ Send({Msg("close", h, a, my[h][a].iss)})
    /\ my' = [my EXCEPT ![h][a].closed = TRUE]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, ph, proc,
                   staleW, foreignW, term, sagent>>

ProcExit(h, a) ==
    /\ proc[h][a] \in {"run", "stop"}
    /\ proc' = [proc EXCEPT ![h][a] = "none"]
    /\ ph' = [ph EXCEPT ![h][a] = "exited"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, up, inc, my,
                   staleW, foreignW, term, sagent>>

\* #780/#734: completion-hook set_status runs after cmd.Wait. A human may
\* have closed or deferred the issue while the agent was running.
HumanStatus(i, status) ==
    /\ STAGE >= 2 /\ (HookStatus \/ DeferredStatus)
    /\ status \in {"closed", "deferred"}
    /\ issue[i].st = "in_progress"
    /\ issue' = [issue EXCEPT ![i].st = status]
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

HookSetStatus(h, a) ==
    /\ HookStatus /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "exited" /\ my[h][a].iss # NoOne
    /\ (~HookGuard \/ issue[my[h][a].iss].st # "closed")
    /\ (~RespectDeferred \/ issue[my[h][a].iss].st # "deferred")
    /\ LET i == my[h][a].iss
       IN /\ issue' = [issue EXCEPT ![i].st = "open"]
          /\ foreignW' = (foreignW \/ (issue[i].st = "closed"))
          /\ staleW' = staleW \cup (IF issue[i].st = "deferred"
                                     THEN {"deferred_lost"} ELSE {})
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   term, sagent>>

\* #381: an agent can mark review before the daemon crashes. The restart
\* recovery pass decides whether review is completed work or needs requeue.
AgentReview(h, a) ==
    /\ ReviewCrash /\ STAGE >= 2
    /\ up[h] /\ proc[h][a] = "run" /\ my[h][a].iss # NoOne
    /\ issue[my[h][a].iss].st = "in_progress"
    /\ issue' = [issue EXCEPT ![my[h][a].iss].st = "review"]
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term, sagent>>

RecoverReviewIssue(i) ==
    /\ ReviewCrash /\ issue[i].st = "review"
    /\ "review_crashed" \in staleW
    /\ \E h \in Hosts : up[h] /\ inc[h] > 0
    /\ IF RecoverReview
         THEN /\ issue' = [issue EXCEPT ![i].st = "open"]
              /\ UNCHANGED staleW
         ELSE /\ staleW' = staleW \cup {"review_stranded"}
              /\ UNCHANGED issue
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   foreignW, term, sagent>>

\* Inside spawnAndWait after cmd.Wait() (supervisor.go:795-816), still under
\* ownership with the heartbeat running: completion hooks, T12
\* finalizeAgentSession (session completion, claim release) and T13
\* postMortemRecovery (resetTask reads the issue and writes later, orphan
\* reset covers every in_progress issue assigned to the agent name).
\* Requests are sent together and may be applied late or in any order, as a
\* timed-out RPC can still reach FleetDB. Stage 1 sends one abstract write.
Finalize(h, a) ==
    /\ up[h] /\ ph[h][a] = "exited" /\ proc[h][a] = "none"
    /\ LET fin == IF STAGE = 3 THEN {Msg("sfin", h, a, NoOne)} ELSE {}
           rc  == IF STAGE >= 2 /\ my[h][a].iss # NoOne
                    THEN {Msg("relclaim", h, a, my[h][a].iss)} ELSE {}
           ra  == IF STAGE >= 2 /\ my[h][a].iss # NoOne
                       /\ LockOnlyRelease /\ ReleaseAssignment
                    THEN {Msg("relassign", h, a, my[h][a].iss)} ELSE {}
           R   == {i \in Issues :
                     \/ i = my[h][a].iss /\ issue[i].st # "closed"
                     \/ issue[i].st = "in_progress" /\ issue[i].asg = ActorOf(a)}
           \* Label-only clean exit has no failure reset; it only releases
           \* the claim lock and (with the fix) the projected assignment.
           rs  == IF STAGE >= 2 /\ ~LockOnlyRelease
                    THEN {Msg("reset", h, a, i) : i \in R}
                               ELSE {Msg("aw", h, a, NoOne)}
       IN Send(fin \cup rc \cup ra \cup rs)
    /\ ph' = [ph EXCEPT ![h][a] = "recovered"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, proc, my,
                   staleW, foreignW, term, sagent>>

\* T14 releaseOwnership after spawnAndWait returns (supervisor.go:337-341):
\* stop the heartbeat, then send Release(token).
ReleaseOwn(h, a) ==
    /\ up[h] /\ ph[h][a] = "recovered"
    /\ Send({Msg("rel", h, a, NoOne)})
    /\ ph' = [ph EXCEPT ![h][a] = "idle"]
    /\ hb' = [hb EXCEPT ![h][a] = FALSE]
    /\ my' = [my EXCEPT ![h][a] = NoAtt]
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, up, inc, proc,
                   staleW, foreignW, term, sagent>>

-----------------------------------------------------------------------------
(* FleetDB applies a delayed request. *)

Deliver(m) ==
    /\ net' = net \ {m}
    /\ CASE m.k = "rel" ->
              \* Release checks the Token only (plus fence if proposed).
              IF /\ lease[m.a].active /\ lease[m.a].tok = m.tok
                 /\ (WriteCheck \in {"fence", "fence_eq"} => lease[m.a].fence = m.fence)
                THEN /\ lease' = [lease EXCEPT ![m.a].active = FALSE]
                     /\ staleW' = staleW \cup StaleRel(m)
                     /\ UNCHANGED <<issue, sess, foreignW, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "aw" ->
              IF Pass(m)
                THEN /\ staleW' = staleW \cup Stale(m)
                     /\ UNCHANGED <<lease, issue, sess, foreignW, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "close" ->
              IF Pass(m) /\ HolderOk(m) /\ issue[m.i].st # "closed"
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "closed",
                                   asg |-> NoOne, holder |-> NoOne, lexp |-> 0,
                                   who |-> NoOne]]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "relclaim" ->
              \* issue_service release: projected assignee must equal actor.
              IF Pass(m) /\
                 (IF LockOnlyRelease
                    THEN issue[m.i].holder = ActorOf(m.a)
                    ELSE HolderOk(m) /\ issue[m.i].asg = ActorOf(m.a))
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "open",
                                   asg |-> IF LockOnlyRelease THEN @.asg ELSE NoOne,
                                   holder |-> IF @.holder = ActorOf(m.a) THEN NoOne ELSE @.holder,
                                   lexp |-> @.lexp, who |-> NoOne]]
                     /\ staleW' = staleW \cup Stale(m)
                          \cup (IF LockOnlyRelease /\ ~ReleaseAssignment
                                 THEN {"assigned_leak"} ELSE {})
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "relassign" ->
              IF Pass(m) /\ issue[m.i].asg = ActorOf(m.a)
                THEN /\ issue' = [issue EXCEPT ![m.i].asg = NoOne]
                     /\ UNCHANGED <<lease, sess, staleW, foreignW, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "reset" ->
              \* resetTask Update (decided on the supervisor's earlier read)
              \* plus release of the lock by actor. FleetDB UpdateIssue
              \* re-reads the issue and rejects edits to a closed issue
              \* (issue_service.go:371-404); that service read is modelled as
              \* atomic with the append (the read/append race is not modelled).
              IF Pass(m) /\ HolderOk(m) /\ issue[m.i].st # "closed"
                 /\ (~RespectDeferred \/ issue[m.i].st # "deferred")
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "open",
                                   asg |-> NoOne,
                                   holder |-> IF @.holder = ActorOf(m.a) THEN NoOne ELSE @.holder,
                                   lexp |-> @.lexp, who |-> NoOne]]
                     /\ staleW' = staleW \cup Stale(m)
                          \cup (IF issue[m.i].st = "deferred"
                                 THEN {"deferred_lost"} ELSE {})
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "srun" ->
              IF SessPass(m) /\ (~TerminalGuard \/ sess[m.sid] \notin Terminal)
                THEN /\ sess' = [sess EXCEPT ![m.sid] = "running"]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ UNCHANGED <<lease, issue, foreignW, term, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
         [] m.k = "sfin" ->
              IF SessPass(m) /\ (~TerminalGuard \/ sess[m.sid] \notin Terminal)
                THEN /\ sess' = [sess EXCEPT ![m.sid] = "completed"]
                     /\ term' = term \cup {m.sid}
                     /\ staleW' = staleW \cup Stale(m)
                          \cup (IF "superseded" \in Stale(m)
                                 THEN {"superseded_finalize"} ELSE {})
                     /\ UNCHANGED <<lease, issue, foreignW, sagent>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, sagent>>
    /\ UNCHANGED <<hb, now, nextFence, up, inc, ph, proc, my>>

-----------------------------------------------------------------------------
Next ==
    \/ Tick
    \/ \E h \in Hosts :
         \/ Crash(h) \/ Restart(h)
         \/ \E a \in Agents :
              \/ Acquire(h, a) \/ HbOk(h, a) \/ HbLostReply(h, a)
              \/ HbTypedFail(h, a) \/ KillUnverifiable(h, a)
              \/ NoWork(h, a) \/ WorkerHb(h, a) \/ ReconcileSessions(h, a)
              \/ Spawn(h, a) \/ AgentWrite(h, a) \/ AgentClose(h, a)
              \/ ProcExit(h, a) \/ Finalize(h, a) \/ ReleaseOwn(h, a)
              \/ HookSetStatus(h, a) \/ AgentReview(h, a)
              \/ \E i \in Issues : Claim(h, a, i) \/ ClaimPreflight(h, a, i)
    \/ \E i \in Issues : HumanStatus(i, "closed")
                          \/ HumanStatus(i, "deferred") \/ RecoverReviewIssue(i)
    \/ \E m \in net : Deliver(m)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Properties *)

TypeOK ==
    /\ now \in 0..MaxTime
    /\ staleW \subseteq {"superseded", "released", "expired", "superseded_finalize",
                         "deferred_lost", "assigned_leak", "review_crashed",
                         "review_stranded"}
    /\ nextFence \in 0..MaxFence
    /\ \A a \in Agents : lease[a].owner \in Owners \cup {NoOne}
    /\ \A h \in Hosts, a \in Agents :
          /\ ph[h][a] \in Phases
          /\ hb[h][a] \in BOOLEAN
          /\ proc[h][a] \in AliveP \cup {"none"}
    /\ \A i \in Issues : issue[i].st \in {"open", "in_progress", "closed",
                                         "deferred", "review"}
    /\ \A s \in Sids : sess[s] \in {"none", "starting", "running", "completed", "failed"}
    /\ \A s \in Sids : sagent[s] \in Agents \cup {NoOne}

\* A1 (contract P2): FleetDB never applies an agent-scoped write (issue,
\* session, abstract stage-1 write, claim) unless the writer's attempt is the
\* agent's current attempt and the ownership lease is active and unexpired;
\* and never applies a release from a superseded attempt.
NoStaleAttemptWrite == staleW = {}
NoSupersededWrite   == "superseded" \notin staleW
NoWriteAfterRelease == "released" \notin staleW
NoWriteAfterExpiry  == "expired" \notin staleW
\* Session completion (sfin) applied after another attempt acquired the agent.
NoSupersededFinalize == "superseded_finalize" \notin staleW

\* A2: at most one live agent process per logical agent across hosts.
\* Expected to depend on timing (Drift, Pause, Grace, PgSkew) and crashes.
SingleLiveProcess ==
    \A a \in Agents : Cardinality({h \in Hosts : proc[h][a] \in AliveP}) <= 1

\* B1: no issue write lands on another attempt's live claim, and no reset
\* reopens a closed issue.
NoForeignIssueWrite == ~foreignW
NoDeferredStatusLoss == "deferred_lost" \notin staleW
NoAssignedClaimLeak == "assigned_leak" \notin staleW
NoStrandedReview == "review_stranded" \notin staleW

\* B2: at most one supervised process works each issue (timing dependent).
NoDoubleWork ==
    \A i \in Issues :
        Cardinality({<<h, a>> \in Hosts \X Agents :
                       proc[h][a] \in {"run", "stop"} /\ my[h][a].iss = i}) <= 1

\* C1: a finalized session keeps its terminal status.
TerminalOnce == \A s \in term : sess[s] \in Terminal

\* Liveness probe for contract P3 ("each session reaches one terminal
\* status"), stated as a state predicate: a session that started is still
\* non-terminal, no completion request for it is in flight, and no
\* supervisor still holds that attempt, so nothing in the model will ever
\* finalize it. Expected to be VIOLATED even in the proposed design: the
\* active-lease predicate rejects a completion delivered after release, and
\* a crash drops the attempt. The model has no retry or ack-before-release;
\* ReconcileSessions (Reconcile = TRUE) only reaches sessions of earlier
\* attempts when the agent acquires again, so terminal liveness is NOT
\* established here (see NoStrandedBeforeClaim for what it does establish).
NoStrandedSession ==
    \A s \in Sids :
        ~ /\ sess[s] \in {"starting", "running"}
          /\ \A m \in net : ~(m.k = "sfin" /\ m.sid = s)
          /\ \A h \in Hosts, a \in Agents : my[h][a].sid # s

\* C2 (open #396, safety form of P3 for earlier attempts): once an attempt
\* of agent a holds a claim, no session a started in an earlier attempt is
\* still unfinished. Covers both strand routes: a daemon crash and a
\* completion rejected after release. The session of the agent's final
\* attempt is not covered; NoStrandedSession above still shows that gap.
NoStrandedBeforeClaim ==
    \A h \in Hosts, a \in Agents :
        ph[h][a] \in {"claimed", "running", "exited"} =>
            \A s \in Sids : (sagent[s] = a /\ s < my[h][a].sid) => sess[s] \notin Unfinished

\* Vacuity probes: these SHOULD be violated (the protocol makes progress).
NeverClosed == \A i \in Issues : issue[i].st # "closed"
NeverFinalized == term = {}

=============================================================================
