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
(*                                                                         *)
(* Time is abstract ticks of the FleetDB Go clock. A daemon may believe   *)
(* its lease is valid for up to Drift ticks past the server expiry (slow   *)
(* local clock), may be paused for up to Pause ticks, and its agent may   *)
(* survive SIGTERM for up to Grace ticks. PgSkew lets the Postgres acquire *)
(* compare expiry against database NOW() running ahead of the Go clock.   *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Hosts, Agents, Issues, NoOne,
          STAGE, TTL, LockTTL, MaxTime, Drift, Pause, Grace, PgSkew,
          MaxFence, MaxInFlight, MaxInc,
          WriteCheck, HolderCheck, TerminalGuard, GuardedSpawn, SessionCheck

ASSUME /\ STAGE \in {1, 2, 3}
       /\ WriteCheck \in {"none", "token", "fence_eq", "fence"}
       /\ SessionCheck \in BOOLEAN
       /\ HolderCheck \in BOOLEAN /\ TerminalGuard \in BOOLEAN
       /\ GuardedSpawn \in BOOLEAN
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
    term        \* ghost: session ids that were finalized

vars == <<now, lease, nextFence, issue, sess, net, up, inc, ph, proc, my, hb,
          staleW, foreignW, term>>

Owners   == Hosts \X (0..MaxInc)
Sids     == 1..MaxFence
Phases   == {"idle", "owned", "claimed", "running", "exited", "recovered"}
AliveP   == {"run", "stop", "orphan"}
Terminal == {"completed"}

NoLease == [owner |-> NoOne, tok |-> 0, fence |-> 0, sid |-> 0, exp |-> 0,
            active |-> FALSE]
NoAtt   == [tok |-> 0, fence |-> 0, sid |-> 0, iss |-> NoOne, sent |-> 0,
            closed |-> FALSE]
NoIssue == [st |-> "open", asg |-> NoOne, holder |-> NoOne, lexp |-> 0]

Symm == Permutations(Hosts) \cup Permutations(Agents) \cup Permutations(Issues)

-----------------------------------------------------------------------------
(* Helpers *)

Owner(h) == <<h, inc[h]>>
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
\* A release only ends a lease; releasing one's own expired lease is benign.
StaleRel(m) == IF m.sid # lease[m.a].sid THEN {"superseded"} ELSE {}

\* Ghost: the issue write lands on a claim held by another agent or by a
\* newer attempt of the same agent.
Foreign(m) ==
    \/ /\ issue[m.i].st = "in_progress"
       /\ (issue[m.i].asg # m.a \/ m.sid # lease[m.a].sid)
    \/ /\ issue[m.i].st = "closed"
       /\ m.k = "reset"

HolderOk(m) == ~HolderCheck \/ (issue[m.i].st = "in_progress" /\ issue[m.i].asg = m.a)

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
                   staleW, foreignW, term>>

\* Daemon crash: supervisor state is lost; children become orphans.
Crash(h) ==
    /\ up[h] /\ inc[h] < MaxInc
    /\ up' = [up EXCEPT ![h] = FALSE]
    /\ proc' = [proc EXCEPT ![h] = [a \in Agents |->
                  IF proc[h][a] \in {"run", "stop"} THEN "orphan" ELSE proc[h][a]]]
    /\ ph' = [ph EXCEPT ![h] = [a \in Agents |-> "idle"]]
    /\ my' = [my EXCEPT ![h] = [a \in Agents |-> NoAtt]]
    /\ hb' = [hb EXCEPT ![h] = [a \in Agents |-> FALSE]]
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, net, inc, staleW, foreignW, term>>

\* Restart with a new OwnerID; the startup orphan sweep kills old children.
Restart(h) ==
    /\ ~up[h]
    /\ up' = [up EXCEPT ![h] = TRUE]
    /\ inc' = [inc EXCEPT ![h] = @ + 1]
    /\ proc' = [proc EXCEPT ![h] = [a \in Agents |->
                  IF proc[h][a] = "orphan" THEN "none" ELSE proc[h][a]]]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, ph, my, staleW, foreignW, term>>

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
    /\ UNCHANGED <<now, issue, sess, net, up, inc, proc, staleW, foreignW, term>>

\* T2 heartbeat success: sent-time becomes the local validity anchor.
HbOk(h, a) ==
    /\ up[h] /\ hb[h][a] /\ TokenOk(h, a)
    /\ lease' = [lease EXCEPT ![a].exp = now + TTL]
    /\ my' = [my EXCEPT ![h][a].sent = now, ![h][a].fence = lease[a].fence]
    /\ UNCHANGED <<hb, now, nextFence, issue, sess, net, up, inc, ph, proc,
                   staleW, foreignW, term>>

\* Delayed/lost reply: the server renewed, the daemon cannot tell (rides out).
HbLostReply(h, a) ==
    /\ up[h] /\ hb[h][a] /\ TokenOk(h, a)
    /\ lease' = [lease EXCEPT ![a].exp = now + TTL]
    /\ UNCHANGED <<hb, now, nextFence, issue, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term>>

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
    /\ UNCHANGED <<now, issue, sess, net, up, inc, ph, staleW, foreignW, term>>

\* continueOwnershipIfWithinValidity gives up once local elapsed >= TTL.
\* The latest point is enforced by Tick through DeadBy.
\* Before spawn there is nothing to kill: the loop still ends (StopAgent is a
\* no-op without a process, health.go:30-38).
KillUnverifiable(h, a) ==
    /\ up[h] /\ hb[h][a] /\ now >= my[h][a].sent + TTL
    /\ proc' = [proc EXCEPT ![h][a] = IF @ = "run" THEN "stop" ELSE @]
    /\ hb' = [hb EXCEPT ![h][a] = FALSE]
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, net, up, inc, ph, my,
                   staleW, foreignW, term>>

-----------------------------------------------------------------------------
(* Issue claim: supervisor/claim.go; fleet-db service/issue_service.go.    *)

\* T5 ClaimTask (atomic lock + projection write in the model). A same-actor
\* reclaim is idempotent; an expired lock allows a stale takeover.
Claim(h, a, i) ==
    /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "owned"
    /\ LET I == issue[i]
           lockFree == I.holder = NoOne \/ I.lexp <= now
       IN /\ I.st # "closed"
          /\ lockFree \/ I.holder = a
          /\ I.st = "open" \/ I.asg = a \/ lockFree
    /\ PassNow(h, a)
    /\ issue' = [issue EXCEPT ![i] = [st |-> "in_progress", asg |-> a,
                                      holder |-> a, lexp |-> now + LockTTL]]
    /\ my' = [my EXCEPT ![h][a].iss = i]
    /\ ph' = [ph EXCEPT ![h][a] = "claimed"]
    /\ staleW' = staleW \cup StaleKinds(a, my[h][a].sid)
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, proc, foreignW, term>>

\* Preflight found no work or was gated (supervisor.go:320-330): go straight
\* to ownership release. postExitCleanup is an empty hook (:830-833).
NoWork(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ ph[h][a] = "owned"
    /\ ph' = [ph EXCEPT ![h][a] = "recovered"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, up, inc, proc, my,
                   staleW, foreignW, term>>

\* Supervisor worker heartbeat during cmd.Wait(): renews the lock to LockTTL
\* when the holder name matches; ownership_lost is ignored (HTTP 200).
WorkerHb(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ my[h][a].iss # NoOne
    /\ issue[my[h][a].iss].holder = a
    /\ PassNow(h, a)
    /\ issue' = [issue EXCEPT ![my[h][a].iss].lexp = now + LockTTL]
    /\ UNCHANGED <<hb, now, lease, nextFence, sess, net, up, inc, ph, proc, my,
                   staleW, foreignW, term>>

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
              /\ Send({Msg("srun", h, a, NoOne)})       \* status -> running
         ELSE UNCHANGED <<sess, net>>
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, up, inc, my,
                   staleW, foreignW, term>>

\* Stage 1: an abstract agent-scoped write sent through daemon IPC.
AgentWrite(h, a) ==
    /\ STAGE = 1
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ Send({Msg("aw", h, a, NoOne)})
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, ph, proc, my,
                   staleW, foreignW, term>>

\* Stage 2+: the agent closes its issue through IPC (update/close do not
\* check the claim in FleetDB).
AgentClose(h, a) ==
    /\ STAGE >= 2
    /\ up[h] /\ proc[h][a] \in {"run", "stop"}
    /\ my[h][a].iss # NoOne /\ ~my[h][a].closed
    /\ Send({Msg("close", h, a, my[h][a].iss)})
    /\ my' = [my EXCEPT ![h][a].closed = TRUE]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, ph, proc,
                   staleW, foreignW, term>>

ProcExit(h, a) ==
    /\ proc[h][a] \in {"run", "stop"}
    /\ proc' = [proc EXCEPT ![h][a] = "none"]
    /\ ph' = [ph EXCEPT ![h][a] = "exited"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, net, up, inc, my,
                   staleW, foreignW, term>>

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
           R   == {i \in Issues :
                     \/ i = my[h][a].iss /\ issue[i].st # "closed"
                     \/ issue[i].st = "in_progress" /\ issue[i].asg = a}
           rs  == IF STAGE >= 2 THEN {Msg("reset", h, a, i) : i \in R}
                               ELSE {Msg("aw", h, a, NoOne)}
       IN Send(fin \cup rc \cup rs)
    /\ ph' = [ph EXCEPT ![h][a] = "recovered"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, proc, my,
                   staleW, foreignW, term>>

\* T14 releaseOwnership after spawnAndWait returns (supervisor.go:337-341):
\* stop the heartbeat, then send Release(token).
ReleaseOwn(h, a) ==
    /\ up[h] /\ ph[h][a] = "recovered"
    /\ Send({Msg("rel", h, a, NoOne)})
    /\ ph' = [ph EXCEPT ![h][a] = "idle"]
    /\ hb' = [hb EXCEPT ![h][a] = FALSE]
    /\ my' = [my EXCEPT ![h][a] = NoAtt]
    /\ UNCHANGED <<now, lease, nextFence, issue, sess, up, inc, proc,
                   staleW, foreignW, term>>

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
                     /\ UNCHANGED <<issue, sess, foreignW, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "aw" ->
              IF Pass(m)
                THEN /\ staleW' = staleW \cup Stale(m)
                     /\ UNCHANGED <<lease, issue, sess, foreignW, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "close" ->
              IF Pass(m) /\ HolderOk(m) /\ issue[m.i].st # "closed"
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "closed",
                                   asg |-> NoOne, holder |-> NoOne, lexp |-> 0]]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "relclaim" ->
              \* issue_service release: projected assignee must equal actor.
              IF Pass(m) /\ HolderOk(m) /\ issue[m.i].asg = m.a
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "open",
                                   asg |-> NoOne,
                                   holder |-> IF @.holder = m.a THEN NoOne ELSE @.holder,
                                   lexp |-> @.lexp]]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "reset" ->
              \* resetTask Update (decided on the supervisor's earlier read)
              \* plus release of the lock by actor. FleetDB UpdateIssue
              \* re-reads the issue and rejects edits to a closed issue
              \* (issue_service.go:371-404); that service read is modelled as
              \* atomic with the append (the read/append race is not modelled).
              IF Pass(m) /\ HolderOk(m) /\ issue[m.i].st # "closed"
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "open",
                                   asg |-> NoOne,
                                   holder |-> IF @.holder = m.a THEN NoOne ELSE @.holder,
                                   lexp |-> @.lexp]]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ UNCHANGED <<lease, sess, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "srun" ->
              IF SessPass(m) /\ (~TerminalGuard \/ sess[m.sid] \notin Terminal)
                THEN /\ sess' = [sess EXCEPT ![m.sid] = "running"]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ UNCHANGED <<lease, issue, foreignW, term>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
         [] m.k = "sfin" ->
              IF SessPass(m) /\ (~TerminalGuard \/ sess[m.sid] \notin Terminal)
                THEN /\ sess' = [sess EXCEPT ![m.sid] = "completed"]
                     /\ term' = term \cup {m.sid}
                     /\ staleW' = staleW \cup Stale(m)
                          \cup (IF "superseded" \in Stale(m)
                                 THEN {"superseded_finalize"} ELSE {})
                     /\ UNCHANGED <<lease, issue, foreignW>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term>>
    /\ UNCHANGED <<hb, now, nextFence, up, inc, ph, proc, my>>

-----------------------------------------------------------------------------
Next ==
    \/ Tick
    \/ \E h \in Hosts :
         \/ Crash(h) \/ Restart(h)
         \/ \E a \in Agents :
              \/ Acquire(h, a) \/ HbOk(h, a) \/ HbLostReply(h, a)
              \/ HbTypedFail(h, a) \/ KillUnverifiable(h, a)
              \/ NoWork(h, a) \/ WorkerHb(h, a)
              \/ Spawn(h, a) \/ AgentWrite(h, a) \/ AgentClose(h, a)
              \/ ProcExit(h, a) \/ Finalize(h, a) \/ ReleaseOwn(h, a)
              \/ \E i \in Issues : Claim(h, a, i)
    \/ \E m \in net : Deliver(m)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(* Properties *)

TypeOK ==
    /\ now \in 0..MaxTime
    /\ staleW \subseteq {"superseded", "released", "expired", "superseded_finalize"}
    /\ nextFence \in 0..MaxFence
    /\ \A a \in Agents : lease[a].owner \in Owners \cup {NoOne}
    /\ \A h \in Hosts, a \in Agents :
          /\ ph[h][a] \in Phases
          /\ hb[h][a] \in BOOLEAN
          /\ proc[h][a] \in AliveP \cup {"none"}
    /\ \A i \in Issues : issue[i].st \in {"open", "in_progress", "closed"}
    /\ \A s \in Sids : sess[s] \in {"none", "starting", "running", "completed"}

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
\* a crash drops the attempt. The model has no retry, ack-before-release,
\* or reaper action, so terminal liveness is NOT established here.
NoStrandedSession ==
    \A s \in Sids :
        ~ /\ sess[s] \in {"starting", "running"}
          /\ \A m \in net : ~(m.k = "sfin" /\ m.sid = s)
          /\ \A h \in Hosts, a \in Agents : my[h][a].sid # s

\* Vacuity probes: these SHOULD be violated (the protocol makes progress).
NeverClosed == \A i \in Issues : issue[i].st # "closed"
NeverFinalized == term = {}

=============================================================================
