------------------------------- MODULE SSELive -------------------------------
EXTENDS Naturals, Sequences, FiniteSets

\* A one-client, three-event model of #626's actual handoff. TLC may schedule
\* each enabled action in any order. IDs are journal positions in one source
\* incarnation; they are not production FleetDB cursor encodings.
CONSTANTS Scenario, Legacy, Disable
N == 3
FastCap == 1
RetryCap == 1
ClientCap == 3

FollowPages == ~(Legacy /\ Scenario = "626a") /\ Disable # "page"
SyncRegister == ~(Legacy /\ Scenario = "626b") /\ Disable # "register"
Deduplicate == ~(Legacy /\ Scenario = "626c") /\ Disable # "dedup"
GuardConnected == ~(Legacy /\ Scenario = "626d") /\ Disable # "connected"
OrderAdmission == ~(Legacy /\ Scenario = "643") /\ Disable # "order"
SignalOverflow == Scenario = "612a" /\ ~Legacy /\ Disable # "overflow"

VARIABLE s
vars == <<s>>

Init ==
  s = [log |-> <<>>, opened |-> FALSE, registerPending |-> FALSE,
       registered |-> FALSE, disconnects |-> 0, replayCursor |-> 0,
       replayDone |-> FALSE, pageCount |-> 0, catchupError |-> FALSE,
       aborted |-> FALSE, connected |-> FALSE, subCursor |-> 0,
       fast |-> <<>>, retryQ |-> <<>>, clientBuf |-> <<>>,
       wire |-> <<>>, replayed |-> {}, applied |-> <<>>,
       checkpoint |-> 0, oldCheckpoint |-> 0, admitted |-> <<>>,
       delivered |-> <<>>, resync |-> FALSE, recovered |-> FALSE,
       recoveryCovered |-> {}]

\* FleetDB commit and subscriber delivery are separate. An event may commit
\* before open, during paged replay, or after replay but before hub admission.
Commit ==
  /\ Len(s.log) < N
  /\ s' = [s EXCEPT !.log = Append(@, Len(s.log) + 1)]

\* #626 registers under the hub mutex before it reads storage. v5 enqueues
\* registration for the hub loop, so a broadcast may overtake HubAddClient.
Open ==
  /\ ~s.opened
  /\ s.disconnects <= 1
  /\ s' = [s EXCEPT !.opened = TRUE,
                    !.registerPending = ~SyncRegister,
                    !.registered = SyncRegister,
                    !.replayCursor = s.checkpoint,
                    !.replayDone = FALSE,
                    !.pageCount = 0,
                    !.catchupError = FALSE,
                    !.aborted = FALSE,
                    !.connected = FALSE,
                    !.replayed = {},
                    !.clientBuf = <<>>,
                    !.wire = <<>>]

HubAddClient ==
  /\ s.opened /\ s.registerPending
  /\ s' = [s EXCEPT !.registerPending = FALSE, !.registered = TRUE]

\* The subscriber visits committed events in order. With a nonempty retry
\* queue, the fixed hub does not put a later event into the fast channel.
\* Only #612a explores a full-both-queues drop; other cases hold admission
\* until there is capacity, isolating their named handoff mechanisms.
FastAdmissible == Len(s.fast) < FastCap /\ (~OrderAdmission \/ Len(s.retryQ) = 0)
RetryAdmissible == Len(s.retryQ) < RetryCap
Broadcast ==
  /\ s.subCursor < Len(s.log)
  /\ Scenario = "612a" \/ FastAdmissible \/ RetryAdmissible
  /\ LET e == s.subCursor + 1
         fastPath == FastAdmissible
         retryPath == ~fastPath /\ RetryAdmissible
     IN s' = [s EXCEPT !.subCursor = e,
                       !.admitted = Append(@, e),
                       !.fast = IF fastPath THEN Append(@, e) ELSE @,
                       !.retryQ = IF retryPath THEN Append(@, e) ELSE @,
                       !.resync = IF ~fastPath /\ ~retryPath /\ SignalOverflow
                                   THEN TRUE ELSE @]

DrainRetry ==
  /\ Len(s.fast) < FastCap /\ Len(s.retryQ) > 0
  /\ s' = [s EXCEPT !.fast = Append(@, Head(s.retryQ)),
                    !.retryQ = Tail(@)]

HubFanOut ==
  /\ Len(s.fast) > 0
  /\ LET e == Head(s.fast)
         sent == s.registered /\ Len(s.clientBuf) < ClientCap
     IN s' = [s EXCEPT !.fast = Tail(@),
                       !.clientBuf = IF sent THEN Append(@, e) ELSE @,
                       !.delivered = IF sent THEN Append(@, e) ELSE @,
                       !.resync = IF s.registered /\ ~sent
                                   THEN TRUE ELSE @]

\* Page size is one. #626 follows all pages; the v5 mutation stops after
\* the first. replayed is exactly the handler's client.replayed cursor set.
CatchUpReadPage ==
  /\ s.opened /\ ~s.replayDone /\ ~s.catchupError
  /\ s.replayCursor < Len(s.log)
  /\ LET e == s.replayCursor + 1
     IN s' = [s EXCEPT !.replayCursor = e,
                       !.pageCount = @ + 1,
                       !.wire = Append(@, e),
                       !.replayed = @ \cup {e},
                       !.replayDone = (~FollowPages \/ e = Len(s.log))]

FinishCatchUp ==
  /\ s.opened /\ ~s.replayDone /\ ~s.catchupError
  /\ s.replayCursor = Len(s.log)
  /\ s' = [s EXCEPT !.replayDone = TRUE]

\* A backend read error cannot be reported as successful replay. The legacy
\* branch models the swallowed error that still permits connected.
CatchUpError ==
  /\ Scenario = "626d" /\ s.opened
  /\ ~s.replayDone /\ ~s.catchupError
  /\ s' = [s EXCEPT !.catchupError = TRUE,
                    !.aborted = GuardConnected,
                    !.replayDone = ~GuardConnected]

\* streamLoop starts only after catch-up. #626 suppresses any queued live
\* cursor already emitted by replay; legacy sends the overlap again.
WriteLive ==
  /\ s.opened /\ s.replayDone /\ Len(s.clientBuf) > 0
  /\ LET e == Head(s.clientBuf)
     IN s' = [s EXCEPT !.clientBuf = Tail(@),
                       !.wire = IF Deduplicate /\ e \in s.replayed
                                THEN @ ELSE Append(@, e)]

\* SSE frames are applied in wire order. A completed mutation frame advances
\* the browser checkpoint; a disconnected, unaccepted frame does not.
BrowserApply ==
  /\ s.opened /\ Len(s.wire) > 0
  /\ LET e == Head(s.wire)
     IN s' = [s EXCEPT !.wire = Tail(@),
                       !.applied = Append(@, e),
                       !.oldCheckpoint = s.checkpoint,
                       !.checkpoint = IF e > s.checkpoint THEN e ELSE @]

WriteConnected ==
  /\ s.opened /\ s.replayDone /\ ~s.connected /\ ~s.aborted
  /\ Len(s.wire) = 0
  /\ (~GuardConnected \/ ~s.catchupError)
  /\ s' = [s EXCEPT !.connected = TRUE]

Disconnect ==
  \* #626c checks catch-up/live overlap within one connection. A stale
  \* broadcast across reconnect is a separate uncovered behaviour.
  /\ Scenario # "626c"
  /\ s.opened /\ s.disconnects = 0
  /\ s' = [s EXCEPT !.opened = FALSE,
                    !.registerPending = FALSE,
                    !.registered = FALSE,
                    !.disconnects = 1,
                    !.replayDone = FALSE,
                    !.connected = FALSE,
                    !.clientBuf = <<>>,
                    !.wire = <<>>]

\* A resync is a fresh durable read, not another application of the queued
\* mutation frames. It replaces the browser view at a committed prefix.
Recover ==
  /\ s.resync /\ ~s.recovered /\ Len(s.log) = N
  /\ s' = [s EXCEPT !.recovered = TRUE,
                    !.recoveryCovered = {s.log[i] : i \in 1..Len(s.log)},
                    !.applied = <<>>,
                    !.oldCheckpoint = s.checkpoint,
                    !.checkpoint = Len(s.log),
                    !.replayCursor = Len(s.log),
                    !.replayDone = TRUE,
                    !.clientBuf = <<>>,
                    !.wire = <<>>,
                    !.replayed = {1, 2, 3}]

Next == Commit \/ Open \/ HubAddClient \/ Broadcast \/ DrainRetry \/
        HubFanOut \/ CatchUpReadPage \/ FinishCatchUp \/ CatchUpError \/
        WriteLive \/ BrowserApply \/ WriteConnected \/ Disconnect \/ Recover
Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

AppliedSet == {s.applied[i] : i \in 1..Len(s.applied)}
LogSet == {s.log[i] : i \in 1..Len(s.log)}
Quiescent ==
  /\ s.connected /\ Len(s.log) = N /\ s.subCursor = N
  /\ Len(s.fast) = 0 /\ Len(s.retryQ) = 0
  /\ Len(s.clientBuf) = 0 /\ Len(s.wire) = 0
  /\ ~s.registerPending
  /\ (~s.resync \/ s.recovered)

TypeOK ==
  /\ s.log \in Seq(1..N)
  /\ s.applied \in Seq(1..N)
  /\ s.replayCursor \in 0..N
  /\ s.checkpoint \in 0..N
  /\ s.disconnects \in 0..1
NoLostEvent == ~Quiescent \/ LogSet \subseteq (AppliedSet \cup s.recoveryCovered)
NoDoubleApply == \A i, j \in 1..Len(s.applied): i # j => s.applied[i] # s.applied[j]
AdmissionOrder == \A i, j \in 1..Len(s.delivered): i < j => s.delivered[i] < s.delivered[j]
CheckpointIsDurable == s.checkpoint \in 0..Len(s.log)
CheckpointMonotonic == s.checkpoint >= s.oldCheckpoint
ConnectedImpliesReplayed == ~s.connected \/ (s.replayDone /\ ~s.catchupError)
ReplayTerminates == []((s.opened /\ Len(s.log) = N /\ ~s.catchupError)
                      ~> (s.replayDone \/ s.catchupError \/ s.aborted))
RecoveryProgress == []((s.resync /\ Len(s.log) = N) ~> s.recovered)
EventuallyQuiescent == <> Quiescent
EventuallySettled == <> (Quiescent \/ s.aborted)
=============================================================================
