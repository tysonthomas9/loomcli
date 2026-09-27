------------------------------- MODULE SSELive -------------------------------
EXTENDS Naturals, Sequences, FiniteSets, TLC

\* A bounded, one-client handoff model. The durable journal has three ordered
\* mutations. Scenario selects one catalogue stimulus; Legacy selects the
\* faulty transition for that stimulus. The fixed transition is the contract,
\* not a claim that every open PR has landed on this branch.
CONSTANTS Scenario, Legacy

VARIABLES step, durable, floor, source, resume, resumeSource, registered,
          fast, retryQ, clientBuf, replayCursor, replayFence, replayDone,
          subscriberEpoch, readEpoch, delivered, frameCursor, frameComplete,
          checkpoint, oldCheckpoint, checkpointSource, applied, connected,
          recovery, viewThrough, snapshotThrough, manifestThrough,
          scope, frameScope

vars == <<step, durable, floor, source, resume, resumeSource, registered,
          fast, retryQ, clientBuf, replayCursor, replayFence, replayDone,
          subscriberEpoch, readEpoch, delivered, frameCursor, frameComplete,
          checkpoint, oldCheckpoint, checkpointSource, applied, connected,
          recovery, viewThrough, snapshotThrough, manifestThrough,
          scope, frameScope>>

Fault(s) == Legacy /\ Scenario = s

Init ==
  /\ step = 0
  /\ durable = <<1, 2>>
  /\ floor = 0
  /\ source = 1
  /\ resume = 0
  /\ resumeSource = 1
  /\ registered = FALSE
  /\ fast = <<>>
  /\ retryQ = <<>>
  /\ clientBuf = <<>>
  /\ replayCursor = 0
  /\ replayFence = 3
  /\ replayDone = FALSE
  /\ subscriberEpoch = 1
  /\ readEpoch = 1
  /\ delivered = <<>>
  /\ frameCursor = 0
  /\ frameComplete = TRUE
  /\ checkpoint = 0
  /\ oldCheckpoint = 0
  /\ checkpointSource = 1
  /\ applied = <<>>
  /\ connected = FALSE
  /\ recovery = "idle"
  /\ viewThrough = 0
  /\ snapshotThrough = 0
  /\ manifestThrough = 0
  /\ scope = 1
  /\ frameScope = 1

\* Publish is a durable append; the third mutation can then race a reconnect.
Publish ==
  /\ step = 0
  /\ step' = 1
  /\ durable' = IF Scenario = "626b" THEN durable ELSE Append(durable, 3)
  /\ UNCHANGED <<floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, connected, recovery, viewThrough,
                  snapshotThrough, manifestThrough, scope, frameScope>>

\* Opening registers before the storage read. The #626b mutation allows the
\* asynchronous hub registration to be overtaken by a broadcast.
Open ==
  /\ step = 1
  /\ step' = 2
  /\ registered' = ~Fault("626b")
  /\ replayFence' = IF Scenario = "626b" THEN 2 ELSE 3
  /\ source' = IF Scenario = "670" THEN 2 ELSE source
  /\ resumeSource' = IF Scenario = "670" /\ ~Legacy THEN 2 ELSE resumeSource
  /\ floor' = IF Scenario = "672" \/ Scenario = "FD1" THEN 2 ELSE floor
  /\ resume' = IF Scenario = "672" \/ Scenario = "FD1" THEN 1 ELSE resume
  /\ oldCheckpoint' = IF Scenario = "644" \/ Scenario = "627b" \/ Scenario = "H2"
                       THEN 3 ELSE oldCheckpoint
  /\ checkpoint' = IF Scenario = "644" \/ Scenario = "627b" \/ Scenario = "H2"
                    THEN 3 ELSE checkpoint
  /\ scope' = scope
  /\ UNCHANGED <<durable, fast, retryQ, clientBuf,
                  replayCursor, replayDone, subscriberEpoch,
                  readEpoch, delivered, frameCursor, frameComplete,
                  checkpointSource, applied, connected, recovery, viewThrough,
                  snapshotThrough, manifestThrough, frameScope>>

\* Replay is fenced at a captured head and follows pages. A subscription
\* epoch belongs to one connection, even if the registry later replaces it.
ReadPages ==
  /\ step = 2
  /\ step' = 3
  /\ registered' = ~Fault("626b")
  /\ replayCursor' = IF Fault("626a") \/ Fault("640a") THEN 1
                     ELSE IF Scenario = "626b" THEN 2 ELSE 3
  /\ replayDone' = ~(Fault("626d") \/ Fault("640b") \/ Fault("655"))
  /\ readEpoch' = IF Fault("656") THEN 2 ELSE readEpoch
  /\ fast' = IF Fault("643") THEN <<2>> ELSE <<1>>
  /\ retryQ' = IF Fault("643") THEN <<1>> ELSE <<2>>
  /\ clientBuf' = IF Scenario = "626b" THEN <<1, 2>>
                   ELSE IF Fault("612a") THEN <<1, 3>> ELSE <<1, 2, 3>>
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, replayFence,
                  subscriberEpoch, delivered, frameCursor, frameComplete,
                  checkpoint, oldCheckpoint, checkpointSource, applied,
                  connected, recovery, viewThrough, snapshotThrough,
                  manifestThrough, scope, frameScope>>

\* The filter rebind is a separate browser-scope transition. A stale writer
\* can still send from the old workspace in the H4 legacy configuration.
Rebind ==
  /\ step = 3
  /\ Scenario # "626b"
  /\ step' = 4
  /\ scope' = IF Scenario = "H4" THEN 2 ELSE scope
  /\ frameScope' = IF Scenario = "H4" /\ ~Legacy THEN 2 ELSE frameScope
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, connected, recovery, viewThrough,
                  snapshotThrough, manifestThrough>>

\* The #626b publication occurs after the catch-up read, while an async
\* registration is still pending. The fixed registration already owns it.
PublishLate ==
  /\ step = 3
  /\ Scenario = "626b"
  /\ step' = 4
  /\ durable' = Append(durable, 3)
  /\ clientBuf' = IF registered THEN Append(clientBuf, 3) ELSE clientBuf
  /\ registered' = TRUE
  /\ UNCHANGED <<floor, source, resume, resumeSource,
                  fast, retryQ, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, connected, recovery, viewThrough,
                  snapshotThrough, manifestThrough, scope, frameScope>>

\* The stream writer may overlap replay. The browser accepts only complete
\* mutation frames; cursorless frames must leave the durable checkpoint alone.
Deliver ==
  /\ step = 4
  /\ step' = 5
  /\ delivered' = Append(fast \o retryQ, 3)
  /\ frameCursor' = 3
  /\ frameComplete' = ~Fault("657")
  /\ checkpoint' = CASE Fault("642") \/ Fault("H1") -> 4
                    [] Fault("644") \/ Fault("H2") -> 2
                    [] Fault("627b") -> 0
                    [] Fault("640a") -> 1
                    [] Scenario = "644" \/ Scenario = "627b" \/ Scenario = "H2" -> 3
                    [] OTHER -> 3
  /\ checkpointSource' = IF Fault("670") THEN 1 ELSE source
  /\ applied' = CASE Scenario = "626a" -> SubSeq(durable, 1, replayCursor)
                [] Scenario = "626b" \/ Scenario = "612a" -> clientBuf
                [] Fault("FD2") -> <<1, 3>>
                [] Fault("626c") -> <<1, 2, 3, 2>>
                [] Fault("610a") -> <<1, 2, 3, 1>>
                [] Fault("657") -> <<1, 2>>
                [] OTHER -> <<1, 2, 3>>
  /\ connected' = IF Scenario = "672" \/ Scenario = "FD1"
                   THEN Legacy ELSE ~Fault("655")
  /\ recovery' = IF Fault("672") \/ Fault("FD1")
                  THEN "idle" ELSE "running"
  /\ frameScope' = IF Fault("H4") THEN 1 ELSE scope
  /\ snapshotThrough' = 3
  /\ manifestThrough' = IF Fault("669") THEN 2 ELSE 3
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, oldCheckpoint,
                  viewThrough, scope>>

\* A lost connection offers a recovery manifest. The browser does not count
\* that offer as a successful read.
Disconnect ==
  /\ step = 5
  /\ step' = 6
  /\ connected' = FALSE
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, recovery, viewThrough,
                  snapshotThrough, manifestThrough, scope, frameScope>>

RecoveryOffer ==
  /\ step = 6
  /\ step' = 7
  /\ recovery' = "running"
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, connected, viewThrough,
                  snapshotThrough, manifestThrough, scope, frameScope>>

\* A recovery offer is acknowledged only after a fresh successful read. An
\* expired or foreign-source resume must recover before connected is reported.
CommitView ==
  /\ step = 7
  /\ step' = 8
  /\ recovery' = IF Fault("V1") \/ Fault("672") \/ Fault("FD1")
                  THEN "idle" ELSE "ok"
  /\ viewThrough' = IF Fault("V1") \/ Fault("645") THEN 1 ELSE 3
  /\ connected' = ~Fault("655")
  /\ UNCHANGED <<durable, floor, source, resume, resumeSource, registered,
                  fast, retryQ, clientBuf, replayCursor, replayFence,
                  replayDone, subscriberEpoch, readEpoch, delivered,
                  frameCursor, frameComplete, checkpoint, oldCheckpoint,
                  checkpointSource, applied, snapshotThrough,
                  manifestThrough, scope, frameScope>>

Next == Publish \/ Open \/ ReadPages \/ Rebind \/ PublishLate \/ Deliver \/
        Disconnect \/ RecoveryOffer \/ CommitView
Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ step \in 0..8
  /\ checkpoint \in 0..4
  /\ replayCursor \in 0..3
  /\ applied \in Seq(0..3)

NoLostEvent == step < 5 \/ {durable[i] : i \in 1..Len(durable)}
                          \subseteq {applied[i] : i \in 1..Len(applied)}
NoDoubleApply == \A i, j \in 1..Len(applied): i # j => applied[i] # applied[j]
AdmissionOrder == step < 5 \/ delivered = durable
CheckpointIsDurable == checkpoint \in 0..3 /\ (checkpoint = 0 \/ checkpointSource = source)
CheckpointMonotonic == checkpoint >= oldCheckpoint
CheckpointFromCompleteFrame == checkpoint <= oldCheckpoint \/ frameComplete
ConnectedImpliesReplayed == ~connected \/ replayDone
ReplayTerminates == step < 5 \/ (replayDone /\ checkpoint >= replayFence)
CursorBoundToSource == resumeSource = source /\ readEpoch = subscriberEpoch
ExpiredCursorForcesRecovery == floor <= resume \/ ~connected \/ recovery = "ok"
SnapshotCursorConsistent == step < 5 \/ snapshotThrough = manifestThrough
RecoverySuccessIsFresh == step < 8 \/ recovery # "ok" \/ viewThrough = 3
FilterRespected == step < 5 \/ frameScope = scope

EventuallyDone == <> (step = 8)
EventuallyConnected == <> connected
ViewConvergesToDurable == <> (viewThrough = 3)
=============================================================================
