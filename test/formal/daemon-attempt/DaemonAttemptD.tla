---------------------------- MODULE DaemonAttemptD ----------------------------
(***************************************************************************)
(* Stage d of the daemon attempt model: session finalization liveness and *)
(* the session lease (enforcement map section 3, gaps L1 and L2).         *)
(*                                                                         *)
(* This module extends DaemonAttempt (stage 3, sessions) without changing  *)
(* it. It adds the proposed rule F and the proposed session-lease binding: *)
(*   - FinalizeD writes a durable pending-finalize record instead of       *)
(*     sending the completion inline (session_finalize.go:145-159 today   *)
(*     clears the ids first and loses the retry).                          *)
(*   - DurableRetry re-sends the completion while a record exists, also    *)
(*     after ownership release and across restarts. A crash keeps the      *)
(*     record unless DurableRecord = FALSE.                                *)
(*   - FleetDB applies the completion as a terminal CAS authorized by a    *)
(*     session-scoped finalize credential (Credential) instead of live     *)
(*     ownership. With Coerce, a completion applied while the session's    *)
(*     bound ownership lease is not live is stored as "failed".            *)
(*   - Reap: a FleetDB reaper fails a nonterminal session once its bound   *)
(*     ownership lease and its session lease are both dead and ReapGrace   *)
(*     has passed; it runs at clock multiples of ReapPeriod.               *)
(*   - Session lease slease: created at spawn, bound to the ownership fence*)
(*     of the attempt. SLeaseRenew = "joint" renews it in the ownership    *)
(*     heartbeat (same server request); "ipc" renews it only when an IPC  *)
(*     write is applied (today, daemon_ipc.go:435). Expiry is derived     *)
(*     (now >= lexp), like ownership expiry; the terminal CAS and the     *)
(*     reaper deactivate it.                                               *)
(*   - IpcCheck selects what FleetDB checks on IPC-routed writes (the     *)
(*     agent's issue close): "bound" = G1 plus a live session lease bound *)
(*     to the current fence (proposed); "lease_only" = a live session     *)
(*     lease only (today's IPC path); "g1_only" = G1 without the session  *)
(*     lease (IPC check skipped, daemon_ipc.go:424, or a direct write).   *)
(*                                                                         *)
(* Finite horizon. Ownership acquire and renewal stop after RenewUntil, so *)
(* every attempt ends inside the model. Tick stops at MaxTime, so liveness *)
(* is meaningful only if MaxTime leaves room for the last lease to lapse,  *)
(* the reaper grace to pass and one reaper run; ASSUME ClockBoundOK makes  *)
(* TLC reject any stage-d config that does not satisfy it. With           *)
(* RenewUntil = 0 the bound is the approved                               *)
(*   MaxTime >= TTL + Drift + Pause + Grace + ReapGrace + ReapPeriod.     *)
(*                                                                         *)
(* Fairness (SpecD). Required by the plan: weak fairness on delivery (any *)
(* message, and per session for completions), DurableRetry, Reap and      *)
(* Tick. Also needed because Tick's guard encodes the kill deadline: weak  *)
(* fairness on KillUnverifiable, on the forced exit of a stopped process   *)
(* (SIGKILL after grace) and on FinalizeD while the daemon is up. Restart *)
(* is fair only when FairRestart = TRUE (control configs); the proposed    *)
(* design must not depend on a daemon coming back.                         *)
(***************************************************************************)
EXTENDS DaemonAttempt

CONSTANTS Credential, Coerce, DurableRecord, Reaper, ReapGrace, ReapPeriod,
          RenewUntil, SLeaseTTL, SLeaseRenew, IpcCheck, FairRestart

Max(x, y) == IF x >= y THEN x ELSE y

ClockBound == RenewUntil + Max(TTL, SLeaseTTL) + Drift + Pause + Grace
              + ReapGrace + ReapPeriod

ASSUME /\ STAGE = 3
       /\ Credential \in BOOLEAN /\ Coerce \in BOOLEAN
       /\ DurableRecord \in BOOLEAN /\ Reaper \in BOOLEAN
       /\ FairRestart \in BOOLEAN
       /\ SLeaseRenew \in {"joint", "ipc"}
       /\ IpcCheck \in {"bound", "lease_only", "g1_only"}
       /\ ReapGrace \in Nat /\ RenewUntil \in Nat
       /\ ReapPeriod \in Nat \ {0} /\ SLeaseTTL \in Nat \ {0}

ClockBoundOK == MaxTime >= ClockBound
ASSUME ClockBoundOK

VARIABLES
    pend,    \* durable pending-finalize records per host (daemon state dir)
    sd,      \* server session data: bound agent/fence, ownership exp, slease
    tval,    \* ghost: first terminal status stored per session
    reaped,  \* ghost: sessions the reaper finalized
    staleC,  \* ghost: "completed" stored while the attempt was not live
    slw      \* ghost: an IPC write applied without a live session lease

dvars == <<pend, sd, tval, reaped, staleC, slw>>
varsD == <<vars, dvars>>

Live      == {"starting", "running"}
TerminalD == {"completed", "failed"}

NoSD == [agent |-> NoOne, fence |-> 0, oexp |-> 0, lact |-> FALSE, lexp |-> 0]
Recs == [sid : Sids, a : Agents, fence : 0..MaxFence, tok : 0..MaxFence]

-----------------------------------------------------------------------------
(* Helpers *)

RenewOpen == now <= RenewUntil

\* The session's bound ownership lease (the fence it was created under) is
\* active and unexpired on the FleetDB clock.
OwnLiveFor(s) ==
    LET a == sd[s].agent
    IN /\ a # NoOne
       /\ lease[a].active /\ now < lease[a].exp
       /\ lease[a].fence = sd[s].fence

SLeaseLive(s) == sd[s].lact /\ now < sd[s].lexp

\* Server side of an ownership renewal to now + TTL for agent a, carrying the
\* session lease id of the daemon's attempt (sid s).
RenewSD(s, a) ==
    IF /\ s \in Sids
       /\ sd[s].agent = a /\ sd[s].fence = lease[a].fence
       /\ sess[s] \in Live
      THEN [sd EXCEPT ![s].oexp = now + TTL,
                      ![s].lexp = IF SLeaseRenew = "joint" /\ SLeaseLive(s)
                                    THEN Max(@, now + TTL) ELSE @]
      ELSE sd

\* A delivered completion whose session is terminal afterwards (200, or 409
\* terminal) acknowledges the record on every daemon that is up to read the
\* reply; a daemon that is down keeps it and retries after restart.
AckPend(sid) == [h \in Hosts |-> IF up[h] THEN {r \in pend[h] : r.sid # sid}
                                          ELSE pend[h]]

FinMsg(r) == [k |-> "sfin", a |-> r.a, i |-> NoOne, sid |-> r.sid,
              fence |-> r.fence, tok |-> r.tok]

\* Terminal CAS authorization: the finalize credential (bound to the session,
\* independent of ownership) or, in the mutation, live ownership (G1).
FinAuth(m) == IF Credential THEN TRUE ELSE Pass(m)

IpcOk(m) ==
    CASE IpcCheck = "bound"      -> /\ Pass(m) /\ SLeaseLive(m.sid)
                                    /\ sd[m.sid].fence = lease[m.a].fence
      [] IpcCheck = "lease_only" -> SLeaseLive(m.sid)
      [] IpcCheck = "g1_only"    -> Pass(m)

Reapable(s) ==
    /\ Reaper
    /\ sess[s] \in Live
    /\ ~OwnLiveFor(s)
    /\ ~SLeaseLive(s)
    /\ now >= Max(sd[s].oexp, sd[s].lexp) + ReapGrace
    /\ now % ReapPeriod = 0

-----------------------------------------------------------------------------
InitD ==
    /\ Init
    /\ pend = [h \in Hosts |-> {}]
    /\ sd = [s \in Sids |-> NoSD]
    /\ tval = [s \in Sids |-> "none"]
    /\ reaped = {}
    /\ staleC = FALSE
    /\ slw = FALSE

-----------------------------------------------------------------------------
(* Stage-3 actions reused, with the stage-d horizon and session data. *)

\* The reaper runs at every multiple of ReapPeriod before the clock moves on.
TickD ==
    /\ Tick
    /\ ~\E s \in Sids : Reapable(s)
    /\ UNCHANGED dvars

CrashD(h) ==
    /\ Crash(h)
    /\ pend' = [pend EXCEPT ![h] = IF DurableRecord THEN @ ELSE {}]
    /\ UNCHANGED <<sd, tval, reaped, staleC, slw>>

RestartD(h)            == Restart(h) /\ UNCHANGED dvars
AcquireD(h, a)         == RenewOpen /\ Acquire(h, a) /\ UNCHANGED dvars
KillUnverifiableD(h, a) == KillUnverifiable(h, a) /\ UNCHANGED dvars
NoWorkD(h, a)          == NoWork(h, a) /\ UNCHANGED dvars
WorkerHbD(h, a)        == WorkerHb(h, a) /\ UNCHANGED dvars
AgentCloseD(h, a)      == AgentClose(h, a) /\ UNCHANGED dvars
ProcExitD(h, a)        == ProcExit(h, a) /\ UNCHANGED dvars
ReleaseOwnD(h, a)      == ReleaseOwn(h, a) /\ UNCHANGED dvars
ClaimD(h, a, i)        == Claim(h, a, i) /\ UNCHANGED dvars

\* SIGKILL after the grace period: a stopped process eventually exits.
ForcedExit(h, a) == proc[h][a] = "stop" /\ ProcExitD(h, a)

\* Joint renewal: the heartbeat carries the session lease id (my sid).
HbOkD(h, a) ==
    /\ RenewOpen
    /\ HbOk(h, a)
    /\ sd' = RenewSD(my[h][a].sid, a)
    /\ UNCHANGED <<pend, tval, reaped, staleC, slw>>

HbLostReplyD(h, a) ==
    /\ RenewOpen
    /\ HbLostReply(h, a)
    /\ sd' = RenewSD(my[h][a].sid, a)
    /\ UNCHANGED <<pend, tval, reaped, staleC, slw>>

\* After the horizon the reacquire branch (the only one that issues a fence)
\* is disabled; the other branches still stop the loop or kill.
HbTypedFailD(h, a) ==
    /\ HbTypedFail(h, a)
    /\ RenewOpen \/ nextFence' = nextFence
    /\ UNCHANGED dvars

\* Session create also creates the session lease, bound to the attempt's
\* fence. Joint mode starts it at no less than the ownership expiry.
SpawnD(h, a) ==
    /\ Spawn(h, a)
    /\ LET s  == my[h][a].sid
           oe == IF lease[a].fence = my[h][a].fence THEN lease[a].exp ELSE 0
       IN sd' = [sd EXCEPT ![s] =
                   [agent |-> a, fence |-> my[h][a].fence, oexp |-> oe,
                    lact |-> TRUE,
                    lexp |-> IF SLeaseRenew = "joint"
                               THEN Max(now + SLeaseTTL, oe)
                               ELSE now + SLeaseTTL]]
    /\ UNCHANGED <<pend, tval, reaped, staleC, slw>>

-----------------------------------------------------------------------------
(* Rule F on the Loom side. *)

\* T12/T13 with a durable record: the completion is not sent inline; the
\* record is written first and DurableRetry sends it. Claim release and
\* post-mortem resets are sent as in stage 3.
FinalizeD(h, a) ==
    /\ up[h] /\ ph[h][a] = "exited" /\ proc[h][a] = "none"
    /\ LET rc == IF my[h][a].iss # NoOne
                   THEN {Msg("relclaim", h, a, my[h][a].iss)} ELSE {}
           R  == {i \in Issues :
                    \/ i = my[h][a].iss /\ issue[i].st # "closed"
                    \/ issue[i].st = "in_progress" /\ issue[i].asg = a}
           rs == {Msg("reset", h, a, i) : i \in R}
       IN Send(rc \cup rs)
    /\ pend' = [pend EXCEPT ![h] = @ \cup
                  {[sid |-> my[h][a].sid, a |-> a, fence |-> my[h][a].fence,
                    tok |-> my[h][a].tok]}]
    /\ ph' = [ph EXCEPT ![h][a] = "recovered"]
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, proc, my,
                   staleW, foreignW, term, sd, tval, reaped, staleC, slw>>

\* Re-send while the record exists and no copy is in flight (the previous
\* send was rejected, or its reply was lost to a crash). Needs no ownership.
\* The record is dropped when a reply reports the session terminal (see
\* AckPend in DeliverS).
DurableRetry(h, r) ==
    /\ up[h] /\ r \in pend[h]
    /\ FinMsg(r) \notin net
    /\ Send({FinMsg(r)})
    /\ UNCHANGED <<hb, now, lease, nextFence, issue, sess, up, inc, ph, proc,
                   my, staleW, foreignW, term, dvars>>

-----------------------------------------------------------------------------
(* Rule F on the FleetDB side. *)

Reap(s) ==
    /\ Reapable(s)
    /\ sess' = [sess EXCEPT ![s] = "failed"]
    /\ term' = term \cup {s}
    /\ tval' = [tval EXCEPT ![s] = IF @ = "none" THEN "failed" ELSE @]
    /\ sd' = [sd EXCEPT ![s].lact = FALSE]
    /\ reaped' = reaped \cup {s}
    /\ UNCHANGED <<now, lease, nextFence, issue, net, up, inc, ph, proc, my,
                   hb, staleW, foreignW, pend, staleC, slw>>

\* Session and IPC-routed writes under stage d. Other kinds as in stage 3.
DeliverS(m) ==
    /\ net' = net \ {m}
    /\ CASE m.k = "srun" ->
              IF SessPass(m) /\ (~TerminalGuard \/ sess[m.sid] \notin TerminalD)
                THEN /\ sess' = [sess EXCEPT ![m.sid] = "running"]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ UNCHANGED <<lease, issue, foreignW, term, dvars>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, dvars>>
         [] m.k = "close" ->
              IF IpcOk(m) /\ HolderOk(m) /\ issue[m.i].st # "closed"
                THEN /\ issue' = [issue EXCEPT ![m.i] = [st |-> "closed",
                                   asg |-> NoOne, holder |-> NoOne, lexp |-> 0]]
                     /\ staleW' = staleW \cup Stale(m)
                     /\ foreignW' = (foreignW \/ Foreign(m))
                     /\ slw' = (slw \/ ~SLeaseLive(m.sid))
                     /\ sd' = IF SLeaseRenew = "ipc"
                                THEN [sd EXCEPT ![m.sid].lexp = Max(@, now + SLeaseTTL)]
                                ELSE sd
                     /\ UNCHANGED <<lease, sess, term, pend, tval, reaped, staleC>>
                ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, dvars>>
         [] m.k = "sfin" ->
              \* Terminal CAS. The finalize credential authorizes only this
              \* transition; it is not an agent-scoped write, so it does not
              \* enter staleW (the tightly scoped post-expiry exception).
              LET st == IF Coerce /\ ~OwnLiveFor(m.sid) THEN "failed"
                                                        ELSE "completed"
              IN IF FinAuth(m) /\ (~TerminalGuard \/ sess[m.sid] \in Live)
                   THEN /\ sess' = [sess EXCEPT ![m.sid] = st]
                        /\ term' = term \cup {m.sid}
                        /\ tval' = [tval EXCEPT ![m.sid] =
                                      IF @ = "none" THEN st ELSE @]
                        /\ sd' = [sd EXCEPT ![m.sid].lact = FALSE]
                        /\ staleC' = (staleC \/ (st = "completed" /\ Stale(m) # {}))
                        /\ pend' = AckPend(m.sid)
                        /\ UNCHANGED <<lease, issue, staleW, foreignW, reaped, slw>>
                 ELSE IF sess[m.sid] \in TerminalD
                   THEN /\ pend' = AckPend(m.sid)          \* 409 terminal
                        /\ UNCHANGED <<lease, issue, sess, staleW, foreignW, term,
                                       sd, tval, reaped, staleC, slw>>
                   ELSE UNCHANGED <<lease, issue, sess, staleW, foreignW, term, dvars>>
    /\ UNCHANGED <<hb, now, nextFence, up, inc, ph, proc, my>>

DeliverD(m) ==
    IF m.k \in {"srun", "close", "sfin"}
      THEN DeliverS(m)
      ELSE Deliver(m) /\ UNCHANGED dvars

-----------------------------------------------------------------------------
\* AgentWrite is stage 1 only and is omitted (disabled at STAGE = 3).
NextD ==
    \/ TickD
    \/ \E s \in Sids : Reap(s)
    \/ \E h \in Hosts :
         \/ CrashD(h) \/ RestartD(h)
         \/ \E r \in pend[h] : DurableRetry(h, r)
         \/ \E a \in Agents :
              \/ AcquireD(h, a) \/ HbOkD(h, a) \/ HbLostReplyD(h, a)
              \/ HbTypedFailD(h, a) \/ KillUnverifiableD(h, a)
              \/ NoWorkD(h, a) \/ WorkerHbD(h, a)
              \/ SpawnD(h, a) \/ AgentCloseD(h, a)
              \/ ProcExitD(h, a) \/ FinalizeD(h, a) \/ ReleaseOwnD(h, a)
              \/ \E i \in Issues : ClaimD(h, a, i)
    \/ \E m \in net : DeliverD(m)

Fairness ==
    /\ WF_varsD(TickD)
    /\ WF_varsD(\E m \in net : DeliverD(m))
    /\ \A s \in Sids :
         /\ WF_varsD(\E m \in net : m.k = "sfin" /\ m.sid = s /\ DeliverD(m))
         /\ WF_varsD(Reap(s))
         /\ \A h \in Hosts :
              WF_varsD(\E r \in pend[h] : r.sid = s /\ DurableRetry(h, r))
    /\ \A h \in Hosts, a \in Agents :
         /\ WF_varsD(KillUnverifiableD(h, a))
         /\ WF_varsD(ForcedExit(h, a))
         /\ WF_varsD(FinalizeD(h, a))
    /\ IF FairRestart THEN \A h \in Hosts : WF_varsD(RestartD(h)) ELSE TRUE

SpecD == InitD /\ [][NextD]_varsD /\ Fairness

-----------------------------------------------------------------------------
(* Properties *)

TypeOKD ==
    /\ now \in 0..MaxTime
    /\ nextFence \in 0..MaxFence
    /\ staleW \subseteq {"superseded", "released", "expired", "superseded_finalize"}
    /\ \A s \in Sids : sess[s] \in {"none"} \cup Live \cup TerminalD
    /\ \A s \in Sids : tval[s] \in {"none"} \cup TerminalD
    /\ \A h \in Hosts : pend[h] \subseteq Recs
    /\ reaped \subseteq Sids
    /\ staleC \in BOOLEAN /\ slw \in BOOLEAN

\* C1 extended: the first terminal status stored is never overwritten.
TerminalOnceD == \A s \in Sids : tval[s] # "none" => sess[s] = tval[s]

\* A session completion applied while its attempt was superseded, released
\* or expired is stored as "failed", never "completed".
NoStaleCompleted == ~staleC

\* L2 safety: while an attempt's ownership lease is live, the session lease
\* bound to that fence (and not yet released) lasts at least as long.
LiveOwnerKeepsSessionLease ==
    \A s \in Sids :
        LET a == sd[s].agent
        IN (/\ a # NoOne /\ sd[s].lact
            /\ GoLive(a) /\ lease[a].fence = sd[s].fence)
           => sd[s].lexp >= lease[a].exp

\* No IPC-routed write is applied without a live session lease.
NoWriteAfterSessionLeaseLoss == ~slw

\* Contract P3 (gap L1): every started session reaches a terminal status.
SessionTerminates == \A s \in Sids : sess[s] \in Live ~> sess[s] \in TerminalD

\* Vacuity probes: these SHOULD be violated.
NeverReaped    == reaped = {}
NeverCompleted == \A s \in Sids : sess[s] # "completed"

=============================================================================
